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

	p, err := prepareSettings(path, routes, func(data map[string]any) ([]setting, error) {
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
	catalog, err := prepareSettings(catalogPath(file.Target), routes, func(map[string]any) ([]setting, error) {
		return []setting{desired([]string{"models"}, models)}, nil
	})
	if err != nil {
		return nil, err
	}
	return []*Plan{catalog, p}, nil
}

func (h codexHarness) Teardown(path string) ([]*Plan, error) {
	file, err := readFile(path)
	if err != nil {
		return nil, err
	}
	config, err := prepareTeardown(path)
	if err != nil {
		return nil, err
	}
	catalog, err := prepareTeardown(catalogPath(file.Target))
	if err != nil {
		return nil, err
	}
	return []*Plan{config, catalog}, nil
}

func (h codexHarness) Inspect(d Detection) (Status, error) {
	r, _, j, err := inspectConfig(d)
	if err != nil {
		return r, err
	}
	file, err := readFile(d.Path)
	if err != nil {
		return r, err
	}
	_, data, _, catalog, err := readConfig(catalogPath(file.Target))
	if err != nil {
		return r, err
	}
	if j == nil {
		if catalog != nil {
			r.State = "interrupted"
			r.Drift = []string{"orphaned model catalog"}
		}
		return r, nil
	}
	if catalog == nil {
		r.State = "drifted"
		r.Drift = append(r.Drift, "model catalog missing")
		return r, nil
	}
	for _, v := range catalog.Settings {
		if !same(get(data, v.Path), v.Installed) {
			r.State = "drifted"
			r.Drift = append(r.Drift, "model catalog changed")
		}
	}
	labels := map[string]string{}
	if models, ok := data["models"].([]any); ok {
		for _, model := range models {
			if row, ok := model.(map[string]any); ok {
				name, _ := row["slug"].(string)
				label, _ := row["display_name"].(string)
				labels[name] = label
			}
		}
	}
	r.addRoutes(catalog, labels)
	return r, nil
}

func (h codexHarness) Configured(path string) (bool, error) {
	configured, err := h.baseHarness.Configured(path)
	if err != nil || configured {
		return configured, err
	}
	target, err := resolveConfigPath(path)
	if err != nil {
		return false, err
	}
	return h.baseHarness.Configured(catalogPath(target))
}
