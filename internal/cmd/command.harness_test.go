package cmd_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/BurntSushi/toml"
	public "github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-cli/internal/auth"
	internalcmd "github.com/basetenlabs/baseten-cli/internal/cmd"
	"github.com/zalando/go-keyring"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
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
	args := []string{"harness", "setup", "--harness", "claude-code", "--config", path, "--team", "team-a", "--key-name", "lifecycle", "--route", "acme/primary", "--output", "json"}
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
func Test_Harness_Setup_VisiblePreReleaseCommands(t *testing.T) {
	h := NewCommandHarness(t)
	found := false
	for _, c := range public.Root.Children {
		if c.Name == "harness" {
			found = true
			h.Require.False(c.Hidden)
			h.Require.Len(c.Children, 3)
			for _, leaf := range c.Children {
				h.Require.False(leaf.Hidden)
				h.Require.NoError(h.Execute("harness", leaf.Name, "--help"))
			}
		}
	}
	h.Require.True(found)
	h.Require.NoError(h.Execute("--help"))
	h.Require.Contains(h.Stdout.String(), "Configure Baseten harness")
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
			h.Require.Error(h.Execute("harness", "setup", "--harness", "claude-code", "--route", "acme/primary", "--team", "team-a", "--config", path, "--yes"))
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
	args := []string{"harness", "setup", "--harness", "claude-code", "--route", "acme/primary", "--team", "team-a", "--key-name", "reuse", "--config", filepath.Join(t.TempDir(), "settings.json"), "--yes", "--output", "json"}
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
	version := "2.1.278 (Claude Code)"
	switch filepath.Base(command.Path) {
	case "codex":
		version = "codex-cli 0.151.0"
	case "opencode":
		version = "1.18.21"
	}
	_, err := fmt.Fprintln(command.Stdout, version)
	return err
}

func Test_Harness_Setup_ReplacementPreviewConfirmationAndRestore(t *testing.T) {
	h, api := productionHarness(t)
	path := filepath.Join(t.TempDir(), "settings.json")
	original := []byte(`{"model":"my-old-model","theme":"dark"}`)
	h.Require.NoError(os.WriteFile(path, original, 0600))
	args := []string{"harness", "setup", "--harness", "claude-code", "--team", "team-a", "--route", "acme/primary", "--config", path}
	h.Require.NoError(h.Execute(append(args, "--dry-run", "--output", "json")...))
	var result public.HarnessPlansResult
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &result))
	h.Require.Contains(result.Changes[0].ReplacedSettings, "model")
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
	h.Require.Contains(h.Stdout.String(), "Existing integration settings will be replaced")
	h.Require.Contains(h.Stdout.String(), "Teardown restores settings from the first setup")
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
	h.Require.Error(h.Execute("harness", "setup", "--harness", "claude-code", "--team", "team-a", "--route", "acme/primary", "--config", path, "--yes"))
	data, err := os.ReadFile(path)
	h.Require.NoError(err)
	h.Require.Equal(edited, data)
	_, err = os.Stat(path + ".baseten-harness.json")
	h.Require.True(os.IsNotExist(err))
}

func Test_Harness_Setup_ConfirmedReplacementOfManagedModel(t *testing.T) {
	h, _ := productionHarness(t)
	path := filepath.Join(t.TempDir(), "settings.json")
	original := []byte(`{"model":"original-model"}`)
	h.Require.NoError(os.WriteFile(path, original, 0600))
	args := []string{"harness", "setup", "--harness", "claude-code", "--team", "team-a", "--route", "acme/primary", "--config", path}
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
	h.Require.Contains(h.Stdout.String(), "Existing integration settings will be replaced")
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
	h.Require.Equal(original, restored)
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
				path := filepath.Join(t.TempDir(), "user's config $(printf expanded)", filename)
				args := []string{"harness", "setup", "--harness", name, "--team", "team-a", "--config", path, "--yes"}
				expected := "acme/z-first"
				if override != "" {
					args = append(args, "--route", override)
					expected = override
				}
				h.Require.NoError(h.Execute(args...))
				h.Require.Contains(h.Stdout.String(), "Default route  "+expected)
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
				setupOutput := h.Stdout.String()
				assertFollowup := func(output, prefix, action string) {
					t.Helper()
					var command string
					for _, line := range strings.Split(output, "\n") {
						if strings.HasPrefix(line, prefix) {
							command = strings.TrimPrefix(line, prefix)
							command = strings.TrimSuffix(command, " (add --team <team> if needed), then restart the harness.")
							break
						}
					}
					h.Require.NotEmpty(command)
					// Parse the suggested command without invoking Baseten. Shell expansion
					// must preserve spaces, apostrophes, and literal command substitutions.
					parsed, err := exec.Command("sh", "-c", "set -- "+command+"; printf '%s\\0' \"$@\"").Output()
					h.Require.NoError(err)
					h.Require.Equal([]string{"baseten", "harness", action, "--harness", name, "--config", path}, strings.Split(strings.TrimSuffix(string(parsed), "\x00"), "\x00"))
				}
				assertFollowup(setupOutput, "Restore settings: ", "teardown")
				h.Require.Equal(1, strings.Count(setupOutput, "Available routes: 2"))
				h.Require.Equal(1, strings.Count(setupOutput, "Configuration saved."))
				for _, value := range []string{"NAME", "DISPLAY NAME", "acme/z-first", "First", "acme/second", "Second"} {
					h.Require.Contains(setupOutput, value)
				}
				h.Require.NotContains(setupOutput+h.Stderr.String(), "created-routes-secret")
				h.Require.NotContains(setupOutput, "ANTHROPIC_AUTH_TOKEN")
				calls := len(api.Calls())
				h.Require.NoError(h.Execute("harness", "status", "--harness", name, "--config", path))
				h.Require.Equal(calls, len(api.Calls()))
				assertFollowup(h.Stdout.String(), "Refresh: ", "setup")
				for _, value := range []string{"Configured routes: 2", "acme/z-first", "First", "acme/second", "Second", "Local configuration only", "Refresh: baseten harness setup --harness " + name} {
					h.Require.Contains(h.Stdout.String(), value)
				}
				h.Require.NotContains(h.Stdout.String()+h.Stderr.String(), "created-routes-secret")
				h.Require.NoError(h.Execute("harness", "status", "--harness", name, "--config", path, "--output", "json"))
				var statuses public.HarnessStatusesResult
				h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &statuses))
				h.Require.Len(statuses.Harnesses, 1)
				status := statuses.Harnesses[0]
				h.Require.Len(status.RouteDetails, 2)
				h.Require.Equal("First", status.RouteDetails[0].DisplayName)
				h.Require.Equal(strings.TrimPrefix(expected, "baseten-harness/"), status.DefaultRoute)
				h.Require.Equal(calls, len(api.Calls()))

			})
		}
	}
}

func Test_Harness_StatusAndTeardown_ConfigRequiresHarness(t *testing.T) {
	for _, command := range []string{"status", "teardown"} {
		for _, output := range []string{"text", "json"} {
			t.Run(command+"/"+output, func(t *testing.T) {
				h := NewCommandHarness(t)
				path := filepath.Join(t.TempDir(), "settings.json")
				original := []byte(`{"model":"user-model"}`)
				h.Require.NoError(os.WriteFile(path, original, 0600))
				h.Require.Error(h.Execute("harness", command, "--config", path, "--output", output))
				h.Require.Contains(h.Stderr.String(), "harness")
				h.Require.Contains(h.Stderr.String(), "--config requires exactly one explicit --harness")
				data, err := os.ReadFile(path)
				h.Require.NoError(err)
				h.Require.Equal(original, data)
			})
		}
	}
}

func Test_Harness_Setup_PrivateConfigOnApplyOnly(t *testing.T) {
	h, api := productionHarness(t)
	path := filepath.Join(t.TempDir(), "opencode.jsonc")
	original := []byte("{\n // User config\n \"$schema\": \"https://opencode.ai/config.json\",\n}\n")
	h.Require.NoError(os.WriteFile(path, original, 0644))
	h.Require.NoError(os.Chmod(path, 0644))
	args := []string{"harness", "setup", "--harness", "opencode", "--config", path, "--team", "team-a"}
	h.Require.NoError(h.Execute(append(args, "--dry-run")...))
	info, err := os.Stat(path)
	h.Require.NoError(err)
	h.Require.Equal(os.FileMode(0644), info.Mode().Perm())
	data, err := os.ReadFile(path)
	h.Require.NoError(err)
	h.Require.Equal(original, data)
	h.Require.Nil(api.FindCall("POST", "/v1/api_keys"))
	h.Require.NoError(h.Execute(append(args, "--yes")...))
	info, err = os.Stat(path)
	h.Require.NoError(err)
	h.Require.Equal(os.FileMode(0600), info.Mode().Perm())
	data, err = os.ReadFile(path)
	h.Require.NoError(err)
	h.Require.Contains(string(data), "User config")
	h.Require.Contains(string(data), "created-routes-secret")
}

func Test_Harness_Teardown_RestoresManagedEditsAndPreservesUnrelatedSettings(t *testing.T) {
	for _, name := range []string{"claude-code", "codex", "opencode"} {
		t.Run(name, func(t *testing.T) {
			h, _ := productionHarness(t)
			filename := "settings.json"
			if name == "codex" {
				filename = "config.toml"
			}
			path := filepath.Join(t.TempDir(), filename)
			write := func(data map[string]any) {
				t.Helper()
				var raw []byte
				var err error
				if name == "codex" {
					raw, err = toml.Marshal(data)
				} else {
					raw, err = json.Marshal(data)
				}
				h.Require.NoError(err)
				h.Require.NoError(os.WriteFile(path, raw, 0600))
			}
			read := func() map[string]any {
				t.Helper()
				raw, err := os.ReadFile(path)
				h.Require.NoError(err)
				var data map[string]any
				if name == "codex" {
					err = toml.Unmarshal(raw, &data)
				} else {
					err = json.Unmarshal(raw, &data)
				}
				h.Require.NoError(err)
				return data
			}
			write(map[string]any{"model": "original-model", "user_setting": "original"})
			h.Require.NoError(h.Execute("harness", "setup", "--harness", name, "--config", path, "--team", "team-a", "--yes"))
			data := read()
			data["model"] = "changed-by-harness"
			data["user_setting"] = "keep-this-edit"
			data["new_setting"] = "also-keep"
			write(data)
			args := []string{"harness", "teardown", "--harness", name, "--config", path}
			h.Require.NoError(h.Execute(append(args, "--dry-run")...))
			h.Require.Contains(h.Stdout.String(), "Would restore")
			h.Require.NotContains(h.Stdout.String(), "Settings:")
			h.Require.Equal(data, read())
			h.Require.Error(h.Execute(args...)) // Noninteractive apply still requires confirmation.
			h.Require.Equal(data, read())
			h.Require.NoError(h.Execute(append(args, "--yes")...))
			h.Require.Equal(map[string]any{"model": "original-model", "user_setting": "keep-this-edit", "new_setting": "also-keep"}, read())
			h.Require.NoFileExists(path + ".baseten-harness.json")
			if name == "codex" {
				h.Require.NoFileExists(path + ".baseten-models.json")
			}
		})
	}
}

func routesAuthHarness(t *testing.T) (*CommandHarness, *MockManagementAPI) {
	h := NewCommandHarness(t)
	api := h.MockManagementAPI()
	api.SetRoute("GET", "/v1/users/me", 200, map[string]string{"user_id": "user-a"})
	api.SetRoute("GET", "/v1/teams", 200, map[string]any{"teams": []map[string]string{{"id": "team-a", "name": "Engineering"}, {"id": "team-b", "name": "Research"}}})
	api.SetRoute("POST", "/v1/api_keys", 200, map[string]string{"api_key": "created-routes-secret"})
	return h, api
}

func Test_Harness_Setup_CredentialsCreateAndReuse(t *testing.T) {
	h, api := productionHarness(t)
	var headers []string
	api.SetRouteFunc("POST", "/v1/api_keys", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		headers = append(headers, r.Header.Get("Authorization"))
		_ = json.NewEncoder(w).Encode(map[string]string{"api_key": "created-routes-secret"})
	})
	args := []string{"--team", "team-a", "--key-name", "laptop", "--output", "json"}
	h.Require.NoError(executeHarnessSetup(t, h, args...))
	h.Require.NotContains(h.Stdout.String()+h.Stderr.String(), "created-routes-secret")
	call := api.FindCall("POST", "/v1/api_keys")
	h.Require.NotNil(call)
	h.Require.Equal(map[string]any{"type": "ROUTES", "name": "laptop", "team_id": "team-a"}, call.BodyJSON(t))
	h.Require.Equal([]string{"Bearer test-key"}, headers)
	store := configDirStore(t)
	saved, err := store.GetRoutesKey(auth.RoutesKeyScope{ManagementURL: api.URL, UserID: "user-a", TeamID: "team-a", Name: "laptop"})
	h.Require.NoError(err)
	h.Require.Equal("created-routes-secret", saved)
	h.Require.NoError(executeHarnessSetup(t, h, args...))
	h.Require.Len(headers, 1)
	h.Require.NoError(filepath.WalkDir(os.Getenv("BASETEN_CONFIG_DIR"), func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		h.Require.NotContains(string(data), "created-routes-secret")
		return err
	}))
}

func Test_Harness_Setup_CredentialsTeamNameReusesIDKey(t *testing.T) {
	h, api := productionHarness(t)
	h.Require.NoError(executeHarnessSetup(t, h, "--team", "Engineering", "--key-name", "laptop"))
	h.Require.Equal("team-a", api.FindCall("POST", "/v1/api_keys").BodyJSON(t)["team_id"])
	h.Require.NoError(executeHarnessSetup(t, h, "--team", "team-a", "--key-name", "laptop", "--output", "json"))
	count := 0
	for _, call := range api.Calls() {
		if call.Method == "POST" {
			count++
		}
	}
	h.Require.Equal(1, count)
}

func Test_Harness_Setup_CredentialsDryRunDoesNotCreate(t *testing.T) {
	h, api := productionHarness(t)
	h.Require.NoError(executeHarnessSetup(t, h, "--team", "team-a", "--key-name", "preview", "--output", "json", "--dry-run"))
	h.Require.Nil(api.FindCall("POST", "/v1/api_keys"))
	entries, err := os.ReadDir(os.Getenv("BASETEN_CONFIG_DIR"))
	h.Require.NoError(err)
	h.Require.Empty(entries)
}

func Test_Harness_Setup_CredentialsAutomaticTeamSelection(t *testing.T) {
	for _, count := range []int{0, 1, 2} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			h, api := productionHarness(t)
			teams := []map[string]string{}
			for i := 0; i < count; i++ {
				teams = append(teams, map[string]string{"id": fmt.Sprintf("team-%d", i), "name": fmt.Sprintf("Team %d", i)})
			}
			api.SetRoute("GET", "/v1/teams", 200, map[string]any{"teams": teams})
			err := executeHarnessSetup(t, h, "--key-name", "automatic", "--output", "json")
			if count == 1 {
				h.Require.NoError(err)
				h.Require.Equal("team-0", api.FindCall("POST", "/v1/api_keys").BodyJSON(t)["team_id"])
			} else {
				h.Require.Error(err)
				h.Require.Nil(api.FindCall("POST", "/v1/api_keys"))
				if count == 0 {
					h.Require.Contains(h.Stderr.String(), "no teams")
				} else {
					h.Require.Contains(h.Stderr.String(), "pass --team")
					h.Require.Contains(h.Stderr.String(), "Team 0 (team-0)")
					h.Require.Contains(h.Stderr.String(), "Team 1 (team-1)")
					h.Require.Contains(h.Stderr.String(), "baseten harness setup --team team-0")
					h.Require.Nil(api.FindCall("GET", "/v1/routes"))
				}
			}
		})
	}
}

func Test_Harness_Setup_CredentialsProfileAndIdentityIsolation(t *testing.T) {
	h, api := productionHarness(t)
	store := configDirStore(t)
	h.Require.NoError(store.SetAPIKeyProfile("alice-routes-test", "https://app.example.com", "alice-login", false, nil))
	args := []string{"--profile", "alice-routes-test", "--team", "team-a", "--key-name", "laptop"}
	var headers []string
	api.SetRouteFunc("POST", "/v1/api_keys", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		headers = append(headers, r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(`{"api_key":"routes-key"}`))
	})
	h.Require.NoError(executeHarnessSetup(t, h, args...))
	h.Require.Equal([]string{"Bearer alice-login"}, headers)
	api.SetRoute("GET", "/v1/users/me", 200, map[string]string{"user_id": "user-b"})
	h.Require.NoError(executeHarnessSetup(t, h, args...))
	h.Require.Len(headers, 2)
	h.Require.NoError(executeHarnessSetup(t, h, "--profile", "alice-routes-test", "--team", "team-b", "--key-name", "laptop"))
	h.Require.Len(headers, 3)
	profile, ok := store.GetProfile("alice-routes-test")
	h.Require.True(ok)
	h.Require.Equal(auth.AuthTypeAPIKey, profile.AuthType)
	original, err := store.GetAPIKey("alice-routes-test")
	h.Require.NoError(err)
	h.Require.Equal("alice-login", original)
}

func Test_Harness_Setup_CredentialsFailureRedactionAndRetry(t *testing.T) {
	for _, status := range []int{403, 500} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			h, api := productionHarness(t)
			api.SetRoute("POST", "/v1/api_keys", status, map[string]string{"api_key": "must-not-leak"})
			args := []string{"--team", "team-a", "--key-name", "failed"}
			h.Require.Error(executeHarnessSetup(t, h, args...))
			h.Require.NotContains(h.Stdout.String()+h.Stderr.String(), "must-not-leak")
			api.SetRoute("POST", "/v1/api_keys", 200, map[string]string{"api_key": "valid-key"})
			if status == 403 {
				h.Require.NoError(executeHarnessSetup(t, h, args...))
			} else {
				h.Require.Error(executeHarnessSetup(t, h, args...))
				h.Require.Contains(h.Stderr.String(), "unresolved")
				count := 0
				for _, call := range api.Calls() {
					if call.Method == "POST" {
						count++
					}
				}
				h.Require.Equal(1, count)
			}
		})
	}
}

func Test_Harness_Setup_CredentialsKeyringUnavailable(t *testing.T) {
	h, api := productionHarness(t)
	keyring.MockInitWithError(errors.New("keyring failure with sensitive details"))
	t.Cleanup(keyring.MockInit)
	h.Require.Error(executeHarnessSetup(t, h, "--team", "team-a"))
	h.Require.Nil(api.FindCall("POST", "/v1/api_keys"))
	h.Require.NotContains(h.Stderr.String(), "sensitive details")
}

func Test_Harness_Setup_CredentialsOAuthProfile(t *testing.T) {
	h, api := productionHarness(t)
	store := configDirStore(t)
	h.Require.NoError(store.SetOAuthProfile("oauth-routes-test", "https://app.example.com", auth.OAuthCredential{AccessToken: "oauth-access", RefreshToken: "oauth-refresh", Expiry: time.Now().Add(time.Hour)}, false, nil))
	var authorization string
	api.SetRouteFunc("POST", "/v1/api_keys", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		authorization = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"api_key":"routes-from-oauth"}`))
	})
	h.Require.NoError(executeHarnessSetup(t, h, "--profile", "oauth-routes-test", "--team", "team-a"))
	h.Require.Equal("Bearer oauth-access", authorization)
	h.Require.NotContains(h.Stdout.String()+h.Stderr.String(), "routes-from-oauth")
}

func Test_Harness_Setup_CredentialsReportsAPIValidationMessage(t *testing.T) {
	h, api := productionHarness(t)
	api.SetRoute("POST", "/v1/api_keys", 400, map[string]any{
		"code":    "VALIDATION_ERROR",
		"message": "routes keys require membership in the selected team.",
		"details": map[string]string{"api_key": "secret-in-details"},
		"api_key": "secret-in-body",
	})
	h.Require.Error(executeHarnessSetup(t, h, "--team", "Engineering"))
	h.Require.Contains(h.Stderr.String(), "HTTP 400")
	h.Require.Contains(h.Stderr.String(), "routes keys require membership in the selected team.")
	h.Require.NotContains(h.Stderr.String()+h.Stdout.String(), "secret-in-")
	api.SetRoute("POST", "/v1/api_keys", 400, map[string]string{"message": "Invalid credential test-key"})
	h.Require.Error(executeHarnessSetup(t, h, "--team", "Engineering"))
	h.Require.Contains(h.Stderr.String(), "[REDACTED]")
	h.Require.NotContains(h.Stderr.String(), "test-key")
}

func Test_Harness_Setup_CredentialsInvalidNameRejectedBeforeAPI(t *testing.T) {
	h, api := productionHarness(t)
	h.Require.Error(executeHarnessSetup(t, h, "--team", "Engineering", "--key-name", "My.Laptop"))
	h.Require.Contains(h.Stderr.String(), "lowercase letters, numbers, and hyphens")
	h.Require.Empty(api.Calls())
}

func Test_Harness_Setup_CredentialsStandardHTTPExitCodes(t *testing.T) {
	for _, tc := range []struct {
		status int
		code   public.ExitCode
		kind   string
	}{
		{401, public.ExitAuth, "ErrAuth"}, {403, public.ExitAuth, "ErrAuth"},
		{404, public.ExitNotFound, "ErrNotFound"}, {400, public.ExitValidation, "ErrValidation"},
		{422, public.ExitValidation, "ErrValidation"}, {500, public.ExitServer, "ErrServer"},
	} {
		t.Run(fmt.Sprint(tc.status), func(t *testing.T) {
			h, api := productionHarness(t)
			api.SetRoute("POST", "/v1/api_keys", tc.status, map[string]string{"message": "failure test-key"})
			h.Require.Error(executeHarnessSetup(t, h, "--team", "team-a", "--key-name", fmt.Sprintf("status-%d", tc.status), "--output", "json"))
			h.Require.Equal(int(tc.code), h.ExitCode)
			var envelope public.JSONErrorEnvelope
			h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &envelope))
			h.Require.Equal(tc.kind, envelope.Error.Type)
			h.Require.Equal(tc.code, envelope.Error.ExitCode)
			h.Require.NotContains(h.Stdout.String()+h.Stderr.String(), "test-key")
		})
	}
}

// Exercise credentials through the public setup path using isolated config files.
func executeHarnessSetup(t *testing.T, h *CommandHarness, flags ...string) error {
	t.Helper()
	args := []string{"harness", "setup", "--harness", "claude-code", "--route", "acme/primary", "--config", filepath.Join(t.TempDir(), "settings.json"), "--yes"}
	return h.Execute(append(args, flags...)...)
}

func Test_Harness_Setup_MultipleTeamsTextRequiresFlag(t *testing.T) {
	h, api := productionHarness(t)
	h.Require.Error(executeHarnessSetup(t, h))
	h.Require.Equal(int(public.ExitUsage), h.ExitCode)
	h.Require.Contains(h.Stderr.String(), "Engineering (team-a)")
	h.Require.Contains(h.Stderr.String(), "Research (team-b)")
	h.Require.Nil(api.FindCall("POST", "/v1/api_keys"))
	h.Require.Nil(api.FindCall("GET", "/v1/routes"))
}

func Test_Harness_Setup_TeamSwitchReusesPreviousKey(t *testing.T) {
	h, api := productionHarness(t)
	minted := []string{}
	api.SetRouteFunc("POST", "/v1/api_keys", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var body map[string]string
		h.Require.NoError(json.NewDecoder(r.Body).Decode(&body))
		minted = append(minted, body["team_id"])
		json.NewEncoder(w).Encode(map[string]string{"api_key": "secret-" + body["team_id"]})
	})
	path := filepath.Join(t.TempDir(), "settings.json")
	for _, team := range []string{"team-a", "team-b", "team-a"} {
		h.Require.NoError(h.Execute("harness", "setup", "--harness", "claude-code", "--route", "acme/primary", "--config", path, "--team", team, "--key-name", "laptop", "--yes"))
		data, err := os.ReadFile(path)
		h.Require.NoError(err)
		h.Require.Contains(string(data), "secret-"+team)
		h.Require.NotContains(h.Stdout.String()+h.Stderr.String(), "secret-")
	}
	h.Require.Equal([]string{"team-a", "team-b"}, minted)
	h.Require.Contains(h.Stdout.String(), "Reuse saved key")
	for _, team := range []string{"team-a", "team-b"} {
		key, err := configDirStore(t).GetRoutesKey(auth.RoutesKeyScope{ManagementURL: api.URL, UserID: "user-a", TeamID: team, Name: "laptop"})
		h.Require.NoError(err)
		h.Require.Equal("secret-"+team, key)
	}
}

func Test_Harness_Setup_SummaryTeamName(t *testing.T) {
	for _, team := range []string{"", "team-a", "Engineering"} {
		t.Run(team, func(t *testing.T) {
			h, api := productionHarness(t)
			api.SetRoute("GET", "/v1/teams", 200, map[string]any{"teams": []map[string]string{{"id": "team-a", "name": "Engineering"}}})
			args := []string{"--dry-run"}
			if team != "" {
				args = append(args, "--team", team)
			}
			h.Require.NoError(executeHarnessSetup(t, h, args...))
			h.Require.Contains(h.Stdout.String(), "Team: Engineering")
			h.Require.NotContains(h.Stdout.String(), "\x1b[")
			lookups := 0
			for _, call := range api.Calls() {
				if call.Method == "GET" && call.Path == "/v1/teams" {
					lookups++
				}
			}
			h.Require.Equal(1, lookups)
		})
	}
}

func Test_Harness_Setup_KeyUsesCLIUserAgent(t *testing.T) {
	h, api := productionHarness(t)
	var agent string
	api.SetRouteFunc("POST", "/v1/api_keys", func(w http.ResponseWriter, r *http.Request) {
		agent = r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"api_key": "created-routes-secret"})
	})
	h.Require.NoError(executeHarnessSetup(t, h, "--team", "team-a"))
	h.Require.Contains(agent, "baseten-cli/", "key creation should use the same client metadata as neighboring commands")
}

// All default paths are isolated: these tests must never discover the developer's config.
func isolateHarnessConfigs(t *testing.T) map[string]string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(dir, "claude"))
	t.Setenv("CODEX_HOME", filepath.Join(dir, "codex"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "xdg"))
	return map[string]string{
		"claude-code": filepath.Join(dir, "claude", "settings.json"),
		"codex":       filepath.Join(dir, "codex", "config.toml"),
		"opencode":    filepath.Join(dir, "xdg", "opencode", "opencode.json"),
	}
}

type missingHarnessExecer struct{}

func (missingHarnessExecer) LookPath(string) (string, error) { return "", exec.ErrNotFound }
func (missingHarnessExecer) Exec(*exec.Cmd) error {
	return fmt.Errorf("should not execute an absent harness")
}

func Test_Harness_MultipleSelectionAndDefaultDiscovery(t *testing.T) {
	h, api := productionHarness(t)
	paths := isolateHarnessConfigs(t)
	// Unmanaged, even malformed config must not be offered for teardown.
	h.Require.NoError(os.MkdirAll(filepath.Dir(paths["opencode"]), 0700))
	h.Require.NoError(os.WriteFile(paths["opencode"], []byte("not json"), 0600))
	args := []string{"harness", "setup", "--harness", "claude-code", "--harness", "codex", "--team", "team-a", "--yes", "--output", "json"}
	h.Require.NoError(h.Execute(args...))
	var setup public.HarnessPlansResult
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &setup))
	h.Require.Len(setup.Changes, 3) // Two configs and Codex's model catalog.
	h.Require.NotContains(h.Stdout.String(), "created-routes-secret")
	calls := len(api.Calls())
	// Detection no longer finds executables, but backups still identify integrations.
	h.Context = internalcmd.WithExecer(h.Context, missingHarnessExecer{})
	h.Require.NoError(h.Execute("harness", "status", "--output", "json"))
	var statuses public.HarnessStatusesResult
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &statuses))
	h.Require.Len(statuses.Harnesses, 2)
	for _, status := range statuses.Harnesses {
		h.Require.False(status.Installed)
		h.Require.Equal("configured", status.State)
	}
	h.Require.NoError(h.Execute("harness", "teardown", "--dry-run", "--output", "json"))
	var preview public.HarnessPlansResult
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &preview))
	h.Require.Len(preview.Changes, 3)
	h.Require.FileExists(paths["claude-code"])
	h.Require.FileExists(paths["codex"])
	h.Require.Error(h.Execute("harness", "teardown"))
	h.Require.FileExists(paths["claude-code"])
	// A filter only restores the chosen harness, and an omitted filter restores the remainder.
	h.Require.NoError(h.Execute("harness", "teardown", "--harness", "codex", "--yes", "--output", "json"))
	h.Require.NoFileExists(paths["codex"])
	h.Require.FileExists(paths["claude-code"])
	h.Require.NoError(h.Execute("harness", "teardown", "--yes", "--output", "json"))
	h.Require.NoFileExists(paths["claude-code"])
	h.Require.NoError(h.Execute("harness", "status", "--output", "json"))
	h.Require.JSONEq(`{"harnesses":[]}`, h.Stdout.String())
	h.Require.NoError(h.Execute("harness", "teardown", "--output", "json"))
	h.Require.JSONEq(`{"changes":[]}`, h.Stdout.String())
	h.Require.Equal(calls, len(api.Calls()), "status and teardown remain local")
	unmanaged, err := os.ReadFile(paths["opencode"])
	h.Require.NoError(err)
	h.Require.Equal("not json", string(unmanaged))
}

func Test_Harness_Setup_ReplacesWholePickerAndRestoresOriginal(t *testing.T) {
	h, _ := productionHarness(t)
	path := filepath.Join(t.TempDir(), "settings.json")
	original := `{"model":"previous","theme":"dark","availableModels":["my/model"],"modelPicker":{"options":[{"model":"my/model","label":"Custom"}],"replaceBuiltInOptions":false}}`
	h.Require.NoError(os.WriteFile(path, []byte(original), 0600))
	args := []string{"harness", "setup", "--harness", "claude-code", "--config", path, "--team", "team-a", "--yes"}
	h.Require.NoError(h.Execute(args...))
	read := func() map[string]any {
		b, err := os.ReadFile(path)
		h.Require.NoError(err)
		var d map[string]any
		h.Require.NoError(json.Unmarshal(b, &d))
		return d
	}
	data := read()
	picker := data["modelPicker"].(map[string]any)
	h.Require.Equal([]any{map[string]any{"model": "acme/primary", "label": "Primary"}}, picker["options"])
	h.Require.NotContains(data["availableModels"], "my/model")
	picker["options"] = []any{map[string]any{"model": "added/later", "label": "Later"}}
	data["theme"] = "light"
	b, err := json.Marshal(data)
	h.Require.NoError(err)
	h.Require.NoError(os.WriteFile(path, b, 0600))
	h.Require.NoError(h.Execute(args...))
	h.Require.Equal([]any{map[string]any{"model": "acme/primary", "label": "Primary"}}, read()["modelPicker"].(map[string]any)["options"])
	h.Require.NoError(h.Execute("harness", "teardown", "--harness", "claude-code", "--config", path, "--yes"))
	h.Require.Equal(map[string]any{"options": []any{map[string]any{"model": "my/model", "label": "Custom"}}, "replaceBuiltInOptions": false}, read()["modelPicker"])
	h.Require.Equal("previous", read()["model"])
	h.Require.Equal("light", read()["theme"])
	h.Require.Equal([]any{"my/model"}, read()["availableModels"])
}

func Test_Harness_Setup_ConfigAndInstallationErrorsPrecedeAPI(t *testing.T) {
	for _, args := range [][]string{
		{"--config", "some.json"},
		{"--config", "some.json", "--harness", "claude-code", "--harness", "opencode"},
		{"--harness", "claude-code"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			h, api := productionHarness(t)
			h.Context = internalcmd.WithExecer(h.Context, missingHarnessExecer{})
			h.Require.Error(h.Execute(append([]string{"harness", "setup", "--dry-run"}, args...)...))
			h.Require.Empty(api.Calls())
			if len(args) == 2 && args[0] == "--harness" {
				h.Require.Contains(h.Stderr.String(), "not installed or not on PATH")
				h.Require.NotEqual(int(public.ExitUsage), h.ExitCode)
			} else {
				h.Require.Contains(h.Stderr.String(), "--config requires exactly one explicit --harness")
			}
		})
	}
}

func Test_Harness_Setup_UnsupportedRouteFlagsNeverMint(t *testing.T) {
	for _, tc := range []struct{ name, flag string }{
		{"claude-code", "--background-route"}, {"codex", "--background-route"}, {"codex", "--subagent-route"}, {"codex", "--fallback-route"}, {"opencode", "--fallback-route"},
	} {
		t.Run(tc.name+tc.flag, func(t *testing.T) {
			h, api := productionHarness(t)
			filename := "settings.json"
			if tc.name == "codex" {
				filename = "config.toml"
			}
			path := filepath.Join(t.TempDir(), filename)
			h.Require.Error(h.Execute("harness", "setup", "--harness", tc.name, "--config", path, "--team", "team-a", tc.flag, "acme/primary", "--yes"))
			h.Require.Contains(h.Stderr.String(), "supported only")
			h.Require.Nil(api.FindCall("POST", "/v1/api_keys"))
			h.Require.NoFileExists(path)
		})
	}
}

func Test_Harness_Setup_DryRunUsesNormalOAuthRefresh(t *testing.T) {
	h, api := productionHarness(t)
	store := configDirStore(t)
	h.Require.NoError(store.SetOAuthProfile("preview-oauth", "https://app.example.com", auth.OAuthCredential{AccessToken: "expired", RefreshToken: "refresh", Expiry: time.Now().Add(-time.Hour)}, false, nil))
	api.SetRoute("POST", "/v1/users/auth/device/token", 200, map[string]any{"access_token": "refreshed", "refresh_token": "next-refresh", "token_type": "Bearer", "expires_in": 3600})
	path := filepath.Join(t.TempDir(), "settings.json")
	h.Require.NoError(h.Execute("harness", "setup", "--harness", "claude-code", "--config", path, "--profile", "preview-oauth", "--team", "team-a", "--dry-run", "--output", "json"))
	saved, err := store.GetOAuthCredential("preview-oauth")
	h.Require.NoError(err)
	h.Require.Equal("refreshed", saved.AccessToken)
	h.Require.NoFileExists(path)
	h.Require.NoFileExists(path + ".baseten-harness.json")
	h.Require.Nil(api.FindCall("POST", "/v1/api_keys"))
	h.Require.NotContains(h.Stdout.String()+h.Stderr.String(), "refreshed")
	h.Require.NoError(h.Execute("harness", "setup", "--help"))
	h.Require.Contains(h.Stdout.String(), "OAuth refresh")
}

func Test_Harness_Setup_DefaultAPIKeyName(t *testing.T) {
	h, api := productionHarness(t)
	h.Require.NoError(executeHarnessSetup(t, h, "--team", "team-a"))
	name := api.FindCall("POST", "/v1/api_keys").BodyJSON(t)["name"].(string)
	h.Require.Regexp(`^baseten-harness-[a-z0-9]+(-[a-z0-9]+)*$`, name)
}
