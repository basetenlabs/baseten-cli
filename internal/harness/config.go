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
	"github.com/tailscale/hujson"
)

type value struct {
	Exists bool
	Data   any
}

type setting struct {
	Path      []string
	Installed value
}

// Setup overwrites only each adapter's integration settings. There are no
// backups or saved previous values. Repeating setup refreshes those same fields.
// Teardown removes Baseten's provider and resets its shared settings by deleting
// the keys, letting the harness supply its native defaults. Unrelated settings
// and edits survive; previous values of overwritten settings are not restored.
// Each adapter derives its teardown scope from the live configuration.
//
// Plan previews paths and setting names only, never credential values.
type Plan struct {
	Harness  string
	Replaced []string
	Managed  bool
	Path     string
	Keys     []string
	Changed  bool

	snapshot       *configFile
	data           []byte
	config         map[string]any
	credentialPath []string
	teardown       bool
	remove         bool
}

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

func readConfig(path string) (*configFile, map[string]any, error) {
	s, err := readFile(path)
	if err != nil {
		return nil, nil, err
	}
	d, err := decodeConfig(s.Path, s.Data)
	return s, d, err
}

func prepareSettings(path string, build func(map[string]any) ([]setting, error)) (*Plan, error) {
	s, d, err := readConfig(path)
	if err != nil {
		return nil, err
	}
	settings, err := build(d)
	if err != nil {
		return nil, err
	}
	p := &Plan{Path: s.Path, Managed: true, snapshot: s, config: d}
	for _, v := range settings {
		current := get(d, v.Path)
		if current.Exists && !same(current, v.Installed) {
			p.Replaced = append(p.Replaced, pathKey(v.Path))
		}
		if err := put(d, v.Path, v.Installed); err != nil {
			return nil, err
		}
		p.Keys = append(p.Keys, pathKey(v.Path))
	}
	return p, p.encode()
}

func prepareTeardown(path string, scope func(map[string]any) [][]string) (*Plan, error) {
	s, d, err := readConfig(path)
	if err != nil {
		return nil, err
	}
	p := &Plan{Path: s.Path, snapshot: s, config: d, teardown: true}
	for _, key := range scope(d) {
		if !get(d, key).Exists {
			continue
		}
		if err := put(d, key, value{}); err != nil {
			return nil, err
		}
		p.Keys = append(p.Keys, pathKey(key))
	}
	p.Managed = len(p.Keys) > 0
	return p, p.encode()
}

// Only dedicated Baseten files (the Codex catalog) are deleted. A shared config
// can remain empty after teardown; without backups we don't know who created it.
func prepareRemoval(path string) (*Plan, error) {
	s, err := readFile(path)
	if err != nil {
		return nil, err
	}
	return &Plan{Path: path, snapshot: s, Managed: s.Info != nil, Changed: s.Info != nil, teardown: true, remove: true}, nil
}

func (p *Plan) encode() error {
	original, err := decodeConfig(p.Path, p.snapshot.Data)
	if err != nil {
		return err
	}
	p.data = p.snapshot.Data
	if !reflect.DeepEqual(original, p.config) {
		p.data, err = encodeConfig(p.Path, p.config)
		if err != nil {
			return err
		}
	}
	p.Changed = !bytes.Equal(p.data, p.snapshot.Data) ||
		(!p.teardown && p.snapshot.Info != nil && p.snapshot.Info.Mode().Perm() != 0600)
	return nil
}

func (p *Plan) apply() error {
	if !p.Changed {
		return nil
	}
	if p.remove {
		err := os.Remove(p.snapshot.Target)
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if p.teardown {
		return p.snapshot.writeExisting(p.data)
	}
	return p.snapshot.writePrivate(p.data)
}

// Configuration is planned once, before confirmation or API key creation.
func (p *Plan) setCredential(token string) error {
	if len(p.credentialPath) == 0 {
		return nil
	}
	if err := put(p.config, p.credentialPath, value{Exists: true, Data: token}); err != nil {
		return err
	}
	return p.encode()
}

func decodeConfig(path string, b []byte) (map[string]any, error) {
	// Standardize also accepts plain JSON, so .json and .jsonc share one reader.
	if filepath.Ext(path) != ".toml" && len(bytes.TrimSpace(b)) > 0 {
		standardized, err := hujson.Standardize(bytes.Clone(b))
		if err != nil {
			return nil, errors.New("invalid settings JSON; repair it before continuing")
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

func encodeConfig(path string, d map[string]any) ([]byte, error) {
	// JSONC comments are not preserved; plain JSON is still valid JSONC.
	if filepath.Ext(path) != ".toml" {
		return encode(d)
	}
	var b bytes.Buffer
	err := toml.NewEncoder(&b).Encode(d)
	return b.Bytes(), err
}
