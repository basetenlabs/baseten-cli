package harness

import (
	"cmp"
	"context"
	"path/filepath"
	"slices"
	"strconv"
)

type claudeCodeHarness struct{}

// claudeMarker identifies Baseten's settings, since Claude Code has no named
// provider block to look for.
const claudeMarker = "BASETEN_HARNESS"

var claudeSubagentPath = []string{"env", "CLAUDE_CODE_SUBAGENT_MODEL"}

var claudeCredentialPath = []string{"env", "ANTHROPIC_AUTH_TOKEN"}

// extendedContextWindow is the smallest context window Claude Code is told is
// 1M, via a [1m] model ID suffix. Smaller windows get its 200K default.
const extendedContextWindow = 500000

var claudePaths = [][]string{
	{"model"}, {"fallbackModel"}, {"modelPicker", "options"},
	{"modelPicker", "replaceBuiltInOptions"}, {"availableModels"},
	{"modelOverrides"}, {"modelSettings"},
	{"env", claudeMarker}, {"env", "ANTHROPIC_BASE_URL"},
	{"env", "ANTHROPIC_AUTH_TOKEN"}, {"env", "ANTHROPIC_DEFAULT_SONNET_MODEL"},
	{"env", "ANTHROPIC_DEFAULT_OPUS_MODEL"}, {"env", "ANTHROPIC_DEFAULT_FABLE_MODEL"},
	{"env", "ANTHROPIC_DEFAULT_HAIKU_MODEL"}, {"env", "ANTHROPIC_SMALL_FAST_MODEL"},
	{"env", "CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY"}, {"env", "CLAUDE_CODE_MAX_OUTPUT_TOKENS"},
}

func (claudeCodeHarness) Name() string { return ClaudeCode }

func (h claudeCodeHarness) Detect(ctx context.Context, execer Execer, dir string) (Detection, error) {
	dir, err := configDir(dir, "CLAUDE_CONFIG_DIR", ".claude")
	if err != nil {
		return Detection{}, err
	}
	return detect(ctx, execer, h.Name(), "claude", filepath.Join(dir, "settings.json"))
}

func (claudeCodeHarness) BackgroundRoute(s Selection) string {
	return cmp.Or(s.Background, defaultBackgroundRoute)
}

func (h claudeCodeHarness) Prepare(path string, routes []Route, s Selection, endpoint string) ([]*Plan, error) {
	p, err := prepareSettings(path, [][]string{claudeCredentialPath}, func(data map[string]any) ([]setting, error) {
		return claudeSettings(routes, s, endpoint, data)
	})
	if err != nil {
		return nil, err
	}
	return []*Plan{p}, nil
}

func claudeSettings(routes []Route, selection Selection, endpoint string, current map[string]any) ([]setting, error) {
	s, err := selection.resolve(routes)
	if err != nil {
		return nil, err
	}
	background := cmp.Or(s.Background, defaultBackgroundRoute)
	// Claude Code sizes the context window from the model ID, and
	// modelOverrides sends the plain route name.
	modelID := func(name string) string {
		if r, ok := routeByName(routes, name); ok && r.ContextWindow >= extendedContextWindow {
			return name + "[1m]"
		}
		return name
	}
	options := []any{}
	allowed := []any{}
	overrides := map[string]any{}
	effort := map[string]any{}
	for _, r := range routes {
		id := modelID(r.Name)
		options = append(options, map[string]any{"model": id, "label": r.DisplayName})
		allowed = append(allowed, id)
		if id != r.Name {
			overrides[id] = r.Name
		}
		if low, high := reasoningBounds(r.ReasoningLevels); high != nil {
			effort[id] = map[string]any{"maxEffortLevel": high, "effortLevel": low}
		}
	}
	if !slices.Contains(allowed, any(background)) {
		allowed = append(allowed, background)
	}
	values := []setting{
		desired([]string{"model"}, modelID(s.Primary)),
		desired([]string{"fallbackModel"}, []string{modelID(s.Fallback)}),
		desired([]string{"modelPicker", "options"}, options),
		desired([]string{"modelPicker", "replaceBuiltInOptions"}, true),
		desired([]string{"availableModels"}, allowed),
		desired([]string{"modelOverrides"}, overrides),
		desired([]string{"modelSettings"}, effort),
	}
	for _, kv := range [][2]string{
		{claudeMarker, "1"},
		{"ANTHROPIC_BASE_URL", endpoint},
		{"ANTHROPIC_AUTH_TOKEN", ""},
		{"ANTHROPIC_DEFAULT_SONNET_MODEL", modelID(s.Primary)},
		{"ANTHROPIC_DEFAULT_OPUS_MODEL", modelID(s.Primary)},
		{"ANTHROPIC_DEFAULT_FABLE_MODEL", modelID(s.Primary)},
		{"ANTHROPIC_DEFAULT_HAIKU_MODEL", modelID(background)},
		{"ANTHROPIC_SMALL_FAST_MODEL", modelID(background)},
		{"CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY", "0"},
	} {
		values = append(values, desired([]string{"env", kv[0]}, kv[1]))
	}
	// Output limits have no per-model setting, so the primary route's applies
	// after a /model switch too.
	primary, _ := routeByName(routes, s.Primary)
	values = append(values, desired([]string{"env", "CLAUDE_CODE_MAX_OUTPUT_TOKENS"}, strconv.Itoa(primary.OutputLimit)))
	if selection.Subagent != "" {
		values = append(values, desired(claudeSubagentPath, modelID(s.Subagent)))
	} else if get(current, []string{"env", claudeMarker}).Data == "1" {
		// A refresh may retire the route an earlier --subagent-route selected.
		previous, _ := get(current, claudeSubagentPath).Data.(string)
		if _, owned := claudeRoutes(current)[previous]; owned && !slices.Contains(allowed, any(previous)) {
			values = append(values, setting{Path: claudeSubagentPath})
		}
	}
	return values, nil
}

func claudeRoutes(data map[string]any) map[string]string {
	labels := map[string]string{}
	options, _ := get(data, []string{"modelPicker", "options"}).Data.([]any)
	for _, option := range options {
		if row, ok := option.(map[string]any); ok {
			name, _ := row["model"].(string)
			labels[name], _ = row["label"].(string)
		}
	}
	return labels
}

func claudeTeardownPaths(data map[string]any) [][]string {
	if get(data, []string{"env", claudeMarker}).Data != "1" {
		return nil
	}
	paths := slices.Clone(claudePaths)
	// A subagent setting is only Baseten's while it names a configured route.
	subagent, _ := get(data, claudeSubagentPath).Data.(string)
	if _, ok := claudeRoutes(data)[subagent]; ok {
		paths = append(paths, claudeSubagentPath)
	}
	return paths
}

func (claudeCodeHarness) Credential(path string) (string, error) {
	_, data, err := readConfig(path)
	if err != nil || get(data, []string{"env", claudeMarker}).Data != "1" {
		return "", err
	}
	token, _ := get(data, claudeCredentialPath).Data.(string)
	return token, nil
}

func (claudeCodeHarness) Teardown(path string) ([]*Plan, error) {
	p, err := prepareTeardown(path, claudeTeardownPaths)
	if err != nil {
		return nil, err
	}
	return []*Plan{p}, nil
}

func (claudeCodeHarness) Inspect(d Detection) (Status, error) {
	r, data, err := inspectConfig(d)
	if err != nil {
		return r, err
	}
	r.managed(data, claudeTeardownPaths(data))
	if r.State == StateNotConfigured {
		return r, nil
	}
	r.BackgroundRoute, _ = get(data, []string{"env", "ANTHROPIC_DEFAULT_HAIKU_MODEL"}).Data.(string)
	r.addRoutes(claudeRoutes(data))
	return r, nil
}
