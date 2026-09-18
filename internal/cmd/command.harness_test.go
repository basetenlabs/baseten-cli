package cmd_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	public "github.com/basetenlabs/baseten-cli/cmd"
	internalcmd "github.com/basetenlabs/baseten-cli/internal/cmd"
)

func Test_Harness_Setup_Lifecycle(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("adapter currently validated on macOS only")
	}
	h, api := productionHarness(t)
	dir := t.TempDir()
	h.Context = internalcmd.WithExecer(h.Context, harnessFakeExecer{})
	for _, name := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_MODEL", "CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY", "CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST", "BASETEN_PROFILE"} {
		t.Setenv(name, "")
	}
	path := filepath.Join(dir, "fixture", "settings.json")
	args := []string{"harness", "setup", "--harness", "claude-code", "--config", path, "--team", "team-a", "--key-name", "lifecycle", "--model", "acme/primary", "--output", "json"}
	h.Require.NoError(h.Execute(append(args, "--dry-run")...))
	h.Require.Contains(h.Stdout.String(), "modelPicker.options")
	h.Require.Nil(api.FindCall("POST", "/v1/api_keys"))
	h.Require.NotContains(h.Stdout.String(), "baseten-harness-local-fixture")
	_, err := os.Stat(filepath.Dir(path))
	h.Require.True(os.IsNotExist(err))
	h.Require.Error(h.Execute(args...))
	_, err = os.Stat(path)
	h.Require.True(os.IsNotExist(err))
	h.Require.NoError(h.Execute(append(args, "--yes")...))
	h.Require.FileExists(path)
	h.Require.NotNil(api.FindCall("POST", "/v1/api_keys"))
	b, readErr := os.ReadFile(path)
	h.Require.NoError(readErr)
	h.Require.Contains(string(b), "created-routes-secret")
	h.Require.NoError(h.Execute(append(args, "--yes")...))
	h.Require.Contains(h.Stdout.String(), `"changed": false`)
	h.Require.NoError(h.Execute("harness", "status", "--config", path, "--output", "json"))
	h.Require.Contains(h.Stdout.String(), `"state": "configured"`)
	h.Require.NotContains(h.Stdout.String(), "baseten-harness-local-fixture")
	h.Require.NoError(h.Execute("harness", "teardown", "--config", path, "--yes", "--output", "json"))
	_, err = os.Stat(path)
	h.Require.True(os.IsNotExist(err))
	h.Require.NoError(h.Execute("harness", "status", "--config", path, "--output", "json"))
	h.Require.Contains(h.Stdout.String(), `"state": "not-configured"`)
}
func Test_Harness_Setup_FixtureFlagsRemoved(t *testing.T) {
	h := NewCommandHarness(t)
	h.Require.Error(h.Execute("harness", "setup", "--catalog-fixture", "unused.json"))
	h.Require.Contains(h.Stderr.String(), "Unknown flag")
	h.Require.NoError(h.Execute("harness", "setup", "--help"))
	h.Require.NotContains(h.Stdout.String(), "fixture")
}
func Test_Harness_Setup_HiddenParentAndVisibleSubcommands(t *testing.T) {
	h := NewCommandHarness(t)
	found := false
	for _, c := range public.Root.Children {
		if c.Name == "harness" {
			found = true
			h.Require.True(c.Hidden)
			for _, leaf := range c.Children {
				h.Require.False(leaf.Hidden)
				h.Require.NoError(h.Execute("harness", leaf.Name, "--help"))
			}
		}
	}
	h.Require.True(found)
	h.Require.NoError(h.Execute("--help"))
	h.Require.NotContains(h.Stdout.String(), "Configure Baseten harness")
	h.Require.NoError(h.Execute("harness"))
	for _, name := range []string{"setup", "status", "teardown"} {
		h.Require.Contains(h.Stdout.String(), name)
	}
	h.Require.NoError(h.Execute("__complete", "harness", ""))
	h.Require.Contains(h.Stdout.String(), "setup\t")
}

func Test_Harness_Setup_RequiresExplicitNoninteractiveChoices(t *testing.T) {
	for _, output := range []string{"text", "json"} {
		t.Run(output, func(t *testing.T) {
			h := NewCommandHarness(t)
			args := []string{"harness", "setup", "--output", output}
			h.Require.Error(h.Execute(args...))
			h.Require.Contains(h.Stderr.String(), "pass --harness")
			args = append(args, "--harness", "claude-code")
			h.Require.Error(h.Execute(args...))
			h.Require.Contains(h.Stderr.String(), "pass --harness and --model")
		})
	}
}

func productionHarness(t *testing.T) (*CommandHarness, *MockManagementAPI) {
	if runtime.GOOS != "darwin" {
		t.Skip("adapter currently validated on macOS only")
	}
	h, api := routesAuthHarness(t)
	h.Context = internalcmd.WithExecer(h.Context, harnessFakeExecer{})
	api.SetRoute("GET", "/v1/routes", 200, map[string]any{
		"items":      []any{map[string]any{"id": "route-a", "name": "acme/primary", "team_id": "team-a", "display_name": "Primary", "invoke_url": api.URL}},
		"pagination": map[string]any{"has_more": false, "cursor": nil},
	})
	api.SetRoute("GET", "/v1/models", 200, map[string]any{"data": []any{map[string]any{
		"id": "acme/primary", "context_length": 128000, "max_completion_tokens": 4096, "supported_features": []string{"tools"}, "input_modalities": []string{"text"},
	}}})
	return h, api
}

func Test_Harness_Setup_FailureNeverMints(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   any
		want   string
	}{
		{"unavailable", 404, map[string]any{}, "HTTP 404"},
		{"empty", 200, map[string]any{"items": []any{}, "pagination": map[string]any{"has_more": false}}, "no accessible Routes"},
		{"pagination", 200, map[string]any{"items": []any{}, "pagination": map[string]any{"has_more": true}}, "pagination cursor"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, api := productionHarness(t)
			api.SetRoute("GET", "/v1/routes", tc.status, tc.body)
			path := filepath.Join(t.TempDir(), "settings.json")
			h.Require.Error(h.Execute("harness", "setup", "--harness", "claude-code", "--model", "acme/primary", "--team", "team-a", "--config", path, "--yes"))
			h.Require.Contains(h.Stderr.String(), tc.want)
			h.Require.Nil(api.FindCall("POST", "/v1/api_keys"))
			_, err := os.Stat(path)
			h.Require.True(os.IsNotExist(err))
		})
	}
}

func Test_Harness_Setup_MissingMetadataNeverMints(t *testing.T) {
	h, api := productionHarness(t)
	api.SetRoute("GET", "/v1/models", 200, map[string]any{"data": []any{}})
	h.Require.Error(h.Execute("harness", "setup", "--harness", "claude-code", "--model", "acme/primary", "--team", "team-a", "--config", filepath.Join(t.TempDir(), "settings.json"), "--yes"))
	h.Require.Contains(h.Stderr.String(), "no accessible Routes have usable")
	h.Require.Nil(api.FindCall("POST", "/v1/api_keys"))
}

func Test_Harness_Setup_PaginationAndReuse(t *testing.T) {
	h, api := productionHarness(t)
	pages := 0
	api.SetRouteFunc("GET", "/v1/routes", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		h.Require.Equal("team-a", r.URL.Query().Get("team_id"))
		pages++
		cursor := r.URL.Query().Get("cursor")
		if cursor == "" {
			json.NewEncoder(w).Encode(map[string]any{"items": []any{}, "pagination": map[string]any{"has_more": true, "cursor": "page-two"}})
			return
		}
		h.Require.Equal("page-two", cursor)
		json.NewEncoder(w).Encode(map[string]any{"items": []any{map[string]any{"id": "route-a", "name": "acme/primary", "team_id": "team-a", "display_name": "Primary", "invoke_url": api.URL}}, "pagination": map[string]any{"has_more": false}})
	})
	args := []string{"harness", "setup", "--harness", "claude-code", "--model", "acme/primary", "--team", "team-a", "--key-name", "reuse", "--config", filepath.Join(t.TempDir(), "settings.json"), "--yes", "--output", "json"}
	h.Require.NoError(h.Execute(args...))
	h.Require.NoError(h.Execute(args...))
	h.Require.Contains(h.Stdout.String(), `"changed": false`)
	h.Require.NotContains(h.Stdout.String(), "created-routes-secret")
	count := 0
	for _, call := range api.Calls() {
		if call.Method == "POST" {
			count++
		}
	}
	h.Require.Equal(1, count)
	h.Require.Equal(4, pages)
}

type harnessFakeExecer struct{}

func (harnessFakeExecer) LookPath(name string) (string, error) { return "/fake/" + name, nil }
func (harnessFakeExecer) Exec(command *exec.Cmd) error {
	_, err := fmt.Fprintln(command.Stdout, "2.1.272 (Claude Code)")
	return err
}
