package harness

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/basetenlabs/baseten-cli/internal/safefile"
)

type Value struct {
	Exists bool `json:"exists"`
	Data   any  `json:"data,omitempty"`
}
type Setting struct {
	Path      []string `json:"path"`
	Before    Value    `json:"before"`
	Installed Value    `json:"installed"`
	Previous  Value    `json:"previous"`
}
type Journal struct {
	Routes   []string  `json:"routes"`
	Version  int       `json:"version"`
	Path     string    `json:"path"`
	Original []byte    `json:"original"`
	Existed  bool      `json:"existed"`
	Pending  bool      `json:"pending"`
	Settings []Setting `json:"settings"`
}

// Plan exposes paths and key names only: configuration and backups may contain
// credentials. Never include their values in JSON output or error messages.
type Plan struct {
	Managed                   bool     `json:"managed"`
	Path                      string   `json:"config"`
	Keys                      []string `json:"settings"`
	Changed                   bool     `json:"changed"`
	Conflicts                 []string `json:"conflicts,omitempty"`
	snapshot, journalSnapshot *safefile.Snapshot
	data                      []byte
	journal                   *Journal
	teardown                  bool
}

func JournalPath(path string) string { return path + ".baseten-harness.json" }
func pathKey(p []string) string      { return strings.Join(p, ".") }
func desired(p []string, v any) Setting {
	return Setting{Path: p, Installed: Value{Exists: true, Data: v}}
}
func same(a, b Value) bool {
	aa, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return bytes.Equal(aa, bb)
}
func get(d map[string]any, p []string) Value {
	for _, k := range p[:len(p)-1] {
		child, ok := d[k].(map[string]any)
		if !ok {
			return Value{}
		}
		d = child
	}
	v, ok := d[p[len(p)-1]]
	return Value{Exists: ok, Data: v}
}
func put(d map[string]any, p []string, v Value) error {
	if len(p) == 1 {
		if v.Exists {
			d[p[0]] = v.Data
		} else {
			delete(d, p[0])
		}
		return nil
	}
	child, ok := d[p[0]].(map[string]any)
	if !ok {
		if _, exists := d[p[0]]; exists {
			return fmt.Errorf("%s conflicts with a scalar", p[0])
		}
		if !v.Exists {
			return nil
		}
		child = map[string]any{}
		d[p[0]] = child
	}
	if err := put(child, p[1:], v); err != nil {
		return err
	}
	if len(child) == 0 {
		delete(d, p[0])
	}
	return nil
}
func decode(b []byte) (map[string]any, error) {
	d := map[string]any{}
	if len(bytes.TrimSpace(b)) == 0 {
		return d, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.UseNumber()
	if err := decoder.Decode(&d); err != nil || d == nil {
		return nil, errors.New("invalid settings JSON; repair it before continuing")
	}
	// json.Valid also rejects trailing JSON values.
	if !json.Valid(b) {
		return nil, errors.New("invalid settings JSON")
	}
	return d, nil
}
func encode(d any) ([]byte, error) {
	b, err := json.MarshalIndent(d, "", "  ")
	return append(b, '\n'), err
}
func Read(path string) (*safefile.Snapshot, map[string]any, *safefile.Snapshot, *Journal, error) {
	s, err := safefile.ReadSnapshot(path)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	d, err := decodeConfig(s.Path, s.Data)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	js, err := safefile.ReadSnapshot(JournalPath(s.Target))
	if err != nil {
		return nil, nil, nil, nil, err
	}
	if js.Info == nil {
		return s, d, js, nil, nil
	}
	if js.Info.Mode().Perm()&0077 != 0 {
		return nil, nil, nil, nil, errors.New("harness journal must be private (0600)")
	}
	var j Journal
	decoder := json.NewDecoder(bytes.NewReader(js.Data))
	decoder.UseNumber()
	if decoder.Decode(&j) != nil || j.Version != 1 || j.Path != s.Target {
		return nil, nil, nil, nil, errors.New("invalid harness ownership journal")
	}
	seen := map[string]bool{}
	for _, v := range j.Settings {
		if len(v.Path) == 0 || seen[pathKey(v.Path)] {
			return nil, nil, nil, nil, errors.New("invalid journal setting path")
		}
		seen[pathKey(v.Path)] = true
		for _, k := range v.Path {
			if k == "" {
				return nil, nil, nil, nil, errors.New("invalid journal setting path")
			}
		}
	}
	return s, d, js, &j, nil
}

func Prepare(path string, routes []Route, selection Selection, endpoint, token string, replacePicker, replaceExisting bool) (*Plan, error) {
	if err := CheckPolicy(path); err != nil {
		return nil, err
	}
	return prepareSettings(path, routes, replaceExisting, func(data map[string]any, j *Journal) ([]Setting, error) {
		return ClaudeSettings(routes, selection, endpoint, token, replacePicker, data, j)
	})
}
func prepareSettings(path string, routes []Route, replaceExisting bool, build func(map[string]any, *Journal) ([]Setting, error)) (*Plan, error) {
	s, d, js, prior, err := Read(path)
	if err != nil {
		return nil, err
	}
	// Static credentials make private files mandatory. Do not silently change a
	// user's file mode or expose a credential through a world-readable config.
	if s.Info != nil && s.Info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("settings file must be private (0600) before installing a harness credential")
	}
	settings, err := build(d, prior)
	if err != nil {
		return nil, err
	}
	j := &Journal{Version: 1, Path: s.Target, Original: s.Data, Existed: s.Info != nil, Pending: true}
	for _, route := range routes {
		j.Routes = append(j.Routes, route.Name)
	}
	if prior != nil {
		j.Original = prior.Original
		j.Existed = prior.Existed
	}
	p := &Plan{Path: s.Path, Managed: true, snapshot: s, journalSnapshot: js, journal: j}
	for _, v := range settings {
		current := get(d, v.Path)
		v.Before = current
		v.Previous = current
		owned := false
		if prior != nil {
			for _, old := range prior.Settings {
				if reflect.DeepEqual(old.Path, v.Path) {
					owned = true
					v.Before = old.Before
					if !same(current, old.Installed) && !(prior.Pending && same(current, old.Previous)) {
						return nil, fmt.Errorf("user changed %s; resolve it before rerunning setup", pathKey(v.Path))
					}
				}
			}
		}
		if !owned && same(current, v.Installed) {
			continue
		} // Pre-existing matching values stay user-owned.
		merge := pathKey(v.Path) == "modelPicker.options" || pathKey(v.Path) == "availableModels"
		if !owned && current.Exists && !merge && !replaceExisting {
			return nil, fmt.Errorf("existing %s conflicts; use --replace-existing to back up and replace it", pathKey(v.Path))
		}
		if err := put(d, v.Path, v.Installed); err != nil {
			return nil, err
		}
		j.Settings = append(j.Settings, v)
		p.Keys = append(p.Keys, pathKey(v.Path))
	}
	p.data, err = encodeConfig(s.Path, d)
	if err != nil {
		return nil, err
	}
	original, _ := decodeConfig(s.Path, s.Data)
	if reflect.DeepEqual(original, d) {
		p.data = s.Data
	}
	p.Changed = !bytes.Equal(p.data, s.Data)
	return p, nil
}
func PrepareTeardown(path string) (*Plan, error) {
	s, d, js, j, err := Read(path)
	if err != nil {
		return nil, err
	}
	p := &Plan{Path: s.Path, snapshot: s, journalSnapshot: js, teardown: true, data: s.Data}
	if j == nil {
		return p, nil
	}
	p.Managed = true
	remaining := *j
	remaining.Settings = nil
	p.journal = &remaining
	for _, v := range j.Settings {
		current := get(d, v.Path)
		if same(current, v.Before) {
			continue
		}
		if !same(current, v.Installed) && !(j.Pending && same(current, v.Previous)) {
			remaining.Settings = append(remaining.Settings, v)
			p.Conflicts = append(p.Conflicts, pathKey(v.Path))
			continue
		}
		original, _ := decodeConfig(s.Path, j.Original)
		before := v.Before
		if value := get(original, v.Path); same(value, before) {
			before = value
		}
		if err := put(d, v.Path, before); err != nil {
			return nil, err
		}
		p.Keys = append(p.Keys, pathKey(v.Path))
	}
	p.data, err = encodeConfig(s.Path, d)
	if err != nil {
		return nil, err
	}
	original, err := decodeConfig(s.Path, j.Original)
	if err == nil && same(Value{Data: original}, Value{Data: d}) {
		p.data = j.Original
	}
	if len(p.Keys) == 0 {
		p.data = s.Data
	}
	p.Changed = !bytes.Equal(p.data, s.Data)
	return p, nil
}

// Lock uses the resolved target so two symlink spellings share ownership and
// serialize mutations. Dry runs and status never create files or directories.
func Lock(path string) (func(), error) {
	s, err := safefile.ReadSnapshot(path)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(s.Target), 0700); err != nil {
		return nil, err
	}
	name := JournalPath(s.Target) + ".lock"
	f, err := os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, fmt.Errorf("harness config is locked; check for another setup or teardown before removing %s", name)
	}
	_ = f.Close()
	return func() { _ = os.Remove(name) }, nil
}
func (p *Plan) Apply() error {
	if err := p.snapshot.Check(); err != nil {
		return err
	}
	if err := p.journalSnapshot.Check(); err != nil {
		return err
	}
	if p.journal == nil {
		return nil
	}
	if !p.teardown {
		data, err := encode(p.journal)
		if err != nil {
			return err
		}
		if err := p.journalSnapshot.Write(data); err != nil {
			return err
		}
	}
	if p.teardown && !p.journal.Existed && len(p.data) == 0 && len(p.journal.Settings) == 0 {
		if err := p.snapshot.Check(); err != nil {
			return err
		}
		if err := os.Remove(p.snapshot.Target); err != nil && !os.IsNotExist(err) {
			return err
		}
	} else if err := p.snapshot.Write(p.data); err != nil {
		return fmt.Errorf("configuration write interrupted; ownership journal retained for teardown: %w", err)
	}
	// Commit ownership after the config write. A crash before this point leaves
	// a pending journal accepting both the previous and newly installed values.
	js, err := safefile.ReadSnapshot(p.journalSnapshot.Path)
	if err != nil {
		return err
	}
	if p.teardown && len(p.journal.Settings) == 0 {
		if err := js.Check(); err != nil {
			return err
		}
		return os.Remove(js.Target)
	}
	if !p.teardown {
		p.journal.Pending = false
	}
	data, err := encode(p.journal)
	if err != nil {
		return err
	}
	return js.Write(data)
}

type Status struct {
	Detection
	State   string   `json:"state"`
	Managed []string `json:"managed_settings,omitempty"`
	Drift   []string `json:"drift,omitempty"`
	Routes  []string `json:"routes,omitempty"`
	Note    string   `json:"note"`
}

func Inspect(d Detection) (Status, error) {
	r := Status{Detection: d, State: "not-configured", Note: "Local configuration only; no API authorization or inference was checked. Rerun setup to refresh Routes, then restart the harness."}
	_, data, _, j, err := Read(d.Path)
	if err != nil {
		return r, err
	}
	if j == nil {
		if d.Name == "codex" {
			_, _, _, catalog, err := Read(CatalogPath(d.Path))
			if err != nil {
				return r, err
			}
			if catalog != nil {
				r.State = "interrupted"
				r.Drift = []string{"orphaned model catalog"}
			}
		}
		return r, nil
	}
	r.State = "configured"
	if j.Pending {
		r.State = "interrupted"
	}
	for _, v := range j.Settings {
		r.Managed = append(r.Managed, pathKey(v.Path))
		if !same(get(data, v.Path), v.Installed) {
			r.Drift = append(r.Drift, pathKey(v.Path))
		}
	}
	if len(r.Drift) > 0 {
		r.State = "drifted"
	}
	if options, ok := get(data, []string{"modelPicker", "options"}).Data.([]any); ok {
		for _, o := range options {
			if m, ok := o.(map[string]any); ok {
				if id, ok := m["id"].(string); ok {
					for _, route := range j.Routes {
						if id == route {
							r.Routes = append(r.Routes, id)
							break
						}
					}
				}
			}
		}
	}
	if d.Name == "opencode" {
		for _, route := range j.Routes {
			if get(data, []string{"provider", providerID, "models", route}).Exists {
				r.Routes = append(r.Routes, route)
			}
		}
	}
	if d.Name == "codex" {
		_, _, _, catalog, err := Read(CatalogPath(d.Path))
		if err != nil {
			return r, err
		}
		if catalog == nil {
			r.State = "drifted"
			r.Drift = append(r.Drift, "model catalog missing")
		} else {
			r.Routes = append(r.Routes, catalog.Routes...)
			_, contents, _, _, err := Read(CatalogPath(d.Path))
			if err != nil {
				return r, err
			}
			for _, v := range catalog.Settings {
				if !same(get(contents, v.Path), v.Installed) {
					r.State = "drifted"
					r.Drift = append(r.Drift, "model catalog changed")
				}
			}
		}
	}
	return r, nil
}

func decodeConfig(path string, b []byte) (map[string]any, error) {
	if filepath.Ext(path) != ".toml" {
		return decode(b)
	}
	d := map[string]any{}
	if _, err := toml.Decode(string(b), &d); err != nil {
		return nil, errors.New("invalid settings TOML; repair it before continuing")
	}
	return d, nil
}
func encodeConfig(path string, d map[string]any) ([]byte, error) {
	if filepath.Ext(path) != ".toml" {
		return encode(d)
	}
	var b bytes.Buffer
	err := toml.NewEncoder(&b).Encode(d)
	return b.Bytes(), err
}
