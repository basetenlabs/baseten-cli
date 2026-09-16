package harnesspoc

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Mode selects where a harness's model list comes from.
type Mode string

const (
	// ModeLocal writes the model list into the harness's own configuration.
	ModeLocal Mode = "local"
	// ModeRemote leaves the list to the harness's discovery against the
	// gateway, where the harness supports it.
	ModeRemote Mode = "remote"
)

// SetupOptions is what every harness needs to write its configuration.
type SetupOptions struct {
	Root            string
	BaseURL         string
	APIKey          string
	Mode            Mode
	ReplaceBuiltins bool
	Catalog         Catalog
}

// SetupResult reports what one harness's setup wrote.
type SetupResult struct {
	Harness string
	Paths   []string
	Launch  string
	Notes   []string
}

// Harness is one configurable agentic harness.
type Harness interface {
	// Name is the value accepted by --harness.
	Name() string
	// Setup writes the harness's isolated configuration.
	Setup(opts SetupOptions) (SetupResult, error)
}

// All returns every supported harness, in --harness name order.
func All() []Harness {
	return []Harness{claudeCode{}}
}

// Select returns the harnesses named, or all of them when names is empty.
func Select(names []string) ([]Harness, error) {
	all := All()
	if len(names) == 0 {
		return all, nil
	}
	var out []Harness
	for _, name := range names {
		idx := slices.IndexFunc(all, func(h Harness) bool { return h.Name() == name })
		if idx < 0 {
			supported := make([]string, len(all))
			for i, h := range all {
				supported[i] = h.Name()
			}
			return nil, fmt.Errorf("unknown harness %q; supported: %s", name, strings.Join(supported, ", "))
		}
		out = append(out, all[idx])
	}
	return out, nil
}

// writeJSON writes v to path as indented JSON, creating parent directories.
func writeJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding %s: %w", path, err)
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}
