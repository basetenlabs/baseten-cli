package cmd_test

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/basetenlabs/baseten-cli/internal/cmd"
)

// modelPushAPICall captures one HTTP request to the fake management API.
type modelPushAPICall struct {
	Method string
	Path   string
	Body   map[string]any
}

// modelPushFakeS3 satisfies transfermanager.S3APIClient via embedding; only
// PutObject is implemented, so any other method call (multipart, etc.) panics.
type modelPushFakeS3 struct {
	transfermanager.S3APIClient
	// mu guards bucket, key, body.
	mu     sync.Mutex
	bucket string
	key    string
	body   []byte
}

func (f *modelPushFakeS3) PutObject(_ context.Context, in *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	body, err := io.ReadAll(in.Body)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.bucket = aws.ToString(in.Bucket)
	f.key = aws.ToString(in.Key)
	f.body = body
	return &s3.PutObjectOutput{}, nil
}

// modelPushHarness wires the CommandHarness with a fake HTTP server (per-route
// canned responses) and a fake S3 client (captured upload body).
type modelPushHarness struct {
	*CommandHarness
	S3 *modelPushFakeS3
	// mu guards Calls and routes.
	mu     sync.Mutex
	Calls  []modelPushAPICall
	routes map[string]func() (int, any) // key = "METHOD PATH"
}

func newModelPushHarness(t *testing.T) *modelPushHarness {
	h := &modelPushHarness{
		S3:     &modelPushFakeS3{},
		routes: map[string]func() (int, any){},
	}
	h.SetRoute("GET", "/v1/models", 200, map[string]any{"models": []any{}})
	h.SetRoute("POST", "/v1/prepare_model_upload", 200, map[string]any{
		"creds": map[string]any{
			"aws_access_key_id":     "AKIA-TEST",
			"aws_secret_access_key": "secret",
			"aws_session_token":     "token",
		},
		"s3_bucket": "baseten-uploads",
		"s3_key":    "uploads/test-key.tar",
		"s3_region": "us-west-2",
	})
	defaultCreated := map[string]any{
		"model": map[string]any{
			"id":                 "model-123",
			"name":               "test-model",
			"created_at":         "2026-01-01T00:00:00Z",
			"deployments_count":  1,
			"instance_type_name": "1x2",
		},
		"deployment": map[string]any{
			"id":             "deploy-456",
			"model_id":       "model-123",
			"name":           "v1",
			"created":        "2026-01-01T00:00:00Z",
			"updated":        "2026-01-01T00:00:00Z",
			"is_development": false,
			"status":         "BUILDING",
		},
	}
	h.SetRoute("POST", "/v1/models", 200, defaultCreated)
	h.SetRoute("POST", "/v1/models/model-123/deployments", 200, defaultCreated)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body := map[string]any{}
		_ = json.Unmarshal(raw, &body)
		h.mu.Lock()
		h.Calls = append(h.Calls, modelPushAPICall{Method: r.Method, Path: r.URL.Path, Body: body})
		handler, ok := h.routes[r.Method+" "+r.URL.Path]
		h.mu.Unlock()
		if !ok {
			http.Error(w, "no route for "+r.Method+" "+r.URL.Path, http.StatusNotFound)
			return
		}
		status, payload := handler()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(payload)
	}))
	t.Cleanup(srv.Close)

	h.CommandHarness = NewCommandHarness(t)
	h.T.Setenv("BASETEN_MANAGEMENT_API_URL_OVERRIDE", srv.URL)
	h.Context = cmd.WithHTTPClient(h.Context, srv.Client())
	h.Context = cmd.WithS3APIClientFactory(h.Context, func(aws.Config) transfermanager.S3APIClient {
		return h.S3
	})
	return h
}

// SetRoute registers (or overrides) the response for a method+path.
func (h *modelPushHarness) SetRoute(method, path string, status int, payload any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.routes[method+" "+path] = func() (int, any) { return status, payload }
}

// FindCall returns the first recorded call matching method+path (or nil).
func (h *modelPushHarness) FindCall(method, path string) *modelPushAPICall {
	h.mu.Lock()
	defer h.mu.Unlock()
	for i := range h.Calls {
		if h.Calls[i].Method == method && h.Calls[i].Path == path {
			return &h.Calls[i]
		}
	}
	return nil
}

// WriteModelDir creates a minimal Truss directory: config.yaml + model.py.
// configYAML is written verbatim.
func (h *modelPushHarness) WriteModelDir(configYAML string) string {
	h.T.Helper()
	dir := h.T.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(configYAML), 0o644); err != nil {
		h.T.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "model.py"), []byte("class Model:\n    pass\n"), 0o644); err != nil {
		h.T.Fatal(err)
	}
	return dir
}

// UntarUploaded reads the captured S3 body as a tar stream and returns a
// map of archive-path -> file contents.
func (h *modelPushHarness) UntarUploaded() map[string]string {
	h.T.Helper()
	tr := tar.NewReader(bytes.NewReader(h.S3.body))
	out := map[string]string{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return out
		}
		if err != nil {
			h.T.Fatalf("tar.Next: %v", err)
		}
		buf, err := io.ReadAll(tr)
		if err != nil {
			h.T.Fatalf("read entry %s: %v", hdr.Name, err)
		}
		out[hdr.Name] = string(buf)
	}
}

const modelPushMinimalConfig = "model_name: test-model\n"

// Dry-run path: prepare is called with dry_run=true, no S3 upload happens,
// no model is created.
func TestModelPush_DryRun(t *testing.T) {
	h := newModelPushHarness(t)
	h.SetRoute("POST", "/v1/prepare_model_upload", 200, map[string]any{
		// dry-run response: creds/bucket/key are null per the REST contract.
		"creds": nil, "s3_bucket": nil, "s3_key": nil,
	})

	dir := h.WriteModelDir(modelPushMinimalConfig)
	h.Require.NoError(h.Execute("model", "push", "--dir", dir, "--dry-run"))

	prep := h.FindCall("POST", "/v1/prepare_model_upload")
	h.Require.NotNil(prep)
	h.Require.Equal(true, prep.Body["dry_run"])
	h.Require.Equal("test-model", prep.Body["name"])

	h.Require.Nil(h.S3.body, "no S3 upload on dry run")
	h.Require.Nil(h.FindCall("POST", "/v1/models"))
	h.Require.Contains(h.Stderr.String(), "Dry run successful")
}

// New-model happy path: routes through POST /v1/models, S3 receives the tar
// archive, deployment payload reflects user-env client version.
func TestModelPush_NewModel(t *testing.T) {
	h := newModelPushHarness(t)
	dir := h.WriteModelDir(modelPushMinimalConfig)

	h.Require.NoError(h.Execute("model", "push", "--dir", dir))

	// Prepare uses Name (new), not ModelId (existing).
	prep := h.FindCall("POST", "/v1/prepare_model_upload")
	h.Require.NotNil(prep)
	h.Require.Equal("test-model", prep.Body["name"])
	h.Require.NotContains(prep.Body, "model_id")
	dep, _ := prep.Body["deployment"].(map[string]any)
	h.Require.NotNil(dep)
	h.Require.NotContains(dep, "user_env")

	// S3 received the uncompressed tar with both source files.
	h.Require.Equal("baseten-uploads", h.S3.bucket)
	h.Require.Equal("uploads/test-key.tar", h.S3.key)
	entries := h.UntarUploaded()
	h.Require.Equal(modelPushMinimalConfig, entries["config.yaml"])
	h.Require.Contains(entries, "model.py")

	// Create-model call references the same s3_key.
	create := h.FindCall("POST", "/v1/models")
	h.Require.NotNil(create)
	src, _ := create.Body["source"].(map[string]any)
	h.Require.NotNil(src)
	h.Require.Equal("uploads/test-key.tar", src["s3_key"])
	h.Require.Equal("test-model", src["name"])

	// No existing-model route hit.
	h.Require.Nil(h.FindCall("POST", "/v1/models/model-123/deployments"))
}

// Existing model: GetModels finds a match, push routes through
// POST /v1/models/{id}/deployments and skips POST /v1/models.
func TestModelPush_ExistingModel(t *testing.T) {
	h := newModelPushHarness(t)
	h.SetRoute("GET", "/v1/models", 200, map[string]any{
		"models": []any{
			map[string]any{
				"id": "model-123", "name": "test-model",
				"created_at":        "2026-01-01T00:00:00Z",
				"deployments_count": 1, "instance_type_name": "1x2",
			},
		},
	})
	dir := h.WriteModelDir(modelPushMinimalConfig)
	h.Require.NoError(h.Execute("model", "push", "--dir", dir))

	prep := h.FindCall("POST", "/v1/prepare_model_upload")
	h.Require.NotNil(prep)
	h.Require.Equal("model-123", prep.Body["model_id"])
	h.Require.NotContains(prep.Body, "name")

	h.Require.NotNil(h.FindCall("POST", "/v1/models/model-123/deployments"))
	h.Require.Nil(h.FindCall("POST", "/v1/models"))
}

// Verifies the asymmetry: --override-name and --no-cache mutate the API
// `config` payload but NOT the archived config.yaml bytes. Also verifies
// external_package_dirs are bundled under the configured directory.
func TestModelPush_OverridesAndExternalPackages(t *testing.T) {
	h := newModelPushHarness(t)
	dir := h.WriteModelDir("model_name: original\nexternal_package_dirs:\n  - extras\n")

	extras := filepath.Join(dir, "extras")
	h.Require.NoError(os.Mkdir(extras, 0o755))
	h.Require.NoError(os.WriteFile(filepath.Join(extras, "util.py"), []byte("X = 1\n"), 0o644))

	h.Require.NoError(h.Execute("model", "push", "--dir", dir,
		"--override-name", "renamed", "--no-cache"))

	prep := h.FindCall("POST", "/v1/prepare_model_upload")
	h.Require.NotNil(prep)
	h.Require.Equal("renamed", prep.Body["name"])
	dep := prep.Body["deployment"].(map[string]any)
	cfg := dep["config"].(map[string]any)
	h.Require.Equal("renamed", cfg["model_name"])
	build := cfg["build"].(map[string]any)
	h.Require.Equal(true, build["no_cache"])
	// raw_config is the on-disk bytes, NOT the mutated map.
	h.Require.Equal("model_name: original\nexternal_package_dirs:\n  - extras\n", dep["raw_config"])

	entries := h.UntarUploaded()
	// Archived config.yaml is verbatim — no override leak.
	h.Require.Equal("model_name: original\nexternal_package_dirs:\n  - extras\n", entries["config.yaml"])
	// External package dir contents bundled under the default "packages/".
	h.Require.Equal("X = 1\n", entries["packages/util.py"])
}

// .truss_ignore at the truss root is parsed as gitignore-style patterns
// and replaces (not merges with) the SDK's bundled defaults, matching the
// Python Truss behavior.
func TestModelPush_TrussIgnore(t *testing.T) {
	h := newModelPushHarness(t)
	dir := h.WriteModelDir(modelPushMinimalConfig)

	// User-supplied .truss_ignore with a mix of patterns.
	trussIgnore := "secret.txt\ndata/*.csv\n*.log\n"
	h.Require.NoError(os.WriteFile(filepath.Join(dir, ".truss_ignore"), []byte(trussIgnore), 0o644))

	// Files that the user's patterns should drop.
	h.Require.NoError(os.WriteFile(filepath.Join(dir, "secret.txt"), []byte("nope"), 0o644))
	h.Require.NoError(os.MkdirAll(filepath.Join(dir, "data"), 0o755))
	h.Require.NoError(os.WriteFile(filepath.Join(dir, "data", "rows.csv"), []byte("nope"), 0o644))
	h.Require.NoError(os.WriteFile(filepath.Join(dir, "app.log"), []byte("nope"), 0o644))
	// Files the user's patterns do NOT mention. With the SDK defaults
	// replaced, these would normally be ignored by defaults but should
	// now ship, matching Python's replace-not-merge semantics.
	h.Require.NoError(os.WriteFile(filepath.Join(dir, "model.pyc"), []byte("kept"), 0o644))
	h.Require.NoError(os.WriteFile(filepath.Join(dir, ".DS_Store"), []byte("kept"), 0o644))
	// Plain file that should always ship.
	h.Require.NoError(os.WriteFile(filepath.Join(dir, "keep.txt"), []byte("kept"), 0o644))

	h.Require.NoError(h.Execute("model", "push", "--dir", dir))

	entries := h.UntarUploaded()
	h.Require.NotContains(entries, "secret.txt")
	h.Require.NotContains(entries, "data/rows.csv")
	h.Require.NotContains(entries, "app.log")
	h.Require.Equal("kept", entries["model.pyc"])
	h.Require.Equal("kept", entries[".DS_Store"])
	h.Require.Equal("kept", entries["keep.txt"])
}

// Without a .truss_ignore present, the SDK's bundled default patterns apply,
// covering both basename (e.g., __pycache__, *.pyc) and path-anchored
// (e.g., docs/_build/, share/python-wheels/) cases.
func TestModelPush_DefaultIgnore(t *testing.T) {
	h := newModelPushHarness(t)
	dir := h.WriteModelDir(modelPushMinimalConfig)

	h.Require.NoError(os.WriteFile(filepath.Join(dir, "model.pyc"), []byte("junk"), 0o644))
	h.Require.NoError(os.MkdirAll(filepath.Join(dir, "__pycache__"), 0o755))
	h.Require.NoError(os.WriteFile(filepath.Join(dir, "__pycache__", "x.pyc"), []byte("junk"), 0o644))
	h.Require.NoError(os.MkdirAll(filepath.Join(dir, "docs", "_build"), 0o755))
	h.Require.NoError(os.WriteFile(filepath.Join(dir, "docs", "_build", "out.html"), []byte("junk"), 0o644))
	h.Require.NoError(os.MkdirAll(filepath.Join(dir, "share", "python-wheels"), 0o755))
	h.Require.NoError(os.WriteFile(filepath.Join(dir, "share", "python-wheels", "x.whl"), []byte("junk"), 0o644))
	// Top-level _build/ must NOT be matched by the docs/_build/ anchored pattern.
	h.Require.NoError(os.MkdirAll(filepath.Join(dir, "_build"), 0o755))
	h.Require.NoError(os.WriteFile(filepath.Join(dir, "_build", "keep.txt"), []byte("kept"), 0o644))

	h.Require.NoError(h.Execute("model", "push", "--dir", dir))

	entries := h.UntarUploaded()
	h.Require.NotContains(entries, "model.pyc")
	h.Require.NotContains(entries, "__pycache__/x.pyc")
	h.Require.NotContains(entries, "docs/_build/out.html")
	h.Require.NotContains(entries, "share/python-wheels/x.whl")
	h.Require.Equal("kept", entries["_build/keep.txt"])
}

// Combined flag-validation coverage. Each subtest is a single Execute, no
// HTTP/S3 traffic expected for the failure cases.
func TestModelPush_Validation(t *testing.T) {
	t.Run("missing_config_file", func(t *testing.T) {
		h := newModelPushHarness(t)
		dir := t.TempDir()
		err := h.Execute("model", "push", "--dir", dir)
		h.Require.ErrorContains(err, "config.yaml not found")
		h.Require.ErrorContains(err, "--dir")
	})

	t.Run("missing_model_name", func(t *testing.T) {
		h := newModelPushHarness(t)
		dir := h.WriteModelDir("build: {}\n")
		err := h.Execute("model", "push", "--dir", dir)
		h.Require.ErrorContains(err, "model_name is required")
	})

	t.Run("promote_and_environment", func(t *testing.T) {
		h := newModelPushHarness(t)
		dir := h.WriteModelDir(modelPushMinimalConfig)
		err := h.Execute("model", "push", "--dir", dir, "--promote", "--environment", "staging")
		h.Require.ErrorContains(err, "mutually exclusive")
	})

	t.Run("labels_invalid_json", func(t *testing.T) {
		h := newModelPushHarness(t)
		dir := h.WriteModelDir(modelPushMinimalConfig)
		err := h.Execute("model", "push", "--dir", dir, "--labels", "not-json")
		h.Require.ErrorContains(err, "--labels")
	})

	t.Run("labels_not_object", func(t *testing.T) {
		h := newModelPushHarness(t)
		dir := h.WriteModelDir(modelPushMinimalConfig)
		err := h.Execute("model", "push", "--dir", dir, "--labels", `[1,2]`)
		h.Require.ErrorContains(err, "JSON object")
	})

	t.Run("deploy_timeout_out_of_range", func(t *testing.T) {
		h := newModelPushHarness(t)
		dir := h.WriteModelDir(modelPushMinimalConfig)
		err := h.Execute("model", "push", "--dir", dir, "--deploy-timeout", "5m")
		h.Require.ErrorContains(err, "--deploy-timeout")
	})

	t.Run("disable_archive_download_on_existing", func(t *testing.T) {
		h := newModelPushHarness(t)
		h.SetRoute("GET", "/v1/models", 200, map[string]any{
			"models": []any{
				map[string]any{
					"id": "model-123", "name": "test-model",
					"created_at":        "2026-01-01T00:00:00Z",
					"deployments_count": 1, "instance_type_name": "1x2",
				},
			},
		})
		dir := h.WriteModelDir(modelPushMinimalConfig)
		err := h.Execute("model", "push", "--dir", dir, "--disable-archive-download")
		h.Require.ErrorContains(err, "--disable-archive-download")
	})

	t.Run("labels_and_timeout_success", func(t *testing.T) {
		h := newModelPushHarness(t)
		dir := h.WriteModelDir(modelPushMinimalConfig)
		h.Require.NoError(h.Execute("model", "push", "--dir", dir,
			"--labels", `{"team":"ml","priority":1}`,
			"--deploy-timeout", "30m",
		))
		prep := h.FindCall("POST", "/v1/prepare_model_upload")
		h.Require.NotNil(prep)
		dep := prep.Body["deployment"].(map[string]any)
		h.Require.Equal(map[string]any{"team": "ml", "priority": float64(1)}, dep["labels"])
		h.Require.Equal(float64(30), dep["deploy_timeout_minutes"])
	})
}
