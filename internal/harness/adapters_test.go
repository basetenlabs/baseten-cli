//go:build !windows

package harness

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAdaptersLifecycle(t *testing.T) {
	for _, name := range []string{"codex", "opencode"} {
		t.Run(name, func(t *testing.T) {
			filename := "config.toml"
			original := []byte("# retained original\ntheme = \"dark\"\nmodel = \"previous\"\n")
			if name == "opencode" {
				filename = "opencode.json"
				original = []byte("{\"theme\":\"dark\",\"model\":\"previous\"}\n")
			}
			path := filepath.Join(t.TempDir(), filename)
			require.NoError(t, os.WriteFile(path, original, 0644))
			plans, e := adapter(t, name).Prepare(path, fixture(t), Selection{Primary: "acme/primary"}, "http://127.0.0.1:1234", FixtureToken)
			require.NoError(t, e)
			require.NoError(t, ApplyPlans(plans, FixtureToken))
			plans, e = adapter(t, name).Prepare(path, fixture(t), Selection{Primary: "acme/primary"}, "http://127.0.0.1:1234", FixtureToken)
			require.NoError(t, e)
			for _, p := range plans {
				require.False(t, p.Changed)
			}
			require.NoError(t, ApplyPlans(plans, FixtureToken))
			status, e := adapter(t, name).Inspect(Detection{Name: name, Path: path})
			require.NoError(t, e)
			require.Equal(t, "configured", status.State)
			require.Len(t, status.Routes, 4)
			routes := fixture(t)[:2]
			plans, e = adapter(t, name).Prepare(path, routes, Selection{Primary: "acme/primary"}, "http://127.0.0.1:1234", FixtureToken)
			require.NoError(t, e)
			require.NoError(t, ApplyPlans(plans, FixtureToken))
			status, e = adapter(t, name).Inspect(Detection{Name: name, Path: path})
			require.NoError(t, e)
			require.Len(t, status.Routes, 2)
			plans, e = adapter(t, name).Teardown(path)
			require.NoError(t, e)
			require.NoError(t, ApplyPlans(plans, FixtureToken))
			b, e := os.ReadFile(path)
			require.NoError(t, e)
			require.Equal(t, original, b)
			if name == "codex" {
				_, e = os.Stat(catalogPath(path))
				require.True(t, os.IsNotExist(e))
			}
		})
	}
}

func TestCodexCatalogDriftAndInterruptedInstall(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	plans, e := adapter(t, codexName).Prepare(path, fixture(t), Selection{Primary: "acme/primary"}, "http://127.0.0.1:1234", FixtureToken)
	require.NoError(t, e)
	// Catalog succeeds, config is never written. Teardown must find the orphan.
	require.NoError(t, ApplyPlans(plans[:1], FixtureToken))
	plans, e = adapter(t, codexName).Teardown(path)
	require.NoError(t, e)
	require.NoError(t, ApplyPlans(plans, FixtureToken))
	_, e = os.Stat(catalogPath(path))
	require.True(t, os.IsNotExist(e))
	plans, e = adapter(t, codexName).Prepare(path, fixture(t), Selection{Primary: "acme/primary"}, "http://127.0.0.1:1234", FixtureToken)
	require.NoError(t, e)
	require.NoError(t, ApplyPlans(plans, FixtureToken))
	save(t, catalogPath(path), map[string]any{"models": []any{map[string]any{"slug": "user-edit"}}})
	status, e := adapter(t, codexName).Inspect(Detection{Name: codexName, Path: path})
	require.NoError(t, e)
	require.Equal(t, "drifted", status.State)
	plans, e = adapter(t, codexName).Teardown(path)
	require.NoError(t, e)
	require.NoError(t, ApplyPlans(plans, FixtureToken))
	require.Contains(t, plans[1].Replaced, "models")
	require.NoFileExists(t, catalogPath(path))
}

func TestAdaptersRejectInvalidNamesAndUnsupportedOptions(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"codex", "opencode"} {
		path := filepath.Join(dir, "config.toml")
		if name == "opencode" {
			path = filepath.Join(dir, "config.json")
		}
		routes := fixture(t)
		routes[0].Name = ""
		_, e := adapter(t, name).Prepare(path, routes, Selection{Primary: "acme/primary"}, "http://127.0.0.1:1234", FixtureToken)
		require.Error(t, e)
		_, e = adapter(t, name).Prepare(path, fixture(t), Selection{Primary: "acme/primary", Fallback: "acme/fallback"}, "http://127.0.0.1:1234", FixtureToken)
		require.ErrorContains(t, e, "only for Claude")
	}
}

func TestCodexEmptyCatalogInstructionsPreservesUserInstructionsAndAgents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	original := []byte("model_instructions_file = \"custom.md\"\n[agents.reviewer]\nconfig_file = \"reviewer.toml\"\n")
	require.NoError(t, os.WriteFile(path, original, 0644))
	plans, e := adapter(t, codexName).Prepare(path, fixture(t), Selection{Primary: "acme/primary"}, "http://127.0.0.1:1234", FixtureToken)
	require.NoError(t, e)
	require.NoError(t, ApplyPlans(plans, FixtureToken))
	_, data, _, _, e := readConfig(path)
	require.NoError(t, e)
	require.Equal(t, "custom.md", data["model_instructions_file"])
	require.Equal(t, "reviewer.toml", get(data, []string{"agents", "reviewer", "config_file"}).Data)
	catalog := load(t, catalogPath(path))
	model := catalog["models"].([]any)[0].(map[string]any)
	require.Equal(t, "", model["base_instructions"])
}

func TestClaudePreservesSubagentAndAdvisorChoicesByDefault(t *testing.T) {
	current := map[string]any{"advisorModel": "existing-advisor", "env": map[string]any{"CLAUDE_CODE_SUBAGENT_MODEL": "existing-subagent"}}
	settings, e := claudeSettings(fixture(t), Selection{Primary: "acme/primary"}, "http://127.0.0.1:1234", FixtureToken, current)
	require.NoError(t, e)
	for _, s := range settings {
		require.NotEqual(t, "advisorModel", pathKey(s.Path))
		require.NotEqual(t, "env.CLAUDE_CODE_SUBAGENT_MODEL", pathKey(s.Path))
		require.NotEqual(t, "env.CLAUDE_CODE_SUBAGENT_MODEL_FORCE", pathKey(s.Path))
	}
}

func TestClaudePolicyDoesNotBlockOtherHarnesses(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "managed-settings.json"), []byte("{}"), 0600))
	for _, name := range []string{"claude-code", "codex", "opencode"} {
		t.Run(name, func(t *testing.T) {
			filename := "settings.json"
			if name == "codex" {
				filename = "config.toml"
			}
			_, err := adapter(t, name).Prepare(filepath.Join(dir, filename), fixture(t), Selection{Primary: "acme/primary"}, "http://127.0.0.1:1234", FixtureToken)
			if name == "claude-code" {
				require.ErrorContains(t, err, "managed policy")
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestOpenCodeSmallTaskDefaultAndOverride(t *testing.T) {
	for _, background := range []string{"", "acme/background"} {
		t.Run(background, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "opencode.json")
			plans, err := adapter(t, openCodeName).Prepare(path, fixture(t), Selection{Primary: "acme/primary", Background: background}, "http://127.0.0.1:1234", FixtureToken)
			require.NoError(t, err)
			require.NoError(t, ApplyPlans(plans, FixtureToken))
			data := load(t, path)
			expected := background
			if expected == "" {
				expected = "deepseek-ai/DeepSeek-V4.1-Flash"
			}
			require.Equal(t, "baseten-harness/"+expected, data["small_model"])
			require.True(t, get(data, []string{"provider", "baseten-harness", "models", expected}).Exists)
		})
	}
}

func TestOpenCodeJSONCLifecycle(t *testing.T) {
	for _, edit := range []bool{false, true} {
		t.Run(fmt.Sprint(edit), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "opencode.jsonc")
			original := []byte("{\n // Keep my comment\n \"$schema\": \"https://opencode.ai/config.json\",\n \"theme\": \"dark\",\n \"provider\": { /* keep provider */ \"user/custom~provider\": {\"name\":\"Mine\"}, },\n}\n")
			require.NoError(t, os.WriteFile(path, original, 0644))
			plans, err := adapter(t, openCodeName).Prepare(path, fixture(t), Selection{Primary: "acme/primary"}, "http://127.0.0.1:1234", FixtureToken)
			require.NoError(t, err)
			require.NoError(t, ApplyPlans(plans, FixtureToken))
			installed, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Contains(t, string(installed), "Keep my comment")
			require.Contains(t, string(installed), "keep provider")
			data, err := decodeConfig(path, installed)
			require.NoError(t, err)
			require.Equal(t, "dark", data["theme"])
			require.Equal(t, "baseten-harness/acme/primary", data["model"])
			plans, err = adapter(t, openCodeName).Prepare(path, fixture(t), Selection{Primary: "acme/primary"}, "http://127.0.0.1:1234", FixtureToken)
			require.NoError(t, err)
			require.False(t, plans[0].Changed)
			if edit {
				installed = bytes.Replace(installed, []byte(`"dark"`), []byte(`"light"`), 1)
				require.NoError(t, os.WriteFile(path, installed, 0600))
			}
			plans, err = adapter(t, openCodeName).Teardown(path)
			require.NoError(t, err)
			require.NoError(t, ApplyPlans(plans, FixtureToken))
			restored, err := os.ReadFile(path)
			require.NoError(t, err)
			if !edit {
				before, err := decodeConfig(path, original)
				require.NoError(t, err)
				after, err := decodeConfig(path, restored)
				require.NoError(t, err)
				require.Equal(t, before, after)
				require.Contains(t, string(restored), "Keep my comment")
				require.Contains(t, string(restored), "keep provider")
			} else {
				require.Contains(t, string(restored), "Keep my comment")
				require.Contains(t, string(restored), "keep provider")
				data, err = decodeConfig(path, restored)
				require.NoError(t, err)
				require.Equal(t, "light", data["theme"])
				require.NotContains(t, data, "model")
			}
		})
	}
}

func TestOpenCodeInvalidJSONCUnchanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.jsonc")
	original := []byte("{ // comment\n invalid }")
	require.NoError(t, os.WriteFile(path, original, 0644))
	_, err := adapter(t, openCodeName).Prepare(path, fixture(t), Selection{Primary: "acme/primary"}, "http://127.0.0.1:1234", FixtureToken)
	require.ErrorContains(t, err, "invalid settings JSONC")
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, original, data)
	require.NoFileExists(t, journalPath(path))
}

func TestOpenCodePolicyPathsMatchPlatform(t *testing.T) {
	require.ElementsMatch(t, []string{
		"/Library/Application Support/opencode/opencode.json",
		"/Library/Application Support/opencode/opencode.jsonc",
		"/Library/Managed Preferences/test-user/ai.opencode.managed.plist",
		"/Library/Managed Preferences/ai.opencode.managed.plist",
	}, openCodePolicyPaths("darwin", "test-user"))
	require.ElementsMatch(t, []string{"/etc/opencode/opencode.json", "/etc/opencode/opencode.jsonc"}, openCodePolicyPaths("linux", ""))
}
