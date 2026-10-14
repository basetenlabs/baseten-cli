package harness

import (
	"cmp"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

type openCodeHarness struct{}

// OpenCode reads two settings formats from the same file. V1 is the original
// format; OpenCode 2 adds a native format and keeps reading V1. Setup writes
// V1 unless the file already has native V2 settings, so a file that works
// with both versions stays that way until the user migrates it themselves.
type openCodeFormat int

const (
	openCodeV1 openCodeFormat = iota + 1
	openCodeV2
)

// openCodeV2Keys are top-level settings only the native V2 format has. Each
// one replaces a V1 key, so any of them means the file has been migrated.
var openCodeV2Keys = []string{"providers", "agents", "permissions", "plugins", "commands"}

// openCodeFormatOf picks the format setup writes to a file with these settings.
func openCodeFormatOf(data map[string]any) openCodeFormat {
	for _, key := range openCodeV2Keys {
		if _, ok := data[key]; ok {
			return openCodeV2
		}
	}
	return openCodeV1
}

var (
	openCodeV1ProviderPath = []string{"provider", providerID}
	openCodeV2ProviderPath = []string{"providers", providerID}
)

// Subagent and background model settings, per format. V2 has no small_model;
// the title agent is what OpenCode 2 maps it to.
var (
	openCodeV1SubagentPaths  = [][]string{{"agent", "general", "model"}, {"agent", "explore", "model"}}
	openCodeV2SubagentPaths  = [][]string{{"agents", "general", "model"}, {"agents", "explore", "model"}}
	openCodeV1BackgroundPath = []string{"small_model"}
	openCodeV2BackgroundPath = []string{"agents", "title", "model"}
)

// openCodeProviderPackages maps route providers to the AI SDK package for
// their native API. Other providers use the Chat Completions package.
var openCodeProviderPackages = map[string]string{
	ProviderAnthropic: "@ai-sdk/anthropic",
	ProviderOpenAI:    "@ai-sdk/openai",
}

// openCodeV2Packages maps the AI SDK packages V1 names to OpenCode 2's
// built-in provider packages.
var openCodeV2Packages = map[string]string{
	"@ai-sdk/openai-compatible": "@opencode/ai/providers/openai-compatible",
	"@ai-sdk/anthropic":         "@opencode/ai/providers/anthropic",
	"@ai-sdk/openai":            "@opencode/ai/providers/openai",
}

var (
	openCodeV1CredentialPath = []string{"provider", providerID, "options", "apiKey"}
	openCodeV2CredentialPath = []string{"providers", providerID, "settings", "apiKey"}
)

// The bearer paths repeat the credential as a header because the Anthropic
// package sends apiKey as x-api-key, and Baseten authenticates only Authorization.
var (
	openCodeV1BearerPath = []string{"provider", providerID, "options", "headers", "Authorization"}
	openCodeV2BearerPath = []string{"providers", providerID, "headers", "Authorization"}
)

func (openCodeHarness) Credential(path string) (string, error) {
	_, data, err := readConfig(path)
	if err != nil {
		return "", err
	}
	// A file can hold both formats while one setup replaces the other, but
	// the credential is the same key in each.
	for _, key := range [][]string{openCodeV2CredentialPath, openCodeV1CredentialPath} {
		if token, _ := get(data, key).Data.(string); token != "" {
			return token, nil
		}
	}
	return "", nil
}

func (openCodeHarness) Name() string { return OpenCode }

func (h openCodeHarness) Detect(ctx context.Context, execer Execer, dir string) (Detection, error) {
	if dir == "" {
		base, err := configDir("", "XDG_CONFIG_HOME", ".config")
		if err != nil {
			return Detection{}, err
		}
		dir = filepath.Join(base, "opencode")
	}
	// OpenCode reads opencode.jsonc in preference to opencode.json.
	path := filepath.Join(dir, "opencode.jsonc")
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		path = filepath.Join(dir, "opencode.json")
	} else if err != nil {
		return Detection{}, err
	}
	return detect(ctx, execer, h.Name(), openCodeCandidates(), path)
}

// openCodeCandidates lists the CLI on PATH, then desktop app bundles, then
// the CLI copy the desktop app's installer leaves in ~/.opencode/bin, which
// is a CLI whether or not it is on PATH.
func openCodeCandidates() []candidate {
	candidates := append([]candidate{cliCandidate("opencode")}, desktopCandidates(desktopOpenCodeBinaries()...)...)
	if home, err := os.UserHomeDir(); err == nil && runtime.GOOS != "windows" {
		candidates = append(candidates, cliCandidate(filepath.Join(home, ".opencode", "bin", "opencode")))
	}
	return candidates
}

// linuxDesktopOpenCode is where OpenCode Desktop's Linux packages install the
// CLI they bundle.
const linuxDesktopOpenCode = "/opt/OpenCode/resources/opencode-cli"

// OpenCodeDesktopLocations describes where Detect looks for a desktop-bundled
// OpenCode on this platform, or "" if it looks only on PATH.
func OpenCodeDesktopLocations() string {
	switch runtime.GOOS {
	case "darwin":
		return "OpenCode.app or ~/.opencode/bin"
	case "linux":
		return filepath.Dir(filepath.Dir(linuxDesktopOpenCode)) + " or ~/.opencode/bin"
	}
	return ""
}

// desktopOpenCodeBinaries lists where OpenCode Desktop bundles the CLI
// without adding it to PATH. The bundled CLI shares the CLI's settings file.
// Nothing is listed on Windows, where harness setup is not supported.
func desktopOpenCodeBinaries() []string {
	switch runtime.GOOS {
	case "darwin":
		roots := []string{"/Applications"}
		if home, err := os.UserHomeDir(); err == nil {
			roots = append(roots, filepath.Join(home, "Applications"))
		}
		var binaries []string
		for _, root := range roots {
			for _, app := range []string{"OpenCode.app", "OpenCode Beta.app"} {
				binaries = append(binaries, filepath.Join(root, app, "Contents", "Resources", "opencode-cli"))
			}
		}
		return binaries
	case "linux":
		return []string{linuxDesktopOpenCode}
	}
	return nil
}

func (openCodeHarness) BackgroundRoute(s Selection) string {
	return cmp.Or(s.Background, defaultBackgroundRoute)
}

// Routes returns every route, since OpenCode picks each route's API by its target.
func (openCodeHarness) Routes(routes []Route) []Route { return routes }

func (openCodeHarness) Prepare(path string, routes []Route, s Selection, endpoint string) ([]*Plan, error) {
	if s.Fallback != "" {
		return nil, errors.New("--fallback-route is supported only for Claude Code")
	}
	explicitSubagent := s.Subagent != ""
	s, err := s.Resolve(routes)
	if err != nil {
		return nil, err
	}
	_, current, err := readConfig(path)
	if err != nil {
		return nil, err
	}
	format := openCodeFormatOf(current)
	credentialPath, bearerPath := openCodeV1CredentialPath, openCodeV1BearerPath
	if format == openCodeV2 {
		credentialPath, bearerPath = openCodeV2CredentialPath, openCodeV2BearerPath
	}
	p, err := prepareSettings(path, credentialPath, func(current map[string]any) ([]setting, error) {
		// Like the API key, keep the file's header until ApplyPlans inserts the real one.
		bearer, _ := get(current, bearerPath).Data.(string)
		var values []setting
		if format == openCodeV2 {
			values = openCodeV2Settings(routes, s, endpoint, bearer)
		} else {
			values = openCodeV1Settings(routes, s, endpoint, bearer)
		}
		subagentPaths := openCodeV1SubagentPaths
		if format == openCodeV2 {
			subagentPaths = openCodeV2SubagentPaths
		}
		if explicitSubagent {
			for _, path := range subagentPaths {
				values = append(values, desired(path, providerID+"/"+s.Subagent))
			}
		} else {
			// A refresh may retire the route an earlier --subagent-route selected.
			for _, key := range subagentPaths {
				model, _ := get(current, key).Data.(string)
				route, ours := strings.CutPrefix(model, providerID+"/")
				if ours && !slices.ContainsFunc(routes, func(r Route) bool { return r.Name == route }) {
					values = append(values, setting{Path: key})
				}
			}
		}
		// Setup in one format retires the other's settings, so OpenCode 2 never
		// sees the same value set both ways.
		if format == openCodeV2 {
			for _, key := range openCodeOwnedPaths(current, openCodeV1) {
				values = append(values, setting{Path: key})
			}
		}
		return values, nil
	})
	if err != nil {
		return nil, err
	}
	p.bearerPath = bearerPath
	return []*Plan{p}, nil
}

// openCodeV1Settings builds the original settings format.
func openCodeV1Settings(routes []Route, s Selection, endpoint, bearer string) []setting {
	models := map[string]any{}
	if s.Background == "" {
		s.Background = defaultBackgroundRoute
		models[defaultBackgroundRoute] = map[string]any{"name": "DeepSeek V4.1 Flash"}
	}
	for _, r := range routes {
		variants := map[string]any{}
		for _, level := range xhighReasoningLevels(r.ReasoningLevels) {
			variants[level] = map[string]any{"reasoningEffort": level}
		}
		model := map[string]any{
			"name":       r.DisplayName,
			"limit":      map[string]any{"context": r.ContextWindow, "output": r.OutputLimit},
			"tool_call":  r.Tools,
			"modalities": map[string]any{"input": r.InputModalities, "output": []string{"text"}},
			"reasoning":  len(r.ReasoningLevels) > 0,
			"variants":   variants,
		}
		if r.Cost != nil {
			model["cost"] = openCodeV1Cost(*r.Cost)
		}
		// A model's npm overrides the provider's, so first-party routes use their
		// native API at the same base URL: Messages for Anthropic, Responses for OpenAI.
		if npm, ok := openCodeProviderPackages[r.Provider]; ok {
			model["provider"] = map[string]any{"npm": npm}
		}
		models[r.Name] = model
	}
	return []setting{
		desired([]string{"model"}, providerID+"/"+s.Primary),
		desired(openCodeV1BackgroundPath, providerID+"/"+s.Background),
		desired(openCodeV1ProviderPath, map[string]any{
			"npm":  "@ai-sdk/openai-compatible",
			"name": "Baseten",
			"options": map[string]any{
				"baseURL": strings.TrimRight(endpoint, "/") + "/v1",
				"apiKey":  "",
				"headers": map[string]any{"Authorization": bearer, clientHeader: OpenCode},
			},
			"models": models,
		}),
	}
}

// openCodeV2Settings builds OpenCode 2's native settings format.
func openCodeV2Settings(routes []Route, s Selection, endpoint, bearer string) []setting {
	models := map[string]any{}
	if s.Background == "" {
		s.Background = defaultBackgroundRoute
		models[defaultBackgroundRoute] = map[string]any{"name": "DeepSeek V4.1 Flash"}
	}
	for _, r := range routes {
		variants := []any{}
		for _, level := range xhighReasoningLevels(r.ReasoningLevels) {
			variants = append(variants, map[string]any{"id": level, "settings": map[string]any{"reasoningEffort": level}})
		}
		model := map[string]any{
			"name":         r.DisplayName,
			"limit":        map[string]any{"context": r.ContextWindow, "output": r.OutputLimit},
			"capabilities": map[string]any{"tools": r.Tools, "input": r.InputModalities, "output": []string{"text"}},
			"variants":     variants,
		}
		if r.Cost != nil {
			model["cost"] = openCodeV2Cost(*r.Cost)
		}
		if npm, ok := openCodeProviderPackages[r.Provider]; ok {
			model["package"] = openCodeV2Packages[npm]
		}
		models[r.Name] = model
	}
	return []setting{
		desired([]string{"model"}, providerID+"/"+s.Primary),
		desired(openCodeV2BackgroundPath, providerID+"/"+s.Background),
		desired(openCodeV2ProviderPath, map[string]any{
			"name":     "Baseten",
			"package":  openCodeV2Packages["@ai-sdk/openai-compatible"],
			"settings": map[string]any{"baseURL": strings.TrimRight(endpoint, "/") + "/v1", "apiKey": ""},
			"headers":  map[string]any{"Authorization": bearer, clientHeader: OpenCode},
			"models":   models,
		}),
	}
}

// openCodeV1Cost is OpenCode's per-model price, in USD per 1M tokens.
func openCodeV1Cost(c Cost) map[string]any {
	cost := map[string]any{"input": c.Input, "output": c.Output}
	if c.CacheRead != nil {
		cost["cache_read"] = *c.CacheRead
	}
	if c.CacheWrite != nil {
		cost["cache_write"] = *c.CacheWrite
	}
	if c.LongContext != nil {
		cost["context_over_200k"] = openCodeV1Cost(*c.LongContext)
	}
	return cost
}

// openCodeV2Cost is OpenCode 2's per-model price list: a base price, then a
// long-context tier when the provider has one.
func openCodeV2Cost(c Cost) []any {
	tier := func(c Cost) map[string]any {
		cost := map[string]any{"input": c.Input, "output": c.Output}
		cache := map[string]any{}
		if c.CacheRead != nil {
			cache["read"] = *c.CacheRead
		}
		if c.CacheWrite != nil {
			cache["write"] = *c.CacheWrite
		}
		if len(cache) > 0 {
			cost["cache"] = cache
		}
		return cost
	}
	costs := []any{tier(c)}
	if c.LongContext != nil {
		long := tier(*c.LongContext)
		long["tier"] = map[string]any{"type": "context", "size": longContextTokens}
		costs = append(costs, long)
	}
	return costs
}

// openCodeOwnedPaths lists Baseten's settings in one format: the provider, and
// any model selection that points at it. A model the user pointed at another
// provider is theirs.
func openCodeOwnedPaths(data map[string]any, format openCodeFormat) [][]string {
	provider, background, subagents := openCodeV1ProviderPath, openCodeV1BackgroundPath, openCodeV1SubagentPaths
	if format == openCodeV2 {
		provider, background, subagents = openCodeV2ProviderPath, openCodeV2BackgroundPath, openCodeV2SubagentPaths
	}
	var paths [][]string
	if get(data, provider).Exists {
		paths = append(paths, provider)
	}
	for _, key := range append([][]string{background}, subagents...) {
		if model, _ := get(data, key).Data.(string); strings.HasPrefix(model, providerID+"/") {
			paths = append(paths, key)
		}
	}
	return paths
}

// openCodeTeardownPaths clears Baseten's settings in both formats, since a
// file may have been migrated after setup.
func openCodeTeardownPaths(data map[string]any) [][]string {
	paths := [][]string{openCodeV1ProviderPath, openCodeV2ProviderPath}
	if model, _ := data["model"].(string); strings.HasPrefix(model, providerID+"/") {
		paths = append(paths, []string{"model"})
	}
	for _, format := range []openCodeFormat{openCodeV1, openCodeV2} {
		for _, key := range openCodeOwnedPaths(data, format) {
			if !slices.ContainsFunc(paths, func(p []string) bool { return slices.Equal(p, key) }) {
				paths = append(paths, key)
			}
		}
	}
	return paths
}

func (openCodeHarness) Teardown(path string) ([]*Plan, error) {
	p, err := prepareTeardown(path, openCodeTeardownPaths)
	if err != nil {
		return nil, err
	}
	return []*Plan{p}, nil
}

func (openCodeHarness) Inspect(d Detection) (Status, error) {
	r, data, err := inspectConfig(d)
	if err != nil {
		return r, err
	}
	r.managed(data, openCodeTeardownPaths(data))
	if r.State == StateNotConfigured {
		return r, nil
	}
	// Prefer the native format: OpenCode 2 does, when a file has both.
	providerPath, backgroundPath := openCodeV2ProviderPath, openCodeV2BackgroundPath
	if !get(data, providerPath).Exists {
		providerPath, backgroundPath = openCodeV1ProviderPath, openCodeV1BackgroundPath
	}
	if !get(data, providerPath).Exists {
		r.State = StateIncomplete
	} else if !strings.HasPrefix(r.DefaultRoute, providerID+"/") {
		r.State = StateInactive
	}
	r.DefaultRoute = strings.TrimPrefix(r.DefaultRoute, providerID+"/")
	small, _ := get(data, backgroundPath).Data.(string)
	r.BackgroundRoute = strings.TrimPrefix(small, providerID+"/")
	labels := map[string]string{}
	models, _ := get(data, append(slices.Clone(providerPath), "models")).Data.(map[string]any)
	for name, model := range models {
		// The default background model is not a route, so it is not listed.
		if row, ok := model.(map[string]any); ok && name != defaultBackgroundRoute {
			labels[name], _ = row["name"].(string)
		}
	}
	r.addRoutes(labels)
	return r, nil
}
