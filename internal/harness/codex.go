package harness

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type codexHarness struct{ baseHarness }

func catalogPath(path string) string { return path + ".baseten-models.json" }

func (h codexHarness) Prepare(path string, routes []Route, s Selection, endpoint, token string) ([]*Plan, error) {
	if s.Subagent != "" {
		return nil, errors.New("--subagent-route is supported only for Claude Code and OpenCode")
	}
	if s.Background != "" {
		return nil, errors.New("--background-route is supported only for OpenCode")
	}

	if len(routes) == 0 {
		return nil, errors.New("no accessible routes")
	}
	if s.Fallback != "" {
		return nil, errors.New("--fallback-route is supported only for Claude Code")
	}
	var err error
	s, err = s.resolve(routes)
	if err != nil {
		return nil, err
	}

	if filepath.Ext(path) != ".toml" {
		return nil, errors.New("Codex --config must name a .toml file")
	}
	// Use the resolved config target so symlink aliases share one catalog.
	file, err := readFile(path)
	if err != nil {
		return nil, err
	}
	values := []setting{
		desired([]string{"model"}, s.Primary),
		desired([]string{"model_provider"}, providerID),
		desired([]string{"model_catalog_json"}, catalogPath(file.Target)),
		desired([]string{"review_model"}, s.Subagent),
		desired([]string{"model_providers", providerID}, map[string]any{
			"name":                      "Baseten",
			"base_url":                  strings.TrimRight(endpoint, "/") + "/v1",
			"wire_api":                  "responses",
			"requires_openai_auth":      false,
			"experimental_bearer_token": token,
		}),
	}

	p, err := prepareSettings(path, func(data map[string]any) ([]setting, error) {
		if _, ok := data["profile"]; ok {
			return nil, errors.New("resolve the active Codex profile before setup")
		}
		for _, policy := range []string{filepath.Join(filepath.Dir(path), "managed_config.toml"), "/etc/codex/managed_config.toml", "/etc/codex/requirements.toml"} {
			if _, err := os.Stat(policy); err == nil {
				return nil, fmt.Errorf("managed policy detected at %s", policy)
			} else if !os.IsNotExist(err) {
				return nil, err
			}
		}
		return values, nil
	})
	if err != nil {
		return nil, err
	}
	p.credentialPath = []string{"model_providers", providerID, "experimental_bearer_token"}
	models := []any{}
	for i, r := range routes {
		// Required Codex catalog fields use conservative defaults. Model-specific
		// capabilities and limits are intentionally left to a follow-up.
		models = append(models, map[string]any{
			"slug":         r.Name,
			"display_name": r.DisplayName,
			"description":  "Baseten route",
			// Required by Codex; leave empty until instruction handling is resolved.
			"base_instructions":            "",
			"default_reasoning_level":      nil,
			"supported_reasoning_levels":   []any{},
			"shell_type":                   "shell_command",
			"visibility":                   "list",
			"supported_in_api":             true,
			"priority":                     i,
			"supports_reasoning_summaries": false,
			"support_verbosity":            false,
			"default_verbosity":            nil,
			"apply_patch_tool_type":        nil,
			"truncation_policy": map[string]any{
				"mode":  "tokens",
				"limit": 10000,
			},
			"supports_parallel_tool_calls": false,
			"experimental_supported_tools": []any{},
		})
	}
	catalog, err := prepareSettings(catalogPath(file.Target), func(map[string]any) ([]setting, error) {
		return []setting{desired([]string{"models"}, models)}, nil
	})
	if err != nil {
		return nil, err
	}
	return []*Plan{catalog, p}, nil
}

func codexTeardownPaths(data map[string]any, catalog string) [][]string {
	paths := [][]string{{"model_providers", providerID}}
	// Only reset shared defaults while our provider is selected. If the user
	// has switched providers, leave their new model selection alone.
	if data["model_provider"] == providerID {
		paths = append(paths, []string{"model_provider"}, []string{"model"}, []string{"review_model"})
	}
	if data["model_catalog_json"] == catalog {
		paths = append(paths, []string{"model_catalog_json"})
	}
	return paths
}

func (h codexHarness) Teardown(path string) ([]*Plan, error) {
	file, err := readFile(path)
	if err != nil {
		return nil, err
	}
	catalog := catalogPath(file.Target)
	config, err := prepareTeardown(path, func(data map[string]any) [][]string {
		return codexTeardownPaths(data, catalog)
	})
	if err != nil {
		return nil, err
	}
	removal, err := prepareRemoval(catalog)
	if err != nil {
		return nil, err
	}
	return []*Plan{config, removal}, nil
}

func (h codexHarness) Inspect(d Detection) (Status, error) {
	r, data, err := inspectConfig(d)
	if err != nil {
		return r, err
	}
	file, err := readFile(d.Path)
	if err != nil {
		return r, err
	}
	catalog := catalogPath(file.Target)
	r.managed(data, codexTeardownPaths(data, catalog))
	catalogFile, err := readFile(catalog)
	if err != nil {
		return r, err
	}
	if r.State == "not-configured" {
		if catalogFile.Info != nil {
			r.State = "incomplete"
			r.Note = "Orphaned Baseten model catalog; rerun setup or teardown."
		}
		return r, nil
	}
	if data["model_provider"] != providerID {
		r.State = "inactive"
	}
	if !get(data, []string{"model_providers", providerID}).Exists || catalogFile.Info == nil {
		r.State = "incomplete"
	}
	// A missing or malformed generated catalog can be repaired by setup or
	// removed by teardown without requiring restoration metadata.
	models, err := decode(catalogFile.Data)
	if err != nil {
		r.State = "incomplete"
		return r, nil
	}
	labels := map[string]string{}
	if entries, ok := models["models"].([]any); ok {
		for _, entry := range entries {
			if row, ok := entry.(map[string]any); ok {
				name, _ := row["slug"].(string)
				labels[name], _ = row["display_name"].(string)
			}
		}
	}
	r.addRoutes(labels)
	return r, nil
}
