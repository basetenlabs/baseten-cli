//go:build e2e

package e2etests

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// TestE2EModelLifecycle pushes a fresh model, drives the management and
// inference APIs against it, redeploys, and tears it down. Skips when the
// required env vars are absent.
func TestE2EModelLifecycle(t *testing.T) {
	l := newLifecycle(t)
	t.Run("APIManagement", l.APIManagement)
	t.Run("APIInference", l.APIInference)
	t.Run("Model", l.Model)
	t.Run("Deployment", l.Deployment)
	t.Run("Logs", l.Logs)
	t.Run("Environment", l.Environment)
	t.Run("ModelPredict", l.ModelPredict)
	t.Run("Metrics", l.Metrics)
	t.Run("SSH", l.SSH)
	t.Run("Redeploy", l.Redeploy)
	t.Run("Delete", l.Delete)
}

// lifecycle holds the state shared across the lifecycle sub-tests. Created
// by [newLifecycle], which also performs the initial push and registers
// teardown.
type lifecycle struct {
	modelName           string
	modelDir            string
	modelID             string
	initialDeploymentID string
	// deploymentName is the initial deployment's server-assigned name, captured
	// in the Deployment phase and reused by the name-based lookups.
	deploymentName string
}

// newLifecycle runs the env-gate, materializes the truss source, performs the
// initial push to production, and registers cleanup. Fatals on setup failure so
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
	l.modelDir = writeTruss(t, l.modelName)

	// Register cleanup before the push so even a partial create gets removed.
	t.Cleanup(func() {
		if os.Getenv("BASETEN_E2E_KEEP_MODEL") != "" {
			t.Logf("BASETEN_E2E_KEEP_MODEL set; leaving model %q in place", l.modelName)
			return
		}
		if l.modelID == "" {
			l.modelID = lookupModelIDByName(t, l.modelName)
		}
		if l.modelID == "" {
			return
		}
		t.Logf("deleting model %s (%s)", l.modelName, l.modelID)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, errOut, err := cliCtx(t, ctx, "model", "delete", "--model-id", l.modelID, "--yes"); err != nil {
			t.Logf("cleanup delete failed: %v\nstderr: %s", err, errOut)
		}
	})

	pushOut := mustCLI(t, "model", "push", "--dir", l.modelDir, "--environment", "production", "--wait", "--output", "json")
	var initial pushedDeployment
	require.NoError(t, json.Unmarshal([]byte(pushOut), &initial))
	require.Equal(t, l.modelName, initial.Model.Name)
	l.modelID = initial.Model.ID
	l.initialDeploymentID = initial.Deployment.ID
	l.deploymentName = initial.Deployment.Name
	return l
}

func (l *lifecycle) APIManagement(t *testing.T) {
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

func (l *lifecycle) APIInference(t *testing.T) {
	out := mustCLI(t, "api", "inference", "--model-id", l.modelID, "-F", "x=1", "production/predict")
	var resp map[string]any
	require.NoError(t, json.Unmarshal([]byte(out), &resp))
	require.Equal(t, map[string]any{"got request": map[string]any{"x": float64(1)}}, resp)
}

func (l *lifecycle) Model(t *testing.T) {
	t.Run("List", func(t *testing.T) {
		out := mustCLI(t, "model", "list", "--output", "json")
		var resp struct {
			Models []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"models"`
		}
		require.NoError(t, json.Unmarshal([]byte(out), &resp))
		for _, m := range resp.Models {
			if m.ID == l.modelID {
				require.Equal(t, l.modelName, m.Name)
				return
			}
		}
		t.Fatalf("model %s missing from model list", l.modelID)
	})

	t.Run("DescribeByID", func(t *testing.T) {
		out := mustCLI(t, "model", "describe", "--model-id", l.modelID, "--output", "json")
		var resp struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		require.NoError(t, json.Unmarshal([]byte(out), &resp))
		require.Equal(t, l.modelID, resp.ID)
		require.Equal(t, l.modelName, resp.Name)
	})

	t.Run("DescribeByName", func(t *testing.T) {
		out := mustCLI(t, "model", "describe", "--model-name", l.modelName, "--output", "json")
		var resp struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		require.NoError(t, json.Unmarshal([]byte(out), &resp))
		require.Equal(t, l.modelID, resp.ID)
		require.Equal(t, l.modelName, resp.Name)
	})
}

func (l *lifecycle) Deployment(t *testing.T) {
	t.Run("List", func(t *testing.T) {
		out := mustCLI(t, "model", "deployment", "list", "--model-id", l.modelID, "--output", "json")
		var resp struct {
			Deployments []struct {
				ID      string `json:"id"`
				ModelID string `json:"model_id"`
			} `json:"deployments"`
		}
		require.NoError(t, json.Unmarshal([]byte(out), &resp))
		for _, d := range resp.Deployments {
			if d.ID == l.initialDeploymentID {
				require.Equal(t, l.modelID, d.ModelID)
				return
			}
		}
		t.Fatalf("deployment %s missing from deployment list", l.initialDeploymentID)
	})

	t.Run("Describe", func(t *testing.T) {
		out := mustCLI(t, "model", "deployment", "describe",
			"--model-id", l.modelID, "--deployment-id", l.initialDeploymentID, "--output", "json")
		var resp struct {
			ID      string `json:"id"`
			ModelID string `json:"model_id"`
		}
		require.NoError(t, json.Unmarshal([]byte(out), &resp))
		require.Equal(t, l.initialDeploymentID, resp.ID)
		require.Equal(t, l.modelID, resp.ModelID)
	})

	t.Run("DescribeByName", func(t *testing.T) {
		// Resolving both the model and the deployment by name (server-side
		// ?name= filters) yields the same deployment as the IDs.
		require.NotEmpty(t, l.deploymentName, "deployment missing name")
		out := mustCLI(t, "model", "deployment", "describe",
			"--model-name", l.modelName, "--deployment-name", l.deploymentName, "--output", "json")
		var resp struct {
			ID string `json:"id"`
		}
		require.NoError(t, json.Unmarshal([]byte(out), &resp))
		require.Equal(t, l.initialDeploymentID, resp.ID)
	})

	t.Run("Config_Text", func(t *testing.T) {
		out := mustCLI(t, "model", "deployment", "config",
			"--model-id", l.modelID, "--deployment-id", l.initialDeploymentID)
		require.Equal(t, fmt.Sprintf(trussConfigTmpl, l.modelName), out)
	})

	t.Run("Config_JSON", func(t *testing.T) {
		out := mustCLI(t, "model", "deployment", "config",
			"--model-id", l.modelID, "--deployment-id", l.initialDeploymentID, "--output", "json")
		var resp struct {
			Config    map[string]any `json:"config"`
			RawConfig *string        `json:"raw_config"`
		}
		require.NoError(t, json.Unmarshal([]byte(out), &resp))
		require.NotNil(t, resp.RawConfig, "raw_config should be persisted from the original push")
		require.Equal(t, fmt.Sprintf(trussConfigTmpl, l.modelName), *resp.RawConfig)
	})

	t.Run("Download_OutDir", func(t *testing.T) {
		outDir := filepath.Join(t.TempDir(), "truss")
		mustCLI(t, "model", "deployment", "download",
			"--model-id", l.modelID, "--deployment-id", l.initialDeploymentID, "--out-dir", outDir)

		// The downloaded archive is the gathered truss: external_package_dirs is
		// stripped and the external module is inlined under packages/. The config
		// is re-marshalled server-side, so assert on structure, not exact bytes.
		gotCfg, err := os.ReadFile(filepath.Join(outDir, "config.yaml"))
		require.NoError(t, err)
		var cfgMap map[string]any
		require.NoError(t, yaml.Unmarshal(gotCfg, &cfgMap))
		require.Equal(t, l.modelName, cfgMap["model_name"])
		require.NotContains(t, cfgMap, "external_package_dirs")
		gotModel, err := os.ReadFile(filepath.Join(outDir, "model", "model.py"))
		require.NoError(t, err)
		require.Equal(t, trussModelPy, string(gotModel))
		gotExt, err := os.ReadFile(filepath.Join(outDir, "packages", "e2e_ext.py"))
		require.NoError(t, err)
		require.Equal(t, e2eExternalModule, string(gotExt))
	})

	t.Run("Download_OutFile", func(t *testing.T) {
		outFile := filepath.Join(t.TempDir(), "truss.tar")
		mustCLI(t, "model", "deployment", "download",
			"--model-id", l.modelID, "--deployment-id", l.initialDeploymentID, "--out-file", outFile)
		st, err := os.Stat(outFile)
		require.NoError(t, err)
		require.Greater(t, st.Size(), int64(0), "downloaded tar should be non-empty")
	})
}

// logLine is the subset of a log record the Logs phase asserts on.
type logLine struct {
	Message string `json:"message"`
	Level   string `json:"level"`
}

// collectLogs runs `model deployment logs --output jsonl` over the last hour
// with the given extra filter args and parses each line. CLI errors are
// returned (not fatal) so callers can retry while logs propagate.
func (l *lifecycle) collectLogs(t *testing.T, extraArgs ...string) ([]logLine, error) {
	t.Helper()
	return l.collectLogsFrom(t, append([]string{"model", "deployment", "logs",
		"--model-id", l.modelID, "--deployment-id", l.initialDeploymentID,
		"--since", "1h", "--output", "jsonl"}, extraArgs...)...)
}

// collectEnvLogs is collectLogs over the production environment path. The model
// is pushed to production, so its current deployment is initialDeploymentID.
func (l *lifecycle) collectEnvLogs(t *testing.T, extraArgs ...string) ([]logLine, error) {
	t.Helper()
	return l.collectLogsFrom(t, append([]string{"model", "environment", "logs",
		"--model-id", l.modelID, "--environment", "production",
		"--since", "1h", "--output", "jsonl"}, extraArgs...)...)
}

// collectLogsFrom runs a `... logs --output jsonl` command and parses each line.
// CLI errors are returned (not fatal) so callers can retry while logs propagate.
func (l *lifecycle) collectLogsFrom(t *testing.T, args ...string) ([]logLine, error) {
	t.Helper()
	out, _, err := cli(t, args...)
	if err != nil {
		return nil, err
	}
	var lines []logLine
	for _, raw := range strings.Split(strings.TrimSpace(out), "\n") {
		if raw == "" {
			continue
		}
		var ll logLine
		require.NoError(t, json.Unmarshal([]byte(raw), &ll))
		lines = append(lines, ll)
	}
	return lines, nil
}

func (l *lifecycle) Logs(t *testing.T) {
	contains := func(lines []logLine, word string) bool {
		for _, ll := range lines {
			if strings.Contains(ll.Message, word) {
				return true
			}
		}
		return false
	}
	mustCollect := func(t *testing.T, extraArgs ...string) []logLine {
		lines, err := l.collectLogs(t, extraArgs...)
		require.NoError(t, err)
		return lines
	}

	// Loki can lag well past the deployment going ACTIVE before the load()
	// log lines are queryable; poll generously until the info marker lands.
	var lines []logLine
	require.Eventually(t, func() bool {
		got, err := l.collectLogs(t)
		if err != nil {
			return false
		}
		lines = got
		return contains(lines, e2eLogInfoWord)
	}, 90*time.Second, 3*time.Second, "info log line never appeared")

	t.Run("AllLevels", func(t *testing.T) {
		require.True(t, contains(lines, e2eLogInfoWord), "info line missing")
		require.True(t, contains(lines, e2eLogWarningWord), "warning line missing")
		require.True(t, contains(lines, e2eLogErrorWord), "error line missing")
	})

	t.Run("MinLevelWarning", func(t *testing.T) {
		lines := mustCollect(t, "--min-level", "warning")
		require.False(t, contains(lines, e2eLogInfoWord), "info should be filtered out")
		require.True(t, contains(lines, e2eLogWarningWord))
		require.True(t, contains(lines, e2eLogErrorWord))
	})

	t.Run("MinLevelError", func(t *testing.T) {
		lines := mustCollect(t, "--min-level", "error")
		require.False(t, contains(lines, e2eLogInfoWord))
		require.False(t, contains(lines, e2eLogWarningWord))
		require.True(t, contains(lines, e2eLogErrorWord))
	})

	t.Run("Includes", func(t *testing.T) {
		lines := mustCollect(t, "--includes", e2eLogWarningWord)
		require.NotEmpty(t, lines)
		for _, ll := range lines {
			require.Contains(t, ll.Message, e2eLogWarningWord)
		}
	})

	t.Run("Excludes", func(t *testing.T) {
		lines := mustCollect(t, "--includes", e2eLogMarker, "--excludes", e2eLogWarningWord)
		require.True(t, contains(lines, e2eLogInfoWord))
		require.True(t, contains(lines, e2eLogErrorWord))
		require.False(t, contains(lines, e2eLogWarningWord))
	})

	// Backward pagination must return the same set as a single fetch. With a
	// tiny --page-size the CLI is forced to walk several pages over the burst of
	// uniquely-numbered lines; the result must match an unpaged fetch exactly,
	// proving no line is lost or duplicated at a page seam.
	t.Run("Pagination", func(t *testing.T) {
		msgs := func(lines []logLine) []string {
			out := make([]string, len(lines))
			for i, ll := range lines {
				out[i] = ll.Message
			}
			return out
		}
		// Wait until every pagination line is queryable so the two fetches below
		// see a stable set rather than racing log propagation.
		var full []logLine
		require.Eventually(t, func() bool {
			got, err := l.collectLogs(t, "--includes", e2ePageMarker, "--limit", "0")
			if err != nil {
				return false
			}
			full = got
			return len(full) >= e2ePageTotalLineCount
		}, 90*time.Second, 3*time.Second, "pagination lines never appeared")

		paged, err := l.collectLogs(t, "--includes", e2ePageMarker, "--limit", "0", "--page-size", "7")
		require.NoError(t, err)
		// Same multiset as the single fetch, and enough lines that page-size 7
		// spanned multiple requests.
		require.Greater(t, len(full), 7)
		require.ElementsMatch(t, msgs(full), msgs(paged))
	})

	// The environment logs path spans the versions active on the environment;
	// production's current deployment is this model, so the same markers show
	// up and filters apply identically.
	t.Run("Environment", func(t *testing.T) {
		lines, err := l.collectEnvLogs(t)
		require.NoError(t, err)
		require.True(t, contains(lines, e2eLogInfoWord), "info line missing")
		require.True(t, contains(lines, e2eLogWarningWord), "warning line missing")
		require.True(t, contains(lines, e2eLogErrorWord), "error line missing")

		filtered, err := l.collectEnvLogs(t, "--min-level", "error")
		require.NoError(t, err)
		require.False(t, contains(filtered, e2eLogInfoWord), "info should be filtered out")
		require.True(t, contains(filtered, e2eLogErrorWord))
	})
}

func (l *lifecycle) Environment(t *testing.T) {
	t.Run("List", func(t *testing.T) {
		out := mustCLI(t, "model", "environment", "list", "--model-id", l.modelID, "--output", "json")
		var resp struct {
			Environments []struct {
				Name              string `json:"name"`
				ModelID           string `json:"model_id"`
				CurrentDeployment struct {
					ID string `json:"id"`
				} `json:"current_deployment"`
			} `json:"environments"`
		}
		require.NoError(t, json.Unmarshal([]byte(out), &resp))
		for _, e := range resp.Environments {
			if e.Name == "production" {
				require.Equal(t, l.modelID, e.ModelID)
				require.Equal(t, l.initialDeploymentID, e.CurrentDeployment.ID)
				return
			}
		}
		t.Fatalf("production environment missing from environment list")
	})

	t.Run("Describe", func(t *testing.T) {
		out := mustCLI(t, "model", "environment", "describe",
			"--model-id", l.modelID, "--environment", "production", "--output", "json")
		var resp struct {
			Name              string `json:"name"`
			ModelID           string `json:"model_id"`
			CurrentDeployment struct {
				ID string `json:"id"`
			} `json:"current_deployment"`
		}
		require.NoError(t, json.Unmarshal([]byte(out), &resp))
		require.Equal(t, "production", resp.Name)
		require.Equal(t, l.modelID, resp.ModelID)
		require.Equal(t, l.initialDeploymentID, resp.CurrentDeployment.ID)
	})
}

func (l *lifecycle) ModelPredict(t *testing.T) {
	t.Run("Default", func(t *testing.T) {
		out := mustCLI(t, "model", "predict", "--model-id", l.modelID, "--data", `{"x":1}`, "--output", "json")
		var resp map[string]any
		require.NoError(t, json.Unmarshal([]byte(out), &resp))
		require.Equal(t, map[string]any{"got request": map[string]any{"x": float64(1)}}, resp)
	})

	t.Run("ExternalPackage", func(t *testing.T) {
		out := mustCLI(t, "model", "predict", "--model-id", l.modelID, "--data", `{"style":"external"}`, "--output", "json")
		var resp map[string]any
		require.NoError(t, json.Unmarshal([]byte(out), &resp))
		require.Equal(t, map[string]any{"external_const": e2eExternalConst}, resp)
  })

	t.Run("ByDeploymentName", func(t *testing.T) {
		// Targets the deployment by resolving both the model and the deployment
		// by name (server-side ?name= filters).
		require.NotEmpty(t, l.deploymentName, "deployment missing name")
		out := mustCLI(t, "model", "predict",
			"--model-name", l.modelName, "--deployment-name", l.deploymentName,
			"--data", `{"x":1}`, "--output", "json")
		var resp map[string]any
		require.NoError(t, json.Unmarshal([]byte(out), &resp))
		require.Equal(t, map[string]any{"got request": map[string]any{"x": float64(1)}}, resp)
	})

	t.Run("Streaming_Text", func(t *testing.T) {
		out := mustCLI(t, "model", "predict", "--model-id", l.modelID,
			"--data", `{"style":"streaming","chunks":["alpha","beta","gamma"]}`)
		require.Equal(t, "alphabetagamma", out)
	})

	t.Run("SSE_JSONL", func(t *testing.T) {
		out := mustCLI(t, "model", "predict", "--model-id", l.modelID,
			"--data", `{"style":"sse","chunks":["alpha","beta","gamma"]}`,
			"--output", "jsonl")
		lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
		require.Equal(t, 3, len(lines), "SSE should yield one envelope per data: event")
		var concat []byte
		for _, line := range lines {
			var env struct {
				Body string `json:"body"`
			}
			require.NoError(t, json.Unmarshal([]byte(line), &env))
			b, err := base64.StdEncoding.DecodeString(env.Body)
			require.NoError(t, err)
			concat = append(concat, b...)
		}
		require.Equal(t, []byte("alphabetagamma"), concat)
	})
}

func (l *lifecycle) Metrics(t *testing.T) {
	type metricsResp struct {
		Mode              string `json:"mode"`
		MetricDescriptors []struct {
			Name string `json:"name"`
		} `json:"metric_descriptors"`
	}
	hasDescriptor := func(r metricsResp, name string) bool {
		for _, d := range r.MetricDescriptors {
			if d.Name == name {
				return true
			}
		}
		return false
	}

	// current is a point-in-time snapshot. baseten_replicas_active is always
	// registered for a deployment, so the descriptor must appear; its value may
	// be 0, so this asserts shape only, never a value.
	t.Run("Current", func(t *testing.T) {
		out := mustCLI(t, "model", "deployment", "metrics",
			"--model-id", l.modelID, "--deployment-id", l.initialDeploymentID, "--output", "json")
		var resp metricsResp
		require.NoError(t, json.Unmarshal([]byte(out), &resp))
		require.Equal(t, "CURRENT", resp.Mode)
		require.True(t, hasDescriptor(resp, "baseten_replicas_active"),
			"baseten_replicas_active missing from current snapshot")
	})

	// series exercises the windowed path; assert only the envelope shape (mode +
	// descriptors present), not values, which may be empty this soon after deploy.
	t.Run("Series", func(t *testing.T) {
		out := mustCLI(t, "model", "deployment", "metrics",
			"--model-id", l.modelID, "--deployment-id", l.initialDeploymentID,
			"--mode", "series", "--since", "1h", "--output", "json")
		var resp metricsResp
		require.NoError(t, json.Unmarshal([]byte(out), &resp))
		require.Equal(t, "SERIES", resp.Mode)
		require.NotEmpty(t, resp.MetricDescriptors)
	})

	// The environment metrics path aggregates across the deployments active on
	// the environment; production's current deployment is this model, so the
	// same registered descriptor appears. Shape only, never a value.
	t.Run("Environment", func(t *testing.T) {
		out := mustCLI(t, "model", "environment", "metrics",
			"--model-id", l.modelID, "--environment", "production", "--output", "json")
		var resp metricsResp
		require.NoError(t, json.Unmarshal([]byte(out), &resp))
		require.Equal(t, "CURRENT", resp.Mode)
		require.True(t, hasDescriptor(resp, "baseten_replicas_active"),
			"baseten_replicas_active missing from current snapshot")
	})
}

// SSH exercises the full `baseten ssh` flow against the live deployment: it
// builds the real binary, runs setup, then connects with the system ssh client
// (which invokes the binary for the sign/proxy steps) and cats the remote
// model.py, asserting it matches what was pushed.
func (l *lifecycle) SSH(t *testing.T) {
	if _, err := exec.LookPath("ssh"); err != nil {
		t.Skip("ssh client not available")
	}

	// The generated config invokes `baseten ssh sign|proxy` via the ssh client,
	// so the in-process harness cannot serve it: build a real binary from the
	// current source and point the config's baked command at it via
	// BASETEN_SSH_BINARY_OVERRIDE.
	binPath := filepath.Join(t.TempDir(), "baseten")
	build := exec.Command("go", "build", "-o", binPath, "./cmd/baseten")
	build.Dir = repoRoot(t)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building baseten binary: %v\n%s", err, out)
	}

	// Isolate the keypair/cert/JWT cache and the ssh config in temp locations.
	configPath := filepath.Join(t.TempDir(), "config")
	t.Setenv("BASETEN_SSH_DIR", t.TempDir())
	t.Setenv("BASETEN_SSH_CONFIG_PATH", configPath)
	t.Setenv("BASETEN_SSH_BINARY_OVERRIDE", binPath)

	mustCLI(t, "ssh", "setup")

	// runSSH connects with the system ssh client (which invokes the built binary
	// for the sign/proxy steps) and cats the remote model.py.
	runSSH := func(host string) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		var stdout, stderr bytes.Buffer
		c := exec.CommandContext(ctx, "ssh", "-F", configPath,
			"-o", "BatchMode=yes", "-o", "ConnectTimeout=30",
			host, "cat", "/app/model/model.py")
		c.Stdout, c.Stderr = &stdout, &stderr
		if err := c.Run(); err != nil {
			return "", fmt.Errorf("%w\nstderr: %s", err, stderr.String())
		}
		return stdout.String(), nil
	}

	// Deployment form. The workload's sshd can lag behind the deployment going
	// ACTIVE, so retry the first connection until it succeeds or the deadline
	// passes.
	deploymentHost := fmt.Sprintf("model-%s-%s.ssh.baseten.co", l.modelID, l.initialDeploymentID)
	var modelPy string
	require.Eventually(t, func() bool {
		out, err := runSSH(deploymentHost)
		if err != nil {
			t.Logf("ssh connect attempt to %s failed: %v", deploymentHost, err)
			return false
		}
		modelPy = out
		return true
	}, 3*time.Minute, 10*time.Second, "ssh connect to %s never succeeded", deploymentHost)
	require.Equal(t, trussModelPy, modelPy, "remote /app/model/model.py should match the pushed source")

	// Environment form: <env>.model-<id> resolves the environment's current
	// deployment (production points at initialDeploymentID) client-side in sign.
	// The workload is already warm from the connection above, so connect directly
	// without the retry loop.
	environmentHost := fmt.Sprintf("production.model-%s.ssh.baseten.co", l.modelID)
	envModelPy, err := runSSH(environmentHost)
	require.NoError(t, err, "ssh connect to environment host %s", environmentHost)
	require.Equal(t, trussModelPy, envModelPy,
		"remote /app/model/model.py via the environment host should match the pushed source")
}

func (l *lifecycle) Redeploy(t *testing.T) {
	out := mustCLI(t, "model", "push", "--dir", l.modelDir, "--environment", "production", "--wait", "--output", "json")
	var redeploy pushedDeployment
	require.NoError(t, json.Unmarshal([]byte(out), &redeploy))
	require.Equal(t, l.modelID, redeploy.Model.ID, "redeploy should reuse existing model")
	require.NotEqual(t, l.initialDeploymentID, redeploy.Deployment.ID, "redeploy should create a new deployment")
}

func (l *lifecycle) Delete(t *testing.T) {
	deletedID := l.modelID
	mustCLI(t, "model", "delete", "--model-id", deletedID, "--yes")
	l.modelID = ""

	_, errOut, err := cli(t, "model", "describe", "--model-id", deletedID)
	require.Error(t, err, "fetch should fail after delete; stderr: %s", errOut)
}

func randomSuffix(t *testing.T) string {
	t.Helper()
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}
	return hex.EncodeToString(b[:])
}
