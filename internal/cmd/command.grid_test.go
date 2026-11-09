package cmd_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
	public "github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-cli/internal/auth"
	internalcmd "github.com/basetenlabs/baseten-cli/internal/cmd"
	"github.com/basetenlabs/baseten-cli/internal/harness"
	"github.com/creack/pty"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
)

// harnessRouteMetadata is usable model metadata for route fixtures.
var harnessRouteMetadata = map[string]any{
	"context_window": 128000, "max_output_tokens": 4096, "input_modalities": []string{"text"}, "tools": true,
	"supported_api_formats": map[string]any{"messages": true, "responses": true, "chat_completions": true},
}

// fakeHarnessExecer finds the one binary it names, or every bare name as on
// PATH. A desktop app, looked up by its absolute path, is found only by name.
type fakeHarnessExecer struct{ binary string }

func (e fakeHarnessExecer) LookPath(name string) (string, error) {
	if e.binary != "" {
		if name != e.binary {
			return "", exec.ErrNotFound
		}
		return name, nil
	}
	if filepath.IsAbs(name) {
		return "", exec.ErrNotFound
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
	// A profile session, so setup installs the token helper. Tests of an
	// ephemeral session set BASETEN_API_KEY themselves.
	t.Setenv("BASETEN_API_KEY", "")
	// Claude Desktop's config has no environment override, so it follows HOME.
	t.Setenv("HOME", t.TempDir())
	h.Require.NoError(configDirStore(t).SetAPIKeyProfile(t.Context(), "alice", api.URL, "test-key", true, nil))
	// The test binary is the baseten on PATH, so setup does not warn.
	if exe, err := os.Executable(); err == nil {
		bin := t.TempDir()
		if os.Symlink(exe, filepath.Join(bin, "baseten")) == nil {
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
		}
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
		map[string]any{"prefix": "secret-team-a", "type": "GRID", "name": "laptop", "team_name": "Engineering", "created_at": "2026-09-01T00:00:00Z"},
		map[string]any{"prefix": "secret-team-b", "type": "GRID", "name": "laptop", "team_name": "Research", "created_at": "2026-09-02T00:00:00Z"},
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
	for _, name := range []string{"claude", "codex", "opencode", "pi"} {
		t.Run(name, func(t *testing.T) {
			h, api := fakeGridAPI(t)
			dir := t.TempDir()
			path := harnessSettingsFile(name, dir)
			h.Require.NoError(os.WriteFile(path, []byte(`{"theme":"dark"}`), 0o600))
			if name == "codex" {
				h.Require.NoError(os.WriteFile(path, []byte("theme = \"dark\"\n"), 0o600))
			}
			args := []string{"grid", "setup", "--harness", name, "--config-dir", dir, "--output", "json"}
			// OpenCode keeps a Grid API key; the others run the token helper,
			// whose arguments Codex lists as a TOML array.
			credential, keys := "--harness", 0
			if name == "opencode" {
				credential, keys = "secret-team-a", 1
			}

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
			h.Require.Contains(string(settings), credential)
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
			h.Require.Equal(keys, countCalls(api, "POST", "/v1/teams/team-a/api_keys"), "the saved key is reused")
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
			h.Require.Equal(keys > 0, strings.Contains(h.Stdout.String(), "Also delete the Grid API key"))
			h.Require.Equal(calls, len(api.Calls()), "status and teardown previews are local")
			unchanged, err := os.ReadFile(path)
			h.Require.NoError(err)
			h.Require.Equal(settings, unchanged, "teardown --dry-run changes nothing")
			h.Require.Error(h.Execute("grid", "teardown", "--harness", name, "--config-dir", dir))
			h.Require.Contains(h.Stderr.String(), "pass --yes")
			h.Require.NoError(h.Execute("grid", "teardown", "--harness", name, "--config-dir", dir, "--yes"))
			h.Require.Equal(map[string]any{"theme": "dark"}, readHarnessSettings(t, name, path))
			h.Require.NoFileExists(filepath.Join(dir, "baseten-models.json"))
			h.Require.Equal(keys, countCalls(api, "DELETE", "/v1/api_keys/secret-team-a"), "teardown deletes the unused key")

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

func Test_Grid_Setup_Status_Teardown_ClaudeDesktop(t *testing.T) {
	skipUnlessSupported(t)
	// Only the app is installed; the claude harness still counts as installed
	// and writes the app's files beside Claude Code's settings.
	binary := "/usr/bin/claude-desktop"
	if runtime.GOOS == "darwin" {
		binary = "/Applications/Claude.app/Contents/MacOS/Claude"
	}
	h, _ := gridAPI(t, "")
	h.Context = internalcmd.WithExecer(h.Context, fakeHarnessExecer{binary: binary})
	dir := t.TempDir()
	library := filepath.Join(dir, "Claude-3p", "configLibrary")
	appConfig := filepath.Join(dir, "Claude-3p", "claude_desktop_config.json")
	h.Require.NoError(h.Execute("grid", "setup", "--harness", "claude", "--config-dir", dir, "--yes"))
	h.Require.Contains(h.Stderr.String(), "Quit and reopen Claude Desktop")
	configs, err := filepath.Glob(filepath.Join(library, "*-*.json"))
	h.Require.NoError(err)
	h.Require.Len(configs, 1, "one Baseten config in the library")
	settings := readHarnessSettings(t, "claude", configs[0])
	h.Require.Equal("gateway", settings["inferenceProvider"])
	h.Require.Equal(map[string]any{"X-Baseten-Grid-Client": "claude-code"}, settings["inferenceCustomHeaders"])
	h.Require.Equal([]any{map[string]any{"name": "claude-desktop-route-primary", "labelOverride": "Primary", "anthropicFamilyTier": "sonnet"}}, settings["inferenceModels"])
	h.Require.Equal(true, settings["autoModeEnabled"])
	h.Require.Equal(true, settings["toolSearchEnabled"])
	h.Require.Equal([]any{"grid", "token", "--harness", "claude", "--profile", "alice"}, settings["inferenceCredentialHelperArgs"])
	h.Require.Equal(strings.TrimSuffix(filepath.Base(configs[0]), ".json"), readHarnessSettings(t, "claude", filepath.Join(library, "_meta.json"))["appliedId"])
	h.Require.Equal("3p", readHarnessSettings(t, "claude", appConfig)["deploymentMode"])
	h.Require.FileExists(filepath.Join(dir, "settings.json"), "Claude Code's settings are written for the app's engine too")
	h.Require.NoError(h.Execute("grid", "status", "--harness", "claude", "--config-dir", dir, "--output", "json"))
	var statuses public.GridStatusList
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &statuses))
	h.Require.Len(statuses.Items, 1)
	h.Require.True(statuses.Items[0].Installed)
	h.Require.Equal("configured", statuses.Items[0].State)
	h.Require.Contains(statuses.Items[0].ManagedSettings, "inferenceModels")
	h.Require.NoError(h.Execute("grid", "teardown", "--harness", "claude", "--config-dir", dir, "--yes"))
	h.Require.Equal("1p", readHarnessSettings(t, "claude", appConfig)["deploymentMode"])
	h.Require.NotContains(readHarnessSettings(t, "claude", filepath.Join(library, "_meta.json")), "appliedId")
	h.Require.NoFileExists(configs[0])
}

func Test_Grid_Setup_TextSummary(t *testing.T) {
	skipUnlessSupported(t)
	h, _ := fakeGridAPI(t)
	dir := t.TempDir()
	// The helper runs this binary, which is not the baseten on PATH.
	bin := t.TempDir()
	h.Require.NoError(os.WriteFile(filepath.Join(bin, "baseten"), []byte("#!/bin/sh\n"), 0o755))
	t.Setenv("PATH", bin)
	h.Require.NoError(h.Execute("grid", "setup", "--harness", "claude", "--config-dir", dir, "--yes"))
	h.Require.Empty(h.Stdout.String())
	h.Require.Contains(h.Stderr.String(), "but baseten on your PATH is "+filepath.Join(bin, "baseten")+"; harnesses will run the former")
	h.Require.Contains(h.Stderr.String(), "Configuration saved.")
	h.Require.Contains(h.Stderr.String(), "Undo with: baseten grid teardown --harness claude --config-dir '"+dir+"'")
	h.Require.NoError(h.Execute("grid", "setup", "--harness", "claude", "--config-dir", dir, "--yes", "--verbose"))
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
			args := []string{"grid", "setup", "--harness", "opencode", "--config-dir", t.TempDir(), "--yes"}
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
			h.Require.Equal(tc.want, harnessRecordFile(t).Harnesses["opencode"].TeamID)
			h.Require.Equal(1, countCalls(api, "POST", "/v1/teams/"+tc.want+"/api_keys"))
		})
	}
	// Ambiguity is reported by the routes API, without a client-side team lookup.
	h, api := fakeGridAPI(t)
	api.SetRoute("GET", "/v1/grid/routes", 400, map[string]any{"error": "Team ID is required when you belong to multiple."})
	dir := t.TempDir()
	h.Require.Error(h.Execute("grid", "setup", "--harness", "claude", "--config-dir", dir, "--yes", "--output", "json"))
	h.Require.Contains(h.Stderr.String(), "Team ID is required")
	h.Require.Equal(400, decodeJSONErrorEnvelope(h).APIStatusCode)
	h.Require.Len(api.Calls(), 1)
	h.Require.Nil(api.FindCall("GET", "/v1/teams"))
	h.Require.NoFileExists(harnessSettingsFile("claude", dir))
	h.Require.Empty(harnessRecordFile(t).Harnesses)
}

func Test_Grid_Setup_KeyPerTeamAndUser(t *testing.T) {
	skipUnlessSupported(t)
	h, api := fakeGridAPI(t)
	api.SetRoute("GET", "/v1/teams", 200, gridTwoTeams)
	dir := t.TempDir()
	path := harnessSettingsFile("opencode", dir)
	for _, team := range []string{"team-a", "team-b", "team-a"} {
		h.Require.NoError(h.Execute("grid", "setup", "--harness", "opencode", "--config-dir", dir, "--team", team, "--key-name", "laptop", "--yes"))
		settings, err := os.ReadFile(path)
		h.Require.NoError(err)
		h.Require.Contains(string(settings), "secret-"+team)
	}
	h.Require.Equal(1, countCalls(api, "POST", "/v1/teams/team-a/api_keys"))
	h.Require.Equal(1, countCalls(api, "POST", "/v1/teams/team-b/api_keys"))
	h.Require.Equal(map[string]any{"type": "GRID", "name": "laptop"}, api.FindCall("POST", "/v1/teams/team-a/api_keys").BodyJSON(t))
	key, err := configDirStore(t).GetRoutesKey(t.Context(), auth.RoutesKeyScope{ManagementURL: api.URL, Profile: "alice", UserID: "user-a", TeamID: "team-b"})
	h.Require.NoError(err)
	h.Require.Equal("secret-team-b", key)

	// Another user gets their own key.
	api.SetRoute("GET", "/v1/users/me", 200, map[string]string{"user_id": "user-b"})
	h.Require.NoError(h.Execute("grid", "setup", "--harness", "opencode", "--config-dir", dir, "--team", "team-a", "--key-name", "laptop", "--yes"))
	h.Require.Equal(2, countCalls(api, "POST", "/v1/teams/team-a/api_keys"))
}

func Test_Grid_Setup_DefaultKeyName(t *testing.T) {
	skipUnlessSupported(t)
	h, api := fakeGridAPI(t)
	h.Require.NoError(h.Execute("grid", "setup", "--harness", "opencode", "--config-dir", t.TempDir(), "--yes"))
	name := api.FindCall("POST", "/v1/teams/team-a/api_keys").BodyJSON(t)["name"].(string)
	h.Require.Regexp(`^baseten-harness-[a-z0-9]+(-[a-z0-9]+)*-[0-9a-f]{6}$`, name)

	// A saved key that's since been deleted is replaced.
	api.SetRoute("GET", "/v1/api_keys", 200, map[string]any{"keys": []any{}})
	h.Require.NoError(h.Execute("grid", "setup", "--harness", "opencode", "--config-dir", t.TempDir(), "--yes"))
	h.Require.Equal(2, countCalls(api, "POST", "/v1/teams/team-a/api_keys"))
}

func Test_Grid_Setup_KeyCreationFailureCanBeRetried(t *testing.T) {
	skipUnlessSupported(t)
	h, api := fakeGridAPI(t)
	dir := t.TempDir()
	args := []string{"grid", "setup", "--harness", "opencode", "--config-dir", dir, "--yes"}
	api.SetRoute("POST", "/v1/teams/team-a/api_keys", 500, map[string]string{"message": "unavailable"})
	h.Require.Error(h.Execute(args...))
	h.Require.Contains(h.Stderr.String(), "creating Grid API key")
	h.Require.NoFileExists(harnessSettingsFile("opencode", dir))
	api.SetRoute("POST", "/v1/teams/team-a/api_keys", 200, map[string]string{"api_key": "secret-team-a"})
	h.Require.NoError(h.Execute(args...))
	h.Require.FileExists(harnessSettingsFile("opencode", dir))
}

func Test_Grid_Setup_KeyringUnavailableStoresPlaintext(t *testing.T) {
	skipUnlessSupported(t)
	h, api := fakeGridAPI(t)
	keyring.MockInitWithError(errors.New("no keyring"))
	t.Cleanup(keyring.MockInit)
	// The profile's key moves to plain text with the keyring.
	h.Require.NoError(configDirStore(t).SetAPIKeyProfile(t.Context(), "alice", api.URL, "test-key", true, nil))
	args := []string{"grid", "setup", "--harness", "opencode", "--config-dir", t.TempDir(), "--yes"}
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
	h.Require.NoError(h.Execute("grid", "setup", "--harness", "claude", "--config-dir", dir, "--background-route-id", "route-primary", "--yes"))
	env := readHarnessSettings(t, "claude", harnessSettingsFile("claude", dir))["env"].(map[string]any)
	h.Require.Equal("acme/primary", env["ANTHROPIC_DEFAULT_HAIKU_MODEL"])
}

func Test_Grid_Setup_SubagentRouteUnsupportedHarnessWarns(t *testing.T) {
	skipUnlessSupported(t)
	for _, name := range []string{"codex", "pi"} {
		t.Run(name, func(t *testing.T) {
			h, _ := fakeGridAPI(t)
			dir := t.TempDir()
			require.NoError(t, h.Execute("grid", "setup", "--harness", name, "--config-dir", dir, "--subagent-route-id", "route-primary", "--yes"))
			require.Contains(t, h.Stderr.String(), "warning: --subagent-route-id is not supported by "+name+"; it will be ignored")
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
			harnessName: "claude",
			endpoint: func(api *MockManagementAPI) {
				api.SetRoute("GET", "/v1/grid/team_settings", 200, claudeHarnessDefaultsBody("acme/server", "acme/background"))
			},
			wantFetch:      true,
			wantPrimary:    "acme/server",
			wantBackground: "acme/background",
		},
		{
			name:        "route flag wins over server default",
			harnessName: "claude",
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
			harnessName: "claude",
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
			harnessName: "claude",
			endpoint: func(api *MockManagementAPI) {
				api.SetRoute("GET", "/v1/grid/team_settings", 200, claudeHarnessDefaultsBody("acme/missing", "acme/background"))
			},
			wantFetch:      true,
			wantPrimary:    "acme/first",
			wantBackground: "acme/background",
		},
		{
			name:        "server background missing from routes falls back to the default",
			harnessName: "claude",
			endpoint: func(api *MockManagementAPI) {
				api.SetRoute("GET", "/v1/grid/team_settings", 200, claudeHarnessDefaultsBody("acme/server", "acme/missing"))
			},
			wantFetch:      true,
			wantPrimary:    "acme/server",
			wantBackground: "deepseek-ai/DeepSeek-V4.1-Flash",
		},
		{
			name:        "server defaults without a primary still seed the background",
			harnessName: "claude",
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
			harnessName: "claude",
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
			harnessName:    "claude",
			wantFetch:      true,
			wantPrimary:    "acme/first",
			wantBackground: "deepseek-ai/DeepSeek-V4.1-Flash",
		},
		{
			name:        "a routes settings error falls back to positional",
			harnessName: "claude",
			endpoint: func(api *MockManagementAPI) {
				api.SetRoute("GET", "/v1/grid/team_settings", 500, map[string]string{"message": "unavailable"})
			},
			wantFetch:      true,
			wantPrimary:    "acme/first",
			wantBackground: "deepseek-ai/DeepSeek-V4.1-Flash",
		},
		{
			name:        "a malformed routes settings response falls back to positional",
			harnessName: "claude",
			endpoint: func(api *MockManagementAPI) {
				api.SetRoute("GET", "/v1/grid/team_settings", 200, "not an object")
			},
			wantFetch:      true,
			wantPrimary:    "acme/first",
			wantBackground: "deepseek-ai/DeepSeek-V4.1-Flash",
		},
		{
			name:        "both route flags set skip the fetch",
			harnessName: "claude",
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
			if tc.harnessName == "claude" {
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
	path := harnessSettingsFile("claude", dir)
	h.Require.NoError(os.WriteFile(path, []byte(`{"model":"previous","theme":"dark","modelPicker":{"options":[{"model":"my/model","label":"Custom"}]}}`), 0o600))
	args := []string{"grid", "setup", "--harness", "claude", "--config-dir", dir}
	h.Require.NoError(h.Execute(append(args, "--dry-run", "--output", "json")...))
	var preview public.GridPlanList
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &preview))
	h.Require.Contains(preview.Items[0].ReplacedSettings, "model")
	h.Require.Contains(preview.Items[0].ReplacedSettings, "modelPicker.options")
	h.Require.NoError(h.Execute(append(args, "--dry-run")...))
	h.Require.Contains(h.Stdout.String(), "Existing integration settings will be overwritten")

	h.Require.NoError(h.Execute(append(args, "--yes")...))
	h.Require.Contains(h.Stderr.String(), "existing integration settings in claude will be overwritten")
	picker := readHarnessSettings(t, "claude", path)["modelPicker"].(map[string]any)
	h.Require.Equal([]any{map[string]any{"model": "acme/primary", "label": "Primary"}}, picker["options"])
	h.Require.NoError(h.Execute("grid", "teardown", "--harness", "claude", "--config-dir", dir, "--yes"))
	h.Require.Equal(map[string]any{"theme": "dark"}, readHarnessSettings(t, "claude", path))
}

func Test_Grid_Setup_Status_Teardown_DefaultDiscovery(t *testing.T) {
	skipUnlessSupported(t)
	h, api := fakeGridAPI(t)
	root := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(root, "claude"))
	t.Setenv("CODEX_HOME", filepath.Join(root, "codex"))
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(root, "pi"))
	h.Require.NoError(h.Execute("grid", "setup", "--harness", "opencode", "--harness", "codex", "--yes", "--output", "json"))
	var setup public.GridPlanList
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &setup))
	h.Require.Len(setup.Items, 3) // Two settings files and the Codex model catalog.
	// A malformed file warns instead of hiding the other harnesses.
	pi := filepath.Join(root, "pi", "models.json")
	h.Require.NoError(os.MkdirAll(filepath.Dir(pi), 0o700))
	h.Require.NoError(os.WriteFile(pi, []byte("{ not json"), 0o600))
	calls := len(api.Calls())

	// Status and teardown find integrations whose executables are gone.
	h.Context = internalcmd.WithExecer(h.Context, missingHarnessExecer{})
	h.Require.NoError(h.Execute("grid", "status", "--output", "json"))
	h.Require.Contains(h.Stderr.String(), "warning: skipping pi: settings at")
	h.Require.Contains(h.Stderr.String(), "are misconfigured")
	var statuses public.GridStatusList
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &statuses))
	h.Require.Len(statuses.Items, 2)
	for _, status := range statuses.Items {
		h.Require.False(status.Installed)
		h.Require.Equal("configured", status.State)
	}
	h.Require.Error(h.Execute("grid", "status", "--harness", "pi"))
	h.Require.Contains(h.Stderr.String(), "invalid settings JSON")

	h.Require.Equal(calls, len(api.Calls()), "status is local")

	// Codex runs the token helper, so removing it leaves OpenCode's key alone.
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
	h.Require.NoError(h.Execute("grid", "setup", "--harness", "claude", "--harness", "opencode", "--yes"))
	h.Require.NoError(h.Execute("grid", "teardown", "--harness", "claude", "--yes"))
	h.Require.Equal(0, countCalls(api, "DELETE", "/v1/api_keys/secret-team-a"), "Claude Code runs the token helper, so OpenCode's key stays")
	h.Require.NoError(h.Execute("grid", "teardown", "--yes", "--output", "json"))
	var result public.GridPlanList
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &result))
	h.Require.Equal([]string{"secret-team-a"}, result.DeletedAPIKeys)
	h.Require.Equal(1, countCalls(api, "DELETE", "/v1/api_keys/secret-team-a"))

	// A failed delete leaves the settings in place so teardown can be retried.
	h.Require.NoError(h.Execute("grid", "setup", "--harness", "opencode", "--yes"))
	api.SetRoute("DELETE", "/v1/api_keys/secret-team-a", 500, map[string]string{"message": "unavailable"})
	h.Require.Error(h.Execute("grid", "teardown", "--yes"))
	h.Require.Contains(h.Stderr.String(), "deleting Grid API key secret-team-a")
	h.Require.NoError(h.Execute("grid", "status", "--harness", "opencode", "--output", "json"))
	h.Require.Contains(h.Stdout.String(), `"state": "configured"`)
}

// harnessGateway records the model and client each inference request names and
// refuses it.
type harnessGateway struct {
	mu             sync.Mutex
	models         []string
	clients        []string
	requestClasses []string
	bodies         []string
}

func (g *harnessGateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Model string `json:"model"`
	}
	raw, _ := io.ReadAll(r.Body)
	if json.Unmarshal(raw, &body) == nil && body.Model != "" {
		g.mu.Lock()
		g.models = append(g.models, body.Model)
		g.clients = append(g.clients, r.Header.Get("X-Baseten-Grid-Client"))
		g.requestClasses = append(g.requestClasses, r.Header.Get("x-claude-code-request-class"))
		g.bodies = append(g.bodies, string(raw))
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

func (g *harnessGateway) sawWith(model, text string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	for i, requested := range g.models {
		if requested == model && strings.Contains(g.bodies[i], text) {
			return true
		}
	}
	return false
}

func (g *harnessGateway) sentClients() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return slices.Clone(g.clients)
}

func (g *harnessGateway) sentBodies() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return slices.Clone(g.bodies)
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
		{label: "claude", name: "claude", binary: "claude", args: []string{"-p", "Reply with OK."}},
		{label: "codex", name: "codex", binary: "codex", args: []string{"exec", "--skip-git-repo-check", "Reply with OK."}},
		{label: "opencode/v1", name: "opencode", binary: "opencode", args: []string{"run", "Reply with OK."}, seed: `{"snapshot": false}`},
		{label: "pi", name: "pi", binary: "pi", args: []string{"--print", "Reply with OK."}},
	}
	if opencode2 := os.Getenv("BASETEN_TEST_OPENCODE2"); opencode2 != "" {
		// OpenCode 2 runs a shared background server per user by default; a
		// private one keeps the test off the developer's.
		args := []string{"run", "--standalone", "Reply with OK."}
		cases = append(cases,
			harnessCase{label: "opencode2/v1", name: "opencode", binary: opencode2, args: args, seed: `{"snapshot": false}`},
			harnessCase{label: "opencode2/v2", name: "opencode", binary: opencode2, args: args, seed: `{"providers": {}, "snapshot": false}`},
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
				"claude":   filepath.Join(root, ".claude"),
				"codex":    filepath.Join(root, ".codex"),
				"opencode": filepath.Join(root, ".config", "opencode"),
				"pi":       filepath.Join(root, ".pi", "agent"),
			}[tc.name]
			if tc.seed != "" {
				h.Require.NoError(os.MkdirAll(configDir, 0o700))
				h.Require.NoError(os.WriteFile(harnessSettingsFile(tc.name, configDir), []byte(tc.seed), 0o600))
			}
			// The token helper would run this test binary, so the session's key is installed instead.
			h.Require.NoError(h.Execute("grid", "setup", "--harness", tc.name, "--config-dir", configDir, "--use-session-credential", "--yes", "--git-context"))

			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
			defer cancel()
			run := exec.CommandContext(ctx, binary, tc.args...)
			run.Dir = t.TempDir()
			var gitContext string
			if tc.name != "codex" {
				gitContext = initHarnessGitRepo(t, run.Dir)
			}
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
			for !gateway.sawWith("acme/primary", gitContext) && !exited && ctx.Err() == nil {
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
			wantClient := tc.name
			if tc.name == harness.Claude {
				wantClient = harness.ClaudeCode
			}
			h.Require.Contains(gateway.sentClients(), wantClient, "X-Baseten-Grid-Client")
			if gitContext != "" {
				h.Require.True(slices.ContainsFunc(gateway.sentBodies(), func(body string) bool { return strings.Contains(body, gitContext) }), "git context %q", gitContext)
			}
			if tc.name == harness.Claude {
				h.Require.True(slices.ContainsFunc(gateway.sentRequestClasses(), func(v string) bool { return v != "" }), "x-claude-code-request-class")
			}
		})
	}
}

func initHarnessGitRepo(t *testing.T, dir string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	run := func(args ...string) string {
		command := exec.Command("git", args...)
		command.Dir = dir
		out, err := command.Output()
		require.NoError(t, err)
		return strings.TrimSpace(string(out))
	}
	run("init", "-q")
	run("-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false", "commit", "-q", "--allow-empty", "-m", "init")
	run("remote", "add", "origin", "https://user:secret@example.com/org/repo.git")
	return "repo=https://example.com/org/repo.git branch=" + run("branch", "--show-current") + " commit=" + run("rev-parse", "HEAD")
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
	h.Require.NoError(h.Execute("grid", "setup", "--harness", "claude", "--harness", "opencode", "--primary-route-id", "route-auto", "--background-route-id", "route-auto", "--yes"))
	h.Require.NoError(h.Execute("grid", "setup", "--harness", "codex", "--primary-route-id", "route-auto", "--yes"))
	h.Require.NotContains(h.Stderr.String(), "skipping routes")

	claude := readHarnessSettings(t, "claude", filepath.Join(root, "claude", "settings.json"))
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

func serveServedModelRoutes(api *MockManagementAPI, routers ...string) {
	items := []any{map[string]any{"team_id": "team-a", "team_name": "Engineering", "id": "route-primary", "name": "acme/primary", "display_name": "Primary", "invoke_url": api.URL, "metadata": harnessRouteMetadata,
		"target": map[string]any{"type": "BASETEN_MODEL_API", "model": "deepseek"}}}
	wide := maps.Clone(harnessRouteMetadata)
	wide["context_window"] = 1000000
	for _, router := range routers {
		name, display, labeled := strings.Cut(router, "=")
		if !labeled {
			display = name
		}
		items = append(items, map[string]any{"team_id": "team-a", "team_name": "Engineering", "id": "route-" + name, "name": "acme/" + name, "display_name": display, "invoke_url": api.URL, "metadata": wide,
			"target": map[string]any{"type": "CLASSIFIER", "default_route": map[string]any{"id": "route-primary", "slug": "acme/primary"}, "allowed_routes": []any{}}})
	}
	api.SetRoute("GET", "/v1/grid/routes", 200, map[string]any{"items": items, "pagination": map[string]any{"has_more": false}})
}

func claudeManagedSettings(t *testing.T, h *CommandHarness, dir string) []string {
	t.Helper()
	h.Require.NoError(h.Execute("grid", "status", "--harness", "claude", "--config-dir", dir, "--output", "json"))
	var statuses public.GridStatusList
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &statuses))
	h.Require.Len(statuses.Items, 1)
	return statuses.Items[0].ManagedSettings
}

func Test_Grid_Setup_ServedModelStatusLine(t *testing.T) {
	skipUnlessSupported(t)
	h, api := fakeGridAPI(t)
	serveServedModelRoutes(api, "auto")
	dir := t.TempDir()
	path := harnessSettingsFile("claude", dir)
	h.Require.NoError(os.WriteFile(path, []byte(`{"theme":"dark"}`), 0o600))
	cli, err := exec.LookPath("baseten")
	h.Require.NoError(err)
	setup := []string{"grid", "setup", "--harness", "claude", "--config-dir", dir, "--yes", "--output", "json"}

	h.Require.NoError(h.Execute(setup...))
	settings := readHarnessSettings(t, "claude", path)
	h.Require.Equal(map[string]any{"type": "command", "command": "'" + cli + "' grid claude-served-model --router 'acme/auto=auto'"}, settings["statusLine"])
	h.Require.Contains(settings["availableModels"], "acme/auto[1m]")
	h.Require.Equal("dark", settings["theme"])
	h.Require.NotContains(settings, "hooks")
	h.Require.NotContains(h.Stderr.String(), "Existing statusLine")

	h.Require.NoError(h.Execute(setup...))
	var rerun public.GridPlanList
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &rerun))
	h.Require.Len(rerun.Items, 1)
	h.Require.False(rerun.Items[0].Changed, "rerunning setup changes nothing")
	h.Require.Contains(claudeManagedSettings(t, h, dir), "statusLine")

	serveServedModelRoutes(api, "auto", "code")
	h.Require.NoError(h.Execute(setup...))
	h.Require.Equal("'"+cli+"' grid claude-served-model --router 'acme/auto=auto' --router 'acme/code=code'", readHarnessSettings(t, "claude", path)["statusLine"].(map[string]any)["command"])

	h.Require.NoError(h.Execute("grid", "teardown", "--harness", "claude", "--config-dir", dir, "--yes"))
	h.Require.Equal(map[string]any{"theme": "dark"}, readHarnessSettings(t, "claude", path))
}

func Test_Grid_Setup_ServedModelStopHook(t *testing.T) {
	skipUnlessSupported(t)
	h, api := fakeGridAPI(t)
	serveServedModelRoutes(api, "auto")
	dir := t.TempDir()
	path := harnessSettingsFile("claude", dir)
	user := `{"statusLine":{"type":"command","command":"~/.claude/statusline.sh","padding":0},` +
		`"hooks":{"Stop":[{"hooks":[{"type":"command","command":"say done","timeout":30}]}],"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"guard"}]}]}}`
	h.Require.NoError(os.WriteFile(path, []byte(user), 0o600))
	var original map[string]any
	h.Require.NoError(json.Unmarshal([]byte(user), &original))
	userStop := original["hooks"].(map[string]any)["Stop"].([]any)[0]
	cli, err := exec.LookPath("baseten")
	h.Require.NoError(err)
	ours := func(routers string) map[string]any {
		return map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "'" + cli + "' grid claude-served-model " + routers + " --hook"}}}
	}
	setup := []string{"grid", "setup", "--harness", "claude", "--config-dir", dir, "--yes"}

	for range 2 {
		h.Require.NoError(h.Execute(setup...))
		h.Require.Contains(h.Stderr.String(), "Existing statusLine in "+path+" left unchanged; for auto routes, the serving model will be shown after each response through a Stop hook instead.")
		h.Require.NotContains(h.Stderr.String(), "will be overwritten")
		settings := readHarnessSettings(t, "claude", path)
		h.Require.Equal(original["statusLine"], settings["statusLine"])
		hooks := settings["hooks"].(map[string]any)
		h.Require.Equal([]any{userStop, ours("--router 'acme/auto=auto'")}, hooks["Stop"])
		h.Require.Equal(original["hooks"].(map[string]any)["PreToolUse"], hooks["PreToolUse"])
	}
	managed := claudeManagedSettings(t, h, dir)
	h.Require.Contains(managed, "hooks.Stop")
	h.Require.NotContains(managed, "statusLine")

	settings := readHarnessSettings(t, "claude", path)
	later := map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "notify"}}}
	settings["hooks"].(map[string]any)["Stop"] = append(settings["hooks"].(map[string]any)["Stop"].([]any), later)
	raw, err := json.Marshal(settings)
	h.Require.NoError(err)
	h.Require.NoError(os.WriteFile(path, raw, 0o600))
	serveServedModelRoutes(api, "auto", "code")
	h.Require.NoError(h.Execute(setup...))
	h.Require.Equal([]any{userStop, ours("--router 'acme/auto=auto' --router 'acme/code=code'"), later}, readHarnessSettings(t, "claude", path)["hooks"].(map[string]any)["Stop"])

	h.Require.NoError(h.Execute("grid", "teardown", "--harness", "claude", "--config-dir", dir, "--yes"))
	original["hooks"].(map[string]any)["Stop"] = []any{userStop, later}
	h.Require.Equal(original, readHarnessSettings(t, "claude", path))
}

func Test_Grid_Setup_ServedModelSharedStopGroup(t *testing.T) {
	skipUnlessSupported(t)
	h, api := fakeGridAPI(t)
	serveServedModelRoutes(api, "auto")
	dir := t.TempDir()
	path := harnessSettingsFile("claude", dir)
	statusLine := map[string]any{"type": "command", "command": "my-status"}
	h.Require.NoError(os.WriteFile(path, []byte(`{"statusLine":{"type":"command","command":"my-status"}}`), 0o600))
	cli, err := exec.LookPath("baseten")
	h.Require.NoError(err)
	ours := func(routers string) map[string]any {
		return map[string]any{"type": "command", "command": "'" + cli + "' grid claude-served-model " + routers + " --hook"}
	}
	notify := map[string]any{"type": "command", "command": "notify"}
	setup := []string{"grid", "setup", "--harness", "claude", "--config-dir", dir, "--yes"}

	h.Require.NoError(h.Execute(setup...))
	settings := readHarnessSettings(t, "claude", path)
	h.Require.Equal([]any{map[string]any{"hooks": []any{ours("--router 'acme/auto=auto'")}}}, settings["hooks"].(map[string]any)["Stop"])
	settings["hooks"].(map[string]any)["Stop"] = []any{
		map[string]any{"matcher": "*", "hooks": []any{ours("--router 'acme/auto=auto'"), notify}},
		map[string]any{"hooks": []any{ours("--router 'acme/old'")}},
	}
	raw, err := json.Marshal(settings)
	h.Require.NoError(err)
	h.Require.NoError(os.WriteFile(path, raw, 0o600))

	serveServedModelRoutes(api, "auto", "code")
	h.Require.NoError(h.Execute(setup...))
	h.Require.Equal([]any{map[string]any{"matcher": "*", "hooks": []any{ours("--router 'acme/auto=auto' --router 'acme/code=code'"), notify}}},
		readHarnessSettings(t, "claude", path)["hooks"].(map[string]any)["Stop"])

	h.Require.NoError(h.Execute("grid", "teardown", "--harness", "claude", "--config-dir", dir, "--yes"))
	h.Require.Equal(map[string]any{"statusLine": statusLine, "hooks": map[string]any{"Stop": []any{map[string]any{"matcher": "*", "hooks": []any{notify}}}}},
		readHarnessSettings(t, "claude", path))
}

func Test_Grid_Setup_ServedModelForeignStatusLine(t *testing.T) {
	skipUnlessSupported(t)
	for name, command := range map[string]func(cli string) string{
		"composed": func(string) string {
			return `echo "$(baseten grid claude-served-model --router acme/auto) | custom stats"`
		},
		"suffixed": func(cli string) string {
			return "'" + cli + "' grid claude-served-model --router 'acme/auto=auto' ; echo hi"
		},
	} {
		t.Run(name, func(t *testing.T) {
			h, api := fakeGridAPI(t)
			serveServedModelRoutes(api, "auto")
			dir := t.TempDir()
			path := harnessSettingsFile("claude", dir)
			cli, err := exec.LookPath("baseten")
			h.Require.NoError(err)
			user := map[string]any{"statusLine": map[string]any{"type": "command", "command": command(cli)}}
			raw, err := json.Marshal(user)
			h.Require.NoError(err)
			h.Require.NoError(os.WriteFile(path, raw, 0o600))

			h.Require.NoError(h.Execute("grid", "setup", "--harness", "claude", "--config-dir", dir, "--yes"))
			h.Require.Contains(h.Stderr.String(), "Existing statusLine in "+path+" left unchanged; for auto routes, the serving model will be shown after each response through a Stop hook instead.")
			settings := readHarnessSettings(t, "claude", path)
			h.Require.Equal(user["statusLine"], settings["statusLine"])
			h.Require.Equal([]any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "'" + cli + "' grid claude-served-model --router 'acme/auto=auto' --hook"}}}},
				settings["hooks"].(map[string]any)["Stop"])

			h.Require.NoError(h.Execute("grid", "teardown", "--harness", "claude", "--config-dir", dir, "--yes"))
			h.Require.Equal(user, readHarnessSettings(t, "claude", path))
		})
	}
}

func Test_Grid_Setup_ServedModelZeroRouters(t *testing.T) {
	skipUnlessSupported(t)
	h, api := fakeGridAPI(t)
	dir := t.TempDir()
	path := harnessSettingsFile("claude", dir)
	setup := []string{"grid", "setup", "--harness", "claude", "--config-dir", dir, "--yes"}

	serveServedModelRoutes(api)
	h.Require.NoError(h.Execute(setup...))
	settings := readHarnessSettings(t, "claude", path)
	h.Require.NotContains(settings, "statusLine")
	h.Require.NotContains(settings, "hooks")

	serveServedModelRoutes(api, "auto")
	h.Require.NoError(h.Execute(setup...))
	h.Require.Contains(readHarnessSettings(t, "claude", path), "statusLine")
	serveServedModelRoutes(api)
	h.Require.NoError(h.Execute(setup...))
	h.Require.NotContains(readHarnessSettings(t, "claude", path), "statusLine")

	settings = readHarnessSettings(t, "claude", path)
	settings["statusLine"] = map[string]any{"type": "command", "command": "my-status"}
	settings["hooks"] = map[string]any{"Stop": []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "say done"}}}}}
	raw, err := json.Marshal(settings)
	h.Require.NoError(err)
	h.Require.NoError(os.WriteFile(path, raw, 0o600))
	serveServedModelRoutes(api, "auto")
	h.Require.NoError(h.Execute(setup...))
	h.Require.Len(readHarnessSettings(t, "claude", path)["hooks"].(map[string]any)["Stop"], 2)
	serveServedModelRoutes(api)
	h.Require.NoError(h.Execute(setup...))
	h.Require.Equal(settings, readHarnessSettings(t, "claude", path))
}

func Test_Grid_Setup_ServedModelSessionCredential(t *testing.T) {
	skipUnlessSupported(t)
	h, api := fakeGridAPI(t)
	serveServedModelRoutes(api, "auto")
	dir := t.TempDir()
	bin := t.TempDir()
	h.Require.NoError(os.WriteFile(filepath.Join(bin, "baseten"), []byte("#!/bin/sh\n"), 0o755))
	t.Setenv("PATH", bin)
	exe, err := os.Executable()
	h.Require.NoError(err)
	h.Require.NoError(h.Execute("grid", "setup", "--harness", "claude", "--config-dir", dir, "--use-session-credential", "--yes"))
	h.Require.Contains(h.Stderr.String(), "but baseten on your PATH is "+filepath.Join(bin, "baseten")+"; harnesses will run the former")
	settings := readHarnessSettings(t, "claude", harnessSettingsFile("claude", dir))
	h.Require.NotContains(settings, "apiKeyHelper")
	h.Require.Equal("'"+exe+"' grid claude-served-model --router 'acme/auto=auto'", settings["statusLine"].(map[string]any)["command"])
}

func Test_Grid_Setup_ServedModelOptOut(t *testing.T) {
	skipUnlessSupported(t)
	h, api := fakeGridAPI(t)
	serveServedModelRoutes(api, "auto")
	cli, err := exec.LookPath("baseten")
	h.Require.NoError(err)
	ours := "'" + cli + "' grid claude-served-model --router 'acme/auto=auto'"
	dir := t.TempDir()
	path := harnessSettingsFile("claude", dir)
	guard := map[string]any{"PreToolUse": []any{map[string]any{"matcher": "Bash", "hooks": []any{map[string]any{"type": "command", "command": "guard"}}}}}
	raw, err := json.Marshal(map[string]any{"theme": "dark", "hooks": guard})
	h.Require.NoError(err)
	h.Require.NoError(os.WriteFile(path, raw, 0o600))
	setup := []string{"grid", "setup", "--harness", "claude", "--config-dir", dir, "--yes"}
	record := func() string {
		raw, err := os.ReadFile(filepath.Join(recordDir(t), "harness.json"))
		h.Require.NoError(err)
		return string(raw)
	}

	h.Require.NoError(h.Execute(setup...))
	h.Require.Equal(ours, readHarnessSettings(t, "claude", path)["statusLine"].(map[string]any)["command"])
	h.Require.NoError(h.Execute(append(setup, "--served-model=false")...))
	settings := readHarnessSettings(t, "claude", path)
	h.Require.NotContains(settings, "statusLine")
	h.Require.Equal(guard, settings["hooks"])
	h.Require.Equal("dark", settings["theme"])
	h.Require.True(harnessRecordFile(t).Harnesses["claude"].NoClaudeServedModel)
	h.Require.Contains(record(), `"no_claude_served_model": true`)

	settings["statusLine"] = map[string]any{"type": "command", "command": ours}
	raw, err = json.Marshal(settings)
	h.Require.NoError(err)
	h.Require.NoError(os.WriteFile(path, raw, 0o600))
	h.Require.NoError(h.Execute("grid", "sync", "--yes"))
	h.Require.NotContains(readHarnessSettings(t, "claude", path), "statusLine", "sync replays the recorded opt-out")

	h.Require.NoError(h.Execute(setup...))
	h.Require.Equal(ours, readHarnessSettings(t, "claude", path)["statusLine"].(map[string]any)["command"], "setup without the flag turns it back on")
	h.Require.False(harnessRecordFile(t).Harnesses["claude"].NoClaudeServedModel)
	h.Require.NotContains(record(), "no_claude_served_model")
	h.Require.NoError(h.Execute(append(setup, "--served-model=false")...))
	h.Require.True(harnessRecordFile(t).Harnesses["claude"].NoClaudeServedModel)
	h.Require.NoError(h.Execute(append(setup, "--served-model")...))
	h.Require.Equal(ours, readHarnessSettings(t, "claude", path)["statusLine"].(map[string]any)["command"], "the explicit flag turns it back on")
	h.Require.NotContains(record(), "no_claude_served_model")

	userStatusLine := map[string]any{"type": "command", "command": "my-status"}
	userStop := map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "say done"}}}
	raw, err = json.Marshal(map[string]any{"statusLine": userStatusLine, "hooks": map[string]any{"Stop": []any{userStop}}})
	h.Require.NoError(err)
	h.Require.NoError(os.WriteFile(path, raw, 0o600))
	h.Require.NoError(h.Execute(setup...))
	h.Require.Len(readHarnessSettings(t, "claude", path)["hooks"].(map[string]any)["Stop"], 2)
	h.Require.NoError(h.Execute(append(setup, "--served-model=false")...))
	settings = readHarnessSettings(t, "claude", path)
	h.Require.Equal(userStatusLine, settings["statusLine"])
	h.Require.Equal(map[string]any{"Stop": []any{userStop}}, settings["hooks"])
}

func Test_Grid_Setup_ServedModelPrompt(t *testing.T) {
	skipUnlessSupported(t)
	h, api := fakeGridAPI(t)
	serveServedModelRoutes(api, "auto")
	base := h.Context
	answer := func(answers ...string) { h.Context = internalcmd.WithPromptAnswers(base, answers...) }
	dir := t.TempDir()
	path := harnessSettingsFile("claude", dir)
	setup := []string{"grid", "setup", "--harness", "claude", "--config-dir", dir}
	question := "Show which model served each response on intelligent routes in Claude Code?"
	shown := func() bool {
		_, ok := readHarnessSettings(t, "claude", path)["statusLine"]
		return ok
	}
	recorded := func() bool { return harnessRecordFile(t).Harnesses["claude"].NoClaudeServedModel }

	answer("1", "1")
	h.Require.NoError(h.Execute(setup...))
	h.Require.Contains(h.Stderr.String(), question)
	h.Require.True(shown())

	answer("2", "1")
	h.Require.NoError(h.Execute(setup...))
	h.Require.Contains(h.Stderr.String(), question)
	h.Require.False(shown(), "answering no removes the display")
	h.Require.True(recorded())
	h.Context = base
	h.Require.NoError(h.Execute("grid", "sync", "--yes"))
	h.Require.NotContains(h.Stderr.String(), question)
	h.Require.False(shown(), "sync replays the answer without asking")

	answer("", "1")
	h.Require.NoError(h.Execute(setup...))
	h.Require.True(shown(), "the default answer is yes")
	h.Require.False(recorded())

	answer("2")
	h.Require.NoError(h.Execute(append(setup, "--yes", "--served-model=false")...))
	h.Require.NotContains(h.Stderr.String(), question)
	h.Require.False(shown())
	h.Require.True(recorded())
	h.Require.NoError(h.Execute(append(setup, "--yes")...))
	h.Require.NotContains(h.Stderr.String(), question)
	h.Require.True(shown(), "--yes shows it without asking")
	h.Require.False(recorded())
	answer("1")
	h.Require.NoError(h.Execute(append(setup, "--served-model=false")...))
	h.Require.NotContains(h.Stderr.String(), question, "an explicit flag is not asked about")
	h.Require.False(shown())
	h.Require.True(recorded())
	answer("1")
	h.Require.NoError(h.Execute(append(setup, "--served-model")...))
	h.Require.NotContains(h.Stderr.String(), question)
	h.Require.True(shown())
	h.Require.False(recorded())
	h.Context = base
	h.Require.NoError(h.Execute(append(setup, "--yes", "--served-model=false")...))
	h.Require.False(shown(), "without a terminal the flag decides")
	h.Require.NoError(h.Execute(append(setup, "--yes")...))
	h.Require.True(shown())

	answer()
	h.Require.NoError(h.Execute(append(setup, "--dry-run")...))
	h.Require.NotContains(h.Stderr.String(), question)

	serveServedModelRoutes(api)
	answer("1")
	h.Require.NoError(h.Execute(setup...))
	h.Require.NotContains(h.Stderr.String(), question, "no router routes, no question")
	serveServedModelRoutes(api, "auto")
	answer("1")
	h.Require.NoError(h.Execute("grid", "setup", "--harness", "opencode", "--config-dir", t.TempDir()))
	h.Require.NotContains(h.Stderr.String(), question, "Claude Code is not a target")
}

func Test_Grid_Setup_ServedModelDisplayNames(t *testing.T) {
	skipUnlessSupported(t)
	h, api := fakeGridAPI(t)
	serveServedModelRoutes(api, "auto=Auto (Baseten Internal Dogfooding)", "eq=x=y's", "bare=")
	dir := t.TempDir()
	path := harnessSettingsFile("claude", dir)
	cli, err := exec.LookPath("baseten")
	h.Require.NoError(err)
	setup := []string{"grid", "setup", "--harness", "claude", "--config-dir", dir, "--yes", "--output", "json"}
	h.Require.NoError(h.Execute(setup...))
	h.Require.Equal("'"+cli+`' grid claude-served-model --router 'acme/auto=Auto (Baseten Internal Dogfooding)' --router 'acme/eq=x=y'\''s' --router 'acme/bare'`,
		readHarnessSettings(t, "claude", path)["statusLine"].(map[string]any)["command"])
	h.Require.NoError(h.Execute(setup...))
	var rerun public.GridPlanList
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &rerun))
	h.Require.False(rerun.Items[0].Changed, "setup recognizes its own command with display names")
	h.Require.NoError(h.Execute("grid", "teardown", "--harness", "claude", "--config-dir", dir, "--yes"))
	h.Require.NotContains(readHarnessSettings(t, "claude", path), "statusLine")
}

func Test_Grid_ClaudeServedModel(t *testing.T) {
	h := NewCommandHarness(t)
	dir := t.TempDir()
	assistant := func(requested, model string) string {
		line, err := json.Marshal(map[string]any{"type": "assistant", "requestedModel": requested, "message": map[string]any{"role": "assistant", "model": model}})
		h.Require.NoError(err)
		return string(line)
	}
	transcript := func(name string, lines ...string) string {
		path := filepath.Join(dir, name)
		h.Require.NoError(os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600))
		return path
	}
	run := func(stdin string, args ...string) string {
		h.Stdin.Reset()
		h.Stdin.WriteString(stdin)
		routers := []string{"--router", "acme/auto=Auto (Baseten Internal Dogfooding)", "--router", "acme/code=x=y's", "--router", "acme/bare", "--router", "acme/empty="}
		h.Require.NoError(h.Execute(append(append([]string{"grid", "claude-served-model"}, routers...), args...)...))
		h.Require.Empty(h.Stderr.String())
		return h.Stdout.String()
	}
	payload := func(path string) string {
		raw, err := json.Marshal(map[string]any{"session_id": "s", "transcript_path": path, "model": map[string]any{"id": "acme/auto[1m]"}})
		h.Require.NoError(err)
		return string(raw)
	}
	user := `{"type":"user","message":{"role":"user","content":"hi"}}`
	reply := func(requested, model, text string, sidechain bool) string {
		line, err := json.Marshal(map[string]any{"type": "assistant", "isSidechain": sidechain, "requestedModel": requested,
			"message": map[string]any{"role": "assistant", "model": model, "content": []any{map[string]any{"type": "text", "text": text}}}})
		h.Require.NoError(err)
		return string(line)
	}
	stop := func(path, message, prompt string) string {
		raw, err := json.Marshal(map[string]any{"session_id": "s", "transcript_path": path, "hook_event_name": "Stop", "last_assistant_message": message, "prompt_id": prompt})
		h.Require.NoError(err)
		return string(raw)
	}

	turns := transcript("turns.jsonl", user, reply("acme/auto[1m]", "zai-org/GLM-5.3-Flash", "Thinking.", false),
		reply("acme/auto[1m]", "zai-org/GLM-5.3-Flash", "\n\nFirst answer.\n", false), user, reply("acme/code", "moonshotai/Kimi-K3", "Second answer.", false))
	h.Require.Equal(`{"systemMessage":"Served by zai-org/GLM-5.3-Flash via Auto (Baseten Internal Dogfooding)"}`+"\n", run(stop(turns, "First answer.", ""), "--hook"))
	h.Require.Equal(`{"systemMessage":"Served by moonshotai/Kimi-K3 via x=y's"}`+"\n", run(stop(turns, "", ""), "--hook"))
	h.Require.Equal("x=y's → moonshotai/Kimi-K3\n", run(stop(turns, "First answer.", "")))
	started := time.Now()
	h.Require.Empty(run(stop(turns, "Never said.", ""), "--hook", "--wait", "300ms"))
	h.Require.GreaterOrEqual(time.Since(started), 300*time.Millisecond)
	h.Require.Less(time.Since(started), 2*time.Second)

	lagging := transcript("lagging.jsonl", user, reply("acme/code", "moonshotai/Kimi-K3", "Earlier answer.", false), user)
	time.AfterFunc(200*time.Millisecond, func() {
		f, err := os.OpenFile(lagging, os.O_APPEND|os.O_WRONLY, 0)
		if err == nil {
			_, err = f.WriteString(reply("acme/auto", "zai-org/GLM-5.3-Flash", "Late answer.", false) + "\n")
			err = errors.Join(err, f.Close())
		}
		if err != nil {
			t.Error(err)
		}
	})
	h.Require.Equal(`{"systemMessage":"Served by zai-org/GLM-5.3-Flash via Auto (Baseten Internal Dogfooding)"}`+"\n", run(stop(lagging, "Late answer.", ""), "--hook"))

	prompted := func(id string) string {
		line, err := json.Marshal(map[string]any{"type": "user", "promptId": id, "message": map[string]any{"role": "user", "content": "hi"}})
		h.Require.NoError(err)
		return string(line)
	}
	repeated := transcript("repeated.jsonl", prompted("p1"), reply("acme/auto", "zai-org/GLM-5.3-Flash", "Done.", false), prompted("p2"))
	h.Require.Empty(run(stop(repeated, "Done.", "p2"), "--hook", "--wait", "300ms"))
	h.Require.Empty(run(stop(repeated, "Done.", "p-unknown"), "--hook", "--wait", "100ms"))
	h.Require.Equal(`{"systemMessage":"Served by zai-org/GLM-5.3-Flash via Auto (Baseten Internal Dogfooding)"}`+"\n", run(stop(repeated, "Done.", ""), "--hook"))
	time.AfterFunc(200*time.Millisecond, func() {
		f, err := os.OpenFile(repeated, os.O_APPEND|os.O_WRONLY, 0)
		if err == nil {
			_, err = f.WriteString(reply("acme/code", "moonshotai/Kimi-K3", "Done.", false) + "\n")
			err = errors.Join(err, f.Close())
		}
		if err != nil {
			t.Error(err)
		}
	})
	h.Require.Equal(`{"systemMessage":"Served by moonshotai/Kimi-K3 via x=y's"}`+"\n", run(stop(repeated, "Done.", "p2"), "--hook"))

	filler, err := json.Marshal(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": []any{
		map[string]any{"type": "tool_result", "tool_use_id": "t", "content": strings.Repeat("y", 200<<10)},
	}}})
	h.Require.NoError(err)
	distant := transcript("distant.jsonl", prompted("p1"), reply("acme/code", "moonshotai/Kimi-K3", "Found it.", false),
		prompted("p2"), reply("acme/auto", "zai-org/GLM-5.3-Flash", "Looking.", false), string(filler), reply("acme/auto", "zai-org/GLM-5.3-Flash", "Found it.", false))
	h.Require.Equal(`{"systemMessage":"Served by zai-org/GLM-5.3-Flash via Auto (Baseten Internal Dogfooding)"}`+"\n", run(stop(distant, "Found it.", "p2"), "--hook"))

	sidechain := transcript("sidechain.jsonl", user, reply("acme/auto", "zai-org/GLM-5.3-Flash", "Main answer.", false), reply("acme/code", "moonshotai/Kimi-K3", "Subagent answer.", true))
	h.Require.Equal("Auto (Baseten Internal Dogfooding) → zai-org/GLM-5.3-Flash\n", run(stop(sidechain, "", "")))
	h.Require.Equal(`{"systemMessage":"Served by zai-org/GLM-5.3-Flash via Auto (Baseten Internal Dogfooding)"}`+"\n", run(stop(sidechain, "Main answer.", ""), "--hook"))
	h.Require.Empty(run(stop(sidechain, "Subagent answer.", ""), "--hook", "--wait", "100ms"))

	routed := transcript("routed.jsonl", user, assistant("acme/primary", "acme/primary"), user, assistant("acme/auto[1m]", "zai-org/GLM-5.3-Flash"))
	h.Require.Equal("Auto (Baseten Internal Dogfooding) → zai-org/GLM-5.3-Flash\n", run(payload(routed)))
	h.Require.Equal(`{"systemMessage":"Served by zai-org/GLM-5.3-Flash via Auto (Baseten Internal Dogfooding)"}`+"\n", run(payload(routed), "--hook"))

	direct := transcript("direct.jsonl", assistant("acme/auto", "zai-org/GLM-5.3-Flash"), user, assistant("acme/primary", "acme/primary"))
	h.Require.Empty(run(payload(direct)))
	h.Require.Empty(run(payload(direct), "--hook"))

	synthetic := transcript("synthetic.jsonl", assistant("acme/code", "moonshotai/Kimi-K3"), assistant("acme/primary", "<synthetic>"), assistant("acme/primary", ""))
	h.Require.Equal("x=y's → moonshotai/Kimi-K3\n", run(payload(synthetic)))
	h.Require.Equal(`{"systemMessage":"Served by moonshotai/Kimi-K3 via x=y's"}`+"\n", run(payload(synthetic), "--hook"))

	h.Require.Equal("acme/bare → zai-org/GLM-5.3-Flash\n", run(payload(transcript("bare.jsonl", assistant("acme/bare", "zai-org/GLM-5.3-Flash")))))
	h.Require.Equal("acme/empty → zai-org/GLM-5.3-Flash\n", run(payload(transcript("unlabeled.jsonl", assistant("acme/empty[1m]", "zai-org/GLM-5.3-Flash")))))
	h.Require.Empty(run(payload(transcript("label.jsonl", assistant("Auto (Baseten Internal Dogfooding)", "zai-org/GLM-5.3-Flash")))))
	h.Require.Empty(run(payload(transcript("flag.jsonl", assistant("acme/code=x=y's", "zai-org/GLM-5.3-Flash")))))

	result, err := json.Marshal(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": []any{
		map[string]any{"type": "tool_result", "tool_use_id": "t", "content": strings.Repeat("x", 3<<20)},
	}}})
	h.Require.NoError(err)
	large := transcript("large.jsonl", user, assistant("acme/auto", "zai-org/GLM-5.3-Flash"), string(result))
	h.Require.Equal("Auto (Baseten Internal Dogfooding) → zai-org/GLM-5.3-Flash\n", run(payload(large)))

	for _, off := range []string{"0", "false", "f"} {
		t.Setenv("BASETEN_CLAUDE_SERVED_MODEL", off)
		started := time.Now()
		h.Require.Empty(run(stop(turns, "Never said.", ""), "--hook"))
		h.Require.Less(time.Since(started), 100*time.Millisecond)
		h.Require.Empty(run(stop(turns, "", "")))
	}
	t.Setenv("BASETEN_CLAUDE_SERVED_MODEL", "1")
	h.Require.Equal("x=y's → moonshotai/Kimi-K3\n", run(stop(turns, "", "")))
	t.Setenv("BASETEN_CLAUDE_SERVED_MODEL", "")

	h.Require.Empty(run(payload(filepath.Join(dir, "missing.jsonl"))))
	h.Require.Empty(run(payload(dir)))
	h.Require.Empty(run(payload(transcript("empty.jsonl"))))
	h.Require.Empty(run("not json"))
	h.Require.Empty(run(""))
	h.Require.Empty(run(`{"transcript_path": 7}`))
}

func Test_Grid_ClaudeServedModel_HookProcess(t *testing.T) {
	if os.Getenv("BASETEN_TEST_RUN_CLI") != "1" {
		t.Skip("runs the CLI as the Claude Code Stop hook of Test_Grid_ClaudeServedModel_RealClaudeCode")
	}
	if internalcmd.Execute(context.Background(), internalcmd.ExecuteOptions{Args: flag.Args()}) != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func Test_Grid_ClaudeServedModel_RealClaudeCode(t *testing.T) {
	skipUnlessSupported(t)
	binary, err := exec.LookPath("claude")
	if err != nil {
		t.Skip("claude is not on PATH")
	}
	var mu sync.Mutex
	var requested []string
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model  string `json:"model"`
			Stream bool   `json:"stream"`
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		mu.Lock()
		requested = append(requested, body.Model)
		mu.Unlock()
		texts, model := []string{"Checking.", "Done."}, "acme/served-x"
		if bytes.Contains(raw, []byte("Reply again.")) {
			model = "acme/served-y"
		}
		message := map[string]any{"id": "msg_served", "type": "message", "role": "assistant", "model": model,
			"stop_reason": "end_turn", "stop_sequence": nil, "usage": map[string]any{"input_tokens": 5, "output_tokens": 5}}
		switch {
		case r.URL.Path == "/v1/messages/count_tokens":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"input_tokens": 5})
		case r.URL.Path != "/v1/messages":
			http.NotFound(w, r)
		case !body.Stream:
			var content []any
			for _, text := range texts {
				content = append(content, map[string]any{"type": "text", "text": text})
			}
			message["content"] = content
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(message)
		default:
			w.Header().Set("Content-Type", "text/event-stream")
			event := func(name string, data map[string]any) {
				raw, _ := json.Marshal(data)
				_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, raw)
			}
			message["content"], message["stop_reason"] = []any{}, nil
			event("message_start", map[string]any{"type": "message_start", "message": message})
			for i, text := range texts {
				event("content_block_start", map[string]any{"type": "content_block_start", "index": i, "content_block": map[string]any{"type": "text", "text": ""}})
				event("content_block_delta", map[string]any{"type": "content_block_delta", "index": i, "delta": map[string]any{"type": "text_delta", "text": text}})
				event("content_block_stop", map[string]any{"type": "content_block_stop", "index": i})
			}
			event("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": "end_turn", "stop_sequence": nil}, "usage": map[string]any{"output_tokens": 5}})
			event("message_stop", map[string]any{"type": "message_stop"})
		}
	}))
	t.Cleanup(gateway.Close)
	h, api := gridAPI(t, gateway.URL)
	wide := maps.Clone(harnessRouteMetadata)
	wide["context_window"] = 1000000
	api.SetRoute("GET", "/v1/grid/routes", 200, map[string]any{
		"items": []any{map[string]any{"team_id": "team-a", "team_name": "Engineering", "id": "route-auto", "name": "acme/auto", "display_name": "Auto", "invoke_url": gateway.URL, "metadata": wide,
			"target": map[string]any{"type": "CLASSIFIER", "default_route": map[string]any{"id": "route-auto", "slug": "acme/auto"}, "allowed_routes": []any{}}}},
		"pagination": map[string]any{"has_more": false},
	})
	root := t.TempDir()
	configDir := filepath.Join(root, ".claude")
	h.Require.NoError(h.Execute("grid", "setup", "--harness", "claude", "--config-dir", configDir, "--use-session-credential", "--yes"))

	exe, err := os.Executable()
	h.Require.NoError(err)
	payloadPath, hookOutput, hook := filepath.Join(root, "stop-payloads.jsonl"), filepath.Join(root, "stop-hook.out"), filepath.Join(root, "stop-hook.sh")
	h.Require.NoError(os.WriteFile(hook, []byte("#!/bin/sh\ntee -a '"+payloadPath+"' | BASETEN_TEST_RUN_CLI=1 '"+exe+
		"' -test.run='^Test_Grid_ClaudeServedModel_HookProcess$' -- grid claude-served-model --router 'acme/auto=Auto' --hook | tee -a '"+hookOutput+"'\n"), 0o700))
	settingsPath := harnessSettingsFile("claude", configDir)
	settings := readHarnessSettings(t, "claude", settingsPath)
	settings["hooks"] = map[string]any{"Stop": []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": hook}}}}}
	raw, err := json.Marshal(settings)
	h.Require.NoError(err)
	h.Require.NoError(os.WriteFile(settingsPath, raw, 0o600))

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	work, tmp := t.TempDir(), t.TempDir()
	turn := func(args ...string) {
		run := exec.CommandContext(ctx, binary, args...)
		run.Dir = work
		run.Env = []string{
			"HOME=" + root, "CLAUDE_CONFIG_DIR=" + configDir, "PATH=" + os.Getenv("PATH"), "TMPDIR=" + tmp, "DISABLE_AUTOUPDATER=1",
			"XDG_CONFIG_HOME=" + filepath.Join(root, ".config"), "XDG_DATA_HOME=" + filepath.Join(root, ".local", "share"),
			"XDG_STATE_HOME=" + filepath.Join(root, ".local", "state"), "XDG_CACHE_HOME=" + filepath.Join(root, ".cache"),
		}
		run.WaitDelay = 5 * time.Second
		output, err := run.CombinedOutput()
		h.Require.NoError(err, string(output))
	}
	turn("-p", "Reply with OK.")
	turn("-p", "--continue", "Reply again.")
	settings = readHarnessSettings(t, "claude", settingsPath)
	settings["env"].(map[string]any)["BASETEN_CLAUDE_SERVED_MODEL"] = "0"
	raw, err = json.Marshal(settings)
	h.Require.NoError(err)
	h.Require.NoError(os.WriteFile(settingsPath, raw, 0o600))
	turn("-p", "--continue", "Reply once more.")
	mu.Lock()
	h.Require.Contains(requested, "acme/auto")
	mu.Unlock()

	type stopPayload struct {
		HookEventName        string `json:"hook_event_name"`
		TranscriptPath       string `json:"transcript_path"`
		LastAssistantMessage string `json:"last_assistant_message"`
		PromptID             string `json:"prompt_id"`
	}
	captured, err := os.ReadFile(payloadPath)
	h.Require.NoError(err, "claude -p ran the Stop hook")
	var stops []stopPayload
	for _, line := range strings.Split(strings.TrimSpace(string(captured)), "\n") {
		var stop stopPayload
		h.Require.NoError(json.Unmarshal([]byte(line), &stop))
		h.Require.Equal("Stop", stop.HookEventName)
		h.Require.Equal("Done.", stop.LastAssistantMessage, "last_assistant_message is the final text block")
		h.Require.NotEmpty(stop.PromptID)
		stops = append(stops, stop)
	}
	h.Require.Len(stops, 3)
	h.Require.NotEqual(stops[0].PromptID, stops[1].PromptID)
	h.Require.NotEqual(stops[1].PromptID, stops[2].PromptID)
	h.Require.Equal(stops[0].TranscriptPath, stops[1].TranscriptPath)
	h.Require.Equal(stops[1].TranscriptPath, stops[2].TranscriptPath)
	printed, err := os.ReadFile(hookOutput)
	h.Require.NoError(err)
	h.Require.Equal(`{"systemMessage":"Served by acme/served-x via Auto"}`+"\n"+`{"systemMessage":"Served by acme/served-y via Auto"}`+"\n", string(printed))

	transcripts, err := filepath.Glob(filepath.Join(configDir, "projects", "*", "*.jsonl"))
	h.Require.NoError(err)
	h.Require.Equal([]string{stops[0].TranscriptPath}, transcripts)
	statusLine, err := json.Marshal(map[string]any{"transcript_path": transcripts[0]})
	h.Require.NoError(err)
	h.Stdin.Reset()
	h.Stdin.Write(statusLine)
	h.Require.NoError(h.Execute("grid", "claude-served-model", "--router", "acme/auto=Auto"))
	h.Require.Equal("Auto → acme/served-y\n", h.Stdout.String())
}

func piServedModelFile(dir string) string {
	return filepath.Join(dir, "extensions", "baseten-served-model.ts")
}

func piGitContextFile(dir string) string {
	return filepath.Join(dir, "extensions", "baseten-git-context.ts")
}

func piServedModelRouters(t *testing.T, path string) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	body := raw[:bytes.LastIndexByte(bytes.TrimSuffix(raw, []byte("\n")), '\n')+1]
	require.Equal(t, fmt.Sprintf("export const basetenGridSetupSha256 = \"%x\";\n", sha256.Sum256(body)), string(raw[len(body):]))
	_, rest, ok := strings.Cut(string(body), "new Map<string, string>(Object.entries(")
	require.True(t, ok)
	encoded, _, ok := strings.Cut(rest, "));\n")
	require.True(t, ok)
	var routers map[string]string
	require.NoError(t, json.Unmarshal([]byte(encoded), &routers))
	return routers
}

func Test_Grid_Setup_PiServedModel(t *testing.T) {
	skipUnlessSupported(t)
	h, api := fakeGridAPI(t)
	serveServedModelRoutes(api, "auto=Auto (Baseten Internal Dogfooding)")
	dir := t.TempDir()
	path := harnessSettingsFile("pi", dir)
	settingsPath := filepath.Join(dir, "settings.json")
	extension := piServedModelFile(dir)
	mine := filepath.Join(dir, "extensions", "mine.ts")
	h.Require.NoError(os.MkdirAll(filepath.Dir(extension), 0o700))
	h.Require.NoError(os.WriteFile(mine, []byte("export default function () {}\n"), 0o600))
	userModels := map[string]any{"providers": map[string]any{"local": map[string]any{"api": "openai-completions", "models": []any{map[string]any{"id": "llama"}}}}}
	userSettings := map[string]any{"theme": "dark", "extensions": []any{"~/my-extension.ts"}}
	for file, data := range map[string]map[string]any{path: userModels, settingsPath: userSettings} {
		raw, err := json.Marshal(data)
		h.Require.NoError(err)
		h.Require.NoError(os.WriteFile(file, raw, 0o600))
	}
	setup := []string{"grid", "setup", "--harness", "pi", "--config-dir", dir, "--yes", "--output", "json"}

	h.Require.NoError(h.Execute(setup...))
	var plans public.GridPlanList
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &plans))
	h.Require.Len(plans.Items, 3)
	h.Require.Equal(extension, plans.Items[2].Config)
	h.Require.True(plans.Items[2].Changed)
	h.Require.Empty(plans.Items[2].ReplacedSettings)
	h.Require.Equal(map[string]string{"acme/auto": "Auto (Baseten Internal Dogfooding)"}, piServedModelRouters(t, extension))
	h.Require.NotContains(h.Stderr.String(), "left unchanged")
	settings := readHarnessSettings(t, "pi", settingsPath)
	h.Require.Equal("dark", settings["theme"])
	h.Require.Equal(userSettings["extensions"], settings["extensions"], "setup needs no extensions setting and leaves the user's alone")
	h.Require.Equal(userModels["providers"].(map[string]any)["local"], readHarnessSettings(t, "pi", path)["providers"].(map[string]any)["local"])

	h.Require.NoError(h.Execute(setup...))
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &plans))
	for _, item := range plans.Items {
		h.Require.False(item.Changed, "rerunning setup changes nothing")
	}

	serveServedModelRoutes(api, "auto=Auto (Baseten Internal Dogfooding)", "eq=x=\"y's\" \\ </script>", "bare=")
	h.Require.NoError(h.Execute(setup...))
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &plans))
	h.Require.Equal(extension, plans.Items[2].Config)
	h.Require.True(plans.Items[2].Changed)
	h.Require.Empty(plans.Items[2].ReplacedSettings, "setup refreshes its own extension without reporting a replacement")
	h.Require.Equal(map[string]string{"acme/auto": "Auto (Baseten Internal Dogfooding)", "acme/eq": `x="y's" \ </script>`, "acme/bare": "acme/bare"}, piServedModelRouters(t, extension))

	h.Require.NoError(h.Execute("grid", "teardown", "--harness", "pi", "--config-dir", dir, "--yes"))
	h.Require.NoFileExists(extension)
	h.Require.FileExists(mine)
	h.Require.Equal(userSettings, readHarnessSettings(t, "pi", settingsPath))
	h.Require.Equal(userModels, readHarnessSettings(t, "pi", path))
}

func Test_Grid_Setup_PiServedModelUserFile(t *testing.T) {
	skipUnlessSupported(t)
	for name, edit := range map[string]func(generated []byte) []byte{
		"own": func([]byte) []byte { return []byte("export default function () {}\n") },
		"edited": func(generated []byte) []byte {
			return bytes.Replace(generated, []byte(`{"acme/auto":"auto"}`), []byte(`{"acme/auto":"Mine"}`), 1)
		},
	} {
		t.Run(name, func(t *testing.T) {
			h, api := fakeGridAPI(t)
			serveServedModelRoutes(api, "auto")
			dir := t.TempDir()
			extension := piServedModelFile(dir)
			setup := []string{"grid", "setup", "--harness", "pi", "--config-dir", dir, "--yes"}
			h.Require.NoError(h.Execute(setup...))
			generated, err := os.ReadFile(extension)
			h.Require.NoError(err)
			user := edit(generated)
			h.Require.NotEqual(generated, user)
			h.Require.NoError(os.WriteFile(extension, user, 0o644))
			h.Require.NoError(os.Chmod(extension, 0o644))
			untouched := func(msgAndArgs ...any) {
				raw, err := os.ReadFile(extension)
				h.Require.NoError(err)
				h.Require.Equal(string(user), string(raw), msgAndArgs...)
				info, err := os.Stat(extension)
				h.Require.NoError(err)
				h.Require.Equal(os.FileMode(0o644), info.Mode().Perm(), msgAndArgs...)
			}

			serveServedModelRoutes(api, "auto", "code")
			h.Require.NoError(h.Execute(setup...))
			h.Require.Contains(h.Stderr.String(), "warning: existing "+extension+" left unchanged; Pi will not show which model served auto-route responses")
			untouched("setup")
			h.Require.NoError(h.Execute("grid", "sync", "--yes"))
			untouched("sync")
			h.Require.NoError(h.Execute(append(setup, "--served-model=false")...))
			untouched("opt-out")
			serveServedModelRoutes(api)
			h.Require.NoError(h.Execute(setup...))
			untouched("zero routers")
			h.Require.NoError(h.Execute("grid", "teardown", "--harness", "pi", "--config-dir", dir, "--yes"))
			untouched("teardown")
		})
	}
}

func Test_Grid_Setup_PiServedModelZeroRouters(t *testing.T) {
	skipUnlessSupported(t)
	h, api := fakeGridAPI(t)
	dir := t.TempDir()
	extension := piServedModelFile(dir)
	setup := []string{"grid", "setup", "--harness", "pi", "--config-dir", dir, "--yes", "--output", "json"}

	serveServedModelRoutes(api)
	h.Require.NoError(h.Execute(setup...))
	var plans public.GridPlanList
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &plans))
	h.Require.Len(plans.Items, 2)
	h.Require.NoDirExists(filepath.Dir(extension))

	serveServedModelRoutes(api, "auto")
	h.Require.NoError(h.Execute(setup...))
	h.Require.FileExists(extension)
	mine := filepath.Join(dir, "extensions", "mine.ts")
	h.Require.NoError(os.WriteFile(mine, []byte("export default function () {}\n"), 0o600))

	serveServedModelRoutes(api)
	h.Require.NoError(h.Execute(setup...))
	h.Require.NoFileExists(extension)
	h.Require.FileExists(mine)
	h.Require.Equal("acme/primary", readHarnessSettings(t, "pi", filepath.Join(dir, "settings.json"))["defaultModel"])
}

func Test_Grid_Setup_PiServedModelOptOut(t *testing.T) {
	skipUnlessSupported(t)
	h, api := fakeGridAPI(t)
	serveServedModelRoutes(api, "auto")
	dir := t.TempDir()
	extension := piServedModelFile(dir)
	setup := []string{"grid", "setup", "--harness", "pi", "--config-dir", dir, "--yes"}
	record := func() string {
		raw, err := os.ReadFile(filepath.Join(recordDir(t), "harness.json"))
		h.Require.NoError(err)
		return string(raw)
	}

	h.Require.NoError(h.Execute(setup...))
	generated, err := os.ReadFile(extension)
	h.Require.NoError(err)
	h.Require.NoError(h.Execute(append(setup, "--served-model=false")...))
	h.Require.NoFileExists(extension)
	h.Require.Equal("baseten-harness", readHarnessSettings(t, "pi", filepath.Join(dir, "settings.json"))["defaultProvider"])
	h.Require.True(harnessRecordFile(t).Harnesses["pi"].NoPiServedModel)
	h.Require.Contains(record(), `"no_pi_served_model": true`)

	h.Require.NoError(os.WriteFile(extension, generated, 0o600))
	h.Require.NoError(h.Execute("grid", "sync", "--yes"))
	h.Require.NoFileExists(extension, "sync replays the recorded opt-out")

	h.Require.NoError(h.Execute(setup...))
	h.Require.FileExists(extension, "setup without the flag turns it back on")
	h.Require.False(harnessRecordFile(t).Harnesses["pi"].NoPiServedModel)
	h.Require.NotContains(record(), "no_pi_served_model")
}

func Test_Grid_Setup_PiServedModelPrompt(t *testing.T) {
	skipUnlessSupported(t)
	h, api := fakeGridAPI(t)
	serveServedModelRoutes(api, "auto")
	base := h.Context
	answer := func(answers ...string) { h.Context = internalcmd.WithPromptAnswers(base, answers...) }
	dir := t.TempDir()
	setup := []string{"grid", "setup", "--harness", "pi", "--config-dir", dir}
	question := "Show which model served each response on intelligent routes in Pi?"
	shown := func() bool { return fileExists(piServedModelFile(dir)) }
	recorded := func() bool { return harnessRecordFile(t).Harnesses["pi"].NoPiServedModel }

	answer("1", "1")
	h.Require.NoError(h.Execute(setup...))
	h.Require.Contains(h.Stderr.String(), question)
	h.Require.True(shown())

	answer("2", "1")
	h.Require.NoError(h.Execute(setup...))
	h.Require.Contains(h.Stderr.String(), question)
	h.Require.False(shown(), "answering no removes the extension")
	h.Require.True(recorded())
	h.Context = base
	h.Require.NoError(h.Execute("grid", "sync", "--yes"))
	h.Require.NotContains(h.Stderr.String(), question)
	h.Require.False(shown(), "sync replays the answer without asking")

	answer("", "1")
	h.Require.NoError(h.Execute(setup...))
	h.Require.True(shown(), "the default answer is yes")
	h.Require.False(recorded())

	answer("2")
	h.Require.NoError(h.Execute(append(setup, "--yes", "--served-model=false")...))
	h.Require.NotContains(h.Stderr.String(), question)
	h.Require.False(shown())
	h.Require.True(recorded())
	h.Require.NoError(h.Execute(append(setup, "--yes")...))
	h.Require.NotContains(h.Stderr.String(), question)
	h.Require.True(shown(), "--yes shows it without asking")
	h.Require.False(recorded())
	answer("1")
	h.Require.NoError(h.Execute(append(setup, "--served-model=false")...))
	h.Require.NotContains(h.Stderr.String(), question, "an explicit flag is not asked about")
	h.Require.False(shown())
	h.Require.True(recorded())
	answer("1")
	h.Require.NoError(h.Execute(append(setup, "--served-model")...))
	h.Require.NotContains(h.Stderr.String(), question)
	h.Require.True(shown())
	h.Require.False(recorded())
	h.Context = base
	h.Require.NoError(h.Execute(append(setup, "--yes", "--served-model=false")...))
	h.Require.False(shown(), "without a terminal the flag decides")
	h.Require.NoError(h.Execute(append(setup, "--yes")...))
	h.Require.True(shown())

	answer()
	h.Require.NoError(h.Execute(append(setup, "--dry-run")...))
	h.Require.NotContains(h.Stderr.String(), question)

	serveServedModelRoutes(api)
	answer("1")
	h.Require.NoError(h.Execute(setup...))
	h.Require.NotContains(h.Stderr.String(), question, "no router routes, no question")
	serveServedModelRoutes(api, "auto")
	answer("1")
	h.Require.NoError(h.Execute("grid", "setup", "--harness", "opencode", "--config-dir", t.TempDir()))
	h.Require.NotContains(h.Stderr.String(), question, "Pi is not a target")

	root := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(root, "claude"))
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(root, "pi"))
	answer("2", "1")
	h.Require.NoError(h.Execute("grid", "setup", "--harness", "claude", "--harness", "pi"))
	h.Require.Equal(1, strings.Count(h.Stderr.String(), "Show which model served"), "one question covers both harnesses")
	h.Require.Contains(h.Stderr.String(), "Show which model served each response on intelligent routes in Claude Code and Pi?")
	h.Require.NotContains(readHarnessSettings(t, "claude", filepath.Join(root, "claude", "settings.json")), "statusLine")
	h.Require.NoFileExists(piServedModelFile(filepath.Join(root, "pi")))
	h.Require.True(harnessRecordFile(t).Harnesses["claude"].NoClaudeServedModel)
	h.Require.True(harnessRecordFile(t).Harnesses["pi"].NoPiServedModel)
}

func Test_Grid_PiServedModel_RealPi(t *testing.T) {
	skipUnlessSupported(t)
	binary, err := exec.LookPath("pi")
	if err != nil {
		t.Skip("pi is not on PATH")
	}
	var mu sync.Mutex
	var requested, bodies []string
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		var body struct {
			Model    string `json:"model"`
			Messages []struct {
				Role string `json:"role"`
			} `json:"messages"`
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		mu.Lock()
		requested = append(requested, body.Model)
		bodies = append(bodies, string(raw))
		mu.Unlock()
		final := len(body.Messages) > 0 && body.Messages[len(body.Messages)-1].Role == "tool"
		served := map[bool]map[string]string{
			false: {"acme/auto": "acme/served-step", "acme/primary": "acme/served-z"},
			true:  {"acme/auto": "acme/served-final", "acme/primary": "acme/served-z"},
		}[final][body.Model]
		delta, finish := map[string]any{"role": "assistant", "content": "Reading notes.", "tool_calls": []any{map[string]any{"index": 0, "id": "call_read", "type": "function",
			"function": map[string]any{"name": "read", "arguments": `{"path":"notes.txt"}`}}}}, "tool_calls"
		if final {
			delta, finish = map[string]any{"role": "assistant", "content": "Done reading."}, "stop"
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, chunk := range []map[string]any{
			{"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": nil}}},
			{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": finish}}},
			{"choices": []any{}, "usage": map[string]any{"prompt_tokens": 5, "completion_tokens": 1, "total_tokens": 6}},
		} {
			chunk["id"], chunk["object"], chunk["created"], chunk["model"] = "chatcmpl-served", "chat.completion.chunk", 1, served
			raw, _ := json.Marshal(chunk)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", raw)
		}
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(gateway.Close)
	h, api := gridAPI(t, gateway.URL)
	route := func(id, name, display string, target map[string]any) map[string]any {
		return map[string]any{"team_id": "team-a", "team_name": "Engineering", "id": id, "name": name, "display_name": display, "invoke_url": gateway.URL, "metadata": harnessRouteMetadata, "target": target}
	}
	router := map[string]any{"type": "CLASSIFIER", "default_route": map[string]any{"id": "route-primary", "slug": "acme/primary"}, "allowed_routes": []any{}}
	api.SetRoute("GET", "/v1/grid/routes", 200, map[string]any{
		"items": []any{
			route("route-primary", "acme/primary", "Primary", map[string]any{"type": "BASETEN_MODEL_API", "model": "deepseek"}),
			route("route-auto", "acme/auto", "Auto (Baseten Internal Dogfooding)", router),
			route("route-code", "acme/code", `Code "beta" \ 'x' </script>`, router),
		},
		"pagination": map[string]any{"has_more": false},
	})
	root := t.TempDir()
	configDir := filepath.Join(root, ".pi", "agent")
	h.Require.NoError(h.Execute("grid", "setup", "--harness", "pi", "--config-dir", configDir, "--primary-route-id", "route-auto", "--use-session-credential", "--yes"))
	h.Require.Equal(map[string]any{"defaultProvider": "baseten-harness", "defaultModel": "acme/auto"}, readHarnessSettings(t, "pi", filepath.Join(configDir, "settings.json")))
	h.Require.FileExists(piServedModelFile(configDir))

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	work, tmp := t.TempDir(), t.TempDir()
	h.Require.NoError(os.WriteFile(filepath.Join(work, "notes.txt"), []byte("hello\n"), 0o600))
	environment := func(env []string) []string {
		return append([]string{
			"HOME=" + root, "PI_CODING_AGENT_DIR=" + configDir, "PATH=" + os.Getenv("PATH"), "TMPDIR=" + tmp, "PI_OFFLINE=1", "PI_TELEMETRY=0",
			"XDG_CONFIG_HOME=" + filepath.Join(root, ".config"), "XDG_DATA_HOME=" + filepath.Join(root, ".local", "share"),
			"XDG_STATE_HOME=" + filepath.Join(root, ".local", "state"), "XDG_CACHE_HOME=" + filepath.Join(root, ".cache"),
		}, env...)
	}
	type command struct {
		Type     string `json:"type"`
		Message  string `json:"message,omitempty"`
		Provider string `json:"provider,omitempty"`
		ModelID  string `json:"modelId,omitempty"`
	}
	state := command{Type: "get_state"}
	prompt := func(message string) command { return command{Type: "prompt", Message: message} }
	use := func(model string) command {
		return command{Type: "set_model", Provider: "baseten-harness", ModelID: model}
	}
	session := func(env []string, args []string, commands ...command) {
		run := exec.CommandContext(ctx, binary, append([]string{"--mode", "rpc"}, args...)...)
		run.Dir = work
		run.Env = environment(env)
		run.WaitDelay = 5 * time.Second
		var stderr strings.Builder
		run.Stderr = &stderr
		stdin, err := run.StdinPipe()
		h.Require.NoError(err)
		stdout, err := run.StdoutPipe()
		h.Require.NoError(err)
		h.Require.NoError(run.Start())
		lines := bufio.NewReader(stdout)
		for _, c := range commands {
			raw, err := json.Marshal(c)
			h.Require.NoError(err)
			_, err = stdin.Write(append(raw, '\n'))
			h.Require.NoError(err)
			for done := false; !done; {
				line, err := lines.ReadBytes('\n')
				h.Require.NoError(err, "pi stopped before finishing %s: %s", c.Type, stderr.String())
				var event struct {
					Type    string `json:"type"`
					Command string `json:"command"`
					Success bool   `json:"success"`
					Error   string `json:"error"`
				}
				if json.Unmarshal(line, &event) != nil {
					continue
				}
				h.Require.NotEqual("extension_error", event.Type, string(line))
				h.Require.False(event.Type == "response" && !event.Success, event.Error)
				done = (c.Type == "prompt" && event.Type == "agent_settled") || (c.Type != "prompt" && event.Type == "response" && event.Command == c.Type)
			}
		}
		h.Require.NoError(stdin.Close())
		h.Require.NoError(run.Wait(), stderr.String())
	}
	transcript := func() []string {
		files, err := filepath.Glob(filepath.Join(configDir, "sessions", "*", "*.jsonl"))
		h.Require.NoError(err)
		h.Require.Len(files, 1)
		raw, err := os.ReadFile(files[0])
		h.Require.NoError(err)
		var entries []string
		for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
			var entry struct {
				Type       string `json:"type"`
				CustomType string `json:"customType"`
				Data       struct {
					Label string `json:"label"`
				} `json:"data"`
				Message struct {
					Role          string `json:"role"`
					ResponseModel string `json:"responseModel"`
				} `json:"message"`
			}
			h.Require.NoError(json.Unmarshal([]byte(line), &entry))
			switch {
			case entry.Type == "custom":
				entries = append(entries, entry.CustomType+": "+entry.Data.Label)
			case entry.Type == "message" && entry.Message.Role != "system":
				entries = append(entries, strings.TrimSpace(entry.Message.Role+" "+entry.Message.ResponseModel))
			}
		}
		return entries
	}
	render := func(last string, env ...string) string {
		t.Helper()
		run := exec.CommandContext(ctx, binary, "--continue")
		run.Dir = work
		run.Env = environment(append([]string{"TERM=xterm-256color"}, env...))
		terminal, err := pty.StartWithSize(run, &pty.Winsize{Rows: 60, Cols: 200})
		h.Require.NoError(err)
		var screenMu sync.Mutex
		var screen strings.Builder
		go func() {
			buf := make([]byte, 32<<10)
			for {
				n, err := terminal.Read(buf)
				screenMu.Lock()
				screen.Write(buf[:n])
				screenMu.Unlock()
				if err != nil {
					return
				}
			}
		}()
		shown := func() string {
			screenMu.Lock()
			defer screenMu.Unlock()
			return terminalEscape.ReplaceAllString(screen.String(), "")
		}
		for ctx.Err() == nil && !strings.Contains(shown(), last) {
			time.Sleep(100 * time.Millisecond)
		}
		time.Sleep(time.Second)
		_ = run.Process.Kill()
		_ = run.Wait()
		_ = terminal.Close()
		h.Require.Contains(shown(), last, "pi rendered the resumed session")
		return shown()
	}
	label := "Auto (Baseten Internal Dogfooding) → acme/served-final"
	served := "baseten-served-model: " + label
	routed := func(entries ...string) []string {
		return append([]string{"user", "assistant acme/served-step", "toolResult", "assistant acme/served-final"}, entries...)
	}
	primary := []string{"user", "assistant acme/served-z", "toolResult", "assistant acme/served-z"}

	session(nil, nil, state, prompt("Summarize notes.txt."), use("acme/primary"), prompt("Summarize notes.txt again."), use("acme/auto"), prompt("Summarize notes.txt once more."))
	want := slices.Concat(routed(served), primary, routed(served))
	h.Require.Equal(want, transcript(), "one entry per router prompt, after its final response, naming the model that served that response")
	h.Require.Contains(render("Summarize notes.txt once more."), label, "a resumed session renders the entries")

	for _, off := range []string{"0", "false"} {
		session([]string{"BASETEN_PI_SERVED_MODEL=" + off}, []string{"--continue"}, prompt("Summarize notes.txt with the display "+off+"."))
		want = append(want, routed()...)
		h.Require.Equal(want, transcript(), "BASETEN_PI_SERVED_MODEL=%s adds no entry", off)
		h.Require.NotContains(render("Summarize notes.txt with the display "+off+".", "BASETEN_PI_SERVED_MODEL="+off), label, "BASETEN_PI_SERVED_MODEL=%s renders no entry", off)
	}
	session([]string{"BASETEN_PI_SERVED_MODEL=1"}, []string{"--continue"}, prompt("Summarize notes.txt with the display on."))
	want = append(want, routed(served)...)
	h.Require.Equal(want, transcript())

	h.Require.NoError(h.Execute("grid", "setup", "--harness", "pi", "--config-dir", configDir, "--primary-route-id", "route-auto", "--use-session-credential", "--yes", "--served-model=false"))
	h.Require.NoFileExists(piServedModelFile(configDir))
	session(nil, []string{"--continue"}, prompt("Summarize notes.txt without the extension."))
	want = append(want, routed()...)
	h.Require.Equal(want, transcript())
	orphaned := render("Summarize notes.txt without the extension.")
	h.Require.NotContains(orphaned, label, "entries left by a removed extension are not shown")
	h.Require.NotContains(orphaned, "baseten-served-model")

	mu.Lock()
	defer mu.Unlock()
	h.Require.Equal(slices.Concat(slices.Repeat([]string{"acme/auto"}, 2), slices.Repeat([]string{"acme/primary"}, 2), slices.Repeat([]string{"acme/auto"}, 10)), requested)
	h.Require.Contains(bodies[len(bodies)-1], "Done reading.", "later requests carry the earlier responses")
	for _, body := range bodies {
		for _, leak := range []string{"baseten-served-model", "Auto (Baseten Internal Dogfooding)", "→", `\u2192`, "acme/served"} {
			h.Require.NotContains(body, leak, "entries never reach the model")
		}
	}
}

var openCode2Execer = versionedHarnessExecer{"claude": "2.1.272 (Claude Code)", "opencode": "opencode v2.2.5"}

func openCodePluginDir(dir string) string {
	return filepath.Join(dir, "plugins", "baseten-served-model")
}

func openCodePluginFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(openCodePluginDir(dir))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	require.NoError(t, err)
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

func openCodePluginRoutes(t *testing.T, dir string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(openCodePluginDir(dir), "routes.json"))
	require.NoError(t, err)
	var routes map[string]any
	require.NoError(t, json.Unmarshal(raw, &routes))
	return routes
}

func openCodePluginRouters(routers map[string]any) map[string]any {
	return map[string]any{"managedBy": "baseten grid setup", "provider": "baseten-harness", "routers": routers}
}

var openCodePluginSources = []string{"routes.json", "server.js", "shared.js", "tui.tsx"}

func Test_Grid_Setup_OpenCodeServedModel(t *testing.T) {
	skipUnlessSupported(t)
	for name, seed := range map[string]string{"original": `{"theme":"dark"}`, "native": `{"providers":{"other":{"name":"Other"}},"theme":"dark"}`} {
		t.Run(name, func(t *testing.T) {
			h, api := fakeGridAPI(t)
			h.Context = internalcmd.WithExecer(h.Context, openCode2Execer)
			serveServedModelRoutes(api, "auto=Auto (Baseten Internal Dogfooding)")
			dir := t.TempDir()
			path := harnessSettingsFile("opencode", dir)
			h.Require.NoError(os.WriteFile(path, []byte(seed), 0o600))
			mine := filepath.Join(dir, "plugins", "mine", "server.js")
			h.Require.NoError(os.MkdirAll(filepath.Dir(mine), 0o700))
			h.Require.NoError(os.WriteFile(mine, []byte("export default {}\n"), 0o600))
			setup := []string{"grid", "setup", "--harness", "opencode", "--config-dir", dir, "--yes", "--output", "json"}

			h.Require.NoError(h.Execute(setup...))
			h.Require.Equal(openCodePluginSources, openCodePluginFiles(t, dir))
			h.Require.Equal(openCodePluginRouters(map[string]any{"acme/auto": "Auto (Baseten Internal Dogfooding)"}), openCodePluginRoutes(t, dir))
			settings := readHarnessSettings(t, "opencode", path)
			h.Require.NotContains(settings, "plugins")
			h.Require.NotContains(settings, "plugin")
			h.Require.Equal("dark", settings["theme"])
			_, native := settings["providers"]
			h.Require.Equal(name == "native", native)
			h.Require.NotContains(h.Stderr.String(), "already exists")

			h.Require.NoError(h.Execute(setup...))
			var rerun public.GridPlanList
			h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &rerun))
			h.Require.Len(rerun.Items, 2)
			h.Require.Equal(openCodePluginDir(dir), rerun.Items[1].Config)
			h.Require.Equal(openCodePluginSources, rerun.Items[1].Settings)
			for _, item := range rerun.Items {
				h.Require.False(item.Changed, "rerunning setup changes nothing")
			}

			server := filepath.Join(openCodePluginDir(dir), "server.js")
			written, err := os.ReadFile(server)
			h.Require.NoError(err)
			h.Require.NoError(os.WriteFile(server, []byte("stale"), 0o600))
			serveServedModelRoutes(api, "auto=Auto (Baseten Internal Dogfooding)", "code")
			h.Require.NoError(h.Execute(setup...))
			h.Require.Equal(openCodePluginRouters(map[string]any{"acme/auto": "Auto (Baseten Internal Dogfooding)", "acme/code": "code"}), openCodePluginRoutes(t, dir))
			refreshed, err := os.ReadFile(server)
			h.Require.NoError(err)
			h.Require.Equal(written, refreshed, "setup refreshes the plugin source")
			h.Require.NotContains(readHarnessSettings(t, "opencode", path), "plugins")

			h.Require.NoError(h.Execute("grid", "teardown", "--harness", "opencode", "--config-dir", dir, "--yes"))
			h.Require.NoDirExists(openCodePluginDir(dir))
			h.Require.FileExists(mine)
			var original map[string]any
			h.Require.NoError(json.Unmarshal([]byte(seed), &original))
			h.Require.Equal(original, readHarnessSettings(t, "opencode", path))
		})
	}
}

func Test_Grid_Setup_OpenCodeServedModelForeignPlugin(t *testing.T) {
	skipUnlessSupported(t)
	for name, files := range map[string]map[string]string{
		"unmarked":    {"server.js": "export default {}\n"},
		"other owner": {"routes.json": `{"managedBy":"someone else","routers":{}}`, "server.js": "export default {}\n"},
		"file":        {"": "not a directory\n"},
	} {
		t.Run(name, func(t *testing.T) {
			h, api := fakeGridAPI(t)
			h.Context = internalcmd.WithExecer(h.Context, openCode2Execer)
			serveServedModelRoutes(api, "auto")
			dir := t.TempDir()
			h.Require.NoError(os.MkdirAll(filepath.Join(dir, "plugins"), 0o700))
			if _, ok := files[""]; !ok {
				h.Require.NoError(os.Mkdir(openCodePluginDir(dir), 0o700))
			}
			for file, content := range files {
				h.Require.NoError(os.WriteFile(filepath.Join(openCodePluginDir(dir), file), []byte(content), 0o600))
			}
			unchanged := func() {
				t.Helper()
				for file, content := range files {
					raw, err := os.ReadFile(filepath.Join(openCodePluginDir(dir), file))
					h.Require.NoError(err)
					h.Require.Equal(content, string(raw))
				}
				if _, ok := files[""]; !ok {
					h.Require.Len(openCodePluginFiles(t, dir), len(files))
				}
			}
			setup := []string{"grid", "setup", "--harness", "opencode", "--config-dir", dir, "--yes"}

			h.Require.NoError(h.Execute(setup...))
			h.Require.Contains(h.Stderr.String(), "warning: "+openCodePluginDir(dir)+" already exists and was not written by baseten grid setup; leaving it unchanged")
			h.Require.Equal("baseten-harness/acme/primary", readHarnessSettings(t, "opencode", harnessSettingsFile("opencode", dir))["model"])
			unchanged()
			h.Require.NoError(h.Execute(append(setup, "--served-model=false")...))
			h.Require.NotContains(h.Stderr.String(), "already exists")
			unchanged()
			h.Require.NoError(h.Execute("grid", "teardown", "--harness", "opencode", "--config-dir", dir, "--yes"))
			unchanged()
		})
	}
}

func Test_Grid_Setup_OpenCodeServedModelZeroRoutersAndOpenCode1(t *testing.T) {
	skipUnlessSupported(t)
	h, api := fakeGridAPI(t)
	base := h.Context
	h.Context = internalcmd.WithExecer(base, openCode2Execer)
	dir := t.TempDir()
	setup := []string{"grid", "setup", "--harness", "opencode", "--config-dir", dir, "--yes"}

	serveServedModelRoutes(api)
	h.Require.NoError(h.Execute(setup...))
	h.Require.Empty(openCodePluginFiles(t, dir))
	serveServedModelRoutes(api, "auto")
	h.Require.NoError(h.Execute(setup...))
	h.Require.Equal(openCodePluginSources, openCodePluginFiles(t, dir))
	serveServedModelRoutes(api)
	h.Require.NoError(h.Execute(setup...))
	h.Require.NoDirExists(openCodePluginDir(dir), "no router routes removes the plugin")

	serveServedModelRoutes(api, "auto")
	h.Require.NoError(h.Execute(setup...))
	h.Require.Equal(openCodePluginSources, openCodePluginFiles(t, dir))
	h.Context = internalcmd.WithExecer(base, versionedHarnessExecer{"opencode": "1.19.5"})
	h.Require.NoError(h.Execute(setup...))
	h.Require.NoDirExists(openCodePluginDir(dir), "OpenCode 1 removes the plugin")
	h.Require.NoError(h.Execute(setup...))
	h.Require.NoDirExists(openCodePluginDir(dir), "OpenCode 1 never writes the plugin")

	desktop := "/Applications/OpenCode.app/Contents/Resources/opencode-cli"
	if runtime.GOOS == "linux" {
		desktop = "/opt/OpenCode/resources/opencode-cli"
	}
	h.Context = internalcmd.WithExecer(base, versionedHarnessExecer{"opencode": "1.19.5", desktop: "opencode v2.0.27"})
	h.Require.NoError(h.Execute(setup...))
	h.Require.Equal(openCodePluginSources, openCodePluginFiles(t, dir), "an OpenCode 2 install sharing the settings gets the plugin")
}

func Test_Grid_Setup_OpenCodeServedModelOptOut(t *testing.T) {
	skipUnlessSupported(t)
	h, api := fakeGridAPI(t)
	h.Context = internalcmd.WithExecer(h.Context, openCode2Execer)
	serveServedModelRoutes(api, "auto")
	dir := t.TempDir()
	path := harnessSettingsFile("opencode", dir)
	h.Require.NoError(os.WriteFile(path, []byte(`{"theme":"dark"}`), 0o600))
	setup := []string{"grid", "setup", "--harness", "opencode", "--config-dir", dir, "--yes"}
	record := func() string {
		raw, err := os.ReadFile(filepath.Join(recordDir(t), "harness.json"))
		h.Require.NoError(err)
		return string(raw)
	}

	h.Require.NoError(h.Execute(setup...))
	h.Require.Equal(openCodePluginSources, openCodePluginFiles(t, dir))
	plugin := map[string][]byte{}
	for _, file := range openCodePluginSources {
		raw, err := os.ReadFile(filepath.Join(openCodePluginDir(dir), file))
		h.Require.NoError(err)
		plugin[file] = raw
	}
	h.Require.NoError(h.Execute(append(setup, "--served-model=false")...))
	h.Require.NoDirExists(openCodePluginDir(dir))
	h.Require.Equal("dark", readHarnessSettings(t, "opencode", path)["theme"])
	h.Require.True(harnessRecordFile(t).Harnesses["opencode"].NoOpenCodeServedModel)
	h.Require.Contains(record(), `"no_opencode_served_model": true`)

	h.Require.NoError(os.MkdirAll(openCodePluginDir(dir), 0o700))
	for file, raw := range plugin {
		h.Require.NoError(os.WriteFile(filepath.Join(openCodePluginDir(dir), file), raw, 0o600))
	}
	h.Require.NoError(h.Execute("grid", "sync", "--yes"))
	h.Require.NoDirExists(openCodePluginDir(dir), "sync replays the recorded opt-out")

	h.Require.NoError(h.Execute(setup...))
	h.Require.Equal(openCodePluginSources, openCodePluginFiles(t, dir), "setup without the flag turns it back on")
	h.Require.False(harnessRecordFile(t).Harnesses["opencode"].NoOpenCodeServedModel)
	h.Require.NotContains(record(), "no_opencode_served_model")
	h.Require.NoError(os.RemoveAll(openCodePluginDir(dir)))
	h.Require.NoError(h.Execute("grid", "sync", "--yes"))
	h.Require.Equal(openCodePluginSources, openCodePluginFiles(t, dir), "sync restores the plugin")
}

func Test_Grid_Setup_OpenCodeServedModelPrompt(t *testing.T) {
	skipUnlessSupported(t)
	h, api := fakeGridAPI(t)
	h.Context = internalcmd.WithExecer(h.Context, openCode2Execer)
	serveServedModelRoutes(api, "auto")
	base := h.Context
	answer := func(answers ...string) { h.Context = internalcmd.WithPromptAnswers(base, answers...) }
	dir := t.TempDir()
	setup := []string{"grid", "setup", "--harness", "opencode", "--config-dir", dir}
	question := "Show which model served each response on intelligent routes in OpenCode?"
	shown := func() bool { return fileExists(filepath.Join(openCodePluginDir(dir), "routes.json")) }
	recorded := func() bool { return harnessRecordFile(t).Harnesses["opencode"].NoOpenCodeServedModel }

	answer("1", "1")
	h.Require.NoError(h.Execute(setup...))
	h.Require.Contains(h.Stderr.String(), question)
	h.Require.True(shown())

	answer("2", "1")
	h.Require.NoError(h.Execute(setup...))
	h.Require.Contains(h.Stderr.String(), question)
	h.Require.False(shown(), "answering no removes the plugin")
	h.Require.True(recorded())
	h.Context = base
	h.Require.NoError(h.Execute("grid", "sync", "--yes"))
	h.Require.NotContains(h.Stderr.String(), question)
	h.Require.False(shown(), "sync replays the answer without asking")

	answer("", "1")
	h.Require.NoError(h.Execute(setup...))
	h.Require.True(shown(), "the default answer is yes")
	h.Require.False(recorded())

	answer("2")
	h.Require.NoError(h.Execute(append(setup, "--yes", "--served-model=false")...))
	h.Require.NotContains(h.Stderr.String(), question)
	h.Require.False(shown())
	h.Require.True(recorded())
	h.Require.NoError(h.Execute(append(setup, "--yes")...))
	h.Require.NotContains(h.Stderr.String(), question)
	h.Require.True(shown(), "--yes shows it without asking")
	h.Require.False(recorded())
	answer("1")
	h.Require.NoError(h.Execute(append(setup, "--served-model=false")...))
	h.Require.NotContains(h.Stderr.String(), question, "an explicit flag is not asked about")
	h.Require.False(shown())
	h.Require.True(recorded())
	answer("1")
	h.Require.NoError(h.Execute(append(setup, "--served-model")...))
	h.Require.NotContains(h.Stderr.String(), question)
	h.Require.True(shown())
	h.Require.False(recorded())
	h.Context = base
	h.Require.NoError(h.Execute(append(setup, "--yes", "--served-model=false")...))
	h.Require.False(shown(), "without a terminal the flag decides")
	h.Require.NoError(h.Execute(append(setup, "--yes")...))
	h.Require.True(shown())

	answer()
	h.Require.NoError(h.Execute(append(setup, "--dry-run")...))
	h.Require.NotContains(h.Stderr.String(), question)

	serveServedModelRoutes(api)
	answer("1")
	h.Require.NoError(h.Execute(setup...))
	h.Require.NotContains(h.Stderr.String(), question, "no router routes, no question")
	serveServedModelRoutes(api, "auto")
	h.Context = internalcmd.WithPromptAnswers(internalcmd.WithExecer(base, versionedHarnessExecer{"opencode": "1.19.5"}), "1")
	h.Require.NoError(h.Execute(setup...))
	h.Require.NotContains(h.Stderr.String(), question, "OpenCode 1 is not asked")

	root := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(root, "claude"))
	t.Setenv("XDG_CONFIG_HOME", root)
	answer("1", "1")
	h.Require.NoError(h.Execute("grid", "setup", "--harness", "claude", "--harness", "opencode"))
	h.Require.Equal(1, strings.Count(h.Stderr.String(), "Show which model served"), "one question covers both harnesses")
	h.Require.Contains(h.Stderr.String(), "Show which model served each response on intelligent routes in Claude Code and OpenCode?")
	h.Require.Contains(readHarnessSettings(t, "claude", filepath.Join(root, "claude", "settings.json")), "statusLine")
	h.Require.Equal(openCodePluginSources, openCodePluginFiles(t, filepath.Join(root, "opencode")))
	h.Require.False(harnessRecordFile(t).Harnesses["claude"].NoClaudeServedModel)
	h.Require.False(harnessRecordFile(t).Harnesses["opencode"].NoOpenCodeServedModel)

	h.Context = internalcmd.WithPromptAnswers(internalcmd.WithExecer(base, versionedHarnessExecer{"claude": "2.1.272 (Claude Code)", "opencode": "1.19.5"}), "2", "1")
	h.Require.NoError(h.Execute("grid", "setup", "--harness", "claude", "--harness", "opencode"))
	h.Require.Contains(h.Stderr.String(), "Show which model served each response on intelligent routes in Claude Code?")
	h.Require.True(harnessRecordFile(t).Harnesses["claude"].NoClaudeServedModel)
	h.Require.False(harnessRecordFile(t).Harnesses["opencode"].NoOpenCodeServedModel, "the answer does not opt out OpenCode 1, which was not asked about")
}

func Test_Grid_Setup_ServedModelSelectedHarnesses(t *testing.T) {
	skipUnlessSupported(t)
	h, api := fakeGridAPI(t)
	base := internalcmd.WithExecer(h.Context, versionedHarnessExecer{"claude": "2.1.272 (Claude Code)", "opencode": "opencode v2.2.5", "pi": "1.2.3"})
	answer := func(answers ...string) { h.Context = internalcmd.WithPromptAnswers(base, answers...) }
	serveServedModelRoutes(api, "auto")
	root := gridSyncHarnessRoot(t)
	setup := []string{"grid", "setup", "--harness", "claude", "--harness", "opencode", "--harness", "pi"}
	shown := func() []bool {
		_, claude := readHarnessSettings(t, "claude", filepath.Join(root, "claude", "settings.json"))["statusLine"]
		return []bool{claude, fileExists(piServedModelFile(filepath.Join(root, "pi"))), fileExists(openCodePluginDir(filepath.Join(root, "opencode")))}
	}
	recorded := func() []bool {
		records := harnessRecordFile(t).Harnesses
		return []bool{records["claude"].NoClaudeServedModel, records["pi"].NoPiServedModel, records["opencode"].NoOpenCodeServedModel}
	}
	asked := func() int { return strings.Count(h.Stderr.String(), "Show which model served") }

	answer("2", "1")
	h.Require.NoError(h.Execute(setup...))
	h.Require.Equal(1, asked(), "one question covers every selected harness")
	h.Require.Contains(h.Stderr.String(), "Show which model served each response on intelligent routes in Claude Code, Pi and OpenCode?")
	h.Require.Equal([]bool{false, false, false}, shown())
	h.Require.Equal([]bool{true, true, true}, recorded())

	answer()
	h.Require.NoError(h.Execute(append(setup, "--yes")...))
	h.Require.Zero(asked())
	h.Require.Equal([]bool{true, true, true}, shown(), "--yes shows it without asking")
	h.Require.Equal([]bool{false, false, false}, recorded())

	answer("1")
	h.Require.NoError(h.Execute(append(setup, "--served-model=false")...))
	h.Require.Zero(asked(), "an explicit flag is not asked about")
	h.Require.Equal([]bool{false, false, false}, shown())
	h.Require.Equal([]bool{true, true, true}, recorded())

	answer()
	h.Require.NoError(h.Execute(append(setup, "--served-model", "--yes")...))
	h.Require.Equal([]bool{true, true, true}, shown())
	before := harnessRecordFile(t).Harnesses
	h.Require.NoError(h.Execute("grid", "setup", "--harness", "pi", "--served-model=false", "--yes"))
	h.Require.Equal([]bool{true, false, true}, shown())
	h.Require.Equal([]bool{false, true, false}, recorded())
	after := harnessRecordFile(t).Harnesses
	h.Require.Equal(before["claude"], after["claude"], "harnesses not selected keep their records")
	h.Require.Equal(before["opencode"], after["opencode"])
}

var terminalEscape = regexp.MustCompile(`\x1b\[[0-9;?<>=]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b[PX^_][^\x1b]*\x1b\\|\x1b[@-Z\\-_]`)

func Test_Grid_OpenCodeServedModel_RealOpenCode2(t *testing.T) {
	skipUnlessSupported(t)
	binary := os.Getenv("BASETEN_TEST_OPENCODE2")
	if binary == "" {
		t.Skip("BASETEN_TEST_OPENCODE2 does not name an OpenCode 2 binary")
	}
	if _, err := exec.LookPath(binary); err != nil {
		t.Skipf("%s is not installed", binary)
	}
	var mu sync.Mutex
	var requested []string
	var answered time.Time
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model    string `json:"model"`
			Tools    []any  `json:"tools"`
			Messages []struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		last := body.Messages[len(body.Messages)-1]
		step := len(body.Tools) > 0 && last.Role == "user" && bytes.Contains(last.Content, []byte("notes.txt"))
		delta, finish := map[string]any{"content": "Served reply."}, "stop"
		if step {
			delta, finish = map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "call_read", "type": "function",
				"function": map[string]any{"name": "read", "arguments": `{"path":"notes.txt"}`}}}}, "tool_calls"
		}
		served := "qwen/Qwen3.6-Coder"
		switch {
		case step:
			served = "zai-org/GLM-5.3-Flash"
		case bytes.Contains(raw, []byte("this session again")):
			served = "deepseek-ai/DeepSeek-V4.2"
		case bytes.Contains(raw, []byte("another session")):
			served = "moonshotai/Kimi-K2.6"
		case bytes.Contains(raw, []byte("Start quietly")):
			served = ""
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, choice := range []map[string]any{
			{"index": 0, "delta": map[string]any{"role": "assistant", "content": ""}, "finish_reason": nil},
			{"index": 0, "delta": delta, "finish_reason": nil},
			{"index": 0, "delta": map[string]any{}, "finish_reason": finish},
		} {
			chunk, _ := json.Marshal(map[string]any{"id": "chatcmpl-served", "object": "chat.completion.chunk", "created": 1, "model": served,
				"choices": []any{choice}, "usage": map[string]any{"prompt_tokens": 5, "completion_tokens": 2, "total_tokens": 7}})
			_, _ = fmt.Fprintf(w, "data: %s\n\n", chunk)
		}
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
		mu.Lock()
		defer mu.Unlock()
		requested = append(requested, body.Model)
		if len(body.Tools) > 0 {
			answered = time.Now()
		}
	}))
	t.Cleanup(gateway.Close)
	h, api := gridAPI(t, gateway.URL)
	t.Setenv("PATH", filepath.Dir(binary)+string(os.PathListSeparator)+os.Getenv("PATH"))
	route := func(id, name, display string, target map[string]any) map[string]any {
		return map[string]any{"team_id": "team-a", "team_name": "Engineering", "id": id, "name": name, "display_name": display, "invoke_url": gateway.URL, "metadata": harnessRouteMetadata, "target": target}
	}
	api.SetRoute("GET", "/v1/grid/routes", 200, map[string]any{
		"items": []any{
			route("route-auto", "acme/auto", "Auto (Baseten Internal Dogfooding)", map[string]any{"type": "CLASSIFIER", "default_route": map[string]any{"id": "route-primary", "slug": "acme/primary"}, "allowed_routes": []any{}}),
			route("route-primary", "acme/primary", "Primary", map[string]any{"type": "BASETEN_MODEL_API", "model": "deepseek"}),
		},
		"pagination": map[string]any{"has_more": false},
	})
	root := t.TempDir()
	configDir := filepath.Join(root, ".config", "opencode")
	setup := func(primary string) {
		h.Require.NoError(h.Execute("grid", "setup", "--harness", "opencode", "--config-dir", configDir, "--use-session-credential", "--primary-route-id", primary, "--yes"))
		h.Require.Equal(openCodePluginSources, openCodePluginFiles(t, configDir), "setup detects OpenCode 2 and writes the plugin")
	}
	work, err := os.MkdirTemp("/tmp", "oc")
	h.Require.NoError(err)
	t.Cleanup(func() { _ = os.RemoveAll(work) })
	h.Require.NoError(os.WriteFile(filepath.Join(work, "notes.txt"), []byte("hello\n"), 0o600))
	tmp := t.TempDir()
	toast := []string{"Served model", "Auto (Baseten Internal Dogfooding)", "→ qwen/Qwen3.6-Coder"}
	toasted := func(screen string) bool {
		return !slices.ContainsFunc(toast, func(part string) bool { return !strings.Contains(screen, part) })
	}
	environment := func(env ...string) []string {
		return append([]string{
			"HOME=" + root, "PATH=" + os.Getenv("PATH"), "TERM=xterm-256color", "TMPDIR=" + tmp,
			"XDG_CONFIG_HOME=" + filepath.Join(root, ".config"), "XDG_DATA_HOME=" + filepath.Join(root, ".local", "share"),
			"XDG_STATE_HOME=" + filepath.Join(root, ".local", "state"), "XDG_CACHE_HOME=" + filepath.Join(root, ".cache"),
		}, env...)
	}
	launch := func(ctx context.Context, args []string, env ...string) (*os.File, func() string, func()) {
		ctx, cancel := context.WithCancel(ctx)
		run := exec.CommandContext(ctx, binary, args...)
		run.Cancel = func() error { return run.Process.Signal(syscall.SIGTERM) }
		run.WaitDelay = 10 * time.Second
		run.Dir = work
		run.Env = environment(env...)
		terminal, err := pty.StartWithSize(run, &pty.Winsize{Rows: 50, Cols: 260})
		h.Require.NoError(err)
		var screenMu sync.Mutex
		var screen strings.Builder
		go func() {
			buf := make([]byte, 32<<10)
			for {
				n, err := terminal.Read(buf)
				screenMu.Lock()
				screen.Write(buf[:n])
				screenMu.Unlock()
				if err != nil {
					return
				}
			}
		}()
		shown := func() string {
			screenMu.Lock()
			defer screenMu.Unlock()
			return terminalEscape.ReplaceAllString(screen.String(), "")
		}
		return terminal, shown, sync.OnceFunc(func() {
			cancel()
			_ = run.Wait()
			_ = terminal.Close()
		})
	}
	tui := func(env ...string) (string, []string) {
		t.Helper()
		started := time.Now()
		mu.Lock()
		before := len(requested)
		mu.Unlock()
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
		defer cancel()
		_, shown, stop := launch(ctx, []string{"--standalone", "--prompt", "Read notes.txt and reply."}, env...)
		settled := func() bool {
			mu.Lock()
			defer mu.Unlock()
			return answered.After(started) && time.Since(answered) > 3*time.Second
		}
		for ctx.Err() == nil && !toasted(shown()) && !settled() {
			time.Sleep(100 * time.Millisecond)
		}
		stop()
		mu.Lock()
		defer mu.Unlock()
		h.Require.True(answered.After(started), "OpenCode answered the prompt; requested %v\n%s", requested, shown())
		return shown(), slices.Clone(requested[before:])
	}

	setup("route-auto")
	screen, models := tui()
	h.Require.NotContains(screen, "zai-org/GLM-5.3-Flash", "the intermediate tool step does not toast")
	for _, part := range toast {
		h.Require.Contains(screen, part, "the TUI toasts the router route and the model that served the final response")
	}
	h.Require.Equal([]string{"acme/auto", "acme/auto"}, slices.DeleteFunc(models, func(model string) bool { return model != "acme/auto" }))

	screen, models = tui("BASETEN_OPENCODE_SERVED_MODEL=0")
	h.Require.NotContains(screen, "Served model", "BASETEN_OPENCODE_SERVED_MODEL=0 turns the display off")
	h.Require.NotContains(screen, "qwen/Qwen3.6-Coder", "BASETEN_OPENCODE_SERVED_MODEL=0 turns the display off")
	h.Require.Contains(models, "acme/auto")

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	h.Require.NoError(err)
	port := listener.Addr().(*net.TCPAddr).Port
	h.Require.NoError(listener.Close())
	h.Require.NoError(os.WriteFile(filepath.Join(configDir, "service.json"), fmt.Appendf(nil, `{"port":%d}`, port), 0o600))
	t.Cleanup(func() {
		service := exec.Command(binary, "service", "stop")
		service.Dir = work
		service.Env = environment()
		_ = service.Run()
	})
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	terminal, shown, stop := launch(ctx, []string{"--prompt", "Start quietly from notes.txt."})
	defer stop()
	await := func(text string) {
		for ctx.Err() == nil && !strings.Contains(shown(), text) {
			time.Sleep(100 * time.Millisecond)
		}
	}
	await("Served reply.")
	other := exec.CommandContext(ctx, binary, "run", "Reply in another session.")
	other.Dir = work
	other.Env = environment()
	output, err := other.CombinedOutput()
	h.Require.NoError(err, string(output))
	h.Require.Contains(string(output), "Served reply.")
	time.Sleep(3 * time.Second)
	h.Require.NotContains(shown(), "zai-org/GLM-5.3-Flash", "a run whose final response names no served model does not toast an earlier step's")
	h.Require.NotContains(shown(), "moonshotai/Kimi-K2.6", "a response in another session on the same background server does not toast")
	_, err = terminal.WriteString("Reply in this session again.")
	h.Require.NoError(err)
	await("Reply in this session again.")
	_, err = terminal.WriteString("\r")
	h.Require.NoError(err)
	await("→ deepseek-ai/DeepSeek-V4.2")
	h.Require.Contains(shown(), "→ deepseek-ai/DeepSeek-V4.2", "a response in the session on screen toasts")
	stop()

	setup("route-primary")
	screen, models = tui()
	h.Require.NotContains(screen, "Served model", "a route that is not a router shows nothing")
	h.Require.NotContains(screen, "qwen/Qwen3.6-Coder", "a route that is not a router shows nothing")
	h.Require.Contains(models, "acme/primary")
}

// serveFirstPartyRoutes serves an Anthropic route and an OpenAI route, each
// through a team connection, a Baseten-hosted route, and a route whose
// connection no longer exists.
func serveFirstPartyRoutes(api *MockManagementAPI) {
	messages := maps.Clone(harnessRouteMetadata)
	messages["supported_api_formats"] = map[string]any{"messages": true}
	responses := maps.Clone(harnessRouteMetadata)
	responses["supported_api_formats"] = map[string]any{"responses": true}
	api.SetRoute("GET", "/v1/grid/routes", 200, map[string]any{
		"items": []any{
			map[string]any{"team_id": "team-a", "team_name": "Engineering", "id": "route-a", "name": "acme/claude", "display_name": "Claude", "invoke_url": api.URL, "metadata": messages,
				"target": map[string]any{"type": "CONNECTION", "model": "claude-opus-5-5", "connection_id": "conn-anthropic"}},
			map[string]any{"team_id": "team-a", "team_name": "Engineering", "id": "route-b", "name": "acme/gpt", "display_name": "GPT", "invoke_url": api.URL, "metadata": responses,
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

func Test_Grid_Setup_GitContext(t *testing.T) {
	skipUnlessSupported(t)
	h, api := fakeGridAPI(t)
	root := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(root, "claude"))
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(root, "pi"))
	setup := []string{"grid", "setup", "--harness", "claude", "--harness", "opencode", "--harness", "pi", "--yes"}

	h.Context = internalcmd.WithExecer(h.Context, versionedHarnessExecer{"claude": "2.1.0 (Claude Code)", "opencode": "1.2.3", "pi": "1.2.3"})
	h.Require.ErrorContains(h.Execute(append(setup, "--git-context")...), "git not found on PATH, required by --git-context")
	h.Require.Empty(api.Calls())
	h.Require.NoFileExists(filepath.Join(recordDir(t), "harness.json"))
	entries, err := os.ReadDir(root)
	h.Require.NoError(err)
	h.Require.Empty(entries)
	h.Context = internalcmd.WithExecer(h.Context, fakeHarnessExecer{})

	installed := func() []bool {
		hooks, _ := readHarnessSettings(t, "claude", filepath.Join(root, "claude", "settings.json"))["hooks"].(map[string]any)
		_, claude := hooks["SessionStart"]
		return []bool{claude, fileExists(filepath.Join(root, "opencode", "plugins", "baseten-git-context.js")), fileExists(piGitContextFile(filepath.Join(root, "pi")))}
	}
	record := func() string {
		raw, err := os.ReadFile(filepath.Join(recordDir(t), "harness.json"))
		h.Require.NoError(err)
		return string(raw)
	}

	h.Require.NoError(h.Execute(setup...))
	h.Require.Equal([]bool{false, false, false}, installed())
	h.Require.NotContains(record(), "git_context")

	h.Require.NoError(h.Execute(append(setup, "--git-context")...))
	h.Require.Equal([]bool{true, true, true}, installed())
	h.Require.Equal(3, strings.Count(record(), `"git_context": true`))
	h.Require.True(harnessRecordFile(t).Harnesses["pi"].GitContext)

	h.Require.NoError(h.Execute("grid", "sync", "--yes"))
	h.Require.Equal([]bool{true, true, true}, installed(), "sync replays the recorded opt-in")

	h.Require.NoError(h.Execute(setup...))
	h.Require.Equal([]bool{false, false, false}, installed(), "setup without the flag removes them")
	h.Require.NotContains(record(), "git_context")
}

func Test_Grid_PiGitContext_RealPi(t *testing.T) {
	skipUnlessSupported(t)
	binary, err := exec.LookPath("pi")
	if err != nil {
		t.Skip("pi is not on PATH")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	var mu sync.Mutex
	var systems []string
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		mu.Lock()
		if len(body.Messages) > 0 && body.Messages[0].Role == "system" {
			systems = append(systems, body.Messages[0].Content)
		}
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		for _, chunk := range []map[string]any{
			{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"role": "assistant", "content": "OK."}, "finish_reason": nil}}},
			{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}}},
		} {
			chunk["id"], chunk["object"], chunk["created"], chunk["model"] = "chatcmpl-git", "chat.completion.chunk", 1, "acme/primary"
			raw, _ := json.Marshal(chunk)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", raw)
		}
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(gateway.Close)
	h, api := gridAPI(t, gateway.URL)
	api.SetRoute("GET", "/v1/grid/routes", 200, map[string]any{
		"items":      []any{map[string]any{"team_id": "team-a", "team_name": "Engineering", "id": "route-primary", "name": "acme/primary", "display_name": "Primary", "invoke_url": gateway.URL, "metadata": harnessRouteMetadata, "target": map[string]any{"type": "BASETEN_MODEL_API", "model": "deepseek"}}},
		"pagination": map[string]any{"has_more": false},
	})
	root := t.TempDir()
	configDir := filepath.Join(root, ".pi", "agent")
	h.Require.NoError(h.Execute("grid", "setup", "--harness", "pi", "--config-dir", configDir, "--use-session-credential", "--yes", "--served-model=false", "--git-context"))
	h.Require.FileExists(piGitContextFile(configDir))
	h.Require.NoFileExists(piServedModelFile(configDir))

	run := func(dir string, args ...string) string {
		command := exec.Command(args[0], args[1:]...)
		command.Dir = dir
		out, err := command.CombinedOutput()
		h.Require.NoError(err, string(out))
		return strings.TrimSpace(string(out))
	}
	prompt := func(dir string) string {
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
		defer cancel()
		command := exec.CommandContext(ctx, binary, "--print", "--no-session", "Reply with OK.")
		command.Dir = dir
		command.Env = []string{
			"HOME=" + root, "PI_CODING_AGENT_DIR=" + configDir, "PATH=" + os.Getenv("PATH"), "TMPDIR=" + t.TempDir(), "PI_OFFLINE=1", "PI_TELEMETRY=0",
			"XDG_CONFIG_HOME=" + filepath.Join(root, ".config"), "XDG_DATA_HOME=" + filepath.Join(root, ".local", "share"),
			"XDG_STATE_HOME=" + filepath.Join(root, ".local", "state"), "XDG_CACHE_HOME=" + filepath.Join(root, ".cache"),
		}
		command.WaitDelay = 5 * time.Second
		out, err := command.CombinedOutput()
		h.Require.NoError(err, string(out))
		h.Require.Equal("OK.", strings.TrimSpace(string(out)))
		mu.Lock()
		defer mu.Unlock()
		h.Require.NotEmpty(systems)
		return systems[len(systems)-1]
	}

	h.Require.NotContains(prompt(t.TempDir()), "repo=")
	repo := t.TempDir()
	run(repo, "git", "init", "-b", "main")
	h.Require.Contains(prompt(repo), "<baseten_git_context>\nrepo= branch=main commit=\n</baseten_git_context>")
	run(repo, "git", "-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false", "commit", "--allow-empty", "-m", "init")
	run(repo, "git", "remote", "add", "origin", "https://user:secret@example.com/org/repo.git")
	commit := run(repo, "git", "rev-parse", "HEAD")
	h.Require.Contains(prompt(repo), "<baseten_git_context>\nrepo=https://example.com/org/repo.git branch=main commit="+commit+"\n</baseten_git_context>")

	h.Require.NoError(h.Execute("grid", "teardown", "--harness", "pi", "--config-dir", configDir, "--yes"))
	h.Require.NoFileExists(piGitContextFile(configDir))
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
	h.Require.NoError(h.Execute("grid", "setup", "--harness", "claude", "--config-dir", dir, "--yes"))
	h.Require.Contains(h.Stderr.String(), "warning: skipping routes without usable model metadata: acme/bare")
	h.Require.NotContains(fmt.Sprint(readHarnessSettings(t, "claude", harnessSettingsFile("claude", dir))["availableModels"]), "acme/bare")

	h.Stderr.Reset()
	h.Require.Error(h.Execute("grid", "setup", "--harness", "claude", "--primary-route-id", "route-bare", "--config-dir", t.TempDir(), "--yes"))
	h.Require.Contains(h.Stderr.String(), `route "acme/bare" has no model metadata`)
	h.Require.Equal(0, countCalls(api, "POST", "/v1/teams/team-a/api_keys"), "a failed setup creates no key")

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
	h.Require.NoError(h.Execute("grid", "setup", "--harness", "claude", "--harness", "codex", "--yes"))

	claude := readHarnessSettings(t, "claude", filepath.Join(root, "claude", "settings.json"))
	h.Require.Equal("acme/claude", claude["model"])
	h.Require.Equal([]any{"acme/claude", "acme/open", "acme/new", "deepseek-ai/DeepSeek-V4.1-Flash"}, claude["availableModels"])
	h.Require.Equal("acme/gpt", readHarnessSettings(t, "codex", filepath.Join(root, "codex", "config.toml"))["model"])
	h.Require.Equal([]string{"acme/gpt", "acme/open", "acme/new"}, catalogSlugs(t, filepath.Join(root, "codex", "baseten-models.json")))

	h.Require.Error(h.Execute("grid", "setup", "--harness", "claude", "--primary-route-id", "route-gpt", "--yes"))
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
			h.Require.NoError(h.Execute("grid", "setup", "--harness", "codex", "--config-dir", dir, "--yes"))
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
	claude := readHarnessSettings(t, "claude", filepath.Join(root, "claude", "settings.json"))
	env := claude["env"].(map[string]any)
	h := map[string][2]string{"claude": {claude["model"].(string), env["ANTHROPIC_DEFAULT_HAIKU_MODEL"].(string)}}
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
		"claude":   {"acme/claude", "acme/fast"},
		"opencode": {"acme/gpt", "acme/claude"},
		"codex":    {"acme/gpt", ""},
		"pi":       {"acme/claude", ""},
	}, harnessSetupRoutes(t, root))
	env := readHarnessSettings(t, "claude", filepath.Join(root, "claude", "settings.json"))["env"].(map[string]any)
	h.Require.Equal("acme/fast", env["ANTHROPIC_SMALL_FAST_MODEL"])
	h.Require.Equal("acme/claude", env["ANTHROPIC_MODEL"], "the primary also beats an organization's claude.ai default")

	// Flags override the defaults for every harness.
	h.Require.NoError(h.Execute("grid", "setup", "--harness", "claude", "--harness", "opencode", "--primary-route-id", "route-primary", "--background-route-id", "route-claude", "--dry-run"))
	h.Require.Contains(h.Stdout.String(), "Primary route     acme/primary (flag)")
	h.Require.Contains(h.Stdout.String(), "Background route  acme/claude (flag)")
	h.Require.NotContains(h.Stdout.String(), " default)")
	h.Require.NoError(h.Execute("grid", "setup", "--harness", "claude", "--harness", "opencode", "--primary-route-id", "route-primary", "--background-route-id", "route-claude", "--yes"))
	h.Require.Equal([2]string{"acme/primary", "acme/claude"}, harnessSetupRoutes(t, root)["claude"])
	h.Require.Equal([2]string{"acme/primary", "acme/claude"}, harnessSetupRoutes(t, root)["opencode"])
	env = readHarnessSettings(t, "claude", filepath.Join(root, "claude", "settings.json"))["env"].(map[string]any)
	h.Require.Equal("acme/primary", env["ANTHROPIC_MODEL"], "a refresh updates the pinned primary")

	// A primary flag keeps the background default.
	h.Require.NoError(h.Execute("grid", "setup", "--harness", "claude", "--primary-route-id", "route-primary", "--yes"))
	h.Require.Equal([2]string{"acme/primary", "acme/fast"}, harnessSetupRoutes(t, root)["claude"])
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
	h.Require.NoError(h.Execute("grid", "setup", "--harness", "claude", "--yes"))
	h.Require.NotContains(h.Stderr.String(), "warning")
	// Skipped roles keep the CLI's defaults.
	h.Require.Equal([2]string{"acme/primary", "deepseek-ai/DeepSeek-V4.1-Flash"}, harnessSetupRoutes(t, root)["claude"])

	h.Require.NoError(h.Execute("grid", "setup", "--harness", "claude", "--verbose", "--dry-run"))
	for _, role := range []string{"primary", "background"} {
		h.Require.Contains(h.Stderr.String(), "Ignoring the team default for the claude "+role+" route: claude can't call route acme/gpt\n")
	}

	// A flag replaces a default setup can't use, without a warning.
	h.Require.NoError(h.Execute("grid", "setup", "--harness", "claude", "--primary-route-id", "route-claude", "--background-route-id", "route-fast", "--yes"))
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
		{"--harness", "claude", "--harness", "opencode", "--primary-route-id", "route-primary", "--background-route-id", "route-fast"},
		// Codex has no background route, so --primary-route-id sets every route it uses.
		{"--harness", "codex", "--primary-route-id", "route-primary"},
		// Pi also has only a primary route.
		{"--harness", "pi", "--primary-route-id", "route-primary"},
	} {
		h.Require.NoError(h.Execute(append([]string{"grid", "setup", "--dry-run"}, args...)...))
	}
	h.Require.Equal(0, countCalls(api, "GET", harnessDefaultsPath))
	h.Require.NoError(h.Execute("grid", "setup", "--dry-run", "--harness", "claude", "--primary-route-id", "route-primary"))
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
	h.Require.Error(h.Execute("grid", "setup", "--harness", "claude", "--yes"))
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
			h.Require.NoError(h.Execute("grid", "setup", "--harness", "claude", "--harness", "opencode", "--harness", "codex", "--dry-run"))
			h.Require.Contains(h.Stdout.String(), "Primary route     acme/primary (CLI default)")
			h.Require.Contains(h.Stdout.String(), "Background route  deepseek-ai/DeepSeek-V4.1-Flash (CLI default)")
			h.Require.NoError(h.Execute("grid", "setup", "--harness", "claude", "--harness", "opencode", "--harness", "codex", "--yes"))
			if name == "failing" {
				h.Require.Contains(h.Stderr.String(), "warning: could not read harness defaults, using CLI defaults")
			} else {
				h.Require.NotContains(h.Stderr.String(), "warning")
			}
			h.Require.Equal(map[string][2]string{
				"claude":   {"acme/primary", "deepseek-ai/DeepSeek-V4.1-Flash"},
				"opencode": {"acme/primary", "deepseek-ai/DeepSeek-V4.1-Flash"},
				"codex":    {"acme/primary", ""},
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
	args := []string{"grid", "setup", "--harness", "claude", "--config-dir", dir, "--team", "team-b", "--primary-route-id", "route-primary", "--background-route-id", "route-second"}
	h.Require.NoError(h.Execute(append(args, "--dry-run")...))
	h.Require.Empty(harnessRecordFile(t).Harnesses)

	h.Require.NoError(h.Execute(append(args, "--yes")...))
	record, ok := harnessRecordFile(t).Harnesses["claude"]
	h.Require.True(ok, "setup records the harness")
	h.Require.Equal(filepath.Join(dir, "settings.json"), record.Config)
	h.Require.Equal(api.URL, record.ManagementURL)
	h.Require.Equal("team-b", record.TeamID)
	h.Require.Equal(harness.CredentialSourceHelper, record.CredentialSource)
	h.Require.Equal("acme/primary", record.Route)
	h.Require.Equal("acme/second", record.BackgroundRoute)
	h.Require.Empty(record.SubagentRoute, "unset flags stay empty so sync follows defaults")
	h.Require.Equal("alice", record.Profile)

	// A record from another profile is not synced.
	file := harnessRecordFile(t)
	record.Profile = "someone-else@example.com"
	file.Harnesses["claude"] = record
	saveHarnessRecordFile(t, file)
	h.Require.NoError(h.Execute("grid", "sync", "--yes"))
	h.Require.Contains(h.Stderr.String(), "No harnesses to sync")

	// Teardown forgets the record.
	h.Require.NoError(h.Execute("grid", "teardown", "--harness", "claude", "--config-dir", dir, "--yes"))
	h.Require.NotContains(harnessRecordFile(t).Harnesses, "claude")

	// So does teardown of a hand-reverted harness, which has no settings left
	// to remove; a dry run keeps the record.
	h.Require.NoError(h.Execute(append(args, "--yes")...))
	h.Require.NoError(os.Remove(filepath.Join(dir, "settings.json")))
	h.Require.NoError(h.Execute("grid", "teardown", "--harness", "claude", "--config-dir", dir, "--dry-run"))
	h.Require.Contains(harnessRecordFile(t).Harnesses, "claude")
	h.Require.NoError(h.Execute("grid", "teardown", "--harness", "claude", "--config-dir", dir))
	h.Require.Contains(h.Stderr.String(), "No Baseten settings to remove.")
	h.Require.NotContains(harnessRecordFile(t).Harnesses, "claude")
}

func Test_Grid_Sync(t *testing.T) {
	skipUnlessSupported(t)
	h, api := gridAPI(t, "")
	setHarnessRoutes(api, "acme/primary")
	h.Context = internalcmd.WithExecer(h.Context, fakeHarnessExecer{})
	dir := t.TempDir()
	h.Require.NoError(h.Execute("grid", "setup", "--harness", "claude", "--config-dir", dir, "--yes"))

	// A configured harness whose record is gone (setup by an older CLI) is skipped.
	require.NoError(t, os.Remove(filepath.Join(recordDir(t), "harness.json")))
	h.Require.NoError(h.Execute("grid", "sync", "--yes"))
	h.Require.Contains(h.Stderr.String(), "No harnesses to sync")
	h.Require.Equal(0, h.ExitCode)

	// With a record and nothing changed, sync applies nothing without prompting
	// (stdin is not a terminal) and creates no key.
	h.Require.NoError(h.Execute("grid", "setup", "--harness", "claude", "--config-dir", dir, "--yes"))
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
	picker := readHarnessSettings(t, "claude", harnessSettingsFile("claude", dir))["modelPicker"].(map[string]any)
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
	h.Require.NoError(h.Execute("grid", "setup", "--harness", "claude", "--config-dir", dir, "--background-route-id", "route-bg", "--yes"))
	settings := readHarnessSettings(t, "claude", harnessSettingsFile("claude", dir))
	h.Require.Equal("acme/bg", settings["env"].(map[string]any)["ANTHROPIC_DEFAULT_HAIKU_MODEL"])

	// The team removes acme/bg; sync falls back to the default and records it.
	setHarnessRoutes(api, "acme/primary")
	h.Require.NoError(h.Execute("grid", "sync", "--yes"))
	h.Require.Contains(h.Stderr.String(), `warning: claude: background route "acme/bg" no longer exists; using "deepseek-ai/DeepSeek-V4.1-Flash"`)
	settings = readHarnessSettings(t, "claude", harnessSettingsFile("claude", dir))
	h.Require.Equal("deepseek-ai/DeepSeek-V4.1-Flash", settings["env"].(map[string]any)["ANTHROPIC_DEFAULT_HAIKU_MODEL"])
	h.Require.Empty(harnessRecordFile(t).Harnesses["claude"].BackgroundRoute, "the fallback is recorded for the next sync")

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
		{"claude", "claude_code", true},
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
	h.Require.NoError(h.Execute("grid", "setup", "--harness", "claude", "--config-dir", dir, "--background-route-id", "route-primary", "--yes"))
	keys := countCalls(api, "POST", "/v1/teams/team-a/api_keys")

	setHarnessRoutes(api, "acme/primary", "acme/second")
	h.Require.NoError(h.Execute("grid", "sync", "--dry-run", "--output", "json"))
	var preview public.GridPlanList
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &preview))
	for _, item := range preview.Items {
		h.Require.True(item.Changed)
	}
	h.Require.Len(readHarnessSettings(t, "claude", harnessSettingsFile("claude", dir))["modelPicker"].(map[string]any)["options"], 1, "no files changed")
	h.Require.Equal("acme/primary", harnessRecordFile(t).Harnesses["claude"].BackgroundRoute, "no record changes")
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

func gridSyncHarnessRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(root, "claude"))
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(root, "pi"))
	return root
}

func harnessRecordJSON(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(recordDir(t), "harness.json"))
	require.NoError(t, err)
	return string(raw)
}

func Test_Grid_Sync_GitContext(t *testing.T) {
	skipUnlessSupported(t)
	h, _ := fakeGridAPI(t)
	root := gridSyncHarnessRoot(t)
	installed := func() []bool {
		hooks, _ := readHarnessSettings(t, "claude", filepath.Join(root, "claude", "settings.json"))["hooks"].(map[string]any)
		_, claude := hooks["SessionStart"]
		return []bool{claude, fileExists(filepath.Join(root, "opencode", "plugins", "baseten-git-context.js")), fileExists(piGitContextFile(filepath.Join(root, "pi")))}
	}
	h.Require.NoError(h.Execute("grid", "setup", "--harness", "claude", "--harness", "opencode", "--harness", "pi", "--yes"))
	h.Require.Equal([]bool{false, false, false}, installed())

	h.Require.NoError(h.Execute("grid", "sync", "--git-context", "--dry-run", "--output", "json"))
	var preview public.GridPlanList
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &preview))
	h.Require.True(slices.ContainsFunc(preview.Items, func(p public.GridPlan) bool { return p.Changed }), "the preview applies the flag")
	h.Require.Equal([]bool{false, false, false}, installed(), "a dry run changes no files")
	h.Require.NotContains(harnessRecordJSON(t), "git_context", "a dry run changes no records")

	h.Require.NoError(h.Execute("grid", "sync", "--git-context", "--yes"))
	h.Require.Equal([]bool{true, true, true}, installed())
	h.Require.Equal(3, strings.Count(harnessRecordJSON(t), `"git_context": true`))

	h.Require.NoError(h.Execute("grid", "sync", "--yes"))
	h.Require.Contains(h.Stderr.String(), "All configured harnesses are up to date.")
	h.Require.Equal([]bool{true, true, true}, installed(), "sync replays the recorded opt-in")
	h.Require.NoError(h.Execute("grid", "sync", "--git-context", "--yes"))
	h.Require.Contains(h.Stderr.String(), "All configured harnesses are up to date.", "a flag matching the record changes nothing")

	h.Require.NoError(h.Execute("grid", "sync", "--git-context=false", "--yes"))
	h.Require.Equal([]bool{false, false, false}, installed())
	h.Require.NotContains(harnessRecordJSON(t), "git_context")

	h.Require.NoError(h.Execute("grid", "sync", "--yes"))
	h.Require.Contains(h.Stderr.String(), "All configured harnesses are up to date.")
	h.Require.Equal([]bool{false, false, false}, installed(), "sync replays the recorded opt-out")
}

func Test_Grid_Sync_ServedModel(t *testing.T) {
	skipUnlessSupported(t)
	h, api := fakeGridAPI(t)
	h.Context = internalcmd.WithExecer(h.Context, versionedHarnessExecer{"claude": "2.1.272 (Claude Code)", "opencode": "opencode v2.2.5", "pi": "1.2.3"})
	serveServedModelRoutes(api, "auto")
	root := gridSyncHarnessRoot(t)
	shown := func() []bool {
		_, claude := readHarnessSettings(t, "claude", filepath.Join(root, "claude", "settings.json"))["statusLine"]
		return []bool{claude, fileExists(piServedModelFile(filepath.Join(root, "pi"))), fileExists(openCodePluginDir(filepath.Join(root, "opencode")))}
	}
	recorded := func() []bool {
		records := harnessRecordFile(t).Harnesses
		return []bool{records["claude"].NoClaudeServedModel, records["pi"].NoPiServedModel, records["opencode"].NoOpenCodeServedModel}
	}
	h.Require.NoError(h.Execute("grid", "setup", "--harness", "claude", "--harness", "opencode", "--harness", "pi", "--yes"))
	h.Require.Equal([]bool{true, true, true}, shown())

	h.Require.NoError(h.Execute("grid", "sync", "--served-model=false", "--yes"))
	h.Require.Equal([]bool{false, false, false}, shown())
	h.Require.Equal([]bool{true, true, true}, recorded())

	h.Require.NoError(h.Execute("grid", "sync", "--yes"))
	h.Require.Contains(h.Stderr.String(), "All configured harnesses are up to date.")
	h.Require.Equal([]bool{false, false, false}, shown(), "sync replays the recorded opt-out")
	h.Require.Equal([]bool{true, true, true}, recorded())

	h.Require.NoError(h.Execute("grid", "sync", "--served-model", "--yes"))
	h.Require.Equal([]bool{true, true, true}, shown())
	h.Require.Equal([]bool{false, false, false}, recorded())
	h.Require.NotContains(harnessRecordJSON(t), "served_model")
	h.Require.NoError(h.Execute("grid", "sync", "--yes"))
	h.Require.Contains(h.Stderr.String(), "All configured harnesses are up to date.")
	h.Require.Equal([]bool{true, true, true}, shown(), "sync replays the recorded opt-in")

	h.Require.NoError(h.Execute("grid", "setup", "--harness", "pi", "--served-model=false", "--yes"))
	h.Require.Equal([]bool{true, false, true}, shown())
	h.Require.NoError(h.Execute("grid", "sync", "--yes"))
	h.Require.Contains(h.Stderr.String(), "All configured harnesses are up to date.")
	h.Require.Equal([]bool{true, false, true}, shown(), "sync replays a per-harness opt-out")
	h.Require.Equal([]bool{false, true, false}, recorded())
}

func Test_Grid_Sync_OmittedFlagsKeepRecord(t *testing.T) {
	skipUnlessSupported(t)
	h, api := fakeGridAPI(t)
	h.Context = internalcmd.WithExecer(h.Context, versionedHarnessExecer{"claude": "2.1.272 (Claude Code)", "opencode": "opencode v2.2.5", "pi": "1.2.3", "git": "git version 2.47.0"})
	serveServedModelRoutes(api, "auto")
	gridSyncHarnessRoot(t)
	h.Require.NoError(h.Execute("grid", "setup", "--harness", "claude", "--harness", "opencode", "--harness", "pi",
		"--git-context", "--served-model=false", "--yes"))
	recorded := harnessRecordJSON(t)
	h.Require.Equal(3, strings.Count(recorded, `"git_context": true`))

	for _, flags := range [][]string{nil, {"--git-context"}, {"--served-model=false"}, {"--git-context", "--served-model=false"}} {
		h.Require.NoError(h.Execute(append([]string{"grid", "sync", "--yes"}, flags...)...))
		h.Require.Contains(h.Stderr.String(), "All configured harnesses are up to date.", flags)
		h.Require.Equal(recorded, harnessRecordJSON(t), flags)
	}
}

func Test_Grid_Sync_GitContextRequiresGit(t *testing.T) {
	skipUnlessSupported(t)
	h, api := fakeGridAPI(t)
	root := gridSyncHarnessRoot(t)
	h.Require.NoError(h.Execute("grid", "setup", "--harness", "claude", "--harness", "opencode", "--harness", "pi", "--yes"))
	files := func() map[string]string {
		contents := map[string]string{}
		for _, dir := range []string{root, recordDir(t)} {
			h.Require.NoError(filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
				if err != nil || d.IsDir() {
					return err
				}
				raw, err := os.ReadFile(path)
				contents[path] = string(raw)
				return err
			}))
		}
		return contents
	}
	before, calls := files(), len(api.Calls())
	h.Context = internalcmd.WithExecer(h.Context, versionedHarnessExecer{"claude": "1.2.3", "opencode": "1.2.3", "pi": "1.2.3"})

	h.Require.Error(h.Execute("grid", "sync", "--git-context", "--yes"))
	h.Require.Equal(int(public.ExitUsage), h.ExitCode)
	h.Require.Contains(h.Stderr.String(), "git not found on PATH, required by --git-context")
	h.Require.Len(api.Calls(), calls, "no API calls")
	h.Require.Equal(before, files(), "no files changed")

	h.Require.NoError(h.Execute("grid", "sync", "--git-context=false", "--yes"))
	h.Require.NotContains(harnessRecordJSON(t), "git_context")
}

func Test_Grid_Setup_UseSessionCredential(t *testing.T) {
	skipUnlessSupported(t)
	h, api := gridAPI(t, "")
	setHarnessRoutes(api, "acme/primary")
	h.Context = internalcmd.WithExecer(h.Context, fakeHarnessExecer{})
	dir := t.TempDir()
	path := harnessSettingsFile("claude", dir)
	base := []string{"grid", "setup", "--harness", "claude", "--config-dir", dir, "--yes"}

	// --key-name names a key the CLI creates, which it never does here.
	h.Require.Error(h.Execute(append(slices.Clone(base), "--use-session-credential", "--key-name", "laptop")...))
	h.Require.Contains(h.Stderr.String(), "--key-name is not used with --use-session-credential")

	h.Require.NoError(h.Execute(append(slices.Clone(base), "--use-session-credential")...))
	h.Require.Equal(0, countCalls(api, "POST", "/v1/teams/team-a/api_keys"), "no Grid API key is created")
	h.Require.Nil(api.FindCall("GET", "/v1/users/me"), "the session key needs no user-scoped save")
	h.Require.Equal("test-key", readHarnessSettings(t, "claude", path)["env"].(map[string]any)["ANTHROPIC_AUTH_TOKEN"])
	record := harnessRecordFile(t).Harnesses["claude"]
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
	h.Require.NoError(h.Execute("grid", "setup", "--harness", "claude", "--config-dir", dir, "--use-session-credential", "--dry-run", "--output", "json"))
	var preview public.GridPlanList
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &preview))
	for _, item := range preview.Items {
		h.Require.True(item.Changed, "a rotated session key is a change")
	}
	h.Require.Equal(0, countCalls(api, "POST", "/v1/teams/team-a/api_keys"), "no key was created by the preview")
	h.Require.NoError(h.Execute(append(slices.Clone(base), "--use-session-credential")...))
	h.Require.Equal("rotated-key", readHarnessSettings(t, "claude", path)["env"].(map[string]any)["ANTHROPIC_AUTH_TOKEN"])
}

func Test_Grid_Setup_UseSessionCredentialRequiresAPIKey(t *testing.T) {
	skipUnlessSupported(t)
	h, api := gridAPI(t, "")
	setHarnessRoutes(api, "acme/primary")
	h.Context = internalcmd.WithExecer(h.Context, fakeHarnessExecer{})
	t.Setenv("BASETEN_API_KEY", "")
	dir := t.TempDir()
	path := harnessSettingsFile("claude", dir)

	// An OAuth session has no embeddable key.
	h.Require.NoError(configDirStore(t).SetOAuthProfile(t.Context(), "alice", api.URL, auth.OAuthCredential{AccessToken: "at", RefreshToken: "rt"}, true, nil))
	h.Require.Error(h.Execute("grid", "setup", "--harness", "claude", "--config-dir", dir, "--use-session-credential", "--dry-run"))
	h.Require.Contains(h.Stderr.String(), "no API key to install")
	h.Require.NoFileExists(path)

	// Neither does an unauthenticated session, which cannot list teams at all.
	h.Require.NoError(configDirStore(t).RemoveProfile(t.Context(), "alice"))
	h.Require.Error(h.Execute("grid", "setup", "--harness", "claude", "--config-dir", dir, "--use-session-credential", "--dry-run"))
	h.Require.Contains(h.Stderr.String(), "not logged in")
	h.Require.NoFileExists(path)
}

func Test_Grid_Sync_UseSessionCredential(t *testing.T) {
	skipUnlessSupported(t)
	h, api := gridAPI(t, "")
	setHarnessRoutes(api, "acme/primary")
	h.Context = internalcmd.WithExecer(h.Context, fakeHarnessExecer{})
	dir := t.TempDir()
	path := harnessSettingsFile("claude", dir)
	// An ephemeral session installs its own key without the flag.
	t.Setenv("BASETEN_API_KEY", "test-key")
	h.Require.NoError(configDirStore(t).RemoveProfile(t.Context(), "alice"))
	h.Require.NoError(h.Execute("grid", "setup", "--harness", "claude", "--config-dir", dir, "--yes"))
	h.Require.Equal(harness.CredentialSourceSession, harnessRecordFile(t).Harnesses["claude"].CredentialSource)
	h.Require.Equal(0, countCalls(api, "POST", "/v1/teams/team-a/api_keys"))

	// Nothing changed: up to date, without creating a key.
	h.Require.NoError(h.Execute("grid", "sync", "--yes"))
	h.Require.Contains(h.Stderr.String(), "All configured harnesses are up to date.")
	h.Require.Equal(0, countCalls(api, "POST", "/v1/teams/team-a/api_keys"))

	// A rotated key is rewritten even though no route changed.
	t.Setenv("BASETEN_API_KEY", "rotated-key")
	h.Require.NoError(h.Execute("grid", "sync", "--yes"))
	h.Require.Equal("rotated-key", readHarnessSettings(t, "claude", path)["env"].(map[string]any)["ANTHROPIC_AUTH_TOKEN"])
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
	h.Require.NoError(h.Execute("grid", "setup", "--harness", "claude", "--config-dir", dir, "--use-session-credential", "--yes"))
	h.Require.NoError(h.Execute("grid", "setup", "--harness", "opencode", "--config-dir", dir, "--key-name", "laptop", "--yes"))
	keys := countCalls(api, "POST", "/v1/teams/team-a/api_keys")
	h.Require.Equal(1, keys)
	h.Require.Equal("laptop", api.FindCall("POST", "/v1/teams/team-a/api_keys").BodyJSON(t)["name"])

	// Sync refreshes both without cross-applying credentials: the session key
	// stays with claude and the created key with opencode.
	setHarnessRoutes(api, "acme/primary", "acme/second")
	h.Require.NoError(h.Execute("grid", "sync", "--yes"))
	h.Require.Equal("test-key", readHarnessSettings(t, "claude", harnessSettingsFile("claude", dir))["env"].(map[string]any)["ANTHROPIC_AUTH_TOKEN"])
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
	path := harnessSettingsFile("claude", dir)
	setup := []string{"grid", "setup", "--harness", "claude", "--config-dir", dir, "--use-session-credential", "--yes"}
	teardown := []string{"grid", "teardown", "--harness", "claude", "--config-dir", dir, "--yes"}
	// List the session's key as a routes key the caller created, so teardown
	// could delete it if it weren't protected.
	api.SetRoute("GET", "/v1/api_keys", 200, map[string]any{"keys": []any{
		map[string]any{"prefix": "test-k", "type": "GRID", "name": "sandbox", "team_name": "Engineering", "created_at": "2026-09-01T00:00:00Z"},
	}})
	api.SetRoute("DELETE", "/v1/api_keys/test-k", 200, map[string]string{"prefix": "test-k"})
	h.Require.NoError(h.Execute(setup...))

	// The settings are removed, but the key is never offered for deletion.
	h.Require.NoError(h.Execute("grid", "teardown", "--harness", "claude", "--config-dir", dir, "--dry-run"))
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
	h.Require.NoError(h.Execute("grid", "setup", "--harness", "opencode", "--config-dir", dir, "--primary-route-id", "route-second", "--yes", "--verbose"))
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
	h.Require.Equal("team-b", harnessRecordFile(t).Harnesses["opencode"].TeamID)
	h.Require.Equal("acme/second", harnessRecordFile(t).Harnesses["opencode"].Route)

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
	h.Require.Equal("team-b", harnessRecordFile(t).Harnesses["opencode"].TeamID)
	h.Require.Equal("acme/second", harnessRecordFile(t).Harnesses["opencode"].Route)
}

func Test_Grid_Setup_ServerTeamResolutionFailureHasNoSideEffects(t *testing.T) {
	skipUnlessSupported(t)
	for _, status := range []int{400, 401, 403, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			h, api := fakeGridAPI(t)
			api.SetRoute("GET", "/v1/grid/routes", status, map[string]string{"message": "server rejected team resolution"})
			dir := t.TempDir()
			h.Require.Error(h.Execute("grid", "setup", "--harness", "claude", "--config-dir", dir, "--yes", "--output", "json"))
			h.Require.Equal(status, decodeJSONErrorEnvelope(h).APIStatusCode)
			h.Require.Len(api.Calls(), 1)
			h.Require.NoFileExists(harnessSettingsFile("claude", dir))
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
			args := []string{"grid", "setup", "--harness", "claude", "--config-dir", dir, "--yes"}
			if tc.selected != "" {
				args = append(args, "--team", tc.selected)
			}
			h.Require.ErrorContains(h.Execute(args...), tc.want)
			h.Require.Nil(api.FindCall("GET", "/v1/grid/connections"))
			for _, call := range api.Calls() {
				h.Require.Equal("GET", call.Method)
			}
			h.Require.NoFileExists(harnessSettingsFile("claude", dir))
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

func Test_Grid_Classifier_Describe(t *testing.T) {
	for _, output := range []string{"text", "json", "jsonl"} {
		t.Run(output, func(t *testing.T) {
			h := NewCommandHarness(t)
			m := h.MockManagementAPI()
			m.SetRouteFunc("GET", "/v1/grid/classifiers/general-v1", func(w http.ResponseWriter, r *http.Request) {
				h.Require.Equal("Bearer test-key", r.Header.Get("Authorization"))
				h.Require.Empty(r.URL.RawQuery)
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"id": "general-v1", "name": "General"})
			})
			h.Require.NoError(h.Execute("grid", "classifier", "describe", "--id", "general-v1", "--output", output))
			h.Require.Len(m.Calls(), 1)
			if output == "text" {
				h.Require.Contains(h.Stdout.String(), "general-v1")
				h.Require.Contains(h.Stdout.String(), "General")
			} else {
				h.Require.JSONEq(`{"id":"general-v1","name":"General"}`, h.Stdout.String())
			}
		})
	}
}

func Test_Grid_Classifier_Describe_Errors(t *testing.T) {
	t.Run("empty id", func(t *testing.T) {
		h := NewCommandHarness(t)
		m := h.MockManagementAPI()
		h.Require.Error(h.Execute("grid", "classifier", "describe", "--id", ""))
		h.Require.Equal(2, h.ExitCode)
		h.Require.Empty(m.Calls())
	})
	for _, status := range []int{401, 403, 404, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			h := NewCommandHarness(t)
			m := h.MockManagementAPI()
			m.SetRoute("GET", "/v1/grid/classifiers/general-v1", status, map[string]string{"error": "unavailable"})
			h.Require.Error(h.Execute("grid", "classifier", "describe", "--id", "general-v1", "--output", "json"))
			h.Require.Equal(status, decodeJSONErrorEnvelope(h).APIStatusCode)
		})
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
	// OpenCode is the harness that still installs a Grid API key.
	args := []string{"grid", "setup", "--harness", "opencode", "--config-dir", dir, "--yes"}
	h.Require.NoError(h.Execute(args...))
	h.Require.NoError(h.Execute(args...))
	h.Require.Equal(1, countCalls(api, "POST", "/v1/teams/team-a/api_keys"), "an active key is reused")

	// The key was deleted on another machine.
	api.SetRoute("GET", "/v1/api_keys", 200, map[string]any{"keys": []any{}})
	api.SetRoute("POST", "/v1/teams/team-a/api_keys", 200, map[string]string{"api_key": "secret-team-a-2"})
	h.Require.NoError(h.Execute(args...))
	h.Require.Contains(h.Stderr.String(), "Created Grid API key")
	h.Require.Equal(2, countCalls(api, "POST", "/v1/teams/team-a/api_keys"))
	settings, err := os.ReadFile(harnessSettingsFile("opencode", dir))
	h.Require.NoError(err)
	h.Require.Contains(string(settings), "secret-team-a-2")

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
			"codex":       map[string]any{"primary": nil, "background": nil},
			"pi":          map[string]any{"primary": nil, "background": nil},
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
		{"claude", []string{"--harness", "claude", "--primary-route-id", "r123456", "--background-route-id", "null"}, map[string]any{"harness_defaults": map[string]any{"claude_code": map[string]any{"primary": "r123456", "background": nil}}}},
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
	h.Require.NoError(h.Execute("grid", "setup", "--harness", "claude", "--config-dir", dir,
		"--primary-route-id", "route-primary", "--background-route-id", "route-background",
		"--subagent-route-id", "route-subagent", "--fallback-route-id", "route-fallback", "--yes"))
	settings := readHarnessSettings(t, "claude", harnessSettingsFile("claude", dir))
	env := settings["env"].(map[string]any)
	h.Require.Equal("acme/primary", settings["model"])
	h.Require.Equal("acme/background", env["ANTHROPIC_DEFAULT_HAIKU_MODEL"])
	h.Require.Equal("acme/subagent", env["CLAUDE_CODE_SUBAGENT_MODEL"])
	h.Require.Equal([]any{"acme/fallback"}, settings["fallbackModel"])
	record := harnessRecordFile(t).Harnesses["claude"]
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
	h.Require.Equal(record, harnessRecordFile(t).Harnesses["claude"])
}

func Test_Grid_Setup_RejectsUnknownRouteIDsBeforeWriting(t *testing.T) {
	skipUnlessSupported(t)
	for _, flag := range []string{"--primary-route-id", "--background-route-id", "--subagent-route-id", "--fallback-route-id"} {
		for _, id := range []string{"route-missing", "acme/primary"} {
			t.Run(flag+"/"+id, func(t *testing.T) {
				h, api := fakeGridAPI(t)
				dir := t.TempDir()
				h.Require.Error(h.Execute("grid", "setup", "--harness", "claude", "--config-dir", dir, flag, id, "--yes"))
				h.Require.Equal(2, h.ExitCode)
				h.Require.Contains(h.Stderr.String(), flag)
				h.Require.Contains(h.Stderr.String(), "no route with ID")
				h.Require.Contains(h.Stderr.String(), "baseten grid route list")
				h.Require.Len(api.Calls(), 1)
				h.Require.Equal("/v1/grid/routes", api.Calls()[0].Path)
				h.Require.NoFileExists(harnessSettingsFile("claude", dir))
				h.Require.Empty(harnessRecordFile(t).Harnesses)
			})
		}
	}
}

func Test_Grid_Token(t *testing.T) {
	skipUnlessSupported(t)
	h := newAuthHarness(t)
	api := h.MockManagementAPI()
	api.SetRoute("POST", "/v1/grid/token", 200, map[string]any{"token": "grid-new", "expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339), "team_id": "team-a"})
	api.SetRoute("GET", "/v1/teams", 200, map[string]any{"teams": []map[string]any{{"id": "team-a", "name": "Engineering", "default": true}}})
	api.SetRoute("GET", "/v1/users/me", 200, map[string]string{"user_id": "user-b", "email": "b@example.com"})
	run := func() (stdout, stderr string, err error) {
		err = h.Execute("grid", "token", "--harness", "codex")
		return h.Stdout.String(), h.Stderr.String(), err
	}

	// No record, then a record without a session.
	stdout, stderr, err := run()
	h.Require.Error(err)
	h.Require.Equal(1, h.ExitCode)
	h.Require.Empty(stdout)
	h.Require.Equal("codex is not set up; run `baseten grid setup`\n", stderr)
	saveHarnessRecordFile(t, harness.RecordFile{Harnesses: map[string]harness.Record{
		"codex": {Config: "/x/config.toml", ManagementURL: api.URL, TeamID: "team-a", CredentialSource: harness.CredentialSourceHelper},
	}})
	_, stderr, err = run()
	h.Require.Error(err)
	h.Require.Contains(stderr, "run `baseten auth login`")

	// A cached token with time left is printed without an API call.
	store := configDirStore(t)
	h.Require.NoError(store.SetAPIKeyProfile(t.Context(), "alice", api.URL, "key-a", true, nil))
	session, err := auth.ResolveSession("alice", "")
	h.Require.NoError(err)
	scope := auth.GridTokenScope{ManagementURL: api.URL, Identity: session.CacheIdentity()}
	h.Require.NoError(store.SetGridToken(t.Context(), scope, "team-a", auth.GridToken{Token: "grid-cached", ExpiresAt: time.Now().Add(30 * time.Minute)}, nil))
	stdout, stderr, err = run()
	h.Require.NoError(err)
	h.Require.Equal("grid-cached\n", stdout)
	h.Require.Empty(stderr)
	h.Require.Equal(0, countCalls(api, "POST", "/v1/grid/token"))

	// The --profile the helper passes beats a BASETEN_API_KEY exported in the
	// harness's shell.
	t.Setenv("BASETEN_API_KEY", "someone-else")
	h.Require.NoError(h.Execute("grid", "token", "--harness", "codex", "--profile", "alice"))
	h.Require.Equal("grid-cached\n", h.Stdout.String())
	t.Setenv("BASETEN_API_KEY", "")

	// An expiring token is replaced once, and the replacement is cached.
	h.Require.NoError(store.SetGridToken(t.Context(), scope, "team-a", auth.GridToken{Token: "grid-stale", ExpiresAt: time.Now().Add(5 * time.Minute)}, nil))
	for range 2 {
		stdout, _, err = run()
		h.Require.NoError(err)
		h.Require.Equal("grid-new\n", stdout)
	}
	h.Require.Equal(1, countCalls(api, "POST", "/v1/grid/token"))
	h.Require.Equal(map[string]any{"team_id": "team-a"}, api.FindCall("POST", "/v1/grid/token").BodyJSON(t))

	h.Require.NoError(h.Execute("grid", "token", "--harness", "codex", "--team", "Engineering"))
	h.Require.Equal("grid-new\n", h.Stdout.String())
	h.Require.Equal(1, countCalls(api, "POST", "/v1/grid/token"), "the cached token serves --team")
	h.Require.Error(h.Execute("grid", "token"))
	h.Require.Contains(h.Stderr.String(), `Required flag(s) "harness" not set`)

	// A credential the API rejects, then login and logout, which forget the cache.
	h.Require.NoError(store.SetGridToken(t.Context(), scope, "team-a", auth.GridToken{Token: "grid-stale", ExpiresAt: time.Now()}, nil))
	api.SetRoute("POST", "/v1/grid/token", 401, map[string]string{"message": "rejected"})
	stdout, stderr, err = run()
	h.Require.Error(err)
	h.Require.Empty(stdout)
	h.Require.Contains(stderr, "rejected")
	h.Require.Equal(int(public.ExitAuth), h.ExitCode)
	for _, command := range [][]string{{"auth", "login", "--with-api-key", "--profile", "alice"}, {"auth", "logout", "--profile", "alice"}} {
		h.Require.NoError(store.SetGridToken(t.Context(), scope, "team-a", auth.GridToken{Token: "grid-cached", ExpiresAt: time.Now().Add(time.Hour)}, nil))
		h.Stdin.WriteString("key-b\n")
		h.Require.NoError(h.Execute(command...))
		token, err := store.GetGridToken(t.Context(), scope, "team-a")
		h.Require.NoError(err)
		h.Require.Nil(token, "%s forgets the cached tokens", command[1])
	}
}
