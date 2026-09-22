package harness

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/tailscale/hujson"
)

type value struct {
	Exists bool `json:"exists"`
	Data   any  `json:"data,omitempty"`
}

type setting struct {
	Path      []string `json:"path"`
	Installed value    `json:"installed"`
}

type journal struct {
	Routes   []string  `json:"routes"`
	Version  int       `json:"version"`
	Path     string    `json:"path"`
	Original []byte    `json:"original"`
	Existed  bool      `json:"existed"`
	Settings []setting `json:"settings"`
}

// A private .baseten-harness.json journal sits beside each configuration file.
// It retains the complete original document from the FIRST setup, plus the paths
// and last installed values of settings this integration manages. Refresh replaces
// those settings wholesale, including the picker, without changing the restore
// point. Optional settings managed by earlier runs stay tracked for teardown.
// Teardown restores only those paths from the original document, so unrelated
// edits survive. The original document also covers settings first managed later.
//
// The journal is saved before configuration writes and removed only after a
// successful teardown. An interrupted setup can therefore be retried or torn
// down without a separate pending state or persistent lock. Files are replaced
// atomically, but a multi-file/multi-harness operation is not a transaction.
//
// Plan exposes paths and key names only: configuration and backups may contain
// credentials. Never include their values in JSON output or error messages.
type Plan struct {
	Harness                   string   `json:"harness"`
	Replaced                  []string `json:"replaced_settings,omitempty"`
	Managed                   bool     `json:"managed"`
	Path                      string   `json:"config"`
	Keys                      []string `json:"settings"`
	Changed                   bool     `json:"changed"`
	snapshot, journalSnapshot *configFile
	data                      []byte
	config                    map[string]any
	credentialPath            []string
	journal                   *journal
	teardown                  bool
}

func journalPath(path string) string { return path + ".baseten-harness.json" }

func pathKey(p []string) string { return strings.Join(p, ".") }

func desired(p []string, v any) setting {
	return setting{
		Path: p,
		Installed: value{
			Exists: true,
			Data:   v,
		},
	}
}

func same(a, b value) bool {
	aa, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return bytes.Equal(aa, bb)
}

func get(d map[string]any, p []string) value {
	for _, k := range p[:len(p)-1] {
		child, ok := d[k].(map[string]any)
		if !ok {
			return value{}
		}
		d = child
	}
	v, ok := d[p[len(p)-1]]
	return value{
		Exists: ok,
		Data:   v,
	}
}

func put(d map[string]any, p []string, v value) error {
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

func readConfig(path string) (*configFile, map[string]any, *configFile, *journal, error) {
	s, err := readFile(path)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	d, err := decodeConfig(s.Path, s.Data)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	js, err := readFile(journalPath(s.Target))
	if err != nil {
		return nil, nil, nil, nil, err
	}
	if js.Info == nil {
		return s, d, js, nil, nil
	}
	if js.Info.Mode().Perm()&0077 != 0 {
		return nil, nil, nil, nil, errors.New("harness journal must be private (0600)")
	}
	var j journal
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

func prepareSettings(path string, routes []Route, build func(map[string]any) ([]setting, error)) (*Plan, error) {
	s, d, js, prior, err := readConfig(path)
	if err != nil {
		return nil, err
	}
	settings, err := build(d)
	if err != nil {
		return nil, err
	}
	j := &journal{
		Version:  1,
		Path:     s.Target,
		Original: s.Data,
		Existed:  s.Info != nil,
	}
	for _, route := range routes {
		j.Routes = append(j.Routes, route.Name)
	}
	if prior != nil {
		j.Original = prior.Original
		j.Existed = prior.Existed
	}
	p := &Plan{
		Path:            s.Path,
		Managed:         true,
		snapshot:        s,
		journalSnapshot: js,
		journal:         j,
	}
	managed := map[string]bool{}
	for _, v := range settings {
		managed[pathKey(v.Path)] = true
		current := get(d, v.Path)
		if current.Exists && !same(current, v.Installed) {
			p.Replaced = append(p.Replaced, pathKey(v.Path))
		}
		if err := put(d, v.Path, v.Installed); err != nil {
			return nil, err
		}
		j.Settings = append(j.Settings, v)
		p.Keys = append(p.Keys, pathKey(v.Path))
	}
	// Optional settings omitted on refresh remain owned so teardown can restore them.
	if prior != nil {
		for _, old := range prior.Settings {
			if !managed[pathKey(old.Path)] {
				j.Settings = append(j.Settings, old)
			}
		}
	}
	p.config = d
	p.data, err = encodeConfig(s.Path, d, s.Data)
	if err != nil {
		return nil, err
	}
	original, _ := decodeConfig(s.Path, s.Data)
	if reflect.DeepEqual(original, d) {
		p.data = s.Data
	}
	p.Changed = !bytes.Equal(p.data, s.Data) || (s.Info != nil && s.Info.Mode().Perm() != 0600)
	return p, nil
}

func prepareTeardown(path string) (*Plan, error) {
	s, d, js, j, err := readConfig(path)
	if err != nil {
		return nil, err
	}
	p := &Plan{
		Path:            s.Path,
		snapshot:        s,
		journalSnapshot: js,
		teardown:        true,
		data:            s.Data,
	}
	if j == nil {
		return p, nil
	}
	p.Managed = true
	p.journal = j
	original, err := decodeConfig(s.Path, j.Original)
	if err != nil {
		return nil, err
	}
	for _, v := range j.Settings {
		current := get(d, v.Path)
		before := get(original, v.Path)
		if same(current, before) {
			continue
		}
		if !same(current, v.Installed) {
			p.Replaced = append(p.Replaced, pathKey(v.Path))
		}
		if err := put(d, v.Path, before); err != nil {
			return nil, err
		}
		p.Keys = append(p.Keys, pathKey(v.Path))
	}
	p.config = d
	p.data, err = encodeConfig(s.Path, d, s.Data)
	if err != nil {
		return nil, err
	}
	// JSONC restoration uses the patched current document so later comments survive.
	if same(value{Data: original}, value{Data: d}) && (filepath.Ext(s.Path) != ".jsonc" || !j.Existed) {
		p.data = j.Original
	}
	if len(p.Keys) == 0 {
		p.data = s.Data
	}
	p.Changed = !bytes.Equal(p.data, s.Data)
	return p, nil
}

func (p *Plan) apply() error {
	if p.journal == nil {
		return nil
	}
	if !p.teardown {
		data, err := encode(p.journal)
		if err != nil {
			return err
		}
		if err := p.journalSnapshot.writeExisting(data); err != nil {
			return err
		}
	}
	if p.teardown && !p.journal.Existed && len(p.data) == 0 {
		if err := os.Remove(p.snapshot.Target); err != nil && !os.IsNotExist(err) {
			return err
		}
	} else {
		write := p.snapshot.writeExisting
		if !p.teardown {
			write = p.snapshot.writePrivate
		}
		if err := write(p.data); err != nil {
			return fmt.Errorf("configuration write interrupted; ownership journal retained for teardown: %w", err)
		}
	}
	if p.teardown {
		return os.Remove(p.journalSnapshot.Target)
	}
	return nil
}

// setCredential updates only the credential field of an already validated plan.
// Configuration is planned once, before confirmation or API key creation.
func (p *Plan) setCredential(token string) error {
	if len(p.credentialPath) == 0 {
		return nil
	}
	if err := put(p.config, p.credentialPath, value{Exists: true, Data: token}); err != nil {
		return err
	}
	for i := range p.journal.Settings {
		v := &p.journal.Settings[i]
		if slices.Equal(v.Path, p.credentialPath[:min(len(v.Path), len(p.credentialPath))]) {
			v.Installed = get(p.config, v.Path)
		}
	}
	data, err := encodeConfig(p.Path, p.config, p.snapshot.Data)
	if err != nil {
		return err
	}
	original, err := decodeConfig(p.Path, p.snapshot.Data)
	if err != nil {
		return err
	}
	if reflect.DeepEqual(original, p.config) {
		data = p.snapshot.Data
	}
	p.data = data
	p.Changed = !bytes.Equal(data, p.snapshot.Data) || (p.snapshot.Info != nil && p.snapshot.Info.Mode().Perm() != 0600)
	return nil
}

func decodeConfig(path string, b []byte) (map[string]any, error) {
	if filepath.Ext(path) == ".jsonc" && len(bytes.TrimSpace(b)) > 0 {
		standardized, err := hujson.Standardize(bytes.Clone(b))
		if err != nil {
			return nil, errors.New("invalid settings JSONC; repair it before continuing")
		}
		return decode(standardized)
	}
	if filepath.Ext(path) != ".toml" {
		return decode(b)
	}
	d := map[string]any{}
	if _, err := toml.Decode(string(b), &d); err != nil {
		return nil, errors.New("invalid settings TOML; repair it before continuing")
	}
	return d, nil
}

func encodeConfig(path string, d map[string]any, original []byte) ([]byte, error) {
	if filepath.Ext(path) == ".jsonc" && len(bytes.TrimSpace(original)) > 0 {
		before, err := decodeConfig(path, original)
		if err != nil {
			return nil, err
		}
		document, err := hujson.Parse(bytes.Clone(original))
		if err != nil {
			return nil, errors.New("invalid settings JSONC")
		}
		var operations []map[string]any
		jsonObjectPatch("", before, d, &operations)
		if len(operations) == 0 {
			return original, nil
		}
		patch, err := json.Marshal(operations)
		if err != nil {
			return nil, err
		}
		if err := document.Patch(patch); err != nil {
			return nil, errors.New("could not update settings JSONC")
		}
		document.Format()
		return document.Pack(), nil
	}
	if filepath.Ext(path) != ".toml" {
		return encode(d)
	}
	var b bytes.Buffer
	err := toml.NewEncoder(&b).Encode(d)
	return b.Bytes(), err
}

// Patch only changed object members so unrelated JSONC comments survive setup and teardown.
func jsonObjectPatch(path string, before, after map[string]any, operations *[]map[string]any) {
	keys := make([]string, 0, len(before)+len(after))
	for key := range before {
		keys = append(keys, key)
	}
	for key := range after {
		if _, exists := before[key]; !exists {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		pointer := path + "/" + strings.ReplaceAll(strings.ReplaceAll(key, "~", "~0"), "/", "~1")
		old, existed := before[key]
		nextValue, exists := after[key]
		if !exists {
			*operations = append(*operations, map[string]any{"op": "remove", "path": pointer})
			continue
		}
		if existed {
			if same(value{Data: old}, value{Data: nextValue}) {
				continue
			}
			oldObject, oldOK := old.(map[string]any)
			newObject, newOK := nextValue.(map[string]any)
			if oldOK && newOK {
				jsonObjectPatch(pointer, oldObject, newObject, operations)
				continue
			}
		}
		op := "add"
		if existed {
			op = "replace"
		}
		*operations = append(*operations, map[string]any{"op": op, "path": pointer, "value": nextValue})
	}
}
