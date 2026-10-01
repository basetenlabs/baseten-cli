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
// unless the e2e env vars are set, plus BASETEN_E2E_TEST_SANDBOXES_URL: while
// the sandbox control plane lives on a different domain than the management
// API during development, the CLI routes it there through
// BASETEN_SANDBOXES_API_URL_OVERRIDE.
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
	sandboxesURL := os.Getenv("BASETEN_E2E_TEST_SANDBOXES_URL")
	if sandboxesURL == "" {
		t.Skip("BASETEN_E2E_TEST_SANDBOXES_URL not set")
	}
	t.Setenv("BASETEN_API_KEY", apiKey)
	t.Setenv("BASETEN_REMOTE_URL", remoteURL)
	t.Setenv("BASETEN_SANDBOXES_API_URL_OVERRIDE", sandboxesURL)
	t.Setenv("BASETEN_CONFIG_DIR", t.TempDir())
	return &sandboxSuite{name: "cli-e2e-sandbox-" + time.Now().Format("20060102-150405")}
}

// TestE2ESandboxLifecycle creates a sandbox and waits for it, runs a command
// in it, checks the listing and the record, stops and starts it, and deletes
// it.
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
		if _, errOut, err := cliCtx(t, ctx, "sandbox", "delete", s.name, "--yes"); err != nil {
			t.Logf("cleanup delete failed (may already be gone): %v\nstderr: %s", err, errOut)
		}
	})

	t.Run("Create", s.Create)
	t.Run("Exec", s.Exec)
	t.Run("List", s.List)
	t.Run("Describe", s.Describe)
	t.Run("Update", s.Update)
	t.Run("StopStart", s.StopStart)
	t.Run("Delete", s.Delete)
}

func (s *sandboxSuite) Create(t *testing.T) {
	createOut := mustCLICtx(t, s.longContext(t), "sandbox", "create", s.name,
		"--label", "e2e=cli")
	require.Contains(t, createOut, "Name:        "+s.name)
	require.Contains(t, createOut, "Status:      DEPLOYED")
	require.Contains(t, createOut, "URL:")
}

func (s *sandboxSuite) Exec(t *testing.T) {
	out := mustCLI(t, "sandbox", "exec", s.name, "--", "echo", "hello")
	require.Contains(t, out, "hello")

	// One quoted argument is the whole command string: variables and
	// redirects are the sandbox shell's to interpret.
	out = mustCLI(t, "sandbox", "exec", s.name, "--", "echo \"$((1+1))\" > /tmp/sum")
	require.Empty(t, out)
	out = mustCLI(t, "sandbox", "exec", s.name, "--", "cat", "/tmp/sum")
	require.Contains(t, out, "2")

	// A failing command's exit code becomes the CLI's.
	_, _, err := cli(t, "sandbox", "exec", s.name, "--", "false")
	require.Error(t, err)
	require.Contains(t, err.Error(), "code 1")
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
	out := mustCLI(t, "sandbox", "describe", s.name)
	require.Contains(t, out, "Name:        "+s.name)
	require.Contains(t, out, "Status:      DEPLOYED")

	jsonOut := mustCLI(t, "sandbox", "describe", s.name, "--output", "json", "--jq", ".status")
	require.Contains(t, strings.TrimSpace(jsonOut), "DEPLOYED")
}

func (s *sandboxSuite) Update(t *testing.T) {
	out := mustCLI(t, "sandbox", "update", s.name,
		"--display-name", "E2E Sandbox", "--label", "e2e=cli", "--label", "stage=two")
	require.Contains(t, out, "Display:     E2E Sandbox")
}

func (s *sandboxSuite) StopStart(t *testing.T) {
	out := mustCLI(t, "sandbox", "stop", s.name)
	require.Contains(t, out, "Enabled:     false")

	out = mustCLI(t, "sandbox", "start", s.name)
	require.Contains(t, out, "Enabled:     true")
}

func (s *sandboxSuite) Delete(t *testing.T) {
	out := mustCLI(t, "sandbox", "delete", s.name, "--yes")
	require.Contains(t, out, "Deleting sandbox "+s.name)
}

// longContext bounds the one command that legitimately takes minutes: create
// waits for the sandbox to deploy.
func (s *sandboxSuite) longContext(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	t.Cleanup(cancel)
	return ctx
}
