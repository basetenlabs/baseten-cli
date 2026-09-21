//go:build !windows

package harness

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const FixtureToken = "baseten-harness-local-fixture"

func fixture(t *testing.T) []Route {
	t.Helper()
	return []Route{
		{Name: "acme/primary", DisplayName: "Engineering"},
		{Name: "acme/background", DisplayName: "Background"},
		{Name: "acme/subagent", DisplayName: "Subagents"},
		{Name: "acme/fallback", DisplayName: "Fallback"},
	}
}
func setup(t *testing.T, path string, routes []Route, replace bool) *Plan {
	t.Helper()
	p, e := Prepare(path, routes, Selection{Primary: "acme/primary"}, "http://127.0.0.1:1234", FixtureToken, false, replace)
	require.NoError(t, e)
	return p
}
func load(t *testing.T, path string) map[string]any {
	t.Helper()
	b, e := os.ReadFile(path)
	require.NoError(t, e)
	d, e := decode(b)
	require.NoError(t, e)
	return d
}
func save(t *testing.T, path string, d map[string]any) {
	t.Helper()
	b, e := encode(d)
	require.NoError(t, e)
	require.NoError(t, os.WriteFile(path, b, 0600))
}
func TestLifecycleRefreshRestoresOriginalAndPreservesUnrelatedEdits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	original := []byte("{\n  \"theme\": \"dark\", \"model\": \"old\", \"env\": {\"MY_VAR\": \"keep\"}, \"modelPicker\": {\"options\": [{\"model\": \"user/model\", \"label\": \"Mine\"}]}\n}\n")
	require.NoError(t, os.WriteFile(path, original, 0600))
	_, err := Prepare(path, fixture(t), Selection{Primary: "acme/primary"}, "http://127.0.0.1:1234", FixtureToken, false, false)
	require.ErrorContains(t, err, "model conflicts")
	p := setup(t, path, fixture(t), true)
	b, _ := os.ReadFile(path)
	require.Equal(t, original, b) // planning writes nothing
	require.NoError(t, p.Apply())
	d := load(t, path)
	require.Equal(t, "keep", get(d, []string{"env", "MY_VAR"}).Data)
	require.Len(t, get(d, []string{"modelPicker", "options"}).Data, 5)
	first, _ := os.ReadFile(path)
	p = setup(t, path, fixture(t), false)
	require.False(t, p.Changed)
	require.NoError(t, p.Apply())
	second, _ := os.ReadFile(path)
	require.Equal(t, first, second)
	// A catalog addition/removal changes generated entries, not Route identity.
	routes := fixture(t)[:1]
	routes = append(routes, Route{Name: "acme/new", DisplayName: "New"})
	p = setup(t, path, routes, false)
	require.NoError(t, p.Apply())
	d = load(t, path)
	require.Len(t, get(d, []string{"modelPicker", "options"}).Data, 3)
	require.Equal(t, "acme/primary", d["model"])
	d["theme"] = "light"
	save(t, path, d)
	p, err = PrepareTeardown(path)
	require.NoError(t, err)
	require.Empty(t, p.Conflicts)
	require.NoError(t, p.Apply())
	d = load(t, path)
	require.Equal(t, "light", d["theme"])
	require.Equal(t, "old", d["model"])
	require.Len(t, get(d, []string{"modelPicker", "options"}).Data, 1)
	require.False(t, get(d, []string{"env", "ANTHROPIC_AUTH_TOKEN"}).Exists)
	_, err = os.Stat(JournalPath(path))
	require.True(t, os.IsNotExist(err))
}
func TestTeardownRestoresBytesOrRemovesNewFile(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "new", true: "existing"}[existing], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "settings.json")
			original := []byte("{\"custom\":9007199254740993}\n")
			if existing {
				require.NoError(t, os.WriteFile(path, original, 0600))
			}
			require.NoError(t, setup(t, path, fixture(t), false).Apply())
			p, e := PrepareTeardown(path)
			require.NoError(t, e)
			require.NoError(t, p.Apply())
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
func TestDriftIsPreservedAndBackupRetained(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	require.NoError(t, setup(t, path, fixture(t), false).Apply())
	d := load(t, path)
	d["model"] = "user-edit"
	save(t, path, d)
	_, e := Prepare(path, fixture(t), Selection{Primary: "acme/primary"}, "http://127.0.0.1:1234", FixtureToken, false, false)
	require.ErrorContains(t, e, "user changed model")
	r, e := Inspect(Detection{Path: path})
	require.NoError(t, e)
	require.Equal(t, "drifted", r.State)
	require.Contains(t, r.Drift, "model")
	p, e := PrepareTeardown(path)
	require.NoError(t, e)
	require.Contains(t, p.Conflicts, "model")
	require.NoError(t, p.Apply())
	require.Equal(t, "user-edit", load(t, path)["model"])
	_, _, _, j, e := Read(path)
	require.NoError(t, e)
	require.Len(t, j.Settings, 1)
}
func TestPreexistingIdenticalValuesAreNotOwned(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	save(t, path, map[string]any{"model": "acme/primary"})
	p := setup(t, path, fixture(t), false)
	require.NotContains(t, p.Keys, "model")
	require.NoError(t, p.Apply())
	p, e := PrepareTeardown(path)
	require.NoError(t, e)
	require.NoError(t, p.Apply())
	require.Equal(t, "acme/primary", load(t, path)["model"])
}
func TestConcurrentEditRejectsApply(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	p := setup(t, path, fixture(t), false)
	require.NoError(t, os.WriteFile(path, []byte("{}"), 0600))
	require.ErrorContains(t, p.Apply(), "concurrently")
	_, e := os.Stat(JournalPath(path))
	require.True(t, os.IsNotExist(e))
}
func TestInterruptedRefreshCanRestorePreviousManagedValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	require.NoError(t, setup(t, path, fixture(t), false).Apply())
	routes := fixture(t)
	routes[0].DisplayName = "Changed"
	p := setup(t, path, routes, false)
	// Simulate a crash after journal update, before the settings replacement.
	b, e := encode(p.journal)
	require.NoError(t, e)
	require.NoError(t, p.journalSnapshot.Write(b))
	teardown, e := PrepareTeardown(path)
	require.NoError(t, e)
	require.Empty(t, teardown.Conflicts)
	require.NoError(t, teardown.Apply())
	_, e = os.Stat(path)
	require.True(t, os.IsNotExist(e))
}
func TestSymlinkPermissionAndLockHandling(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.json")
	link := filepath.Join(dir, "link.json")
	require.NoError(t, os.WriteFile(target, []byte("{}"), 0644))
	require.NoError(t, os.Symlink(target, link))
	plan, e := Prepare(link, fixture(t), Selection{Primary: "acme/primary"}, "http://127.0.0.1:1234", FixtureToken, false, false)
	require.NoError(t, e)
	require.True(t, plan.Changed)
	before, e := os.Stat(target)
	require.NoError(t, e)
	require.Equal(t, os.FileMode(0644), before.Mode().Perm())
	unlock, e := Lock(link)
	require.NoError(t, e)
	defer unlock()
	_, e = Lock(target)
	require.Error(t, e)
	require.NoError(t, setup(t, link, fixture(t), false).Apply())
	info, e := os.Lstat(link)
	require.NoError(t, e)
	require.NotZero(t, info.Mode()&os.ModeSymlink)
	info, e = os.Stat(target)
	require.NoError(t, e)
	require.Equal(t, os.FileMode(0600), info.Mode().Perm())
	_, e = os.Stat(JournalPath(target))
	require.NoError(t, e)
}
func TestValidationNeverLeaksValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	secret := "sensitive-value"
	save(t, path, map[string]any{"env": map[string]any{"ANTHROPIC_AUTH_TOKEN": secret}})
	_, e := Prepare(path, fixture(t), Selection{Primary: "acme/primary"}, "http://127.0.0.1:1234", FixtureToken, false, false)
	require.Error(t, e)
	require.NotContains(t, e.Error(), secret)
	p := setup(t, path, fixture(t), true)
	b, e := json.Marshal(p)
	require.NoError(t, e)
	require.NotContains(t, string(b), secret)
	require.NotContains(t, string(b), FixtureToken)
	require.NoError(t, os.WriteFile(filepath.Join(filepath.Dir(path), "managed-settings.json"), []byte("{}"), 0600))
	_, e = Prepare(path, fixture(t), Selection{Primary: "acme/primary"}, "http://127.0.0.1:1234", FixtureToken, false, true)
	require.ErrorContains(t, e, "managed policy")
}
func TestCatalogAndSelections(t *testing.T) {
	for _, raw := range []string{"http://127.0.0.1:1234", "http://[::1]:8080"} {
		require.NoError(t, FixtureEndpoint(raw))
	}
	for _, raw := range []string{"https://coding.baseten.co", "http://localhost:1234", "http://127.0.0.1:1234/v1", "http://token@127.0.0.1:1234"} {
		require.Error(t, FixtureEndpoint(raw))
	}
	for _, name := range []string{"", "opus", "with space", "acme/primary"} {
		routes := fixture(t)
		routes = append(routes, Route{Name: name, DisplayName: "bad"})
		_, e := ValidateCatalog(routes)
		require.Error(t, e)
	}
	routes := fixture(t)
	routes[0].DisplayName = ""
	_, e := ValidateCatalog(routes)
	require.Error(t, e)
	_, e = (Selection{Primary: "missing"}).Resolve(fixture(t))
	require.Error(t, e)

}
func mustJSON(t *testing.T, v any) []byte { b, e := json.Marshal(v); require.NoError(t, e); return b }
func TestRolesAndPicker(t *testing.T) {
	values, e := ClaudeSettings(fixture(t), Selection{Primary: "acme/primary", Background: "acme/background", Subagent: "acme/subagent", Fallback: "acme/fallback"}, "http://127.0.0.1:1234", FixtureToken, true, map[string]any{}, nil)
	require.NoError(t, e)
	d := map[string]any{}
	for _, v := range values {
		require.NoError(t, put(d, v.Path, v.Installed))
	}
	require.Equal(t, "acme/subagent", get(d, []string{"env", "CLAUDE_CODE_SUBAGENT_MODEL"}).Data)
	require.False(t, get(d, []string{"env", "CLAUDE_CODE_SUBAGENT_MODEL_FORCE"}).Exists)
	require.Equal(t, "deepseek-ai/DeepSeek-V4.1-Flash", get(d, []string{"env", "ANTHROPIC_DEFAULT_HAIKU_MODEL"}).Data)
	require.False(t, get(d, []string{"env", "ANTHROPIC_MODEL"}).Exists)
	require.True(t, get(d, []string{"modelPicker", "replaceBuiltInOptions"}).Data.(bool))
	b := string(mustJSON(t, d))
	require.False(t, strings.Contains(b, "claude-sonnet"))
}

func TestPickerEditBlocksRefreshAndSurvivesTeardown(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	require.NoError(t, setup(t, path, fixture(t), false).Apply())
	d := load(t, path)
	require.NoError(t, put(d, []string{"modelPicker", "options"}, Value{Exists: true, Data: []any{map[string]any{"model": "user/custom", "label": "Custom"}}}))
	save(t, path, d)
	_, e := Prepare(path, fixture(t), Selection{Primary: "acme/primary"}, "http://127.0.0.1:1234", FixtureToken, false, false)
	require.ErrorContains(t, e, "user changed modelPicker.options")
	p, e := PrepareTeardown(path)
	require.NoError(t, e)
	require.Contains(t, p.Conflicts, "modelPicker.options")
	require.NoError(t, p.Apply())
	require.Equal(t, "user/custom", get(load(t, path), []string{"modelPicker", "options"}).Data.([]any)[0].(map[string]any)["model"])
}

func TestClaudePickerUsesModelAndPreservesCustomRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	original := map[string]any{"modelPicker": map[string]any{"options": []any{
		map[string]any{"model": "acme/primary", "label": "My label", "description": "My description"},
	}}}
	save(t, path, original)
	plan := setup(t, path, fixture(t), false)
	require.NoError(t, plan.Apply())
	rows := get(load(t, path), []string{"modelPicker", "options"}).Data.([]any)
	require.Len(t, rows, len(fixture(t)))
	require.Equal(t, original["modelPicker"].(map[string]any)["options"].([]any)[0], rows[0])
	for _, row := range rows {
		m := row.(map[string]any)
		require.NotEmpty(t, m["model"])
		require.NotContains(t, m, "id")
	}
	// Simulate a config and journal written by the previous adapter.
	_, data, _, prior, err := Read(path)
	require.NoError(t, err)
	for _, row := range rows[1:] {
		m := row.(map[string]any)
		m["id"] = m["model"]
		delete(m, "model")
	}
	require.NoError(t, put(data, []string{"modelPicker", "options"}, Value{Exists: true, Data: rows}))
	save(t, path, data)
	for i := range prior.Settings {
		if pathKey(prior.Settings[i].Path) == "modelPicker.options" {
			prior.Settings[i].Installed.Data = rows
		}
	}
	journal, err := encode(prior)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(JournalPath(path), journal, 0600))
	require.NoError(t, setup(t, path, fixture(t), false).Apply())
	for _, row := range get(load(t, path), []string{"modelPicker", "options"}).Data.([]any) {
		require.NotEmpty(t, row.(map[string]any)["model"])
		require.NotContains(t, row.(map[string]any), "id")
	}
	teardown, err := PrepareTeardown(path)
	require.NoError(t, err)
	require.NoError(t, teardown.Apply())
	require.Equal(t, original, load(t, path))
}

func TestConfirmedRefreshPreservesEditedPickerWithoutChangingRestorePoint(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	require.NoError(t, setup(t, path, fixture(t), false).Apply())
	data := load(t, path)
	edited := []any{map[string]any{"model": "user/custom", "label": "My custom model"}}
	require.NoError(t, put(data, []string{"modelPicker", "options"}, Value{Exists: true, Data: edited}))
	save(t, path, data)
	require.NoError(t, setup(t, path, fixture(t), true).Apply())
	rows := get(load(t, path), []string{"modelPicker", "options"}).Data.([]any)
	require.Equal(t, edited[0], rows[0])
	require.Len(t, rows, len(fixture(t))+1)
	teardown, err := PrepareTeardown(path)
	require.NoError(t, err)
	require.NoError(t, teardown.Apply())
	_, err = os.Stat(path)
	require.True(t, os.IsNotExist(err)) // No settings file existed before the first setup.
}

func TestSetupRepairsPermissionsWithoutContentChanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	require.NoError(t, setup(t, path, fixture(t), false).Apply())
	original, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, os.Chmod(path, 0644))
	plan := setup(t, path, fixture(t), false)
	require.True(t, plan.Changed)
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0644), info.Mode().Perm())
	require.NoError(t, plan.Apply())
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, original, data)
	info, err = os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0600), info.Mode().Perm())
	require.False(t, setup(t, path, fixture(t), false).Changed)
}

func TestRefreshKeepsFirstSetupRestorePoint(t *testing.T) {
	for _, filename := range []string{"settings.json", "opencode.jsonc", "config.toml"} {
		t.Run(filename, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), filename)
			original, err := encodeConfig(path, map[string]any{"model": "original", "subagent": "original-subagent", "theme": "dark"}, nil)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(path, original, 0600))
			apply := func(settings []Setting) {
				t.Helper()
				p, err := prepareSettings(path, []Route{{Name: "updated-route"}}, true, func(map[string]any, *Journal) ([]Setting, error) { return settings, nil })
				require.NoError(t, err)
				require.NoError(t, p.Apply())
				_, _, _, journal, err := Read(path)
				require.NoError(t, err)
				require.Equal(t, original, journal.Original)
			}
			apply([]Setting{desired([]string{"model"}, "first-route")})
			// Edits after the first setup must never replace the restore point.
			_, current, _, _, err := Read(path)
			require.NoError(t, err)
			current["model"] = "session-choice"
			current["subagent"] = "later-choice"
			current["theme"] = "light"
			edited, err := encodeConfig(path, current, nil)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(path, edited, 0600))
			apply([]Setting{desired([]string{"model"}, "second-route"), desired([]string{"subagent"}, "managed-subagent")})
			// Omitting a previous optional setting must not lose its restoration record.
			apply([]Setting{desired([]string{"model"}, "latest-route")})
			_, current, _, journal, err := Read(path)
			require.NoError(t, err)
			require.Equal(t, "latest-route", current["model"])
			require.Len(t, journal.Settings, 2)
			p, err := PrepareTeardown(path)
			require.NoError(t, err)
			require.Empty(t, p.Conflicts)
			require.NoError(t, p.Apply())
			_, restored, _, _, err := Read(path)
			require.NoError(t, err)
			require.Equal(t, "original", restored["model"])
			require.Equal(t, "original-subagent", restored["subagent"])
			require.Equal(t, "light", restored["theme"])
		})
	}
}

func TestTeardownPreservesJSONCCommentOnlyEdit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.jsonc")
	require.NoError(t, os.WriteFile(path, []byte("{\n // original comment\n \"theme\": \"dark\",\n}\n"), 0600))
	plans, err := PrepareHarness("opencode", path, fixture(t), Selection{Primary: "acme/primary"}, "http://127.0.0.1:1234", FixtureToken, false, true)
	require.NoError(t, err)
	require.NoError(t, ApplyPlans(plans))
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, bytes.Replace(data, []byte("original comment"), []byte("updated user comment"), 1), 0600))
	plans, err = PrepareHarnessTeardown("opencode", path)
	require.NoError(t, err)
	require.NoError(t, ApplyPlans(plans))
	data, err = os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(data), "updated user comment")
}

func TestCodexStatusReadsActualCatalog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	plans, err := PrepareHarness("codex", path, fixture(t), Selection{Primary: "acme/primary"}, "http://127.0.0.1:1234", FixtureToken, false, true)
	require.NoError(t, err)
	require.NoError(t, ApplyPlans(plans))
	save(t, CatalogPath(path), map[string]any{"models": []any{}})
	status, err := Inspect(Detection{Name: "codex", Path: path})
	require.NoError(t, err)
	require.Equal(t, "drifted", status.State)
	require.Empty(t, status.Routes, "no configured routes remain in the actual catalog")
	require.Empty(t, status.RouteDetails)
}
