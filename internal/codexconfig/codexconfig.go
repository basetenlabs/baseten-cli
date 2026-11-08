// Package codexconfig writes Codex's config.toml and model catalog without
// overwriting user settings.
//
// It copies the file plumbing in internal/harness; Grid's Codex harness has not
// adopted it yet.
package codexconfig

import (
	"bytes"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
)

// AddMissing adds each key in values to the TOML file at path unless the key is
// already set, and returns the dotted keys it added. Tables merge key by key;
// any other existing value is left untouched. The file is rewritten only when a
// key is added, so a second run is a no-op.
func AddMissing(path string, values map[string]any) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	config := map[string]any{}
	if _, err := toml.Decode(string(data), &config); err != nil {
		return nil, errors.New("invalid " + path + "; repair it before continuing")
	}
	added := addMissing(config, values, "")
	if len(added) == 0 {
		return nil, nil
	}
	// Re-encoding drops comments and reorders keys.
	var b bytes.Buffer
	if err := toml.NewEncoder(&b).Encode(config); err != nil {
		return nil, err
	}
	return added, write(path, b.Bytes())
}

func addMissing(dst, src map[string]any, prefix string) []string {
	var added []string
	for _, k := range slices.Sorted(maps.Keys(src)) {
		key := strings.TrimPrefix(prefix+"."+k, ".")
		current, exists := dst[k]
		if !exists {
			dst[k] = src[k]
			added = append(added, key)
			continue
		}
		child, isTable := src[k].(map[string]any)
		existing, wasTable := current.(map[string]any)
		if isTable && wasTable {
			added = append(added, addMissing(existing, child, key)...)
		}
	}
	return added
}

// WriteCatalog writes models as a Codex model catalog ({"models": [...]}) to
// path, which Baseten owns entirely. It reports whether the file changed.
func WriteCatalog(path string, models []any) (bool, error) {
	data, err := json.MarshalIndent(map[string]any{"models": models}, "", "  ")
	if err != nil {
		return false, err
	}
	data = append(data, '\n')
	if current, err := os.ReadFile(path); err == nil && bytes.Equal(current, data) {
		return false, nil
	}
	return true, write(path, data)
}

func write(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
