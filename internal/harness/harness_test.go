package harness

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

const testToken = "baseten-harness-test-token"

const testEndpoint = "https://inference.example.com"

func testRoutes() []Route {
	return []Route{
		{Name: "acme/primary", DisplayName: "Primary"},
		{Name: "acme/background", DisplayName: "Background"},
		{Name: "acme/subagent", DisplayName: "Subagents"},
		{Name: "acme/fallback", DisplayName: "Fallback"},
	}
}

type fakeExecer struct{ missing bool }

func (e fakeExecer) LookPath(name string) (string, error) {
	if e.missing {
		return "", exec.ErrNotFound
	}
	return filepath.Join(string(filepath.Separator), "fake", name), nil
}

func (fakeExecer) Exec(command *exec.Cmd) error {
	_, err := fmt.Fprintln(command.Stdout, "1.2.3\nextra")
	return err
}

type codexDetectionExecer struct {
	fakeExecer
	binaries map[string]bool
	executed string
}

func (e *codexDetectionExecer) LookPath(name string) (string, error) {
	if !e.binaries[name] {
		return "", exec.ErrNotFound
	}
	if name == "codex" {
		return "/cli/codex", nil
	}
	return name, nil
}

func (e *codexDetectionExecer) Exec(command *exec.Cmd) error {
	e.executed = command.Path
	return e.fakeExecer.Exec(command)
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
	chatgpt := bundled("/Applications", "ChatGPT.app")
	linuxChatGPT := "/usr/lib/chatgpt/resources/codex"
	for _, tc := range []struct {
		name     string
		binaries map[string]bool
		want     string
		// goos is the only platform where want applies; elsewhere nothing is found.
		goos string
	}{
		{"chatgpt", map[string]bool{chatgpt: true}, chatgpt, "darwin"},
		{"codex", map[string]bool{bundled("/Applications", "Codex.app"): true}, bundled("/Applications", "Codex.app"), "darwin"},
		{"user chatgpt", map[string]bool{bundled(filepath.Join(home, "Applications"), "ChatGPT.app"): true}, bundled(filepath.Join(home, "Applications"), "ChatGPT.app"), "darwin"},
		{"user codex", map[string]bool{bundled(filepath.Join(home, "Applications"), "Codex.app"): true}, bundled(filepath.Join(home, "Applications"), "Codex.app"), "darwin"},
		{"linux chatgpt", map[string]bool{linuxChatGPT: true}, linuxChatGPT, "linux"},
		{"CLI takes precedence", map[string]bool{"codex": true, chatgpt: true, linuxChatGPT: true}, "/cli/codex", ""},
		{"config alone is not an installation", nil, "", ""},
		{"classic app without bundled codex", map[string]bool{"/Applications/ChatGPT.app/Contents/MacOS/ChatGPT": true}, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			execer := &codexDetectionExecer{binaries: tc.binaries}
			want := tc.want
			if tc.goos != "" && tc.goos != runtime.GOOS {
				want = ""
			}
			d, err := codexHarness{}.Detect(t.Context(), execer, "")
			require.NoError(t, err)
			require.Equal(t, path, d.Path)
			require.Equal(t, want != "", d.Installed)
			require.Equal(t, want, execer.executed)
			if d.Installed {
				require.Equal(t, "1.2.3", d.Version)
			}
		})
	}
}

func TestCodexDesktopDetectionWithoutHome(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	chatgpt := filepath.Join("/Applications", "ChatGPT.app", "Contents", "Resources", "codex")
	execer := &codexDetectionExecer{binaries: map[string]bool{chatgpt: true}}
	d, err := codexHarness{}.Detect(t.Context(), execer, t.TempDir())
	require.NoError(t, err)
	require.Equal(t, runtime.GOOS == "darwin", d.Installed)
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
	plans, err := h.Prepare(path, routes, s, testEndpoint)
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
			plans, err := h.Prepare(path, testRoutes(), Selection{Primary: "acme/background"}, testEndpoint)
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
			require.Equal(t, map[string]any{"theme": "light", "user-added": "keep"}, load(t, path))
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

func TestRefreshWithFewerRoutes(t *testing.T) {
	for _, h := range All() {
		t.Run(h.Name(), func(t *testing.T) {
			path := settingsPath(t, h)
			setup(t, h, path, testRoutes(), Selection{})
			setup(t, h, path, testRoutes()[:2], Selection{})
			status, err := h.Inspect(Detection{Name: h.Name(), Path: path})
			require.NoError(t, err)
			require.ElementsMatch(t, testRoutes()[:2], status.Routes)
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
			require.Equal(t, d, load(t, path))
		})
	}
}

func TestOptionalSubagentCleanup(t *testing.T) {
	for _, h := range []Harness{claudeCodeHarness{}, openCodeHarness{}} {
		for _, explicit := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/explicit=%t", h.Name(), explicit), func(t *testing.T) {
				path := settingsPath(t, h)
				key := claudeSubagentPath
				if h.Name() == OpenCode {
					key = openCodeSubagentPaths[0]
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
			path := settingsPath(t, claudeCodeHarness{})
			setup(t, claudeCodeHarness{}, path, testRoutes(), Selection{Background: background})
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

func TestUnsupportedSelectionsAreRejected(t *testing.T) {
	for _, tc := range []struct {
		h Harness
		s Selection
	}{
		{codexHarness{}, Selection{Background: "acme/background"}},
		{codexHarness{}, Selection{Subagent: "acme/subagent"}},
		{codexHarness{}, Selection{Fallback: "acme/fallback"}},
		{openCodeHarness{}, Selection{Fallback: "acme/fallback"}},
	} {
		t.Run(fmt.Sprintf("%s/%+v", tc.h.Name(), tc.s), func(t *testing.T) {
			_, err := tc.h.Prepare(settingsPath(t, tc.h), testRoutes(), tc.s, testEndpoint)
			require.ErrorContains(t, err, "supported only")
		})
	}
	_, err := claudeCodeHarness{}.Prepare(settingsPath(t, claudeCodeHarness{}), testRoutes(), Selection{Primary: "acme/missing"}, testEndpoint)
	require.ErrorContains(t, err, "not one of the team's routes")
}

func TestCodexOrphanedCatalog(t *testing.T) {
	h := codexHarness{}
	path := settingsPath(t, h)
	plans, err := h.Prepare(path, testRoutes(), Selection{}, testEndpoint)
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
	_, err = openCodeHarness{}.Prepare(path, testRoutes(), Selection{}, testEndpoint)
	require.ErrorContains(t, err, "invalid settings JSON")
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, invalid, data)
}

func TestOpenCodeFirstPartyRoutesUseNativeAPIs(t *testing.T) {
	path := settingsPath(t, openCodeHarness{})
	routes := testRoutes()
	routes[0].Target = TargetAnthropic
	routes[1].Target = TargetOpenAI
	routes[2].Target = "XAI"
	setup(t, openCodeHarness{}, path, routes, Selection{})
	data := load(t, path)
	require.Equal(t, "@ai-sdk/openai-compatible", get(data, []string{"provider", providerID, "npm"}).Data)
	require.Equal(t, "Bearer "+testToken, get(data, openCodeBearerPath).Data, "@ai-sdk/anthropic sends apiKey only as x-api-key")
	models := get(data, []string{"provider", providerID, "models"}).Data.(map[string]any)
	require.Equal(t, map[string]any{"name": "Primary", "provider": map[string]any{"npm": "@ai-sdk/anthropic"}}, models["acme/primary"])
	require.Equal(t, map[string]any{"name": "Background", "provider": map[string]any{"npm": "@ai-sdk/openai"}}, models["acme/background"])
	require.Equal(t, map[string]any{"name": "Subagents"}, models["acme/subagent"])
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
	for _, env := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME", "XDG_CONFIG_HOME"} {
		t.Setenv(env, "")
	}
	custom := t.TempDir()
	for _, tc := range []struct {
		h                  Harness
		env                string
		defaultPath, inEnv string
	}{
		{claudeCodeHarness{}, "CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude", "settings.json"), filepath.Join(custom, "settings.json")},
		{codexHarness{}, "CODEX_HOME", filepath.Join(home, ".codex", "config.toml"), filepath.Join(custom, "config.toml")},
		{openCodeHarness{}, "XDG_CONFIG_HOME", filepath.Join(home, ".config", "opencode", "opencode.json"), filepath.Join(custom, "opencode", "opencode.json")},
	} {
		t.Run(tc.h.Name(), func(t *testing.T) {
			d, err := tc.h.Detect(t.Context(), fakeExecer{}, "")
			require.NoError(t, err)
			require.Equal(t, Detection{Name: tc.h.Name(), Path: tc.defaultPath, Installed: true, Version: "1.2.3"}, d)
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
