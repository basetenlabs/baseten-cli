package harness

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
)

type piHarness struct{}

// piSettingsPath is Pi's settings file, next to the models.json that holds
// Baseten's provider. Both files are shared with the user's own settings.
func piSettingsPath(path string) string {
	return filepath.Join(filepath.Dir(path), "settings.json")
}

var piProviderPath = []string{"providers", providerID}

var piCredentialPath = []string{"providers", providerID, "apiKey"}

// longContextTokens is where long-context prices start, as OpenCode's
// context_over_200k assumes.
const longContextTokens = 200000

func (piHarness) Credential(path string) (string, error) {
	return credential(path, piCredentialPath)
}

func (piHarness) Name() string { return Pi }

func (h piHarness) Detect(ctx context.Context, execer Execer, dir string) (Detection, error) {
	dir, err := configDir(dir, "PI_CODING_AGENT_DIR", ".pi", "agent")
	if err != nil {
		return Detection{}, err
	}
	return detect(ctx, execer, h.Name(), "pi", filepath.Join(dir, "models.json"))
}

func (piHarness) BackgroundRoute(Selection) string { return "" }

// Routes returns every route, since Pi picks each route's API by its target.
func (piHarness) Routes(routes []Route) []Route { return routes }

func (piHarness) Prepare(path string, routes []Route, s Selection, endpoint string) ([]*Plan, error) {
	switch {
	case s.Background != "":
		return nil, errors.New("--background-route is supported only for Claude Code and OpenCode")
	case s.Subagent != "":
		return nil, errors.New("--subagent-route is supported only for Claude Code and OpenCode")
	case s.Fallback != "":
		return nil, errors.New("--fallback-route is supported only for Claude Code")
	}
	s, err := s.Resolve(routes)
	if err != nil {
		return nil, err
	}
	baseURL := strings.TrimRight(endpoint, "/")
	models := []any{}
	for _, r := range routes {
		models = append(models, piModel(r, baseURL))
	}
	provider := desired(piProviderPath, map[string]any{
		"name":    "Baseten",
		"baseUrl": baseURL + "/v1",
		"api":     "openai-completions",
		"apiKey":  "",
		// Pi sends apiKey as x-api-key to Anthropic routes, and Baseten
		// authenticates only Authorization.
		"authHeader": true,
		"headers":    map[string]any{clientHeader: Pi},
		"models":     models,
	})
	p, err := prepareSettings(path, piCredentialPath, func(map[string]any) ([]setting, error) {
		return []setting{provider}, nil
	})
	if err != nil {
		return nil, err
	}
	settings, err := prepareSettings(piSettingsPath(path), nil, func(map[string]any) ([]setting, error) {
		return []setting{
			desired([]string{"defaultProvider"}, providerID),
			desired([]string{"defaultModel"}, s.Primary),
		}, nil
	})
	if err != nil {
		return nil, err
	}
	// The models precede the settings that select one.
	return []*Plan{p, settings}, nil
}

// piModel describes a route to Pi. A model's api overrides the provider's, so
// first-party routes use their native API: Messages for Anthropic, Responses
// for OpenAI.
func piModel(r Route, baseURL string) map[string]any {
	levels := piThinkingLevels(r.ReasoningLevels)
	model := map[string]any{
		"id":            r.Name,
		"name":          r.DisplayName,
		"contextWindow": r.ContextWindow,
		"maxTokens":     r.OutputLimit,
		"input":         r.InputModalities,
		"reasoning":     levels != nil,
	}
	if levels != nil {
		model["thinkingLevelMap"] = levels
	}
	if cost := piCost(r.Cost); cost != nil {
		model["cost"] = cost
	}
	switch r.Target {
	case TargetAnthropic:
		// Pi's Anthropic client appends /v1/messages itself.
		model["api"], model["baseUrl"] = "anthropic-messages", baseURL
	case TargetOpenAI:
		model["api"] = "openai-responses"
	default:
		// The provider's compat would apply to every API, so only Chat
		// Completions models carry it, as Pi's built-in Baseten provider sets it.
		model["compat"] = map[string]any{
			"supportsStore":           false,
			"supportsDeveloperRole":   false,
			"supportsReasoningEffort": levels != nil,
			"maxTokensField":          "max_tokens",
			"supportsStrictMode":      true,
			// Prompt caching needs related requests to reach the same replica.
			"sendSessionAffinityHeaders": true,
			"supportsLongCacheRetention": false,
		}
	}
	return model
}

// piThinkingLevels maps each of Pi's thinking levels to the route's effort
// level, or to nil to hide it. Off is hidden unless the route accepts none.
// It returns nil if the route has no real effort levels.
func piThinkingLevels(levels []string) map[string]any {
	if _, high := reasoningBounds(levels); high == nil {
		return nil
	}
	m := map[string]any{"off": nil}
	if slices.Contains(levels, "none") {
		m["off"] = "none"
	}
	for _, level := range reasoningOrder {
		m[level] = nil
		if slices.Contains(levels, level) {
			m[level] = level
		}
	}
	return m
}

// piCost is Pi's per-model price, in USD per 1M tokens. Pi requires every
// price, so it is nil unless the cache prices are known too.
func piCost(c *Cost) map[string]any {
	if c == nil || c.CacheRead == nil || c.CacheWrite == nil {
		return nil
	}
	cost := map[string]any{"input": c.Input, "output": c.Output, "cacheRead": *c.CacheRead, "cacheWrite": *c.CacheWrite}
	if tier := piCost(c.LongContext); tier != nil {
		tier["inputTokensAbove"] = longContextTokens
		cost["tiers"] = []any{tier}
	}
	return cost
}

// piSettingsTeardownPaths resets Pi's default model only while Baseten is the
// default provider; a default the user pointed at another provider is theirs.
func piSettingsTeardownPaths(data map[string]any) [][]string {
	if data["defaultProvider"] != providerID {
		return nil
	}
	return [][]string{{"defaultProvider"}, {"defaultModel"}}
}

func (piHarness) Teardown(path string) ([]*Plan, error) {
	settings, err := prepareTeardown(piSettingsPath(path), piSettingsTeardownPaths)
	if err != nil {
		return nil, err
	}
	models, err := prepareTeardown(path, func(map[string]any) [][]string { return [][]string{piProviderPath} })
	if err != nil {
		return nil, err
	}
	// The settings that select a model precede the models.
	return []*Plan{settings, models}, nil
}

func (piHarness) Inspect(d Detection) (Status, error) {
	r, models, err := inspectConfig(d)
	if err != nil {
		return r, err
	}
	_, settings, err := readConfig(piSettingsPath(d.Path))
	if err != nil {
		return r, err
	}
	r.DefaultRoute, _ = settings["defaultModel"].(string)
	r.managed(models, [][]string{piProviderPath})
	r.managed(settings, piSettingsTeardownPaths(settings))
	switch {
	case r.State == StateNotConfigured:
		return r, nil
	case !get(models, piProviderPath).Exists:
		r.State = StateIncomplete
	case settings["defaultProvider"] != providerID:
		r.State = StateInactive
	}
	labels := map[string]string{}
	entries, _ := get(models, []string{"providers", providerID, "models"}).Data.([]any)
	for _, entry := range entries {
		if row, ok := entry.(map[string]any); ok {
			name, _ := row["id"].(string)
			labels[name], _ = row["name"].(string)
		}
	}
	r.addRoutes(labels)
	return r, nil
}
