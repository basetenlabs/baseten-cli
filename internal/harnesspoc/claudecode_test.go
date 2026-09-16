//go:build harnesspoc

// Package harnesspoc's harness tests drive the real harness binaries off the
// PATH against the mock gateway. They are behind the harnesspoc build tag
// because they need those binaries installed, and they are the whole point of
// the proof of concept: what a harness does with our model list is not
// something a unit test can assert.
package harnesspoc

import (
	"context"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// pocCatalog is a catalog of names no harness could know, which is the
// interesting case: they stand in for route names.
func pocCatalog() Catalog {
	return Catalog{Models: []Model{
		{Slug: "acme/glm-5-3", DisplayName: "ACME GLM 5.3", Description: "Served by Baseten"},
		{Slug: "acme/kimi-k3", DisplayName: "ACME Kimi K3"},
	}}
}

func TestClaudeCodeSendsOurModelName(t *testing.T) {
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude is not on the PATH")
	}
	catalog := pocCatalog()
	srv := &Server{Catalog: catalog, Logf: func(format string, args ...any) { t.Logf(format, args...) }}
	gateway := httptest.NewServer(srv.Handler())
	defer gateway.Close()

	root := t.TempDir()
	harness := claudeCode{}
	res, err := harness.Setup(SetupOptions{
		Root:            root,
		BaseURL:         gateway.URL,
		APIKey:          "harness-poc-key",
		Mode:            ModeLocal,
		ReplaceBuiltins: true,
		Catalog:         catalog,
	})
	require.NoError(t, err)
	require.FileExists(t, res.Paths[0])

	model := catalog.Models[0].Slug
	out, runErr := runClaude(t, harness.dir(root), "--model", model, "-p", "Reply with OK and nothing else.")
	t.Logf("claude exit: %v\noutput:\n%s", runErr, out)

	// The gateway refuses every inference call, so a non-zero exit is
	// expected. What matters is what reached us and what the harness showed.
	var messages []Call
	for _, c := range srv.Calls() {
		if strings.HasPrefix(c.Path, "/v1/messages") {
			messages = append(messages, c)
		}
	}
	require.NotEmpty(t, messages, "claude made no inference call to the gateway")
	require.Equal(t, model, messages[0].Model,
		"claude sent a different model name than the one selected")
	require.Contains(t, out, ErrorMarker,
		"claude did not surface the gateway's error message")
}

// runClaude runs the claude binary against an isolated config dir, in a
// working directory of its own so no project settings are picked up.
func runClaude(t *testing.T, configDir string, args ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Dir = t.TempDir()
	// A minimal environment: anything inherited could point the harness at a
	// real provider and make the test pass for the wrong reason.
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + filepath.Dir(configDir),
		"CLAUDE_CONFIG_DIR=" + configDir,
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}
