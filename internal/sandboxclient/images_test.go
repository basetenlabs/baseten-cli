package sandboxclient_test

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/basetenlabs/baseten-cli/internal/sandboxclient"
)

func TestImagePushZipsDirectoryUploadsAndWaits(t *testing.T) {
	sourceDir := t.TempDir()
	writeTestFile(t, filepath.Join(sourceDir, "Dockerfile"), "FROM scratch\n")
	writeTestFile(t, filepath.Join(sourceDir, "app", "main.py"), "print('hi')\n")

	var uploadedZip []byte
	var uploadContentType string
	storageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uploadedZip, _ = io.ReadAll(r.Body)
		uploadContentType = r.Header.Get("Content-Type")
		w.WriteHeader(200)
	}))
	t.Cleanup(storageServer.Close)

	// The push answers with the storage URL; the wait sees processing then
	// BUILT, so the require-progress guard is exercised.
	imageCalls := 0
	controlServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/v1/token":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"token":      "tok-1",
				"expires_at": "2026-09-30T23:59:00Z",
			})
		case r.URL.Path == "/v1/sandboxes/images" && r.Method == "POST":
			var request map[string]any
			_ = json.NewDecoder(r.Body).Decode(&request)
			if request["image"] != nil {
				t.Error("a directory push must not send a registry image")
			}
			w.WriteHeader(202)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"name": "my-image", "status": "UPLOADING",
				"upload_url": storageServer.URL + "/upload",
			})
		case r.URL.Path == "/v1/sandboxes/images/my-image" && r.Method == "GET":
			imageCalls++
			status := "UPLOADING"
			if imageCalls > 1 {
				status = "BUILT"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"name": "my-image", "status": status, "tag_count": 1,
			})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(controlServer.Close)

	client, err := sandboxclient.NewSandboxesClient(sandboxclient.SandboxesClientOptions{
		APIKey:            "test-key",
		ManagementBaseURL: controlServer.URL,
	})
	if err != nil {
		t.Fatal(err)
	}

	info, err := client.Images().Push(context.Background(), &sandboxclient.ImagePushOptions{
		Name:         "my-image",
		Directory:    sourceDir,
		WaitForBuilt: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if info.Status != sandboxclient.ImageStatusBuilt {
		t.Errorf("status %q", info.Status)
	}
	if uploadContentType != "application/zip" {
		t.Errorf("upload content type %q", uploadContentType)
	}
	reader, err := zip.NewReader(strings.NewReader(string(uploadedZip)), int64(len(uploadedZip)))
	if err != nil {
		t.Fatalf("the uploaded archive is not a valid zip: %v", err)
	}
	names := map[string]bool{}
	for _, file := range reader.File {
		names[file.Name] = true
	}
	if !names["Dockerfile"] || !names["app/main.py"] {
		t.Errorf("archive entries %v", names)
	}
}

func TestImagePushRequiresExactlyOneSource(t *testing.T) {
	client, err := sandboxclient.NewSandboxesClient(sandboxclient.SandboxesClientOptions{APIKey: "test-key"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Images().Push(context.Background(), &sandboxclient.ImagePushOptions{Name: "x"})
	if err == nil || !strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("want a one-source error, got %v", err)
	}
}

func TestImagePushWithoutDockerfileFails(t *testing.T) {
	sourceDir := t.TempDir()
	writeTestFile(t, filepath.Join(sourceDir, "main.py"), "print('hi')\n")
	client, err := sandboxclient.NewSandboxesClient(sandboxclient.SandboxesClientOptions{APIKey: "test-key"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Images().Push(context.Background(), &sandboxclient.ImagePushOptions{
		Name: "x", Directory: sourceDir,
	})
	if err == nil || !strings.Contains(err.Error(), "Dockerfile") {
		t.Fatalf("want a Dockerfile error, got %v", err)
	}
}

func TestImageWaitBuiltRidesOutMissingImage(t *testing.T) {
	// 404 (not visible yet), then BUILDING (processing observed), then BUILT.
	imageCalls := 0
	controlServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/v1/token":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"token": "tok-1", "expires_at": "2026-09-30T23:59:00Z",
			})
		case r.URL.Path == "/v1/sandboxes/images/my-image":
			imageCalls++
			switch imageCalls {
			case 1:
				w.WriteHeader(404)
				_, _ = io.WriteString(w, `{"error": "not found"}`)
			case 2:
				_ = json.NewEncoder(w).Encode(map[string]any{
					"name": "my-image", "status": "BUILDING",
				})
			default:
				_ = json.NewEncoder(w).Encode(map[string]any{
					"name": "my-image", "status": "BUILT",
				})
			}
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(controlServer.Close)

	client, err := sandboxclient.NewSandboxesClient(sandboxclient.SandboxesClientOptions{
		APIKey:            "test-key",
		ManagementBaseURL: controlServer.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	info, err := client.Images().WaitBuilt(context.Background(), &sandboxclient.ImageWaitOptions{
		Name: "my-image", PollIntervalSeconds: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if info.Status != sandboxclient.ImageStatusBuilt || imageCalls != 3 {
		t.Errorf("status %q after %d checks", info.Status, imageCalls)
	}
}

func TestImagePushSeedsProgressFromAcceptedStatus(t *testing.T) {
	// The 202 answers UPLOADING, so the wait has seen this push processing
	// before its first poll; a BUILT that is already visible on the first
	// poll is therefore terminal at once, not stale-looking.
	imageCalls := 0
	controlServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/v1/token":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"token": "tok-1", "expires_at": "2026-09-30T23:59:00Z",
			})
		case r.URL.Path == "/v1/sandboxes/images" && r.Method == "POST":
			w.WriteHeader(202)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"name": "my-image", "status": "UPLOADING",
			})
		case r.URL.Path == "/v1/sandboxes/images/my-image":
			imageCalls++
			_ = json.NewEncoder(w).Encode(map[string]any{
				"name": "my-image", "status": "BUILT",
			})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(controlServer.Close)

	client, err := sandboxclient.NewSandboxesClient(sandboxclient.SandboxesClientOptions{
		APIKey:            "test-key",
		ManagementBaseURL: controlServer.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	info, err := client.Images().Push(context.Background(), &sandboxclient.ImagePushOptions{
		Name: "my-image", RegistryImage: "registry.example/my-image:v1", WaitForBuilt: true,
		TimeoutSeconds: 2, PollIntervalSeconds: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if info.Status != sandboxclient.ImageStatusBuilt || imageCalls != 1 {
		t.Errorf("seeded wait must accept the first BUILT; status %q after %d checks",
			info.Status, imageCalls)
	}
}

func TestImagePushFastFailureIsTerminal(t *testing.T) {
	// The build fails before the first poll; the seeded progress makes that
	// FAILED terminal at once, and the error carries the fetched build logs.
	imageCalls := 0
	controlServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/v1/token":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"token": "tok-1", "expires_at": "2026-09-30T23:59:00Z",
			})
		case r.URL.Path == "/v1/sandboxes/images" && r.Method == "POST":
			w.WriteHeader(202)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"name": "my-image", "status": "UPLOADING",
			})
		case r.URL.Path == "/v1/sandboxes/images/my-image/logs":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"logs": []any{map[string]any{
					"message": "Step 1/2: failed to pull base image", "severity": 9,
					"timestamp": "2026-09-30T12:00:00Z",
				}},
				"total_count": 1,
			})
		case r.URL.Path == "/v1/sandboxes/images/my-image":
			imageCalls++
			_ = json.NewEncoder(w).Encode(map[string]any{
				"name": "my-image", "status": "FAILED",
			})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(controlServer.Close)

	client, err := sandboxclient.NewSandboxesClient(sandboxclient.SandboxesClientOptions{
		APIKey:            "test-key",
		ManagementBaseURL: controlServer.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Images().Push(context.Background(), &sandboxclient.ImagePushOptions{
		Name: "my-image", RegistryImage: "registry.example/my-image:v1", WaitForBuilt: true,
		TimeoutSeconds: 2, PollIntervalSeconds: 1,
	})
	var buildError *sandboxclient.ImageBuildError
	if !errors.As(err, &buildError) || buildError.TimedOut {
		t.Fatalf("want a failed ImageBuildError, got %v", err)
	}
	if imageCalls != 1 {
		t.Errorf("seeded wait must fail on the first poll; %d checks", imageCalls)
	}
	if !strings.Contains(err.Error(), "failed to pull base image") {
		t.Errorf("error must carry the build log reason: %v", err)
	}
}

func TestImageListPaginates(t *testing.T) {
	controlServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/v1/token":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"token": "tok-1", "expires_at": "2026-09-30T23:59:00Z",
			})
		case r.URL.Path == "/v1/sandboxes/images":
			if r.URL.Query().Get("cursor") == "" {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"items":      []any{map[string]any{"name": "img-1", "status": "BUILT"}},
					"pagination": map[string]any{"has_more": true, "cursor": "c2"},
				})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"items":      []any{map[string]any{"name": "img-2", "status": "BUILT"}},
				"pagination": map[string]any{"has_more": false, "cursor": nil},
			})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(controlServer.Close)

	client, err := sandboxclient.NewSandboxesClient(sandboxclient.SandboxesClientOptions{
		APIKey:            "test-key",
		ManagementBaseURL: controlServer.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for info, err := range client.Images().List(context.Background(), &sandboxclient.ImageListOptions{}) {
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, info.Name)
	}
	if len(names) != 2 || names[0] != "img-1" || names[1] != "img-2" {
		t.Errorf("pagination incomplete: %v", names)
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestImageSourceSymlinks(t *testing.T) {
	sourceDir := t.TempDir()
	writeTestFile(t, filepath.Join(sourceDir, "Dockerfile"), "FROM scratch\n")
	writeTestFile(t, filepath.Join(sourceDir, "secret.txt"), "token\n")
	if err := os.Symlink("secret.txt", filepath.Join(sourceDir, "in-tree-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/hostname", filepath.Join(sourceDir, "absolute-link")); err != nil {
		t.Fatal(err)
	}
	// From nested/, "../../secrets" reaches past the source root: the
	// lexical check must reject it before anything is uploaded.
	if err := os.MkdirAll(filepath.Join(sourceDir, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../../secrets", filepath.Join(sourceDir, "nested", "escaping-link")); err != nil {
		t.Fatal(err)
	}

	client, err := sandboxclient.NewSandboxesClient(sandboxclient.SandboxesClientOptions{APIKey: "test-key"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Images().Push(context.Background(), &sandboxclient.ImagePushOptions{
		Name: "x", Directory: sourceDir,
	})
	if err == nil || !strings.Contains(err.Error(), "absolute target") {
		t.Fatalf("want an absolute-target rejection, got %v", err)
	}

	// Without the absolute link, the escaping one is rejected instead.
	if err := os.Remove(filepath.Join(sourceDir, "absolute-link")); err != nil {
		t.Fatal(err)
	}
	_, err = client.Images().Push(context.Background(), &sandboxclient.ImagePushOptions{
		Name: "x", Directory: sourceDir,
	})
	if err == nil || !strings.Contains(err.Error(), "escapes the source directory") {
		t.Fatalf("want an escaping-target rejection, got %v", err)
	}

}
