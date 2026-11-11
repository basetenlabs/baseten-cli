package harness

import (
	"cmp"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"
)

type piHarness struct{}

//go:embed pi_served_model.ts
var piServedModelTemplate string

//go:embed pi_git_context.ts
var piGitContextSource []byte

const piServedModelIntelligentRoutesSlot = "Object.entries({})"

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
	token, err := credential(path, piCredentialPath)
	// A "!command" apiKey is the helper, not a key.
	if strings.HasPrefix(token, "!") {
		return "", err
	}
	return token, err
}

func (piHarness) Name() string { return Pi }

func (h piHarness) Detect(ctx context.Context, execer Execer, dir string) (Detection, error) {
	dir, err := configDir(dir, "PI_CODING_AGENT_DIR", ".pi", "agent")
	if err != nil {
		return Detection{}, err
	}
	return detect(ctx, execer, h.Name(), []candidate{cliCandidate("pi")}, filepath.Join(dir, "models.json"))
}

func (piHarness) BackgroundRoute(Selection) string { return "" }

// Routes returns every route, since Pi picks each route's API per model.
func (piHarness) Routes(routes []Route) []Route { return routes }

func (piHarness) SupportsSubagent() bool { return false }

func (piHarness) SupportsHelper() bool { return true }

func (piHarness) Prepare(path string, routes []Route, opts PrepareOptions) ([]*Plan, error) {
	s, endpoint, helper := opts.Selection, opts.Endpoint, opts.Helper
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
	baseURL := strings.TrimRight(endpoint, "/")
	models := []any{}
	for _, r := range routes {
		models = append(models, piModel(r, baseURL))
	}
	apiKey, credentialPath := "", piCredentialPath
	if helper != nil {
		apiKey, credentialPath = "!"+helper.shellCommand(Pi), nil
	}
	provider := desired(piProviderPath, map[string]any{
		"name":    "Baseten",
		"baseUrl": baseURL + "/v1",
		"api":     "openai-completions",
		"apiKey":  apiKey,
		// Pi sends apiKey as x-api-key to Anthropic routes, and Baseten
		// authenticates only Authorization.
		"authHeader": true,
		"headers":    map[string]any{clientHeader: Pi},
		"models":     models,
	})
	p, err := prepareSettings(path, credentialPath, func(map[string]any) ([]setting, error) {
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
	served := routes
	if opts.NoPiServedModel {
		served = nil
	}
	extension, err := piServedModelPlans(path, served)
	if err != nil {
		return nil, err
	}
	var gitContext []*Plan
	if opts.GitContext {
		gitContext, err = prepareSealedFile(piGitContextPath(path), piGitContextSource, "Pi sessions will not include the git repository, branch, and commit")
	} else {
		gitContext, err = prepareSealedFileRemoval(piGitContextPath(path))
	}
	if err != nil {
		return nil, err
	}
	// The models precede the settings that select one.
	return slices.Concat([]*Plan{p, settings}, extension, gitContext), nil
}

func piGitContextPath(path string) string {
	return filepath.Join(filepath.Dir(path), "extensions", "baseten-git-context.ts")
}

func piServedModelPath(path string) string {
	return filepath.Join(filepath.Dir(path), "extensions", "baseten-served-model.ts")
}

func piServedModelPlans(path string, routes []Route) ([]*Plan, error) {
	intelligentRoutes := piServedModelIntelligentRoutes(routes)
	if len(intelligentRoutes) == 0 {
		return prepareSealedFileRemoval(piServedModelPath(path))
	}
	return prepareSealedFile(piServedModelPath(path), piServedModelSource(intelligentRoutes), "Pi will not show which model served auto-route responses")
}

func piServedModelIntelligentRoutes(routes []Route) map[string]string {
	intelligentRoutes := map[string]string{}
	for _, r := range routes {
		if r.IntelligentRoute {
			intelligentRoutes[r.Name] = cmp.Or(r.DisplayName, r.Name)
		}
	}
	return intelligentRoutes
}

func piServedModelSource(intelligentRoutes map[string]string) []byte {
	encoded, _ := json.Marshal(intelligentRoutes)
	return []byte(strings.Replace(piServedModelTemplate, piServedModelIntelligentRoutesSlot, "Object.entries("+string(encoded)+")", 1))
}

// piModel describes a route to Pi. A model's api overrides the provider's, so
// a route is called with the API its supported formats advertise.
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
	switch r.TargetAPI {
	case TargetAPIMessages:
		// Pi's Anthropic client appends /v1/messages itself.
		model["api"], model["baseUrl"] = "anthropic-messages", baseURL
		compat := map[string]any{}
		if piAdaptiveThinking(r.UpstreamModel) {
			compat["forceAdaptiveThinking"] = true
		}
		if r.IntelligentRoute {
			compat["sendSessionAffinityHeaders"] = true
		}
		if len(compat) > 0 {
			model["compat"] = compat
		}
	case TargetAPIResponses:
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

func piAdaptiveThinking(upstreamModel string) bool {
	// NB(nikhil): We simplify with substring check for now, given fully correct coverage today, and
	// seemingly low risk of a new model ID with these identifiers that supports extended thinking.
	for _, legacy := range []string{"claude-opus-4-5", "claude-haiku-4-5", "claude-sonnet-4-5"} {
		if strings.Contains(upstreamModel, legacy) {
			return false
		}
	}

	return true
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
	extension, err := prepareSealedFileRemoval(piServedModelPath(path))
	if err != nil {
		return nil, err
	}
	gitContext, err := prepareSealedFileRemoval(piGitContextPath(path))
	if err != nil {
		return nil, err
	}
	// The settings that select a model precede the models.
	return slices.Concat([]*Plan{settings, models}, extension, gitContext), nil
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
