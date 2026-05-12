//go:build e2e

package e2etests

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestE2EModelLifecycle pushes a fresh model, drives the management and
// inference APIs against it, redeploys, and tears it down. Skips when the
// required env vars are absent.
func TestE2EModelLifecycle(t *testing.T) {
	l := newLifecycle(t)
	t.Run("WaitForDeploy", l.WaitForDeploy)
	t.Run("ManagementAPI", l.ManagementAPI)
	t.Run("InferenceAPI", l.InferenceAPI)
	t.Run("Redeploy", l.Redeploy)
}

// lifecycle holds the state shared across the lifecycle sub-tests. Created
// by [newLifecycle], which also performs the initial push and registers
// teardown.
type lifecycle struct {
	modelName           string
	modelDir            string
	modelID             string
	initialDeploymentID string
}

// newLifecycle runs the env-gate, materializes the truss source, performs the
// initial --promote push, and registers cleanup. Fatals on setup failure so
// sub-tests can assume valid state.
func newLifecycle(t *testing.T) *lifecycle {
	apiKey := os.Getenv("BASETEN_E2E_TEST_API_KEY")
	if apiKey == "" {
		t.Skip("BASETEN_E2E_TEST_API_KEY not set")
	}
	remoteURL := os.Getenv("BASETEN_E2E_TEST_REMOTE_URL")
	require.NotEmpty(t, remoteURL, "BASETEN_E2E_TEST_API_KEY is set but BASETEN_E2E_TEST_REMOTE_URL is missing")

	t.Setenv("BASETEN_API_KEY", apiKey)
	t.Setenv("BASETEN_REMOTE_URL", remoteURL)
	t.Setenv("BASETEN_CONFIG_DIR", t.TempDir())

	l := &lifecycle{
		modelName: fmt.Sprintf("cli-e2e-%s", randomSuffix(t)),
	}
	l.modelDir = writeSimpleTruss(t, l.modelName)

	// Register cleanup before the push so even a partial create gets removed.
	t.Cleanup(func() {
		if l.modelID == "" {
			l.modelID = lookupModelIDByName(t, l.modelName)
		}
		if l.modelID == "" {
			t.Logf("no model to clean up for %s", l.modelName)
			return
		}
		t.Logf("deleting model %s (%s)", l.modelName, l.modelID)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, errOut, err := cliCtx(t, ctx, "api", "management", "-X", "DELETE", "models/"+l.modelID); err != nil {
			t.Logf("cleanup delete failed: %v\nstderr: %s", err, errOut)
		}
	})

	pushOut := mustCLI(t, "model", "push", "--dir", l.modelDir, "--promote", "--output", "json")
	var initial pushedDeployment
	require.NoError(t, json.Unmarshal([]byte(pushOut), &initial))
	require.Equal(t, l.modelName, initial.Model.Name)
	l.modelID = initial.Model.ID
	l.initialDeploymentID = initial.Deployment.ID
	return l
}

func (l *lifecycle) WaitForDeploy(t *testing.T) {
	waitForActive(t, l.modelID, l.initialDeploymentID)
}

func (l *lifecycle) ManagementAPI(t *testing.T) {
	t.Run("ListIncludesModel", func(t *testing.T) {
		out := mustCLI(t, "api", "management", "models")
		var resp struct {
			Models []struct {
				ID string `json:"id"`
			} `json:"models"`
		}
		require.NoError(t, json.Unmarshal([]byte(out), &resp))
		found := false
		for _, m := range resp.Models {
			if m.ID == l.modelID {
				found = true
				break
			}
		}
		require.True(t, found, "model %s missing from list", l.modelID)
	})

	t.Run("GetModel", func(t *testing.T) {
		out := mustCLI(t, "api", "management", "models/"+l.modelID)
		var resp map[string]any
		require.NoError(t, json.Unmarshal([]byte(out), &resp))
		require.Equal(t, l.modelID, resp["id"])
		require.Equal(t, l.modelName, resp["name"])
	})

	t.Run("GetModelJQ", func(t *testing.T) {
		out := mustCLI(t, "api", "management", "--jq", ".id", "models/"+l.modelID)
		require.Equal(t, fmt.Sprintf("%q\n", l.modelID), out)
	})

	t.Run("ListDeployments", func(t *testing.T) {
		out := mustCLI(t, "api", "management", "models/"+l.modelID+"/deployments")
		var resp struct {
			Deployments []map[string]any `json:"deployments"`
		}
		require.NoError(t, json.Unmarshal([]byte(out), &resp))
		require.NotEmpty(t, resp.Deployments)
	})

	t.Run("NotFound", func(t *testing.T) {
		_, errOut, err := cli(t, "api", "management", "models/does-not-exist")
		require.Error(t, err)
		require.Contains(t, errOut, "status 404")
	})
}

func (l *lifecycle) InferenceAPI(t *testing.T) {
	out := mustCLI(t, "api", "inference", "--model-id", l.modelID, "-F", "x=1", "production/predict")
	var resp map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &resp))
	require.Equal(t, map[string]any{"got request": map[string]any{"x": float64(1)}}, resp)
}

func (l *lifecycle) Redeploy(t *testing.T) {
	out := mustCLI(t, "model", "push", "--dir", l.modelDir, "--promote", "--output", "json")
	var redeploy pushedDeployment
	require.NoError(t, json.Unmarshal([]byte(out), &redeploy))
	require.Equal(t, l.modelID, redeploy.Model.ID, "redeploy should reuse existing model")
	require.NotEqual(t, l.initialDeploymentID, redeploy.Deployment.ID, "redeploy should create a new deployment")
	waitForActive(t, l.modelID, redeploy.Deployment.ID)
}

func randomSuffix(t *testing.T) string {
	t.Helper()
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}
	return hex.EncodeToString(b[:])
}
