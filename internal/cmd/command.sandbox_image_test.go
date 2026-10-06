package cmd_test

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/basetenlabs/baseten-go/sandbox"
	"github.com/moby/patternmatcher"
	"github.com/stretchr/testify/require"
)

// sandboxTestImage is a control plane image record.
func sandboxTestImage(status string) map[string]any {
	return map[string]any{"name": "img", "status": status, "tags": []any{}, "tag_count": 2, "size": 2048, "created_at": sandboxTestTime}
}

// sandboxTestPushRoutes serves an image push whose upload goes to the mock and
// whose image then has the given status.
func sandboxTestPushRoutes(m *MockManagementAPI, status string) {
	m.SetRoute("POST", "/v1/sandboxes/images", 202, map[string]any{"name": "img", "status": "UPLOADING", "upload_url": m.URL + "/upload"})
	m.SetRoute("PUT", "/upload", 200, map[string]any{})
	m.SetRoute("GET", "/v1/sandboxes/images/img", 200, sandboxTestImage(status))
}

// sandboxTestZipNames returns the file names in an uploaded zip, sorted.
func sandboxTestZipNames(t *testing.T, archive string) []string {
	t.Helper()
	reader, err := zip.NewReader(bytes.NewReader([]byte(archive)), int64(len(archive)))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, file := range reader.File {
		if !file.FileInfo().IsDir() {
			names = append(names, file.Name)
		}
	}
	slices.Sort(names)
	return names
}

// sandboxTestDir creates a directory of files by forward-slashed path.
func sandboxTestDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for path, content := range files {
		full := filepath.Join(dir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func Test_Sandbox_Image_Push_DirHonorsDockerignore(t *testing.T) {
	h := NewCommandHarness(t)
	m := newSandboxTestAPI(h)
	sandboxTestPushRoutes(m, "UPLOADING")
	dir := sandboxTestDir(t, map[string]string{
		"Dockerfile":         "FROM debian\n",
		".dockerignore":      "# comment\nsecret.txt\nlogs\n!logs/keep.txt\ncache\n",
		"secret.txt":         "x",
		"app/secret.txt":     "nested",
		"logs/a.txt":         "a",
		"logs/keep.txt":      "kept",
		"cache/x":            "x",
		"app/main.py":        "print()",
		".env":               "not ignored: the .dockerignore replaces the defaults",
		"app/node_modules/x": "not ignored either",
	})

	h.Require.NoError(h.Execute("sandbox", "image", "push", "--name", "img", "--dir", dir))
	// Docker's semantics: patterns are anchored at the root, and an exception
	// brings a file back from an ignored directory.
	upload := sandboxTestCalls(m, "PUT", "/upload")[0].Body
	h.Require.Equal([]string{".dockerignore", ".env", "Dockerfile", "app/main.py", "app/node_modules/x", "app/secret.txt", "logs/keep.txt"},
		sandboxTestZipNames(t, upload))
	// An exception elsewhere does not bring back an ignored directory, not
	// even empty.
	reader, err := zip.NewReader(bytes.NewReader([]byte(upload)), int64(len(upload)))
	h.Require.NoError(err)
	for _, file := range reader.File {
		h.Require.NotEqual("cache/", file.Name)
	}
	h.Require.Contains(h.Stdout.String(), "Status:         UPLOADING")
}

func Test_Sandbox_Image_Push_DirDefaults(t *testing.T) {
	h := NewCommandHarness(t)
	m := newSandboxTestAPI(h)
	sandboxTestPushRoutes(m, "UPLOADING")
	dir := sandboxTestDir(t, map[string]string{
		"Dockerfile":         "FROM debian\n",
		".env":               "SECRET=1",
		".git/config":        "x",
		"app/node_modules/x": "x",
		"app/main.py":        "print()",
	})

	h.Require.NoError(h.Execute("sandbox", "image", "push", "--name", "img", "--dir", dir))
	h.Require.Equal([]string{"Dockerfile", "app/main.py"}, sandboxTestZipNames(t, sandboxTestCalls(m, "PUT", "/upload")[0].Body))
}

func Test_Sandbox_Image_Push_DirDefaultsMatchDocumentedDockerignore(t *testing.T) {
	// The defaults are documented, in the push help and the SDK, as this
	// .dockerignore, so Docker's matcher must agree with them.
	matcher, err := patternmatcher.New([]string{
		"**/.blaxel", "**/.env.build", "**/.docker", "**/.git", "**/dist", "**/.venv", "**/venv",
		"**/node_modules", "**/.env", ".env*", "**/.next", "**/__pycache__",
	})
	require.NoError(t, err)
	for _, path := range []string{
		".git", ".git/config", "a/.git", "dist", "src/dist/x.js", "distribution", ".env", "a/.env", ".env.local",
		"a/.env.local", ".envrc", ".env.build", "a/.env.build", "venv", "a/venv/bin/python", "node_modules/x",
		"a/b/__pycache__/c.pyc", ".next", ".docker/config.json", ".blaxel", "main.py", "src/app.go", "Dockerfile",
	} {
		want, err := matcher.MatchesOrParentMatches(path)
		require.NoError(t, err)
		got, err := sandbox.DefaultImageIgnoreFile(t.Context(), sandbox.ImageIgnoreFileOptions{RelPath: path})
		require.NoError(t, err)
		require.Equal(t, want, got, path)
	}
}

func Test_Sandbox_Image_Push_RegistryWithDockerConfig(t *testing.T) {
	h := NewCommandHarness(t)
	m := newSandboxTestAPI(h)
	sandboxTestPushRoutes(m, "UPLOADING")
	config := filepath.Join(t.TempDir(), "config.json")
	h.Require.NoError(os.WriteFile(config, []byte(`{"auths":{}}`), 0o600))

	h.Require.NoError(h.Execute("sandbox", "image", "push", "--name", "img", "--registry-image", "registry.example.com/app:v1", "--docker-config", config))
	h.Require.Equal(map[string]any{"name": "img", "image": "registry.example.com/app:v1", "docker_config": `{"auths":{}}`},
		sandboxTestCalls(m, "POST", "/v1/sandboxes/images")[0].BodyJSON(t))
	h.Require.Empty(sandboxTestCalls(m, "PUT", "/upload"))

	h.Require.ErrorContains(h.Execute("sandbox", "image", "push", "--name", "img", "--dir", ".", "--docker-config", config), "--registry-image")
}

func Test_Sandbox_Image_Push_WaitFailedNamesLogs(t *testing.T) {
	h := NewCommandHarness(t)
	m := newSandboxTestAPI(h)
	sandboxTestPushRoutes(m, "FAILED")

	err := h.Execute("sandbox", "image", "push", "--name", "img", "--registry-image", "registry.example.com/app:v1", "--wait")
	h.Require.ErrorContains(err, "baseten sandbox image logs --name img")
}

func Test_Sandbox_Image_Push_WaitBuilt(t *testing.T) {
	h := NewCommandHarness(t)
	m := newSandboxTestAPI(h)
	sandboxTestPushRoutes(m, "BUILT")

	h.Require.NoError(h.Execute("sandbox", "image", "push", "--name", "img", "--registry-image", "registry.example.com/app:v1", "--wait", "-o", "json"))
	var image map[string]any
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &image))
	h.Require.Equal("BUILT", image["status"])
}

func Test_Sandbox_Image_List_Limit(t *testing.T) {
	h := NewCommandHarness(t)
	m := newSandboxTestAPI(h)
	m.SetRoute("GET", "/v1/sandboxes/images", 200, sandboxTestPage("c1", sandboxTestImage("BUILT")))

	h.Require.NoError(h.Execute("sandbox", "image", "list", "--limit", "1"))
	h.Require.Contains(h.Stdout.String(), "2.0 KiB")
	h.Require.Contains(h.Stderr.String(), "Reached the --limit of 1")
}

func Test_Sandbox_Image_Delete_ConfirmsAndLogsToStderr(t *testing.T) {
	h := NewCommandHarness(t)
	m := newSandboxTestAPI(h)
	m.SetRoute("DELETE", "/v1/sandboxes/images/img", 200, sandboxTestImage("BUILT"))

	h.Require.ErrorContains(h.Execute("sandbox", "image", "delete", "--name", "img"), "--yes")
	h.Require.NoError(h.Execute("sandbox", "image", "delete", "--name", "img", "--yes"))
	h.Require.Empty(h.Stdout.String())
	h.Require.Contains(h.Stderr.String(), "Deleting sandbox image img.")
}

func Test_Sandbox_Image_Logs_OldestFirst(t *testing.T) {
	h := NewCommandHarness(t)
	m := newSandboxTestAPI(h)
	// The endpoint returns newest first.
	m.SetRoute("GET", "/v1/sandboxes/images/img/logs", 200, map[string]any{"total_count": 2, "logs": []any{
		map[string]any{"timestamp": sandboxTestTime.Add(1e9), "severity": 9, "message": "second"},
		map[string]any{"timestamp": sandboxTestTime, "severity": 9, "message": "first"},
	}})

	h.Require.NoError(h.Execute("sandbox", "image", "logs", "--name", "img"))
	h.Require.Equal("2026-09-30T22:00:00Z first\n2026-09-30T22:00:01Z second\n", h.Stdout.String())

	h.Require.NoError(h.Execute("sandbox", "image", "logs", "--name", "img", "--jq", ".message"))
	h.Require.Equal("\"first\"\n\"second\"\n", h.Stdout.String())
}

func Test_Sandbox_Image_ListTags(t *testing.T) {
	h := NewCommandHarness(t)
	m := newSandboxTestAPI(h)
	m.SetRoute("GET", "/v1/sandboxes/images/img/tags", 200, sandboxTestPage("", map[string]any{"name": "abc123", "size": 1024, "created_at": sandboxTestTime}))

	h.Require.NoError(h.Execute("sandbox", "image", "list-tags", "--name", "img"))
	h.Require.Contains(h.Stdout.String(), "abc123")
}

func Test_Sandbox_Image_ListLibrary(t *testing.T) {
	h := NewCommandHarness(t)
	m := newSandboxTestAPI(h)
	m.SetRoute("GET", "/v1/sandboxes/library_images", 200, map[string]any{"items": []any{
		map[string]any{"name": "base-image", "display_name": "Base", "image": "baseten/base-image:latest", "memory": 4096, "categories": []string{"general"}},
	}})

	h.Require.NoError(h.Execute("sandbox", "image", "list-library"))
	out := h.Stdout.String()
	h.Require.Contains(out, "Base")
	h.Require.Contains(out, "baseten/base-image:latest")
	h.Require.Contains(out, "4096 MB")
}
