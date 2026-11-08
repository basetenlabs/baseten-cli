// Package codex writes Codex's config.toml and model catalogs.
//
// It copies file plumbing from internal/harness; Grid's Codex harness has not
// adopted it yet.
package codex

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
)

// Dir returns CODEX_HOME, else ~/.codex.
func Dir() (string, error) {
	if dir := os.Getenv("CODEX_HOME"); dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	return filepath.Join(home, ".codex"), err
}

// Provider returns a model_providers entry for a provider that serves the
// OpenAI Responses API at baseURL.
func Provider(name, baseURL string) map[string]any {
	return map[string]any{"name": name, "base_url": baseURL, "wire_api": "responses"}
}

// ConfigChange lists dotted config.toml keys by what Patch did to them.
type ConfigChange struct {
	Added     []string `json:"added"`
	Preserved []string `json:"preserved"`
	Replaced  []string `json:"replaced"`
}

// Patch merges two nested maps of settings into the TOML file at path. Keys in
// add are written only when missing; keys in set are always written. Tables
// merge key by key. The file is written only when write is set and something
// changed.
func Patch(path string, add, set map[string]any, write bool) (ConfigChange, error) {
	change := ConfigChange{Added: []string{}, Preserved: []string{}, Replaced: []string{}}
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return change, err
	}
	config := map[string]any{}
	if _, err := toml.Decode(string(data), &config); err != nil {
		return change, errors.New("invalid " + path + "; repair it before continuing")
	}
	merge(config, set, "", true, &change)
	merge(config, add, "", false, &change)
	if !write || len(change.Added)+len(change.Replaced) == 0 {
		return change, nil
	}
	// Re-encoding drops comments and reorders keys.
	var b bytes.Buffer
	if err := toml.NewEncoder(&b).Encode(config); err != nil {
		return change, err
	}
	return change, writeFile(path, b.Bytes())
}

func merge(dst, src map[string]any, prefix string, overwrite bool, change *ConfigChange) {
	for _, k := range slices.Sorted(maps.Keys(src)) {
		key := strings.TrimPrefix(prefix+"."+k, ".")
		current, exists := dst[k]
		child, isTable := src[k].(map[string]any)
		existing, wasTable := current.(map[string]any)
		switch {
		case isTable && len(child) > 0 && !exists:
			// Report a new table by its keys.
			existing = map[string]any{}
			dst[k] = existing
			fallthrough
		case isTable && len(child) > 0 && wasTable:
			merge(existing, child, key, overwrite, change)
		case !exists:
			dst[k] = src[k]
			change.Added = append(change.Added, key)
		case isTable && wasTable:
			// An empty table only ensures the table exists.
		case !overwrite || reflect.DeepEqual(current, src[k]):
			change.Preserved = append(change.Preserved, key)
		default:
			dst[k] = src[k]
			change.Replaced = append(change.Replaced, key)
		}
	}
}

// CatalogDiff lists catalog slugs by how they changed.
type CatalogDiff struct {
	Added    []string `json:"added"`
	Removed  []string `json:"removed"`
	Modified []string `json:"modified"`
}

// WriteCatalog compares models with the catalog at path, which Baseten owns
// entirely, and replaces it when write is set and it differs. Each model needs
// a string "slug".
func WriteCatalog(path string, models []map[string]any, write bool) (CatalogDiff, error) {
	diff := CatalogDiff{Added: []string{}, Removed: []string{}, Modified: []string{}}
	if models == nil {
		// Codex needs a list, not null.
		models = []map[string]any{}
	}
	data, err := json.MarshalIndent(map[string]any{"models": models}, "", "  ")
	if err != nil {
		return diff, err
	}
	data = append(data, '\n')
	current, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return diff, err
	}
	before := catalogEntries(current)
	after := catalogEntries(data)
	for _, slug := range slices.Sorted(maps.Keys(after)) {
		old, ok := before[slug]
		switch {
		case !ok:
			diff.Added = append(diff.Added, slug)
		case old != after[slug]:
			diff.Modified = append(diff.Modified, slug)
		}
	}
	for _, slug := range slices.Sorted(maps.Keys(before)) {
		if _, ok := after[slug]; !ok {
			diff.Removed = append(diff.Removed, slug)
		}
	}
	if !write || bytes.Equal(current, data) {
		return diff, nil
	}
	return diff, writeFile(path, data)
}

// catalogEntries maps each slug in a catalog to its canonical JSON. An
// unreadable catalog has no entries, so it is replaced.
func catalogEntries(data []byte) map[string]string {
	var catalog struct {
		Models []map[string]any `json:"models"`
	}
	_ = json.Unmarshal(data, &catalog)
	entries := map[string]string{}
	for _, m := range catalog.Models {
		slug, _ := m["slug"].(string)
		b, _ := json.Marshal(m)
		entries[cmp.Or(slug, string(b))] = string(b)
	}
	return entries
}

func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
