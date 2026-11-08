package codexconfig

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAddMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	require.NoError(t, os.WriteFile(path, []byte("model = \"gpt-5\"\n[model_providers.baseten]\nname = \"Mine\"\n"), 0o644))
	values := map[string]any{
		"model":           "ignored",
		"model_providers": map[string]any{"baseten": map[string]any{"name": "Baseten", "base_url": "https://x/v1"}},
		"profile":         "baseten",
	}

	added, err := AddMissing(path, values)
	require.NoError(t, err)
	require.Equal(t, []string{"model_providers.baseten.base_url", "profile"}, added)
	first, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(first), `model = "gpt-5"`)
	require.Contains(t, string(first), `name = "Mine"`)

	added, err = AddMissing(path, values)
	require.NoError(t, err)
	require.Empty(t, added)
	second, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, first, second)
}

func TestAddMissingRejectsInvalidTOML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	require.NoError(t, os.WriteFile(path, []byte("model = \n"), 0o644))
	_, err := AddMissing(path, map[string]any{"a": "b"})
	require.Error(t, err)
}

func TestWriteCatalog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "codex", "catalog.json")
	models := []any{map[string]any{"slug": "a"}}
	changed, err := WriteCatalog(path, models)
	require.NoError(t, err)
	require.True(t, changed)
	changed, err = WriteCatalog(path, models)
	require.NoError(t, err)
	require.False(t, changed)
}
