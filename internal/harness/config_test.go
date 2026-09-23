//go:build !windows

package harness

import (
	"fmt"
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"testing"
)

const FixtureToken = "baseten-harness-local-fixture"

func fixture(t *testing.T) []Route {
	t.Helper()
	return []Route{{Name: "acme/primary", DisplayName: "Engineering"}, {Name: "acme/background", DisplayName: "Background"}, {Name: "acme/subagent", DisplayName: "Subagents"}, {Name: "acme/fallback", DisplayName: "Fallback"}}
}

func setup(t *testing.T, path string, routes []Route) *Plan {
	t.Helper()
	h, err := Find(ClaudeCode)
	require.NoError(t, err)
	plans, err := h.Prepare(path, routes, Selection{Primary: "acme/primary"}, "http://127.0.0.1:1234", FixtureToken)
	require.NoError(t, err)
	return plans[0]
}

func load(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	d, err := decode(b)
	require.NoError(t, err)
	return d
}

func save(t *testing.T, path string, d map[string]any) {
	t.Helper()
	b, err := encode(d)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, b, 0600))
}

func TestTeardownOnlyRemovesIntegrationSettings(t *testing.T) {
	for _, name := range []string{ClaudeCode, codexName, openCodeName} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "settings.json")
			if name == codexName {
				path = filepath.Join(t.TempDir(), "config.toml")
			}
			original := map[string]any{"model": "original-model", "theme": "dark"}
			writeConfig := func(d map[string]any) {
				b, err := encodeConfig(path, d)
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(path, b, 0600))
			}
			writeConfig(original)
			h := adapter(t, name)
			for i := 0; i < 10; i++ {
				if i == 3 {
					_, d, err := readConfig(path)
					require.NoError(t, err)
					d["theme"] = "light"
					d["user-added"] = "keep"
					writeConfig(d)
				}
				plans, err := h.Prepare(path, fixture(t), Selection{Primary: fixture(t)[i%2].Name}, "https://coding.baseten.co", FixtureToken)
				require.NoError(t, err)
				require.NoError(t, ApplyPlans(plans, FixtureToken))
				require.NoFileExists(t, path+".baseten-harness.json")
			}
			plans, err := h.Teardown(path)
			require.NoError(t, err)
			require.NoError(t, ApplyPlans(plans, ""))
			_, d, err := readConfig(path)
			require.NoError(t, err)
			require.Equal(t, map[string]any{"theme": "light", "user-added": "keep"}, d)
			before, err := os.ReadFile(path)
			require.NoError(t, err)
			plans, err = h.Teardown(path)
			require.NoError(t, err)
			for _, p := range plans {
				require.False(t, p.Changed)
			}
			require.NoError(t, ApplyPlans(plans, ""))
			after, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, before, after)
			configured, err := h.Configured(path)
			require.NoError(t, err)
			require.False(t, configured)
		})
	}
}

func TestConcurrentEditRejectsApply(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	p := setup(t, path, fixture(t))
	require.NoError(t, os.WriteFile(path, []byte("{}"), 0600))
	require.ErrorContains(t, ApplyPlans([]*Plan{p}, FixtureToken), "concurrently")
	require.NoFileExists(t, path+".baseten-harness.json")
}

func TestSetupRepairsPermissionsWithoutContentChanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	require.NoError(t, ApplyPlans([]*Plan{setup(t, path, fixture(t))}, FixtureToken))
	original, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, os.Chmod(path, 0644))
	plan := setup(t, path, fixture(t))
	require.True(t, plan.Changed)
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0644), info.Mode().Perm())
	require.NoError(t, ApplyPlans([]*Plan{plan}, FixtureToken))
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, original, data)
	info, err = os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0600), info.Mode().Perm())
	require.False(t, setup(t, path, fixture(t)).Changed)
}

func TestSymlinkSetupAndTeardownPreserveLinkAndUnrelatedSettings(t *testing.T) {
	for _, name := range []string{ClaudeCode, codexName, openCodeName} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			target := filepath.Join(dir, "dotfile")
			filename := "config.json"
			original := []byte(`{"model":"old","theme":"dark"}`)
			if name == codexName {
				filename = "config.toml"
				original = []byte("model = \"old\"\ntheme = \"dark\"\n")
			}
			link := filepath.Join(dir, filename)
			require.NoError(t, os.WriteFile(target, original, 0644))
			require.NoError(t, os.Symlink(target, link))
			h := adapter(t, name)
			plans, err := h.Prepare(link, fixture(t), Selection{}, "https://coding.baseten.co", FixtureToken)
			require.NoError(t, err)
			require.NoError(t, ApplyPlans(plans, FixtureToken))
			info, err := os.Lstat(link)
			require.NoError(t, err)
			require.NotZero(t, info.Mode()&os.ModeSymlink)
			info, err = os.Stat(target)
			require.NoError(t, err)
			require.Equal(t, os.FileMode(0600), info.Mode().Perm())
			configured, err := h.Configured(link)
			require.NoError(t, err)
			require.True(t, configured)
			status, err := h.Inspect(Detection{Name: name, Path: link})
			require.NoError(t, err)
			require.Equal(t, "configured", status.State)
			plans, err = h.Teardown(link)
			require.NoError(t, err)
			require.NoError(t, ApplyPlans(plans, ""))
			restored, err := os.ReadFile(target)
			require.NoError(t, err)
			d, err := decodeConfig(link, restored)
			require.NoError(t, err)
			require.Equal(t, map[string]any{"theme": "dark"}, d)
			info, err = os.Lstat(link)
			require.NoError(t, err)
			require.NotZero(t, info.Mode()&os.ModeSymlink)
			require.NoFileExists(t, target+".baseten-harness.json")
		})
	}
}

func TestTeardownUnconfiguredIsNoop(t *testing.T) {
	for _, name := range []string{ClaudeCode, codexName, openCodeName} {
		for _, existing := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/existing=%t", name, existing), func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "settings.json")
				original := []byte(`{"model":"user-model","env":{"ANTHROPIC_BASE_URL":"https://other.example"}}`)
				if name == codexName {
					path += ".toml"
					original = []byte("model = \"user-model\"\nmodel_provider = \"other\"\n[model_providers.other]\nname = \"Other\"\n")
				}
				if existing {
					require.NoError(t, os.WriteFile(path, original, 0600))
				}
				plans, err := adapter(t, name).Teardown(path)
				require.NoError(t, err)
				for _, p := range plans {
					require.False(t, p.Changed)
					require.False(t, p.Managed)
				}
				require.NoError(t, ApplyPlans(plans, ""))
				if existing {
					b, err := os.ReadFile(path)
					require.NoError(t, err)
					require.Equal(t, original, b)
				} else {
					require.NoFileExists(t, path)
				}
			})
		}
	}
}

func TestTeardownPreservesAnotherProviderSelection(t *testing.T) {
	for _, name := range []string{codexName, openCodeName} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "settings.json")
			if name == codexName {
				path += ".toml"
			}
			h := adapter(t, name)
			plans, err := h.Prepare(path, fixture(t), Selection{}, "https://coding.baseten.co", FixtureToken)
			require.NoError(t, err)
			require.NoError(t, ApplyPlans(plans, FixtureToken))
			_, d, err := readConfig(path)
			require.NoError(t, err)
			d["model"] = "other/user-model"
			if name == codexName {
				d["model_provider"] = "other"
				d["review_model"] = "other/reviewer"
				d["model_catalog_json"] = "/user/custom-catalog.json"
				require.NoError(t, put(d, []string{"model_providers", "other"}, value{Exists: true, Data: map[string]any{"name": "Other"}}))
			} else {
				d["small_model"] = "other/small"
				require.NoError(t, put(d, []string{"provider", "other"}, value{Exists: true, Data: map[string]any{"name": "Other"}}))
				require.NoError(t, put(d, []string{"agent", "general", "model"}, value{Exists: true, Data: "other/subagent"}))
			}
			b, err := encodeConfig(path, d)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(path, b, 0600))
			status, err := h.Inspect(Detection{Name: name, Path: path})
			require.NoError(t, err)
			require.Equal(t, "inactive", status.State)
			plans, err = h.Teardown(path)
			require.NoError(t, err)
			require.NoError(t, ApplyPlans(plans, ""))
			providers := "provider"
			if name == codexName {
				providers = "model_providers"
			}
			require.NoError(t, put(d, []string{providers, providerID}, value{}))
			_, after, err := readConfig(path)
			require.NoError(t, err)
			require.Equal(t, d, after)
		})
	}
}

func TestOptionalSubagentCleanup(t *testing.T) {
	for _, name := range []string{ClaudeCode, openCodeName} {
		for _, explicit := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/explicit=%t", name, explicit), func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "settings.json")
				key := []string{"env", "CLAUDE_CODE_SUBAGENT_MODEL"}
				if name == openCodeName {
					key = []string{"agent", "general", "model"}
				}
				d := map[string]any{}
				require.NoError(t, put(d, key, value{Exists: true, Data: "user-subagent"}))
				save(t, path, d)
				h := adapter(t, name)
				selection := Selection{}
				if explicit {
					selection.Subagent = "acme/subagent"
				}
				plans, err := h.Prepare(path, fixture(t), selection, "https://coding.baseten.co", FixtureToken)
				require.NoError(t, err)
				require.NoError(t, ApplyPlans(plans, FixtureToken))
				// Refresh drops this route and omits the optional flag.
				plans, err = h.Prepare(path, fixture(t)[:2], Selection{}, "https://coding.baseten.co", FixtureToken)
				require.NoError(t, err)
				require.NoError(t, ApplyPlans(plans, FixtureToken))
				plans, err = h.Teardown(path)
				require.NoError(t, err)
				require.NoError(t, ApplyPlans(plans, ""))
				d = load(t, path)
				if explicit {
					require.False(t, get(d, key).Exists)
				} else {
					require.Equal(t, "user-subagent", get(d, key).Data)
				}
			})
		}
	}
}
