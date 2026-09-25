package harness

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

type codexHarness struct{}

// catalogPath is the model catalog Baseten owns next to the Codex settings.
func catalogPath(path string) string {
	return filepath.Join(filepath.Dir(path), "baseten-models.json")
}

func (codexHarness) Name() string { return Codex }

func (h codexHarness) Detect(ctx context.Context, execer Execer, dir string) (Detection, error) {
	dir, err := configDir(dir, "CODEX_HOME", ".codex")
	if err != nil {
		return Detection{}, err
	}
	path := filepath.Join(dir, "config.toml")
	d, err := detect(ctx, execer, h.Name(), "codex", path)
	if err != nil || d.Installed || runtime.GOOS != "darwin" {
		return d, err
	}
	// Desktop installs bundle Codex without adding it to PATH, and share its config.
	roots := []string{"/Applications"}
	if home, err := os.UserHomeDir(); err == nil {
		roots = append(roots, filepath.Join(home, "Applications"))
	}
	for _, root := range roots {
		for _, app := range []string{"ChatGPT.app", "Codex.app"} {
			binary := filepath.Join(root, app, "Contents", "Resources", "codex")
			d, err = detect(ctx, execer, h.Name(), binary, path)
			if err != nil || d.Installed {
				return d, err
			}
		}
	}
	return d, nil
}

func (codexHarness) BackgroundRoute(Selection) string { return "" }

// Codex's wire API is per provider, so routes without Responses support use a
// second provider over Chat Completions. Setup writes only the primary route's.
var codexProviders = []string{providerID, chatProviderID}

func codexCredentialPath(provider string) []string {
	return []string{"model_providers", provider, "experimental_bearer_token"}
}

func (codexHarness) Credential(path string) (string, error) {
	for _, provider := range codexProviders {
		if token, err := credential(path, codexCredentialPath(provider)); err != nil || token != "" {
			return token, err
		}
	}
	return "", nil
}

func (codexHarness) Prepare(path string, routes []Route, s Selection, endpoint string) ([]*Plan, error) {
	switch {
	case s.Background != "":
		return nil, errors.New("--background-route is supported only for Claude Code and OpenCode")
	case s.Subagent != "":
		return nil, errors.New("--subagent-route is supported only for Claude Code and OpenCode")
	case s.Fallback != "":
		return nil, errors.New("--fallback-route is supported only for Claude Code")
	}
	s, err := s.resolve(routes)
	if err != nil {
		return nil, err
	}
	// model_provider is global, so Codex can only switch among routes that
	// share the primary route's wire API.
	primary, _ := routeByName(routes, s.Primary)
	routes = slices.DeleteFunc(slices.Clone(routes), func(r Route) bool { return codexProvider(r) != codexProvider(primary) })
	values := []setting{
		desired([]string{"model"}, s.Primary),
		desired([]string{"model_provider"}, codexProvider(primary)),
		desired([]string{"model_catalog_json"}, catalogPath(path)),
		desired([]string{"review_model"}, s.Primary),
	}
	var credentials [][]string
	for _, provider := range codexProviders {
		key := []string{"model_providers", provider}
		if !slices.ContainsFunc(routes, func(r Route) bool { return codexProvider(r) == provider }) {
			values = append(values, setting{Path: key})
			continue
		}
		wire := map[string]string{providerID: "responses", chatProviderID: "chat"}[provider]
		values = append(values, desired(key, map[string]any{
			"name":                      "Baseten",
			"base_url":                  strings.TrimRight(endpoint, "/") + "/v1",
			"wire_api":                  wire,
			"requires_openai_auth":      false,
			"experimental_bearer_token": "",
		}))
		credentials = append(credentials, codexCredentialPath(provider))
	}
	p, err := prepareSettings(path, credentials, func(map[string]any) ([]setting, error) { return values, nil })
	if err != nil {
		return nil, err
	}
	models := []any{}
	for i, r := range routes {
		levels := []any{}
		for _, level := range r.ReasoningLevels {
			levels = append(levels, map[string]any{"effort": level, "description": level})
		}
		low, _ := reasoningBounds(r.ReasoningLevels)
		// Codex requires every field.
		models = append(models, map[string]any{
			"slug":                         r.Name,
			"display_name":                 r.DisplayName,
			"description":                  "Baseten route",
			"base_instructions":            "",
			"default_reasoning_level":      low,
			"supported_reasoning_levels":   levels,
			"shell_type":                   "shell_command",
			"visibility":                   "list",
			"supported_in_api":             true,
			"priority":                     i,
			"supports_reasoning_summaries": false,
			"support_verbosity":            false,
			"default_verbosity":            nil,
			"apply_patch_tool_type":        nil,
			"truncation_policy":            map[string]any{"mode": "tokens", "limit": 10000},
			"context_window":               r.ContextWindow,
			"input_modalities":             r.InputModalities,
			"supports_parallel_tool_calls": r.ParallelTools,
			"experimental_supported_tools": []any{},
		})
	}
	catalog, err := prepareSettings(catalogPath(path), nil, func(map[string]any) ([]setting, error) {
		return []setting{desired([]string{"models"}, models)}, nil
	})
	if err != nil {
		return nil, err
	}
	// The catalog precedes the settings that reference it.
	return []*Plan{catalog, p}, nil
}

func codexProvider(r Route) string {
	if r.Responses {
		return providerID
	}
	return chatProviderID
}

// codexSelected reports whether a Baseten provider is Codex's selected provider.
func codexSelected(data map[string]any) bool {
	provider, _ := data["model_provider"].(string)
	return slices.Contains(codexProviders, provider)
}

func codexTeardownPaths(data map[string]any, catalog string) [][]string {
	paths := [][]string{{"model_providers", providerID}, {"model_providers", chatProviderID}}
	// Only reset shared defaults while Baseten is the selected provider.
	if codexSelected(data) {
		paths = append(paths, []string{"model_provider"}, []string{"model"}, []string{"review_model"})
	}
	if data["model_catalog_json"] == catalog {
		paths = append(paths, []string{"model_catalog_json"})
	}
	return paths
}

func (codexHarness) Teardown(path string) ([]*Plan, error) {
	config, err := prepareTeardown(path, func(data map[string]any) [][]string {
		return codexTeardownPaths(data, catalogPath(path))
	})
	if err != nil {
		return nil, err
	}
	removal, err := prepareRemoval(catalogPath(path))
	if err != nil {
		return nil, err
	}
	return []*Plan{config, removal}, nil
}

func (codexHarness) Inspect(d Detection) (Status, error) {
	r, data, err := inspectConfig(d)
	if err != nil {
		return r, err
	}
	r.managed(data, codexTeardownPaths(data, catalogPath(d.Path)))
	f, models, err := readConfig(catalogPath(d.Path))
	switch {
	case err != nil:
		// Setup rewrites a malformed catalog and teardown deletes it.
		r.State = StateIncomplete
		return r, nil
	case r.State == StateNotConfigured:
		if f.exists {
			r.State = StateIncomplete
		}
		return r, nil
	case !f.exists || !slices.ContainsFunc(codexProviders, func(p string) bool { return get(data, []string{"model_providers", p}).Exists }):
		r.State = StateIncomplete
	case !codexSelected(data):
		r.State = StateInactive
	}
	labels := map[string]string{}
	entries, _ := models["models"].([]any)
	for _, entry := range entries {
		if row, ok := entry.(map[string]any); ok {
			name, _ := row["slug"].(string)
			labels[name], _ = row["display_name"].(string)
		}
	}
	r.addRoutes(labels)
	return r, nil
}
