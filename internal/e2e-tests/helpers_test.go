//go:build e2e

package e2etests

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/basetenlabs/baseten-cli/internal/cmd"
	"github.com/stretchr/testify/require"
)

// Minimal Truss source files baked into the test binary.
const trussConfigTmpl = `model_name: %s
python_version: py313
resources:
  cpu: 50m
  memory: 50Mi
  use_gpu: false
`

const trussModelPy = `class Model:
    def predict(self, request):
        return {"got request": request}
`

// deployWaitTimeout caps how long we wait for any single deployment to reach
// ACTIVE. Two deploys happen in this test (initial + redeploy), so the
// effective ceiling on the whole test is roughly 2x this plus overhead.
const deployWaitTimeout = 10 * time.Minute

// pushedDeployment is the JSON shape returned by `model push --output json`.
type pushedDeployment struct {
	Model struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"model"`
	Deployment struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	} `json:"deployment"`
}

// cli runs the CLI in-process with the given args, returning captured stdout
// and stderr. Non-zero exits surface as a non-nil error.
func cli(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	return cliCtx(t, t.Context(), args...)
}

// cliCtx is cli with an explicit context. Use this from t.Cleanup, since
// `t.Context()` is canceled before cleanup runs.
func cliCtx(t *testing.T, ctx context.Context, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	return cliWithStdin(t, ctx, "", args...)
}

// cliWithStdin is cliCtx that pipes the given string to the command's stdin.
func cliWithStdin(t *testing.T, ctx context.Context, stdin string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var sout, serr bytes.Buffer
	exit := 0
	err = cmd.Execute(ctx, cmd.ExecuteOptions{
		Args:         args,
		Stdin:        strings.NewReader(stdin),
		Stdout:       &sout,
		Stderr:       &serr,
		ExitWithCode: func(c int) { exit = c },
	})
	if err == nil && exit != 0 {
		err = fmt.Errorf("baseten %s: exit %d", strings.Join(args, " "), exit)
	}
	return sout.String(), serr.String(), err
}

// mustCLI runs cli and fatals on error, returning stdout.
func mustCLI(t *testing.T, args ...string) string {
	t.Helper()
	out, errOut, err := cli(t, args...)
	if err != nil {
		t.Fatalf("baseten %s failed: %v\nstderr: %s", strings.Join(args, " "), err, errOut)
	}
	return out
}

// mustCLIStdin is mustCLI with a string piped to the command's stdin.
func mustCLIStdin(t *testing.T, stdin string, args ...string) string {
	t.Helper()
	out, errOut, err := cliWithStdin(t, t.Context(), stdin, args...)
	if err != nil {
		t.Fatalf("baseten %s failed: %v\nstderr: %s", strings.Join(args, " "), err, errOut)
	}
	return out
}

// writeTruss materializes the baked-in Truss source into a temp dir with
// the given model name baked into config.yaml.
func writeTruss(t *testing.T, modelName string) string {
	t.Helper()
	dir := t.TempDir()
	cfg := fmt.Sprintf(trussConfigTmpl, modelName)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(cfg), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "model"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "model", "model.py"), []byte(trussModelPy), 0o644))
	return dir
}

// waitForActive polls the deployment until it reaches ACTIVE, fatals on
// terminal failure states or timeout.
func waitForActive(t *testing.T, modelID, deploymentID string) {
	t.Helper()
	deadline := time.Now().Add(deployWaitTimeout)
	for time.Now().Before(deadline) {
		out, _, err := cli(t, "api", "management", fmt.Sprintf("models/%s/deployments/%s", modelID, deploymentID))
		if err == nil {
			var resp struct {
				Status string `json:"status"`
			}
			if jerr := json.Unmarshal([]byte(out), &resp); jerr == nil {
				t.Logf("deployment %s status: %s", deploymentID, resp.Status)
				switch resp.Status {
				case "ACTIVE":
					return
				case "FAILED", "BUILD_FAILED", "DEPLOY_FAILED", "UNHEALTHY":
					t.Fatalf("deployment %s entered terminal failure state: %s", deploymentID, resp.Status)
				}
			}
		}
		time.Sleep(10 * time.Second)
	}
	t.Fatalf("deployment %s did not reach ACTIVE within %s", deploymentID, deployWaitTimeout)
}

// lookupModelIDByName is the fallback used at cleanup when we couldn't parse
// the push response (e.g. push failed mid-way). Returns "" if not found.
func lookupModelIDByName(t *testing.T, name string) string {
	t.Helper()
	out, _, err := cli(t, "api", "management", "models")
	if err != nil {
		return ""
	}
	var resp struct {
		Models []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		return ""
	}
	for _, m := range resp.Models {
		if m.Name == name {
			return m.ID
		}
	}
	return ""
}
