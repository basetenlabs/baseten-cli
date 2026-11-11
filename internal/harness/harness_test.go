package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/basetenlabs/baseten-go/client/managementapi"
	"github.com/stretchr/testify/require"
)

const testToken = "baseten-harness-test-token"

const testEndpoint = "https://inference.example.com"

var chatCompletionsFormats = []managementapi.GridRouteMetadataApiFormats{managementapi.GridRouteMetadataApiFormats_chat_completions}

func testRoute(name, displayName string) Route {
	return Route{
		ID: name, Name: name, DisplayName: displayName,
		ContextWindow: 128000, OutputLimit: 4096, InputModalities: []string{"text"}, Tools: true, Responses: true,
		TargetAPI: TargetAPIChatCompletions,
	}
}

func TestNewRouteAcceptsMirroredOutputLimit(t *testing.T) {
	contextWindow, mirroredOutput, tools := 500000, 500000, true
	route, err := NewRoute(managementapi.GridRoute{
		Name: "acme/possible",
		Metadata: &managementapi.GridRouteMetadata{
			ContextWindow:   &contextWindow,
			MaxOutputTokens: &mirroredOutput,
			InputModalities: []string{"text", "image"},
			Tools:           &tools,
			ApiFormats:      chatCompletionsFormats,
		},
	}, nil)
	require.NoError(t, err)
	require.Equal(t, 500000, route.ContextWindow)
	require.Equal(t, 500000, route.OutputLimit)

	impossibleOutput := 500001
	_, err = NewRoute(managementapi.GridRoute{
		Name: "acme/impossible",
		Metadata: &managementapi.GridRouteMetadata{
			ContextWindow:   &contextWindow,
			MaxOutputTokens: &impossibleOutput,
			InputModalities: []string{"text"},
			Tools:           &tools,
			ApiFormats:      chatCompletionsFormats,
		},
	}, nil)
	require.ErrorContains(t, err, "no usable context and output limits")
}

func TestNewRouteMarksRouters(t *testing.T) {
	tools, ctx, out := true, 128000, 4096
	metadata := &managementapi.GridRouteMetadata{ContextWindow: &ctx, MaxOutputTokens: &out, InputModalities: []string{"text"}, Tools: &tools, ApiFormats: chatCompletionsFormats}
	for _, tc := range []struct {
		target string
		want   bool
	}{
		{`{"type": "INTELLIGENT"}`, true},
		{`{"type": "BASETEN_MODEL_API", "model": "deepseek"}`, false},
		{`{"type": "CONNECTION", "connection_id": "conn-a", "model": "claude"}`, false},
		{`{"type": "CONNECTION", "connection_id": "conn-gone", "model": "gpt"}`, false},
	} {
		api := managementapi.GridRoute{Name: "acme/route", Metadata: metadata}
		require.NoError(t, json.Unmarshal([]byte(tc.target), &api.Target))
		r, err := NewRoute(api, Providers{"conn-a": ProviderAnthropic})
		require.NoError(t, err)
		require.Equal(t, tc.want, r.Router, tc.target)
	}
	r, err := NewRoute(managementapi.GridRoute{Name: "acme/untyped", Metadata: metadata}, nil)
	require.NoError(t, err)
	require.False(t, r.Router, "a route without a target type is not a router")
}

func TestNewRouteTakesTargetAPIFromFirstSupportedAPIFormat(t *testing.T) {
	tools, ctx, out := true, 128000, 4096
	providers := Providers{"conn-anthropic": ProviderAnthropic, "conn-openai": ProviderOpenAI}
	for _, tc := range []struct {
		name                string
		target              string
		formats             []managementapi.GridRouteMetadataApiFormats
		want                TargetAPI
		messages, responses bool
		err                 string
	}{
		{"intelligent route", `{"type": "INTELLIGENT"}`, []managementapi.GridRouteMetadataApiFormats{"messages", "responses", "chat_completions"}, TargetAPIMessages, true, true, ""},
		{"model api", `{"type": "BASETEN_MODEL_API", "model": "deepseek"}`, []managementapi.GridRouteMetadataApiFormats{"chat_completions", "messages", "responses"}, TargetAPIChatCompletions, true, true, ""},
		{"model api in the server's order", `{"type": "BASETEN_MODEL_API", "model": "deepseek"}`, []managementapi.GridRouteMetadataApiFormats{"messages", "chat_completions"}, TargetAPIMessages, true, false, ""},
		{"responses only", `{"type": "CONNECTION", "connection_id": "conn-openai", "model": "gpt"}`, []managementapi.GridRouteMetadataApiFormats{"responses"}, TargetAPIResponses, false, true, ""},
		{"messages only", `{"type": "CONNECTION", "connection_id": "conn-anthropic", "model": "claude"}`, []managementapi.GridRouteMetadataApiFormats{"messages"}, TargetAPIMessages, true, false, ""},
		{"empty", `{"type": "INTELLIGENT"}`, []managementapi.GridRouteMetadataApiFormats{}, "", false, false, `route "acme/route" has no supported API formats`},
		{"unknown first", `{"type": "INTELLIGENT"}`, []managementapi.GridRouteMetadataApiFormats{"future_format", "messages"}, "", false, false, `route "acme/route" prefers unknown API format "future_format"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api := managementapi.GridRoute{
				Name: "acme/route", DisplayName: "Route",
				Metadata: &managementapi.GridRouteMetadata{
					ContextWindow: &ctx, MaxOutputTokens: &out,
					InputModalities: []string{"text"}, Tools: &tools,
					ApiFormats: tc.formats,
				},
			}
			require.NoError(t, json.Unmarshal([]byte(tc.target), &api.Target))
			r, err := NewRoute(api, providers)
			if tc.err != "" {
				require.ErrorContains(t, err, tc.err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, r.TargetAPI)
			require.Equal(t, tc.messages, r.Messages, "Claude Code lists routes whose formats include messages")
			require.Equal(t, tc.responses, r.Responses, "Codex lists routes whose formats include responses")
		})
	}
	_, err := NewRoute(managementapi.GridRoute{Name: "acme/route"}, providers)
	require.ErrorContains(t, err, `route "acme/route" has no model metadata`)
}

func TestNewRouteKeepsOnlyTextAndImageInputModalities(t *testing.T) {
	tools, ctx, out := true, 128000, 4096
	route := func(modalities ...string) (Route, error) {
		return NewRoute(managementapi.GridRoute{
			Name: "acme/av", DisplayName: "AV",
			Metadata: &managementapi.GridRouteMetadata{
				ContextWindow: &ctx, MaxOutputTokens: &out,
				InputModalities: modalities, Tools: &tools,
				ApiFormats: chatCompletionsFormats,
			},
		}, nil)
	}
	r, err := route("text", "image", "video", "audio")
	require.NoError(t, err)
	require.Equal(t, []string{"text", "image"}, r.InputModalities)
	_, err = route("audio", "video")
	require.ErrorContains(t, err, "has no input modalities")
}

func TestClaudeServedModelOwnsOnlyGeneratedCommands(t *testing.T) {
	routes := []Route{
		{Name: "acme/o'brien", Router: true}, {Name: "acme/plain", DisplayName: "Plain"},
		{Name: "b10-internal/auto-XP9WlBD", DisplayName: "Auto (Baseten Internal Dogfooding)", Router: true},
		{Name: "acme/it's", DisplayName: "x=y's", Router: true},
	}
	values := claudeServedModelSettings(routes, map[string]any{}, "/opt/it's/baseten")
	require.Len(t, values, 1)
	command := values[0].Installed.Data.(map[string]any)["command"].(string)
	require.Equal(t, `'/opt/it'\''s/baseten' grid claude-served-model --router 'acme/o'\''brien' --router 'b10-internal/auto-XP9WlBD=Auto (Baseten Internal Dogfooding)' --router 'acme/it'\''s=x=y'\''s'`, command)
	for _, tc := range []struct {
		command string
		want    bool
	}{
		{command, true},
		{command + " --hook", true},
		{command + " ; echo hi", false},
		{"echo " + command, false},
		{command + " --hook --hook", false},
		{`'/opt/it'\''s/baseten' grid claude-served-model`, false},
		{`'/opt/it's/baseten' grid claude-served-model --router 'acme/auto'`, false},
		{`echo "$(baseten grid claude-served-model --router acme/auto) | custom stats"`, false},
	} {
		require.Equal(t, tc.want, claudeServedModelOwned(map[string]any{"command": tc.command}), tc.command)
	}
}

func TestHasOpenCode2(t *testing.T) {
	for version, want := range map[string]bool{
		"opencode v2.2.5": true, "2.0.27": true, "opencode 10.1.0": true,
		"1.19.5": false, "opencode v1.18.34": false, "": false, "unknown": false,
	} {
		require.Equal(t, want, HasOpenCode2(Detection{Installs: []Install{{Version: version}}}), version)
	}
	require.True(t, HasOpenCode2(Detection{Installs: []Install{{Version: "1.19.5"}, {Version: "opencode v2.0.27"}}}))
	require.False(t, HasOpenCode2(Detection{}))
}

func testRoutes() []Route {
	return []Route{
		testRoute("acme/primary", "Primary"),
		testRoute("acme/background", "Background"),
		testRoute("acme/subagent", "Subagents"),
		testRoute("acme/fallback", "Fallback"),
	}
}

type fakeExecer struct{ missing bool }

// LookPath finds every bare name, as on PATH, and no desktop app, which is
// looked up by its absolute path.
func (e fakeExecer) LookPath(name string) (string, error) {
	if e.missing || filepath.IsAbs(name) {
		return "", exec.ErrNotFound
	}
	return filepath.Join(string(filepath.Separator), "fake", name), nil
}

func (fakeExecer) Exec(command *exec.Cmd) error {
	_, err := fmt.Fprintln(command.Stdout, "1.2.3\nextra")
	return err
}

// onPATH is where detectionExecer finds a bare binary name, as on PATH.
func onPATH(name string) string { return filepath.Join(string(filepath.Separator), "cli", name) }

// detectionExecer finds only the listed binaries. A bare name, as looked up
// on PATH, resolves with onPATH; a path resolves to itself. Each binary
// reports the version it maps to, or 1.2.3 if true.
type detectionExecer struct {
	fakeExecer
	binaries map[string]any
	executed []string
}

func (e *detectionExecer) LookPath(name string) (string, error) {
	if _, ok := e.binaries[name]; !ok {
		return "", exec.ErrNotFound
	}
	if !strings.ContainsRune(name, filepath.Separator) {
		return onPATH(name), nil
	}
	return name, nil
}

func (e *detectionExecer) Exec(command *exec.Cmd) error {
	e.executed = append(e.executed, command.Path)
	for name, version := range e.binaries {
		if version, ok := version.(string); ok && (name == command.Path || onPATH(name) == command.Path) {
			_, err := fmt.Fprintln(command.Stdout, version)
			return err
		}
	}
	return e.fakeExecer.Exec(command)
}

// desktopDetectionCase is one layout of harness installs on this machine.
type desktopDetectionCase struct {
	name     string
	binaries map[string]any
	want     []Install
	// goos lists the platforms where want applies. On other platforms the
	// desktop installs are not looked for, so only the installs in elsewhere
	// are found, and on Windows only the one on PATH. Empty means every
	// platform.
	goos      []string
	elsewhere []Install
}

func testDesktopDetection(t *testing.T, h Harness, path string, cases []desktopDetectionCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			execer := &detectionExecer{binaries: tc.binaries}
			want := tc.want
			if len(tc.goos) > 0 && !slices.Contains(tc.goos, runtime.GOOS) {
				want = tc.elsewhere
				if runtime.GOOS == "windows" {
					want = slices.DeleteFunc(slices.Clone(want), func(i Install) bool { return !strings.HasPrefix(i.Binary, onPATH("")) })
					if len(want) == 0 {
						want = nil
					}
				}
			}
			d, err := h.Detect(t.Context(), execer, "")
			require.NoError(t, err)
			require.Equal(t, path, d.Path)
			require.Equal(t, want != nil, d.Installed)
			require.Equal(t, want, d.Installs)
			var executed []string
			for _, install := range want {
				executed = append(executed, install.Binary)
			}
			require.Equal(t, executed, execer.executed, "every install's version is read, and only those")
		})
	}
}

func cli(binary, version string) Install {
	return Install{Kind: InstallCLI, Binary: binary, Version: version}
}
func desktop(binary, version string) Install {
	return Install{Kind: InstallDesktop, Binary: binary, Version: version}
}

func TestCodexDesktopDetection(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CODEX_HOME", "")
	path := filepath.Join(home, ".codex", "config.toml")
	save(t, path, map[string]any{})
	bundled := func(root, app string) string {
		return filepath.Join(root, app, "Contents", "Resources", "codex")
	}
	newBundled := func(root, app string) string {
		return filepath.Join(root, app, "Contents", "Resources", "codex-cli", "CodexCLI.app", "Contents", "MacOS", "codex")
	}
	chatgpt := bundled("/Applications", "ChatGPT.app")
	linuxChatGPT := "/usr/lib/chatgpt/resources/codex"
	one := func(binary string, goos string) desktopDetectionCase {
		return desktopDetectionCase{name: binary, binaries: map[string]any{binary: true}, want: []Install{desktop(binary, "1.2.3")}, goos: []string{goos}}
	}
	darwin := map[string]any{"codex": "0.1.0", chatgpt: "0.2.0"}
	linux := map[string]any{"codex": "0.1.0", linuxChatGPT: "0.3.0"}
	testDesktopDetection(t, codexHarness{}, path, []desktopDetectionCase{
		one(chatgpt, "darwin"),
		one(newBundled("/Applications", "ChatGPT.app"), "darwin"),
		one(bundled("/Applications", "Codex.app"), "darwin"),
		one(newBundled("/Applications", "Codex.app"), "darwin"),
		one(bundled(filepath.Join(home, "Applications"), "ChatGPT.app"), "darwin"),
		one(newBundled(filepath.Join(home, "Applications"), "ChatGPT.app"), "darwin"),
		one(bundled(filepath.Join(home, "Applications"), "Codex.app"), "darwin"),
		one(newBundled(filepath.Join(home, "Applications"), "Codex.app"), "darwin"),
		one(linuxChatGPT, "linux"),
		{name: "CLI and desktop on darwin", binaries: darwin, want: []Install{cli(onPATH("codex"), "0.1.0"), desktop(chatgpt, "0.2.0")}, goos: []string{"darwin"}, elsewhere: []Install{cli(onPATH("codex"), "0.1.0")}},
		{name: "CLI and desktop on linux", binaries: linux, want: []Install{cli(onPATH("codex"), "0.1.0"), desktop(linuxChatGPT, "0.3.0")}, goos: []string{"linux"}, elsewhere: []Install{cli(onPATH("codex"), "0.1.0")}},
		{name: "config alone is not an installation"},
		{name: "classic app without bundled codex", binaries: map[string]any{"/Applications/ChatGPT.app/Contents/MacOS/ChatGPT": true}},
	})
}

func TestCodexDesktopDetectionWithoutHome(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	chatgpt := filepath.Join("/Applications", "ChatGPT.app", "Contents", "Resources", "codex")
	execer := &detectionExecer{binaries: map[string]any{chatgpt: true}}
	d, err := codexHarness{}.Detect(t.Context(), execer, t.TempDir())
	require.NoError(t, err)
	require.Equal(t, runtime.GOOS == "darwin", d.Installed)
}

// lookPathExecer resolves binaries like the real CLI does, and fakes only
// running them.
type lookPathExecer struct{ fakeExecer }

func (lookPathExecer) LookPath(name string) (string, error) { return exec.LookPath(name) }

func TestDetectListsOneInstallPerExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("desktop installs are not looked for on Windows")
	}
	// OpenCode Desktop's CLI installer puts the CLI in ~/.opencode/bin and
	// the user links it onto PATH, so the same executable is found twice.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	target := filepath.Join(home, ".opencode", "bin", "opencode")
	require.NoError(t, os.MkdirAll(filepath.Dir(target), 0o700))
	require.NoError(t, os.WriteFile(target, []byte("#!/bin/sh\n"), 0o700))
	link := filepath.Join(home, "bin", "opencode")
	require.NoError(t, os.MkdirAll(filepath.Dir(link), 0o700))
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	t.Setenv("PATH", filepath.Dir(link))
	d, err := openCodeHarness{}.Detect(t.Context(), lookPathExecer{}, "")
	require.NoError(t, err)
	require.Equal(t, []Install{cli(link, "1.2.3")}, d.Installs)
}

// settingsPath is the settings file h uses in a fresh configuration directory.
func settingsPath(t *testing.T, h Harness) string {
	t.Helper()
	d, err := h.Detect(t.Context(), fakeExecer{}, t.TempDir())
	require.NoError(t, err)
	return d.Path
}

func setup(t *testing.T, h Harness, path string, routes []Route, s Selection) []*Plan {
	t.Helper()
	plans, err := h.Prepare(path, routes, PrepareOptions{Selection: s, Endpoint: testEndpoint})
	require.NoError(t, err)
	require.NoError(t, ApplyPlans(plans, testToken))
	return plans
}

func setupGitContext(t *testing.T, h Harness, path string) []*Plan {
	t.Helper()
	plans, err := h.Prepare(path, testRoutes(), PrepareOptions{Endpoint: testEndpoint, GitContext: true})
	require.NoError(t, err)
	require.NoError(t, ApplyPlans(plans, testToken))
	return plans
}

func teardown(t *testing.T, h Harness, path string) []*Plan {
	t.Helper()
	plans, err := h.Teardown(path)
	require.NoError(t, err)
	require.NoError(t, ApplyPlans(plans, ""))
	return plans
}

func load(t *testing.T, path string) map[string]any {
	t.Helper()
	_, d, err := readConfig(path)
	require.NoError(t, err)
	return d
}

func save(t *testing.T, path string, d map[string]any) {
	t.Helper()
	b, err := encodeConfig(path, d)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, b, 0o600))
}

func TestSetupRefreshAndTeardownKeepUnrelatedSettings(t *testing.T) {
	for _, h := range All() {
		t.Run(h.Name(), func(t *testing.T) {
			path := settingsPath(t, h)
			save(t, path, map[string]any{"model": "original-model", "theme": "dark"})
			for i := range 10 {
				if i == 3 {
					d := load(t, path)
					d["theme"] = "light"
					d["user-added"] = "keep"
					save(t, path, d)
				}
				setup(t, h, path, testRoutes(), Selection{Primary: testRoutes()[i%2].Name})
			}
			plans, err := h.Prepare(path, testRoutes(), PrepareOptions{Selection: Selection{Primary: "acme/background"}, Endpoint: testEndpoint})
			require.NoError(t, err)
			require.NoError(t, ApplyPlans(plans, testToken))
			for _, p := range plans {
				require.False(t, p.Changed, "repeating setup changes nothing")
			}
			status, err := h.Inspect(Detection{Name: h.Name(), Path: path})
			require.NoError(t, err)
			require.Equal(t, StateConfigured, status.State)
			require.Equal(t, testRoutes()[1].Name, status.DefaultRoute)
			require.Len(t, status.Routes, 4)

			teardown(t, h, path)
			want := map[string]any{"theme": "light", "user-added": "keep"}
			if h.Name() == Pi {
				// Pi's default model is in settings.json, so setup leaves this file's model alone.
				want["model"] = "original-model"
			}
			require.Equal(t, want, load(t, path))
			for _, p := range teardown(t, h, path) {
				require.False(t, p.Changed, "repeating teardown changes nothing")
			}
			status, err = h.Inspect(Detection{Name: h.Name(), Path: path})
			require.NoError(t, err)
			require.Equal(t, StateNotConfigured, status.State)
			require.NoFileExists(t, catalogPath(path))
		})
	}
}

func TestCredential(t *testing.T) {
	for _, h := range All() {
		t.Run(h.Name(), func(t *testing.T) {
			path := settingsPath(t, h)
			token, err := h.Credential(path)
			require.NoError(t, err)
			require.Empty(t, token, "no settings file")
			setup(t, h, path, testRoutes(), Selection{})
			token, err = h.Credential(path)
			require.NoError(t, err)
			require.Equal(t, testToken, token)
			teardown(t, h, path)
			token, err = h.Credential(path)
			require.NoError(t, err)
			require.Empty(t, token)
		})
	}
}

func TestHelperCredential(t *testing.T) {
	for _, h := range []Harness{claudeHarness{}, codexHarness{}, piHarness{}} {
		t.Run(h.Name(), func(t *testing.T) {
			path := settingsPath(t, h)
			setup(t, h, path, testRoutes(), Selection{})
			cli := &Helper{Path: "/opt/my tools/baseten", Profile: "alice"}
			plans, err := h.Prepare(path, testRoutes(), PrepareOptions{Endpoint: testEndpoint, Helper: cli})
			require.NoError(t, err)
			require.NoError(t, ApplyPlans(plans, ""))
			raw, err := os.ReadFile(path)
			require.NoError(t, err)
			require.NotContains(t, string(raw), testToken, "the static key is dropped")
			require.Contains(t, string(raw), cli.Path)
			require.Contains(t, string(raw), "--profile")
			token, err := h.Credential(path)
			require.NoError(t, err)
			require.Empty(t, token, "a helper command is not a key")
			teardown(t, h, path)
			raw, err = os.ReadFile(path)
			require.NoError(t, err)
			require.NotContains(t, string(raw), cli.Path)
		})
	}
}

func TestRefreshWithFewerRoutes(t *testing.T) {
	for _, h := range All() {
		t.Run(h.Name(), func(t *testing.T) {
			path := settingsPath(t, h)
			setup(t, h, path, testRoutes(), Selection{})
			setup(t, h, path, testRoutes()[:2], Selection{})
			status, err := h.Inspect(Detection{Name: h.Name(), Path: path})
			require.NoError(t, err)
			expected := []Route{}
			for _, r := range testRoutes()[:2] {
				expected = append(expected, Route{Name: r.Name, DisplayName: r.DisplayName})
			}
			require.ElementsMatch(t, expected, status.Routes)
		})
	}
}

func TestTeardownUnconfiguredIsNoop(t *testing.T) {
	for _, h := range All() {
		for _, existing := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/existing=%t", h.Name(), existing), func(t *testing.T) {
				path := settingsPath(t, h)
				var before []byte
				if existing {
					save(t, path, map[string]any{"model": "user-model", "env": map[string]any{"ANTHROPIC_BASE_URL": "https://other.example"}})
					var err error
					before, err = os.ReadFile(path)
					require.NoError(t, err)
				}
				for _, p := range teardown(t, h, path) {
					require.False(t, p.Changed)
					require.False(t, p.Managed)
				}
				if !existing {
					require.NoFileExists(t, path)
					return
				}
				after, err := os.ReadFile(path)
				require.NoError(t, err)
				require.Equal(t, before, after)
			})
		}
	}
}

func TestTeardownPreservesAnotherProviderSelection(t *testing.T) {
	for _, h := range []Harness{codexHarness{}, openCodeHarness{}} {
		t.Run(h.Name(), func(t *testing.T) {
			path := settingsPath(t, h)
			setup(t, h, path, testRoutes(), Selection{})
			d := load(t, path)
			d["model"] = "other/user-model"
			if h.Name() == Codex {
				require.Equal(t, "api", d["forced_login_method"])
				d["model_provider"] = "other"
				d["review_model"] = "other/reviewer"
				d["model_catalog_json"] = "/user/custom-catalog.json"
				require.NoError(t, put(d, []string{"model_providers", "other"}, value{Exists: true, Data: map[string]any{"name": "Other"}}))
			} else {
				d["small_model"] = "other/small"
				require.NoError(t, put(d, []string{"provider", "other"}, value{Exists: true, Data: map[string]any{"name": "Other"}}))
				require.NoError(t, put(d, []string{"agent", "general", "model"}, value{Exists: true, Data: "other/subagent"}))
			}
			save(t, path, d)
			status, err := h.Inspect(Detection{Name: h.Name(), Path: path})
			require.NoError(t, err)
			require.Equal(t, StateInactive, status.State)
			teardown(t, h, path)
			providers := "provider"
			if h.Name() == Codex {
				providers = "model_providers"
			}
			require.NoError(t, put(d, []string{providers, providerID}, value{}))
			delete(d, "forced_login_method")
			require.Equal(t, d, load(t, path))
		})
	}
}

func TestOptionalSubagentCleanup(t *testing.T) {
	for _, h := range []Harness{claudeHarness{}, openCodeHarness{}} {
		for _, explicit := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/explicit=%t", h.Name(), explicit), func(t *testing.T) {
				path := settingsPath(t, h)
				key := claudeSubagentPath
				if h.Name() == OpenCode {
					key = openCodeV1SubagentPaths[0]
				}
				d := map[string]any{}
				require.NoError(t, put(d, key, value{Exists: true, Data: "user-subagent"}))
				save(t, path, d)
				selection := Selection{}
				if explicit {
					selection.Subagent = "acme/subagent"
				}
				setup(t, h, path, testRoutes(), selection)
				// A refresh without the flag drops the subagent's route.
				setup(t, h, path, testRoutes()[:2], Selection{})
				if explicit {
					require.False(t, get(load(t, path), key).Exists, "a retired subagent route is cleared on refresh")
				}
				teardown(t, h, path)
				if explicit {
					require.False(t, get(load(t, path), key).Exists)
				} else {
					require.Equal(t, "user-subagent", get(load(t, path), key).Data)
				}
			})
		}
	}
}

func TestBackgroundRouteDefaultAndOverride(t *testing.T) {
	for _, background := range []string{"", "acme/background"} {
		want := background
		if want == "" {
			want = defaultBackgroundRoute
		}
		t.Run(ClaudeCode+"/"+want, func(t *testing.T) {
			path := settingsPath(t, claudeHarness{})
			setup(t, claudeHarness{}, path, testRoutes(), Selection{Background: background})
			d := load(t, path)
			require.Equal(t, want, get(d, []string{"env", "ANTHROPIC_DEFAULT_HAIKU_MODEL"}).Data)
			require.Equal(t, want, get(d, []string{"env", "ANTHROPIC_SMALL_FAST_MODEL"}).Data)
			require.Contains(t, d["availableModels"], want)
		})
		t.Run(OpenCode+"/"+want, func(t *testing.T) {
			path := settingsPath(t, openCodeHarness{})
			setup(t, openCodeHarness{}, path, testRoutes(), Selection{Background: background})
			d := load(t, path)
			require.Equal(t, providerID+"/"+want, d["small_model"])
			require.True(t, get(d, []string{"provider", providerID, "models", want}).Exists)
		})
	}
}

func TestClaudeAnthropicModelEnvIsManaged(t *testing.T) {
	path := settingsPath(t, claudeHarness{})
	setup(t, claudeHarness{}, path, testRoutes(), Selection{Primary: "acme/primary"})
	d := load(t, path)
	require.Equal(t, d["model"], get(d, []string{"env", "ANTHROPIC_MODEL"}).Data)
	status, err := claudeHarness{}.Inspect(Detection{Name: ClaudeCode, Path: path})
	require.NoError(t, err)
	require.Contains(t, status.Managed, "env.ANTHROPIC_MODEL")
	teardown(t, claudeHarness{}, path)
	require.False(t, get(load(t, path), []string{"env", "ANTHROPIC_MODEL"}).Exists)
}

func TestClaudeSessionStartHookIsInstalledOnce(t *testing.T) {
	h := claudeHarness{}
	path := settingsPath(t, h)
	save(t, path, map[string]any{"theme": "dark"})
	ours := map[string]any{"hooks": []any{map[string]any{"type": "command", "command": claudeSessionStartCommand}}}
	setup(t, h, path, testRoutes(), Selection{})
	require.NotContains(t, load(t, path), "hooks")
	setupGitContext(t, h, path)
	require.Equal(t, []any{ours}, get(load(t, path), claudeSessionStartHooksPath).Data)
	before, err := os.ReadFile(path)
	require.NoError(t, err)
	for _, p := range setupGitContext(t, h, path) {
		require.False(t, p.Changed, "repeating setup changes nothing")
	}
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, string(before), string(after))
	status, err := h.Inspect(Detection{Name: ClaudeCode, Path: path})
	require.NoError(t, err)
	require.Contains(t, status.Managed, pathKey(claudeSessionStartHooksPath))
	setup(t, h, path, testRoutes(), Selection{})
	require.NotContains(t, load(t, path), "hooks")
	setupGitContext(t, h, path)
	teardown(t, h, path)
	require.Equal(t, map[string]any{"theme": "dark"}, load(t, path))
}

func TestClaudeSessionStartHookKeepsUserHooks(t *testing.T) {
	h := claudeHarness{}
	path := settingsPath(t, h)
	user := []any{
		map[string]any{"matcher": "startup", "hooks": []any{map[string]any{"type": "command", "command": "echo hi"}}},
		map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "date"}}},
	}
	save(t, path, map[string]any{"hooks": map[string]any{"SessionStart": user}})
	ours := map[string]any{"hooks": []any{map[string]any{"type": "command", "command": claudeSessionStartCommand}}}
	for range 2 {
		plans := setupGitContext(t, h, path)
		require.Empty(t, plans[0].Replaced)
		require.Equal(t, append(slices.Clone(user), ours), get(load(t, path), claudeSessionStartHooksPath).Data)
	}
	setup(t, h, path, testRoutes(), Selection{})
	require.Equal(t, user, get(load(t, path), claudeSessionStartHooksPath).Data)
	teardown(t, h, path)
	require.Equal(t, map[string]any{"hooks": map[string]any{"SessionStart": user}}, load(t, path))
}

func TestClaudeSessionStartHookPrintsGitContext(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh is not on PATH")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	run := func(dir string, args ...string) string {
		t.Helper()
		command := exec.Command(args[0], args[1:]...)
		command.Dir = dir
		out, err := command.Output()
		require.NoError(t, err)
		return string(out)
	}
	require.Empty(t, run(t.TempDir(), "sh", "-c", claudeSessionStartCommand))
	repo := t.TempDir()
	run(repo, "git", "init")
	require.Empty(t, run(filepath.Join(repo, ".git"), "sh", "-c", claudeSessionStartCommand))
	branch := strings.TrimSpace(run(repo, "git", "branch", "--show-current"))
	require.Equal(t, "repo= branch="+branch+" commit=\n", run(repo, "sh", "-c", claudeSessionStartCommand))
	run(repo, "git", "-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false", "commit", "--allow-empty", "-m", "init")
	run(repo, "git", "remote", "add", "origin", "https://user:secret@example.com/org/repo.git")
	commit := strings.TrimSpace(run(repo, "git", "rev-parse", "HEAD"))
	require.Equal(t, "repo=https://example.com/org/repo.git branch="+branch+" commit="+commit+"\n", run(repo, "sh", "-c", claudeSessionStartCommand))
}

func TestOpenCodeGitContextPluginIsOptIn(t *testing.T) {
	h := openCodeHarness{}
	path := settingsPath(t, h)
	save(t, path, map[string]any{"theme": "dark"})
	plugin := openCodeGitContextPath(path)
	mine := filepath.Join(filepath.Dir(plugin), "mine.js")
	require.NoError(t, os.MkdirAll(filepath.Dir(mine), 0o700))
	require.NoError(t, os.WriteFile(mine, []byte("export default {}\n"), 0o600))
	setup(t, h, path, testRoutes(), Selection{})
	require.NoFileExists(t, plugin)
	plans, err := h.Prepare(path, testRoutes(), PrepareOptions{Endpoint: testEndpoint, NoOpenCodeServedModel: true, GitContext: true})
	require.NoError(t, err)
	require.NoError(t, ApplyPlans(plans, testToken))
	written, err := os.ReadFile(plugin)
	require.NoError(t, err)
	require.True(t, gridSetupSealed(written))
	require.True(t, strings.HasPrefix(string(written), string(openCodeGitContextSource)))
	for _, p := range setupGitContext(t, h, path) {
		require.False(t, p.Changed, "repeating setup changes nothing")
	}
	setup(t, h, path, testRoutes(), Selection{})
	require.NoFileExists(t, plugin)
	require.FileExists(t, mine)
	setupGitContext(t, h, path)
	teardown(t, h, path)
	require.NoFileExists(t, plugin)
	require.FileExists(t, mine)
	require.Equal(t, map[string]any{"theme": "dark"}, load(t, path))
}

func TestOpenCodeGitContextPluginKeepsUserFile(t *testing.T) {
	h := openCodeHarness{}
	path := settingsPath(t, h)
	plugin := openCodeGitContextPath(path)
	require.NoError(t, os.MkdirAll(filepath.Dir(plugin), 0o700))
	require.NoError(t, os.WriteFile(plugin, []byte("export default {}\n"), 0o600))
	plans := setupGitContext(t, h, path)
	require.True(t, slices.ContainsFunc(plans, func(p *Plan) bool {
		return slices.ContainsFunc(p.Notes, func(note string) bool { return strings.Contains(note, plugin+" left unchanged") })
	}))
	teardown(t, h, path)
	written, err := os.ReadFile(plugin)
	require.NoError(t, err)
	require.Equal(t, "export default {}\n", string(written))
}

func TestGitContextSanitizesOriginURL(t *testing.T) {
	for _, tool := range []string{"sh", "git", "node"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " is not on PATH")
		}
	}
	dir := t.TempDir()
	plugin := filepath.Join(dir, "plugin.mjs")
	require.NoError(t, os.WriteFile(plugin, openCodeGitContextSource, 0o600))
	extension := filepath.Join(dir, "extension.mts")
	require.NoError(t, os.WriteFile(extension, piGitContextSource, 0o600))
	openCodeDriver := `const { pathToFileURL } = await import("node:url"); const { default: plugin } = await import(pathToFileURL(process.argv[1]).href); const hooks = await plugin.server({ directory: process.cwd() }); const out = { system: [] }; await hooks["experimental.chat.system.transform"]({ sessionID: "s" }, out); process.stdout.write(out.system.map((line) => line + "\n").join(""))`
	piDriver := `const { pathToFileURL } = await import("node:url"); const { execFile } = await import("node:child_process"); const { default: extension } = await import(pathToFileURL(process.argv[1]).href); const handlers = {}; extension({ on: (event, fn) => { handlers[event] = fn }, exec: (cmd, args, opts) => new Promise((resolve) => execFile(cmd, args, opts, (error, stdout) => resolve({ code: error ? 1 : 0, stdout: String(stdout) }))) }); await handlers.session_start({}, { cwd: process.cwd() }); const event = { systemPromptOptions: { sections: {} } }; await handlers.before_agent_start(event); const line = event.systemPromptOptions.sections.baseten_git_context; if (line) process.stdout.write(line + "\n")`
	run := func(t *testing.T, dir string, args ...string) string {
		t.Helper()
		command := exec.Command(args[0], args[1:]...)
		command.Dir = dir
		out, err := command.Output()
		require.NoError(t, err)
		return string(out)
	}
	repo := t.TempDir()
	run(t, repo, "git", "init", "-q")
	branch := strings.TrimSpace(run(t, repo, "git", "branch", "--show-current"))
	stripTypes := exec.Command("node", "--experimental-strip-types", "-e", "").Run() == nil
	vectors := [][2]string{
		{"https://user:secret@example.com/org/repo.git", "https://example.com/org/repo.git"},
		{"https://token@github.com/org/repo", "https://github.com/org/repo"},
		{"https://git@github.com/org/repo.git", "https://github.com/org/repo.git"},
		{"https://alice%40example.com:secret%3Atoken@github.com/org/repo.git", "https://github.com/org/repo.git"},
		{"https://a@b@example.com/org/repo.git", "https://example.com/org/repo.git"},
		{"ssh://git@github.com/org/repo.git", "ssh://git@github.com/org/repo.git"},
		{"ssh://git:secret@github.com/org/repo.git", "ssh://git@github.com/org/repo.git"},
		{"ssh://bob@host.example:2222/up.git", "ssh://host.example:2222/up.git"},
		{"git+ssh://git@github.com/org/repo.git", "git+ssh://git@github.com/org/repo.git"},
		{"ssh+git://git@github.com/org/repo.git", "ssh+git://git@github.com/org/repo.git"},
		{"SSH://git@github.com/org/repo.git", "SSH://github.com/org/repo.git"},
		{"ssh://GIT@github.com/org/repo.git", "ssh://github.com/org/repo.git"},
		{"git@github.com:org/gh.git", "git@github.com:org/gh.git"},
		{"alice@host.example:org/scp.git", "host.example:org/scp.git"},
		{"GIT@github.com:org/repo.git", "github.com:org/repo.git"},
		{"git:secret@github.com:org/repo.git", "git@github.com:org/repo.git"},
		{"alice:pw@github.com:org/repo.git", "github.com:org/repo.git"},
		{"@host:path", "host:path"},
		{"alice@[2001:db8::1]:org/repo.git", "[2001:db8::1]:org/repo.git"},
		{"git@[2001:db8::1]:org/repo.git", "git@[2001:db8::1]:org/repo.git"},
		{"host.example:org/noUser.git", "host.example:org/noUser.git"},
		{"/srv/git/repo.git", "/srv/git/repo.git"},
		{"file:///srv/git/repo.git", "file:///srv/git/repo.git"},
		{"file://alice:secret@localhost/repo.git", "file://localhost/repo.git"},
		{"https://example.com/org/repo.git?token=abc#frag", "https://example.com/org/repo.git"},
		{"https://example.com/org/repo.git#frag", "https://example.com/org/repo.git"},
		{"https://example.com/org/repo.git?token=abc", "https://example.com/org/repo.git"},
		{"https://alice:pw@example.com/org/repo.git?token=abc#frag", "https://example.com/org/repo.git"},
		{"https://example.com?ref=a@b", "https://example.com"},
		{"http://[::1]:8080/org/repo.git", "http://[::1]:8080/org/repo.git"},
		{"hg::https://alice:secret@example.invalid/org/repo.git", "hg::https://example.invalid/org/repo.git"},
		{"hg::https://alice:secret@example.invalid/org/repo.git?x=1", "hg::https://example.invalid/org/repo.git"},
		{"remote-hg::git@github.com:org/repo.git", "remote-hg::git@github.com:org/repo.git"},
		{"ext::ssh alice:secret-token@example.com git-upload-pack /repo", ""},
		{"alice@host.example", "alice@host.example"},
		{"C:\\srv\\repo.git", "C:\\srv\\repo.git"},
	}
	for _, column := range []struct {
		name    string
		command []string
	}{
		{"claude", []string{"sh", "-c", claudeSessionStartCommand}},
		{"opencode", []string{"node", "--input-type=module", "-e", openCodeDriver, plugin}},
		{"pi", []string{"node", "--experimental-strip-types", "--input-type=module", "-e", piDriver, extension}},
	} {
		t.Run(column.name, func(t *testing.T) {
			if column.name == "pi" && !stripTypes {
				t.Skip("node cannot strip TypeScript types")
			}
			require.Empty(t, run(t, t.TempDir(), column.command...))
			for _, v := range vectors {
				run(t, repo, "git", "config", "remote.origin.url", v[0])
				require.Equal(t, "repo="+v[1]+" branch="+branch+" commit=\n", run(t, repo, column.command...), v[0])
			}
		})
	}
}

func TestUnsupportedSelectionsAreRejected(t *testing.T) {
	for _, tc := range []struct {
		h Harness
		s Selection
	}{
		{codexHarness{}, Selection{Background: "acme/background"}},
		{codexHarness{}, Selection{Subagent: "acme/subagent"}},
		{codexHarness{}, Selection{Fallback: "acme/fallback"}},
		{openCodeHarness{}, Selection{Fallback: "acme/fallback"}},
		{piHarness{}, Selection{Background: "acme/background"}},
		{piHarness{}, Selection{Subagent: "acme/subagent"}},
		{piHarness{}, Selection{Fallback: "acme/fallback"}},
	} {
		t.Run(fmt.Sprintf("%s/%+v", tc.h.Name(), tc.s), func(t *testing.T) {
			_, err := tc.h.Prepare(settingsPath(t, tc.h), testRoutes(), PrepareOptions{Selection: tc.s, Endpoint: testEndpoint})
			require.ErrorContains(t, err, "supported only")
		})
	}
	_, err := claudeHarness{}.Prepare(settingsPath(t, claudeHarness{}), testRoutes(), PrepareOptions{Selection: Selection{Primary: "acme/missing"}, Endpoint: testEndpoint})
	require.ErrorContains(t, err, "not one of the team's routes")
}

func TestCodexOrphanedCatalog(t *testing.T) {
	h := codexHarness{}
	path := settingsPath(t, h)
	plans, err := h.Prepare(path, testRoutes(), PrepareOptions{Endpoint: testEndpoint})
	require.NoError(t, err)
	// Only the catalog is written, as when setup is interrupted.
	require.NoError(t, ApplyPlans(plans[:1], testToken))
	status, err := h.Inspect(Detection{Name: Codex, Path: path})
	require.NoError(t, err)
	require.Equal(t, StateIncomplete, status.State)
	teardown(t, h, path)
	require.NoFileExists(t, catalogPath(path))
}

func TestCodexPreservesInstructionsAndAgents(t *testing.T) {
	h := codexHarness{}
	path := settingsPath(t, h)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte("model_instructions_file = \"custom.md\"\n[agents.reviewer]\nconfig_file = \"reviewer.toml\"\n"), 0o644))
	setup(t, h, path, testRoutes(), Selection{})
	d := load(t, path)
	require.Equal(t, "custom.md", d["model_instructions_file"])
	require.Equal(t, "reviewer.toml", get(d, []string{"agents", "reviewer", "config_file"}).Data)
	model := load(t, catalogPath(path))["models"].([]any)[0].(map[string]any)
	require.Equal(t, "", model["base_instructions"])
}

func TestOpenCodeJSONC(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "opencode.jsonc")
	require.NoError(t, os.WriteFile(path, []byte("{\n // comment\n \"theme\": \"dark\",\n}\n"), 0o644))
	d, err := openCodeHarness{}.Detect(t.Context(), fakeExecer{}, dir)
	require.NoError(t, err)
	require.Equal(t, path, d.Path, "opencode.jsonc is preferred")
	setup(t, openCodeHarness{}, path, testRoutes(), Selection{})
	require.Equal(t, "dark", load(t, path)["theme"])
	teardown(t, openCodeHarness{}, path)
	require.Equal(t, map[string]any{"theme": "dark"}, load(t, path))

	invalid := []byte("{ // comment\n invalid }")
	require.NoError(t, os.WriteFile(path, invalid, 0o644))
	_, err = openCodeHarness{}.Prepare(path, testRoutes(), PrepareOptions{Endpoint: testEndpoint})
	require.ErrorContains(t, err, "invalid settings JSON")
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, invalid, data)
}

func TestOpenCodeFirstPartyRoutesUseNativeAPIs(t *testing.T) {
	path := settingsPath(t, openCodeHarness{})
	routes := testRoutes()
	routes[0].TargetAPI = TargetAPIMessages
	routes[1].TargetAPI = TargetAPIResponses
	routes[2].TargetAPI = TargetAPIChatCompletions
	routes[3].TargetAPI, routes[3].UpstreamModel = TargetAPIMessages, "claude-opus-4-5"
	for i := range routes {
		routes[i].ReasoningLevels = []string{"none", "high"}
	}
	setup(t, openCodeHarness{}, path, routes, Selection{})
	data := load(t, path)
	require.Equal(t, "@ai-sdk/openai-compatible", get(data, []string{"provider", providerID, "npm"}).Data)
	require.Equal(t, "Bearer "+testToken, get(data, openCodeV1BearerPath).Data, "@ai-sdk/anthropic sends apiKey only as x-api-key")
	models := get(data, []string{"provider", providerID, "models"}).Data.(map[string]any)
	require.Equal(t, map[string]any{"npm": "@ai-sdk/anthropic"}, models["acme/primary"].(map[string]any)["provider"])
	require.Equal(t, map[string]any{"npm": "@ai-sdk/openai"}, models["acme/background"].(map[string]any)["provider"])
	require.NotContains(t, models["acme/subagent"], "provider")
	require.Equal(t, map[string]any{"high": map[string]any{"thinking": map[string]any{"type": "adaptive", "display": "summarized"}, "effort": "high"}}, models["acme/primary"].(map[string]any)["variants"])
	for _, name := range []string{"acme/background", "acme/subagent", "acme/fallback"} {
		require.Equal(t, map[string]any{"none": map[string]any{"reasoningEffort": "none"}, "high": map[string]any{"reasoningEffort": "high"}}, models[name].(map[string]any)["variants"], name)
	}
}

// openCodeV2File is a settings file the user has migrated to OpenCode 2's
// native format, with nothing of Baseten's in it.
func openCodeV2File() map[string]any {
	return map[string]any{
		"theme":     "dark",
		"model":     "anthropic/claude-sonnet-4-5",
		"providers": map[string]any{"anthropic": map[string]any{"settings": map[string]any{"baseURL": "https://proxy.example"}}},
		"agents":    map[string]any{"reviewer": map[string]any{"mode": "subagent"}},
	}
}

func TestOpenCodeFormatFollowsTheFile(t *testing.T) {
	for _, tc := range []struct {
		name string
		file map[string]any
		want openCodeFormat
	}{
		{"no file", nil, openCodeV1},
		{"empty", map[string]any{}, openCodeV1},
		{"shared keys only", map[string]any{"model": "x/y", "theme": "dark", "mcp": map[string]any{}}, openCodeV1},
		{"v1 keys", map[string]any{"provider": map[string]any{}, "agent": map[string]any{}, "small_model": "x/y", "permission": map[string]any{}}, openCodeV1},
		{"providers", map[string]any{"providers": map[string]any{}}, openCodeV2},
		{"agents", map[string]any{"agents": map[string]any{}}, openCodeV2},
		{"permissions", map[string]any{"permissions": []any{}}, openCodeV2},
		{"plugins", map[string]any{"plugins": []any{}}, openCodeV2},
		{"commands", map[string]any{"commands": map[string]any{}}, openCodeV2},
		{"mixed", map[string]any{"provider": map[string]any{}, "providers": map[string]any{}}, openCodeV2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := settingsPath(t, openCodeHarness{})
			if tc.file != nil {
				save(t, path, tc.file)
			}
			setup(t, openCodeHarness{}, path, testRoutes(), Selection{})
			d := load(t, path)
			require.Equal(t, tc.want == openCodeV2, get(d, openCodeV2ProviderPath).Exists)
			require.Equal(t, tc.want == openCodeV1, get(d, openCodeV1ProviderPath).Exists)
			require.Equal(t, providerID+"/acme/primary", d["model"])
			token, err := openCodeHarness{}.Credential(path)
			require.NoError(t, err)
			require.Equal(t, testToken, token)
		})
	}
}

func TestOpenCodeV2Settings(t *testing.T) {
	path := settingsPath(t, openCodeHarness{})
	save(t, path, openCodeV2File())
	cacheRead := 0.3
	routes := testRoutes()
	routes[0].TargetAPI = TargetAPIMessages
	routes[0].ReasoningLevels = []string{"none", "low", "max"}
	routes[0].Cost = &Cost{Input: 3, Output: 15, CacheRead: &cacheRead, LongContext: &Cost{Input: 6, Output: 22.5}}
	routes[1].TargetAPI = TargetAPIResponses
	routes[3].ReasoningLevels = []string{"none"}
	plans := setup(t, openCodeHarness{}, path, routes, Selection{Subagent: "acme/subagent"})
	require.Equal(t, []string{"model"}, plans[0].Replaced)
	d := load(t, path)

	require.Equal(t, providerID+"/acme/primary", d["model"])
	require.Equal(t, providerID+"/"+defaultBackgroundRoute, get(d, openCodeV2BackgroundPath).Data)
	for _, key := range openCodeV2SubagentPaths {
		require.Equal(t, providerID+"/acme/subagent", get(d, key).Data)
	}
	require.Equal(t, "subagent", get(d, []string{"agents", "reviewer", "mode"}).Data, "the user's agents stay")
	for _, key := range []string{"provider", "small_model", "agent"} {
		require.NotContains(t, d, key, "no V1 keys in a V2 file")
	}

	provider := get(d, openCodeV2ProviderPath).Data.(map[string]any)
	require.Equal(t, "Baseten", provider["name"])
	require.Equal(t, "@opencode/ai/providers/openai-compatible", provider["package"])
	require.Equal(t, map[string]any{"baseURL": testEndpoint + "/v1", "apiKey": testToken}, provider["settings"])
	require.Equal(t, map[string]any{"Authorization": "Bearer " + testToken, clientHeader: OpenCode}, provider["headers"])
	require.NotContains(t, provider, "npm")
	require.NotContains(t, provider, "options")

	models := provider["models"].(map[string]any)
	require.Len(t, models, 5)
	primary := models["acme/primary"].(map[string]any)
	require.Equal(t, map[string]any{
		"name":         "Primary",
		"package":      "@opencode/ai/providers/anthropic",
		"limit":        map[string]any{"context": json.Number("128000"), "output": json.Number("4096")},
		"capabilities": map[string]any{"tools": true, "input": []any{"text"}, "output": []any{"text"}},
		"variants": []any{
			map[string]any{"id": "low", "settings": map[string]any{"thinking": map[string]any{"type": "adaptive", "display": "summarized"}, "effort": "low"}},
			map[string]any{"id": "xhigh", "settings": map[string]any{"thinking": map[string]any{"type": "adaptive", "display": "summarized"}, "effort": "xhigh"}},
		},
		"cost": []any{
			map[string]any{"input": json.Number("3"), "output": json.Number("15"), "cache": map[string]any{"read": json.Number("0.3")}},
			map[string]any{"tier": map[string]any{"type": "context", "size": json.Number("200000")}, "input": json.Number("6"), "output": json.Number("22.5")},
		},
	}, primary)
	require.Equal(t, "@opencode/ai/providers/openai", models["acme/background"].(map[string]any)["package"])
	subagent := models["acme/subagent"].(map[string]any)
	require.NotContains(t, subagent, "package")
	require.NotContains(t, subagent, "cost")
	require.Equal(t, []any{}, subagent["variants"])
	require.Equal(t, []any{map[string]any{"id": "none", "settings": map[string]any{"reasoningEffort": "none"}}}, models["acme/fallback"].(map[string]any)["variants"])
	for _, key := range []string{"tool_call", "modalities", "reasoning", "provider"} {
		require.NotContains(t, primary, key, "no V1 model fields in a V2 file")
	}

	status, err := openCodeHarness{}.Inspect(Detection{Name: OpenCode, Path: path})
	require.NoError(t, err)
	require.Equal(t, StateConfigured, status.State)
	require.Equal(t, "acme/primary", status.DefaultRoute)
	require.Equal(t, defaultBackgroundRoute, status.BackgroundRoute)
	require.Len(t, status.Routes, 4)
	require.Contains(t, status.Managed, "providers."+providerID)
	require.Contains(t, status.Managed, "agents.title.model")

	// A refresh is a no-op, and stays V2.
	for _, p := range setup(t, openCodeHarness{}, path, routes, Selection{Subagent: "acme/subagent"}) {
		require.False(t, p.Changed)
	}
	teardown(t, openCodeHarness{}, path)
	want := openCodeV2File()
	delete(want, "model")
	require.Equal(t, want, load(t, path))
}

func TestOpenCodeSetupAfterMigrationRetiresV1Settings(t *testing.T) {
	path := settingsPath(t, openCodeHarness{})
	setup(t, openCodeHarness{}, path, testRoutes(), Selection{Subagent: "acme/subagent", Background: "acme/background"})
	// The user migrates their own settings but leaves Baseten's block alone.
	d := load(t, path)
	d["agents"] = map[string]any{"reviewer": map[string]any{"mode": "subagent"}}
	require.NoError(t, put(d, []string{"agent", "mine", "model"}, value{Exists: true, Data: "other/model"}))
	save(t, path, d)

	plans := setup(t, openCodeHarness{}, path, testRoutes(), Selection{Background: "acme/background"})
	require.ElementsMatch(t, []string{"provider." + providerID, "small_model", "agent.general.model", "agent.explore.model"}, plans[0].Replaced)
	d = load(t, path)
	require.True(t, get(d, openCodeV2ProviderPath).Exists)
	require.False(t, get(d, openCodeV1ProviderPath).Exists)
	require.NotContains(t, d, "small_model")
	require.Equal(t, providerID+"/acme/background", get(d, openCodeV2BackgroundPath).Data)
	for _, key := range openCodeV1SubagentPaths {
		require.False(t, get(d, key).Exists, "retired V1 subagent settings")
	}
	for _, key := range openCodeV2SubagentPaths {
		require.False(t, get(d, key).Exists, "no --subagent-route-id on this run")
	}
	require.Equal(t, "other/model", get(d, []string{"agent", "mine", "model"}).Data, "the user's V1 agent stays")
	token, err := openCodeHarness{}.Credential(path)
	require.NoError(t, err)
	require.Equal(t, testToken, token)
}

func TestOpenCodeTeardownSweepsBothFormats(t *testing.T) {
	path := settingsPath(t, openCodeHarness{})
	d := map[string]any{
		"theme":       "dark",
		"model":       providerID + "/acme/primary",
		"small_model": providerID + "/acme/background",
		"provider":    map[string]any{providerID: map[string]any{"npm": "x"}, "other": map[string]any{"npm": "y"}},
		"providers":   map[string]any{providerID: map[string]any{"package": "x"}, "other": map[string]any{"package": "y"}},
		"agent": map[string]any{
			"general": map[string]any{"model": providerID + "/acme/subagent"},
			"explore": map[string]any{"model": "other/model"},
		},
		"agents": map[string]any{
			"title":   map[string]any{"model": providerID + "/acme/background"},
			"general": map[string]any{"model": providerID + "/acme/subagent", "mode": "subagent"},
			"explore": map[string]any{"model": "other/model"},
		},
	}
	save(t, path, d)
	status, err := openCodeHarness{}.Inspect(Detection{Name: OpenCode, Path: path})
	require.NoError(t, err)
	require.Equal(t, StateConfigured, status.State)
	require.Equal(t, "acme/background", status.BackgroundRoute, "the native format is preferred")
	teardown(t, openCodeHarness{}, path)
	require.Equal(t, map[string]any{
		"theme":     "dark",
		"provider":  map[string]any{"other": map[string]any{"npm": "y"}},
		"providers": map[string]any{"other": map[string]any{"package": "y"}},
		"agent":     map[string]any{"explore": map[string]any{"model": "other/model"}},
		"agents": map[string]any{
			"general": map[string]any{"mode": "subagent"},
			"explore": map[string]any{"model": "other/model"},
		},
	}, load(t, path))
}

func TestOpenCodeV2TeardownPreservesAnotherProviderSelection(t *testing.T) {
	path := settingsPath(t, openCodeHarness{})
	save(t, path, openCodeV2File())
	setup(t, openCodeHarness{}, path, testRoutes(), Selection{Subagent: "acme/subagent"})
	d := load(t, path)
	d["model"] = "other/user-model"
	require.NoError(t, put(d, openCodeV2BackgroundPath, value{Exists: true, Data: "other/small"}))
	require.NoError(t, put(d, openCodeV2SubagentPaths[0], value{Exists: true, Data: "other/subagent"}))
	save(t, path, d)
	status, err := openCodeHarness{}.Inspect(Detection{Name: OpenCode, Path: path})
	require.NoError(t, err)
	require.Equal(t, StateInactive, status.State)
	teardown(t, openCodeHarness{}, path)
	require.NoError(t, put(d, openCodeV2ProviderPath, value{}))
	require.NoError(t, put(d, openCodeV2SubagentPaths[1], value{}))
	require.Equal(t, d, load(t, path))
}

func TestOpenCodeV2OptionalSubagentCleanup(t *testing.T) {
	path := settingsPath(t, openCodeHarness{})
	save(t, path, map[string]any{"agents": map[string]any{"general": map[string]any{"model": "user-subagent"}}})
	setup(t, openCodeHarness{}, path, testRoutes(), Selection{Subagent: "acme/subagent"})
	// A refresh without the flag drops the subagent's route.
	setup(t, openCodeHarness{}, path, testRoutes()[:2], Selection{})
	require.False(t, get(load(t, path), openCodeV2SubagentPaths[0]).Exists, "a retired subagent route is cleared on refresh")
}

func TestOpenCodeDesktopDetection(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	path := filepath.Join(home, ".config", "opencode", "opencode.json")
	bundled := func(root, app string) string {
		return filepath.Join(root, app, "Contents", "Resources", "opencode-cli")
	}
	installed := filepath.Join(home, ".opencode", "bin", "opencode")
	desktopApp := bundled("/Applications", "OpenCode.app")
	one := func(binary string, goos string) desktopDetectionCase {
		return desktopDetectionCase{name: binary, binaries: map[string]any{binary: true}, want: []Install{desktop(binary, "1.2.3")}, goos: []string{goos}}
	}
	darwin := map[string]any{"opencode": "1.0.0", desktopApp: "2.0.0", installed: "2.0.1"}
	linux := map[string]any{"opencode": "1.0.0", linuxDesktopOpenCode: "2.0.0", installed: "2.0.1"}
	testDesktopDetection(t, openCodeHarness{}, path, []desktopDetectionCase{
		one(desktopApp, "darwin"),
		one(bundled("/Applications", "OpenCode Beta.app"), "darwin"),
		one(bundled(filepath.Join(home, "Applications"), "OpenCode.app"), "darwin"),
		one(linuxDesktopOpenCode, "linux"),
		{name: "installed by desktop", binaries: map[string]any{installed: true}, want: []Install{cli(installed, "1.2.3")}, goos: []string{"darwin", "linux"}},
		{name: "everything on darwin", binaries: darwin, want: []Install{cli(onPATH("opencode"), "1.0.0"), desktop(desktopApp, "2.0.0"), cli(installed, "2.0.1")}, goos: []string{"darwin"},
			elsewhere: []Install{cli(onPATH("opencode"), "1.0.0"), cli(installed, "2.0.1")}},
		{name: "everything on linux", binaries: linux, want: []Install{cli(onPATH("opencode"), "1.0.0"), desktop(linuxDesktopOpenCode, "2.0.0"), cli(installed, "2.0.1")}, goos: []string{"linux"},
			elsewhere: []Install{cli(onPATH("opencode"), "1.0.0"), cli(installed, "2.0.1")}},
		{name: "config alone is not an installation"},
	})
}

func TestPiFirstPartyRoutesUseNativeAPIs(t *testing.T) {
	path := settingsPath(t, piHarness{})
	routes := testRoutes()
	routes[0].TargetAPI = TargetAPIMessages
	routes[1].TargetAPI = TargetAPIResponses
	routes[2].TargetAPI = TargetAPIChatCompletions
	routes[3].TargetAPI, routes[3].Router = TargetAPIMessages, true
	setup(t, piHarness{}, path, routes, Selection{})
	provider := get(load(t, path), piProviderPath).Data.(map[string]any)
	require.Equal(t, "openai-completions", provider["api"])
	require.Equal(t, testEndpoint+"/v1", provider["baseUrl"])
	require.Equal(t, true, provider["authHeader"], "Pi sends apiKey to Anthropic routes only as x-api-key")
	require.NotContains(t, provider, "compat", "a provider's compat would reach every API")
	models := map[string]map[string]any{}
	for _, m := range provider["models"].([]any) {
		models[m.(map[string]any)["id"].(string)] = m.(map[string]any)
	}
	require.Equal(t, "anthropic-messages", models["acme/primary"]["api"])
	require.Equal(t, testEndpoint, models["acme/primary"]["baseUrl"], "Pi's Anthropic client appends /v1")
	require.Equal(t, true, get(models["acme/primary"], []string{"compat", "forceAdaptiveThinking"}).Data)
	require.NotContains(t, models["acme/primary"]["compat"], "sendSessionAffinityHeaders")
	require.Equal(t, map[string]any{"forceAdaptiveThinking": true, "sendSessionAffinityHeaders": true}, models["acme/fallback"]["compat"])
	require.Equal(t, "openai-responses", models["acme/background"]["api"])
	require.NotContains(t, models["acme/background"], "baseUrl")
	require.NotContains(t, models["acme/background"], "compat")
	require.NotContains(t, models["acme/subagent"], "api")
	require.Equal(t, "max_tokens", get(models["acme/subagent"], []string{"compat", "maxTokensField"}).Data)
}

func TestPiDefaultModel(t *testing.T) {
	h := piHarness{}
	path := settingsPath(t, h)
	settings := piSettingsPath(path)
	save(t, settings, map[string]any{"defaultProvider": "anthropic", "defaultModel": "claude-opus-5-5", "theme": "dark"})
	plans := setup(t, h, path, testRoutes(), Selection{Primary: "acme/background"})
	require.Equal(t, []string{"defaultProvider", "defaultModel"}, plans[1].Replaced)
	require.Equal(t, map[string]any{"defaultProvider": providerID, "defaultModel": "acme/background", "theme": "dark"}, load(t, settings))
	teardown(t, h, path)
	require.Equal(t, map[string]any{"theme": "dark"}, load(t, settings), "previous values are not restored")
	require.Equal(t, map[string]any{}, load(t, path))
}

func TestPiTeardownPreservesAnotherProviderSelection(t *testing.T) {
	h := piHarness{}
	path := settingsPath(t, h)
	setup(t, h, path, testRoutes(), Selection{})
	settings := map[string]any{"defaultProvider": "other", "defaultModel": "user-model"}
	save(t, piSettingsPath(path), settings)
	models := load(t, path)
	require.NoError(t, put(models, []string{"providers", "other"}, value{Exists: true, Data: map[string]any{"baseUrl": "https://other.example"}}))
	save(t, path, models)
	status, err := h.Inspect(Detection{Name: Pi, Path: path})
	require.NoError(t, err)
	require.Equal(t, StateInactive, status.State)
	teardown(t, h, path)
	require.NoError(t, put(models, piProviderPath, value{}))
	require.Equal(t, models, load(t, path))
	require.Equal(t, settings, load(t, piSettingsPath(path)))
}

func TestPiGitContextExtensionIsOptIn(t *testing.T) {
	h := piHarness{}
	path := settingsPath(t, h)
	target := piGitContextPath(path)
	setup(t, h, path, testRoutes(), Selection{})
	require.NoFileExists(t, target)
	plans, err := h.Prepare(path, testRoutes(), PrepareOptions{Endpoint: testEndpoint, NoPiServedModel: true, GitContext: true})
	require.NoError(t, err)
	require.NoError(t, ApplyPlans(plans, testToken))
	raw, err := os.ReadFile(target)
	require.NoError(t, err)
	require.Equal(t, string(piGitContextSource)+gridSetupSeal(piGitContextSource), string(raw))
	require.True(t, gridSetupSealed(raw))
	require.NoFileExists(t, piServedModelPath(path))
	for _, p := range setupGitContext(t, h, path) {
		require.False(t, p.Changed && p.Path == target, "repeating setup leaves the extension alone")
	}
	setup(t, h, path, testRoutes(), Selection{})
	require.NoFileExists(t, target)
	setupGitContext(t, h, path)
	teardown(t, h, path)
	require.NoFileExists(t, target)
}

func TestPiGitContextExtensionKeepsUserFile(t *testing.T) {
	h := piHarness{}
	path := settingsPath(t, h)
	target := piGitContextPath(path)
	require.NoError(t, os.MkdirAll(filepath.Dir(target), 0o700))
	user := "export default function () {}\n"
	require.NoError(t, os.WriteFile(target, []byte(user), 0o600))
	plans := setupGitContext(t, h, path)
	require.Contains(t, plans[len(plans)-1].Notes, "warning: existing "+target+" left unchanged; Pi sessions will not include the git repository, branch, and commit")
	teardown(t, h, path)
	raw, err := os.ReadFile(target)
	require.NoError(t, err)
	require.Equal(t, user, string(raw))
}

func TestPiIncomplete(t *testing.T) {
	h := piHarness{}
	path := settingsPath(t, h)
	plans, err := h.Prepare(path, testRoutes(), PrepareOptions{Endpoint: testEndpoint})
	require.NoError(t, err)
	// Only the default model is written, as when models.json is removed by hand.
	require.NoError(t, ApplyPlans(plans[1:], testToken))
	status, err := h.Inspect(Detection{Name: Pi, Path: path})
	require.NoError(t, err)
	require.Equal(t, StateIncomplete, status.State)
	teardown(t, h, path)
	require.Equal(t, map[string]any{}, load(t, piSettingsPath(path)))

	// A malformed settings.json is the user's to repair.
	require.NoError(t, os.WriteFile(piSettingsPath(path), []byte("{{ not valid"), 0o600))
	_, err = h.Inspect(Detection{Name: Pi, Path: path})
	require.ErrorContains(t, err, "invalid settings JSON")
}

func TestClientHeaderNamesHarness(t *testing.T) {
	paths := map[string][]string{
		Claude:   {"env", "ANTHROPIC_CUSTOM_HEADERS"},
		Codex:    {"model_providers", providerID, "http_headers", clientHeader},
		OpenCode: {"provider", providerID, "options", "headers", clientHeader},
		Pi:       {"providers", providerID, "headers", clientHeader},
	}
	for _, h := range All() {
		t.Run(h.Name(), func(t *testing.T) {
			path := settingsPath(t, h)
			setup(t, h, path, testRoutes(), Selection{})
			want := h.Name()
			if h.Name() == Claude {
				// Claude Code and Claude Desktop identify as claude-code on the wire.
				want = clientHeader + ": " + ClaudeCode
			}
			require.Equal(t, want, get(load(t, path), paths[h.Name()]).Data)
			if h.Name() == Claude {
				require.Equal(t, "1", get(load(t, path), []string{"env", "CLAUDE_CODE_GATEWAY_HINT_HEADERS"}).Data)
				require.Equal(t, "true", get(load(t, path), []string{"env", "ENABLE_TOOL_SEARCH"}).Data)
				teardown(t, h, path)
				require.False(t, get(load(t, path), []string{"env", "CLAUDE_CODE_GATEWAY_HINT_HEADERS"}).Exists, "teardown removes the gateway hint key")
				require.False(t, get(load(t, path), []string{"env", "ENABLE_TOOL_SEARCH"}).Exists, "teardown removes the tool search key")
			}
		})
	}
}

func TestSymlinkedSettingsStayLinked(t *testing.T) {
	for _, h := range All() {
		t.Run(h.Name(), func(t *testing.T) {
			link := settingsPath(t, h)
			target := filepath.Join(t.TempDir(), "dotfile"+filepath.Ext(link))
			save(t, target, map[string]any{"theme": "dark"})
			require.NoError(t, os.MkdirAll(filepath.Dir(link), 0o700))
			if err := os.Symlink(target, link); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
			setup(t, h, link, testRoutes(), Selection{})
			teardown(t, h, link)
			info, err := os.Lstat(link)
			require.NoError(t, err)
			require.NotZero(t, info.Mode()&os.ModeSymlink)
			require.Equal(t, map[string]any{"theme": "dark"}, load(t, target))
		})
	}
}

func TestDetect(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, env := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME", "XDG_CONFIG_HOME", "PI_CODING_AGENT_DIR"} {
		t.Setenv(env, "")
	}
	custom := t.TempDir()
	for _, tc := range []struct {
		h                  Harness
		env                string
		defaultPath, inEnv string
	}{
		{claudeHarness{}, "CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude", "settings.json"), filepath.Join(custom, "settings.json")},
		{codexHarness{}, "CODEX_HOME", filepath.Join(home, ".codex", "config.toml"), filepath.Join(custom, "config.toml")},
		{openCodeHarness{}, "XDG_CONFIG_HOME", filepath.Join(home, ".config", "opencode", "opencode.json"), filepath.Join(custom, "opencode", "opencode.json")},
		{piHarness{}, "PI_CODING_AGENT_DIR", filepath.Join(home, ".pi", "agent", "models.json"), filepath.Join(custom, "models.json")},
	} {
		t.Run(tc.h.Name(), func(t *testing.T) {
			d, err := tc.h.Detect(t.Context(), fakeExecer{}, "")
			require.NoError(t, err)
			binary := map[string]string{Claude: "claude", Codex: "codex", OpenCode: "opencode", Pi: "pi"}[tc.h.Name()]
			want := Detection{Name: tc.h.Name(), Path: tc.defaultPath, Installed: true,
				Installs: []Install{cli(filepath.Join(string(filepath.Separator), "fake", binary), "1.2.3")}}
			require.Equal(t, want, d)
			t.Setenv(tc.env, custom)
			d, err = tc.h.Detect(t.Context(), fakeExecer{missing: true}, "")
			require.NoError(t, err)
			require.Equal(t, Detection{Name: tc.h.Name(), Path: tc.inEnv}, d)
			dir := t.TempDir()
			d, err = tc.h.Detect(t.Context(), fakeExecer{}, dir)
			require.NoError(t, err)
			require.Equal(t, dir, filepath.Dir(d.Path), "--config-dir wins over the environment")
		})
	}
}

func TestLoadRecordsRenamesClaudeCode(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, SaveRecords(dir, RecordFile{Harnesses: map[string]Record{ClaudeCode: {TeamID: "team-a"}}}))
	file, err := LoadRecords(dir)
	require.NoError(t, err)
	require.Equal(t, map[string]Record{Claude: {TeamID: "team-a"}}, file.Harnesses)
}

func TestClaudeDesktopConfigFollowsClaudeCodeSettings(t *testing.T) {
	// The app's files are in its own library when the settings are in Claude
	// Code's default directory, else under the settings directory, so that
	// --config-dir keeps both apps' files together.
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	native := claudeDesktopConfig(filepath.Join(home, ".claude", "settings.json"))
	require.Equal(t, claudeDesktopConfigID+".json", filepath.Base(native))
	require.Contains(t, native, "Claude-3p")
	require.NotContains(t, native, ".claude")
	dir := t.TempDir()
	require.Equal(t, filepath.Join(dir, "Claude-3p", "configLibrary", claudeDesktopConfigID+".json"), claudeDesktopConfig(filepath.Join(dir, "settings.json")))
	for _, name := range []string{"claude-desktop-b0dgYBn", "claude-desktop-XP9WlBD"} {
		require.False(t, claudeDesktopDeniedID(name), name)
	}
	require.True(t, claudeDesktopDeniedID("claude-desktop-aGLMbcd"))
}

func TestClaudeDesktopFilesWithClaudeCodeSettings(t *testing.T) {
	h := claudeHarness{}
	path := settingsPath(t, h)
	opts := PrepareOptions{Endpoint: testEndpoint, Installs: []Install{{Kind: InstallDesktop, Binary: "/Applications/Claude.app/Contents/MacOS/Claude"}}}
	plans, err := h.Prepare(path, testRoutes(), opts)
	require.NoError(t, err)
	require.Len(t, plans, 4, "Claude Code's settings, the app config, the library's applied config, and the app's mode")
	require.NoError(t, ApplyPlans(plans, testToken))
	config := claudeDesktopConfig(path)
	data := load(t, config)
	require.Equal(t, ClaudeCode, get(data, claudeDesktopMarkerPath).Data)
	require.Equal(t, true, get(data, []string{"autoModeEnabled"}).Data, "Auto mode is offered in Cowork as well as Code")
	require.Equal(t, true, get(data, []string{"toolSearchEnabled"}).Data)
	models := data["inferenceModels"].([]any)
	require.Equal(t, "sonnet", models[0].(map[string]any)["anthropicFamilyTier"], "no listed background route, so the primary serves Auto mode")
	for _, m := range models[1:] {
		require.NotContains(t, m.(map[string]any), "anthropicFamilyTier")
	}
	require.Equal(t, testToken, data["inferenceGatewayApiKey"])
	require.Equal(t, claudeDesktopID(config), load(t, claudeDesktopMetaPath(config))["appliedId"])
	require.Equal(t, "3p", load(t, claudeDesktopAppConfigPath(config))["deploymentMode"])
	status, err := h.Inspect(Detection{Name: Claude, Path: path})
	require.NoError(t, err)
	require.Equal(t, StateConfigured, status.State)
	require.Contains(t, status.Managed, "inferenceModels")

	plans, err = h.Prepare(path, testRoutes(), opts)
	require.NoError(t, err)
	require.NoError(t, ApplyPlans(plans, testToken))
	for _, p := range plans {
		require.False(t, p.Changed, "repeating setup changes nothing: %s", p.Path)
	}

	// Without the app, setup leaves its files alone and teardown still removes them.
	plans, err = h.Prepare(path, testRoutes(), PrepareOptions{Endpoint: testEndpoint})
	require.NoError(t, err)
	require.Len(t, plans, 1)
	teardown(t, h, path)
	require.NoFileExists(t, config)
	require.NotContains(t, load(t, claudeDesktopMetaPath(config)), "appliedId")
	require.Equal(t, "1p", load(t, claudeDesktopAppConfigPath(config))["deploymentMode"])
	status, err = h.Inspect(Detection{Name: Claude, Path: path})
	require.NoError(t, err)
	require.Equal(t, StateNotConfigured, status.State)
}

func TestMalformedSettingsAreReported(t *testing.T) {
	for _, h := range All() {
		t.Run(h.Name(), func(t *testing.T) {
			path := settingsPath(t, h)
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
			require.NoError(t, os.WriteFile(path, []byte("{{ not valid"), 0o600))
			_, err := h.Inspect(Detection{Name: h.Name(), Path: path})
			require.ErrorContains(t, err, "invalid settings")
		})
	}
}

type codexDaemonExecer struct {
	fakeExecer
	outputs  map[string]string
	binaries map[string]bool
	env      []string
	path     string
}

func (e *codexDaemonExecer) LookPath(name string) (string, error) {
	if e.binaries == nil {
		return e.fakeExecer.LookPath(name)
	}
	if !e.binaries[name] {
		return "", exec.ErrNotFound
	}
	return filepath.Join(string(filepath.Separator), "fake", name), nil
}

func (e *codexDaemonExecer) Exec(command *exec.Cmd) error {
	e.env, e.path = command.Env, command.Path
	out, ok := e.outputs[strings.Join(command.Args[1:], " ")]
	if !ok {
		return errors.New("exit status 1")
	}
	_, err := fmt.Fprint(command.Stdout, out)
	return err
}

func TestCodexDaemonSocket(t *testing.T) {
	dir := t.TempDir()
	running := map[string]string{"app-server daemon version": `{"status":"running","socketPath":"/tmp/codex.sock"}`}
	cases := map[string]struct {
		outputs  map[string]string
		binaries map[string]bool
		want     string
		bin      string
	}{
		"running":   {running, nil, "/tmp/codex.sock", filepath.Join(string(filepath.Separator), "fake", "codex")},
		"stopped":   {map[string]string{"app-server daemon version": `{"status":"stopped"}`}, nil, "", ""},
		"not json":  {map[string]string{"app-server daemon version": "1.2.3"}, nil, "", ""},
		"failed":    {nil, nil, "", ""},
		"no binary": {running, map[string]bool{}, "", ""},
	}
	if desktop := desktopCodexBinaries(); len(desktop) > 0 {
		cases["desktop binary"] = struct {
			outputs  map[string]string
			binaries map[string]bool
			want     string
			bin      string
		}{running, map[string]bool{desktop[0]: true}, "/tmp/codex.sock", filepath.Join(string(filepath.Separator), "fake", desktop[0])}
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			e := &codexDaemonExecer{outputs: tc.outputs, binaries: tc.binaries}
			require.Equal(t, tc.want, CodexDaemonSocket(t.Context(), e, dir))
			if tc.bin != "" {
				require.Equal(t, tc.bin, e.path)
				require.Contains(t, e.env, "CODEX_HOME="+dir)
			}
		})
	}
	require.Empty(t, CodexDaemonSocket(t.Context(), fakeExecer{missing: true}, dir))
}

func TestCodexSocketClients(t *testing.T) {
	const socket = "/tmp/codex-daemon-1000/abc"
	for name, tc := range map[string]struct {
		lsof    string
		clients int
		err     string
	}{
		"linux one client":   {"p1\nf31\nn" + socket + " type=STREAM\nf14\nn" + socket + " type=STREAM\nf7\nntype=STREAM\n", 1, ""},
		"darwin two clients": {"p1\nf3\nn" + socket + "\nf4\nn->0x3d9b71d2b764900d\nf5\nn" + socket + "\nf7\nn" + socket + "\n", 2, ""},
		"listener only":      {"p1\nf31\nn" + socket + " type=STREAM\n", 0, ""},
		"other socket":       {"p1\nf3\nn/tmp/other.sock\n", 0, "not listed"},
		"no output":          {"", 0, "not listed"},
	} {
		t.Run(name, func(t *testing.T) {
			clients, err := codexSocketClients(tc.lsof, socket)
			if tc.err != "" {
				require.ErrorContains(t, err, tc.err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.clients, clients)
		})
	}
}

func TestCodexDaemonClientsResolvesSocket(t *testing.T) {
	link := filepath.Join(t.TempDir(), "app-server-control.sock")
	target := filepath.Join(t.TempDir(), "real.sock")
	require.NoError(t, os.WriteFile(target, nil, 0o600))
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	resolved, err := filepath.EvalSymlinks(link)
	require.NoError(t, err)
	e := &codexDaemonExecer{outputs: map[string]string{"-U -F n": "p1\nf3\nn" + resolved + "\nf5\nn" + resolved + "\n"}}
	clients, err := CodexDaemonClients(t.Context(), e, link)
	require.NoError(t, err)
	require.Equal(t, 1, clients)
	_, err = CodexDaemonClients(t.Context(), fakeExecer{missing: true}, link)
	require.Error(t, err)
	_, err = CodexDaemonClients(t.Context(), e, filepath.Join(t.TempDir(), "missing.sock"))
	require.Error(t, err)
}

func TestRestartCodexDaemon(t *testing.T) {
	dir := t.TempDir()
	e := &codexDaemonExecer{outputs: map[string]string{"app-server daemon restart": `{"status":"restarted","pid":4242}`}}
	pid, err := RestartCodexDaemon(t.Context(), e, dir)
	require.NoError(t, err)
	require.Equal(t, 4242, pid)
	require.Contains(t, e.env, "CODEX_HOME="+dir)
	_, err = RestartCodexDaemon(t.Context(), &codexDaemonExecer{}, dir)
	require.Error(t, err)
	require.NotErrorIs(t, err, context.DeadlineExceeded)
	expired, cancel := context.WithTimeout(t.Context(), 0)
	defer cancel()
	_, err = RestartCodexDaemon(expired, &codexDaemonExecer{}, dir)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestCodexLogin(t *testing.T) {
	dir := t.TempDir()
	e := &codexDaemonExecer{outputs: map[string]string{"login status": "Logged in using an API key - sk-***\n", "logout": "Successfully logged out\n"}}
	require.True(t, CodexLoggedIn(t.Context(), e, dir))
	require.Contains(t, e.env, "CODEX_HOME="+dir)
	require.NoError(t, CodexLogout(t.Context(), e, dir))
	require.False(t, CodexLoggedIn(t.Context(), &codexDaemonExecer{}, dir))
	require.False(t, CodexLoggedIn(t.Context(), fakeExecer{missing: true}, dir))
	require.Error(t, CodexLogout(t.Context(), &codexDaemonExecer{}, dir))
	expired, cancel := context.WithTimeout(t.Context(), 0)
	defer cancel()
	require.ErrorIs(t, CodexLogout(expired, &codexDaemonExecer{}, dir), context.DeadlineExceeded)
}

func TestCodexClearCloudConfig(t *testing.T) {
	dir := t.TempDir()
	cache := CodexCloudConfigPath(dir)
	require.NoError(t, os.WriteFile(cache, []byte("{}"), 0o600))
	require.NoError(t, CodexClearCloudConfig(dir))
	require.NoFileExists(t, cache)
	require.NoError(t, CodexClearCloudConfig(dir))
	require.NoError(t, os.MkdirAll(filepath.Join(cache, "child"), 0o700))
	require.Error(t, CodexClearCloudConfig(dir))
}
