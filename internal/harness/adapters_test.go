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
			plans, e := PrepareHarness(name, path, fixture(t), Selection{Primary: "acme/primary", Background: "acme/background"}, "http://127.0.0.1:1234", FixtureToken, false, true)
			require.NoError(t, e)
			require.NoError(t, ApplyPlans(plans))
			plans, e = PrepareHarness(name, path, fixture(t), Selection{Primary: "acme/primary", Background: "acme/background"}, "http://127.0.0.1:1234", FixtureToken, false, false)
			require.NoError(t, e)
			for _, p := range plans {
				require.False(t, p.Changed)
			}
			require.NoError(t, ApplyPlans(plans))
			status, e := Inspect(Detection{Name: name, Path: path})
			require.NoError(t, e)
			require.Equal(t, "configured", status.State)
			require.Len(t, status.Routes, 4)
			routes := fixture(t)[:2]
			plans, e = PrepareHarness(name, path, routes, Selection{Primary: "acme/primary", Background: "acme/background"}, "http://127.0.0.1:1234", FixtureToken, false, false)
			require.NoError(t, e)
			require.NoError(t, ApplyPlans(plans))
			status, e = Inspect(Detection{Name: name, Path: path})
			require.NoError(t, e)
			require.Len(t, status.Routes, 2)
			plans, e = PrepareHarnessTeardown(name, path)
			require.NoError(t, e)
			require.NoError(t, ApplyPlans(plans))
			b, e := os.ReadFile(path)
			require.NoError(t, e)
			require.Equal(t, original, b)
			if name == "codex" {
				_, e = os.Stat(CatalogPath(path))
				require.True(t, os.IsNotExist(e))
			}
		})
	}
}
func TestCodexCatalogDriftAndInterruptedInstall(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	plans, e := PrepareHarness("codex", path, fixture(t), Selection{Primary: "acme/primary"}, "http://127.0.0.1:1234", FixtureToken, false, false)
	require.NoError(t, e)
	// Catalog succeeds, config is never written. Teardown must find the orphan.
	require.NoError(t, plans[0].Apply())
	plans, e = PrepareHarnessTeardown("codex", path)
	require.NoError(t, e)
	require.NoError(t, ApplyPlans(plans))
	_, e = os.Stat(CatalogPath(path))
	require.True(t, os.IsNotExist(e))
	plans, e = PrepareHarness("codex", path, fixture(t), Selection{Primary: "acme/primary"}, "http://127.0.0.1:1234", FixtureToken, false, false)
	require.NoError(t, e)
	require.NoError(t, ApplyPlans(plans))
	save(t, CatalogPath(path), map[string]any{"models": []any{map[string]any{"slug": "user-edit"}}})
	status, e := Inspect(Detection{Name: "codex", Path: path})
	require.NoError(t, e)
	require.Equal(t, "drifted", status.State)
	plans, e = PrepareHarnessTeardown("codex", path)
	require.NoError(t, e)
	require.NoError(t, ApplyPlans(plans))
	require.NotEmpty(t, plans[1].Conflicts)
	require.FileExists(t, CatalogPath(path))
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
		_, e := PrepareHarness(name, path, routes, Selection{Primary: "acme/primary"}, "http://127.0.0.1:1234", FixtureToken, false, false)
		require.Error(t, e)
		_, e = PrepareHarness(name, path, fixture(t), Selection{Primary: "acme/primary", Fallback: "acme/fallback"}, "http://127.0.0.1:1234", FixtureToken, false, false)
		require.ErrorContains(t, e, "only for Claude")
	}
}

func TestPreservesNativeInstructionsAndAgentSettings(t *testing.T) {
	require.Greater(t, len(codexNativeInstructions), 10000)
	path := filepath.Join(t.TempDir(), "config.toml")
	original := []byte("model_instructions_file = \"custom.md\"\n[agents.reviewer]\nconfig_file = \"reviewer.toml\"\n")
	require.NoError(t, os.WriteFile(path, original, 0644))
	plans, e := PrepareHarness("codex", path, fixture(t), Selection{Primary: "acme/primary"}, "http://127.0.0.1:1234", FixtureToken, false, false)
	require.NoError(t, e)
	require.NoError(t, ApplyPlans(plans))
	_, data, _, _, e := Read(path)
	require.NoError(t, e)
	require.Equal(t, "custom.md", data["model_instructions_file"])
	require.Equal(t, "reviewer.toml", get(data, []string{"agents", "reviewer", "config_file"}).Data)
	catalog := load(t, CatalogPath(path))
	model := catalog["models"].([]any)[0].(map[string]any)
	require.Equal(t, codexNativeInstructions, model["base_instructions"])
}
func TestClaudePreservesSubagentAndAdvisorChoicesByDefault(t *testing.T) {
	current := map[string]any{"advisorModel": "existing-advisor", "env": map[string]any{"CLAUDE_CODE_SUBAGENT_MODEL": "existing-subagent"}}
	settings, e := ClaudeSettings(fixture(t), Selection{Primary: "acme/primary"}, "http://127.0.0.1:1234", FixtureToken, false, current, nil)
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
			_, err := PrepareHarness(name, filepath.Join(dir, filename), fixture(t), Selection{Primary: "acme/primary"}, "http://127.0.0.1:1234", FixtureToken, false, false)
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
			plans, err := PrepareHarness("opencode", path, fixture(t), Selection{Primary: "acme/primary", Background: background}, "http://127.0.0.1:1234", FixtureToken, false, true)
			require.NoError(t, err)
			require.NoError(t, ApplyPlans(plans))
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
			plans, err := PrepareHarness("opencode", path, fixture(t), Selection{Primary: "acme/primary"}, "http://127.0.0.1:1234", FixtureToken, false, true)
			require.NoError(t, err)
			require.NoError(t, ApplyPlans(plans))
			installed, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Contains(t, string(installed), "Keep my comment")
			require.Contains(t, string(installed), "keep provider")
			data, err := decodeConfig(path, installed)
			require.NoError(t, err)
			require.Equal(t, "dark", data["theme"])
			require.Equal(t, "baseten-harness/acme/primary", data["model"])
			plans, err = PrepareHarness("opencode", path, fixture(t), Selection{Primary: "acme/primary"}, "http://127.0.0.1:1234", FixtureToken, false, true)
			require.NoError(t, err)
			require.False(t, plans[0].Changed)
			if edit {
				installed = bytes.Replace(installed, []byte(`"dark"`), []byte(`"light"`), 1)
				require.NoError(t, os.WriteFile(path, installed, 0600))
			}
			plans, err = PrepareHarnessTeardown("opencode", path)
			require.NoError(t, err)
			require.NoError(t, ApplyPlans(plans))
			restored, err := os.ReadFile(path)
			require.NoError(t, err)
			if !edit {
				require.Equal(t, original, restored)
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
	_, err := PrepareHarness("opencode", path, fixture(t), Selection{Primary: "acme/primary"}, "http://127.0.0.1:1234", FixtureToken, false, true)
	require.ErrorContains(t, err, "invalid settings JSONC")
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, original, data)
	require.NoFileExists(t, JournalPath(path))
}
