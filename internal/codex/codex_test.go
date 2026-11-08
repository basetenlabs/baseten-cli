package codex

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/stretchr/testify/require"
)

func TestPatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	require.NoError(t, os.WriteFile(path, []byte("model = \"gpt-5\"\n[model_providers.mine]\nname = \"Mine\"\n[model_providers.mine.http_headers]\nId = \"old\"\n"), 0o644))
	add := map[string]any{
		"model": "ignored",
		"model_providers": map[string]any{
			"mine":  map[string]any{"name": "Other", "base_url": "https://x/v1", "http_headers": map[string]any{}},
			"added": map[string]any{"name": "Added", "http_headers": map[string]any{}},
		},
	}
	set := map[string]any{"model_providers": map[string]any{"mine": map[string]any{"http_headers": map[string]any{"Id": "new"}}}}

	change, err := Patch(path, add, set, false)
	require.NoError(t, err)
	require.Equal(t, ConfigChange{
		Added:     []string{"model_providers.added.http_headers", "model_providers.added.name", "model_providers.mine.base_url"},
		Preserved: []string{"model", "model_providers.mine.name"},
		Replaced:  []string{"model_providers.mine.http_headers.Id"},
	}, change)
	unchanged, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(unchanged), "Id = \"old\"", "dry run writes nothing")

	_, err = Patch(path, add, set, true)
	require.NoError(t, err)
	first, err := os.ReadFile(path)
	require.NoError(t, err)
	var config map[string]any
	_, err = toml.Decode(string(first), &config)
	require.NoError(t, err)
	require.Equal(t, "gpt-5", config["model"])
	providers := config["model_providers"].(map[string]any)
	require.Equal(t, map[string]any{"name": "Mine", "base_url": "https://x/v1", "http_headers": map[string]any{"Id": "new"}}, providers["mine"])
	require.Equal(t, map[string]any{"name": "Added", "http_headers": map[string]any{}}, providers["added"])

	change, err = Patch(path, add, set, true)
	require.NoError(t, err)
	require.Empty(t, change.Added)
	require.Empty(t, change.Replaced)
	second, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, string(first), string(second))
}

func TestPatchRejectsInvalidTOML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	require.NoError(t, os.WriteFile(path, []byte("model = \n"), 0o644))
	_, err := Patch(path, map[string]any{"a": "b"}, nil, true)
	require.Error(t, err)
}

func TestWriteCatalog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "codex", "catalog.json")
	models := []map[string]any{{"slug": "a", "priority": 1}, {"slug": "b", "priority": 2}}

	diff, err := WriteCatalog(path, models, false)
	require.NoError(t, err)
	require.Equal(t, []string{"a", "b"}, diff.Added)
	require.NoFileExists(t, path)

	_, err = WriteCatalog(path, models, true)
	require.NoError(t, err)
	diff, err = WriteCatalog(path, models, true)
	require.NoError(t, err)
	require.Equal(t, CatalogDiff{Added: []string{}, Removed: []string{}, Modified: []string{}}, diff)

	diff, err = WriteCatalog(path, []map[string]any{{"slug": "b", "priority": 1}, {"slug": "c", "priority": 2}}, true)
	require.NoError(t, err)
	require.Equal(t, CatalogDiff{Added: []string{"c"}, Removed: []string{"a"}, Modified: []string{"b"}}, diff)
}
