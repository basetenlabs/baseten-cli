package code

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

var Harnesses = []string{"codex", "codex-desktop", "claude-code", "claude-desktop", "opencode"}

func Canonical(h string) (string, error) {
	for _, name := range Harnesses {
		if h == name {
			if h == "codex-desktop" {
				return "codex", nil
			}
			return h, nil
		}
	}
	return "", fmt.Errorf("unknown harness %q; use %s", h, strings.Join(Harnesses, ", "))
}
func Normalize(names []string) ([]string, error) {
	out := []string{}
	seen := map[string]bool{}
	for _, h := range names {
		h, err := Canonical(h)
		if err != nil {
			return nil, err
		}
		if !seen[h] {
			out = append(out, h)
			seen[h] = true
		}
	}
	sort.Strings(out)
	return out, nil
}

type Harness struct {
	Name       string `json:"name"`
	Path       string `json:"config_path"`
	Installed  bool   `json:"installed"`
	Version    string `json:"version"`
	Limitation string `json:"limitation,omitempty"`
}

func Describe(ctx context.Context, h string) (Harness, error) {
	h, err := Canonical(h)
	if err != nil {
		return Harness{}, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return Harness{}, err
	}
	info := Harness{Name: h, Version: "unknown"}
	binary := h
	switch h {
	case "codex":
		dir := os.Getenv("CODEX_HOME")
		if dir == "" {
			dir = filepath.Join(home, ".codex")
		}
		info.Path = filepath.Join(dir, "config.toml")
		info.Limitation = "CLI and desktop share this file; desktop provider selection and minimum supported versions require validation."
	case "claude-code":
		binary = "claude"
		dir := os.Getenv("CLAUDE_CONFIG_DIR")
		if dir == "" {
			dir = filepath.Join(home, ".claude")
		}
		info.Path = filepath.Join(dir, "settings.json")
		info.Limitation = "Explicit Route selection is supported; gateway discovery filters Route IDs and does not provide active-session refresh."
	case "claude-desktop":
		info.Path = filepath.Join(home, "Library", "Application Support", "Claude-3p", "configLibrary")
		info.Limitation = "Desktop installation is not implemented: third-party profile schema, credential storage and arbitrary Route discovery require version validation."
		return info, nil
	case "opencode":
		dir := os.Getenv("XDG_CONFIG_HOME")
		if dir == "" {
			dir = filepath.Join(home, ".config")
		}
		info.Path = filepath.Join(dir, "opencode", "opencode.json")
		info.Limitation = "OpenCode configuration and credential delivery are not implemented; V1/V2 schemas and JSONC need a verified adapter."
	}
	info.Path, err = filepath.Abs(info.Path)
	if err != nil {
		return info, err
	}
	path, err := exec.LookPath(binary)
	if err != nil {
		return info, nil
	}
	info.Installed = true
	versionCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	// --version only, never start an interactive harness or a coding session.
	data, err := exec.CommandContext(versionCtx, path, "--version").Output()
	if err == nil {
		version := strings.TrimSpace(string(data))
		if len(version) < 120 && !strings.ContainsAny(version, "\r\n\x1b") {
			info.Version = version
		}
	}
	return info, nil
}

type Value struct {
	Exists bool `json:"exists"`
	Data   any  `json:"data,omitempty"`
}
type Setting struct {
	Path      []string `json:"path"`
	Before    Value    `json:"before"`
	Installed Value    `json:"installed"`
}
type ManagedConfig struct {
	Harness  string    `json:"harness"`
	Path     string    `json:"path"`
	Format   string    `json:"format"`
	Original []byte    `json:"original"`
	Existed  bool      `json:"existed"`
	Settings []Setting `json:"settings"`
}

// Change never serializes file contents. Configs and backups can contain
// existing secrets; previews report paths and setting names only.
type Change struct {
	Harness  string   `json:"harness"`
	Path     string   `json:"path"`
	Keys     []string `json:"settings"`
	snapshot *Snapshot
	data     []byte
	managed  *ManagedConfig
}

func decode(format string, data []byte) (map[string]any, error) {
	out := map[string]any{}
	if len(bytes.TrimSpace(data)) == 0 {
		return out, nil
	}
	var err error
	if format == "toml" {
		_, err = toml.Decode(string(data), &out)
	} else {
		err = json.Unmarshal(data, &out)
	}
	if err != nil || out == nil {
		return nil, errors.New("invalid configuration syntax; repair the file before continuing")
	}
	return out, nil
}
func encode(format string, data map[string]any) ([]byte, error) {
	if format == "toml" {
		var b bytes.Buffer
		err := toml.NewEncoder(&b).Encode(data)
		return b.Bytes(), err
	}
	b, err := json.MarshalIndent(data, "", "  ")
	return append(b, '\n'), err
}
func get(data map[string]any, path []string) Value {
	for _, p := range path[:len(path)-1] {
		child, ok := data[p].(map[string]any)
		if !ok {
			return Value{}
		}
		data = child
	}
	v, ok := data[path[len(path)-1]]
	return Value{Exists: ok, Data: v}
}
func set(data map[string]any, path []string, value Value) error {
	if len(path) == 1 {
		if value.Exists {
			data[path[0]] = value.Data
		} else {
			delete(data, path[0])
		}
		return nil
	}
	child, ok := data[path[0]].(map[string]any)
	if !ok {
		if _, exists := data[path[0]]; exists {
			return fmt.Errorf("setting %s conflicts with an existing scalar", path[0])
		}
		if !value.Exists {
			return nil
		}
		child = map[string]any{}
		data[path[0]] = child
	}
	if err := set(child, path[1:], value); err != nil {
		return err
	}
	if len(child) == 0 {
		delete(data, path[0])
	}
	return nil
}
func equal(a, b Value) bool {
	// JSON normalizes numbers and nested arrays when a journal is reloaded.
	aa, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return bytes.Equal(aa, bb)
}
func setting(path []string, v any) Setting {
	return Setting{Path: path, Installed: Value{Exists: true, Data: v}}
}

func Prepare(h Harness, model, helper, stateDir, org string, prior *ManagedConfig) (*Change, error) {
	if h.Name == "claude-desktop" {
		return nil, errors.New(h.Limitation)
	}
	if !h.Installed {
		return nil, fmt.Errorf("%s is not installed or not on PATH", h.Name)
	}
	snap, err := ReadSnapshot(h.Path)
	if err != nil {
		return nil, err
	}
	format := "json"
	if h.Name == "codex" {
		format = "toml"
	}
	data, err := decode(format, snap.Data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", h.Path, err)
	}
	// Configuration managed by organizations is never changed. A known local
	// managed-policy file makes setup conservative until policy resolution is
	// integrated; no attempt is made to weaken or overwrite policy.
	policyPaths := []string{}
	switch h.Name {
	case "codex":
		policyPaths = []string{filepath.Join(filepath.Dir(h.Path), "managed_config.toml"), "/etc/codex/managed_config.toml", "/etc/codex/requirements.toml"}
	case "claude-code":
		policyPaths = []string{filepath.Join(filepath.Dir(h.Path), "managed-settings.json"), "/Library/Application Support/ClaudeCode/managed-settings.json", "/etc/claude-code/managed-settings.json"}
	}
	for _, path := range policyPaths {
		if _, err := os.Stat(path); err == nil {
			return nil, fmt.Errorf("managed policy detected at %s; ask your administrator to configure Baseten Code", path)
		} else if !os.IsNotExist(err) {
			return nil, err
		}
	}
	var settings []Setting
	switch h.Name {
	case "codex":
		if value := get(data, []string{"profile"}); value.Exists {
			return nil, errors.New("Codex has a selected profile override; resolve it before configuring the shared CLI/desktop provider")
		}
		if value := get(data, []string{"model_catalog_json"}); value.Exists {
			return nil, errors.New("Codex has a model_catalog_json override; resolve it before selecting a Code Route")
		}
		settings = []Setting{
			setting([]string{"model"}, model), setting([]string{"model_provider"}, "baseten-code"),
			setting([]string{"model_providers", "baseten-code"}, map[string]any{
				"name": "Baseten Code", "base_url": Endpoint + "/v1", "wire_api": "responses",
				"auth": map[string]any{"command": helper, "args": []string{"code", "auth", "token", "--config-dir", stateDir, "--org", org}},
			}),
		}
	case "claude-code":
		for _, name := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY"} {
			if value := get(data, []string{"env", name}); value.Exists {
				return nil, fmt.Errorf("Claude setting env.%s overrides gateway authentication; resolve it before setup", name)
			}
			if os.Getenv(name) != "" {
				return nil, fmt.Errorf("environment variable %s overrides gateway authentication; unset it before setup", name)
			}
		}
		settings = []Setting{setting([]string{"apiKeyHelper"}, shellQuote(helper)+" code auth token --config-dir "+shellQuote(stateDir)+" --org "+shellQuote(org)), setting([]string{"env", "ANTHROPIC_BASE_URL"}, Endpoint), setting([]string{"model"}, model)}
		for _, role := range []string{"ANTHROPIC_MODEL", "ANTHROPIC_DEFAULT_OPUS_MODEL", "ANTHROPIC_DEFAULT_SONNET_MODEL", "ANTHROPIC_DEFAULT_HAIKU_MODEL", "CLAUDE_CODE_SUBAGENT_MODEL", "ANTHROPIC_SMALL_FAST_MODEL"} {
			settings = append(settings, setting([]string{"env", role}, model))
		}
	case "opencode":
		// OpenCode's published V1 config has no command-backed credential field.
		// Do not write a key into config or pretend an environment reference works
		// for desktop launches. Add credential delivery with the versioned adapter.
		return nil, errors.New("OpenCode setup requires a verified V1/V2 credential-delivery and catalog adapter; no configuration was changed")
	}
	record := &ManagedConfig{Harness: h.Name, Path: h.Path, Format: format, Original: snap.Data, Existed: snap.Info != nil}
	if prior != nil {
		if prior.Path != h.Path || prior.Format != format {
			return nil, errors.New("harness config location changed; teardown the previous location before reinitializing")
		}
		record.Original, record.Existed = prior.Original, prior.Existed
	}
	keys := []string{}
	for _, s := range settings {
		before := get(data, s.Path)
		s.Before = before
		owned := false
		if prior != nil {
			for _, p := range prior.Settings {
				if reflect.DeepEqual(p.Path, s.Path) {
					owned = true
					if !equal(before, p.Installed) {
						return nil, fmt.Errorf("user changed %s in %s; preserve or restore that setting before syncing", strings.Join(s.Path, "."), h.Path)
					}
					s.Before = p.Before
				}
			}
		}
		// Do not take over a pre-existing provider or helper with different values.
		if !owned && before.Exists && (s.Path[0] == "model_providers" || s.Path[0] == "apiKeyHelper") && !equal(before, s.Installed) {
			return nil, fmt.Errorf("existing %s conflicts with Baseten Code; resolve it before setup", strings.Join(s.Path, "."))
		}
		if err := set(data, s.Path, s.Installed); err != nil {
			return nil, err
		}
		record.Settings = append(record.Settings, s)
		keys = append(keys, strings.Join(s.Path, "."))
	}
	output, err := encode(format, data)
	if err != nil {
		return nil, err
	}
	// A repeat with identical values does not rewrite comments/formatting.
	existing, _ := decode(format, snap.Data)
	if reflect.DeepEqual(existing, data) {
		output = snap.Data
	}
	return &Change{Harness: h.Name, Path: h.Path, Keys: keys, snapshot: snap, data: output, managed: record}, nil
}
func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }

// Apply journals original values before any harness write. If interrupted,
// teardown can conservatively restore only values that match the journal.
// All changes are preflighted before the first write.
func (s *Store) Apply(i *Installation, changes []*Change) error {
	for _, change := range changes {
		if err := change.snapshot.Check(); err != nil {
			return err
		}
	}
	for _, change := range changes {
		i.Configs[change.Harness] = change.managed
	}
	if err := s.Save(i); err != nil {
		return err
	}
	for _, change := range changes {
		if err := change.snapshot.Write(change.data); err != nil {
			return fmt.Errorf("setup interrupted: %w; backups were saved, use code teardown to recover", err)
		}
	}
	return nil
}

func Restore(record *ManagedConfig) (*Change, []string, error) {
	snap, err := ReadSnapshot(record.Path)
	if err != nil {
		return nil, nil, err
	}
	data, err := decode(record.Format, snap.Data)
	if err != nil {
		return nil, nil, err
	}
	remaining := *record
	remaining.Settings = nil
	conflicts := []string{}
	keys := []string{}
	for _, s := range record.Settings {
		current := get(data, s.Path)
		if equal(current, s.Before) {
			continue
		} // Journal may precede a failed write.
		if !equal(current, s.Installed) {
			remaining.Settings = append(remaining.Settings, s)
			conflicts = append(conflicts, strings.Join(s.Path, "."))
			continue
		}
		if err := set(data, s.Path, s.Before); err != nil {
			return nil, nil, err
		}
		keys = append(keys, strings.Join(s.Path, "."))
	}
	output, err := encode(record.Format, data)
	if err != nil {
		return nil, nil, err
	}
	original, err := decode(record.Format, record.Original)
	if err == nil && reflect.DeepEqual(data, original) {
		output = record.Original
	}
	if len(keys) == 0 {
		output = snap.Data
	}
	return &Change{Harness: record.Harness, Path: record.Path, Keys: keys, snapshot: snap, data: output, managed: &remaining}, conflicts, nil
}
func (s *Store) Teardown(i *Installation, changes []*Change) error {
	for _, change := range changes {
		if err := change.snapshot.Check(); err != nil {
			return err
		}
	}
	for _, change := range changes {
		// Remove a newly created empty file, but retain files with later user settings.
		if !change.managed.Existed && len(change.data) == 0 && len(change.managed.Settings) == 0 {
			if err := change.snapshot.Check(); err != nil {
				return err
			}
			if err := os.Remove(change.snapshot.Target); err != nil && !os.IsNotExist(err) {
				return err
			}
		} else if err := change.snapshot.Write(change.data); err != nil {
			return err
		}
		if len(change.managed.Settings) == 0 {
			delete(i.Configs, change.Harness)
		} else {
			i.Configs[change.Harness] = change.managed
		}
	}
	// Keep the installation credential until server revocation succeeds.
	return s.Save(i)
}
