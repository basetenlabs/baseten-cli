package cmd_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
	public "github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-cli/internal/auth"
	internalcmd "github.com/basetenlabs/baseten-cli/internal/cmd"
	"github.com/basetenlabs/baseten-cli/internal/harness"
	"github.com/zalando/go-keyring"
)

// harnessRouteMetadata is usable model metadata for route fixtures.
var harnessRouteMetadata = map[string]any{
	"context_window": 128000, "max_output_tokens": 4096, "input_modalities": []string{"text"}, "tools": true,
	"supported_api_formats": map[string]any{"messages": true, "responses": true, "chat_completions": true},
}

type fakeHarnessExecer struct{ binary string }

func (e fakeHarnessExecer) LookPath(name string) (string, error) {
	if e.binary != "" {
		if name != e.binary {
			return "", exec.ErrNotFound
		}
		return name, nil
	}
	return "/fake/" + name, nil
}

func (fakeHarnessExecer) Exec(command *exec.Cmd) error {
	_, err := fmt.Fprintln(command.Stdout, "1.2.3")
	return err
}

type missingHarnessExecer struct{}

func (missingHarnessExecer) LookPath(string) (string, error) { return "", exec.ErrNotFound }

func (missingHarnessExecer) Exec(*exec.Cmd) error { return errors.New("absent harness executed") }

// harnessAPI serves a user, two teams (team-a is the default), one route whose
// invoke URL is invokeURL, and routes API key creation for both teams.
func harnessAPI(t *testing.T, invokeURL string) (*CommandHarness, *MockManagementAPI) {
	t.Helper()
	h := NewCommandHarness(t)
	api := h.MockManagementAPI()
	if invokeURL == "" {
		invokeURL = api.URL
	}
	api.SetRoute("GET", "/v1/users/me", 200, map[string]string{"user_id": "user-a"})
	api.SetRoute("GET", "/v1/teams", 200, map[string]any{"teams": []map[string]any{
		{"id": "team-a", "name": "Engineering", "default": true},
		{"id": "team-b", "name": "Research", "default": false},
	}})
	api.SetRouteFunc("GET", "/v1/routes", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"items":      []any{map[string]any{"id": "route-a", "name": "acme/primary", "team_id": r.URL.Query().Get("team_id"), "display_name": "Primary", "invoke_url": invokeURL, "metadata": harnessRouteMetadata}},
			"pagination": map[string]any{"has_more": false},
		})
	})
	for _, team := range []string{"team-a", "team-b"} {
		api.SetRoute("POST", "/v1/teams/"+team+"/api_keys", 200, map[string]string{"api_key": "secret-" + team})
		api.SetRoute("DELETE", "/v1/api_keys/secret-"+team, 200, map[string]string{"prefix": "secret-" + team})
	}
	api.SetRoute("GET", "/v1/api_keys", 200, map[string]any{"keys": []any{
		map[string]any{"prefix": "secret-team-a", "type": "ROUTES", "name": "laptop", "team_name": "Engineering", "created_at": "2026-09-01T00:00:00Z"},
		map[string]any{"prefix": "secret-team-b", "type": "ROUTES", "name": "laptop", "team_name": "Research", "created_at": "2026-09-02T00:00:00Z"},
	}})
	return h, api
}

// fakeHarnessAPI is harnessAPI with every harness reported as installed.
func fakeHarnessAPI(t *testing.T) (*CommandHarness, *MockManagementAPI) {
	t.Helper()
	h, api := harnessAPI(t, "")
	h.Context = internalcmd.WithExecer(h.Context, fakeHarnessExecer{})
	return h, api
}

func harnessSettingsFile(name, dir string) string {
	switch name {
	case "codex":
		return filepath.Join(dir, "config.toml")
	case "opencode":
		return filepath.Join(dir, "opencode.json")
	case "pi":
		return filepath.Join(dir, "models.json")
	default:
		return filepath.Join(dir, "settings.json")
	}
}

func readHarnessSettings(t *testing.T, name, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var data map[string]any
	if name == "codex" {
		err = toml.Unmarshal(raw, &data)
	} else {
		err = json.Unmarshal(raw, &data)
	}
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func catalogSlugs(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var catalog struct {
		Models []struct {
			Slug string `json:"slug"`
		} `json:"models"`
	}
	if err := json.Unmarshal(raw, &catalog); err != nil {
		t.Fatal(err)
	}
	slugs := make([]string, 0, len(catalog.Models))
	for _, m := range catalog.Models {
		slugs = append(slugs, m.Slug)
	}
	return slugs
}

func countCalls(api *MockManagementAPI, method, path string) int {
	count := 0
	for _, call := range api.Calls() {
		if call.Method == method && call.Path == path {
			count++
		}
	}
	return count
}

// skipUnlessSupported skips tests of harness commands, which support only macOS and Linux.
func skipUnlessSupported(t *testing.T) {
	t.Helper()
	if !harness.Supported() {
		t.Skip("harness commands support only macOS and Linux")
	}
}

func Test_HarnessCommands_RejectOtherPlatforms(t *testing.T) {
	if harness.Supported() {
		t.Skip("harness commands support this platform")
	}
	for _, command := range []string{"setup", "status", "teardown"} {
		h := NewCommandHarness(t)
		h.Require.Error(h.Execute("harness", command, "--harness", "codex"))
		h.Require.Contains(h.Stderr.String(), "harness commands support only macOS and Linux for now")
	}
}

func Test_Harness_Setup_Lifecycle(t *testing.T) {
	skipUnlessSupported(t)
	for _, name := range []string{"claude-code", "codex", "opencode", "pi"} {
		t.Run(name, func(t *testing.T) {
			h, api := fakeHarnessAPI(t)
			dir := t.TempDir()
			path := harnessSettingsFile(name, dir)
			h.Require.NoError(os.WriteFile(path, []byte(`{"theme":"dark"}`), 0o600))
			if name == "codex" {
				h.Require.NoError(os.WriteFile(path, []byte("theme = \"dark\"\n"), 0o600))
			}
			args := []string{"harness", "setup", "--harness", name, "--config-dir", dir, "--key-name", "laptop", "--output", "json"}

			h.Require.NoError(h.Execute(append(args, "--dry-run")...))
			var preview public.HarnessPlanList
			h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &preview))
			h.Require.NotEmpty(preview.Items)
			h.Require.Equal(map[string]any{"theme": "dark"}, readHarnessSettings(t, name, path))
			h.Require.Equal(0, countCalls(api, "POST", "/v1/teams/team-a/api_keys"))
			h.Require.Nil(api.FindCall("GET", "/v1/users/me"), "dry run skips the routes API key")

			h.Require.Error(h.Execute(args...))
			h.Require.Contains(h.Stderr.String(), "pass --yes")
			h.Require.Equal(0, countCalls(api, "POST", "/v1/teams/team-a/api_keys"))

			h.Require.NoError(h.Execute(append(args, "--yes")...))
			h.Require.NotContains(h.Stdout.String()+h.Stderr.String(), "secret-team-a")
			settings, err := os.ReadFile(path)
			h.Require.NoError(err)
			h.Require.Contains(string(settings), "secret-team-a")
			h.Require.Contains(string(settings), "acme/primary")
			h.Require.Equal("dark", readHarnessSettings(t, name, path)["theme"])
			if name == "codex" {
				h.Require.Equal("api", readHarnessSettings(t, name, path)["forced_login_method"])
			}

			h.Require.NoError(h.Execute(append(args, "--yes")...))
			var rerun public.HarnessPlanList
			h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &rerun))
			for _, item := range rerun.Items {
				h.Require.False(item.Changed, "rerunning setup changes nothing")
			}
			h.Require.Equal(1, countCalls(api, "POST", "/v1/teams/team-a/api_keys"), "the saved key is reused")
			h.Require.NoError(h.Execute(append(args, "--dry-run")...))
			h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &rerun))
			for _, item := range rerun.Items {
				h.Require.False(item.Changed, "a dry run keeps the configured key")
				h.Require.Empty(item.ReplacedSettings)
			}

			calls := len(api.Calls())
			h.Require.NoError(h.Execute("harness", "status", "--harness", name, "--config-dir", dir, "--output", "json"))
			var statuses public.HarnessStatusList
			h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &statuses))
			h.Require.Len(statuses.Items, 1)
			h.Require.Equal("configured", statuses.Items[0].State)
			h.Require.Equal("acme/primary", statuses.Items[0].DefaultRoute)
			h.Require.Equal([]public.HarnessRoute{{Name: "acme/primary", DisplayName: "Primary"}}, statuses.Items[0].Routes)
			if name == "codex" {
				h.Require.Contains(statuses.Items[0].ManagedSettings, "forced_login_method")
			}
			h.Require.NotContains(h.Stdout.String(), "secret-team-a")

			h.Require.NoError(h.Execute("harness", "teardown", "--harness", name, "--config-dir", dir, "--dry-run"))
			h.Require.Contains(h.Stdout.String(), "Remove Baseten settings from "+name)
			h.Require.Contains(h.Stdout.String(), "Also delete the routes API key")
			h.Require.Equal(calls, len(api.Calls()), "status and teardown previews are local")
			unchanged, err := os.ReadFile(path)
			h.Require.NoError(err)
			h.Require.Equal(settings, unchanged, "teardown --dry-run changes nothing")
			h.Require.Error(h.Execute("harness", "teardown", "--harness", name, "--config-dir", dir))
			h.Require.Contains(h.Stderr.String(), "pass --yes")
			h.Require.NoError(h.Execute("harness", "teardown", "--harness", name, "--config-dir", dir, "--yes"))
			h.Require.Equal(map[string]any{"theme": "dark"}, readHarnessSettings(t, name, path))
			h.Require.NoFileExists(filepath.Join(dir, "baseten-models.json"))
			h.Require.Equal(1, countCalls(api, "DELETE", "/v1/api_keys/secret-team-a"), "teardown deletes the unused key")
			key, err := configDirStore(t).GetRoutesKey(auth.RoutesKeyScope{ManagementURL: api.URL, UserID: "user-a", TeamID: "team-a", Name: "laptop"})
			h.Require.NoError(err)
			h.Require.Empty(key, "teardown forgets the deleted key")

			h.Require.NoError(h.Execute("harness", "status", "--harness", name, "--config-dir", dir, "--output", "json"))
			h.Require.Contains(h.Stdout.String(), `"state": "not-configured"`)
		})
	}
}

func Test_Harness_Setup_CodexDesktopWithoutCLI(t *testing.T) {
	skipUnlessSupported(t)
	binary := "/Applications/ChatGPT.app/Contents/Resources/codex"
	if runtime.GOOS == "darwin" {
		binary = "/Applications/ChatGPT.app/Contents/Resources/codex-cli/CodexCLI.app/Contents/MacOS/codex"
	}
	if runtime.GOOS == "linux" {
		binary = "/usr/lib/chatgpt/resources/codex"
	}
	h, _ := harnessAPI(t, "")
	h.Context = internalcmd.WithExecer(h.Context, fakeHarnessExecer{binary: binary})
	dir := t.TempDir()
	t.Setenv("CODEX_HOME", dir)
	h.Require.NoError(h.Execute("harness", "setup", "--harness", "codex", "--yes"))
	settings := readHarnessSettings(t, "codex", filepath.Join(dir, "config.toml"))
	h.Require.Equal("baseten-harness", settings["model_provider"])
	h.Require.Equal("acme/primary", settings["model"])
	h.Require.NoError(h.Execute("harness", "status", "--harness", "codex", "--output", "json"))
	var statuses public.HarnessStatusList
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &statuses))
	h.Require.Len(statuses.Items, 1)
	h.Require.True(statuses.Items[0].Installed)
	h.Require.Equal("configured", statuses.Items[0].State)
}

func Test_Harness_Setup_TextSummary(t *testing.T) {
	skipUnlessSupported(t)
	h, _ := fakeHarnessAPI(t)
	dir := t.TempDir()
	h.Require.NoError(h.Execute("harness", "setup", "--harness", "claude-code", "--config-dir", dir, "--yes"))
	h.Require.Empty(h.Stdout.String())
	h.Require.Contains(h.Stderr.String(), "Created routes API key baseten-harness-")
	h.Require.Contains(h.Stderr.String(), "Configuration saved.")
	h.Require.Contains(h.Stderr.String(), "Undo with: baseten harness teardown --harness claude-code --config-dir '"+dir+"'")
	h.Require.NoError(h.Execute("harness", "setup", "--harness", "claude-code", "--config-dir", dir, "--yes", "--verbose"))
	for _, want := range []string{"Team: Engineering", "Default route     acme/primary", "Background route  deepseek-ai/DeepSeek-V4.1-Flash", "Already configured"} {
		h.Require.Contains(h.Stdout.String(), want)
	}
	h.Require.NotContains(h.Stdout.String(), "Available routes")
	h.Require.NotContains(h.Stderr.String(), "Created routes API key")
}

func Test_Harness_Setup_RequiresHarnessWhenNotInteractive(t *testing.T) {
	skipUnlessSupported(t)
	h := NewCommandHarness(t)
	h.Require.Error(h.Execute("harness", "setup"))
	h.Require.Equal(int(public.ExitUsage), h.ExitCode)
	h.Require.Contains(h.Stderr.String(), "pass --harness")
}

func Test_Harness_ConfigDirRequiresOneHarness(t *testing.T) {
	skipUnlessSupported(t)
	for _, command := range []string{"setup", "status", "teardown"} {
		for _, harnesses := range [][]string{nil, {"--harness", "codex", "--harness", "opencode"}} {
			t.Run(fmt.Sprint(command, len(harnesses)), func(t *testing.T) {
				h := NewCommandHarness(t)
				h.Require.Error(h.Execute(append([]string{"harness", command, "--config-dir", t.TempDir()}, harnesses...)...))
				h.Require.Equal(int(public.ExitUsage), h.ExitCode)
				h.Require.Contains(h.Stderr.String(), "--config-dir requires exactly one --harness")
			})
		}
	}
}

func Test_Harness_Setup_NotInstalledFailsBeforeAPI(t *testing.T) {
	skipUnlessSupported(t)
	h, api := fakeHarnessAPI(t)
	h.Context = internalcmd.WithExecer(h.Context, missingHarnessExecer{})
	h.Require.Error(h.Execute("harness", "setup", "--harness", "codex", "--dry-run"))
	want := "codex is not installed or not on PATH"
	switch runtime.GOOS {
	case "darwin":
		want = "codex is not installed or not on PATH or in ChatGPT.app or Codex.app"
	case "linux":
		want = "codex is not installed or not on PATH or in /usr/lib/chatgpt"
	}
	h.Require.Contains(h.Stderr.String(), want)
	h.Require.Empty(api.Calls())
}

func Test_Harness_Setup_Teams(t *testing.T) {
	skipUnlessSupported(t)
	for _, tc := range []struct{ team, want string }{{"", "team-a"}, {"team-b", "team-b"}, {"Research", "team-b"}} {
		t.Run(tc.team, func(t *testing.T) {
			h, api := fakeHarnessAPI(t)
			args := []string{"harness", "setup", "--harness", "claude-code", "--config-dir", t.TempDir(), "--yes"}
			if tc.team != "" {
				args = append(args, "--team", tc.team)
			}
			h.Require.NoError(h.Execute(args...))
			h.Require.Equal([]string{tc.want}, api.FindCall("GET", "/v1/routes").Query()["team_id"])
			h.Require.Equal(1, countCalls(api, "POST", "/v1/teams/"+tc.want+"/api_keys"))
		})
	}
	h, api := fakeHarnessAPI(t)
	api.SetRoute("GET", "/v1/teams", 200, map[string]any{"teams": []map[string]any{{"id": "team-b", "name": "Research"}}})
	h.Require.Error(h.Execute("harness", "setup", "--harness", "claude-code", "--config-dir", t.TempDir(), "--yes"))
	h.Require.Contains(h.Stderr.String(), "no default team; pass --team")
}

func Test_Harness_Setup_KeyPerTeamAndProfile(t *testing.T) {
	skipUnlessSupported(t)
	h, api := fakeHarnessAPI(t)
	dir := t.TempDir()
	path := harnessSettingsFile("claude-code", dir)
	for _, team := range []string{"team-a", "team-b", "team-a"} {
		h.Require.NoError(h.Execute("harness", "setup", "--harness", "claude-code", "--config-dir", dir, "--team", team, "--key-name", "laptop", "--yes"))
		settings, err := os.ReadFile(path)
		h.Require.NoError(err)
		h.Require.Contains(string(settings), "secret-"+team)
	}
	h.Require.Equal(1, countCalls(api, "POST", "/v1/teams/team-a/api_keys"))
	h.Require.Equal(1, countCalls(api, "POST", "/v1/teams/team-b/api_keys"))
	h.Require.Equal(map[string]any{"type": "ROUTES", "name": "laptop"}, api.FindCall("POST", "/v1/teams/team-a/api_keys").BodyJSON(t))
	key, err := configDirStore(t).GetRoutesKey(auth.RoutesKeyScope{ManagementURL: api.URL, UserID: "user-a", TeamID: "team-b", Name: "laptop"})
	h.Require.NoError(err)
	h.Require.Equal("secret-team-b", key)

	// Another user of the same profile gets their own key.
	api.SetRoute("GET", "/v1/users/me", 200, map[string]string{"user_id": "user-b"})
	h.Require.NoError(h.Execute("harness", "setup", "--harness", "claude-code", "--config-dir", dir, "--key-name", "laptop", "--yes"))
	h.Require.Equal(2, countCalls(api, "POST", "/v1/teams/team-a/api_keys"))
}

func Test_Harness_Setup_DefaultKeyName(t *testing.T) {
	skipUnlessSupported(t)
	h, api := fakeHarnessAPI(t)
	h.Require.NoError(h.Execute("harness", "setup", "--harness", "claude-code", "--config-dir", t.TempDir(), "--yes"))
	name := api.FindCall("POST", "/v1/teams/team-a/api_keys").BodyJSON(t)["name"].(string)
	h.Require.Regexp(`^baseten-harness-[a-z0-9]+(-[a-z0-9]+)*$`, name)

	// A key made under another profile, or whose saved copy is gone, holds the default name.
	h, api = fakeHarnessAPI(t)
	keys := []any{map[string]any{"prefix": "elsewhere", "type": "ROUTES", "name": name, "team_name": "Engineering", "created_at": "2026-09-01T00:00:00Z"}}
	api.SetRoute("GET", "/v1/api_keys", 200, map[string]any{"keys": keys})
	args := []string{"harness", "setup", "--harness", "claude-code", "--config-dir", t.TempDir(), "--yes"}
	h.Require.NoError(h.Execute(args...))
	h.Require.Equal(map[string]any{"type": "ROUTES", "name": name + "-2"}, api.FindCall("POST", "/v1/teams/team-a/api_keys").BodyJSON(t))

	keys = append(keys, map[string]any{"prefix": "secret-team-a", "type": "ROUTES", "name": name + "-2", "team_name": "Engineering", "created_at": "2026-09-02T00:00:00Z"})
	api.SetRoute("GET", "/v1/api_keys", 200, map[string]any{"keys": keys})
	h.Require.NoError(h.Execute(args...))
	h.Require.Equal(1, countCalls(api, "POST", "/v1/teams/team-a/api_keys"), "a rerun reuses the numbered key")

	// The saved numbered key still wins once the default name is free.
	api.SetRoute("GET", "/v1/api_keys", 200, map[string]any{"keys": keys[1:]})
	h.Require.NoError(h.Execute(args...))
	h.Require.Equal(1, countCalls(api, "POST", "/v1/teams/team-a/api_keys"))
}

func Test_Harness_Setup_KeyCreationFailureCanBeRetried(t *testing.T) {
	skipUnlessSupported(t)
	h, api := fakeHarnessAPI(t)
	dir := t.TempDir()
	args := []string{"harness", "setup", "--harness", "claude-code", "--config-dir", dir, "--yes"}
	api.SetRoute("POST", "/v1/teams/team-a/api_keys", 500, map[string]string{"message": "unavailable"})
	h.Require.Error(h.Execute(args...))
	h.Require.Contains(h.Stderr.String(), "creating routes API key")
	h.Require.NoFileExists(harnessSettingsFile("claude-code", dir))
	api.SetRoute("POST", "/v1/teams/team-a/api_keys", 200, map[string]string{"api_key": "secret-team-a"})
	h.Require.NoError(h.Execute(args...))
	h.Require.FileExists(harnessSettingsFile("claude-code", dir))
}

func Test_Harness_Setup_KeyringUnavailableStoresPlaintext(t *testing.T) {
	skipUnlessSupported(t)
	h, api := fakeHarnessAPI(t)
	keyring.MockInitWithError(errors.New("no keyring"))
	t.Cleanup(keyring.MockInit)
	args := []string{"harness", "setup", "--harness", "claude-code", "--config-dir", t.TempDir(), "--yes"}
	h.Require.NoError(h.Execute(args...))
	h.Require.Contains(h.Stderr.String(), "warning: could not store routes API key in system keyring")
	h.Require.NoError(h.Execute(args...))
	h.Require.Equal(1, countCalls(api, "POST", "/v1/teams/team-a/api_keys"))
}

func Test_Harness_Setup_RouteFlags(t *testing.T) {
	skipUnlessSupported(t)
	for _, tc := range []struct{ name, flag string }{
		{"codex", "--background-route"}, {"codex", "--subagent-route"}, {"codex", "--fallback-route"}, {"opencode", "--fallback-route"},
		{"pi", "--background-route"}, {"pi", "--subagent-route"}, {"pi", "--fallback-route"},
	} {
		t.Run(tc.name+tc.flag, func(t *testing.T) {
			h, api := fakeHarnessAPI(t)
			dir := t.TempDir()
			h.Require.Error(h.Execute("harness", "setup", "--harness", tc.name, "--config-dir", dir, tc.flag, "acme/primary", "--yes"))
			h.Require.Contains(h.Stderr.String(), "supported only")
			h.Require.Equal(0, countCalls(api, "POST", "/v1/teams/team-a/api_keys"))
			h.Require.NoFileExists(harnessSettingsFile(tc.name, dir))
		})
	}
	h, _ := fakeHarnessAPI(t)
	dir := t.TempDir()
	h.Require.NoError(h.Execute("harness", "setup", "--harness", "claude-code", "--config-dir", dir, "--background-route", "acme/primary", "--yes"))
	env := readHarnessSettings(t, "claude-code", harnessSettingsFile("claude-code", dir))["env"].(map[string]any)
	h.Require.Equal("acme/primary", env["ANTHROPIC_DEFAULT_HAIKU_MODEL"])
}

func Test_Harness_Setup_ReplacesSettingsAndPicker(t *testing.T) {
	skipUnlessSupported(t)
	h, _ := fakeHarnessAPI(t)
	dir := t.TempDir()
	path := harnessSettingsFile("claude-code", dir)
	h.Require.NoError(os.WriteFile(path, []byte(`{"model":"previous","theme":"dark","modelPicker":{"options":[{"model":"my/model","label":"Custom"}]}}`), 0o600))
	args := []string{"harness", "setup", "--harness", "claude-code", "--config-dir", dir}
	h.Require.NoError(h.Execute(append(args, "--dry-run", "--output", "json")...))
	var preview public.HarnessPlanList
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &preview))
	h.Require.Contains(preview.Items[0].ReplacedSettings, "model")
	h.Require.Contains(preview.Items[0].ReplacedSettings, "modelPicker.options")
	h.Require.NoError(h.Execute(append(args, "--dry-run")...))
	h.Require.Contains(h.Stdout.String(), "Existing integration settings will be overwritten")

	h.Require.NoError(h.Execute(append(args, "--yes")...))
	h.Require.Contains(h.Stderr.String(), "existing integration settings in claude-code will be overwritten")
	picker := readHarnessSettings(t, "claude-code", path)["modelPicker"].(map[string]any)
	h.Require.Equal([]any{map[string]any{"model": "acme/primary", "label": "Primary"}}, picker["options"])
	h.Require.NoError(h.Execute("harness", "teardown", "--harness", "claude-code", "--config-dir", dir, "--yes"))
	h.Require.Equal(map[string]any{"theme": "dark"}, readHarnessSettings(t, "claude-code", path))
}

func Test_Harness_DefaultDiscovery(t *testing.T) {
	skipUnlessSupported(t)
	h, api := fakeHarnessAPI(t)
	root := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(root, "claude"))
	t.Setenv("CODEX_HOME", filepath.Join(root, "codex"))
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(root, "pi"))
	h.Require.NoError(h.Execute("harness", "setup", "--harness", "claude-code", "--harness", "codex", "--yes", "--output", "json"))
	var setup public.HarnessPlanList
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &setup))
	h.Require.Len(setup.Items, 3) // Two settings files and the Codex model catalog.
	// A malformed file warns instead of hiding the other harnesses.
	openCode := filepath.Join(root, "opencode", "opencode.json")
	h.Require.NoError(os.MkdirAll(filepath.Dir(openCode), 0o700))
	h.Require.NoError(os.WriteFile(openCode, []byte("{ not json"), 0o600))
	calls := len(api.Calls())

	// Status and teardown find integrations whose executables are gone.
	h.Context = internalcmd.WithExecer(h.Context, missingHarnessExecer{})
	h.Require.NoError(h.Execute("harness", "status", "--output", "json"))
	h.Require.Contains(h.Stderr.String(), "warning: skipping opencode: settings at")
	h.Require.Contains(h.Stderr.String(), "are misconfigured")
	var statuses public.HarnessStatusList
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &statuses))
	h.Require.Len(statuses.Items, 2)
	for _, status := range statuses.Items {
		h.Require.False(status.Installed)
		h.Require.Equal("configured", status.State)
	}
	h.Require.Error(h.Execute("harness", "status", "--harness", "opencode"))
	h.Require.Contains(h.Stderr.String(), "invalid settings JSON")

	h.Require.Equal(calls, len(api.Calls()), "status is local")

	// Claude Code still uses the key, so removing Codex keeps it.
	h.Require.NoError(h.Execute("harness", "teardown", "--harness", "codex", "--yes"))
	h.Require.Equal(0, countCalls(api, "DELETE", "/v1/api_keys/secret-team-a"))
	h.Require.NoError(h.Execute("harness", "status", "--output", "json"))
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &statuses))
	h.Require.Len(statuses.Items, 1)
	// An unreadable file might still use the key, so teardown keeps it.
	h.Require.NoError(h.Execute("harness", "teardown", "--yes"))
	h.Require.Contains(h.Stderr.String(), "warning: keeping the routes API key")
	h.Require.Equal(0, countCalls(api, "DELETE", "/v1/api_keys/secret-team-a"))
	h.Require.NoError(h.Execute("harness", "status"))
	h.Require.Contains(h.Stderr.String(), "No configured Baseten harnesses found.")
	h.Require.NoError(h.Execute("harness", "teardown", "--output", "json"))
	h.Require.JSONEq(`{"items":[]}`, h.Stdout.String())
}

func Test_Harness_Teardown_DeletesKeyWithLastHarness(t *testing.T) {
	skipUnlessSupported(t)
	h, api := fakeHarnessAPI(t)
	root := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(root, "claude"))
	t.Setenv("CODEX_HOME", filepath.Join(root, "codex"))
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(root, "pi"))
	h.Require.NoError(h.Execute("harness", "setup", "--harness", "claude-code", "--harness", "opencode", "--yes"))
	h.Require.NoError(h.Execute("harness", "teardown", "--harness", "opencode", "--yes"))
	h.Require.Equal(0, countCalls(api, "DELETE", "/v1/api_keys/secret-team-a"), "Claude Code still uses the key")
	h.Require.NoError(h.Execute("harness", "teardown", "--yes", "--output", "json"))
	var result public.HarnessPlanList
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &result))
	h.Require.Equal([]string{"secret-team-a"}, result.DeletedAPIKeys)
	h.Require.Equal(1, countCalls(api, "DELETE", "/v1/api_keys/secret-team-a"))

	// A failed delete leaves the settings in place so teardown can be retried.
	h.Require.NoError(h.Execute("harness", "setup", "--harness", "codex", "--yes"))
	api.SetRoute("DELETE", "/v1/api_keys/secret-team-a", 500, map[string]string{"message": "unavailable"})
	h.Require.Error(h.Execute("harness", "teardown", "--yes"))
	h.Require.Contains(h.Stderr.String(), "deleting routes API key secret-team-a")
	h.Require.NoError(h.Execute("harness", "status", "--harness", "codex", "--output", "json"))
	h.Require.Contains(h.Stdout.String(), `"state": "configured"`)
}

// harnessGateway records the model and client each inference request names and
// refuses it.
type harnessGateway struct {
	mu             sync.Mutex
	models         []string
	clients        []string
	requestClasses []string
}

func (g *harnessGateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Model string `json:"model"`
	}
	raw, _ := io.ReadAll(r.Body)
	if json.Unmarshal(raw, &body) == nil && body.Model != "" {
		g.mu.Lock()
		g.models = append(g.models, body.Model)
		g.clients = append(g.clients, r.Header.Get("X-Baseten-Client"))
		g.requestClasses = append(g.requestClasses, r.Header.Get("x-claude-code-request-class"))
		g.mu.Unlock()
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_, _ = w.Write([]byte(`{"type":"error","error":{"type":"invalid_request_error","message":"harness test gateway"}}`))
}

func (g *harnessGateway) requested() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return slices.Clone(g.models)
}

func (g *harnessGateway) saw(model string) bool { return slices.Contains(g.requested(), model) }

func (g *harnessGateway) sentClients() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return slices.Clone(g.clients)
}

func (g *harnessGateway) sentRequestClasses() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return slices.Clone(g.requestClasses)
}

// Test_Harness_Setup_RealHarness configures each installed harness in an
// isolated directory, runs it, and checks that it asks the gateway for the
// configured route. Harnesses not on PATH are skipped.
func Test_Harness_Setup_RealHarness(t *testing.T) {
	skipUnlessSupported(t)
	for _, tc := range []struct {
		name, binary string
		args         []string
	}{
		{"claude-code", "claude", []string{"-p", "Reply with OK."}},
		{"codex", "codex", []string{"exec", "--skip-git-repo-check", "Reply with OK."}},
		{"opencode", "opencode", []string{"run", "Reply with OK."}},
		{"pi", "pi", []string{"--print", "Reply with OK."}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			binary, err := exec.LookPath(tc.binary)
			if err != nil {
				t.Skipf("%s is not on PATH", tc.binary)
			}
			gateway := &harnessGateway{}
			server := httptest.NewServer(gateway)
			t.Cleanup(server.Close)
			h, _ := harnessAPI(t, server.URL)
			root := t.TempDir()
			configDir := map[string]string{
				"claude-code": filepath.Join(root, ".claude"),
				"codex":       filepath.Join(root, ".codex"),
				"opencode":    filepath.Join(root, ".config", "opencode"),
				"pi":          filepath.Join(root, ".pi", "agent"),
			}[tc.name]
			h.Require.NoError(h.Execute("harness", "setup", "--harness", tc.name, "--config-dir", configDir, "--yes"))

			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
			defer cancel()
			run := exec.CommandContext(ctx, binary, tc.args...)
			run.Dir = t.TempDir()
			// A minimal environment: anything inherited could point the harness at
			// a real provider and make the test pass for the wrong reason.
			run.Env = []string{
				"HOME=" + root, "USERPROFILE=" + root,
				"CLAUDE_CONFIG_DIR=" + configDir, "CODEX_HOME=" + configDir, "PI_CODING_AGENT_DIR=" + configDir,
				"XDG_CONFIG_HOME=" + filepath.Join(root, ".config"),
				"XDG_DATA_HOME=" + filepath.Join(root, ".local", "share"),
				"XDG_STATE_HOME=" + filepath.Join(root, ".local", "state"),
				"XDG_CACHE_HOME=" + filepath.Join(root, ".cache"),
				"APPDATA=" + filepath.Join(root, "AppData", "Roaming"),
				"LOCALAPPDATA=" + filepath.Join(root, "AppData", "Local"),
				"TMPDIR=" + t.TempDir(),
			}
			// Windows needs these to start processes and find Git Bash.
			for _, name := range []string{"PATH", "PATHEXT", "SYSTEMROOT", "SYSTEMDRIVE", "WINDIR", "COMSPEC", "TEMP", "TMP", "PROGRAMFILES", "PROGRAMDATA"} {
				if value, ok := os.LookupEnv(name); ok {
					run.Env = append(run.Env, name+"="+value)
				}
			}
			var output strings.Builder
			run.Stdout, run.Stderr = &output, &output
			// A leftover child process could hold the output pipe open after cancel.
			run.WaitDelay = 5 * time.Second
			h.Require.NoError(run.Start())
			done := make(chan error, 1)
			go func() { done <- run.Wait() }()
			// The gateway refuses every request, so stop the harness once it asks
			// for the route rather than waiting out its retries.
			exited := false
			for !gateway.saw("acme/primary") && !exited && ctx.Err() == nil {
				select {
				case <-done:
					exited = true
				case <-ctx.Done():
				case <-time.After(100 * time.Millisecond):
				}
			}
			cancel()
			if !exited {
				<-done
			}
			if !gateway.saw("acme/primary") {
				t.Fatalf("%s did not request acme/primary; requested %v\n%s", tc.binary, gateway.requested(), output.String())
			}
			h.Require.Contains(gateway.sentClients(), tc.name, "X-Baseten-Client")
			if tc.name == "claude-code" {
				h.Require.True(slices.ContainsFunc(gateway.sentRequestClasses(), func(v string) bool { return v != "" }), "x-claude-code-request-class")
			}
		})
	}
}

func Test_Harness_Setup_RouterRouteInPickers(t *testing.T) {
	skipUnlessSupported(t)
	h, api := fakeHarnessAPI(t)
	api.SetRoute("GET", "/v1/routes", 200, map[string]any{
		"items": []any{
			map[string]any{"id": "route-a", "name": "acme/primary", "display_name": "Primary", "invoke_url": api.URL, "metadata": harnessRouteMetadata,
				"target": map[string]any{"type": "BASETEN_MODEL_API", "model": "deepseek"}},
			map[string]any{"id": "route-b", "name": "acme/auto", "display_name": "Auto", "invoke_url": api.URL, "metadata": harnessRouteMetadata,
				"target": map[string]any{"type": "ROUTER"}},
		},
		"pagination": map[string]any{"has_more": false},
	})
	root := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(root, "claude"))
	t.Setenv("CODEX_HOME", filepath.Join(root, "codex"))
	t.Setenv("XDG_CONFIG_HOME", root)
	h.Require.NoError(h.Execute("harness", "setup", "--harness", "claude-code", "--harness", "opencode", "--route", "acme/auto", "--background-route", "acme/auto", "--yes"))
	h.Require.NoError(h.Execute("harness", "setup", "--harness", "codex", "--route", "acme/auto", "--yes"))
	h.Require.NotContains(h.Stderr.String(), "skipping routes")

	claude := readHarnessSettings(t, "claude-code", filepath.Join(root, "claude", "settings.json"))
	h.Require.Equal("acme/auto", claude["model"])
	h.Require.Contains(claude["modelPicker"].(map[string]any)["options"], map[string]any{"model": "acme/auto", "label": "Auto"})
	h.Require.Equal("acme/auto", claude["env"].(map[string]any)["ANTHROPIC_DEFAULT_HAIKU_MODEL"])

	opencode := readHarnessSettings(t, "opencode", filepath.Join(root, "opencode", "opencode.json"))
	h.Require.Equal("baseten-harness/acme/auto", opencode["model"])
	h.Require.Equal("baseten-harness/acme/auto", opencode["small_model"])
	models := opencode["provider"].(map[string]any)["baseten-harness"].(map[string]any)["models"].(map[string]any)
	h.Require.Contains(models, "acme/auto")
	h.Require.NotContains(models["acme/auto"], "provider")

	h.Require.Equal("acme/auto", readHarnessSettings(t, "codex", filepath.Join(root, "codex", "config.toml"))["model"])
	h.Require.Equal([]string{"acme/primary", "acme/auto"}, catalogSlugs(t, filepath.Join(root, "codex", "baseten-models.json")))
}

func Test_Harness_Setup_OpenCodeFirstPartyRoutes(t *testing.T) {
	skipUnlessSupported(t)
	h, api := fakeHarnessAPI(t)
	api.SetRoute("GET", "/v1/routes", 200, map[string]any{
		"items": []any{
			map[string]any{"id": "route-a", "name": "acme/claude", "display_name": "Claude", "invoke_url": api.URL, "metadata": harnessRouteMetadata,
				"target": map[string]any{"type": "ANTHROPIC", "model": "claude-opus-5-5", "secret_name": "anthropic-key"}},
			map[string]any{"id": "route-b", "name": "acme/gpt", "display_name": "GPT", "invoke_url": api.URL, "metadata": harnessRouteMetadata,
				"target": map[string]any{"type": "OPENAI", "model": "gpt-5.5", "secret_name": "openai-key"}},
			map[string]any{"id": "route-c", "name": "acme/open", "display_name": "Open", "invoke_url": api.URL, "metadata": harnessRouteMetadata,
				"target": map[string]any{"type": "BASETEN_MODEL_API", "model": "deepseek"}},
		},
		"pagination": map[string]any{"has_more": false},
	})
	dir := t.TempDir()
	path := harnessSettingsFile("opencode", dir)
	h.Require.NoError(h.Execute("harness", "setup", "--harness", "opencode", "--config-dir", dir, "--yes"))
	provider := readHarnessSettings(t, "opencode", path)["provider"].(map[string]any)["baseten-harness"].(map[string]any)
	models := provider["models"].(map[string]any)
	h.Require.Equal(map[string]any{"npm": "@ai-sdk/anthropic"}, models["acme/claude"].(map[string]any)["provider"])
	h.Require.Equal(map[string]any{"npm": "@ai-sdk/openai"}, models["acme/gpt"].(map[string]any)["provider"])
	h.Require.NotContains(models["acme/open"], "provider")
}

func Test_Harness_Setup_PiFirstPartyRoutes(t *testing.T) {
	skipUnlessSupported(t)
	h, api := fakeHarnessAPI(t)
	api.SetRoute("GET", "/v1/routes", 200, map[string]any{
		"items": []any{
			map[string]any{"id": "route-a", "name": "acme/claude", "display_name": "Claude", "invoke_url": api.URL, "metadata": harnessRouteMetadata,
				"target": map[string]any{"type": "ANTHROPIC", "model": "claude-opus-5-5", "secret_name": "anthropic-key"}},
			map[string]any{"id": "route-b", "name": "acme/gpt", "display_name": "GPT", "invoke_url": api.URL, "metadata": harnessRouteMetadata,
				"target": map[string]any{"type": "OPENAI", "model": "gpt-5.5", "secret_name": "openai-key"}},
			map[string]any{"id": "route-c", "name": "acme/open", "display_name": "Open", "invoke_url": api.URL, "metadata": harnessRouteMetadata,
				"target": map[string]any{"type": "BASETEN_MODEL_API", "model": "deepseek"}},
		},
		"pagination": map[string]any{"has_more": false},
	})
	dir := t.TempDir()
	h.Require.NoError(h.Execute("harness", "setup", "--harness", "pi", "--config-dir", dir, "--yes"))
	provider := readHarnessSettings(t, "pi", harnessSettingsFile("pi", dir))["providers"].(map[string]any)["baseten-harness"].(map[string]any)
	apis := map[string]any{}
	for _, m := range provider["models"].([]any) {
		apis[m.(map[string]any)["id"].(string)] = m.(map[string]any)["api"]
	}
	h.Require.Equal(map[string]any{"acme/claude": "anthropic-messages", "acme/gpt": "openai-responses", "acme/open": nil}, apis)
	settings := readHarnessSettings(t, "pi", filepath.Join(dir, "settings.json"))
	h.Require.Equal(map[string]any{"defaultProvider": "baseten-harness", "defaultModel": "acme/claude"}, settings)
}

func Test_Harness_Setup_RouteMetadata(t *testing.T) {
	skipUnlessSupported(t)
	h, api := fakeHarnessAPI(t)
	api.SetRouteFunc("GET", "/v1/routes", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// The second page holds a route without metadata.
		if r.URL.Query().Get("cursor") == "" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"items":      []any{map[string]any{"name": "acme/primary", "display_name": "Primary", "invoke_url": api.URL, "metadata": harnessRouteMetadata}},
				"pagination": map[string]any{"has_more": true, "cursor": "page-two"},
			})
			return
		}
		// A skipped route's invoke URL doesn't have to match.
		_ = json.NewEncoder(w).Encode(map[string]any{
			"items":      []any{map[string]any{"name": "acme/bare", "display_name": "Bare", "invoke_url": "https://elsewhere.example.com", "metadata": nil}},
			"pagination": map[string]any{"has_more": false},
		})
	})
	dir := t.TempDir()
	h.Require.NoError(h.Execute("harness", "setup", "--harness", "claude-code", "--config-dir", dir, "--yes"))
	h.Require.Contains(h.Stderr.String(), "warning: skipping routes without usable model metadata: acme/bare")
	h.Require.NotContains(fmt.Sprint(readHarnessSettings(t, "claude-code", harnessSettingsFile("claude-code", dir))["availableModels"]), "acme/bare")

	h.Stderr.Reset()
	h.Require.Error(h.Execute("harness", "setup", "--harness", "claude-code", "--route", "acme/bare", "--config-dir", t.TempDir(), "--yes"))
	h.Require.Contains(h.Stderr.String(), `route "acme/bare" has no model metadata`)
	h.Require.Equal(1, countCalls(api, "POST", "/v1/teams/team-a/api_keys"), "a failed setup creates no key")

	// A route's cost reaches OpenCode, and needs both input and output prices.
	priced := maps.Clone(harnessRouteMetadata)
	priced["cost"] = map[string]any{"input": 0.3, "output": 1.2, "cache_read": nil, "long_context": map[string]any{"input": 0.6}}
	api.SetRoute("GET", "/v1/routes", 200, map[string]any{
		"items":      []any{map[string]any{"name": "acme/primary", "display_name": "Primary", "invoke_url": api.URL, "metadata": priced}},
		"pagination": map[string]any{"has_more": false},
	})
	opencode := t.TempDir()
	h.Require.NoError(h.Execute("harness", "setup", "--harness", "opencode", "--config-dir", opencode, "--yes"))
	provider := readHarnessSettings(t, "opencode", harnessSettingsFile("opencode", opencode))["provider"].(map[string]any)
	model := provider["baseten-harness"].(map[string]any)["models"].(map[string]any)["acme/primary"].(map[string]any)
	h.Require.Equal(map[string]any{"input": 0.3, "output": 1.2}, model["cost"])
}

func Test_Harness_Setup_RoutesByAPIFormat(t *testing.T) {
	skipUnlessSupported(t)
	h, api := fakeHarnessAPI(t)
	formats := func(messages, responses bool) map[string]any {
		m := maps.Clone(harnessRouteMetadata)
		m["supported_api_formats"] = map[string]any{"messages": messages, "responses": responses}
		return m
	}
	unknown := maps.Clone(harnessRouteMetadata)
	delete(unknown, "supported_api_formats")
	api.SetRoute("GET", "/v1/routes", 200, map[string]any{
		"items": []any{
			map[string]any{"name": "acme/claude", "display_name": "Claude", "invoke_url": api.URL, "metadata": formats(true, false)},
			map[string]any{"name": "acme/gpt", "display_name": "GPT", "invoke_url": api.URL, "metadata": formats(false, true)},
			map[string]any{"name": "acme/open", "display_name": "Open", "invoke_url": api.URL, "metadata": formats(true, true)},
			map[string]any{"name": "acme/new", "display_name": "New", "invoke_url": api.URL, "metadata": unknown},
		},
		"pagination": map[string]any{"has_more": false},
	})
	root := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(root, "claude"))
	t.Setenv("CODEX_HOME", filepath.Join(root, "codex"))
	h.Require.NoError(h.Execute("harness", "setup", "--harness", "claude-code", "--harness", "codex", "--yes"))

	claude := readHarnessSettings(t, "claude-code", filepath.Join(root, "claude", "settings.json"))
	h.Require.Equal("acme/claude", claude["model"])
	h.Require.Equal([]any{"acme/claude", "acme/open", "acme/new", "deepseek-ai/DeepSeek-V4.1-Flash"}, claude["availableModels"])
	h.Require.Equal("acme/gpt", readHarnessSettings(t, "codex", filepath.Join(root, "codex", "config.toml"))["model"])
	h.Require.Equal([]string{"acme/gpt", "acme/open", "acme/new"}, catalogSlugs(t, filepath.Join(root, "codex", "baseten-models.json")))

	h.Require.Error(h.Execute("harness", "setup", "--harness", "claude-code", "--route", "acme/gpt", "--yes"))
	h.Require.Contains(h.Stderr.String(), `route "acme/gpt" is not one of the team's routes this harness can call`)
}

type codexDaemonHarnessExecer struct {
	fakeHarnessExecer
	socket        string
	lsof          string
	noLsof        bool
	restartFails  bool
	restarts      int
	loggedIn      bool
	apiKey        bool
	logoutFails   bool
	logouts       int
	probes        int
	forcedAtProbe bool
}

func (e *codexDaemonHarnessExecer) LookPath(name string) (string, error) {
	if name == "lsof" && e.noLsof {
		return "", exec.ErrNotFound
	}
	return e.fakeHarnessExecer.LookPath(name)
}

func (e *codexDaemonHarnessExecer) Exec(command *exec.Cmd) error {
	switch strings.Join(command.Args[1:], " ") {
	case "app-server daemon version":
		if e.socket == "" {
			return errors.New("exit status 1")
		}
		_, err := fmt.Fprintf(command.Stdout, `{"status":"running","socketPath":%q}`, e.socket)
		return err
	case "app-server daemon restart":
		e.restarts++
		if e.restartFails {
			return errors.New("exit status 1")
		}
		_, err := fmt.Fprint(command.Stdout, `{"status":"restarted","pid":4242}`)
		return err
	case "-U -F n":
		_, err := fmt.Fprint(command.Stdout, e.lsof)
		return err
	case "login status":
		e.probes++
		for _, kv := range command.Env {
			if dir, ok := strings.CutPrefix(kv, "CODEX_HOME="); ok {
				config, _ := os.ReadFile(filepath.Join(dir, "config.toml"))
				e.forcedAtProbe = e.forcedAtProbe || strings.Contains(string(config), "forced_login_method")
			}
		}
		switch {
		case e.apiKey:
			_, err := fmt.Fprintln(command.Stderr, "Logged in using an API key - sk-***")
			return err
		case e.loggedIn:
			_, err := fmt.Fprintln(command.Stderr, "Logged in using ChatGPT")
			return err
		}
		return errors.New("exit status 1")
	case "logout":
		e.logouts++
		if e.logoutFails {
			return errors.New("exit status 1")
		}
		return nil
	}
	return e.fakeHarnessExecer.Exec(command)
}

func Test_Harness_Setup_RestartsCodexDaemon(t *testing.T) {
	skipUnlessSupported(t)
	socket := filepath.Join(t.TempDir(), "app-server-control.sock")
	if err := os.WriteFile(socket, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	socket, err := filepath.EvalSymlinks(socket)
	if err != nil {
		t.Fatal(err)
	}
	listener := "p1\nf31\nn" + socket + " type=STREAM\n"
	for name, tc := range map[string]struct {
		execer   *codexDaemonHarnessExecer
		restarts int
		stderr   []string
		absent   []string
	}{
		"no daemon":        {&codexDaemonHarnessExecer{}, 0, nil, []string{"codex app-server daemon"}},
		"no sessions":      {&codexDaemonHarnessExecer{socket: socket, lsof: listener}, 1, []string{"Restarted codex app-server daemon (pid 4242) so the new model catalog takes effect."}, []string{"warning"}},
		"sessions":         {&codexDaemonHarnessExecer{socket: socket, lsof: listener + "f14\nn" + socket + " type=STREAM\nf15\nn" + socket + " type=STREAM\n"}, 1, []string{"will disconnect 2 running codex session(s)", "Restarted codex app-server daemon (pid 4242)"}, nil},
		"sessions unknown": {&codexDaemonHarnessExecer{socket: socket, noLsof: true}, 1, []string{"could not check for running codex sessions", "Restarted codex app-server daemon (pid 4242)"}, nil},
		"restart fails":    {&codexDaemonHarnessExecer{socket: socket, lsof: listener, restartFails: true}, 1, []string{"could not restart the codex app-server daemon", "Run `codex app-server daemon restart` when you're done to pick up the new models."}, []string{"Restarted"}},
	} {
		t.Run(name, func(t *testing.T) {
			h, _ := harnessAPI(t, "")
			h.Context = internalcmd.WithExecer(h.Context, tc.execer)
			h.Require.NoError(h.Execute("harness", "setup", "--harness", "codex", "--config-dir", t.TempDir(), "--yes"))
			h.Require.Equal(tc.restarts, tc.execer.restarts)
			h.Require.Contains(h.Stderr.String(), "Configuration saved.")
			for _, want := range tc.stderr {
				h.Require.Contains(h.Stderr.String(), want)
			}
			for _, unwanted := range tc.absent {
				h.Require.NotContains(h.Stderr.String(), unwanted)
			}
		})
	}
}

func Test_Harness_Setup_SignsOutCodex(t *testing.T) {
	skipUnlessSupported(t)
	signingOut := "Signing codex out of OpenAI/ChatGPT"
	signedOut := "Signed codex out of OpenAI/ChatGPT and cleared the cached workspace policy."
	hint := "Run `codex logout` and delete `"
	for name, tc := range map[string]struct {
		execer                             *codexDaemonHarnessExecer
		auth, authAfter, cache, cacheAfter bool
		logouts                            int
		stderr                             []string
		absent                             []string
	}{
		"not logged in":        {&codexDaemonHarnessExecer{}, true, true, true, true, 0, nil, []string{signingOut, "codex logout"}},
		"api key login":        {&codexDaemonHarnessExecer{apiKey: true}, true, true, true, false, 1, []string{signingOut, signedOut}, []string{"warning"}},
		"logged in":            {&codexDaemonHarnessExecer{loggedIn: true}, true, true, false, false, 1, []string{signingOut, signedOut}, []string{"warning"}},
		"logout fails":         {&codexDaemonHarnessExecer{loggedIn: true, logoutFails: true}, true, false, true, false, 1, []string{signingOut, "codex logout failed", "removed", signedOut}, []string{hint}},
		"logout fails no file": {&codexDaemonHarnessExecer{loggedIn: true, logoutFails: true}, false, false, true, true, 1, []string{"codex logout failed", hint, "cloud-config-bundle-cache.json` to stop the workspace default model from overriding the Baseten route."}, []string{"removed", signedOut}},
	} {
		t.Run(name, func(t *testing.T) {
			h, _ := harnessAPI(t, "")
			h.Context = internalcmd.WithExecer(h.Context, tc.execer)
			dir := t.TempDir()
			auth := filepath.Join(dir, "auth.json")
			cache := filepath.Join(dir, "cloud-config-bundle-cache.json")
			for path, present := range map[string]bool{auth: tc.auth, cache: tc.cache} {
				if present {
					h.Require.NoError(os.WriteFile(path, []byte("{}"), 0o600))
				}
			}
			h.Require.NoError(h.Execute("harness", "setup", "--harness", "codex", "--config-dir", dir, "--yes"))
			h.Require.Equal(tc.logouts, tc.execer.logouts)
			h.Require.Equal(1, tc.execer.probes)
			h.Require.False(tc.execer.forcedAtProbe, "login status must be probed before forced_login_method is written")
			h.Require.Equal("api", readHarnessSettings(t, "codex", filepath.Join(dir, "config.toml"))["forced_login_method"])
			_, err := os.Stat(auth)
			h.Require.Equal(tc.authAfter, err == nil)
			_, err = os.Stat(cache)
			h.Require.Equal(tc.cacheAfter, err == nil)
			for _, want := range tc.stderr {
				h.Require.Contains(h.Stderr.String(), want)
			}
			for _, unwanted := range tc.absent {
				h.Require.NotContains(h.Stderr.String(), unwanted)
			}
		})
	}
}

func Test_Harness_Teardown_RestartsCodexDaemon(t *testing.T) {
	skipUnlessSupported(t)
	socket := filepath.Join(t.TempDir(), "app-server-control.sock")
	if err := os.WriteFile(socket, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	socket, err := filepath.EvalSymlinks(socket)
	if err != nil {
		t.Fatal(err)
	}
	listener := "p1\nf31\nn" + socket + " type=STREAM\n"
	signBackIn := "Teardown removed the ChatGPT login restriction. Run `codex login` to sign back in; your workspace defaults re-apply on the next launch."
	for name, tc := range map[string]struct {
		execer   *codexDaemonHarnessExecer
		restarts int
		stderr   []string
		absent   []string
	}{
		"no daemon":     {&codexDaemonHarnessExecer{}, 0, nil, []string{"app-server daemon"}},
		"sessions":      {&codexDaemonHarnessExecer{socket: socket, lsof: listener + "f14\nn" + socket + " type=STREAM\nf15\nn" + socket + " type=STREAM\n"}, 1, []string{"will disconnect 2 running codex session(s)", "Restarted codex app-server daemon (pid 4242)"}, nil},
		"restart fails": {&codexDaemonHarnessExecer{socket: socket, lsof: listener, restartFails: true}, 1, []string{"could not restart the codex app-server daemon", "Run `codex app-server daemon restart` when you're done to pick up the new models."}, []string{"Restarted"}},
	} {
		t.Run(name, func(t *testing.T) {
			h, _ := harnessAPI(t, "")
			h.Context = internalcmd.WithExecer(h.Context, fakeHarnessExecer{})
			dir := t.TempDir()
			h.Require.NoError(h.Execute("harness", "setup", "--harness", "codex", "--config-dir", dir, "--key-name", "laptop", "--yes"))
			h.Context = internalcmd.WithExecer(h.Context, tc.execer)
			h.Require.NoError(h.Execute("harness", "teardown", "--harness", "codex", "--config-dir", dir, "--yes"))
			h.Require.Equal(tc.restarts, tc.execer.restarts)
			h.Require.Contains(h.Stderr.String(), "Baseten settings removed.")
			h.Require.Contains(h.Stderr.String(), signBackIn)
			for _, want := range tc.stderr {
				h.Require.Contains(h.Stderr.String(), want)
			}
			for _, unwanted := range tc.absent {
				h.Require.NotContains(h.Stderr.String(), unwanted)
			}
		})
	}
}

func Test_Harness_Setup_DryRunSkipsCodexSignOut(t *testing.T) {
	skipUnlessSupported(t)
	h, _ := harnessAPI(t, "")
	execer := &codexDaemonHarnessExecer{loggedIn: true, socket: "/tmp/unused.sock"}
	h.Context = internalcmd.WithExecer(h.Context, execer)
	h.Require.NoError(h.Execute("harness", "setup", "--harness", "codex", "--config-dir", t.TempDir(), "--dry-run"))
	h.Require.Equal(0, execer.probes)
	h.Require.Equal(0, execer.logouts)
	h.Require.Equal(0, execer.restarts)
	h.Require.NotContains(h.Stderr.String(), "Signing codex out")
}
