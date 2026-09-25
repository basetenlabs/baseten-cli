package harness

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
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
	return detect(ctx, execer, h.Name(), "codex", filepath.Join(dir, "config.toml"))
}

func (codexHarness) BackgroundRoute(Selection) string { return "" }

func (codexHarness) Prepare(path string, routes []Route, mcpServers []MCPServer, s Selection, endpoint, token string) ([]*Plan, error) {
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
	responses, chat := false, false
	for _, r := range routes {
		if r.Responses {
			responses = true
		} else {
			chat = true
		}
	}
	provider := providerID
	if r, ok := routeByName(routes, s.Primary); ok && !r.Responses {
		provider = chatProviderID
	}
	values := []setting{
		desired([]string{"model"}, s.Primary),
		desired([]string{"model_provider"}, provider),
		desired([]string{"model_catalog_json"}, catalogPath(path)),
		desired([]string{"review_model"}, s.Primary),
	}
	var credentialPaths [][]string
	if responses {
		values = append(values, desired([]string{"model_providers", providerID}, map[string]any{
			"name":                      "Baseten",
			"base_url":                  strings.TrimRight(endpoint, "/") + "/v1",
			"wire_api":                  "responses",
			"requires_openai_auth":      false,
			"experimental_bearer_token": token,
		}))
		credentialPaths = append(credentialPaths, []string{"model_providers", providerID, "experimental_bearer_token"})
	}
	if chat {
		values = append(values, desired([]string{"model_providers", chatProviderID}, map[string]any{
			"name":                      "Baseten",
			"base_url":                  strings.TrimRight(endpoint, "/") + "/v1",
			"wire_api":                  "chat",
			"requires_openai_auth":      false,
			"experimental_bearer_token": token,
		}))
		credentialPaths = append(credentialPaths, []string{"model_providers", chatProviderID, "experimental_bearer_token"})
	}
	for _, server := range mcpServers {
		values = append(values, desired([]string{"mcp_servers", server.Name, "url"}, server.URL))
		if server.AuthorizationToken != "" {
			values = append(values, desired([]string{"mcp_servers", server.Name, "http_headers", "Authorization"}, "Bearer "+server.AuthorizationToken))
		}
	}
	p, err := prepareSettings(path, credentialPaths, token, func(map[string]any) ([]setting, error) { return values, nil })
	if err != nil {
		return nil, err
	}
	models := []any{}
	for i, r := range routes {
		if r.ContextWindow <= 0 || len(r.InputModalities) == 0 {
			return nil, fmt.Errorf("Route %q requires a context limit and explicit input modalities", r.Name)
		}
		levels := []any{}
		for _, level := range r.ReasoningLevels {
			levels = append(levels, map[string]any{"effort": level, "description": level})
		}
		models = append(models, map[string]any{
			"slug":                         r.Name,
			"display_name":                 r.DisplayName,
			"description":                  "Baseten route",
			"base_instructions":            "",
			"default_reasoning_level":      defaultReasoningLevel(r.ReasoningLevels),
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
	catalog, err := prepareSettings(catalogPath(path), nil, "", func(map[string]any) ([]setting, error) {
		return []setting{desired([]string{"models"}, models)}, nil
	})
	if err != nil {
		return nil, err
	}
	// The catalog precedes the settings that reference it.
	return []*Plan{catalog, p}, nil
}

func codexTeardownPaths(data map[string]any, catalog string) [][]string {
	paths := [][]string{{"model_providers", providerID}, {"model_providers", chatProviderID}}
	// Only reset shared defaults while Baseten is the selected provider.
	if data["model_provider"] == providerID || data["model_provider"] == chatProviderID {
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
	case !get(data, []string{"model_providers", providerID}).Exists && !get(data, []string{"model_providers", chatProviderID}).Exists || !f.exists:
		r.State = StateIncomplete
	case data["model_provider"] != providerID && data["model_provider"] != chatProviderID:
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
