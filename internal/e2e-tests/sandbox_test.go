//go:build e2e

package e2etests

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// sandboxSuite drives the sandbox commands against a real backend. Skips
// unless the e2e env vars are set: the sandbox control plane serves on the
// management domain.
type sandboxSuite struct {
	name string
}

func newSandboxSuite(t *testing.T) *sandboxSuite {
	apiKey := os.Getenv("BASETEN_E2E_TEST_API_KEY")
	if apiKey == "" {
		t.Skip("BASETEN_E2E_TEST_API_KEY not set")
	}
	remoteURL := os.Getenv("BASETEN_E2E_TEST_REMOTE_URL")
	require.NotEmpty(t, remoteURL, "BASETEN_E2E_TEST_API_KEY is set but BASETEN_E2E_TEST_REMOTE_URL is missing")
	t.Setenv("BASETEN_API_KEY", apiKey)
	t.Setenv("BASETEN_REMOTE_URL", remoteURL)
	t.Setenv("BASETEN_CONFIG_DIR", t.TempDir())
	return &sandboxSuite{name: "cli-e2e-sandbox-" + time.Now().Format("20060102-150405")}
}

// TestE2ESandboxLifecycle creates a sandbox and waits for it, runs commands
// in it, checks the listing and the record, and deletes it.
func TestE2ESandboxLifecycle(t *testing.T) {
	s := newSandboxSuite(t)
	// Registered on the lifecycle test, not the Create subtest: a failure
	// in any later phase must still delete the sandbox.
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if _, errOut, err := cliCtx(t, ctx, "sandbox", "delete", "--name", s.name, "--yes"); err != nil {
			t.Logf("cleanup delete failed (may already be gone): %v\nstderr: %s", err, errOut)
		}
	})

	t.Run("Create", s.Create)
	t.Run("Exec", s.Exec)
	t.Run("Process", s.Process)
	t.Run("List", s.List)
	t.Run("Describe", s.Describe)
	t.Run("Update", s.Update)
	t.Run("Delete", s.Delete)
}

func (s *sandboxSuite) Create(t *testing.T) {
	createOut := mustCLICtx(t, s.longContext(t), "sandbox", "create",
		"--name", s.name, "--label", "e2e=cli", "--wait")
	require.Contains(t, createOut, "Name:        "+s.name)
	require.Contains(t, createOut, "Status:      DEPLOYED")
	require.Contains(t, createOut, "URL:")
}

func (s *sandboxSuite) Exec(t *testing.T) {
	out := mustCLI(t, "sandbox", "exec", "--name", s.name, "--", "echo", "hello")
	require.Contains(t, out, "hello")

	// One quoted argument is the whole command string: variables and
	// redirects are the sandbox shell's to interpret.
	out = mustCLI(t, "sandbox", "exec", "--name", s.name, "--", "echo \"$((1+1))\" > /tmp/sum")
	require.Empty(t, out)
	out = mustCLI(t, "sandbox", "exec", "--name", s.name, "--", "cat", "/tmp/sum")
	require.Contains(t, out, "2")

	// A failing command's exit code becomes the CLI's.
	_, _, err := cli(t, "sandbox", "exec", "--name", s.name, "--", "false")
	require.Error(t, err)
	require.Contains(t, err.Error(), "code 1")
}

func (s *sandboxSuite) Process(t *testing.T) {
	startJSON := mustCLI(t, "sandbox", "process", "start", "--name", s.name,
		"--output", "json", "--", "echo e2e-process")
	var started struct {
		PID string `json:"pid"`
	}
	require.NoError(t, json.Unmarshal([]byte(startJSON), &started))
	require.NotEmpty(t, started.PID)

	listOut := mustCLI(t, "sandbox", "process", "list", "--name", s.name)
	require.Contains(t, listOut, "e2e-process")

	// The process may need a moment to flush its output.
	require.Eventually(t, func() bool {
		logsOut, _, err := cli(t, "sandbox", "process", "logs", "--name", s.name, "--pid", started.PID)
		return err == nil && strings.Contains(logsOut, "e2e-process")
	}, 30*time.Second, 2*time.Second)
}

func (s *sandboxSuite) List(t *testing.T) {
	out := mustCLI(t, "sandbox", "list", "--query", s.name)
	require.Contains(t, out, s.name)

	jsonOut := mustCLI(t, "sandbox", "list", "--query", s.name, "--output", "json")
	var listed struct {
		Items []struct {
			Name string `json:"name"`
			URL  string `json:"url"`
		} `json:"items"`
	}
	require.NoError(t, json.Unmarshal([]byte(jsonOut), &listed))
	require.Len(t, listed.Items, 1)
	require.NotEmpty(t, listed.Items[0].URL)
}

func (s *sandboxSuite) Describe(t *testing.T) {
	out := mustCLI(t, "sandbox", "describe", "--name", s.name)
	require.Contains(t, out, "Name:        "+s.name)
	require.Contains(t, out, "Status:      DEPLOYED")

	jsonOut := mustCLI(t, "sandbox", "describe", "--name", s.name, "--output", "json", "--jq", ".status")
	require.Contains(t, strings.TrimSpace(jsonOut), "DEPLOYED")
}

func (s *sandboxSuite) Update(t *testing.T) {
	out := mustCLI(t, "sandbox", "update", "--name", s.name,
		"--label", "e2e=cli", "--label", "stage=two")
	require.Contains(t, out, "stage=two")
}

func (s *sandboxSuite) Delete(t *testing.T) {
	out := mustCLI(t, "sandbox", "delete", "--name", s.name, "--yes")
	require.Contains(t, out, "Deleting sandbox "+s.name)
}

// longContext bounds the one command that legitimately takes minutes: create
// with --wait waits for the sandbox to deploy.
func (s *sandboxSuite) longContext(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	t.Cleanup(cancel)
	return ctx
}
