package harness

import (
	"cmp"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
)

// Claude Desktop's third-party mode reads a config from its library, the
// library's applied config, and the app's deployment mode, each in its own
// file. The claude harness writes them beside Claude Code's settings when the
// app is installed.

// claudeDesktopModelPrefix makes route model IDs Claude-shaped, since Claude
// Desktop lists no others. Baseten resolves claude-desktop-<route id> to the route.
const claudeDesktopModelPrefix = "claude-desktop-"

// claudeDesktopDenied is Claude Desktop's own model ID denylist, which it
// matches against the lowercased ID before the Claude-name check; an ID it
// matches never reaches the picker. Copied from Claude Desktop 4.16336.0
// (app.asar) on 2026-11-05, as were the setting names below.
var claudeDesktopDenied = regexp.MustCompile(`ark-code|astron|command-r|deepseek|doubao|gemini|gemma|glm|gpt|grok|hermes|hy3|kimi|lfm|\bling\b|llama|longcat|mimo|minimax|mistral|mixtral|moonshot|nemotron|openai|phi-|qianfan|qwen|tc-code|\bunic\b|yi-|stepfun|step-3|seed-|bytedance|hunyuan|granite|amazon\.nova|nova-|devstral|ministral|ernie|codex|arcee|trinity|abab|phi\d|\bk2\.|\bm2\.|jamba|arctic|solar|mercury|zamba|kat-coder|\bds-|dpsk`)

// claudeDesktopMarkerPath identifies Baseten's config in the library. The app
// identifies as claude-code on the wire, like Claude Code.
var claudeDesktopMarkerPath = []string{"inferenceCustomHeaders", clientHeader}

var claudeDesktopCredentialPath = []string{"inferenceGatewayApiKey"}

var claudeDesktopHelperPaths = [][]string{{"inferenceCredentialHelper"}, {"inferenceCredentialHelperArgs"}, {"inferenceCredentialHelperTtlSec"}}

var claudeDesktopPaths = append([][]string{
	{"inferenceProvider"}, {"inferenceGatewayBaseUrl"}, {"inferenceGatewayAuthScheme"},
	{"inferenceCredentialKind"}, claudeDesktopCredentialPath, {"inferenceCustomHeaders"},
	{"modelDiscoveryEnabled"}, {"toolSearchEnabled"}, {"autoModeEnabled"}, {"inferenceModels"},
}, claudeDesktopHelperPaths...)

// claudeDesktopHelperTTL is how long, in seconds, Claude Desktop keeps a helper
// token. 'grid token' replaces its token once under 15 minutes remain, so the
// app must ask again sooner than that; 5 minutes matches Claude Code's helper.
const claudeDesktopHelperTTL = 300

// claudeDesktopSealedPrefix marks a static key Claude Desktop has encrypted in place.
const claudeDesktopSealedPrefix = "__sealed__:"

// claudeDesktopConfigID names Baseten's config in the library. Claude Desktop
// names configs by v4 UUID; a fixed one keeps the path the same across calls.
const claudeDesktopConfigID = "b45e7e00-c0de-4000-8000-000000000001"

// linuxDesktopClaude is where the community Claude Desktop .deb package
// installs its launcher. Desktop apps are looked up by absolute path, never
// by a bare name on PATH.
const linuxDesktopClaude = "/usr/bin/claude-desktop"

func claudeDesktopBinaries() []string {
	switch runtime.GOOS {
	case "darwin":
		binaries := []string{"/Applications/Claude.app/Contents/MacOS/Claude"}
		if home, err := os.UserHomeDir(); err == nil {
			binaries = append(binaries, filepath.Join(home, "Applications", "Claude.app", "Contents", "MacOS", "Claude"))
		}
		return binaries
	case "linux":
		return []string{linuxDesktopClaude}
	}
	return nil
}

// ClaudeDesktopLocations names where Detect looks for the app, for error messages.
func ClaudeDesktopLocations() string {
	switch runtime.GOOS {
	case "darwin":
		return "Claude.app"
	case "linux":
		return linuxDesktopClaude
	}
	return ""
}

// HasClaudeDesktop reports whether Detect found the Claude Desktop app.
func HasClaudeDesktop(installs []Install) bool {
	return slices.ContainsFunc(installs, func(i Install) bool { return i.Kind == InstallDesktop })
}

// claudeDesktopConfig locates Baseten's Claude Desktop config from the Claude
// Code settings path: in the app's own library when the settings are in Claude
// Code's default directory, else under the settings directory, so --config-dir
// keeps both apps' files together and sync finds them again.
func claudeDesktopConfig(settingsPath string) string {
	dir := filepath.Dir(settingsPath)
	library := filepath.Join(dir, "Claude-3p", "configLibrary")
	if def, err := configDir("", "CLAUDE_CONFIG_DIR", ".claude"); err == nil {
		if abs, err := filepath.Abs(def); err == nil && abs == dir {
			rel := []string{".config", "Claude-3p", "configLibrary"}
			if runtime.GOOS == "darwin" {
				rel = []string{"Library", "Application Support", "Claude-3p", "configLibrary"}
			}
			if native, err := configDir("", "", rel...); err == nil {
				library = native
			}
		}
	}
	return filepath.Join(library, claudeDesktopConfigID+".json")
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

// claudeDesktopModel is one picker entry. The app knows nothing about an
// alias: behavesAs gives it the Claude model's limits and effort levels, the
// 1M keys its real window (else the app assumes 200k), and the sonnet tier
// makes it the model Auto mode's checks go to.
func claudeDesktopModel(r Route, sonnetTier bool) map[string]any {
	model := map[string]any{"name": claudeDesktopModelPrefix + r.ID, "labelOverride": cmp.Or(r.DisplayName, r.Name)}
	if r.Provider == ProviderAnthropic && r.UpstreamModel != "" {
		model["behavesAs"] = r.UpstreamModel
	}
	if r.ContextWindow >= extendedContextWindow {
		model["supports1m"], model["prefer1m"] = true, true
	}
	if sonnetTier {
		model["anthropicFamilyTier"] = "sonnet"
	}
	return model
}

// claudeDesktopPrepare plans the app's files. routes are the routes Claude
// Code lists; the app lists those under a model ID it accepts, the primary
// first, so a denied route is absent from the app's picker.
func claudeDesktopPrepare(path string, routes []Route, selection Selection, endpoint string, helper *Helper) ([]*Plan, error) {
	s, err := selection.Resolve(routes)
	if err != nil {
		return nil, err
	}
	listed := slices.DeleteFunc(slices.Clone(routes), func(r Route) bool {
		return claudeDesktopDeniedID(claudeDesktopModelPrefix + r.ID)
	})
	if len(listed) == 0 {
		return nil, errors.New("Claude Desktop can list none of the routes: their model IDs match the app's denylist")
	}
	// Auto mode's checks go to the app's "sonnet" model: the background route
	// when the app lists it, else the primary.
	tier := s.Primary
	if _, ok := routeByName(listed, s.Background); ok {
		tier = s.Background
	}
	// The first model is Claude Desktop's default.
	var models []any
	if primary, ok := routeByName(listed, s.Primary); ok {
		models = append(models, claudeDesktopModel(primary, primary.Name == tier))
	}
	for _, r := range listed {
		if r.Name != s.Primary {
			models = append(models, claudeDesktopModel(r, r.Name == tier))
		}
	}
	values := []setting{
		desired([]string{"inferenceProvider"}, "gateway"),
		desired([]string{"inferenceGatewayBaseUrl"}, endpoint),
		desired([]string{"inferenceGatewayAuthScheme"}, "bearer"),
		desired([]string{"inferenceCustomHeaders"}, map[string]any{clientHeader: ClaudeCode}),
		desired([]string{"modelDiscoveryEnabled"}, false),
		// Tool search keeps MCP tool schemas out of every request; without it a
		// large MCP setup fills the context and trips model tool-count limits.
		desired([]string{"toolSearchEnabled"}, true),
		// Deleted, not false: the app's default offers Auto mode in Code sessions.
		{Path: []string{"autoModeEnabled"}},
		desired([]string{"inferenceModels"}, models),
	}
	credentialPath := claudeDesktopCredentialPath
	if helper != nil {
		credentialPath = nil
		values = append(values,
			desired([]string{"inferenceCredentialKind"}, "helper-script"),
			desired(claudeDesktopHelperPaths[0], helper.Path),
			desired(claudeDesktopHelperPaths[1], helper.args(Claude)),
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

// claudeDesktopCredential returns the static key in the app's config, or ""
// once the app has sealed it, since a sealed key is unreadable.
func claudeDesktopCredential(path string) (string, error) {
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

func claudeDesktopTeardown(path string) ([]*Plan, error) {
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
	// config the user applied since stays in charge. The mode is written, not
	// deleted: the app treats a missing deploymentMode as a first run before
	// sign-in.
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

// claudeDesktopInspect reports the app's integration state. Its routes are
// route IDs with the app's labels, since the config holds no route names.
func claudeDesktopInspect(path string) (Status, error) {
	r, data, err := inspectConfig(Detection{Path: path})
	if err != nil {
		return r, err
	}
	r.DefaultRoute = ""
	if !claudeDesktopOwned(data) {
		return r, nil
	}
	r.managed(data, claudeDesktopPaths)
	_, meta, err := readConfig(claudeDesktopMetaPath(path))
	if err != nil {
		return r, err
	}
	_, app, err := readConfig(claudeDesktopAppConfigPath(path))
	if err != nil {
		return r, err
	}
	if meta["appliedId"] != claudeDesktopID(path) || app["deploymentMode"] != "3p" {
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
