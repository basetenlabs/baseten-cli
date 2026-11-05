package harness

import (
	"cmp"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
)

// claudeDesktopHarness configures Claude Desktop's third-party mode, which
// reads a config from its library, the library's applied config, and the
// app's deployment mode, each in its own file.
type claudeDesktopHarness struct{}

// claudeDesktopModelPrefix makes route model IDs Claude-shaped, since Claude
// Desktop lists no others. Baseten resolves claude-desktop-<route id> to the route.
const claudeDesktopModelPrefix = "claude-desktop-"

// claudeDesktopDenied is Claude Desktop's own model ID denylist, which it
// matches against the lowercased ID before the Claude-name check; an ID it
// matches never reaches the picker.
var claudeDesktopDenied = regexp.MustCompile(`ark-code|astron|command-r|deepseek|doubao|gemini|gemma|glm|gpt|grok|hermes|hy3|kimi|lfm|\bling\b|llama|longcat|mimo|minimax|mistral|mixtral|moonshot|nemotron|openai|phi-|qianfan|qwen|tc-code|\bunic\b|yi-|stepfun|step-3|seed-|bytedance|hunyuan|granite|amazon\.nova|nova-|devstral|ministral|ernie|codex|arcee|trinity|abab|phi\d|\bk2\.|\bm2\.|jamba|arctic|solar|mercury|zamba|kat-coder|\bds-|dpsk`)

// claudeDesktopMarkerPath identifies Baseten's config in the library. The app
// identifies as claude-code on the wire, so it shares that harness's team
// defaults and attribution on Baseten's side.
var claudeDesktopMarkerPath = []string{"inferenceCustomHeaders", clientHeader}

var claudeDesktopCredentialPath = []string{"inferenceGatewayApiKey"}

var claudeDesktopHelperPaths = [][]string{{"inferenceCredentialHelper"}, {"inferenceCredentialHelperArgs"}, {"inferenceCredentialHelperTtlSec"}}

var claudeDesktopPaths = append([][]string{
	{"inferenceProvider"}, {"inferenceGatewayBaseUrl"}, {"inferenceGatewayAuthScheme"},
	{"inferenceCredentialKind"}, claudeDesktopCredentialPath, {"inferenceCustomHeaders"},
	{"modelDiscoveryEnabled"}, {"autoModeEnabled"}, {"inferenceModels"},
}, claudeDesktopHelperPaths...)

// claudeDesktopHelperTTL is how long, in seconds, Claude Desktop keeps a helper
// token. 'grid token' replaces its token once under 15 minutes remain, so the
// app must ask again sooner than that; 5 minutes matches Claude Code's helper.
const claudeDesktopHelperTTL = 300

// claudeDesktopSealedPrefix marks a static key Claude Desktop has encrypted in place.
const claudeDesktopSealedPrefix = "__sealed__:"

func (claudeDesktopHarness) Name() string { return ClaudeDesktop }

// Detect finds the app bundle without running it, since running the app
// opens it rather than printing a version.
func (h claudeDesktopHarness) Detect(_ context.Context, execer Execer, dir string) (Detection, error) {
	// dir is the config library, the directory that holds the config, as
	// --config-dir and sync name it.
	rel := []string{".config", "Claude-3p", "configLibrary"}
	if runtime.GOOS == "darwin" {
		rel = []string{"Library", "Application Support", "Claude-3p", "configLibrary"}
	}
	library, err := configDir(dir, "", rel...)
	if err != nil {
		return Detection{}, err
	}
	path, err := filepath.Abs(claudeDesktopConfigPath(library))
	if err != nil {
		return Detection{}, err
	}
	d := Detection{Name: h.Name(), Path: path}
	for _, binary := range claudeDesktopBinaries() {
		if bin, err := execer.LookPath(binary); err == nil {
			d.Installs = append(d.Installs, Install{Kind: InstallDesktop, Binary: bin})
		}
	}
	d.Installed = len(d.Installs) > 0
	return d, nil
}

func claudeDesktopBinaries() []string {
	switch runtime.GOOS {
	case "darwin":
		binaries := []string{"/Applications/Claude.app/Contents/MacOS/Claude"}
		if home, err := os.UserHomeDir(); err == nil {
			binaries = append(binaries, filepath.Join(home, "Applications", "Claude.app", "Contents", "MacOS", "Claude"))
		}
		return binaries
	case "linux":
		return []string{"claude-desktop"}
	}
	return nil
}

// claudeDesktopConfigPath is the library config setup wrote before, else a new
// one named by a random v4 UUID, as Claude Desktop names its configs.
func claudeDesktopConfigPath(library string) string {
	entries, _ := os.ReadDir(library)
	for _, e := range entries {
		path := filepath.Join(library, e.Name())
		if e.Name() == "_meta.json" || filepath.Ext(path) != ".json" {
			continue
		}
		if _, data, err := readConfig(path); err == nil && claudeDesktopOwned(data) {
			return path
		}
	}
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return filepath.Join(library, fmt.Sprintf("%x-%x-%x-%x-%x.json", b[0:4], b[4:6], b[6:8], b[8:10], b[10:]))
}

func claudeDesktopOwned(data map[string]any) bool {
	return get(data, claudeDesktopMarkerPath).Data == ClaudeCode
}

func claudeDesktopID(path string) string { return strings.TrimSuffix(filepath.Base(path), ".json") }

func claudeDesktopMetaPath(path string) string {
	return filepath.Join(filepath.Dir(path), "_meta.json")
}

func claudeDesktopAppConfigPath(path string) string {
	return filepath.Join(filepath.Dir(filepath.Dir(path)), "claude_desktop_config.json")
}

func claudeDesktopDeniedID(name string) bool {
	return claudeDesktopDenied.MatchString(strings.ToLower(name))
}

func claudeDesktopModel(r Route) map[string]any {
	return map[string]any{"name": claudeDesktopModelPrefix + r.ID, "labelOverride": cmp.Or(r.DisplayName, r.Name)}
}

func (claudeDesktopHarness) BackgroundRoute(Selection) string { return "" }

// Routes keeps the routes that serve the Anthropic Messages API under a model
// ID Claude Desktop accepts, so a denied route is absent everywhere, and an
// explicit choice of one fails like any other unlisted route.
func (claudeDesktopHarness) Routes(routes []Route) []Route {
	return slices.DeleteFunc(slices.Clone(routes), func(r Route) bool {
		return !r.Messages || claudeDesktopDeniedID(claudeDesktopModelPrefix+r.ID)
	})
}

func (claudeDesktopHarness) SupportsSubagent() bool { return false }

func (claudeDesktopHarness) SupportsHelper() bool { return true }

func (claudeDesktopHarness) Prepare(path string, routes []Route, opts PrepareOptions) ([]*Plan, error) {
	s, helper := opts.Selection, opts.Helper
	switch {
	case s.Background != "":
		return nil, errors.New("--background-route-id is supported only for Claude Code and OpenCode")
	case s.Subagent != "":
		return nil, errors.New("--subagent-route-id is supported only for Claude Code and OpenCode")
	case s.Fallback != "":
		return nil, errors.New("--fallback-route-id is supported only for Claude Code")
	}
	s, err := s.Resolve(routes)
	if err != nil {
		return nil, err
	}
	// The first model is Claude Desktop's default.
	primary, _ := routeByName(routes, s.Primary)
	models := []any{claudeDesktopModel(primary)}
	for _, r := range routes {
		if r.Name != primary.Name {
			models = append(models, claudeDesktopModel(r))
		}
	}
	values := []setting{
		desired([]string{"inferenceProvider"}, "gateway"),
		desired([]string{"inferenceGatewayBaseUrl"}, opts.Endpoint),
		desired([]string{"inferenceGatewayAuthScheme"}, "bearer"),
		desired([]string{"inferenceCustomHeaders"}, map[string]any{clientHeader: ClaudeCode}),
		desired([]string{"modelDiscoveryEnabled"}, false),
		desired([]string{"autoModeEnabled"}, false),
		desired([]string{"inferenceModels"}, models),
	}
	credentialPath := claudeDesktopCredentialPath
	if helper != nil {
		credentialPath = nil
		values = append(values,
			desired([]string{"inferenceCredentialKind"}, "helper-script"),
			desired(claudeDesktopHelperPaths[0], helper.Path),
			desired(claudeDesktopHelperPaths[1], helper.args(ClaudeDesktop)),
			desired(claudeDesktopHelperPaths[2], claudeDesktopHelperTTL),
			setting{Path: claudeDesktopCredentialPath},
		)
	} else {
		values = append(values, desired([]string{"inferenceCredentialKind"}, "static"), desired(claudeDesktopCredentialPath, ""))
		for _, p := range claudeDesktopHelperPaths {
			values = append(values, setting{Path: p})
		}
	}
	config, err := prepareSettings(path, credentialPath, func(map[string]any) ([]setting, error) { return values, nil })
	if err != nil {
		return nil, err
	}
	meta, err := prepareSettings(claudeDesktopMetaPath(path), nil, func(map[string]any) ([]setting, error) {
		return []setting{desired([]string{"appliedId"}, claudeDesktopID(path))}, nil
	})
	if err != nil {
		return nil, err
	}
	// 3p mode makes the app start with the applied config instead of a Claude login.
	mode, err := prepareSettings(claudeDesktopAppConfigPath(path), nil, func(map[string]any) ([]setting, error) {
		return []setting{desired([]string{"deploymentMode"}, "3p")}, nil
	})
	if err != nil {
		return nil, err
	}
	// The config precedes the files that select it.
	return []*Plan{config, meta, mode}, nil
}

func (claudeDesktopHarness) Credential(path string) (string, error) {
	_, data, err := readConfig(path)
	if err != nil || !claudeDesktopOwned(data) {
		return "", err
	}
	token, _ := get(data, claudeDesktopCredentialPath).Data.(string)
	if strings.HasPrefix(token, claudeDesktopSealedPrefix) {
		return "", nil
	}
	return token, nil
}

func (claudeDesktopHarness) Teardown(path string) ([]*Plan, error) {
	_, data, err := readConfig(path)
	if err != nil {
		return nil, err
	}
	id := claudeDesktopID(path)
	_, applied, err := readConfig(claudeDesktopMetaPath(path))
	if err != nil {
		return nil, err
	}
	var plans []*Plan
	// 1p mode comes back only while the app still applies Baseten's config; a
	// config the user applied since stays in charge.
	if claudeDesktopOwned(data) && applied["appliedId"] == id {
		mode, err := prepareSettings(claudeDesktopAppConfigPath(path), nil, func(map[string]any) ([]setting, error) {
			return []setting{desired([]string{"deploymentMode"}, "1p")}, nil
		})
		if err != nil {
			return nil, err
		}
		mode.teardown, mode.Replaced = true, nil
		plans = append(plans, mode)
	}
	meta, err := prepareTeardown(claudeDesktopMetaPath(path), func(d map[string]any) [][]string {
		if d["appliedId"] != id {
			return nil
		}
		return [][]string{{"appliedId"}}
	})
	if err != nil {
		return nil, err
	}
	config, err := prepareTeardown(path, func(d map[string]any) [][]string {
		if !claudeDesktopOwned(d) {
			return nil
		}
		return claudeDesktopPaths
	})
	if err != nil {
		return nil, err
	}
	// The config is Baseten's own file, so it goes once nothing else is in it.
	config.remove = config.Managed && len(config.config) == 0
	// The files that select the config precede it.
	return append(plans, meta, config), nil
}

func (claudeDesktopHarness) Inspect(d Detection) (Status, error) {
	r, data, err := inspectConfig(d)
	if err != nil {
		return r, err
	}
	r.DefaultRoute = ""
	if !claudeDesktopOwned(data) {
		return r, nil
	}
	r.managed(data, claudeDesktopPaths)
	_, meta, err := readConfig(claudeDesktopMetaPath(d.Path))
	if err != nil {
		return r, err
	}
	_, app, err := readConfig(claudeDesktopAppConfigPath(d.Path))
	if err != nil {
		return r, err
	}
	if meta["appliedId"] != claudeDesktopID(d.Path) || app["deploymentMode"] != "3p" {
		r.State = StateInactive
	}
	labels := map[string]string{}
	models, _ := data["inferenceModels"].([]any)
	for i, model := range models {
		row, _ := model.(map[string]any)
		name, _ := row["name"].(string)
		id := strings.TrimPrefix(name, claudeDesktopModelPrefix)
		labels[id], _ = row["labelOverride"].(string)
		if i == 0 {
			r.DefaultRoute = id
		}
	}
	r.addRoutes(labels)
	return r, nil
}
