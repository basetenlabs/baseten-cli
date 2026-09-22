//go:build !windows

package harness

import (
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

func TestTeardownRestoresBytesOrRemovesNewFile(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "new", true: "existing"}[existing], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "settings.json")
			original := []byte("{\"custom\":9007199254740993}\n")
			if existing {
				require.NoError(t, os.WriteFile(path, original, 0600))
			}
			require.NoError(t, ApplyPlans([]*Plan{setup(t, path, fixture(t))}, FixtureToken))
			p, e := prepareTeardown(path)
			require.NoError(t, e)
			require.NoError(t, ApplyPlans([]*Plan{p}, FixtureToken))
			b, e := os.ReadFile(path)
			if existing {
				require.NoError(t, e)
				require.Equal(t, original, b)
			} else {
				require.True(t, os.IsNotExist(e))
			}
		})
	}
}

func TestConcurrentEditRejectsApply(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	p := setup(t, path, fixture(t))
	require.NoError(t, os.WriteFile(path, []byte("{}"), 0600))
	require.ErrorContains(t, ApplyPlans([]*Plan{p}, FixtureToken), "concurrently")
	_, e := os.Stat(journalPath(path))
	require.True(t, os.IsNotExist(e))
}

func TestInterruptedRefreshCanRestorePreviousManagedValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	require.NoError(t, ApplyPlans([]*Plan{setup(t, path, fixture(t))}, FixtureToken))
	routes := fixture(t)
	routes[0].DisplayName = "Changed"
	p := setup(t, path, routes)
	// Simulate a crash after journal update, before the settings replacement.
	b, e := encode(p.journal)
	require.NoError(t, e)
	require.NoError(t, p.journalSnapshot.writeExisting(b))
	teardown, e := prepareTeardown(path)
	require.NoError(t, e)
	require.NoError(t, ApplyPlans([]*Plan{teardown}, FixtureToken))
	_, e = os.Stat(path)
	require.True(t, os.IsNotExist(e))
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

func TestRefreshKeepsFirstSetupRestorePoint(t *testing.T) {
	for _, filename := range []string{"settings.json", "opencode.jsonc", "config.toml"} {
		t.Run(filename, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), filename)
			original, err := encodeConfig(path, map[string]any{"model": "original", "subagent": "original-subagent", "theme": "dark"}, nil)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(path, original, 0600))
			apply := func(settings []setting) {
				t.Helper()
				p, err := prepareSettings(path, []Route{{Name: "updated-route"}}, func(map[string]any) ([]setting, error) { return settings, nil })
				require.NoError(t, err)
				require.NoError(t, ApplyPlans([]*Plan{p}, FixtureToken))
				_, _, _, journal, err := readConfig(path)
				require.NoError(t, err)
				require.Equal(t, original, journal.Original)
			}
			apply([]setting{desired([]string{"model"}, "first-route")})
			// Edits after the first setup must never replace the restore point.
			_, current, _, _, err := readConfig(path)
			require.NoError(t, err)
			current["model"] = "session-choice"
			current["subagent"] = "later-choice"
			current["theme"] = "light"
			edited, err := encodeConfig(path, current, nil)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(path, edited, 0600))
			apply([]setting{desired([]string{"model"}, "second-route"), desired([]string{"subagent"}, "managed-subagent")})
			// Omitting a previous optional setting must not lose its restoration record.
			apply([]setting{desired([]string{"model"}, "latest-route")})
			_, current, _, journal, err := readConfig(path)
			require.NoError(t, err)
			require.Equal(t, "latest-route", current["model"])
			require.Len(t, journal.Settings, 2)
			p, err := prepareTeardown(path)
			require.NoError(t, err)
			require.NoError(t, ApplyPlans([]*Plan{p}, FixtureToken))
			_, restored, _, _, err := readConfig(path)
			require.NoError(t, err)
			require.Equal(t, "original", restored["model"])
			require.Equal(t, "original-subagent", restored["subagent"])
			require.Equal(t, "light", restored["theme"])
		})
	}
}

func TestSymlinkSetupPreservesLinkAndRestoresTarget(t *testing.T) {
	for _, name := range []string{ClaudeCode, Codex, OpenCode} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			target := filepath.Join(dir, "dotfile")
			filename := "config.json"
			original := []byte(`{"model":"old","theme":"dark"}`)
			if name == Codex {
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
			require.Equal(t, original, restored)
			info, err = os.Lstat(link)
			require.NoError(t, err)
			require.NotZero(t, info.Mode()&os.ModeSymlink)
			require.NoFileExists(t, journalPath(target))
		})
	}
}
