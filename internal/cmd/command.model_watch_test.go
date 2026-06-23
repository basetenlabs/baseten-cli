package cmd_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/basetenlabs/baseten-cli/internal/cmd"
)

const (
	watchModelID       = "model-123"
	watchDeploymentID  = "deploy-456"
	watchDevPath       = "/v1/models/model-123/deployments/development"
	watchDepPath       = "/v1/models/model-123/deployments/deploy-456"
	watchStatePath     = "/v1/models/model-123/deployments/deploy-456/patches/state"
	watchStagePath     = "/v1/models/model-123/deployments/deploy-456/patches"
	watchSyncPath      = "/v1/models/model-123/deployments/deploy-456/patches/sync"
	watchWakeInference = "/deployment/deploy-456/wake"
	watchKeepalivePath = "/deployment/deploy-456/sync/v1/models/model"
)

// newModelWatchHarness wires a CommandHarness to a MockManagementAPI, points the
// inference base URL at it (the standalone watch wakes the deployment), and
// registers the happy-path routes: model lookup, a ready development deployment,
// an empty running patch point, and successful stage/sync. Individual tests
// override the routes they exercise.
func newModelWatchHarness(t *testing.T) (*CommandHarness, *MockManagementAPI, string) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	t.Setenv("BASETEN_INFERENCE_BASE_URL_OVERRIDE", m.URL)

	m.SetRoute("GET", "/v1/models", 200, map[string]any{
		"models": []any{map[string]any{
			"id": watchModelID, "name": "test-model",
			"created_at": "2026-01-01T00:00:00Z", "deployments_count": 1,
			"instance_type_name": "1x2",
		}},
	})
	m.SetRoute("GET", watchDevPath, 200, deploymentResponse("ACTIVE"))
	m.SetRoute("GET", watchDepPath, 200, deploymentResponse("ACTIVE"))
	m.SetRoute("POST", watchWakeInference, 200, map[string]any{})
	// Running point is empty, so the local model directory diffs to a non-empty
	// set of ops on the first tick (every file is an add plus a config op).
	m.SetRoute("GET", watchStatePath, 200, map[string]any{
		"running_patch_point": map[string]any{
			"config": "", "content_hashes": map[string]any{}, "hash": "h0",
		},
	})
	m.SetRoute("POST", watchStagePath, 200, map[string]any{
		"patch_point": map[string]any{
			"config": "", "content_hashes": map[string]any{}, "hash": "h1",
		},
	})
	m.SetRoute("POST", watchSyncPath, 200, map[string]any{})

	return h, m, writeWatchModelDir(t)
}

// writeWatchModelDir creates a minimal Truss directory (config.yaml naming the
// model the harness serves, plus a model.py) and returns its path.
func writeWatchModelDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("model_name: test-model\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "model.py"), []byte("class Model:\n    pass\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// interruptWatchOnSync simulates a Ctrl-C right after the patch flow reaches the sync
// endpoint: the route responds, then cancels the command context. Cancelling on
// a real HTTP call gives the loop a deterministic, time-independent exit, the
// same way the signal handler cancels the context in production.
func interruptWatchOnSync(h *CommandHarness, m *MockManagementAPI) {
	ctx, cancel := context.WithCancel(h.Context)
	h.Context = ctx
	m.SetRouteFunc("POST", watchSyncPath, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{})
		cancel()
	})
}

func Test_Model_Watch_ModelNotFound(t *testing.T) {
	h, m, dir := newModelWatchHarness(t)
	m.SetRoute("GET", "/v1/models", 200, map[string]any{"models": []any{}})

	err := h.Execute("model", "watch", "--dir", dir)
	h.Require.Error(err)
	h.Require.Contains(h.Stderr.String(), "no model named")
	h.Require.Contains(h.Stderr.String(), "push --develop")
}

func Test_Model_Watch_NoDevelopmentDeployment(t *testing.T) {
	h, m, dir := newModelWatchHarness(t)
	m.SetRoute("GET", watchDevPath, 404, map[string]any{"error": "not found"})

	err := h.Execute("model", "watch", "--dir", dir)
	h.Require.Error(err)
	h.Require.Contains(h.Stderr.String(), "no development deployment")
}

func Test_Model_Watch_MissingConfig(t *testing.T) {
	h, _, _ := newModelWatchHarness(t)
	err := h.Execute("model", "watch", "--dir", t.TempDir())
	h.Require.Error(err)
	h.Require.Contains(h.Stderr.String(), "config.yaml not found")
}

// A development deployment in a terminal status is not patchable; the readiness
// wait fails with a plain error (not an interrupt).
func Test_Model_Watch_ReadinessFailure(t *testing.T) {
	h, m, dir := newModelWatchHarness(t)
	m.SetRoute("GET", watchDepPath, 200, deploymentResponse("BUILD_FAILED"))

	err := h.Execute("model", "watch", "--dir", dir)
	h.Require.Error(err)
	h.Require.Contains(h.Stderr.String(), "not ready")
	h.Require.Equal(1, h.ExitCode)
}

// Happy path: readiness passes, the first tick stages and syncs a patch, then a
// simulated Ctrl-C during sync interrupts the otherwise-endless watch. The stage
// request echoes the running point's hash and carries the computed ops.
func Test_Model_Watch_InitialPatchThenInterrupt(t *testing.T) {
	h, m, dir := newModelWatchHarness(t)
	interruptWatchOnSync(h, m)

	err := h.Execute("model", "watch", "--dir", dir)
	h.Require.Error(err)
	h.Require.Equal(130, h.ExitCode)
	h.Require.Contains(h.Stderr.String(), "Canceled.")

	stage := m.FindCall("POST", watchStagePath)
	h.Require.NotNil(stage)
	body := stage.BodyJSON(h.T)
	h.Require.Equal("h0", body["prev_patch_hash"])
	ops, _ := body["patch_ops"].([]any)
	h.Require.NotEmpty(ops)

	h.Require.NotNil(m.FindCall("POST", watchSyncPath))
}

// Keepalive is on by default (Truss parity): the watch loop pings the
// deployment to prevent scale-to-zero without any flag. Making that first ping
// cancel the context gives a deterministic, time-independent exit that fires
// only if the keepalive ran.
func Test_Model_Watch_KeepaliveOnByDefault(t *testing.T) {
	h, m, dir := newModelWatchHarness(t)
	ctx, cancel := context.WithCancel(h.Context)
	h.Context = ctx
	m.SetRouteFunc("GET", watchKeepalivePath, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		cancel()
	})

	err := h.Execute("model", "watch", "--dir", dir)
	h.Require.Error(err)
	h.Require.Equal(130, h.ExitCode)
	h.Require.NotNil(m.FindCall("GET", watchKeepalivePath))
}

// --no-keepalive opts out: the watch loop never pings the deployment.
func Test_Model_Watch_NoKeepaliveSkipsPing(t *testing.T) {
	h, m, dir := newModelWatchHarness(t)
	interruptWatchOnSync(h, m)

	err := h.Execute("model", "watch", "--dir", dir, "--no-keepalive")
	h.Require.Error(err)
	h.Require.Equal(130, h.ExitCode)
	h.Require.Nil(m.FindCall("GET", watchKeepalivePath))
}

// A sync that keeps reporting a recoverable failure is retried a bounded number
// of times and then gives up with a non-interrupt error.
func Test_Model_Watch_InitialSyncRecoverableExhausted(t *testing.T) {
	h, m, dir := newModelWatchHarness(t)
	h.Context = cmd.WithSleep(h.Context, func(_ context.Context, _ time.Duration) error { return nil })
	m.SetRoute("POST", watchSyncPath, 503, map[string]any{"error": "not ready"})

	err := h.Execute("model", "watch", "--dir", dir)
	h.Require.Error(err)
	h.Require.Contains(h.Stderr.String(), "did not succeed after 5 attempts")
	h.Require.Equal(1, h.ExitCode)
}
