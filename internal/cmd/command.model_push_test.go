package cmd_test

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/s3/transfermanager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/basetenlabs/baseten-cli/internal/cmd"
)

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

// modelPushHarness wires the CommandHarness with the shared MockManagementAPI
// and a fake S3 client (captured upload body).
type modelPushHarness struct {
	*CommandHarness
	API *MockManagementAPI
	S3  *modelPushFakeS3
}

func newModelPushHarness(t *testing.T) *modelPushHarness {
	h := &modelPushHarness{
		CommandHarness: NewCommandHarness(t),
		S3:             &modelPushFakeS3{},
	}
	h.API = h.MockManagementAPI()
	h.API.SetRoute("GET", "/v1/models", 200, map[string]any{"models": []any{}})
	h.API.SetRoute("POST", "/v1/prepare_model_upload", 200, map[string]any{
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
	h.API.SetRoute("POST", "/v1/models", 200, defaultCreated)
	h.API.SetRoute("POST", "/v1/models/model-123/deployments", 200, defaultCreated)

	h.Context = cmd.WithS3APIClientFactory(h.Context, func(aws.Config) transfermanager.S3APIClient {
		return h.S3
	})
	return h
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
func Test_Model_Push_DryRun(t *testing.T) {
	h := newModelPushHarness(t)
	h.API.SetRoute("POST", "/v1/prepare_model_upload", 200, map[string]any{
		// dry-run response: creds/bucket/key are null per the REST contract.
		"creds": nil, "s3_bucket": nil, "s3_key": nil,
	})

	dir := h.WriteModelDir(modelPushMinimalConfig)
	h.Require.NoError(h.Execute("model", "push", "--dir", dir, "--dry-run"))

	prep := h.API.FindCall("POST", "/v1/prepare_model_upload")
	h.Require.NotNil(prep)
	body := prep.BodyJSON(h.T)
	h.Require.Equal(true, body["dry_run"])
	h.Require.Equal("test-model", body["name"])

	h.Require.Nil(h.S3.body, "no S3 upload on dry run")
	h.Require.Nil(h.API.FindCall("POST", "/v1/models"))
	h.Require.Contains(h.Stderr.String(), "Dry run successful")
}

// New-model happy path: routes through POST /v1/models, S3 receives the tar
// archive, deployment payload reflects user-env client version.
func Test_Model_Push_NewModel(t *testing.T) {
	h := newModelPushHarness(t)
	dir := h.WriteModelDir(modelPushMinimalConfig)

	h.Require.NoError(h.Execute("model", "push", "--dir", dir))

	// Prepare uses Name (new), not ModelId (existing).
	prep := h.API.FindCall("POST", "/v1/prepare_model_upload")
	h.Require.NotNil(prep)
	prepBody := prep.BodyJSON(h.T)
	h.Require.Equal("test-model", prepBody["name"])
	h.Require.NotContains(prepBody, "model_id")
	dep, _ := prepBody["deployment"].(map[string]any)
	h.Require.NotNil(dep)
	h.Require.NotContains(dep, "user_env")

	// S3 received the uncompressed tar with both source files.
	h.Require.Equal("baseten-uploads", h.S3.bucket)
	h.Require.Equal("uploads/test-key.tar", h.S3.key)
	entries := h.UntarUploaded()
	h.Require.Equal(modelPushMinimalConfig, entries["config.yaml"])
	h.Require.Contains(entries, "model.py")

	// Create-model call references the same s3_key.
	create := h.API.FindCall("POST", "/v1/models")
	h.Require.NotNil(create)
	src, _ := create.BodyJSON(h.T)["source"].(map[string]any)
	h.Require.NotNil(src)
	h.Require.Equal("uploads/test-key.tar", src["s3_key"])
	h.Require.Equal("test-model", src["name"])

	// No existing-model route hit.
	h.Require.Nil(h.API.FindCall("POST", "/v1/models/model-123/deployments"))
}

// Existing model: GetModels finds a match, push routes through
// POST /v1/models/{id}/deployments and skips POST /v1/models.
func Test_Model_Push_ExistingModel(t *testing.T) {
	h := newModelPushHarness(t)
	h.API.SetRoute("GET", "/v1/models", 200, map[string]any{
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

	prep := h.API.FindCall("POST", "/v1/prepare_model_upload")
	h.Require.NotNil(prep)
	body := prep.BodyJSON(h.T)
	h.Require.Equal("model-123", body["model_id"])
	h.Require.NotContains(body, "name")

	h.Require.NotNil(h.API.FindCall("POST", "/v1/models/model-123/deployments"))
	h.Require.Nil(h.API.FindCall("POST", "/v1/models"))
}

// --team <id>: resolves through GET /v1/teams, sets prepareReq.team_id, and
// routes create through POST /v1/teams/{team_id}/models.
func Test_Model_Push_TeamByID(t *testing.T) {
	h := newModelPushHarness(t)
	h.API.SetRoute("GET", "/v1/teams", 200, map[string]any{
		"teams": []any{
			map[string]any{"id": "team-abc", "name": "ml"},
			map[string]any{"id": "team-xyz", "name": "infra"},
		},
	})
	h.API.SetRoute("GET", "/v1/teams/team-abc/models", 200, map[string]any{"models": []any{}})
	h.API.SetRoute("POST", "/v1/teams/team-abc/models", 200, map[string]any{
		"model": map[string]any{
			"id": "model-123", "name": "test-model",
			"created_at":        "2026-01-01T00:00:00Z",
			"deployments_count": 1, "instance_type_name": "1x2",
		},
		"deployment": map[string]any{
			"id": "deploy-456", "model_id": "model-123", "name": "v1",
			"created": "2026-01-01T00:00:00Z", "updated": "2026-01-01T00:00:00Z",
			"is_development": false, "status": "BUILDING",
		},
	})

	dir := h.WriteModelDir(modelPushMinimalConfig)
	h.Require.NoError(h.Execute("model", "push", "--dir", dir, "--team", "team-abc"))

	prep := h.API.FindCall("POST", "/v1/prepare_model_upload")
	h.Require.NotNil(prep)
	h.Require.Equal("team-abc", prep.BodyJSON(h.T)["team_id"])

	h.Require.NotNil(h.API.FindCall("POST", "/v1/teams/team-abc/models"))
	h.Require.Nil(h.API.FindCall("POST", "/v1/models"))
}

// --team <name>: ResolveTeam matches by Name, routing uses the resolved ID.
func Test_Model_Push_TeamByName(t *testing.T) {
	h := newModelPushHarness(t)
	h.API.SetRoute("GET", "/v1/teams", 200, map[string]any{
		"teams": []any{
			map[string]any{"id": "team-abc", "name": "ml"},
			map[string]any{"id": "team-xyz", "name": "infra"},
		},
	})
	h.API.SetRoute("GET", "/v1/teams/team-abc/models", 200, map[string]any{"models": []any{}})
	h.API.SetRoute("POST", "/v1/teams/team-abc/models", 200, map[string]any{
		"model": map[string]any{
			"id": "model-123", "name": "test-model",
			"created_at":        "2026-01-01T00:00:00Z",
			"deployments_count": 1, "instance_type_name": "1x2",
		},
		"deployment": map[string]any{
			"id": "deploy-456", "model_id": "model-123", "name": "v1",
			"created": "2026-01-01T00:00:00Z", "updated": "2026-01-01T00:00:00Z",
			"is_development": false, "status": "BUILDING",
		},
	})

	dir := h.WriteModelDir(modelPushMinimalConfig)
	h.Require.NoError(h.Execute("model", "push", "--dir", dir, "--team", "ml"))

	prep := h.API.FindCall("POST", "/v1/prepare_model_upload")
	h.Require.NotNil(prep)
	h.Require.Equal("team-abc", prep.BodyJSON(h.T)["team_id"])

	h.Require.NotNil(h.API.FindCall("POST", "/v1/teams/team-abc/models"))
	h.Require.Nil(h.API.FindCall("POST", "/v1/models"))
}

// Verifies the asymmetry: --override-name and --no-build-cache mutate the API
// `config` payload but NOT the archived config.yaml bytes. Also verifies
// external_package_dirs are bundled under the configured directory.
func Test_Model_Push_OverridesAndExternalPackages(t *testing.T) {
	h := newModelPushHarness(t)
	dir := h.WriteModelDir("model_name: original\nexternal_package_dirs:\n  - extras\n")

	extras := filepath.Join(dir, "extras")
	h.Require.NoError(os.Mkdir(extras, 0o755))
	h.Require.NoError(os.WriteFile(filepath.Join(extras, "util.py"), []byte("X = 1\n"), 0o644))

	h.Require.NoError(h.Execute("model", "push", "--dir", dir,
		"--override-name", "renamed", "--no-build-cache"))

	prep := h.API.FindCall("POST", "/v1/prepare_model_upload")
	h.Require.NotNil(prep)
	body := prep.BodyJSON(h.T)
	h.Require.Equal("renamed", body["name"])
	dep := body["deployment"].(map[string]any)
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
func Test_Model_Push_TrussIgnore(t *testing.T) {
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
func Test_Model_Push_DefaultIgnore(t *testing.T) {
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
func Test_Model_Push_Validation(t *testing.T) {
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
		h.API.SetRoute("GET", "/v1/models", 200, map[string]any{
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
		prep := h.API.FindCall("POST", "/v1/prepare_model_upload")
		h.Require.NotNil(prep)
		dep := prep.BodyJSON(h.T)["deployment"].(map[string]any)
		h.Require.Equal(map[string]any{"team": "ml", "priority": float64(1)}, dep["labels"])
		h.Require.Equal(float64(30), dep["deploy_timeout_minutes"])
	})
}

// deploymentResponse builds a JSON payload matching managementapi.Deployment
// for the test model+deployment IDs the harness creates by default.
func deploymentResponse(status string) map[string]any {
	return map[string]any{
		"id":                   "deploy-456",
		"model_id":             "model-123",
		"name":                 "v1",
		"status":               status,
		"created_at":           "2026-01-01T00:00:00Z",
		"active_replica_count": 0,
		"autoscaling_settings": map[string]any{},
		"is_development":       false,
		"is_production":        false,
	}
}

// stubModelPushTimeAndSleep pins ctx.Now and makes ctx.Sleep an instant
// no-op so polling loops finish in the test goroutine.
func stubModelPushTimeAndSleep(h *modelPushHarness) {
	h.Context = cmd.WithSleep(h.Context, func(_ context.Context, _ time.Duration) error { return nil })
	fixed := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	h.Context = cmd.WithNow(h.Context, func() time.Time { return fixed })
}

// --wait polls deployment status, logs transitions to stderr, finishes
// cleanly when status reaches ACTIVE, and the JSON result reflects the
// final status.
func Test_Model_Push_WaitSuccess(t *testing.T) {
	h := newModelPushHarness(t)
	stubModelPushTimeAndSleep(h)
	statuses := []string{"BUILDING", "BUILDING", "ACTIVE"}
	idx := 0
	h.API.SetRouteFunc("GET", "/v1/models/model-123/deployments/deploy-456", func(w http.ResponseWriter, _ *http.Request) {
		s := statuses[idx]
		if idx < len(statuses)-1 {
			idx++
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(deploymentResponse(s))
	})

	dir := h.WriteModelDir(modelPushMinimalConfig)
	h.Require.NoError(h.Execute("model", "push", "--dir", dir, "--wait", "--output", "json"))

	h.Require.Contains(h.Stderr.String(), "Status: BUILDING")
	h.Require.Contains(h.Stderr.String(), "Status: ACTIVE")

	var result map[string]any
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &result))
	dep, _ := result["deployment"].(map[string]any)
	h.Require.Equal("ACTIVE", dep["status"])
}

// --wait exits non-zero on a terminal-failure status, and the JSON result
// still emits with the failure status in deployment.status.
func Test_Model_Push_WaitFailure(t *testing.T) {
	h := newModelPushHarness(t)
	stubModelPushTimeAndSleep(h)
	statuses := []string{"BUILDING", "BUILD_FAILED"}
	idx := 0
	h.API.SetRouteFunc("GET", "/v1/models/model-123/deployments/deploy-456", func(w http.ResponseWriter, _ *http.Request) {
		s := statuses[idx]
		if idx < len(statuses)-1 {
			idx++
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(deploymentResponse(s))
	})

	dir := h.WriteModelDir(modelPushMinimalConfig)
	err := h.Execute("model", "push", "--dir", dir, "--wait", "--output", "json")
	h.Require.Error(err)
	h.Require.Contains(err.Error(), "failed deployment status: BUILD_FAILED")
	h.Require.NotZero(h.ExitCode)

	var result map[string]any
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &result))
	dep, _ := result["deployment"].(map[string]any)
	h.Require.Equal("BUILD_FAILED", dep["status"])
}

// --tail without --wait stops only on terminal-failure statuses; logs go
// to stderr as text regardless of --output, and the JSON result reflects
// the final fetched status.
func Test_Model_Push_TailFailure(t *testing.T) {
	h := newModelPushHarness(t)
	stubModelPushTimeAndSleep(h)
	h.API.SetRoute("POST", "/v1/models/model-123/deployments/deploy-456/logs", 200, map[string]any{
		"logs": []any{
			map[string]any{"timestamp": "1", "message": "build started", "replica": nil},
		},
	})
	h.API.SetRoute("GET", "/v1/models/model-123/deployments/deploy-456", 200, deploymentResponse("BUILD_FAILED"))

	dir := h.WriteModelDir(modelPushMinimalConfig)
	err := h.Execute("model", "push", "--dir", dir, "--tail", "--output", "json")
	h.Require.Error(err)
	h.Require.Contains(err.Error(), "failed deployment status: BUILD_FAILED")
	h.Require.Contains(h.Stderr.String(), "build started")

	// JSON result is on stdout regardless of --tail.
	var result map[string]any
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &result))
	dep, _ := result["deployment"].(map[string]any)
	h.Require.Equal("BUILD_FAILED", dep["status"])
}

// --tail --wait stops on ACTIVE (success), exits zero, and the JSON
// result reflects the final ACTIVE status.
func Test_Model_Push_TailWaitSuccess(t *testing.T) {
	h := newModelPushHarness(t)
	stubModelPushTimeAndSleep(h)
	h.API.SetRoute("POST", "/v1/models/model-123/deployments/deploy-456/logs", 200, map[string]any{
		"logs": []any{
			map[string]any{"timestamp": "1", "message": "almost ready", "replica": nil},
		},
	})
	h.API.SetRoute("GET", "/v1/models/model-123/deployments/deploy-456", 200, deploymentResponse("ACTIVE"))

	dir := h.WriteModelDir(modelPushMinimalConfig)
	h.Require.NoError(h.Execute("model", "push", "--dir", dir, "--tail", "--wait", "--output", "json"))
	h.Require.Contains(h.Stderr.String(), "almost ready")

	var result map[string]any
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &result))
	dep, _ := result["deployment"].(map[string]any)
	h.Require.Equal("ACTIVE", dep["status"])
}

// --tail tolerates 404s from the logs API during the warmup window
// (brand-new deployments may 404 for a few seconds after creation), then
// proceeds normally once the first response succeeds.
func Test_Model_Push_TailWarmup404(t *testing.T) {
	h := newModelPushHarness(t)
	stubModelPushTimeAndSleep(h)

	logsCall := 0
	h.API.SetRouteFunc("POST", "/v1/models/model-123/deployments/deploy-456/logs", func(w http.ResponseWriter, _ *http.Request) {
		logsCall++
		w.Header().Set("Content-Type", "application/json")
		if logsCall <= 2 {
			w.WriteHeader(404)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "not found"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"logs": []any{
				map[string]any{"timestamp": "1", "message": "online", "replica": nil},
			},
		})
	})
	h.API.SetRoute("GET", "/v1/models/model-123/deployments/deploy-456", 200, deploymentResponse("BUILD_FAILED"))

	dir := h.WriteModelDir(modelPushMinimalConfig)
	err := h.Execute("model", "push", "--dir", dir, "--tail")
	h.Require.Error(err)
	h.Require.Contains(err.Error(), "failed deployment status: BUILD_FAILED")
	h.Require.Contains(h.Stderr.String(), "online")
	h.Require.GreaterOrEqual(logsCall, 3, "two 404s plus one success")
}

// addPushWatchRoutes points the inference base at the mock and registers the
// patch-loop routes against the deployment the push harness creates.
func addPushWatchRoutes(h *modelPushHarness) {
	h.T.Setenv("BASETEN_INFERENCE_BASE_URL_OVERRIDE", h.API.URL)
	h.API.SetRoute("GET", watchDepPath, 200, deploymentResponse("ACTIVE"))
	h.API.SetRoute("GET", watchStatePath, 200, map[string]any{
		"running_patch_point": map[string]any{
			"config": "", "content_hashes": map[string]any{}, "hash": "h0",
		},
	})
	h.API.SetRoute("POST", watchStagePath, 200, map[string]any{
		"patch_point": map[string]any{
			"config": "", "content_hashes": map[string]any{}, "hash": "h1",
		},
	})
	h.API.SetRoute("POST", watchSyncPath, 200, map[string]any{})
}

// --develop (without --watch) sets is_development on the deployment payload and
// otherwise pushes normally.
func Test_Model_Push_Develop_SetsIsDevelopment(t *testing.T) {
	h := newModelPushHarness(t)
	dir := h.WriteModelDir(modelPushMinimalConfig)
	h.Require.NoError(h.Execute("model", "push", "--dir", dir, "--develop"))

	prep := h.API.FindCall("POST", "/v1/prepare_model_upload")
	h.Require.NotNil(prep)
	dep := prep.BodyJSON(h.T)["deployment"].(map[string]any)
	h.Require.Equal(true, dep["is_development"])
}

func Test_Model_Push_WatchValidation(t *testing.T) {
	t.Run("watch_and_promote", func(t *testing.T) {
		h := newModelPushHarness(t)
		dir := h.WriteModelDir(modelPushMinimalConfig)
		err := h.Execute("model", "push", "--dir", dir, "--watch", "--promote")
		h.Require.ErrorContains(err, "cannot be combined with --promote")
	})

	t.Run("hot_reload_requires_watch", func(t *testing.T) {
		h := newModelPushHarness(t)
		dir := h.WriteModelDir(modelPushMinimalConfig)
		err := h.Execute("model", "push", "--dir", dir, "--watch-hot-reload")
		h.Require.ErrorContains(err, "require --watch")
	})
}

// push --watch with a development deployment that never becomes ready fails out
// of the watch loop, and because the loop errored no JSON result is written to
// stdout (the success document must not precede a failed watch).
func Test_Model_Push_Watch_ReadinessFailureNoJSON(t *testing.T) {
	h := newModelPushHarness(t)
	addPushWatchRoutes(h)
	h.API.SetRoute("GET", watchDepPath, 200, deploymentResponse("BUILD_FAILED"))

	dir := h.WriteModelDir(modelPushMinimalConfig)
	err := h.Execute("model", "push", "--dir", dir, "--watch", "--output", "json")
	h.Require.Error(err)
	h.Require.Contains(h.Stderr.String(), "not ready")
	h.Require.Zero(h.Stdout.Len(), "no JSON result on a failed watch")

	prep := h.API.FindCall("POST", "/v1/prepare_model_upload")
	h.Require.NotNil(prep)
	dep := prep.BodyJSON(h.T)["deployment"].(map[string]any)
	h.Require.Equal(true, dep["is_development"])
}

// push --watch pushes a development deployment then enters the patch loop; a
// simulated Ctrl-C during the first sync ends it as an interrupt.
func Test_Model_Push_Watch_EntersLoopThenInterrupt(t *testing.T) {
	h := newModelPushHarness(t)
	addPushWatchRoutes(h)
	interruptWatchOnSync(h.CommandHarness, h.API)

	dir := h.WriteModelDir(modelPushMinimalConfig)
	err := h.Execute("model", "push", "--dir", dir, "--watch")
	h.Require.Error(err)
	h.Require.Equal(130, h.ExitCode)
	h.Require.NotNil(h.API.FindCall("POST", watchSyncPath))
	h.Require.Zero(h.Stdout.Len(), "interrupt is not a clean completion")
}
