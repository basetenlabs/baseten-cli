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
	"github.com/stretchr/testify/require"
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

// versionedHarnessExecer finds only the listed binaries, each printing its
// own --version output. Bare names resolve under /fake, as on PATH.
type versionedHarnessExecer map[string]string

func (e versionedHarnessExecer) LookPath(name string) (string, error) {
	if _, ok := e[name]; !ok {
		return "", exec.ErrNotFound
	}
	if filepath.IsAbs(name) {
		return name, nil
	}
	return "/fake/" + name, nil
}

func (e versionedHarnessExecer) Exec(command *exec.Cmd) error {
	for name, version := range e {
		if command.Path == name || command.Path == "/fake/"+name {
			_, err := fmt.Fprintln(command.Stdout, version)
			return err
		}
	}
	return errors.New("unknown harness executed")
}

type missingHarnessExecer struct{}

func (missingHarnessExecer) LookPath(string) (string, error) { return "", exec.ErrNotFound }

func (missingHarnessExecer) Exec(*exec.Cmd) error { return errors.New("absent harness executed") }

func gridRouteTeam(r *http.Request) (string, string) {
	if r.URL.Query().Get("team_id") == "team-b" {
		return "team-b", "Research"
	}
	return "team-a", "Engineering"
}

// gridAPI serves a user, one team, one route whose
// invoke URL is invokeURL, and Grid API key creation for both teams.
func gridAPI(t *testing.T, invokeURL string) (*CommandHarness, *MockManagementAPI) {
	t.Helper()
	h := NewCommandHarness(t)
	api := h.MockManagementAPI()
	if invokeURL == "" {
		invokeURL = api.URL
	}
	api.SetRoute("GET", "/v1/users/me", 200, map[string]string{"user_id": "user-a"})
	api.SetRoute("GET", "/v1/teams", 200, map[string]any{"teams": []map[string]any{{"id": "team-a", "name": "Engineering", "default": true}}})
	api.SetRouteFunc("GET", "/v1/grid/routes", func(w http.ResponseWriter, r *http.Request) {
		teamID, teamName := gridRouteTeam(r)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"items":      []any{map[string]any{"id": "route-primary", "name": "acme/primary", "team_id": teamID, "team_name": teamName, "display_name": "Primary", "invoke_url": invokeURL, "metadata": harnessRouteMetadata}},
			"pagination": map[string]any{"has_more": false},
		})
	})
	api.SetRoute("GET", "/v1/grid/connections", 200, map[string]any{"items": []any{}, "pagination": map[string]any{"has_more": false}})
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

// fakeGridAPI is gridAPI with every harness reported as installed.
// gridTwoTeams is a GET /v1/teams body for an organization with two teams,
// where setup needs --team.
var gridTwoTeams = map[string]any{"teams": []map[string]any{
	{"id": "team-a", "name": "Engineering", "default": true},
	{"id": "team-b", "name": "Research", "default": false},
}}

func fakeGridAPI(t *testing.T) (*CommandHarness, *MockManagementAPI) {
	t.Helper()
	h, api := gridAPI(t, "")
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

// skipUnlessSupported skips tests of Grid harness integrations, which support only macOS and Linux.
func skipUnlessSupported(t *testing.T) {
	t.Helper()
	if !harness.Supported() {
		t.Skip("Grid harness integrations support only macOS and Linux")
	}
}

func Test_Grid_Setup_Status_Teardown_RejectOtherPlatforms(t *testing.T) {
	if harness.Supported() {
		t.Skip("Grid harness integrations support this platform")
	}
	for _, command := range [][]string{{"grid", "setup"},
		{"grid", "status"},
		{"grid", "teardown"}} {
		h := NewCommandHarness(t)
		h.Require.Error(h.Execute(append(command, "--harness", "codex")...))
		h.Require.Contains(h.Stderr.String(), "Grid harness integrations support only macOS and Linux for now")
	}
}

func Test_Grid_Setup_Lifecycle(t *testing.T) {
	skipUnlessSupported(t)
	for _, name := range []string{"claude-code", "codex", "opencode", "pi"} {
		t.Run(name, func(t *testing.T) {
			h, api := fakeGridAPI(t)
			dir := t.TempDir()
			path := harnessSettingsFile(name, dir)
			h.Require.NoError(os.WriteFile(path, []byte(`{"theme":"dark"}`), 0o600))
			if name == "codex" {
				h.Require.NoError(os.WriteFile(path, []byte("theme = \"dark\"\n"), 0o600))
			}
			args := []string{"grid", "setup", "--harness", name, "--config-dir", dir, "--key-name", "laptop", "--output", "json"}

			h.Require.NoError(h.Execute(append(args, "--dry-run")...))
			var preview public.GridPlanList
			h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &preview))
			h.Require.NotEmpty(preview.Items)
			h.Require.Equal(map[string]any{"theme": "dark"}, readHarnessSettings(t, name, path))
			h.Require.Equal(0, countCalls(api, "POST", "/v1/teams/team-a/api_keys"))
			h.Require.Nil(api.FindCall("GET", "/v1/users/me"), "dry run skips the Grid API key")

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
			var rerun public.GridPlanList
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
			h.Require.NoError(h.Execute("grid", "status", "--harness", name, "--config-dir", dir, "--output", "json"))
			var statuses public.GridStatusList
			h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &statuses))
			h.Require.Len(statuses.Items, 1)
			h.Require.Equal("configured", statuses.Items[0].State)
			h.Require.Equal("acme/primary", statuses.Items[0].DefaultRoute)
			h.Require.Equal([]public.GridHarnessRoute{{Name: "acme/primary", DisplayName: "Primary"}}, statuses.Items[0].Routes)
			if name == "codex" {
				h.Require.Contains(statuses.Items[0].ManagedSettings, "forced_login_method")
			}
			h.Require.NotContains(h.Stdout.String(), "secret-team-a")

			h.Require.NoError(h.Execute("grid", "teardown", "--harness", name, "--config-dir", dir, "--dry-run"))
			h.Require.Contains(h.Stdout.String(), "Remove Baseten settings from "+name)
			h.Require.Contains(h.Stdout.String(), "Also delete the Grid API key")
			h.Require.Equal(calls, len(api.Calls()), "status and teardown previews are local")
			unchanged, err := os.ReadFile(path)
			h.Require.NoError(err)
			h.Require.Equal(settings, unchanged, "teardown --dry-run changes nothing")
			h.Require.Error(h.Execute("grid", "teardown", "--harness", name, "--config-dir", dir))
			h.Require.Contains(h.Stderr.String(), "pass --yes")
			h.Require.NoError(h.Execute("grid", "teardown", "--harness", name, "--config-dir", dir, "--yes"))
			h.Require.Equal(map[string]any{"theme": "dark"}, readHarnessSettings(t, name, path))
			h.Require.NoFileExists(filepath.Join(dir, "baseten-models.json"))
			h.Require.Equal(1, countCalls(api, "DELETE", "/v1/api_keys/secret-team-a"), "teardown deletes the unused key")

			h.Require.NoError(h.Execute("grid", "status", "--harness", name, "--config-dir", dir, "--output", "json"))
			h.Require.Contains(h.Stdout.String(), `"state": "not-configured"`)
		})
	}
}

func Test_Grid_Setup_CodexDesktopWithoutCLI(t *testing.T) {
	skipUnlessSupported(t)
	binary := "/Applications/ChatGPT.app/Contents/Resources/codex"
	if runtime.GOOS == "darwin" {
		binary = "/Applications/ChatGPT.app/Contents/Resources/codex-cli/CodexCLI.app/Contents/MacOS/codex"
	}
	if runtime.GOOS == "linux" {
		binary = "/usr/lib/chatgpt/resources/codex"
	}
	h, _ := gridAPI(t, "")
	h.Context = internalcmd.WithExecer(h.Context, fakeHarnessExecer{binary: binary})
	dir := t.TempDir()
	t.Setenv("CODEX_HOME", dir)
	h.Require.NoError(h.Execute("grid", "setup", "--harness", "codex", "--yes"))
	settings := readHarnessSettings(t, "codex", filepath.Join(dir, "config.toml"))
	h.Require.Equal("baseten-harness", settings["model_provider"])
	h.Require.Equal("acme/primary", settings["model"])
	h.Require.NoError(h.Execute("grid", "status", "--harness", "codex", "--output", "json"))
	var statuses public.GridStatusList
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &statuses))
	h.Require.Len(statuses.Items, 1)
	h.Require.True(statuses.Items[0].Installed)
	h.Require.Equal("configured", statuses.Items[0].State)
}

func Test_Grid_Setup_TextSummary(t *testing.T) {
	skipUnlessSupported(t)
	h, _ := fakeGridAPI(t)
	dir := t.TempDir()
	h.Require.NoError(h.Execute("grid", "setup", "--harness", "claude-code", "--config-dir", dir, "--yes"))
	h.Require.Empty(h.Stdout.String())
	h.Require.Contains(h.Stderr.String(), "Created Grid API key baseten-harness-")
	h.Require.Contains(h.Stderr.String(), "Configuration saved.")
	h.Require.Contains(h.Stderr.String(), "Undo with: baseten grid teardown --harness claude-code --config-dir '"+dir+"'")
	h.Require.NoError(h.Execute("grid", "setup", "--harness", "claude-code", "--config-dir", dir, "--yes", "--verbose"))
	for _, want := range []string{"Team: Engineering", "Primary route     acme/primary", "Background route  deepseek-ai/DeepSeek-V4.1-Flash", "Already configured"} {
		h.Require.Contains(h.Stdout.String(), want)
	}
	h.Require.NotContains(h.Stdout.String(), "Available routes")
	h.Require.NotContains(h.Stderr.String(), "Created Grid API key")
}

func Test_Grid_Setup_RequiresHarnessWhenNotInteractive(t *testing.T) {
	skipUnlessSupported(t)
	h := NewCommandHarness(t)
	h.Require.Error(h.Execute("grid", "setup"))
	h.Require.Equal(int(public.ExitUsage), h.ExitCode)
	h.Require.Contains(h.Stderr.String(), "pass --harness")
}

func Test_Grid_Setup_Status_Teardown_ConfigDirRequiresOneHarness(t *testing.T) {
	skipUnlessSupported(t)
	for _, command := range [][]string{{"grid", "setup"},
		{"grid", "status"},
		{"grid", "teardown"}} {
		for _, harnesses := range [][]string{nil, {"--harness", "codex", "--harness", "opencode"}} {
			t.Run(fmt.Sprint(command, len(harnesses)), func(t *testing.T) {
				h := NewCommandHarness(t)
				h.Require.Error(h.Execute(append(append(command, "--config-dir", t.TempDir()), harnesses...)...))
				h.Require.Equal(int(public.ExitUsage), h.ExitCode)
				h.Require.Contains(h.Stderr.String(), "--config-dir requires exactly one --harness")
			})
		}
	}
}

func Test_Grid_Setup_NotInstalledFailsBeforeAPI(t *testing.T) {
	skipUnlessSupported(t)
	h, api := fakeGridAPI(t)
	h.Context = internalcmd.WithExecer(h.Context, missingHarnessExecer{})
	h.Require.Error(h.Execute("grid", "setup", "--harness", "codex", "--dry-run"))
	want := "codex is not installed or not on PATH"
	switch runtime.GOOS {
	case "darwin":
		want = "codex is not installed or not on PATH or in ChatGPT.app or Codex.app"
	case "linux":
		want = "codex is not installed or not on PATH or in /usr/lib/chatgpt"
	}
	h.Require.Contains(h.Stderr.String(), want)
	h.Require.Empty(api.Calls())

	h.Require.Error(h.Execute("grid", "setup", "--harness", "opencode", "--dry-run"))
	want = "opencode is not installed or not on PATH"
	switch runtime.GOOS {
	case "darwin":
		want = "opencode is not installed or not on PATH or in OpenCode.app or ~/.opencode/bin"
	case "linux":
		want = "opencode is not installed or not on PATH or in /opt/OpenCode or ~/.opencode/bin"
	}
	h.Require.Contains(h.Stderr.String(), want)
	h.Require.Empty(api.Calls())
}

func Test_Grid_Setup_OpenCodeDesktopWithoutCLI(t *testing.T) {
	skipUnlessSupported(t)
	binary := "/Applications/OpenCode.app/Contents/Resources/opencode-cli"
	if runtime.GOOS == "linux" {
		binary = "/opt/OpenCode/resources/opencode-cli"
	}
	h, _ := gridAPI(t, "")
	h.Context = internalcmd.WithExecer(h.Context, fakeHarnessExecer{binary: binary})
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	h.Require.NoError(h.Execute("grid", "setup", "--harness", "opencode", "--yes"))
	settings := readHarnessSettings(t, "opencode", filepath.Join(dir, "opencode", "opencode.json"))
	h.Require.Equal("baseten-harness/acme/primary", settings["model"])
	h.Require.NoError(h.Execute("grid", "status", "--harness", "opencode", "--output", "json"))
	var statuses public.GridStatusList
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &statuses))
	h.Require.Len(statuses.Items, 1)
	h.Require.True(statuses.Items[0].Installed)
	h.Require.Equal("configured", statuses.Items[0].State)
	h.Require.Equal([]public.GridHarnessInstall{{Kind: "Desktop", Binary: binary, Version: "1.2.3"}}, statuses.Items[0].Installs)
	h.Require.NoError(h.Execute("grid", "status", "--harness", "opencode"))
	h.Require.Contains(h.Stdout.String(), "Version           1.2.3\n")
}

func Test_Grid_Status_ListsEveryInstall(t *testing.T) {
	skipUnlessSupported(t)
	desktop := "/Applications/OpenCode.app/Contents/Resources/opencode-cli"
	if runtime.GOOS == "linux" {
		desktop = "/opt/OpenCode/resources/opencode-cli"
	}
	h, _ := fakeGridAPI(t)
	h.Context = internalcmd.WithExecer(h.Context, versionedHarnessExecer{"opencode": "1.18.34", desktop: "opencode v2.0.27"})
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	h.Require.NoError(h.Execute("grid", "setup", "--harness", "opencode", "--yes"))
	h.Require.NoError(h.Execute("grid", "status", "--harness", "opencode"))
	h.Require.Contains(h.Stdout.String(), "Version           1.18.34 (CLI), 2.0.27 (Desktop)\n")
	h.Require.NoError(h.Execute("grid", "status", "--harness", "opencode", "--output", "json"))
	var statuses public.GridStatusList
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &statuses))
	h.Require.Equal([]public.GridHarnessInstall{
		{Kind: "CLI", Binary: "/fake/opencode", Version: "1.18.34"},
		{Kind: "Desktop", Binary: desktop, Version: "opencode v2.0.27"},
	}, statuses.Items[0].Installs)
}

func Test_Grid_Setup_OpenCodeFormatFollowsTheFile(t *testing.T) {
	skipUnlessSupported(t)
	h, _ := fakeGridAPI(t)
	dir := t.TempDir()
	path := harnessSettingsFile("opencode", dir)
	h.Require.NoError(os.MkdirAll(dir, 0o700))
	h.Require.NoError(os.WriteFile(path, []byte(`{"providers": {"other": {"name": "Other"}}}`), 0o600))
	h.Require.NoError(h.Execute("grid", "setup", "--harness", "opencode", "--config-dir", dir, "--yes"))
	settings := readHarnessSettings(t, "opencode", path)
	h.Require.Contains(settings["providers"], "baseten-harness")
	h.Require.NotContains(settings, "provider")
	h.Require.NotContains(settings, "small_model")
	h.Require.Equal("baseten-harness/deepseek-ai/DeepSeek-V4.1-Flash", settings["agents"].(map[string]any)["title"].(map[string]any)["model"])

	h.Require.NoError(h.Execute("grid", "status", "--harness", "opencode", "--config-dir", dir, "--output", "json"))
	var statuses public.GridStatusList
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &statuses))
	h.Require.Equal("configured", statuses.Items[0].State)
	h.Require.Equal("acme/primary", statuses.Items[0].DefaultRoute)
	h.Require.Equal("deepseek-ai/DeepSeek-V4.1-Flash", statuses.Items[0].BackgroundRoute)
	h.Require.NoError(h.Execute("grid", "status", "--harness", "opencode", "--config-dir", dir, "--verbose"))
	h.Require.Contains(h.Stderr.String(), "providers.baseten-harness")
	h.Require.Contains(h.Stderr.String(), "agents.title.model")
	h.Require.NotContains(h.Stderr.String(), " provider.baseten-harness")

	h.Require.NoError(h.Execute("grid", "teardown", "--harness", "opencode", "--config-dir", dir, "--yes"))
	h.Require.Equal(map[string]any{"providers": map[string]any{"other": map[string]any{"name": "Other"}}}, readHarnessSettings(t, "opencode", path))
}

func Test_Grid_Setup_Teams(t *testing.T) {
	skipUnlessSupported(t)
	for _, tc := range []struct{ team, want string }{{"", "team-a"}, {"team-b", "team-b"}, {"Research", "team-b"}} {
		t.Run(tc.team, func(t *testing.T) {
			h, api := fakeGridAPI(t)
			args := []string{"grid", "setup", "--harness", "claude-code", "--config-dir", t.TempDir(), "--yes"}
			if tc.team != "" {
				api.SetRoute("GET", "/v1/teams", 200, gridTwoTeams)
				args = append(args, "--team", tc.team)
			}
			h.Require.NoError(h.Execute(args...))
			query := api.FindCall("GET", "/v1/grid/routes").Query()
			if tc.team == "" {
				h.Require.NotContains(query, "team_id")
				h.Require.Nil(api.FindCall("GET", "/v1/teams"))
			} else {
				h.Require.Equal([]string{tc.want}, query["team_id"])
			}
			h.Require.Equal(tc.want, api.FindCall("GET", "/v1/grid/connections").Query().Get("team_id"))
			h.Require.Equal(tc.want, harnessRecordFile(t).Harnesses["claude-code"].TeamID)
			h.Require.Equal(1, countCalls(api, "POST", "/v1/teams/"+tc.want+"/api_keys"))
		})
	}
	// Ambiguity is reported by the routes API, without a client-side team lookup.
	h, api := fakeGridAPI(t)
	api.SetRoute("GET", "/v1/grid/routes", 400, map[string]any{"error": "Team ID is required when you belong to multiple."})
	dir := t.TempDir()
	h.Require.Error(h.Execute("grid", "setup", "--harness", "claude-code", "--config-dir", dir, "--yes", "--output", "json"))
	h.Require.Contains(h.Stderr.String(), "Team ID is required")
	h.Require.Equal(400, decodeJSONErrorEnvelope(h).APIStatusCode)
	h.Require.Len(api.Calls(), 1)
	h.Require.Nil(api.FindCall("GET", "/v1/teams"))
	h.Require.NoFileExists(harnessSettingsFile("claude-code", dir))
	h.Require.Empty(harnessRecordFile(t).Harnesses)
}

func Test_Grid_Setup_KeyPerTeamAndUser(t *testing.T) {
	skipUnlessSupported(t)
	h, api := fakeGridAPI(t)
	api.SetRoute("GET", "/v1/teams", 200, gridTwoTeams)
	dir := t.TempDir()
	path := harnessSettingsFile("claude-code", dir)
	for _, team := range []string{"team-a", "team-b", "team-a"} {
		h.Require.NoError(h.Execute("grid", "setup", "--harness", "claude-code", "--config-dir", dir, "--team", team, "--key-name", "laptop", "--yes"))
		settings, err := os.ReadFile(path)
		h.Require.NoError(err)
		h.Require.Contains(string(settings), "secret-"+team)
	}
	h.Require.Equal(1, countCalls(api, "POST", "/v1/teams/team-a/api_keys"))
	h.Require.Equal(1, countCalls(api, "POST", "/v1/teams/team-b/api_keys"))
	h.Require.Equal(map[string]any{"type": "ROUTES", "name": "laptop"}, api.FindCall("POST", "/v1/teams/team-a/api_keys").BodyJSON(t))
	key, err := configDirStore(t).GetRoutesKey(auth.RoutesKeyScope{ManagementURL: api.URL, UserID: "user-a", TeamID: "team-b"})
	h.Require.NoError(err)
	h.Require.Equal("secret-team-b", key)

	// Another user gets their own key.
	api.SetRoute("GET", "/v1/users/me", 200, map[string]string{"user_id": "user-b"})
	h.Require.NoError(h.Execute("grid", "setup", "--harness", "claude-code", "--config-dir", dir, "--team", "team-a", "--key-name", "laptop", "--yes"))
	h.Require.Equal(2, countCalls(api, "POST", "/v1/teams/team-a/api_keys"))
}

func Test_Grid_Setup_DefaultKeyName(t *testing.T) {
	skipUnlessSupported(t)
	h, api := fakeGridAPI(t)
	h.Require.NoError(h.Execute("grid", "setup", "--harness", "claude-code", "--config-dir", t.TempDir(), "--yes"))
	name := api.FindCall("POST", "/v1/teams/team-a/api_keys").BodyJSON(t)["name"].(string)
	h.Require.Regexp(`^baseten-harness-[a-z0-9]+(-[a-z0-9]+)*-[0-9a-f]{6}$`, name)

	// A saved key that's since been deleted is replaced.
	api.SetRoute("GET", "/v1/api_keys", 200, map[string]any{"keys": []any{}})
	h.Require.NoError(h.Execute("grid", "setup", "--harness", "claude-code", "--config-dir", t.TempDir(), "--yes"))
	h.Require.Equal(2, countCalls(api, "POST", "/v1/teams/team-a/api_keys"))
}

func Test_Grid_Setup_KeyCreationFailureCanBeRetried(t *testing.T) {
	skipUnlessSupported(t)
	h, api := fakeGridAPI(t)
	dir := t.TempDir()
	args := []string{"grid", "setup", "--harness", "claude-code", "--config-dir", dir, "--yes"}
	api.SetRoute("POST", "/v1/teams/team-a/api_keys", 500, map[string]string{"message": "unavailable"})
	h.Require.Error(h.Execute(args...))
	h.Require.Contains(h.Stderr.String(), "creating Grid API key")
	h.Require.NoFileExists(harnessSettingsFile("claude-code", dir))
	api.SetRoute("POST", "/v1/teams/team-a/api_keys", 200, map[string]string{"api_key": "secret-team-a"})
	h.Require.NoError(h.Execute(args...))
	h.Require.FileExists(harnessSettingsFile("claude-code", dir))
}

func Test_Grid_Setup_KeyringUnavailableStoresPlaintext(t *testing.T) {
	skipUnlessSupported(t)
	h, api := fakeGridAPI(t)
	keyring.MockInitWithError(errors.New("no keyring"))
	t.Cleanup(keyring.MockInit)
	args := []string{"grid", "setup", "--harness", "claude-code", "--config-dir", t.TempDir(), "--yes"}
	h.Require.NoError(h.Execute(args...))
	h.Require.Contains(h.Stderr.String(), "warning: could not store Grid API key in system keyring")
	h.Require.NoError(h.Execute(args...))
	h.Require.Equal(1, countCalls(api, "POST", "/v1/teams/team-a/api_keys"))
}

func Test_Grid_Setup_RouteFlags(t *testing.T) {
	skipUnlessSupported(t)
	for _, tc := range []struct{ name, flag string }{
		{"codex", "--background-route-id"}, {"codex", "--fallback-route-id"}, {"opencode", "--fallback-route-id"},
		{"pi", "--background-route-id"}, {"pi", "--fallback-route-id"},
	} {
		t.Run(tc.name+tc.flag, func(t *testing.T) {
			h, api := fakeGridAPI(t)
			dir := t.TempDir()
			h.Require.Error(h.Execute("grid", "setup", "--harness", tc.name, "--config-dir", dir, tc.flag, "route-primary", "--yes"))
			h.Require.Contains(h.Stderr.String(), "supported only")
			h.Require.Equal(0, countCalls(api, "POST", "/v1/teams/team-a/api_keys"))
			h.Require.NoFileExists(harnessSettingsFile(tc.name, dir))
		})
	}
	h, _ := fakeGridAPI(t)
	dir := t.TempDir()
	h.Require.NoError(h.Execute("grid", "setup", "--harness", "claude-code", "--config-dir", dir, "--background-route-id", "route-primary", "--yes"))
	env := readHarnessSettings(t, "claude-code", harnessSettingsFile("claude-code", dir))["env"].(map[string]any)
	h.Require.Equal("acme/primary", env["ANTHROPIC_DEFAULT_HAIKU_MODEL"])
}

func Test_Grid_Setup_SubagentRouteUnsupportedHarnessWarns(t *testing.T) {
	skipUnlessSupported(t)
	for _, name := range []string{"codex", "pi"} {
		t.Run(name, func(t *testing.T) {
			h, api := fakeGridAPI(t)
			dir := t.TempDir()
			require.NoError(t, h.Execute("grid", "setup", "--harness", name, "--config-dir", dir, "--subagent-route-id", "route-primary", "--yes"))
			require.Contains(t, h.Stderr.String(), "warning: --subagent-route-id is not supported by "+name+"; it will be ignored")
			require.Equal(t, 1, countCalls(api, "POST", "/v1/teams/team-a/api_keys"))
			require.FileExists(t, harnessSettingsFile(name, dir))
		})
	}
}

func harnessDefaultsModel(name string) map[string]any {
	if name == "" {
		return nil
	}
	return map[string]any{"route": map[string]any{"name": name}, "source": "team"}
}

func claudeHarnessDefaultsBody(primary, background string) map[string]any {
	return map[string]any{"harness_defaults": map[string]any{
		"claude_code": map[string]any{
			"primary": harnessDefaultsModel(primary), "background": harnessDefaultsModel(background),
		},
	}}
}

func Test_Grid_Setup_ServerDefaults(t *testing.T) {
	skipUnlessSupported(t)
	for _, tc := range []struct {
		name           string
		harnessName    string
		flags          []string
		endpoint       func(api *MockManagementAPI)
		wantFetch      bool
		wantPrimary    string
		wantBackground string
	}{
		{
			name:        "server defaults seed primary and background",
			harnessName: "claude-code",
			endpoint: func(api *MockManagementAPI) {
				api.SetRoute("GET", "/v1/grid/team_settings", 200, claudeHarnessDefaultsBody("acme/server", "acme/background"))
			},
			wantFetch:      true,
			wantPrimary:    "acme/server",
			wantBackground: "acme/background",
		},
		{
			name:        "route flag wins over server default",
			harnessName: "claude-code",
			flags:       []string{"--primary-route-id", "route-first"},
			endpoint: func(api *MockManagementAPI) {
				api.SetRoute("GET", "/v1/grid/team_settings", 200, claudeHarnessDefaultsBody("acme/server", "acme/background"))
			},
			wantFetch:      true,
			wantPrimary:    "acme/first",
			wantBackground: "acme/background",
		},
		{
			name:        "background route flag wins over server default",
			harnessName: "claude-code",
			flags:       []string{"--background-route-id", "route-first"},
			endpoint: func(api *MockManagementAPI) {
				api.SetRoute("GET", "/v1/grid/team_settings", 200, claudeHarnessDefaultsBody("acme/server", "acme/background"))
			},
			wantFetch:      true,
			wantPrimary:    "acme/server",
			wantBackground: "acme/first",
		},
		{
			name:        "server primary missing from routes falls back to positional",
			harnessName: "claude-code",
			endpoint: func(api *MockManagementAPI) {
				api.SetRoute("GET", "/v1/grid/team_settings", 200, claudeHarnessDefaultsBody("acme/missing", "acme/background"))
			},
			wantFetch:      true,
			wantPrimary:    "acme/first",
			wantBackground: "acme/background",
		},
		{
			name:        "server background missing from routes falls back to the default",
			harnessName: "claude-code",
			endpoint: func(api *MockManagementAPI) {
				api.SetRoute("GET", "/v1/grid/team_settings", 200, claudeHarnessDefaultsBody("acme/server", "acme/missing"))
			},
			wantFetch:      true,
			wantPrimary:    "acme/server",
			wantBackground: "deepseek-ai/DeepSeek-V4.1-Flash",
		},
		{
			name:        "server defaults without a primary still seed the background",
			harnessName: "claude-code",
			endpoint: func(api *MockManagementAPI) {
				api.SetRoute("GET", "/v1/grid/team_settings", 200, map[string]any{
					"harness_defaults": map[string]any{
						"claude_code": map[string]any{"background": harnessDefaultsModel("acme/background")},
					},
				})
			},
			wantFetch:      true,
			wantPrimary:    "acme/first",
			wantBackground: "acme/background",
		},
		{
			name:        "a response without a claude_code entry is ignored",
			harnessName: "claude-code",
			endpoint: func(api *MockManagementAPI) {
				api.SetRoute("GET", "/v1/grid/team_settings", 200, map[string]any{
					"harness_defaults": map[string]any{
						"codex": map[string]any{"primary": harnessDefaultsModel("acme/server")},
					},
				})
			},
			wantFetch:      true,
			wantPrimary:    "acme/first",
			wantBackground: "deepseek-ai/DeepSeek-V4.1-Flash",
		},
		{
			name:           "an unrouted routes settings endpoint falls back to positional",
			harnessName:    "claude-code",
			wantFetch:      true,
			wantPrimary:    "acme/first",
			wantBackground: "deepseek-ai/DeepSeek-V4.1-Flash",
		},
		{
			name:        "a routes settings error falls back to positional",
			harnessName: "claude-code",
			endpoint: func(api *MockManagementAPI) {
				api.SetRoute("GET", "/v1/grid/team_settings", 500, map[string]string{"message": "unavailable"})
			},
			wantFetch:      true,
			wantPrimary:    "acme/first",
			wantBackground: "deepseek-ai/DeepSeek-V4.1-Flash",
		},
		{
			name:        "a malformed routes settings response falls back to positional",
			harnessName: "claude-code",
			endpoint: func(api *MockManagementAPI) {
				api.SetRoute("GET", "/v1/grid/team_settings", 200, "not an object")
			},
			wantFetch:      true,
			wantPrimary:    "acme/first",
			wantBackground: "deepseek-ai/DeepSeek-V4.1-Flash",
		},
		{
			name:        "both route flags set skip the fetch",
			harnessName: "claude-code",
			flags:       []string{"--primary-route-id", "route-first", "--background-route-id", "route-background"},
			endpoint: func(api *MockManagementAPI) {
				api.SetRoute("GET", "/v1/grid/team_settings", 200, claudeHarnessDefaultsBody("acme/server", "acme/missing"))
			},
			wantPrimary:    "acme/first",
			wantBackground: "acme/background",
		},
		{
			name:        "codex ignores defaults for other harnesses",
			harnessName: "codex",
			endpoint: func(api *MockManagementAPI) {
				api.SetRoute("GET", "/v1/grid/team_settings", 200, claudeHarnessDefaultsBody("acme/server", "acme/background"))
			},
			wantFetch:   true,
			wantPrimary: "acme/first",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, api := fakeGridAPI(t)
			api.SetRouteFunc("GET", "/v1/grid/routes", func(w http.ResponseWriter, r *http.Request) {
				teamID, teamName := gridRouteTeam(r)
				w.Header().Set("Content-Type", "application/json")
				items := []any{}
				for _, route := range []struct{ name, display string }{
					{"acme/first", "First"}, {"acme/server", "Server"}, {"acme/background", "Background"},
				} {
					items = append(items, map[string]any{"id": "route-" + strings.TrimPrefix(route.name, "acme/"), "name": route.name, "display_name": route.display, "invoke_url": api.URL, "team_id": teamID, "team_name": teamName, "metadata": harnessRouteMetadata})
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"items": items, "pagination": map[string]any{"has_more": false}})
			})
			if tc.endpoint != nil {
				tc.endpoint(api)
			}
			dir := t.TempDir()
			args := append([]string{"grid", "setup", "--harness", tc.harnessName, "--config-dir", dir, "--yes"}, tc.flags...)
			h.Require.NoError(h.Execute(args...))
			settings := readHarnessSettings(t, tc.harnessName, harnessSettingsFile(tc.harnessName, dir))
			h.Require.Equal(tc.wantPrimary, settings["model"])
			if tc.harnessName == "claude-code" {
				env := settings["env"].(map[string]any)
				h.Require.Equal(tc.wantBackground, env["ANTHROPIC_DEFAULT_HAIKU_MODEL"])
				h.Require.Equal(settings["model"], env["ANTHROPIC_MODEL"])
			}
			call := api.FindCall("GET", "/v1/grid/team_settings")
			if tc.wantFetch {
				h.Require.NotNil(call, "setup fetches the routes settings endpoint")
				h.Require.Equal("team-a", call.Query().Get("team_id"))
			} else {
				h.Require.Nil(call, "setup does not fetch the routes settings endpoint")
			}
		})
	}
}

func Test_Grid_Setup_ReplacesSettingsAndPicker(t *testing.T) {
	skipUnlessSupported(t)
	h, _ := fakeGridAPI(t)
	dir := t.TempDir()
	path := harnessSettingsFile("claude-code", dir)
	h.Require.NoError(os.WriteFile(path, []byte(`{"model":"previous","theme":"dark","modelPicker":{"options":[{"model":"my/model","label":"Custom"}]}}`), 0o600))
	args := []string{"grid", "setup", "--harness", "claude-code", "--config-dir", dir}
	h.Require.NoError(h.Execute(append(args, "--dry-run", "--output", "json")...))
	var preview public.GridPlanList
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &preview))
	h.Require.Contains(preview.Items[0].ReplacedSettings, "model")
	h.Require.Contains(preview.Items[0].ReplacedSettings, "modelPicker.options")
	h.Require.NoError(h.Execute(append(args, "--dry-run")...))
	h.Require.Contains(h.Stdout.String(), "Existing integration settings will be overwritten")

	h.Require.NoError(h.Execute(append(args, "--yes")...))
	h.Require.Contains(h.Stderr.String(), "existing integration settings in claude-code will be overwritten")
	picker := readHarnessSettings(t, "claude-code", path)["modelPicker"].(map[string]any)
	h.Require.Equal([]any{map[string]any{"model": "acme/primary", "label": "Primary"}}, picker["options"])
	h.Require.NoError(h.Execute("grid", "teardown", "--harness", "claude-code", "--config-dir", dir, "--yes"))
	h.Require.Equal(map[string]any{"theme": "dark"}, readHarnessSettings(t, "claude-code", path))
}

func Test_Grid_Setup_Status_Teardown_DefaultDiscovery(t *testing.T) {
	skipUnlessSupported(t)
	h, api := fakeGridAPI(t)
	root := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(root, "claude"))
	t.Setenv("CODEX_HOME", filepath.Join(root, "codex"))
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(root, "pi"))
	h.Require.NoError(h.Execute("grid", "setup", "--harness", "claude-code", "--harness", "codex", "--yes", "--output", "json"))
	var setup public.GridPlanList
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &setup))
	h.Require.Len(setup.Items, 3) // Two settings files and the Codex model catalog.
	// A malformed file warns instead of hiding the other harnesses.
	openCode := filepath.Join(root, "opencode", "opencode.json")
	h.Require.NoError(os.MkdirAll(filepath.Dir(openCode), 0o700))
	h.Require.NoError(os.WriteFile(openCode, []byte("{ not json"), 0o600))
	calls := len(api.Calls())

	// Status and teardown find integrations whose executables are gone.
	h.Context = internalcmd.WithExecer(h.Context, missingHarnessExecer{})
	h.Require.NoError(h.Execute("grid", "status", "--output", "json"))
	h.Require.Contains(h.Stderr.String(), "warning: skipping opencode: settings at")
	h.Require.Contains(h.Stderr.String(), "are misconfigured")
	var statuses public.GridStatusList
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &statuses))
	h.Require.Len(statuses.Items, 2)
	for _, status := range statuses.Items {
		h.Require.False(status.Installed)
		h.Require.Equal("configured", status.State)
	}
	h.Require.Error(h.Execute("grid", "status", "--harness", "opencode"))
	h.Require.Contains(h.Stderr.String(), "invalid settings JSON")

	h.Require.Equal(calls, len(api.Calls()), "status is local")

	// Claude Code still uses the key, so removing Codex keeps it.
	h.Require.NoError(h.Execute("grid", "teardown", "--harness", "codex", "--yes"))
	h.Require.Equal(0, countCalls(api, "DELETE", "/v1/api_keys/secret-team-a"))
	h.Require.NoError(h.Execute("grid", "status", "--output", "json"))
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &statuses))
	h.Require.Len(statuses.Items, 1)
	// An unreadable file might still use the key, so teardown keeps it.
	h.Require.NoError(h.Execute("grid", "teardown", "--yes"))
	h.Require.Contains(h.Stderr.String(), "warning: keeping the Grid API key")
	h.Require.Equal(0, countCalls(api, "DELETE", "/v1/api_keys/secret-team-a"))
	h.Require.NoError(h.Execute("grid", "status"))
	h.Require.Contains(h.Stderr.String(), "No configured Baseten harnesses found.")
	h.Require.NoError(h.Execute("grid", "teardown", "--output", "json"))
	h.Require.JSONEq(`{"items":[]}`, h.Stdout.String())
}

func Test_Grid_Teardown_DeletesKeyWithLastHarness(t *testing.T) {
	skipUnlessSupported(t)
	h, api := fakeGridAPI(t)
	root := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(root, "claude"))
	t.Setenv("CODEX_HOME", filepath.Join(root, "codex"))
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(root, "pi"))
	h.Require.NoError(h.Execute("grid", "setup", "--harness", "claude-code", "--harness", "opencode", "--yes"))
	h.Require.NoError(h.Execute("grid", "teardown", "--harness", "opencode", "--yes"))
	h.Require.Equal(0, countCalls(api, "DELETE", "/v1/api_keys/secret-team-a"), "Claude Code still uses the key")
	h.Require.NoError(h.Execute("grid", "teardown", "--yes", "--output", "json"))
	var result public.GridPlanList
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &result))
	h.Require.Equal([]string{"secret-team-a"}, result.DeletedAPIKeys)
	h.Require.Equal(1, countCalls(api, "DELETE", "/v1/api_keys/secret-team-a"))

	// A failed delete leaves the settings in place so teardown can be retried.
	h.Require.NoError(h.Execute("grid", "setup", "--harness", "codex", "--yes"))
	api.SetRoute("DELETE", "/v1/api_keys/secret-team-a", 500, map[string]string{"message": "unavailable"})
	h.Require.Error(h.Execute("grid", "teardown", "--yes"))
	h.Require.Contains(h.Stderr.String(), "deleting Grid API key secret-team-a")
	h.Require.NoError(h.Execute("grid", "status", "--harness", "codex", "--output", "json"))
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

// Test_Grid_Setup_RealHarness configures each installed harness in an
// isolated directory, runs it, and checks that it asks the gateway for the
// configured route. Harnesses not on PATH are skipped.
//
// OpenCode 1 and 2 install the same `opencode` command, so the one on PATH is
// taken for OpenCode 1 and OpenCode 2 is the binary named by
// BASETEN_TEST_OPENCODE2, when set. OpenCode 2 reads both settings formats
// and runs with each; OpenCode 1 reads only the original.
func Test_Grid_Setup_RealHarness(t *testing.T) {
	skipUnlessSupported(t)
	type harnessCase struct {
		label, name, binary string
		args                []string
		// seed is a settings file in place before setup, which decides the
		// OpenCode settings format.
		seed string
	}
	cases := []harnessCase{
		{label: "claude-code", name: "claude-code", binary: "claude", args: []string{"-p", "Reply with OK."}},
		{label: "codex", name: "codex", binary: "codex", args: []string{"exec", "--skip-git-repo-check", "Reply with OK."}},
		{label: "opencode/v1", name: "opencode", binary: "opencode", args: []string{"run", "Reply with OK."}},
		{label: "pi", name: "pi", binary: "pi", args: []string{"--print", "Reply with OK."}},
	}
	if opencode2 := os.Getenv("BASETEN_TEST_OPENCODE2"); opencode2 != "" {
		// OpenCode 2 runs a shared background server per user by default; a
		// private one keeps the test off the developer's.
		args := []string{"run", "--standalone", "Reply with OK."}
		cases = append(cases,
			harnessCase{label: "opencode2/v1", name: "opencode", binary: opencode2, args: args},
			harnessCase{label: "opencode2/v2", name: "opencode", binary: opencode2, args: args, seed: `{"providers": {}}`},
		)
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			binary, err := exec.LookPath(tc.binary)
			if err != nil {
				t.Skipf("%s is not on PATH", tc.binary)
			}
			gateway := &harnessGateway{}
			server := httptest.NewServer(gateway)
			t.Cleanup(server.Close)
			h, _ := gridAPI(t, server.URL)
			root := t.TempDir()
			configDir := map[string]string{
				"claude-code": filepath.Join(root, ".claude"),
				"codex":       filepath.Join(root, ".codex"),
				"opencode":    filepath.Join(root, ".config", "opencode"),
				"pi":          filepath.Join(root, ".pi", "agent"),
			}[tc.name]
			if tc.seed != "" {
				h.Require.NoError(os.MkdirAll(configDir, 0o700))
				h.Require.NoError(os.WriteFile(harnessSettingsFile(tc.name, configDir), []byte(tc.seed), 0o600))
			}
			h.Require.NoError(h.Execute("grid", "setup", "--harness", tc.name, "--config-dir", configDir, "--yes"))

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

func Test_Grid_Setup_RouterRouteInPickers(t *testing.T) {
	skipUnlessSupported(t)
	h, api := fakeGridAPI(t)
	api.SetRoute("GET", "/v1/grid/routes", 200, map[string]any{
		"items": []any{
			map[string]any{"team_id": "team-a", "team_name": "Engineering", "id": "route-primary", "name": "acme/primary", "display_name": "Primary", "invoke_url": api.URL, "metadata": harnessRouteMetadata,
				"target": map[string]any{"type": "BASETEN_MODEL_API", "model": "deepseek"}},
			map[string]any{"team_id": "team-a", "team_name": "Engineering", "id": "route-auto", "name": "acme/auto", "display_name": "Auto", "invoke_url": api.URL, "metadata": harnessRouteMetadata,
				"target": map[string]any{"type": "ROUTER"}},
		},
		"pagination": map[string]any{"has_more": false},
	})
	root := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(root, "claude"))
	t.Setenv("CODEX_HOME", filepath.Join(root, "codex"))
	t.Setenv("XDG_CONFIG_HOME", root)
	h.Require.NoError(h.Execute("grid", "setup", "--harness", "claude-code", "--harness", "opencode", "--primary-route-id", "route-auto", "--background-route-id", "route-auto", "--yes"))
	h.Require.NoError(h.Execute("grid", "setup", "--harness", "codex", "--primary-route-id", "route-auto", "--yes"))
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

// serveFirstPartyRoutes serves an Anthropic route and an OpenAI route, each
// through a team connection, a Baseten-hosted route, and a route whose
// connection no longer exists.
func serveFirstPartyRoutes(api *MockManagementAPI) {
	api.SetRoute("GET", "/v1/grid/routes", 200, map[string]any{
		"items": []any{
			map[string]any{"team_id": "team-a", "team_name": "Engineering", "id": "route-a", "name": "acme/claude", "display_name": "Claude", "invoke_url": api.URL, "metadata": harnessRouteMetadata,
				"target": map[string]any{"type": "CONNECTION", "model": "claude-opus-5-5", "connection_id": "conn-anthropic"}},
			map[string]any{"team_id": "team-a", "team_name": "Engineering", "id": "route-b", "name": "acme/gpt", "display_name": "GPT", "invoke_url": api.URL, "metadata": harnessRouteMetadata,
				"target": map[string]any{"type": "CONNECTION", "model": "gpt-5.5", "connection_id": "conn-openai"}},
			map[string]any{"team_id": "team-a", "team_name": "Engineering", "id": "route-c", "name": "acme/open", "display_name": "Open", "invoke_url": api.URL, "metadata": harnessRouteMetadata,
				"target": map[string]any{"type": "BASETEN_MODEL_API", "model": "deepseek"}},
			map[string]any{"team_id": "team-a", "team_name": "Engineering", "id": "route-d", "name": "acme/orphan", "display_name": "Orphan", "invoke_url": api.URL, "metadata": harnessRouteMetadata,
				"target": map[string]any{"type": "CONNECTION", "model": "gpt-5.5", "connection_id": "conn-deleted"}},
		},
		"pagination": map[string]any{"has_more": false},
	})
	// Connections page, so the join reads every page.
	api.SetRouteFunc("GET", "/v1/grid/connections", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("cursor") == "" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"items": []any{map[string]any{"id": "conn-anthropic", "team_id": r.URL.Query().Get("team_id"), "created_at": "2026-09-01T00:00:00Z", "updated_at": "2026-09-01T00:00:00Z",
					"config": map[string]any{"provider": "ANTHROPIC", "secret_id": "s1", "secret_name": "anthropic-key"}}},
				"pagination": map[string]any{"has_more": true, "cursor": "page-two"},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"items": []any{map[string]any{"id": "conn-openai", "team_id": r.URL.Query().Get("team_id"), "created_at": "2026-09-01T00:00:00Z", "updated_at": "2026-09-01T00:00:00Z",
				"config": map[string]any{"provider": "OPENAI", "secret_id": "s2", "secret_name": "openai-key"}}},
			"pagination": map[string]any{"has_more": false},
		})
	})
}

func Test_Grid_Setup_OpenCodeFirstPartyRoutes(t *testing.T) {
	skipUnlessSupported(t)
	h, api := fakeGridAPI(t)
	serveFirstPartyRoutes(api)
	dir := t.TempDir()
	path := harnessSettingsFile("opencode", dir)
	h.Require.NoError(h.Execute("grid", "setup", "--harness", "opencode", "--config-dir", dir, "--yes"))
	provider := readHarnessSettings(t, "opencode", path)["provider"].(map[string]any)["baseten-harness"].(map[string]any)
	models := provider["models"].(map[string]any)
	h.Require.Equal(map[string]any{"npm": "@ai-sdk/anthropic"}, models["acme/claude"].(map[string]any)["provider"])
	h.Require.Equal(map[string]any{"npm": "@ai-sdk/openai"}, models["acme/gpt"].(map[string]any)["provider"])
	h.Require.NotContains(models["acme/open"], "provider")
	h.Require.NotContains(models["acme/orphan"], "provider", "a route whose connection is unknown is called with Chat Completions")
	h.Require.Contains(h.Stderr.String(), "warning: routes whose connection is not in the team's connections are called with the Chat Completions API: acme/orphan")
	connections := api.FindCall("GET", "/v1/grid/connections")
	h.Require.NotNil(connections)
	h.Require.Contains(connections.RawQuery, "team_id=team-a")
}

func Test_Grid_Setup_PiFirstPartyRoutes(t *testing.T) {
	skipUnlessSupported(t)
	h, api := fakeGridAPI(t)
	serveFirstPartyRoutes(api)
	dir := t.TempDir()
	h.Require.NoError(h.Execute("grid", "setup", "--harness", "pi", "--config-dir", dir, "--yes"))
	provider := readHarnessSettings(t, "pi", harnessSettingsFile("pi", dir))["providers"].(map[string]any)["baseten-harness"].(map[string]any)
	apis := map[string]any{}
	for _, m := range provider["models"].([]any) {
		apis[m.(map[string]any)["id"].(string)] = m.(map[string]any)["api"]
	}
	h.Require.Equal(map[string]any{"acme/claude": "anthropic-messages", "acme/gpt": "openai-responses", "acme/open": nil, "acme/orphan": nil}, apis)
	settings := readHarnessSettings(t, "pi", filepath.Join(dir, "settings.json"))
	h.Require.Equal(map[string]any{"defaultProvider": "baseten-harness", "defaultModel": "acme/claude"}, settings)
}

func Test_Grid_Setup_RouteMetadata(t *testing.T) {
	skipUnlessSupported(t)
	h, api := fakeGridAPI(t)
	api.SetRouteFunc("GET", "/v1/grid/routes", func(w http.ResponseWriter, r *http.Request) {
		teamID, teamName := gridRouteTeam(r)
		w.Header().Set("Content-Type", "application/json")
		// The second page holds a route without metadata.
		if r.URL.Query().Get("cursor") == "" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"items":      []any{map[string]any{"team_id": teamID, "team_name": teamName, "id": "route-primary", "name": "acme/primary", "display_name": "Primary", "invoke_url": api.URL, "metadata": harnessRouteMetadata}},
				"pagination": map[string]any{"has_more": true, "cursor": "page-two"},
			})
			return
		}
		// A skipped route's invoke URL doesn't have to match.
		_ = json.NewEncoder(w).Encode(map[string]any{
			"items":      []any{map[string]any{"team_id": teamID, "team_name": teamName, "id": "route-bare", "name": "acme/bare", "display_name": "Bare", "invoke_url": "https://elsewhere.example.com", "metadata": nil}},
			"pagination": map[string]any{"has_more": false},
		})
	})
	dir := t.TempDir()
	h.Require.NoError(h.Execute("grid", "setup", "--harness", "claude-code", "--config-dir", dir, "--yes"))
	h.Require.Contains(h.Stderr.String(), "warning: skipping routes without usable model metadata: acme/bare")
	h.Require.NotContains(fmt.Sprint(readHarnessSettings(t, "claude-code", harnessSettingsFile("claude-code", dir))["availableModels"]), "acme/bare")

	h.Stderr.Reset()
	h.Require.Error(h.Execute("grid", "setup", "--harness", "claude-code", "--primary-route-id", "route-bare", "--config-dir", t.TempDir(), "--yes"))
	h.Require.Contains(h.Stderr.String(), `route "acme/bare" has no model metadata`)
	h.Require.Equal(1, countCalls(api, "POST", "/v1/teams/team-a/api_keys"), "a failed setup creates no key")

	// A route's cost reaches OpenCode, and needs both input and output prices.
	priced := maps.Clone(harnessRouteMetadata)
	priced["cost"] = map[string]any{"input": 0.3, "output": 1.2, "cache_read": nil, "long_context": map[string]any{"input": 0.6}}
	api.SetRoute("GET", "/v1/grid/routes", 200, map[string]any{
		"items":      []any{map[string]any{"team_id": "team-a", "team_name": "Engineering", "id": "route-primary", "name": "acme/primary", "display_name": "Primary", "invoke_url": api.URL, "metadata": priced}},
		"pagination": map[string]any{"has_more": false},
	})
	opencode := t.TempDir()
	h.Require.NoError(h.Execute("grid", "setup", "--harness", "opencode", "--config-dir", opencode, "--yes"))
	provider := readHarnessSettings(t, "opencode", harnessSettingsFile("opencode", opencode))["provider"].(map[string]any)
	model := provider["baseten-harness"].(map[string]any)["models"].(map[string]any)["acme/primary"].(map[string]any)
	h.Require.Equal(map[string]any{"input": 0.3, "output": 1.2}, model["cost"])
}

func Test_Grid_Setup_RoutesByAPIFormat(t *testing.T) {
	skipUnlessSupported(t)
	h, api := fakeGridAPI(t)
	formats := func(messages, responses bool) map[string]any {
		m := maps.Clone(harnessRouteMetadata)
		m["supported_api_formats"] = map[string]any{"messages": messages, "responses": responses}
		return m
	}
	unknown := maps.Clone(harnessRouteMetadata)
	delete(unknown, "supported_api_formats")
	api.SetRoute("GET", "/v1/grid/routes", 200, map[string]any{
		"items": []any{
			map[string]any{"team_id": "team-a", "team_name": "Engineering", "id": "route-claude", "name": "acme/claude", "display_name": "Claude", "invoke_url": api.URL, "metadata": formats(true, false)},
			map[string]any{"team_id": "team-a", "team_name": "Engineering", "id": "route-gpt", "name": "acme/gpt", "display_name": "GPT", "invoke_url": api.URL, "metadata": formats(false, true)},
			map[string]any{"team_id": "team-a", "team_name": "Engineering", "id": "route-open", "name": "acme/open", "display_name": "Open", "invoke_url": api.URL, "metadata": formats(true, true)},
			map[string]any{"team_id": "team-a", "team_name": "Engineering", "id": "route-new", "name": "acme/new", "display_name": "New", "invoke_url": api.URL, "metadata": unknown},
		},
		"pagination": map[string]any{"has_more": false},
	})
	root := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(root, "claude"))
	t.Setenv("CODEX_HOME", filepath.Join(root, "codex"))
	h.Require.NoError(h.Execute("grid", "setup", "--harness", "claude-code", "--harness", "codex", "--yes"))

	claude := readHarnessSettings(t, "claude-code", filepath.Join(root, "claude", "settings.json"))
	h.Require.Equal("acme/claude", claude["model"])
	h.Require.Equal([]any{"acme/claude", "acme/open", "acme/new", "deepseek-ai/DeepSeek-V4.1-Flash"}, claude["availableModels"])
	h.Require.Equal("acme/gpt", readHarnessSettings(t, "codex", filepath.Join(root, "codex", "config.toml"))["model"])
	h.Require.Equal([]string{"acme/gpt", "acme/open", "acme/new"}, catalogSlugs(t, filepath.Join(root, "codex", "baseten-models.json")))

	h.Require.Error(h.Execute("grid", "setup", "--harness", "claude-code", "--primary-route-id", "route-gpt", "--yes"))
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

func Test_Grid_Setup_RestartsCodexDaemon(t *testing.T) {
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
			h, _ := gridAPI(t, "")
			h.Context = internalcmd.WithExecer(h.Context, tc.execer)
			h.Require.NoError(h.Execute("grid", "setup", "--harness", "codex", "--config-dir", t.TempDir(), "--yes"))
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

func Test_Grid_Setup_SignsOutCodex(t *testing.T) {
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
			h, _ := gridAPI(t, "")
			h.Context = internalcmd.WithExecer(h.Context, tc.execer)
			dir := t.TempDir()
			auth := filepath.Join(dir, "auth.json")
			cache := filepath.Join(dir, "cloud-config-bundle-cache.json")
			for path, present := range map[string]bool{auth: tc.auth, cache: tc.cache} {
				if present {
					h.Require.NoError(os.WriteFile(path, []byte("{}"), 0o600))
				}
			}
			h.Require.NoError(h.Execute("grid", "setup", "--harness", "codex", "--config-dir", dir, "--yes"))
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

func Test_Grid_Teardown_RestartsCodexDaemon(t *testing.T) {
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
			h, _ := gridAPI(t, "")
			h.Context = internalcmd.WithExecer(h.Context, fakeHarnessExecer{})
			dir := t.TempDir()
			h.Require.NoError(h.Execute("grid", "setup", "--harness", "codex", "--config-dir", dir, "--key-name", "laptop", "--yes"))
			h.Context = internalcmd.WithExecer(h.Context, tc.execer)
			h.Require.NoError(h.Execute("grid", "teardown", "--harness", "codex", "--config-dir", dir, "--yes"))
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

// harnessRoute is a team-a route in the fake API, whose invoke URL is the API's
// own. acme/gpt serves only the Responses API, so Claude Code can't call it.
func harnessRoute(api *MockManagementAPI, name string) map[string]any {
	metadata := harnessRouteMetadata
	if name == "gpt" {
		metadata = maps.Clone(harnessRouteMetadata)
		metadata["supported_api_formats"] = map[string]any{"messages": false, "responses": true}
	}
	return map[string]any{
		"id": "route-" + name, "name": "acme/" + name, "display_name": "acme/" + name,
		"team_id": "team-a", "team_name": "Engineering", "invoke_url": api.URL, "metadata": metadata,
	}
}

// harnessDefaultsPath is team-a's route settings, which hold its harness defaults.
const harnessDefaultsPath = "/v1/grid/team_settings"

// harnessDefaultsBody is a team settings response with the given harness defaults.
func harnessDefaultsBody(defaults map[string]any) map[string]any {
	return map[string]any{"team_id": "team-a", "team_name": "Engineering", "harness_defaults": defaults}
}

// harnessDefaultsAPI is fakeGridAPI with four team-a routes and the given
// harness defaults, and harness settings directories under a temporary root.
func harnessDefaultsAPI(t *testing.T, defaults map[string]any) (*CommandHarness, *MockManagementAPI, string) {
	t.Helper()
	h, api := fakeGridAPI(t)
	var routes []any
	for _, name := range []string{"primary", "claude", "fast", "gpt"} {
		routes = append(routes, harnessRoute(api, name))
	}
	api.SetRoute("GET", "/v1/grid/routes", 200, map[string]any{"items": routes, "pagination": map[string]any{"has_more": false}})
	api.SetRoute("GET", harnessDefaultsPath, 200, harnessDefaultsBody(defaults))
	root := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(root, "claude"))
	t.Setenv("CODEX_HOME", filepath.Join(root, "codex"))
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(root, "pi"))
	return h, api, root
}

// harnessDefaultModel is one role of a harness default.
func harnessDefaultModel(source string, route map[string]any) map[string]any {
	return map[string]any{"source": source, "route": route}
}

// harnessSetupRoutes reads the primary and background routes each harness's settings name.
func harnessSetupRoutes(t *testing.T, root string) map[string][2]string {
	t.Helper()
	claude := readHarnessSettings(t, "claude-code", filepath.Join(root, "claude", "settings.json"))
	env := claude["env"].(map[string]any)
	h := map[string][2]string{"claude-code": {claude["model"].(string), env["ANTHROPIC_DEFAULT_HAIKU_MODEL"].(string)}}
	if path := filepath.Join(root, "opencode", "opencode.json"); fileExists(path) {
		openCode := readHarnessSettings(t, "opencode", path)
		h["opencode"] = [2]string{
			strings.TrimPrefix(openCode["model"].(string), "baseten-harness/"),
			strings.TrimPrefix(openCode["small_model"].(string), "baseten-harness/"),
		}
	}
	if path := filepath.Join(root, "codex", "config.toml"); fileExists(path) {
		h["codex"] = [2]string{readHarnessSettings(t, "codex", path)["model"].(string), ""}
	}
	if path := filepath.Join(root, "pi", "settings.json"); fileExists(path) {
		h["pi"] = [2]string{readHarnessSettings(t, "pi", path)["defaultModel"].(string), ""}
	}
	return h
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func Test_Grid_Setup_Defaults(t *testing.T) {
	skipUnlessSupported(t)
	h, api, root := harnessDefaultsAPI(t, nil)
	route := func(name string) map[string]any { return harnessRoute(api, name) }
	api.SetRoute("GET", harnessDefaultsPath, 200, harnessDefaultsBody(map[string]any{
		"claude_code": map[string]any{
			"primary":    harnessDefaultModel("team", route("claude")),
			"background": harnessDefaultModel("team", route("fast")),
		},
		"opencode": map[string]any{
			"primary":    harnessDefaultModel("team", route("gpt")),
			"background": harnessDefaultModel("team", route("claude")),
		},
		"codex": map[string]any{
			"primary": harnessDefaultModel("team", route("gpt")),
		},
		"pi": map[string]any{
			"primary": harnessDefaultModel("team", route("claude")),
		},
	}))
	// Every harness, so one setup can't read defaults for has to be added here.
	args := []string{"grid", "setup"}
	for _, choice := range harness.All() {
		args = append(args, "--harness", choice.Name())
	}

	h.Require.NoError(h.Execute(append(args, "--dry-run")...))
	h.Require.Equal(1, countCalls(api, "GET", harnessDefaultsPath), "setup reads the setup team's defaults once")
	for _, want := range []string{
		"Primary route     acme/claude (team default)",
		"Background route  acme/fast (team default)",
		"Primary route     acme/gpt (team default)",
		"Background route  acme/claude (team default)",
	} {
		h.Require.Contains(h.Stdout.String(), want)
	}
	h.Require.NotContains(h.Stderr.String(), "warning")

	h.Require.NoError(h.Execute(append(args, "--yes")...))
	h.Require.Equal(map[string][2]string{
		"claude-code": {"acme/claude", "acme/fast"},
		"opencode":    {"acme/gpt", "acme/claude"},
		"codex":       {"acme/gpt", ""},
		"pi":          {"acme/claude", ""},
	}, harnessSetupRoutes(t, root))
	env := readHarnessSettings(t, "claude-code", filepath.Join(root, "claude", "settings.json"))["env"].(map[string]any)
	h.Require.Equal("acme/fast", env["ANTHROPIC_SMALL_FAST_MODEL"])
	h.Require.Equal("acme/claude", env["ANTHROPIC_MODEL"], "the primary also beats an organization's claude.ai default")

	// Flags override the defaults for every harness.
	h.Require.NoError(h.Execute("grid", "setup", "--harness", "claude-code", "--harness", "opencode", "--primary-route-id", "route-primary", "--background-route-id", "route-claude", "--dry-run"))
	h.Require.Contains(h.Stdout.String(), "Primary route     acme/primary (flag)")
	h.Require.Contains(h.Stdout.String(), "Background route  acme/claude (flag)")
	h.Require.NotContains(h.Stdout.String(), " default)")
	h.Require.NoError(h.Execute("grid", "setup", "--harness", "claude-code", "--harness", "opencode", "--primary-route-id", "route-primary", "--background-route-id", "route-claude", "--yes"))
	h.Require.Equal([2]string{"acme/primary", "acme/claude"}, harnessSetupRoutes(t, root)["claude-code"])
	h.Require.Equal([2]string{"acme/primary", "acme/claude"}, harnessSetupRoutes(t, root)["opencode"])
	env = readHarnessSettings(t, "claude-code", filepath.Join(root, "claude", "settings.json"))["env"].(map[string]any)
	h.Require.Equal("acme/primary", env["ANTHROPIC_MODEL"], "a refresh updates the pinned primary")

	// A primary flag keeps the background default.
	h.Require.NoError(h.Execute("grid", "setup", "--harness", "claude-code", "--primary-route-id", "route-primary", "--yes"))
	h.Require.Equal([2]string{"acme/primary", "acme/fast"}, harnessSetupRoutes(t, root)["claude-code"])
}

func Test_Grid_Setup_DefaultsSetupCannotUse(t *testing.T) {
	skipUnlessSupported(t)
	h, api, root := harnessDefaultsAPI(t, nil)
	// The API doesn't check that the harness can call a route, and acme/gpt
	// serves only the Responses API.
	gpt := harnessRoute(api, "gpt")
	api.SetRoute("GET", harnessDefaultsPath, 200, harnessDefaultsBody(map[string]any{
		"claude_code": map[string]any{
			"primary":    harnessDefaultModel("team", gpt),
			"background": harnessDefaultModel("team", gpt),
		},
	}))
	// `team` also covers the defaults chosen from the team's Model APIs, which
	// no admin set, so a default setup can't use doesn't warn.
	h.Require.NoError(h.Execute("grid", "setup", "--harness", "claude-code", "--yes"))
	h.Require.NotContains(h.Stderr.String(), "warning")
	// Skipped roles keep the CLI's defaults.
	h.Require.Equal([2]string{"acme/primary", "deepseek-ai/DeepSeek-V4.1-Flash"}, harnessSetupRoutes(t, root)["claude-code"])

	h.Require.NoError(h.Execute("grid", "setup", "--harness", "claude-code", "--verbose", "--dry-run"))
	for _, role := range []string{"primary", "background"} {
		h.Require.Contains(h.Stderr.String(), "Ignoring the team default for the claude-code "+role+" route: claude-code can't call route acme/gpt\n")
	}

	// A flag replaces a default setup can't use, without a warning.
	h.Require.NoError(h.Execute("grid", "setup", "--harness", "claude-code", "--primary-route-id", "route-claude", "--background-route-id", "route-fast", "--yes"))
	h.Require.NotContains(h.Stderr.String(), "ignoring the")
}

func Test_Grid_Setup_DefaultsUnknownSource(t *testing.T) {
	skipUnlessSupported(t)
	h, api, root := harnessDefaultsAPI(t, nil)
	// Not a source the API returns today: it stands in for one added after this CLI was built.
	api.SetRoute("GET", harnessDefaultsPath, 200, harnessDefaultsBody(map[string]any{
		"opencode": map[string]any{
			"primary": harnessDefaultModel("organization", harnessRoute(api, "claude")),
		},
	}))
	h.Require.NoError(h.Execute("grid", "setup", "--harness", "opencode", "--dry-run"))
	h.Require.Contains(h.Stdout.String(), "Primary route     acme/claude (default)")
	h.Require.NotContains(h.Stderr.String(), "warning")
	h.Require.NoError(h.Execute("grid", "setup", "--harness", "opencode", "--yes"))
	openCode := readHarnessSettings(t, "opencode", filepath.Join(root, "opencode", "opencode.json"))
	h.Require.Equal("baseten-harness/acme/claude", openCode["model"])
}

func Test_Grid_Setup_DefaultsSkippedWhenFlagsSetEveryRoute(t *testing.T) {
	skipUnlessSupported(t)
	h, api, _ := harnessDefaultsAPI(t, map[string]any{})
	for _, args := range [][]string{
		{"--harness", "claude-code", "--harness", "opencode", "--primary-route-id", "route-primary", "--background-route-id", "route-fast"},
		// Codex has no background route, so --primary-route-id sets every route it uses.
		{"--harness", "codex", "--primary-route-id", "route-primary"},
		// Pi also has only a primary route.
		{"--harness", "pi", "--primary-route-id", "route-primary"},
	} {
		h.Require.NoError(h.Execute(append([]string{"grid", "setup", "--dry-run"}, args...)...))
	}
	h.Require.Equal(0, countCalls(api, "GET", harnessDefaultsPath))
	h.Require.NoError(h.Execute("grid", "setup", "--dry-run", "--harness", "claude-code", "--primary-route-id", "route-primary"))
	h.Require.Equal(1, countCalls(api, "GET", harnessDefaultsPath), "the background route still comes from the defaults")
}

func Test_Grid_Setup_DefaultsInterrupted(t *testing.T) {
	skipUnlessSupported(t)
	h, api, _ := harnessDefaultsAPI(t, nil)
	ctx, cancel := context.WithCancel(h.Context)
	h.Context = ctx
	api.SetRouteFunc("GET", harnessDefaultsPath, func(w http.ResponseWriter, r *http.Request) {
		cancel()
		w.WriteHeader(http.StatusInternalServerError)
	})
	h.Require.Error(h.Execute("grid", "setup", "--harness", "claude-code", "--yes"))
	h.Require.Equal(130, h.ExitCode)
	h.Require.NotContains(h.Stderr.String(), "could not read harness defaults")
	h.Require.Equal(0, countCalls(api, "POST", "/v1/teams/team-a/api_keys"), "an interrupted setup creates no key")
}

func Test_Grid_Setup_NoDefaults(t *testing.T) {
	skipUnlessSupported(t)
	for name, respond := range map[string]func(*MockManagementAPI){
		"empty": func(api *MockManagementAPI) {},
		"not found": func(api *MockManagementAPI) {
			api.SetRoute("GET", harnessDefaultsPath, 404, map[string]string{"error": "not found"})
		},
		"forbidden": func(api *MockManagementAPI) {
			api.SetRoute("GET", harnessDefaultsPath, 403, map[string]string{"error": "forbidden"})
		},
		"failing": func(api *MockManagementAPI) {
			api.SetRoute("GET", harnessDefaultsPath, 500, map[string]string{"error": "unavailable"})
		},
	} {
		t.Run(name, func(t *testing.T) {
			h, api, root := harnessDefaultsAPI(t, map[string]any{})
			respond(api)
			h.Require.NoError(h.Execute("grid", "setup", "--harness", "claude-code", "--harness", "opencode", "--harness", "codex", "--dry-run"))
			h.Require.Contains(h.Stdout.String(), "Primary route     acme/primary (CLI default)")
			h.Require.Contains(h.Stdout.String(), "Background route  deepseek-ai/DeepSeek-V4.1-Flash (CLI default)")
			h.Require.NoError(h.Execute("grid", "setup", "--harness", "claude-code", "--harness", "opencode", "--harness", "codex", "--yes"))
			if name == "failing" {
				h.Require.Contains(h.Stderr.String(), "warning: could not read harness defaults, using CLI defaults")
			} else {
				h.Require.NotContains(h.Stderr.String(), "warning")
			}
			h.Require.Equal(map[string][2]string{
				"claude-code": {"acme/primary", "deepseek-ai/DeepSeek-V4.1-Flash"},
				"opencode":    {"acme/primary", "deepseek-ai/DeepSeek-V4.1-Flash"},
				"codex":       {"acme/primary", ""},
			}, harnessSetupRoutes(t, root))
		})
	}
}

func Test_Grid_Setup_DryRunSkipsCodexSignOut(t *testing.T) {
	skipUnlessSupported(t)
	h, _ := gridAPI(t, "")
	execer := &codexDaemonHarnessExecer{loggedIn: true, socket: "/tmp/unused.sock"}
	h.Context = internalcmd.WithExecer(h.Context, execer)
	h.Require.NoError(h.Execute("grid", "setup", "--harness", "codex", "--config-dir", t.TempDir(), "--dry-run"))
	h.Require.Equal(0, execer.probes)
	h.Require.Equal(0, execer.logouts)
	h.Require.Equal(0, execer.restarts)
	h.Require.NotContains(h.Stderr.String(), "Signing codex out")
}

// harnessRecordFile reads the harness.json in the test's BASETEN_CONFIG_DIR.
func harnessRecordFile(t *testing.T) harness.RecordFile {
	t.Helper()
	dir, err := auth.DefaultConfigDir()
	require.NoError(t, err)
	file, err := harness.LoadRecords(dir)
	require.NoError(t, err)
	return file
}

func saveHarnessRecordFile(t *testing.T, file harness.RecordFile) {
	t.Helper()
	dir, err := auth.DefaultConfigDir()
	require.NoError(t, err)
	require.NoError(t, harness.SaveRecords(dir, file))
}

// setHarnessRoutes serves a route per name, each with usable metadata.
func setHarnessRoutes(api *MockManagementAPI, names ...string) {
	api.SetRouteFunc("GET", "/v1/grid/routes", func(w http.ResponseWriter, r *http.Request) {
		teamID, teamName := gridRouteTeam(r)
		w.Header().Set("Content-Type", "application/json")
		items := []any{}
		for _, name := range names {
			items = append(items, map[string]any{"id": "route-" + strings.TrimPrefix(name, "acme/"), "name": name, "team_id": teamID, "team_name": teamName, "display_name": name, "invoke_url": api.URL, "metadata": harnessRouteMetadata})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": items, "pagination": map[string]any{"has_more": false}})
	})
}

// recordDir returns the test's BASETEN_CONFIG_DIR.
func recordDir(t *testing.T) string {
	t.Helper()
	dir, err := auth.DefaultConfigDir()
	require.NoError(t, err)
	return dir
}

func Test_Grid_Setup_RecordsSyncOptions(t *testing.T) {
	skipUnlessSupported(t)
	h, api := gridAPI(t, "")
	api.SetRoute("GET", "/v1/teams", 200, gridTwoTeams)
	setHarnessRoutes(api, "acme/primary", "acme/second")
	h.Context = internalcmd.WithExecer(h.Context, fakeHarnessExecer{})
	dir := t.TempDir()
	h.Require.NoError(h.Execute("grid", "sync", "--yes"))
	h.Require.Contains(h.Stderr.String(), "No harnesses to sync")

	// A dry run records nothing.
	args := []string{"grid", "setup", "--harness", "claude-code", "--config-dir", dir, "--team", "team-b", "--key-name", "laptop", "--primary-route-id", "route-primary", "--background-route-id", "route-second"}
	h.Require.NoError(h.Execute(append(args, "--dry-run")...))
	h.Require.Empty(harnessRecordFile(t).Harnesses)

	h.Require.NoError(h.Execute(append(args, "--yes")...))
	record, ok := harnessRecordFile(t).Harnesses["claude-code"]
	h.Require.True(ok, "setup records the harness")
	h.Require.Equal(filepath.Join(dir, "settings.json"), record.Config)
	h.Require.Equal(api.URL, record.ManagementURL)
	h.Require.Equal("team-b", record.TeamID)
	h.Require.Equal("laptop", record.KeyName)
	h.Require.Equal("acme/primary", record.Route)
	h.Require.Equal("acme/second", record.BackgroundRoute)
	h.Require.Empty(record.SubagentRoute, "unset flags stay empty so sync follows defaults")
	h.Require.Empty(record.Profile, "an ephemeral session has no profile name")

	// A record from another profile is not synced.
	file := harnessRecordFile(t)
	record.Profile = "someone-else@example.com"
	file.Harnesses["claude-code"] = record
	saveHarnessRecordFile(t, file)
	h.Require.NoError(h.Execute("grid", "sync", "--yes"))
	h.Require.Contains(h.Stderr.String(), "No harnesses to sync")

	// Teardown forgets the record.
	h.Require.NoError(h.Execute("grid", "teardown", "--harness", "claude-code", "--config-dir", dir, "--yes"))
	h.Require.NotContains(harnessRecordFile(t).Harnesses, "claude-code")

	// So does teardown of a hand-reverted harness, which has no settings left
	// to remove; a dry run keeps the record.
	h.Require.NoError(h.Execute(append(args, "--yes")...))
	h.Require.NoError(os.Remove(filepath.Join(dir, "settings.json")))
	h.Require.NoError(h.Execute("grid", "teardown", "--harness", "claude-code", "--config-dir", dir, "--dry-run"))
	h.Require.Contains(harnessRecordFile(t).Harnesses, "claude-code")
	h.Require.NoError(h.Execute("grid", "teardown", "--harness", "claude-code", "--config-dir", dir))
	h.Require.Contains(h.Stderr.String(), "No Baseten settings to remove.")
	h.Require.NotContains(harnessRecordFile(t).Harnesses, "claude-code")
}

func Test_Grid_Sync(t *testing.T) {
	skipUnlessSupported(t)
	h, api := gridAPI(t, "")
	setHarnessRoutes(api, "acme/primary")
	h.Context = internalcmd.WithExecer(h.Context, fakeHarnessExecer{})
	dir := t.TempDir()
	h.Require.NoError(h.Execute("grid", "setup", "--harness", "claude-code", "--config-dir", dir, "--yes"))

	// A configured harness whose record is gone (setup by an older CLI) is skipped.
	require.NoError(t, os.Remove(filepath.Join(recordDir(t), "harness.json")))
	h.Require.NoError(h.Execute("grid", "sync", "--yes"))
	h.Require.Contains(h.Stderr.String(), "No harnesses to sync")
	h.Require.Equal(0, h.ExitCode)

	// With a record and nothing changed, sync applies nothing without prompting
	// (stdin is not a terminal) and creates no key.
	h.Require.NoError(h.Execute("grid", "setup", "--harness", "claude-code", "--config-dir", dir, "--yes"))
	keys := countCalls(api, "POST", "/v1/teams/team-a/api_keys")
	h.Require.NoError(h.Execute("grid", "sync"))
	h.Require.Contains(h.Stderr.String(), "All configured harnesses are up to date.")
	h.Require.Equal(keys, countCalls(api, "POST", "/v1/teams/team-a/api_keys"))

	// A changed catalog needs confirmation, then applies with the recorded key.
	setHarnessRoutes(api, "acme/primary", "acme/second")
	h.Require.Error(h.Execute("grid", "sync"))
	h.Require.Contains(h.Stderr.String(), "pass --yes")
	h.Require.Equal(keys, countCalls(api, "POST", "/v1/teams/team-a/api_keys"), "no key before confirmation")
	h.Require.NoError(h.Execute("grid", "sync", "--yes", "--output", "json"))
	var result public.GridPlanList
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &result))
	for _, item := range result.Items {
		h.Require.True(item.Changed)
	}
	picker := readHarnessSettings(t, "claude-code", harnessSettingsFile("claude-code", dir))["modelPicker"].(map[string]any)
	h.Require.Len(picker["options"], 2)
	h.Require.Equal(keys, countCalls(api, "POST", "/v1/teams/team-a/api_keys"), "sync reuses the recorded key")

	// Nothing changed again: up to date.
	h.Require.NoError(h.Execute("grid", "sync", "--yes"))
	h.Require.Contains(h.Stderr.String(), "All configured harnesses are up to date.")
}

func Test_Grid_Sync_FallsBackWhenRouteVanishes(t *testing.T) {
	skipUnlessSupported(t)
	h, api := gridAPI(t, "")
	setHarnessRoutes(api, "acme/primary", "acme/bg")
	h.Context = internalcmd.WithExecer(h.Context, fakeHarnessExecer{})
	dir := t.TempDir()
	h.Require.NoError(h.Execute("grid", "setup", "--harness", "claude-code", "--config-dir", dir, "--background-route-id", "route-bg", "--yes"))
	settings := readHarnessSettings(t, "claude-code", harnessSettingsFile("claude-code", dir))
	h.Require.Equal("acme/bg", settings["env"].(map[string]any)["ANTHROPIC_DEFAULT_HAIKU_MODEL"])

	// The team removes acme/bg; sync falls back to the default and records it.
	setHarnessRoutes(api, "acme/primary")
	h.Require.NoError(h.Execute("grid", "sync", "--yes"))
	h.Require.Contains(h.Stderr.String(), `warning: claude-code: background route "acme/bg" no longer exists; using "deepseek-ai/DeepSeek-V4.1-Flash"`)
	settings = readHarnessSettings(t, "claude-code", harnessSettingsFile("claude-code", dir))
	h.Require.Equal("deepseek-ai/DeepSeek-V4.1-Flash", settings["env"].(map[string]any)["ANTHROPIC_DEFAULT_HAIKU_MODEL"])
	h.Require.Empty(harnessRecordFile(t).Harnesses["claude-code"].BackgroundRoute, "the fallback is recorded for the next sync")

	// The next sync is up to date and does not warn again.
	h.Require.NoError(h.Execute("grid", "sync", "--yes"))
	h.Require.Contains(h.Stderr.String(), "All configured harnesses are up to date.")
	h.Require.NotContains(h.Stderr.String(), "no longer exists")
}

func Test_Grid_Sync_ServerDefaults(t *testing.T) {
	skipUnlessSupported(t)
	for _, tc := range []struct {
		name, apiName string
		background    bool
	}{
		{"claude-code", "claude_code", true},
		{"opencode", "opencode", true},
		{"codex", "codex", false},
		{"pi", "pi", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, api, _ := harnessDefaultsAPI(t, nil)
			dir := t.TempDir()
			setDefaults := func(primary, background string) {
				roles := map[string]any{"primary": harnessDefaultModel("team", harnessRoute(api, primary))}
				if tc.background {
					roles["background"] = harnessDefaultModel("team", harnessRoute(api, background))
				}
				api.SetRoute("GET", harnessDefaultsPath, 200, harnessDefaultsBody(map[string]any{tc.apiName: roles}))
			}
			primaryModel := func() any {
				path := harnessSettingsFile(tc.name, dir)
				key := "model"
				if tc.name == "pi" {
					path, key = filepath.Join(dir, "settings.json"), "defaultModel"
				}
				return readHarnessSettings(t, tc.name, path)[key]
			}
			modelID := func(name string) string {
				if tc.name == "opencode" {
					return "baseten-harness/acme/" + name
				}
				return "acme/" + name
			}
			setup := []string{"grid", "setup", "--harness", tc.name, "--config-dir", dir, "--yes"}
			setDefaults("claude", "fast")
			h.Require.NoError(h.Execute(setup...))
			h.Require.Equal(modelID("claude"), primaryModel())
			h.Require.Empty(harnessRecordFile(t).Harnesses[tc.name].Route)

			setDefaults("fast", "claude")
			h.Require.NoError(h.Execute("grid", "sync", "--dry-run"))
			h.Require.Contains(h.Stdout.String(), "acme/fast (team default)")
			h.Require.Equal(modelID("claude"), primaryModel())
			h.Require.NoError(h.Execute("grid", "sync", "--yes"))
			h.Require.Equal(modelID("fast"), primaryModel())
			h.Require.Empty(harnessRecordFile(t).Harnesses[tc.name].Route)

			setup = append(setup, "--primary-route-id", "route-primary")
			if tc.background {
				setup = append(setup, "--background-route-id", "route-fast")
			}
			calls := countCalls(api, "GET", harnessDefaultsPath)
			h.Require.NoError(h.Execute(setup...))
			h.Require.Equal(calls, countCalls(api, "GET", harnessDefaultsPath))

			setDefaults("claude", "claude")
			setHarnessRoutes(api, "acme/claude")
			h.Require.NoError(h.Execute("grid", "sync", "--dry-run"))
			h.Require.Equal("acme/primary", harnessRecordFile(t).Harnesses[tc.name].Route)
			h.Require.Equal(modelID("primary"), primaryModel())
			h.Require.NoError(h.Execute("grid", "sync", "--yes"))
			h.Require.Greater(countCalls(api, "GET", harnessDefaultsPath), calls)
			h.Require.Contains(h.Stderr.String(), `primary route "acme/primary" no longer exists; using "acme/claude"`)
			h.Require.Equal(modelID("claude"), primaryModel())
			record := harnessRecordFile(t).Harnesses[tc.name]
			h.Require.Empty(record.Route)
			h.Require.Empty(record.BackgroundRoute)
			if tc.background {
				h.Require.Contains(h.Stderr.String(), `background route "acme/fast" no longer exists; using "acme/claude"`)
			}
			h.Require.NoError(h.Execute("grid", "sync", "--yes"))
			h.Require.Contains(h.Stderr.String(), "All configured harnesses are up to date.")
			h.Require.NotContains(h.Stderr.String(), "no longer exists")
		})
	}
}

func Test_Grid_Sync_DryRun(t *testing.T) {
	skipUnlessSupported(t)
	h, api := gridAPI(t, "")
	setHarnessRoutes(api, "acme/primary")
	h.Context = internalcmd.WithExecer(h.Context, fakeHarnessExecer{})
	dir := t.TempDir()
	h.Require.NoError(h.Execute("grid", "setup", "--harness", "claude-code", "--config-dir", dir, "--background-route-id", "route-primary", "--yes"))
	keys := countCalls(api, "POST", "/v1/teams/team-a/api_keys")

	setHarnessRoutes(api, "acme/primary", "acme/second")
	h.Require.NoError(h.Execute("grid", "sync", "--dry-run", "--output", "json"))
	var preview public.GridPlanList
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &preview))
	for _, item := range preview.Items {
		h.Require.True(item.Changed)
	}
	h.Require.Len(readHarnessSettings(t, "claude-code", harnessSettingsFile("claude-code", dir))["modelPicker"].(map[string]any)["options"], 1, "no files changed")
	h.Require.Equal("acme/primary", harnessRecordFile(t).Harnesses["claude-code"].BackgroundRoute, "no record changes")
	h.Require.Equal(keys, countCalls(api, "POST", "/v1/teams/team-a/api_keys"), "no key created")
}

func Test_Grid_Sync_RestartsCodexDaemon(t *testing.T) {
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
	h, api := gridAPI(t, "")
	setHarnessRoutes(api, "acme/primary")
	quiet := &codexDaemonHarnessExecer{}
	h.Context = internalcmd.WithExecer(h.Context, quiet)
	dir := t.TempDir()
	h.Require.NoError(h.Execute("grid", "setup", "--harness", "codex", "--config-dir", dir, "--yes"))
	h.Require.Equal(0, quiet.restarts)
	h.Require.False(harnessRecordFile(t).Harnesses["codex"].CodexRestartPending)

	// The catalog changes but the restart fails: it stays pending.
	failing := &codexDaemonHarnessExecer{socket: socket, lsof: listener, restartFails: true}
	setHarnessRoutes(api, "acme/primary", "acme/second")
	h.Context = internalcmd.WithExecer(h.Context, failing)
	h.Require.NoError(h.Execute("grid", "sync", "--yes"))
	h.Require.Equal(1, failing.restarts)
	h.Require.Contains(h.Stderr.String(), "could not restart the codex app-server daemon")
	h.Require.True(harnessRecordFile(t).Harnesses["codex"].CodexRestartPending)

	// The next sync retries even though nothing changed, and --yes restarts
	// despite attached sessions, as setup does.
	busy := &codexDaemonHarnessExecer{socket: socket, lsof: listener + "f14\nn" + socket + " type=STREAM\n"}
	h.Context = internalcmd.WithExecer(h.Context, busy)
	h.Require.NoError(h.Execute("grid", "sync", "--yes"))
	h.Require.Equal(1, busy.restarts)
	h.Require.Contains(h.Stderr.String(), "will disconnect 1 running codex session(s)")
	h.Require.Contains(h.Stderr.String(), "Restarted codex app-server daemon (pid 4242)")
	h.Require.False(harnessRecordFile(t).Harnesses["codex"].CodexRestartPending)

	// An unchanged sync without a pending restart leaves the daemon alone.
	h.Require.NoError(h.Execute("grid", "sync", "--yes"))
	h.Require.Equal(1, busy.restarts)
}

func Test_Grid_Setup_UseSessionCredential(t *testing.T) {
	skipUnlessSupported(t)
	h, api := gridAPI(t, "")
	setHarnessRoutes(api, "acme/primary")
	h.Context = internalcmd.WithExecer(h.Context, fakeHarnessExecer{})
	dir := t.TempDir()
	path := harnessSettingsFile("claude-code", dir)
	base := []string{"grid", "setup", "--harness", "claude-code", "--config-dir", dir, "--yes"}

	// --key-name names a key the CLI creates, which it never does here.
	h.Require.Error(h.Execute(append(slices.Clone(base), "--use-session-credential", "--key-name", "laptop")...))
	h.Require.Contains(h.Stderr.String(), "--key-name is not used with --use-session-credential")

	h.Require.NoError(h.Execute(append(slices.Clone(base), "--use-session-credential")...))
	h.Require.Equal(0, countCalls(api, "POST", "/v1/teams/team-a/api_keys"), "no Grid API key is created")
	h.Require.Nil(api.FindCall("GET", "/v1/users/me"), "the session key needs no user-scoped save")
	h.Require.Equal("test-key", readHarnessSettings(t, "claude-code", path)["env"].(map[string]any)["ANTHROPIC_AUTH_TOKEN"])
	record := harnessRecordFile(t).Harnesses["claude-code"]
	h.Require.Equal("session", record.CredentialSource)
	h.Require.Empty(record.KeyName)

	// A rerun with the same key changes nothing.
	h.Require.NoError(h.Execute(append(slices.Clone(base), "--use-session-credential", "--output", "json")...))
	var rerun public.GridPlanList
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &rerun))
	for _, item := range rerun.Items {
		h.Require.False(item.Changed)
	}

	// A rotated key shows as a change in the preview and is applied.
	t.Setenv("BASETEN_API_KEY", "rotated-key")
	h.Require.NoError(h.Execute("grid", "setup", "--harness", "claude-code", "--config-dir", dir, "--use-session-credential", "--dry-run", "--output", "json"))
	var preview public.GridPlanList
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &preview))
	for _, item := range preview.Items {
		h.Require.True(item.Changed, "a rotated session key is a change")
	}
	h.Require.Equal(0, countCalls(api, "POST", "/v1/teams/team-a/api_keys"), "no key was created by the preview")
	h.Require.NoError(h.Execute(append(slices.Clone(base), "--use-session-credential")...))
	h.Require.Equal("rotated-key", readHarnessSettings(t, "claude-code", path)["env"].(map[string]any)["ANTHROPIC_AUTH_TOKEN"])
}

func Test_Grid_Setup_UseSessionCredentialRequiresAPIKey(t *testing.T) {
	skipUnlessSupported(t)
	h, api := gridAPI(t, "")
	setHarnessRoutes(api, "acme/primary")
	h.Context = internalcmd.WithExecer(h.Context, fakeHarnessExecer{})
	t.Setenv("BASETEN_API_KEY", "")
	dir := t.TempDir()
	path := harnessSettingsFile("claude-code", dir)

	// An OAuth session has no embeddable key.
	h.Require.NoError(configDirStore(t).SetOAuthProfile("alice", api.URL, auth.OAuthCredential{AccessToken: "at", RefreshToken: "rt"}, true, nil))
	h.Require.Error(h.Execute("grid", "setup", "--harness", "claude-code", "--config-dir", dir, "--use-session-credential", "--dry-run"))
	h.Require.Contains(h.Stderr.String(), "no API key to install")
	h.Require.NoFileExists(path)

	// Neither does an unauthenticated session, which cannot list teams at all.
	h.Require.NoError(configDirStore(t).RemoveProfile("alice"))
	h.Require.Error(h.Execute("grid", "setup", "--harness", "claude-code", "--config-dir", dir, "--use-session-credential", "--dry-run"))
	h.Require.Contains(h.Stderr.String(), "not logged in")
	h.Require.NoFileExists(path)
}

func Test_Grid_Sync_UseSessionCredential(t *testing.T) {
	skipUnlessSupported(t)
	h, api := gridAPI(t, "")
	setHarnessRoutes(api, "acme/primary")
	h.Context = internalcmd.WithExecer(h.Context, fakeHarnessExecer{})
	dir := t.TempDir()
	path := harnessSettingsFile("claude-code", dir)
	h.Require.NoError(h.Execute("grid", "setup", "--harness", "claude-code", "--config-dir", dir, "--use-session-credential", "--yes"))
	h.Require.Equal(0, countCalls(api, "POST", "/v1/teams/team-a/api_keys"))

	// Nothing changed: up to date, without creating a key.
	h.Require.NoError(h.Execute("grid", "sync", "--yes"))
	h.Require.Contains(h.Stderr.String(), "All configured harnesses are up to date.")
	h.Require.Equal(0, countCalls(api, "POST", "/v1/teams/team-a/api_keys"))

	// A rotated key is rewritten even though no route changed.
	t.Setenv("BASETEN_API_KEY", "rotated-key")
	h.Require.NoError(h.Execute("grid", "sync", "--yes"))
	h.Require.Equal("rotated-key", readHarnessSettings(t, "claude-code", path)["env"].(map[string]any)["ANTHROPIC_AUTH_TOKEN"])
	h.Require.Equal(0, countCalls(api, "POST", "/v1/teams/team-a/api_keys"), "sync never creates a replacement key")

	// The key is gone: sync errors instead of minting one.
	t.Setenv("BASETEN_API_KEY", "")
	h.Require.Error(h.Execute("grid", "sync", "--yes"))
	h.Require.Contains(h.Stderr.String(), "set BASETEN_API_KEY or use an API-key profile")
	h.Require.Contains(h.Stderr.String(), "--use-session-credential")
	h.Require.Equal(0, countCalls(api, "POST", "/v1/teams/team-a/api_keys"))
}

func Test_Grid_Sync_MixedCredentialSources(t *testing.T) {
	skipUnlessSupported(t)
	h, api := gridAPI(t, "")
	setHarnessRoutes(api, "acme/primary")
	h.Context = internalcmd.WithExecer(h.Context, fakeHarnessExecer{})
	dir := t.TempDir()
	h.Require.NoError(h.Execute("grid", "setup", "--harness", "claude-code", "--config-dir", dir, "--use-session-credential", "--yes"))
	h.Require.NoError(h.Execute("grid", "setup", "--harness", "opencode", "--config-dir", dir, "--key-name", "laptop", "--yes"))
	keys := countCalls(api, "POST", "/v1/teams/team-a/api_keys")
	h.Require.Equal(1, keys)

	// Sync refreshes both without cross-applying credentials: the session key
	// stays with claude-code and the created key with opencode.
	setHarnessRoutes(api, "acme/primary", "acme/second")
	h.Require.NoError(h.Execute("grid", "sync", "--yes"))
	h.Require.Equal("test-key", readHarnessSettings(t, "claude-code", harnessSettingsFile("claude-code", dir))["env"].(map[string]any)["ANTHROPIC_AUTH_TOKEN"])
	opencode, err := os.ReadFile(harnessSettingsFile("opencode", dir))
	h.Require.NoError(err)
	h.Require.Contains(string(opencode), "secret-team-a")
	h.Require.NotContains(string(opencode), "test-key")
	h.Require.Equal(keys, countCalls(api, "POST", "/v1/teams/team-a/api_keys"), "the created key is reused")
}

func Test_Grid_Teardown_KeepsSessionCredential(t *testing.T) {
	skipUnlessSupported(t)
	h, api := gridAPI(t, "")
	setHarnessRoutes(api, "acme/primary")
	h.Context = internalcmd.WithExecer(h.Context, fakeHarnessExecer{})
	dir := t.TempDir()
	path := harnessSettingsFile("claude-code", dir)
	setup := []string{"grid", "setup", "--harness", "claude-code", "--config-dir", dir, "--use-session-credential", "--yes"}
	teardown := []string{"grid", "teardown", "--harness", "claude-code", "--config-dir", dir, "--yes"}
	// List the session's key as a routes key the caller created, so teardown
	// could delete it if it weren't protected.
	api.SetRoute("GET", "/v1/api_keys", 200, map[string]any{"keys": []any{
		map[string]any{"prefix": "test-k", "type": "ROUTES", "name": "sandbox", "team_name": "Engineering", "created_at": "2026-09-01T00:00:00Z"},
	}})
	api.SetRoute("DELETE", "/v1/api_keys/test-k", 200, map[string]string{"prefix": "test-k"})
	h.Require.NoError(h.Execute(setup...))

	// The settings are removed, but the key is never offered for deletion.
	h.Require.NoError(h.Execute("grid", "teardown", "--harness", "claude-code", "--config-dir", dir, "--dry-run"))
	h.Require.Contains(h.Stderr.String(), "Keeping the API key installed with --use-session-credential")
	h.Require.NotContains(h.Stdout.String()+h.Stderr.String(), "Also delete the Grid API key")
	h.Require.NoError(h.Execute(teardown...))
	raw, err := os.ReadFile(path)
	h.Require.NoError(err)
	h.Require.NotContains(string(raw), "test-key", "the settings were removed")
	h.Require.Equal(0, countCalls(api, "DELETE", "/v1/api_keys/test-k"))

	// Settings without a record — a baked image, say — keep the session's key
	// too, since it is the key the session runs with.
	h.Require.NoError(h.Execute(setup...))
	h.Require.NoError(os.Remove(filepath.Join(recordDir(t), "harness.json")))
	h.Require.NoError(h.Execute(teardown...))
	h.Require.NotContains(h.Stdout.String()+h.Stderr.String(), "Also delete the Grid API key")
	raw, err = os.ReadFile(path)
	h.Require.NoError(err)
	h.Require.NotContains(string(raw), "test-key", "the settings were removed")
	for _, call := range api.Calls() {
		h.Require.NotEqual("DELETE", call.Method, "the session's key is never deleted")
	}
}

func Test_Grid_Help(t *testing.T) {
	for _, path := range [][]string{
		{"grid"},
		{"grid", "setup"},
		{"grid", "sync"},
		{"grid", "status"},
		{"grid", "teardown"},
		{"grid", "usage"},
		{"grid", "harness-defaults"},
		{"grid", "route"},
		{"grid", "route", "list"},
		{"grid", "route", "describe"},
		{"grid", "route", "create"},
		{"grid", "route", "update"},
		{"grid", "route", "delete"},
		{"grid", "route", "usage"},
		{"grid", "team-settings", "describe"},
		{"grid", "team-settings", "update"},
		{"grid", "user-settings", "list"},
		{"grid", "user-settings", "describe"},
		{"grid", "user-settings", "update"},
		{"grid", "connection", "list"},
		{"grid", "connection", "create"},
		{"grid", "connection", "update"},
		{"grid", "connection", "delete"},
		{"grid", "classifier", "list"},
	} {
		t.Run(strings.Join(path, " "), func(t *testing.T) {
			h := NewCommandHarness(t)
			for _, helpFlag := range []string{"--help", "--help-output"} {
				h.Require.NoError(h.Execute(append(path, helpFlag)...))
				h.Require.Contains(h.Stdout.String(), "baseten "+strings.Join(path, " "))
			}
		})
	}
}

func Test_Grid_Setup_ServerResolvedTeamAcrossPagesAndSync(t *testing.T) {
	skipUnlessSupported(t)
	h, api := fakeGridAPI(t)
	// A team-directory outage must not affect server-side resolution during setup.
	api.SetRoute("GET", "/v1/teams", 500, map[string]string{"message": "team directory unavailable"})
	api.SetRouteFunc("GET", "/v1/grid/routes", func(w http.ResponseWriter, r *http.Request) {
		h.Require.Equal("true", r.URL.Query().Get("for_harnesses"))
		if team := r.URL.Query().Get("team_id"); team != "" {
			h.Require.Equal("team-b", team)
		}
		route := harnessRoute(api, "primary")
		route["team_id"], route["team_name"] = "team-b", "Research"
		var cursor any = "page-two"
		if r.URL.Query().Get("cursor") == "page-two" {
			route["id"], route["name"] = "route-second", "acme/second"
			cursor = nil
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(gridRoutePage([]any{route}, cursor))
	})
	dir := t.TempDir()
	h.Require.NoError(h.Execute("grid", "setup", "--harness", "claude-code", "--config-dir", dir, "--primary-route-id", "route-second", "--yes", "--verbose"))
	h.Require.Nil(api.FindCall("GET", "/v1/teams"))
	h.Require.Equal(2, countCalls(api, "GET", "/v1/grid/routes"))
	for _, call := range api.Calls() {
		if call.Path == "/v1/grid/routes" {
			h.Require.NotContains(call.Query(), "team_id")
		}
	}
	h.Require.Contains(h.Stdout.String(), "Team: Research")
	h.Require.Equal("team-b", api.FindCall("GET", "/v1/grid/connections").Query().Get("team_id"))
	h.Require.Equal("team-b", api.FindCall("GET", "/v1/grid/team_settings").Query().Get("team_id"))
	h.Require.Equal(1, countCalls(api, "POST", "/v1/teams/team-b/api_keys"))
	h.Require.Equal(0, countCalls(api, "POST", "/v1/teams/team-a/api_keys"))
	h.Require.Equal("team-b", harnessRecordFile(t).Harnesses["claude-code"].TeamID)
	h.Require.Equal("acme/second", harnessRecordFile(t).Harnesses["claude-code"].Route)

	// Sync stays with the saved team even after the caller joins another team.
	api.SetRoute("GET", "/v1/teams", 200, gridTwoTeams)
	start := len(api.Calls())
	h.Require.NoError(h.Execute("grid", "sync", "--yes"))
	for _, call := range api.Calls()[start:] {
		if call.Path == "/v1/grid/routes" {
			h.Require.Equal("team-b", call.Query().Get("team_id"))
		}
	}
	h.Require.Contains(h.Stderr.String(), "All configured harnesses are up to date")
	h.Require.Equal("team-b", harnessRecordFile(t).Harnesses["claude-code"].TeamID)
	h.Require.Equal("acme/second", harnessRecordFile(t).Harnesses["claude-code"].Route)
}

func Test_Grid_Setup_ServerTeamResolutionFailureHasNoSideEffects(t *testing.T) {
	skipUnlessSupported(t)
	for _, status := range []int{400, 401, 403, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			h, api := fakeGridAPI(t)
			api.SetRoute("GET", "/v1/grid/routes", status, map[string]string{"message": "server rejected team resolution"})
			dir := t.TempDir()
			h.Require.Error(h.Execute("grid", "setup", "--harness", "claude-code", "--config-dir", dir, "--yes", "--output", "json"))
			h.Require.Equal(status, decodeJSONErrorEnvelope(h).APIStatusCode)
			h.Require.Len(api.Calls(), 1)
			h.Require.NoFileExists(harnessSettingsFile("claude-code", dir))
			h.Require.Empty(harnessRecordFile(t).Harnesses)
		})
	}
}

func Test_Grid_Setup_RejectsEmptyOrInvalidTeamScope(t *testing.T) {
	skipUnlessSupported(t)
	for _, tc := range []struct {
		name, selected, want string
		teamIDs              []string
	}{
		{"no-routes", "", "no routes available", nil},
		// These responses deliberately violate the harness-scoped single-team contract.
		{"missing-team", "", "without a team ID", []string{""}},
		{"mixed-teams", "", "different teams", []string{"team-a", "team-b"}},
		{"wrong-team", "team-a", "requested", []string{"team-b"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, api := fakeGridAPI(t)
			items := []any{}
			for _, id := range tc.teamIDs {
				route := harnessRoute(api, "primary")
				route["team_id"] = id
				items = append(items, route)
			}
			api.SetRoute("GET", "/v1/grid/routes", 200, gridRoutePage(items, nil))
			dir := t.TempDir()
			args := []string{"grid", "setup", "--harness", "claude-code", "--config-dir", dir, "--yes"}
			if tc.selected != "" {
				args = append(args, "--team", tc.selected)
			}
			h.Require.ErrorContains(h.Execute(args...), tc.want)
			h.Require.Nil(api.FindCall("GET", "/v1/grid/connections"))
			for _, call := range api.Calls() {
				h.Require.Equal("GET", call.Method)
			}
			h.Require.NoFileExists(harnessSettingsFile("claude-code", dir))
			h.Require.Empty(harnessRecordFile(t).Harnesses)
		})
	}
}

func Test_Grid_Classifier_List(t *testing.T) {
	for _, empty := range []bool{false, true} {
		for _, output := range []string{"text", "json", "jsonl"} {
			t.Run(fmt.Sprintf("%t/%s", empty, output), func(t *testing.T) {
				h := NewCommandHarness(t)
				m := h.MockManagementAPI()
				items := []any{}
				if !empty {
					items = append(items, map[string]any{"id": "general-v1", "name": "General"})
				}
				body := gridRoutePage(items, nil)
				m.SetRouteFunc("GET", "/v1/grid/classifiers", func(w http.ResponseWriter, r *http.Request) {
					h.Require.Equal("Bearer test-key", r.Header.Get("Authorization"))
					h.Require.Empty(r.URL.RawQuery)
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(body)
				})
				h.Require.NoError(h.Execute("grid", "classifier", "list", "--output", output))
				h.Require.Len(m.Calls(), 1)
				if output == "text" {
					if empty {
						h.Require.Contains(h.Stderr.String(), "No classifiers found")
					} else {
						h.Require.Contains(h.Stdout.String(), "general-v1")
						h.Require.Contains(h.Stdout.String(), "General")
					}
				} else {
					var got any
					h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &got))
					h.Require.Equal(map[string]any{"items": items}, got)
				}
			})
		}
	}
}

func Test_Grid_Classifier_List_Errors(t *testing.T) {
	for _, status := range []int{401, 403, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			h := NewCommandHarness(t)
			m := h.MockManagementAPI()
			m.SetRoute("GET", "/v1/grid/classifiers", status, map[string]string{"error": "unavailable"})
			h.Require.Error(h.Execute("grid", "classifier", "list", "--output", "json"))
			h.Require.Equal(status, decodeJSONErrorEnvelope(h).APIStatusCode)
		})
	}
	t.Run("malformed", func(t *testing.T) {
		h := NewCommandHarness(t)
		m := h.MockManagementAPI()
		m.SetRouteFunc("GET", "/v1/grid/classifiers", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"items":`))
		})
		h.Require.ErrorContains(h.Execute("grid", "classifier", "list"), "unexpected end of JSON input")
	})
}

func Test_Grid_Setup_ReplacesDeletedKey(t *testing.T) {
	skipUnlessSupported(t)
	h, api := fakeGridAPI(t)
	dir := t.TempDir()
	args := []string{"grid", "setup", "--harness", "claude-code", "--config-dir", dir, "--yes"}
	h.Require.NoError(h.Execute(args...))
	h.Require.NoError(h.Execute(args...))
	h.Require.Equal(1, countCalls(api, "POST", "/v1/teams/team-a/api_keys"), "an active key is reused")

	// The key was deleted on another machine.
	api.SetRoute("GET", "/v1/api_keys", 200, map[string]any{"keys": []any{}})
	api.SetRoute("POST", "/v1/teams/team-a/api_keys", 200, map[string]string{"api_key": "secret-team-a-2"})
	h.Require.NoError(h.Execute(args...))
	h.Require.Contains(h.Stderr.String(), "Created Grid API key")
	h.Require.Equal(2, countCalls(api, "POST", "/v1/teams/team-a/api_keys"))
	h.Require.Equal("secret-team-a-2", readHarnessSettings(t, "claude-code", harnessSettingsFile("claude-code", dir))["env"].(map[string]any)["ANTHROPIC_AUTH_TOKEN"])

	// A failed check leaves the saved key and configuration alone.
	api.SetRoute("GET", "/v1/api_keys", 500, map[string]string{"message": "unavailable"})
	h.Require.Error(h.Execute(args...))
	h.Require.Contains(h.Stderr.String(), "listing Grid API keys")
	h.Require.Equal(2, countCalls(api, "POST", "/v1/teams/team-a/api_keys"))
}

func Test_Grid_Classifier_List_Pagination(t *testing.T) {
	for _, output := range []string{"text", "json", "jsonl"} {
		t.Run(output, func(t *testing.T) {
			h := NewCommandHarness(t)
			m := h.MockManagementAPI()
			first := map[string]any{"id": "general-v1", "name": "General"}
			second := map[string]any{"id": "coding-v1", "name": "Coding"}
			m.SetRouteFunc("GET", "/v1/grid/classifiers", func(w http.ResponseWriter, r *http.Request) {
				h.Require.NotContains(r.URL.Query(), "team_id")
				body := gridRoutePage([]any{first}, "next")
				if r.URL.Query().Get("cursor") == "next" {
					body = gridRoutePage([]any{second}, nil)
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(body)
			})
			h.Require.NoError(h.Execute("grid", "classifier", "list", "--output", output))
			h.Require.Equal(2, countCalls(m, "GET", "/v1/grid/classifiers"))
			if output == "text" {
				h.Require.Contains(h.Stdout.String(), "General")
				h.Require.Contains(h.Stdout.String(), "Coding")
			} else {
				var got any
				h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &got))
				h.Require.Equal(map[string]any{"items": []any{first, second}}, got)
			}
		})
	}
}

func Test_Grid_Classifier_List_PageError(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRouteFunc("GET", "/v1/grid/classifiers", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("cursor") != "" {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"message": "unavailable"})
			return
		}
		_ = json.NewEncoder(w).Encode(gridRoutePage([]any{map[string]any{"id": "general-v1", "name": "General"}}, "next"))
	})
	h.Require.Error(h.Execute("grid", "classifier", "list", "--output", "json"))
	h.Require.Equal(500, decodeJSONErrorEnvelope(h).APIStatusCode)
	h.Require.NotContains(h.Stdout.String(), "general-v1")
	h.Require.Equal(2, countCalls(m, "GET", "/v1/grid/classifiers"))
}

func gridSettingsFixture() map[string]any {
	return map[string]any{
		"team_id":   "t123456",
		"team_name": "Engineering",
		"harness_defaults": map[string]any{
			"claude_code": map[string]any{"primary": nil, "background": nil},
			"opencode":    map[string]any{"primary": nil, "background": nil},
			"codex":       map[string]any{"primary": nil}, "pi": map[string]any{"primary": nil},
		},
		"spend_limit": map[string]any{"per_member_monthly_limit_usd": "100", "team_wide_monthly_limit_usd": nil},
	}
}

func gridUserSettingsFixture() map[string]any {
	return map[string]any{
		"team_id": "t123456", "team_name": "Engineering", "user_id": "u123456", "email": "member@example.com",
		"month_spend_usd": "12.5", "is_blocked": false, "blocked_by": nil,
		"spend_limit": map[string]any{
			"user_monthly_limit_usd": nil, "month_override_usd": nil,
			"team_default":                map[string]any{"team_id": "t123456", "team_name": "Engineering", "per_member_monthly_limit_usd": "100"},
			"effective":                   map[string]any{"monthly_limit_usd": "100", "source": "team"},
			"team_wide_monthly_limit_usd": nil,
		},
	}
}

func Test_Grid_TeamSettings_UserSettings_ReadContracts(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		path string
		body any
		text string
	}{
		{"team", []string{"team-settings", "describe"}, "/v1/grid/team_settings", gridSettingsFixture(), "Per-member monthly limit"},
		{"user", []string{"user-settings", "describe"}, "/v1/grid/team_settings/users/me", gridUserSettingsFixture(), "member@example.com"},
		{"users", []string{"user-settings", "list"}, "/v1/grid/team_settings/users", gridRoutePage([]any{gridUserSettingsFixture()}, nil), "member@example.com"},
	} {
		for _, team := range []string{"", "t123456", "Engineering"} {
			for _, output := range []string{"text", "json", "jsonl"} {
				t.Run(tc.name+"/"+team+"/"+output, func(t *testing.T) {
					h := NewCommandHarness(t)
					m := h.MockManagementAPI()
					if team != "" {
						gridRouteTeams(h)
					}
					m.SetRouteFunc("GET", tc.path, func(w http.ResponseWriter, r *http.Request) {
						h.Require.Equal("Bearer test-key", r.Header.Get("Authorization"))
						w.Header().Set("Content-Type", "application/json")
						_ = json.NewEncoder(w).Encode(tc.body)
					})
					args := append([]string{"grid"}, tc.args...)
					if team != "" {
						args = append(args, "--team", team)
					}
					h.Require.NoError(h.Execute(append(args, "--output", output)...))
					query := m.FindCall("GET", tc.path).Query()
					if team == "" {
						h.Require.NotContains(query, "team_id")
						h.Require.Len(m.Calls(), 1)
					} else {
						h.Require.Equal("t123456", query.Get("team_id"))
					}
					if output == "text" {
						h.Require.Contains(h.Stdout.String(), tc.text)
						h.Require.Contains(h.Stdout.String(), "Engineering")
					} else {
						var got any
						h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &got))
						want := tc.body
						if tc.name == "users" {
							want = map[string]any{"items": []any{gridUserSettingsFixture()}}
						}
						h.Require.Equal(want, got)
					}
				})
			}
		}
	}
}

func Test_Grid_TeamSettings_Update_PatchSemantics(t *testing.T) {
	for _, tc := range []struct {
		name  string
		flags []string
		body  map[string]any
	}{
		{"zero", []string{"--per-member-monthly-limit-usd", "0"}, map[string]any{"spend_limit": map[string]any{"per_member_monthly_limit_usd": "0"}}},
		{"exact", []string{"--team-wide-monthly-limit-usd", "123456789.12"}, map[string]any{"spend_limit": map[string]any{"team_wide_monthly_limit_usd": "123456789.12"}}},
		{"clear-limits", []string{"--per-member-monthly-limit-usd", "null", "--team-wide-monthly-limit-usd", "null"}, map[string]any{"spend_limit": map[string]any{"per_member_monthly_limit_usd": nil, "team_wide_monthly_limit_usd": nil}}},
		{"clear-spend-limit", []string{"--clear-spend-limit"}, map[string]any{"spend_limit": nil}},
		{"claude", []string{"--harness", "claude-code", "--primary-route-id", "r123456", "--background-route-id", "null"}, map[string]any{"harness_defaults": map[string]any{"claude_code": map[string]any{"primary": "r123456", "background": nil}}}},
		{"opencode", []string{"--harness", "opencode", "--background-route-id", "r123456"}, map[string]any{"harness_defaults": map[string]any{"opencode": map[string]any{"background": "r123456"}}}},
		{"codex", []string{"--harness", "codex", "--primary-route-id", "null"}, map[string]any{"harness_defaults": map[string]any{"codex": map[string]any{"primary": nil}}}},
		{"pi", []string{"--harness", "pi", "--primary-route-id", "r123456"}, map[string]any{"harness_defaults": map[string]any{"pi": map[string]any{"primary": "r123456"}}}},
		{"clear-all", []string{"--clear-harness-defaults"}, map[string]any{"harness_defaults": nil}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := NewCommandHarness(t)
			m := h.MockManagementAPI()
			m.SetRoute("PATCH", "/v1/grid/team_settings", 200, gridSettingsFixture())
			h.Require.NoError(h.Execute(append([]string{"grid", "team-settings", "update", "--output", "json"}, tc.flags...)...))
			h.Require.Equal(tc.body, m.FindCall("PATCH", "/v1/grid/team_settings").BodyJSON(t))
			h.Require.Len(m.Calls(), 1)
			h.Require.Contains(h.Stdout.String(), "harness_defaults")
		})
	}
}

func Test_Grid_UserSettings_Update_PatchSemantics(t *testing.T) {
	for _, tc := range []struct {
		name  string
		flags []string
		limit map[string]any
	}{
		{"zero", []string{"--user-monthly-limit-usd", "0"}, map[string]any{"user_monthly_limit_usd": "0"}},
		{"override", []string{"--month-override-usd", "250.50"}, map[string]any{"month_override_usd": "250.50"}},
		{"clear", []string{"--user-monthly-limit-usd", "null", "--month-override-usd", "null"}, map[string]any{"user_monthly_limit_usd": nil, "month_override_usd": nil}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := NewCommandHarness(t)
			m := h.MockManagementAPI()
			m.SetRoute("PATCH", "/v1/grid/team_settings/users/me", 200, gridUserSettingsFixture())
			h.Require.NoError(h.Execute(append([]string{"grid", "user-settings", "update", "--output", "json"}, tc.flags...)...))
			h.Require.Equal(map[string]any{"spend_limit": tc.limit}, m.FindCall("PATCH", "/v1/grid/team_settings/users/me").BodyJSON(t))
			h.Require.Len(m.Calls(), 1)
		})
	}
}

func Test_Grid_TeamSettings_UserSettings_Update_ExplicitTeamAndUser(t *testing.T) {
	for _, tc := range []struct {
		group, path string
		flags       []string
	}{
		{"team-settings", "/v1/grid/team_settings", []string{"--per-member-monthly-limit-usd", "100"}},
		{"user-settings", "/v1/grid/team_settings/users/u123456", []string{"--user-id", "u123456", "--month-override-usd", "100"}},
	} {
		t.Run(tc.group, func(t *testing.T) {
			h := NewCommandHarness(t)
			gridRouteTeams(h)
			m := h.MockManagementAPI()
			body := gridSettingsFixture()
			if tc.group == "user-settings" {
				body = gridUserSettingsFixture()
			}
			m.SetRoute("PATCH", tc.path, 200, body)
			h.Require.NoError(h.Execute(append([]string{"grid", tc.group, "update", "--team", "Engineering"}, tc.flags...)...))
			call := m.FindCall("PATCH", tc.path)
			h.Require.Empty(call.Query())
			h.Require.Equal("t123456", call.BodyJSON(t)["team_id"])
		})
	}
}

func Test_Grid_TeamSettings_UserSettings_RejectsEmptyOrConflictingUpdates(t *testing.T) {
	for _, args := range [][]string{
		{"team-settings", "update"}, {"team-settings", "update", "--team", "t123456"},
		{"team-settings", "update", "--harness", "codex"},
		{"team-settings", "update", "--primary-route-id", "r123456"},
		{"team-settings", "update", "--harness", "pi", "--background-route-id", "null"},
		{"team-settings", "update", "--clear-harness-defaults", "--harness", "codex"},
		{"team-settings", "update", "--clear-spend-limit", "--per-member-monthly-limit-usd", "100"},
		{"team-settings", "update", "--clear-spend-limit", "--team-wide-monthly-limit-usd", "100"},
		{"user-settings", "update"}, {"user-settings", "update", "--user-id", "", "--month-override-usd", "0"},
		{"user-settings", "describe", "--user-id", ""},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			h := NewCommandHarness(t)
			m := h.MockManagementAPI()
			h.Require.Error(h.Execute(append([]string{"grid"}, args...)...))
			h.Require.Equal(2, h.ExitCode)
			h.Require.Empty(m.Calls())
		})
	}
}

func Test_Grid_TeamSettings_UserSettings_ServerErrorsPropagate(t *testing.T) {
	for _, tc := range []struct {
		method, path string
		args         []string
	}{
		{"GET", "/v1/grid/team_settings", []string{"team-settings", "describe"}},
		{"GET", "/v1/grid/team_settings/users", []string{"user-settings", "list"}},
		{"GET", "/v1/grid/team_settings/users/me", []string{"user-settings", "describe"}},
		{"PATCH", "/v1/grid/team_settings", []string{"team-settings", "update", "--per-member-monthly-limit-usd", "-1"}},
		{"PATCH", "/v1/grid/team_settings/users/me", []string{"user-settings", "update", "--month-override-usd", "1.234"}},
	} {
		for _, status := range []int{400, 403, 404, 500} {
			t.Run(fmt.Sprintf("%s/%s/%d", tc.method, tc.path, status), func(t *testing.T) {
				h := NewCommandHarness(t)
				m := h.MockManagementAPI()
				m.SetRoute(tc.method, tc.path, status, map[string]string{"error": "server rejected request"})
				args := append([]string{"grid"}, tc.args...)
				args = append(args, "--output", "json")
				h.Require.Error(h.Execute(args...))
				h.Require.Equal(status, decodeJSONErrorEnvelope(h).APIStatusCode)
				h.Require.Len(m.Calls(), 1)
			})
		}
	}
}

func Test_Grid_UserSettings_List_Empty(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", "/v1/grid/team_settings/users", 200, gridRoutePage([]any{}, nil))
	h.Require.NoError(h.Execute("grid", "user-settings", "list"))
	h.Require.Contains(h.Stderr.String(), "No user settings")
	h.Require.NoError(h.Execute("grid", "user-settings", "list", "--output", "json"))
	h.Require.JSONEq(`{"items":[]}`, h.Stdout.String())
}

func Test_Grid_UserSettings_List_MalformedResponse(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRouteFunc("GET", "/v1/grid/team_settings/users", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":`))
	})
	h.Require.ErrorContains(h.Execute("grid", "user-settings", "list"), "unexpected end of JSON input")
	h.Require.Len(m.Calls(), 1)
}

func Test_Grid_UserSettings_List_Pagination(t *testing.T) {
	for _, team := range []string{"", "t123456", "Engineering"} {
		for _, output := range []string{"text", "json", "jsonl"} {
			t.Run(team+"/"+output, func(t *testing.T) {
				h := NewCommandHarness(t)
				m := h.MockManagementAPI()
				gridRouteTeams(h)
				first, second := gridUserSettingsFixture(), gridUserSettingsFixture()
				if team == "" {
					// The same user can have a membership in more than one team.
					second["team_id"], second["team_name"] = "t765432", "Research"
					limit := second["spend_limit"].(map[string]any)
					limit["team_default"] = map[string]any{"team_id": "t765432", "team_name": "Research", "per_member_monthly_limit_usd": "100"}
				} else {
					second["user_id"], second["email"] = "u765432", "another@example.com"
				}
				m.SetRouteFunc("GET", "/v1/grid/team_settings/users", func(w http.ResponseWriter, r *http.Request) {
					want := ""
					if team != "" {
						want = "t123456"
					}
					h.Require.Equal(want, r.URL.Query().Get("team_id"))
					body := gridRoutePage([]any{first}, "next")
					if r.URL.Query().Get("cursor") == "next" {
						body = gridRoutePage([]any{second}, nil)
					}
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(body)
				})
				args := []string{"grid", "user-settings", "list", "--output", output}
				if team != "" {
					args = append(args, "--team", team)
				}
				h.Require.NoError(h.Execute(args...))
				h.Require.Equal(2, countCalls(m, "GET", "/v1/grid/team_settings/users"))
				if team == "" {
					h.Require.Nil(m.FindCall("GET", "/v1/teams"))
				}
				if output == "text" {
					h.Require.Contains(h.Stdout.String(), "Engineering")
					h.Require.Contains(h.Stdout.String(), second["team_name"])
					h.Require.Contains(h.Stdout.String(), second["user_id"])
				} else {
					var got any
					h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &got))
					h.Require.Equal(map[string]any{"items": []any{first, second}}, got)
				}
			})
		}
	}
}

func Test_Grid_UserSettings_List_PageError(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRouteFunc("GET", "/v1/grid/team_settings/users", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("cursor") != "" {
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]string{"message": "forbidden"})
			return
		}
		_ = json.NewEncoder(w).Encode(gridRoutePage([]any{gridUserSettingsFixture()}, "next"))
	})
	h.Require.Error(h.Execute("grid", "user-settings", "list", "--output", "json"))
	h.Require.Equal(403, decodeJSONErrorEnvelope(h).APIStatusCode)
	h.Require.NotContains(h.Stdout.String(), "member@example.com")
	h.Require.Equal(2, countCalls(m, "GET", "/v1/grid/team_settings/users"))
}

func Test_Grid_Setup_RouteIDsConfigureNamesAndSync(t *testing.T) {
	skipUnlessSupported(t)
	h, api := fakeGridAPI(t)
	routes := []any{}
	for _, name := range []string{"primary", "background", "subagent", "fallback"} {
		routes = append(routes, harnessRoute(api, name))
	}
	api.SetRoute("GET", "/v1/grid/routes", 200, gridRoutePage(routes, nil))
	dir := t.TempDir()
	h.Require.NoError(h.Execute("grid", "setup", "--harness", "claude-code", "--config-dir", dir,
		"--primary-route-id", "route-primary", "--background-route-id", "route-background",
		"--subagent-route-id", "route-subagent", "--fallback-route-id", "route-fallback", "--yes"))
	settings := readHarnessSettings(t, "claude-code", harnessSettingsFile("claude-code", dir))
	env := settings["env"].(map[string]any)
	h.Require.Equal("acme/primary", settings["model"])
	h.Require.Equal("acme/background", env["ANTHROPIC_DEFAULT_HAIKU_MODEL"])
	h.Require.Equal("acme/subagent", env["CLAUDE_CODE_SUBAGENT_MODEL"])
	h.Require.Equal([]any{"acme/fallback"}, settings["fallbackModel"])
	record := harnessRecordFile(t).Harnesses["claude-code"]
	h.Require.Equal("acme/primary", record.Route)
	h.Require.Equal("acme/background", record.BackgroundRoute)
	h.Require.Equal("acme/subagent", record.SubagentRoute)
	h.Require.Equal("acme/fallback", record.FallbackRoute)
	h.Require.Equal(1, countCalls(api, "GET", "/v1/grid/routes"))
	for _, call := range api.Calls() {
		h.Require.False(strings.HasPrefix(call.Path, "/v1/grid/routes/"), "resolve IDs from the fetched route list")
	}
	h.Require.NoError(h.Execute("grid", "sync", "--yes"))
	h.Require.Contains(h.Stderr.String(), "All configured harnesses are up to date")
	h.Require.Equal(record, harnessRecordFile(t).Harnesses["claude-code"])
}

func Test_Grid_Setup_RejectsUnknownRouteIDsBeforeWriting(t *testing.T) {
	skipUnlessSupported(t)
	for _, flag := range []string{"--primary-route-id", "--background-route-id", "--subagent-route-id", "--fallback-route-id"} {
		for _, id := range []string{"route-missing", "acme/primary"} {
			t.Run(flag+"/"+id, func(t *testing.T) {
				h, api := fakeGridAPI(t)
				dir := t.TempDir()
				h.Require.Error(h.Execute("grid", "setup", "--harness", "claude-code", "--config-dir", dir, flag, id, "--yes"))
				h.Require.Equal(2, h.ExitCode)
				h.Require.Contains(h.Stderr.String(), flag)
				h.Require.Contains(h.Stderr.String(), "no route with ID")
				h.Require.Contains(h.Stderr.String(), "baseten grid route list")
				h.Require.Len(api.Calls(), 1)
				h.Require.Equal("/v1/grid/routes", api.Calls()[0].Path)
				h.Require.NoFileExists(harnessSettingsFile("claude-code", dir))
				h.Require.Empty(harnessRecordFile(t).Harnesses)
			})
		}
	}
}
