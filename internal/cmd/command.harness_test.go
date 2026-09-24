package cmd_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	"github.com/zalando/go-keyring"
)

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
			"items":      []any{map[string]any{"id": "route-a", "name": "acme/primary", "team_id": r.URL.Query().Get("team_id"), "display_name": "Primary", "invoke_url": invokeURL}},
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

func countCalls(api *MockManagementAPI, method, path string) int {
	count := 0
	for _, call := range api.Calls() {
		if call.Method == method && call.Path == path {
			count++
		}
	}
	return count
}

// skipUnlessMacOS skips tests of harness commands, which support only macOS.
func skipUnlessMacOS(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "darwin" {
		t.Skip("harness commands support only macOS")
	}
}

func Test_HarnessCommands_RejectOtherPlatforms(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("harness commands support macOS")
	}
	for _, command := range []string{"setup", "status", "teardown"} {
		h := NewCommandHarness(t)
		h.Require.Error(h.Execute("harness", command, "--harness", "codex"))
		h.Require.Contains(h.Stderr.String(), "harness commands support only macOS for now")
	}
}

func Test_Harness_Setup_Lifecycle(t *testing.T) {
	skipUnlessMacOS(t)
	for _, name := range []string{"claude-code", "codex", "opencode"} {
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
	skipUnlessMacOS(t)
	if runtime.GOOS != "darwin" {
		t.Skip("macOS desktop installation")
	}
	h, _ := harnessAPI(t, "")
	h.Context = internalcmd.WithExecer(h.Context, fakeHarnessExecer{
		binary: "/Applications/ChatGPT.app/Contents/Resources/codex",
	})
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
	skipUnlessMacOS(t)
	h, _ := fakeHarnessAPI(t)
	dir := t.TempDir()
	h.Require.NoError(h.Execute("harness", "setup", "--harness", "claude-code", "--config-dir", dir, "--yes"))
	for _, want := range []string{"Team: Engineering", "Available routes: 1", "acme/primary", "Primary", "Default route     acme/primary", "Background route  deepseek-ai/DeepSeek-V4.1-Flash"} {
		h.Require.Contains(h.Stdout.String(), want)
	}
	h.Require.Contains(h.Stderr.String(), "Created routes API key baseten-harness-")
	h.Require.Contains(h.Stderr.String(), "Configuration saved.")
	h.Require.Contains(h.Stderr.String(), "Undo with: baseten harness teardown --harness claude-code --config-dir '"+dir+"'")
	h.Require.NoError(h.Execute("harness", "setup", "--harness", "claude-code", "--config-dir", dir, "--yes"))
	h.Require.NotContains(h.Stderr.String(), "Created routes API key")
	h.Require.Contains(h.Stdout.String(), "Already configured")
}

func Test_Harness_Setup_RequiresHarnessWhenNotInteractive(t *testing.T) {
	skipUnlessMacOS(t)
	h := NewCommandHarness(t)
	h.Require.Error(h.Execute("harness", "setup"))
	h.Require.Equal(int(public.ExitUsage), h.ExitCode)
	h.Require.Contains(h.Stderr.String(), "pass --harness")
}

func Test_Harness_ConfigDirRequiresOneHarness(t *testing.T) {
	skipUnlessMacOS(t)
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
	skipUnlessMacOS(t)
	h, api := fakeHarnessAPI(t)
	h.Context = internalcmd.WithExecer(h.Context, missingHarnessExecer{})
	h.Require.Error(h.Execute("harness", "setup", "--harness", "codex", "--dry-run"))
	want := "codex is not installed or not on PATH"
	if runtime.GOOS == "darwin" {
		want = "codex is not installed or not on PATH or in ChatGPT.app or Codex.app"
	}
	h.Require.Contains(h.Stderr.String(), want)
	h.Require.Empty(api.Calls())
}

func Test_Harness_Setup_Teams(t *testing.T) {
	skipUnlessMacOS(t)
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
	skipUnlessMacOS(t)
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
	skipUnlessMacOS(t)
	h, api := fakeHarnessAPI(t)
	h.Require.NoError(h.Execute("harness", "setup", "--harness", "claude-code", "--config-dir", t.TempDir(), "--yes"))
	name := api.FindCall("POST", "/v1/teams/team-a/api_keys").BodyJSON(t)["name"].(string)
	h.Require.Regexp(`^baseten-harness-[a-z0-9]+(-[a-z0-9]+)*$`, name)
}

func Test_Harness_Setup_KeyCreationFailureCanBeRetried(t *testing.T) {
	skipUnlessMacOS(t)
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
	skipUnlessMacOS(t)
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
	skipUnlessMacOS(t)
	for _, tc := range []struct{ name, flag string }{
		{"codex", "--background-route"}, {"codex", "--subagent-route"}, {"codex", "--fallback-route"}, {"opencode", "--fallback-route"},
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
	skipUnlessMacOS(t)
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
	picker := readHarnessSettings(t, "claude-code", path)["modelPicker"].(map[string]any)
	h.Require.Equal([]any{map[string]any{"model": "acme/primary", "label": "Primary"}}, picker["options"])
	h.Require.NoError(h.Execute("harness", "teardown", "--harness", "claude-code", "--config-dir", dir, "--yes"))
	h.Require.Equal(map[string]any{"theme": "dark"}, readHarnessSettings(t, "claude-code", path))
}

func Test_Harness_DefaultDiscovery(t *testing.T) {
	skipUnlessMacOS(t)
	h, api := fakeHarnessAPI(t)
	root := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(root, "claude"))
	t.Setenv("CODEX_HOME", filepath.Join(root, "codex"))
	t.Setenv("XDG_CONFIG_HOME", root)
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
	skipUnlessMacOS(t)
	h, api := fakeHarnessAPI(t)
	root := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(root, "claude"))
	t.Setenv("CODEX_HOME", filepath.Join(root, "codex"))
	t.Setenv("XDG_CONFIG_HOME", root)
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

// harnessGateway records the model each inference request names and refuses it.
type harnessGateway struct {
	mu     sync.Mutex
	models []string
}

func (g *harnessGateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Model string `json:"model"`
	}
	raw, _ := io.ReadAll(r.Body)
	if json.Unmarshal(raw, &body) == nil && body.Model != "" {
		g.mu.Lock()
		g.models = append(g.models, body.Model)
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

// Test_Harness_Setup_RealHarness configures each installed harness in an
// isolated directory, runs it, and checks that it asks the gateway for the
// configured route. Harnesses not on PATH are skipped.
func Test_Harness_Setup_RealHarness(t *testing.T) {
	skipUnlessMacOS(t)
	for _, tc := range []struct {
		name, binary string
		args         []string
	}{
		{"claude-code", "claude", []string{"-p", "Reply with OK."}},
		{"codex", "codex", []string{"exec", "--skip-git-repo-check", "Reply with OK."}},
		{"opencode", "opencode", []string{"run", "Reply with OK."}},
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
				"CLAUDE_CONFIG_DIR=" + configDir, "CODEX_HOME=" + configDir,
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
		})
	}
}

func Test_Harness_Setup_OpenCodeFirstPartyRoutes(t *testing.T) {
	skipUnlessMacOS(t)
	h, api := fakeHarnessAPI(t)
	api.SetRoute("GET", "/v1/routes", 200, map[string]any{
		"items": []any{
			map[string]any{"id": "route-a", "name": "acme/claude", "display_name": "Claude", "invoke_url": api.URL,
				"target": map[string]any{"type": "ANTHROPIC", "model": "claude-opus-5-5", "secret_name": "anthropic-key"}},
			map[string]any{"id": "route-b", "name": "acme/gpt", "display_name": "GPT", "invoke_url": api.URL,
				"target": map[string]any{"type": "OPENAI", "model": "gpt-5.5", "secret_name": "openai-key"}},
			map[string]any{"id": "route-c", "name": "acme/open", "display_name": "Open", "invoke_url": api.URL,
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
