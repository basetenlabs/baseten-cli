package harness

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

type claudeHarness struct{ baseHarness }

func (h claudeHarness) Prepare(path string, routes []Route, s Selection, endpoint, token string) ([]*Plan, error) {
	if err := checkClaudePolicy(path); err != nil {
		return nil, err
	}
	p, err := prepareSettings(path, routes, func(data map[string]any) ([]setting, error) {
		return claudeSettings(routes, s, endpoint, token, data)
	})
	if err != nil {
		return nil, err
	}
	p.credentialPath = []string{"env", "ANTHROPIC_AUTH_TOKEN"}
	return []*Plan{p}, nil
}

func (h claudeHarness) SmallTaskModel(s Selection) string { return defaultSmallTaskModel }

func claudeSettings(routes []Route, selection Selection, endpoint, token string, current map[string]any) ([]setting, error) {
	for _, route := range routes {
		switch route.Name {
		case "default", "inherit", "opus", "sonnet", "haiku", "fable", "opusplan", "best":
			return nil, fmt.Errorf("route %q conflicts with a Claude model keyword", route.Name)
		}
	}
	if selection.Background != "" {
		return nil, errors.New("--background-route is supported only for OpenCode")
	}

	explicitSubagent := selection.Subagent != ""
	s, err := selection.resolve(routes)
	if err != nil {
		return nil, err
	}
	if token == "" || strings.ContainsAny(token, "\r\n\t ") {
		return nil, errors.New("missing or invalid harness credential")
	}
	for _, key := range []string{"apiKeyHelper", "modelOverrides"} {
		if _, ok := current[key]; ok {
			return nil, fmt.Errorf("existing %s must be resolved before setup", key)
		}
	}
	blocked := []string{"ANTHROPIC_API_KEY", "ANTHROPIC_MODEL", "CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY", "CLAUDE_CODE_PROVIDER_MANAGED_BY_HOST"}
	for _, key := range blocked {
		if get(current, []string{"env", key}).Exists || os.Getenv(key) != "" {
			return nil, fmt.Errorf("%s overrides harness configuration; resolve it before setup", key)
		}
	}
	options := []any{}
	allowed := []any{}
	for _, r := range routes {
		options = append(options, map[string]any{"model": r.Name, "label": r.DisplayName})
		allowed = append(allowed, r.Name)
	}
	if !slices.Contains(allowed, any(defaultSmallTaskModel)) {
		allowed = append(allowed, defaultSmallTaskModel)
	}
	values := []setting{
		desired([]string{"model"}, s.Primary),
		desired([]string{"fallbackModel"}, []string{s.Fallback}),
		desired([]string{"modelPicker", "options"}, options),
		desired([]string{"modelPicker", "replaceBuiltInOptions"}, true),
		desired([]string{"availableModels"}, allowed),
	}
	for _, kv := range [][2]string{
		{"ANTHROPIC_BASE_URL", endpoint},
		{"ANTHROPIC_AUTH_TOKEN", token},
		{"ANTHROPIC_DEFAULT_SONNET_MODEL", s.Primary},
		{"ANTHROPIC_DEFAULT_OPUS_MODEL", s.Primary},
		{"ANTHROPIC_DEFAULT_FABLE_MODEL", s.Primary},
		{"ANTHROPIC_DEFAULT_HAIKU_MODEL", defaultSmallTaskModel},
		{"ANTHROPIC_SMALL_FAST_MODEL", defaultSmallTaskModel},
		{"CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY", "0"},
	} {
		values = append(values, desired([]string{"env", kv[0]}, kv[1]))
	}
	if explicitSubagent {
		values = append(values, desired([]string{"env", "CLAUDE_CODE_SUBAGENT_MODEL"}, s.Subagent))
	}
	return values, nil
}

func checkClaudePolicy(path string) error {
	for _, p := range []string{filepath.Join(filepath.Dir(path), "managed-settings.json"), "/Library/Application Support/ClaudeCode/managed-settings.json", "/etc/claude-code/managed-settings.json"} {
		if _, err := os.Stat(p); err == nil {
			return fmt.Errorf("managed policy detected at %s; ask your administrator to configure the harness", p)
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func (h claudeHarness) Inspect(d Detection) (Status, error) {
	r, data, j, err := inspectConfig(d)
	if err != nil {
		return r, err
	}
	r.SmallTaskModel, _ = get(data, []string{"env", "ANTHROPIC_DEFAULT_HAIKU_MODEL"}).Data.(string)
	labels := map[string]string{}
	if options, ok := get(data, []string{"modelPicker", "options"}).Data.([]any); ok {
		for _, option := range options {
			if row, ok := option.(map[string]any); ok {
				name, _ := row["model"].(string)
				label, _ := row["label"].(string)
				labels[name] = label
			}
		}
	}
	r.addRoutes(j, labels)
	return r, nil
}
