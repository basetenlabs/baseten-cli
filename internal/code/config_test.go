package code

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func testHarness(t *testing.T, name, original string) Harness {
	t.Helper()
	path := filepath.Join(t.TempDir(), "settings.json")
	if name == "codex" {
		path = filepath.Join(filepath.Dir(path), "config.toml")
	}
	if original != "" {
		require.NoError(t, os.WriteFile(path, []byte(original), 0640))
	}
	return Harness{Name: name, Path: path, Installed: true, Version: "test"}
}
func prepare(t *testing.T, h Harness, prior *ManagedConfig) *Change {
	t.Helper()
	change, err := Prepare(h, "engineering", "/opt/baseten cli", t.TempDir(), "org-1", prior)
	require.NoError(t, err)
	return change
}
func installation() *Installation {
	return &Installation{Version: 1, Org: "org-1", KeyID: "key-1", Model: "engineering", Configs: map[string]*ManagedConfig{}}
}

func TestConfigPreservesAndRestores(t *testing.T) {
	for _, name := range []string{"codex", "claude-code"} {
		t.Run(name, func(t *testing.T) {
			original := "{\n \"theme\":\"dark\",\"model\":\"previous\",\"env\":{\"EXAMPLE\":\"keep\"}\n}\n"
			if name == "codex" {
				original = "# my comment\nmodel = \"previous\"\napproval_policy = \"on-request\"\n[model_providers.other]\nname = \"keep\"\n"
			}
			h := testHarness(t, name, original)
			s := &Store{Dir: t.TempDir()}
			i := installation()
			change := prepare(t, h, nil)
			preview, err := json.Marshal(change)
			require.NoError(t, err)
			require.NotContains(t, string(preview), "Before")
			require.NotContains(t, string(preview), "previous")
			require.NoError(t, s.Apply(i, []*Change{change}))
			written, err := os.ReadFile(h.Path)
			require.NoError(t, err)
			require.Contains(t, string(written), Endpoint)
			require.NotContains(t, string(written), "web-search-provider-priority")
			require.Contains(t, string(written), "--config-dir")
			require.Contains(t, string(written), "--org")
			data, err := decode(change.managed.Format, written)
			require.NoError(t, err)
			if name == "codex" {
				require.Equal(t, "on-request", data["approval_policy"])
				require.Contains(t, string(written), "responses")
				require.NotContains(t, string(written), "requires_openai_auth")
			} else {
				require.Equal(t, "dark", data["theme"])
				require.Equal(t, "keep", get(data, []string{"env", "EXAMPLE"}).Data)
			}
			// Re-read the serialized journal to exercise JSON/TOML type normalization.
			loaded, err := s.Load()
			require.NoError(t, err)

			// Same state dir and executable must yield a no-op config write.
			var helperDir string
			if name == "codex" {
				provider := get(data, []string{"model_providers", "baseten-code"}).Data.(map[string]any)
				args := provider["auth"].(map[string]any)["args"].([]any)
				helperDir = args[4].(string)
			} else {
				helperDir = "unused"
			}
			if name == "codex" {
				again, err := Prepare(h, "engineering", "/opt/baseten cli", helperDir, "org-1", loaded.Configs[name])
				require.NoError(t, err)
				require.Equal(t, written, again.data)
			}
			restored, conflicts, err := Restore(loaded.Configs[name])
			require.NoError(t, err)
			require.Empty(t, conflicts)
			require.NoError(t, s.Teardown(loaded, []*Change{restored}))
			back, err := os.ReadFile(h.Path)
			require.NoError(t, err)
			require.Equal(t, original, string(back))
			info, err := os.Stat(h.Path)
			require.NoError(t, err)
			if info.Mode().Perm() != 0666 {
				require.Equal(t, os.FileMode(0640), info.Mode().Perm())
			}
		})
	}
}
func TestConfigPreservesUserEditsOnTeardown(t *testing.T) {
	h := testHarness(t, "claude-code", `{"model":"old","env":{"UNRELATED":"keep"}}`)
	s := &Store{Dir: t.TempDir()}
	i := installation()
	change := prepare(t, h, nil)
	require.NoError(t, s.Apply(i, []*Change{change}))
	data, err := decode("json", change.data)
	require.NoError(t, err)
	data["model"] = "user-choice"
	data["extra"] = "later"
	edited, err := encode("json", data)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(h.Path, edited, 0600))
	_, err = Prepare(h, "new", "/opt/baseten cli", "state", "org-1", i.Configs[h.Name])
	require.ErrorContains(t, err, "user changed")
	restore, conflicts, err := Restore(i.Configs[h.Name])
	require.NoError(t, err)
	require.Equal(t, []string{"model"}, conflicts)
	require.NoError(t, s.Teardown(i, []*Change{restore}))
	raw, err := os.ReadFile(h.Path)
	require.NoError(t, err)
	data, err = decode("json", raw)
	require.NoError(t, err)
	require.Equal(t, "user-choice", data["model"])
	require.Equal(t, "later", data["extra"])
	require.NotContains(t, data, "apiKeyHelper")
	require.Len(t, i.Configs[h.Name].Settings, 1)
}
func TestConfigRejectsConflictsAndInvalidSyntax(t *testing.T) {
	for _, original := range []string{`{"apiKeyHelper":"other"}`, `{"env":{"ANTHROPIC_AUTH_TOKEN":"secret"}}`, `{"env":null}`, `{broken`} {
		h := testHarness(t, "claude-code", original)
		_, err := Prepare(h, "route", "helper", "state", "org", nil)
		require.Error(t, err)
		require.NotContains(t, err.Error(), "secret")
		raw, err := os.ReadFile(h.Path)
		require.NoError(t, err)
		require.Equal(t, original, string(raw))
	}
	h := testHarness(t, "codex", "profile = \"work\"\n")
	_, err := Prepare(h, "route", "helper", "state", "org", nil)
	require.ErrorContains(t, err, "profile override")
	h = testHarness(t, "claude-code", `{}`)
	require.NoError(t, os.WriteFile(filepath.Join(filepath.Dir(h.Path), "managed-settings.json"), []byte(`{}`), 0600))
	_, err = Prepare(h, "route", "helper", "state", "org", nil)
	require.ErrorContains(t, err, "managed policy")
}
func TestSnapshotConcurrentEditsAndSymlinks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	require.NoError(t, os.WriteFile(path, []byte("before"), 0600))
	snap, err := ReadSnapshot(path)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, []byte("concurrent"), 0600))
	require.ErrorContains(t, snap.Write([]byte("ours")), "concurrently")
	link := filepath.Join(filepath.Dir(path), "link")
	if err := os.Symlink(path, link); err != nil {
		t.Skip("symlinks unavailable")
	}
	snap, err = ReadSnapshot(link)
	require.NoError(t, err)
	require.NoError(t, snap.Write([]byte("through-link")))
	info, err := os.Lstat(link)
	require.NoError(t, err)
	require.NotZero(t, info.Mode()&os.ModeSymlink)
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "through-link", string(raw))
	dangling := filepath.Join(filepath.Dir(path), "dangling")
	require.NoError(t, os.Symlink(path+"-missing", dangling))
	_, err = ReadSnapshot(dangling)
	require.ErrorContains(t, err, "dangling")
}
func TestApplyPreflightsAllFiles(t *testing.T) {
	h1 := testHarness(t, "codex", "")
	h2 := testHarness(t, "claude-code", `{}`)
	a := prepare(t, h1, nil)
	b := prepare(t, h2, nil)
	require.NoError(t, os.WriteFile(h2.Path, []byte(`{"changed":true}`), 0600))
	s := &Store{Dir: t.TempDir()}
	require.ErrorContains(t, s.Apply(installation(), []*Change{a, b}), "concurrently")
	_, err := os.Stat(h1.Path)
	require.True(t, os.IsNotExist(err))
	_, err = os.Stat(s.Path())
	require.True(t, os.IsNotExist(err))
}
func TestInterruptedJournalCanRestoreUnwrittenSettings(t *testing.T) {
	h := testHarness(t, "claude-code", `{"model":"old"}`)
	c := prepare(t, h, nil)
	restore, conflicts, err := Restore(c.managed)
	require.NoError(t, err)
	require.Empty(t, conflicts)
	require.Empty(t, restore.managed.Settings)
}
func TestConsolidatesCodexSurfaces(t *testing.T) {
	names, err := Normalize([]string{"codex-desktop", "codex", "claude-code"})
	require.NoError(t, err)
	require.Equal(t, []string{"claude-code", "codex"}, names)
}
func TestStoreLock(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	unlock, err := s.Lock()
	require.NoError(t, err)
	_, err = s.Lock()
	require.Error(t, err)
	unlock()
	unlock, err = s.Lock()
	require.NoError(t, err)
	unlock()
}

func TestTeardownRemovesOnlyNewEmptyFile(t *testing.T) {
	h := testHarness(t, "codex", "")
	s := &Store{Dir: t.TempDir()}
	i := installation()
	c := prepare(t, h, nil)
	require.NoError(t, s.Apply(i, []*Change{c}))
	restore, conflicts, err := Restore(i.Configs[h.Name])
	require.NoError(t, err)
	require.Empty(t, conflicts)
	require.NoError(t, s.Teardown(i, []*Change{restore}))
	_, err = os.Stat(h.Path)
	require.True(t, os.IsNotExist(err))
}
