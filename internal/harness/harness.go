// Package harness configures coding harnesses (Claude Code, Codex, and
// OpenCode) to use Baseten routes, and removes that configuration again.
//
// Setup overwrites a fixed set of integration settings in each harness's native
// settings file. There are no backups: repeating setup refreshes the same
// settings, and teardown deletes them so the harness's native defaults apply.
// Unrelated settings are left alone, and previous values of overwritten
// settings are not restored. Setup and teardown are idempotent.
package harness

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/tailscale/hujson"
)

// Harness names, as accepted by --harness.
const (
	ClaudeCode = "claude-code"
	Codex      = "codex"
	OpenCode   = "opencode"
)

// Integration states reported by [Harness.Inspect].
const (
	StateNotConfigured = "not-configured"
	StateConfigured    = "configured"
	// StateInactive means Baseten settings exist but another provider is selected.
	StateInactive = "inactive"
	// StateIncomplete means some Baseten settings are missing; rerun setup or teardown.
	StateIncomplete = "incomplete"
)

const providerID = "baseten-harness"
const chatProviderID = "baseten-harness-chat"

// TODO: Make the default background route server-driven.
const defaultBackgroundRoute = "deepseek-ai/DeepSeek-V4.1-Flash"

// Harness configures one coding harness. API access, user interaction, and
// output belong to the calling command.
type Harness interface {
	// Name is the --harness value.
	Name() string
	// Detect finds the harness executable and its settings file in configDir,
	// or in the harness's native configuration directory when configDir is empty.
	Detect(ctx context.Context, execer Execer, configDir string) (Detection, error)
	// Prepare plans setup of the settings file at path and, for harnesses with
	// MCP support, registration of mcpServers. ApplyPlans replaces token; an
	// empty token keeps the file's current credential, so a preview doesn't
	// report it as changed.
	Prepare(path string, routes []Route, mcpServers []MCPServer, selection Selection, endpoint, token string) ([]*Plan, error)
	// Inspect reports the integration state of the detected settings file.
	Inspect(d Detection) (Status, error)
	// Teardown plans removal of the integration settings at path.
	Teardown(path string) ([]*Plan, error)
	// BackgroundRoute is the route for lightweight background tasks, or "" if
	// the harness has no such setting.
	BackgroundRoute(selection Selection) string
}

// All returns every supported harness.
func All() []Harness {
	return []Harness{claudeCodeHarness{}, codexHarness{}, openCodeHarness{}}
}

// Route is a Baseten route as shown in a harness's model picker, with the model
// capabilities supplied by the route's own metadata.
type Route struct {
	Name            string
	DisplayName     string
	ContextWindow   int
	OutputLimit     int
	InputModalities []string
	Tools           bool
	ReasoningLevels []string
	ParallelTools   bool
	Messages        bool
	Responses       bool
	ChatCompletions bool
}

// ValidateCatalog rejects routes the harnesses cannot describe or drive.
func ValidateCatalog(routes []Route) ([]Route, error) {
	if len(routes) == 0 {
		return nil, errors.New("no accessible Routes in catalog")
	}
	seen := map[string]bool{}
	for _, r := range routes {
		if r.Name == "" || strings.TrimSpace(r.Name) != r.Name || strings.ContainsAny(r.Name, "\r\n\t []") || seen[r.Name] {
			return nil, errors.New("catalog has an invalid or duplicate Route name")
		}
		// Claude's built-in keywords are interpreted before sending a model ID.
		switch r.Name {
		case "default", "inherit", "opus", "sonnet", "haiku", "fable", "opusplan", "best":
			return nil, fmt.Errorf("Route %q conflicts with a Claude model keyword", r.Name)
		}
		if strings.TrimSpace(r.DisplayName) == "" {
			return nil, fmt.Errorf("Route %q has no display name", r.Name)
		}
		if !r.Tools {
			return nil, fmt.Errorf("Route %q lacks verified tool support", r.Name)
		}
		for _, modality := range r.InputModalities {
			if modality != "text" && modality != "image" {
				return nil, fmt.Errorf("Route %q has an unsupported input modality", r.Name)
			}
		}
		for _, level := range r.ReasoningLevels {
			switch level {
			case "low", "medium", "high", "minimal", "none", "xhigh":
			default:
				return nil, fmt.Errorf("Route %q has an unsupported reasoning level", r.Name)
			}
		}
		seen[r.Name] = true
	}
	return routes, nil
}

func routeByName(routes []Route, name string) (Route, bool) {
	for _, r := range routes {
		if r.Name == name {
			return r, true
		}
	}
	return Route{}, false
}

// RoutesWithoutMessages names the routes Claude Code cannot serve.
func RoutesWithoutMessages(routes []Route) []string {
	var names []string
	for _, r := range routes {
		if !r.Messages {
			names = append(names, r.Name)
		}
	}
	return names
}

// WireFamilies reports which wire protocols the selected routes require.
func WireFamilies(routes []Route, s Selection) (responses, chat bool, err error) {
	for _, name := range []string{s.Primary, s.Background, s.Subagent} {
		if name == "" {
			continue
		}
		r, ok := routeByName(routes, name)
		if !ok {
			continue
		}
		if r.Responses {
			responses = true
		} else if r.ChatCompletions {
			chat = true
		} else {
			return false, false, fmt.Errorf("Route %q requires Responses or Chat Completions support", r.Name)
		}
	}
	return responses, chat, nil
}

func defaultReasoningLevel(levels []string) any {
	best := ""
	for _, level := range levels {
		if level == "none" || level == "default" {
			continue
		}
		if best == "" || reasoningRank(level) < reasoningRank(best) {
			best = level
		}
	}
	if best == "" {
		return nil
	}
	return best
}

func reasoningRank(level string) int {
	switch level {
	case "minimal":
		return 0
	case "low":
		return 1
	case "medium":
		return 2
	case "high":
		return 3
	case "xhigh":
		return 4
	}
	return 5
}

// Selection holds the requested routes. Empty fields use each harness's default.
type Selection struct {
	Primary    string
	Background string
	Subagent   string
	Fallback   string
}

// resolve defaults the primary route to the first route and the subagent and
// fallback routes to the primary, then checks that every selected route exists.
func (s Selection) resolve(routes []Route) (Selection, error) {
	if len(routes) == 0 {
		return s, errors.New("no accessible routes")
	}
	s.Primary = cmp.Or(s.Primary, routes[0].Name)
	s.Subagent = cmp.Or(s.Subagent, s.Primary)
	s.Fallback = cmp.Or(s.Fallback, s.Primary)
	for _, name := range []string{s.Primary, s.Background, s.Subagent, s.Fallback} {
		if name != "" && !slices.ContainsFunc(routes, func(r Route) bool { return r.Name == name }) {
			return s, fmt.Errorf("route %q is not one of the team's routes", name)
		}
	}
	return s, nil
}

// Detection describes a harness installation and its settings file.
type Detection struct {
	Name      string
	Path      string
	Installed bool
	Version   string
}

// Execer runs subprocesses. The CLI's shared executor satisfies it.
type Execer interface {
	LookPath(file string) (string, error)
	Exec(cmd *exec.Cmd) error
}

// detect looks up binary on PATH and reads its version, which is informational only.
func detect(ctx context.Context, execer Execer, name, binary, path string) (Detection, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return Detection{}, err
	}
	d := Detection{Name: name, Path: path}
	bin, err := execer.LookPath(binary)
	if err != nil {
		return d, nil
	}
	d.Installed = true
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var out bytes.Buffer
	command := exec.CommandContext(ctx, bin, "--version")
	command.Stdout = &out
	if execer.Exec(command) == nil {
		d.Version, _, _ = strings.Cut(strings.TrimSpace(out.String()), "\n")
	}
	return d, nil
}

// configDir returns dir, else the directory named by env, else home joined with rel.
func configDir(dir, env string, rel ...string) (string, error) {
	if dir = cmp.Or(dir, os.Getenv(env)); dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(append([]string{home}, rel...)...), nil
}

// Status describes the current integration settings of one harness.
type Status struct {
	Detection
	State           string
	DefaultRoute    string
	BackgroundRoute string
	Routes          []Route
	// Managed lists the integration settings present in the file.
	Managed []string
}

func inspectConfig(d Detection) (Status, map[string]any, error) {
	r := Status{Detection: d, State: StateNotConfigured}
	_, data, err := readConfig(d.Path)
	if err != nil {
		return r, nil, err
	}
	r.DefaultRoute, _ = data["model"].(string)
	return r, data, nil
}

func (r *Status) managed(data map[string]any, paths [][]string) {
	for _, path := range paths {
		if get(data, path).Exists {
			r.Managed = append(r.Managed, pathKey(path))
		}
	}
	if len(r.Managed) > 0 {
		r.State = StateConfigured
	}
}

func (r *Status) addRoutes(labels map[string]string) {
	for name, label := range labels {
		r.Routes = append(r.Routes, Route{Name: name, DisplayName: label})
	}
	slices.SortFunc(r.Routes, func(a, b Route) int { return strings.Compare(a.Name, b.Name) })
}

// Plan is a pending change to one file. It exposes paths and setting names
// only, never credential values.
type Plan struct {
	Harness  string
	Path     string
	Keys     []string
	Replaced []string
	Managed  bool
	Changed  bool

	file            *configFile
	data            []byte
	config          map[string]any
	credentialPaths [][]string
	teardown        bool
	remove          bool
}

// ApplyPlans writes plans in order, inserting token into setup plans first.
// Concurrent invocations are not supported, and a multi-file operation is not a
// transaction: rerun an interrupted setup or teardown to finish it.
func ApplyPlans(plans []*Plan, token string) error {
	for _, p := range plans {
		if !p.teardown {
			if err := p.setCredential(token); err != nil {
				return err
			}
		}
	}
	for _, p := range plans {
		if err := p.apply(); err != nil {
			return err
		}
	}
	return nil
}

type value struct {
	Exists bool
	Data   any
}

type setting struct {
	Path      []string
	Installed value
}

func pathKey(p []string) string { return strings.Join(p, ".") }

func desired(p []string, v any) setting {
	return setting{Path: p, Installed: value{Exists: true, Data: v}}
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
	return value{Exists: ok, Data: v}
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

func readConfig(path string) (*configFile, map[string]any, error) {
	f, err := readFile(path)
	if err != nil {
		return nil, nil, err
	}
	d, err := decodeConfig(path, f.data)
	return f, d, err
}

func prepareSettings(path string, credentialPaths [][]string, token string, build func(map[string]any) ([]setting, error)) (*Plan, error) {
	f, d, err := readConfig(path)
	if err != nil {
		return nil, err
	}
	original, err := decodeConfig(path, f.data)
	if err != nil {
		return nil, err
	}
	settings, err := build(d)
	if err != nil {
		return nil, err
	}
	p := &Plan{Path: path, Managed: true, file: f, config: d, credentialPaths: credentialPaths}
	for _, v := range settings {
		if err := put(d, v.Path, v.Installed); err != nil {
			return nil, err
		}
		p.Keys = append(p.Keys, pathKey(v.Path))
	}
	if token == "" {
		for _, credentialPath := range credentialPaths {
			if current := get(original, credentialPath); current.Exists {
				if err := put(d, credentialPath, current); err != nil {
					return nil, err
				}
			}
		}
	}
	for _, v := range settings {
		if before := get(original, v.Path); before.Exists && !same(before, get(d, v.Path)) {
			p.Replaced = append(p.Replaced, pathKey(v.Path))
		}
	}
	return p, p.encode()
}

// prepareTeardown deletes the settings that scope reports as Baseten's.
func prepareTeardown(path string, scope func(map[string]any) [][]string) (*Plan, error) {
	f, d, err := readConfig(path)
	if err != nil {
		return nil, err
	}
	p := &Plan{Path: path, file: f, config: d, teardown: true}
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

// prepareRemoval deletes a file Baseten owns entirely. Shared settings files are
// never deleted, even when teardown leaves them empty.
func prepareRemoval(path string) (*Plan, error) {
	f, err := readFile(path)
	if err != nil {
		return nil, err
	}
	return &Plan{Path: path, file: f, Managed: f.exists, Changed: f.exists, teardown: true, remove: true}, nil
}

func (p *Plan) encode() error {
	original, err := decodeConfig(p.Path, p.file.data)
	if err != nil {
		return err
	}
	p.data = p.file.data
	if !reflect.DeepEqual(original, p.config) {
		if p.data, err = encodeConfig(p.Path, p.config); err != nil {
			return err
		}
	}
	p.Changed = !bytes.Equal(p.data, p.file.data)
	return nil
}

func (p *Plan) setCredential(token string) error {
	for _, credentialPath := range p.credentialPaths {
		if err := put(p.config, credentialPath, value{Exists: true, Data: token}); err != nil {
			return err
		}
	}
	if len(p.credentialPaths) == 0 {
		return nil
	}
	return p.encode()
}

func (p *Plan) apply() error {
	switch {
	case !p.Changed:
		return nil
	case p.remove:
		if err := os.Remove(p.file.target); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	case p.teardown:
		return p.file.write(p.data, p.file.mode)
	default:
		// Setup writes a credential, so the file is private.
		return p.file.write(p.data, 0o600)
	}
}

func decode(b []byte) (map[string]any, error) {
	d := map[string]any{}
	if len(bytes.TrimSpace(b)) == 0 {
		return d, nil
	}
	// Standardize accepts plain JSON too, so .json and .jsonc share one reader.
	standardized, err := hujson.Standardize(bytes.Clone(b))
	if err != nil {
		return nil, errors.New("invalid settings JSON; repair it before continuing")
	}
	decoder := json.NewDecoder(bytes.NewReader(standardized))
	decoder.UseNumber()
	if err := decoder.Decode(&d); err != nil || d == nil {
		return nil, errors.New("invalid settings JSON; repair it before continuing")
	}
	return d, nil
}

func encode(d any) ([]byte, error) {
	b, err := json.MarshalIndent(d, "", "  ")
	return append(b, '\n'), err
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

// encodeConfig writes JSONC files as plain JSON, which drops their comments.
func encodeConfig(path string, d map[string]any) ([]byte, error) {
	if filepath.Ext(path) != ".toml" {
		return encode(d)
	}
	var b bytes.Buffer
	err := toml.NewEncoder(&b).Encode(d)
	return b.Bytes(), err
}

// configFile is a settings file as read before planning. A symlinked file is
// written through to its target so dotfile links survive.
type configFile struct {
	target string
	exists bool
	mode   os.FileMode
	data   []byte
}

func readFile(path string) (*configFile, error) {
	target, err := filepath.EvalSymlinks(path)
	if os.IsNotExist(err) {
		return &configFile{target: path, mode: 0o600}, nil
	}
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(target)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(target)
	return &configFile{target: target, exists: true, mode: info.Mode().Perm(), data: data}, err
}

// write replaces the file atomically so an interrupted write never leaves a
// truncated settings file.
func (f *configFile) write(data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(f.target), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(f.target), ".baseten-harness-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err = tmp.Chmod(mode); err == nil {
		_, err = tmp.Write(data)
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmp.Name(), f.target)
}
