package harness

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
)

type claudeCodeHarness struct{}

// claudeMarker identifies Baseten's settings, since Claude Code has no named
// provider block to look for.
const claudeMarker = "BASETEN_HARNESS"

var claudeSubagentPath = []string{"env", "CLAUDE_CODE_SUBAGENT_MODEL"}

var claudePaths = [][]string{
	{"model"}, {"fallbackModel"}, {"modelPicker", "options"},
	{"modelPicker", "replaceBuiltInOptions"}, {"availableModels"},
	{"env", claudeMarker}, {"env", "ANTHROPIC_BASE_URL"},
	{"env", "ANTHROPIC_AUTH_TOKEN"}, {"env", "ANTHROPIC_DEFAULT_SONNET_MODEL"},
	{"env", "ANTHROPIC_DEFAULT_OPUS_MODEL"}, {"env", "ANTHROPIC_DEFAULT_FABLE_MODEL"},
	{"env", "ANTHROPIC_DEFAULT_HAIKU_MODEL"}, {"env", "ANTHROPIC_SMALL_FAST_MODEL"},
	{"env", "CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY"},
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

func (h claudeCodeHarness) Prepare(path string, routes []Route, s Selection, endpoint, token string) ([]*Plan, error) {
	if err := checkClaudePolicy(path); err != nil {
		return nil, err
	}
	p, err := prepareSettings(path, func(data map[string]any) ([]setting, error) {
		return claudeSettings(routes, s, endpoint, token, data)
	})
	if err != nil {
		return nil, err
	}
	p.credentialPath = []string{"env", "ANTHROPIC_AUTH_TOKEN"}
	return []*Plan{p}, nil
}

func claudeSettings(routes []Route, selection Selection, endpoint, token string, current map[string]any) ([]setting, error) {
	for _, route := range routes {
		switch route.Name {
		case "default", "inherit", "opus", "sonnet", "haiku", "fable", "opusplan", "best":
			return nil, fmt.Errorf("route %q conflicts with a Claude model keyword", route.Name)
		}
	}
	s, err := selection.resolve(routes)
	if err != nil {
		return nil, err
	}
	for _, key := range []string{"apiKeyHelper", "modelOverrides"} {
		if _, ok := current[key]; ok {
			return nil, fmt.Errorf("existing %s must be removed before setup", key)
		}
	}
	for _, key := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_MODEL", "CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY", "CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST"} {
		if get(current, []string{"env", key}).Exists || os.Getenv(key) != "" {
			return nil, fmt.Errorf("%s overrides the harness configuration; remove it before setup", key)
		}
	}
	background := cmp.Or(s.Background, defaultBackgroundRoute)
	options := []any{}
	allowed := []any{}
	for _, r := range routes {
		options = append(options, map[string]any{"model": r.Name, "label": r.DisplayName})
		allowed = append(allowed, r.Name)
	}
	if !slices.Contains(allowed, any(background)) {
		allowed = append(allowed, background)
	}
	values := []setting{
		desired([]string{"model"}, s.Primary),
		desired([]string{"fallbackModel"}, []string{s.Fallback}),
		desired([]string{"modelPicker", "options"}, options),
		desired([]string{"modelPicker", "replaceBuiltInOptions"}, true),
		desired([]string{"availableModels"}, allowed),
	}
	for _, kv := range [][2]string{
		{claudeMarker, "1"},
		{"ANTHROPIC_BASE_URL", endpoint},
		{"ANTHROPIC_AUTH_TOKEN", token},
		{"ANTHROPIC_DEFAULT_SONNET_MODEL", s.Primary},
		{"ANTHROPIC_DEFAULT_OPUS_MODEL", s.Primary},
		{"ANTHROPIC_DEFAULT_FABLE_MODEL", s.Primary},
		{"ANTHROPIC_DEFAULT_HAIKU_MODEL", background},
		{"ANTHROPIC_SMALL_FAST_MODEL", background},
		{"CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY", "0"},
	} {
		values = append(values, desired([]string{"env", kv[0]}, kv[1]))
	}
	if selection.Subagent != "" {
		values = append(values, desired(claudeSubagentPath, s.Subagent))
	} else if get(current, []string{"env", claudeMarker}).Data == "1" {
		// A refresh may retire the route an earlier --subagent-route selected.
		previous, _ := get(current, claudeSubagentPath).Data.(string)
		if _, owned := claudeRoutes(current)[previous]; owned && !slices.Contains(allowed, any(previous)) {
			values = append(values, setting{Path: claudeSubagentPath})
		}
	}
	return values, nil
}

// checkClaudePolicy refuses setup when an administrator manages Claude Code.
func checkClaudePolicy(path string) error {
	for _, p := range []string{filepath.Join(filepath.Dir(path), "managed-settings.json"), "/Library/Application Support/ClaudeCode/managed-settings.json", "/etc/claude-code/managed-settings.json"} {
		if _, err := os.Stat(p); err == nil {
			return fmt.Errorf("managed policy detected at %s; ask your administrator to configure the harness", p)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
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
