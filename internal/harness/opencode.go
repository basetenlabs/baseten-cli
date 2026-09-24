package harness

import (
	"cmp"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

type openCodeHarness struct{}

var openCodeSubagentPaths = [][]string{{"agent", "general", "model"}, {"agent", "explore", "model"}}

var openCodeCredentialPath = []string{"provider", providerID, "options", "apiKey"}

func (openCodeHarness) Credential(path string) (string, error) {
	return credential(path, openCodeCredentialPath)
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
	return detect(ctx, execer, h.Name(), "opencode", path)
}

func (openCodeHarness) BackgroundRoute(s Selection) string {
	return cmp.Or(s.Background, defaultBackgroundRoute)
}

func (openCodeHarness) Prepare(path string, routes []Route, s Selection, endpoint string) ([]*Plan, error) {
	if s.Fallback != "" {
		return nil, errors.New("--fallback-route is supported only for Claude Code")
	}
	explicitSubagent := s.Subagent != ""
	s, err := s.resolve(routes)
	if err != nil {
		return nil, err
	}
	models := map[string]any{}
	if s.Background == "" {
		s.Background = defaultBackgroundRoute
		models[defaultBackgroundRoute] = map[string]any{"name": "DeepSeek V4.1 Flash"}
	}
	for _, r := range routes {
		models[r.Name] = map[string]any{"name": r.DisplayName}
	}
	values := []setting{
		desired([]string{"model"}, providerID+"/"+s.Primary),
		desired([]string{"small_model"}, providerID+"/"+s.Background),
		desired([]string{"provider", providerID}, map[string]any{
			"npm":  "@ai-sdk/openai-compatible",
			"name": "Baseten",
			"options": map[string]any{
				"baseURL": strings.TrimRight(endpoint, "/") + "/v1",
				"apiKey":  "",
			},
			"models": models,
		}),
	}
	if explicitSubagent {
		for _, path := range openCodeSubagentPaths {
			values = append(values, desired(path, providerID+"/"+s.Subagent))
		}
	}
	p, err := prepareSettings(path, openCodeCredentialPath, func(current map[string]any) ([]setting, error) {
		if explicitSubagent {
			return values, nil
		}
		// A refresh may retire the route an earlier --subagent-route selected.
		for _, key := range openCodeSubagentPaths {
			model, _ := get(current, key).Data.(string)
			route, ours := strings.CutPrefix(model, providerID+"/")
			if ours && !slices.ContainsFunc(routes, func(r Route) bool { return r.Name == route }) {
				values = append(values, setting{Path: key})
			}
		}
		return values, nil
	})
	if err != nil {
		return nil, err
	}
	return []*Plan{p}, nil
}

// openCodeTeardownPaths clears only references to Baseten's provider; a model
// the user pointed at another provider is theirs.
func openCodeTeardownPaths(data map[string]any) [][]string {
	paths := [][]string{{"provider", providerID}}
	for _, key := range append([][]string{{"model"}, {"small_model"}}, openCodeSubagentPaths...) {
		if model, _ := get(data, key).Data.(string); strings.HasPrefix(model, providerID+"/") {
			paths = append(paths, key)
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
	if !get(data, []string{"provider", providerID}).Exists {
		r.State = StateIncomplete
	} else if !strings.HasPrefix(r.DefaultRoute, providerID+"/") {
		r.State = StateInactive
	}
	r.DefaultRoute = strings.TrimPrefix(r.DefaultRoute, providerID+"/")
	small, _ := data["small_model"].(string)
	r.BackgroundRoute = strings.TrimPrefix(small, providerID+"/")
	labels := map[string]string{}
	models, _ := get(data, []string{"provider", providerID, "models"}).Data.(map[string]any)
	for name, model := range models {
		// The default background model is not a route, so it is not listed.
		if row, ok := model.(map[string]any); ok && name != defaultBackgroundRoute {
			labels[name], _ = row["name"].(string)
		}
	}
	r.addRoutes(labels)
	return r, nil
}
