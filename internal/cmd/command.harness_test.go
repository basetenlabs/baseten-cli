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

	"github.com/BurntSushi/toml"

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
	h.Require.Contains(string(b), `"replaceBuiltInOptions": true`)
	h.Require.NoError(h.Execute(append(args, "--yes")...))
	h.Require.Contains(h.Stdout.String(), `"changed": false`)
	h.Require.NoError(h.Execute("harness", "status", "--harness", "claude-code", "--config", path, "--output", "json"))
	h.Require.Contains(h.Stdout.String(), `"state": "configured"`)
	h.Require.NotContains(h.Stdout.String(), "baseten-harness-local-fixture")
	h.Require.NoError(h.Execute("harness", "teardown", "--harness", "claude-code", "--config", path, "--yes", "--output", "json"))
	_, err = os.Stat(path)
	h.Require.True(os.IsNotExist(err))
	h.Require.NoError(h.Execute("harness", "status", "--harness", "claude-code", "--config", path, "--output", "json"))
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
			h.Require.Len(c.Children, 3)
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
		})
	}
}

func productionHarness(t *testing.T) (*CommandHarness, *MockManagementAPI) {
	if runtime.GOOS != "darwin" {
		t.Skip("adapter currently validated on macOS only")
	}
	h, api := routesAuthHarness(t)
	h.Context = internalcmd.WithExecer(h.Context, harnessFakeExecer{})
	api.SetRouteFunc("GET", "/v1/routes", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"items":      []any{map[string]any{"id": "route-a", "name": "acme/primary", "team_id": r.URL.Query().Get("team_id"), "display_name": "Primary", "invoke_url": api.URL}},
			"pagination": map[string]any{"has_more": false, "cursor": nil},
		})
	})

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
		{"empty", 200, map[string]any{"items": []any{}, "pagination": map[string]any{"has_more": false}}, "no accessible routes"},
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

func Test_Harness_Setup_FirstClaudeRouteWithoutModelMetadata(t *testing.T) {
	h, api := productionHarness(t)
	api.SetRoute("GET", "/v1/models", 500, map[string]string{"message": "must not be called"})
	path := filepath.Join(t.TempDir(), "settings.json")
	h.Require.NoError(h.Execute("harness", "setup", "--harness", "claude-code", "--team", "team-a", "--config", path, "--yes"))
	h.Require.Nil(api.FindCall("GET", "/v1/models"))
	data, err := os.ReadFile(path)
	h.Require.NoError(err)
	var config map[string]any
	h.Require.NoError(json.Unmarshal(data, &config))
	h.Require.Equal("acme/primary", config["model"])
	env := config["env"].(map[string]any)
	h.Require.Equal("deepseek-ai/DeepSeek-V4.1-Flash", env["ANTHROPIC_DEFAULT_HAIKU_MODEL"])
	h.Require.Equal("deepseek-ai/DeepSeek-V4.1-Flash", env["ANTHROPIC_SMALL_FAST_MODEL"])
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
	version := "2.1.272 (Claude Code)"
	switch filepath.Base(command.Path) {
	case "codex":
		version = "codex-cli 0.134.0"
	case "opencode":
		version = "1.18.31"
	}
	_, err := fmt.Fprintln(command.Stdout, version)
	return err
}

func Test_Harness_Setup_ReplacementPreviewConfirmationAndRestore(t *testing.T) {
	h, api := productionHarness(t)
	path := filepath.Join(t.TempDir(), "settings.json")
	original := []byte(`{"model":"my-old-model","theme":"dark"}`)
	h.Require.NoError(os.WriteFile(path, original, 0600))
	args := []string{"harness", "setup", "--harness", "claude-code", "--team", "team-a", "--model", "acme/primary", "--config", path}
	h.Require.NoError(h.Execute(append(args, "--dry-run", "--output", "json")...))
	var result public.HarnessPlanResult
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &result))
	h.Require.Contains(result.Replaced, "model")
	h.Require.NotContains(h.Stdout.String(), "my-old-model")
	checkUnchanged := func() {
		data, err := os.ReadFile(path)
		h.Require.NoError(err)
		h.Require.Equal(original, data)
		_, err = os.Stat(path + ".baseten-harness.json")
		h.Require.True(os.IsNotExist(err))
		h.Require.Nil(api.FindCall("POST", "/v1/api_keys"))
	}
	checkUnchanged()
	h.Require.Error(h.Execute(args...))
	h.Require.Contains(h.Stderr.String(), "pass --yes")
	h.Require.Contains(h.Stdout.String(), "Settings to replace: model")
	h.Require.Contains(h.Stdout.String(), "backed up")
	checkUnchanged()
	h.Require.NoError(h.Execute(append(args, "--yes")...))
	data, err := os.ReadFile(path)
	h.Require.NoError(err)
	h.Require.Contains(string(data), "acme/primary")
	h.Require.Contains(string(data), "dark")
	h.Require.NoError(h.Execute("harness", "teardown", "--harness", "claude-code", "--config", path, "--yes"))
	restored, err := os.ReadFile(path)
	h.Require.NoError(err)
	h.Require.JSONEq(string(original), string(restored))
}

func Test_Harness_Setup_ReplacementRejectsConcurrentEdit(t *testing.T) {
	h, api := productionHarness(t)
	path := filepath.Join(t.TempDir(), "settings.json")
	h.Require.NoError(os.WriteFile(path, []byte(`{"model":"old"}`), 0600))
	edited := []byte(`{"model":"user-edited-after-preview"}`)
	api.SetRouteFunc("POST", "/v1/api_keys", func(w http.ResponseWriter, r *http.Request) {
		h.Require.NoError(os.WriteFile(path, edited, 0600))
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"api_key": "dummy-route-key"})
	})
	h.Require.Error(h.Execute("harness", "setup", "--harness", "claude-code", "--team", "team-a", "--model", "acme/primary", "--config", path, "--yes"))
	data, err := os.ReadFile(path)
	h.Require.NoError(err)
	h.Require.Equal(edited, data)
	_, err = os.Stat(path + ".baseten-harness.json")
	h.Require.True(os.IsNotExist(err))
}

func Test_Harness_Setup_ConfirmedReplacementOfManagedModel(t *testing.T) {
	h, _ := productionHarness(t)
	path := filepath.Join(t.TempDir(), "settings.json")
	args := []string{"harness", "setup", "--harness", "claude-code", "--team", "team-a", "--model", "acme/primary", "--config", path}
	h.Require.NoError(h.Execute(append(args, "--yes")...))
	data, err := os.ReadFile(path)
	h.Require.NoError(err)
	var config map[string]any
	h.Require.NoError(json.Unmarshal(data, &config))
	config["model"] = "opus"
	edited, err := json.Marshal(config)
	h.Require.NoError(err)
	h.Require.NoError(os.WriteFile(path, edited, 0600))
	journalBefore, err := os.ReadFile(path + ".baseten-harness.json")
	h.Require.NoError(err)
	h.Require.NoError(h.Execute(append(args, "--dry-run")...))
	h.Require.Contains(h.Stdout.String(), "Settings to replace: model")
	h.Require.Error(h.Execute(args...))
	unchanged, err := os.ReadFile(path)
	h.Require.NoError(err)
	h.Require.Equal(edited, unchanged)
	journalAfter, err := os.ReadFile(path + ".baseten-harness.json")
	h.Require.NoError(err)
	h.Require.Equal(journalBefore, journalAfter)
	h.Require.NoError(h.Execute(append(args, "--yes")...))
	h.Require.NoError(h.Execute("harness", "teardown", "--harness", "claude-code", "--config", path, "--yes"))
	restored, err := os.ReadFile(path)
	h.Require.NoError(err)
	h.Require.NoError(json.Unmarshal(restored, &config))
	h.Require.Equal("opus", config["model"])
}

func Test_Harness_Setup_DefaultRouteAndOverride(t *testing.T) {
	for _, name := range []string{"claude-code", "codex", "opencode"} {
		for _, override := range []string{"", "acme/second"} {
			t.Run(name+"/"+override, func(t *testing.T) {
				h, api := productionHarness(t)
				api.SetRoute("GET", "/v1/routes", 200, map[string]any{
					"items": []any{
						map[string]any{"id": "first", "name": "acme/z-first", "team_id": "team-a", "display_name": "First", "invoke_url": api.URL},
						map[string]any{"id": "second", "name": "acme/second", "team_id": "team-a", "display_name": "Second", "invoke_url": api.URL},
					},
					"pagination": map[string]any{"has_more": false},
				})
				filename := "settings.json"
				if name == "codex" {
					filename = "config.toml"
				}
				path := filepath.Join(t.TempDir(), filename)
				args := []string{"harness", "setup", "--harness", name, "--team", "team-a", "--config", path, "--yes"}
				expected := "acme/z-first"
				if override != "" {
					args = append(args, "--model", override)
					expected = override
				}
				h.Require.NoError(h.Execute(args...))
				h.Require.Contains(h.Stderr.String(), name+" default route: "+expected)
				data, err := os.ReadFile(path)
				h.Require.NoError(err)
				var config map[string]any
				if name == "codex" {
					h.Require.NoError(toml.Unmarshal(data, &config))
				} else {
					h.Require.NoError(json.Unmarshal(data, &config))
				}
				if name == "opencode" {
					expected = "baseten-harness/" + expected
				}
				h.Require.Equal(expected, config["model"])
			})
		}
	}
}

func Test_Harness_StatusAndTeardown_RequireHarness(t *testing.T) {
	for _, command := range []string{"status", "teardown"} {
		for _, output := range []string{"text", "json"} {
			t.Run(command+"/"+output, func(t *testing.T) {
				h := NewCommandHarness(t)
				path := filepath.Join(t.TempDir(), "settings.json")
				original := []byte(`{"model":"user-model"}`)
				h.Require.NoError(os.WriteFile(path, original, 0600))
				h.Require.Error(h.Execute("harness", command, "--config", path, "--output", output))
				h.Require.Contains(h.Stderr.String(), "harness")
				h.Require.Contains(h.Stderr.String(), `Required flag(s) "harness" not set`)
				data, err := os.ReadFile(path)
				h.Require.NoError(err)
				h.Require.Equal(original, data)
			})
		}
	}
}
