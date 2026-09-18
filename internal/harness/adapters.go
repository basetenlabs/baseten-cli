package harness

import (
	_ "embed"
	"errors"
	"fmt"
	"github.com/basetenlabs/baseten-cli/internal/safefile"
	"os"
	"path/filepath"
	"strings"
)

// The native fallback prompt captured from an isolated Codex 0.134.0 request.
// Keep version-pinned: updating Codex requires recapturing and verifying it.
//
//go:embed templates/codex-0.134.0-prompt.txt
var codexNativeInstructions string

const providerID = "baseten-harness"

func CatalogPath(path string) string {
	if snapshot, err := safefile.ReadSnapshot(path); err == nil {
		path = snapshot.Target
	}
	return path + ".baseten-models.json"
}

// PrepareHarness plans all files before any are written. Catalogs are installed
// before configs that refer to them; teardown reverses that dependency order.
func PrepareHarness(name, path string, routes []Route, mcpServers []MCPServer, s Selection, endpoint, token string, replacePicker, replaceExisting bool) ([]*Plan, error) {
	if name == "claude-code" {
		p, e := Prepare(path, routes, s, endpoint, token, replacePicker, replaceExisting)
		if e != nil {
			return nil, e
		}
		if len(mcpServers) == 0 {
			return []*Plan{p}, nil
		}
		mcpPath, e := claudeMCPPath()
		if e != nil {
			return nil, e
		}
		mcp, e := prepareSettings(mcpPath, routes, replaceExisting, func(map[string]any, *Journal) ([]Setting, error) {
			var values []Setting
			for _, server := range mcpServers {
				values = append(values, desired([]string{"mcpServers", server.Name, "type"}, "http"), desired([]string{"mcpServers", server.Name, "url"}, server.URL))
				if server.AuthorizationToken != "" {
					values = append(values, desired([]string{"mcpServers", server.Name, "headers", "Authorization"}, "Bearer "+server.AuthorizationToken))
				}
			}
			return values, nil
		})
		if e != nil {
			return nil, e
		}
		return []*Plan{p, mcp}, nil
	}
	if _, e := ValidateCatalog(routes); e != nil {
		return nil, e
	}
	if s.Fallback != "" {
		return nil, errors.New("--fallback-model is currently supported only for Claude Code")
	}
	explicitSubagent := s.Subagent != ""
	var err error
	s, err = s.Resolve(routes)
	if err != nil {
		return nil, err
	}
	if replacePicker {
		return nil, errors.New("--replace-picker is Claude-specific; Codex catalogs replace built-ins and OpenCode adds a provider")
	}
	if name == "codex" && filepath.Ext(path) != ".toml" {
		return nil, errors.New("Codex --config must name a .toml file")
	}
	if name == "opencode" && filepath.Ext(path) != ".json" {
		return nil, errors.New("OpenCode currently supports strict .json configs; JSONC requires a separate adapter")
	}
	var values []Setting
	switch name {
	case "opencode":
		models := map[string]any{}
		for _, r := range routes {
			if !r.ChatCompletions || r.ContextWindow <= 0 || r.OutputLimit <= 0 || r.OutputLimit >= r.ContextWindow {
				return nil, fmt.Errorf("Route %q requires Chat Completions and explicit context/output limits", r.Name)
			}
			if len(r.InputModalities) == 0 {
				return nil, fmt.Errorf("Route %q requires explicit input modalities", r.Name)
			}
			variants := map[string]any{}
			for _, level := range r.ReasoningLevels {
				variants[level] = map[string]any{"reasoningEffort": level}
			}
			models[r.Name] = map[string]any{"name": r.DisplayName, "limit": map[string]any{"context": r.ContextWindow, "output": r.OutputLimit}, "tool_call": r.Tools, "modalities": map[string]any{"input": r.InputModalities, "output": []string{"text"}}, "reasoning": len(r.ReasoningLevels) > 0, "variants": variants}
		}
		values = []Setting{desired([]string{"model"}, providerID+"/"+s.Primary), desired([]string{"small_model"}, providerID+"/"+s.Background), desired([]string{"provider", providerID}, map[string]any{"npm": "@ai-sdk/openai-compatible", "name": "Baseten harness", "options": map[string]any{"baseURL": strings.TrimRight(endpoint, "/") + "/v1", "apiKey": token}, "models": models})}
		if explicitSubagent {
			for _, role := range []string{"general", "explore"} {
				values = append(values, desired([]string{"agent", role, "model"}, providerID+"/"+s.Subagent))
			}
		}
		for _, server := range mcpServers {
			values = append(values, desired([]string{"mcp", server.Name, "type"}, "remote"), desired([]string{"mcp", server.Name, "url"}, server.URL))
			if server.AuthorizationToken != "" {
				values = append(values, desired([]string{"mcp", server.Name, "headers", "Authorization"}, "Bearer "+server.AuthorizationToken))
			}
		}
	case "codex":
		values = []Setting{desired([]string{"model"}, s.Primary), desired([]string{"model_provider"}, providerID), desired([]string{"model_catalog_json"}, CatalogPath(path)), desired([]string{"review_model"}, s.Subagent), desired([]string{"memories", "extract_model"}, s.Background), desired([]string{"memories", "consolidation_model"}, s.Background), desired([]string{"model_providers", providerID}, map[string]any{"name": "Baseten harness", "base_url": strings.TrimRight(endpoint, "/") + "/v1", "wire_api": "responses", "requires_openai_auth": false, "experimental_bearer_token": token})}
		// Subagents inherit the primary model unless the user configures a role.
		// Preserve explicit user roles instead of claiming a universal override.
		if s.Subagent != s.Primary {
			return nil, errors.New("Codex subagents inherit the primary Route; separate --subagent-model requires a verified role-file adapter")
		}
		for _, server := range mcpServers {
			values = append(values, desired([]string{"mcp_servers", server.Name, "url"}, server.URL))
			if server.AuthorizationToken != "" {
				values = append(values, desired([]string{"mcp_servers", server.Name, "http_headers", "Authorization"}, "Bearer "+server.AuthorizationToken))
			}
		}
	default:
		return nil, fmt.Errorf("unknown harness %s", name)
	}
	p, err := prepareSettings(path, routes, replaceExisting, func(data map[string]any, j *Journal) ([]Setting, error) {
		if name == "codex" {
			if _, ok := data["profile"]; ok {
				return nil, errors.New("resolve the active Codex profile before setup")
			}

		}
		if name == "opencode" {
			if _, ok := data["providers"]; ok {
				return nil, errors.New("OpenCode V2 providers require a separately verified adapter")
			}
		}
		for _, policy := range policyPaths(name, path) {
			if _, e := os.Stat(policy); e == nil {
				return nil, fmt.Errorf("managed policy detected at %s", policy)
			} else if !os.IsNotExist(e) {
				return nil, e
			}
		}
		return values, nil
	})
	if err != nil {
		return nil, err
	}
	if name == "opencode" {
		return []*Plan{p}, nil
	}
	models := []any{}
	for i, r := range routes {
		if !r.Responses || r.ContextWindow <= 0 || len(r.InputModalities) == 0 {
			return nil, fmt.Errorf("Route %q requires Responses, a context limit and explicit input modalities", r.Name)
		}
		levels := []any{}
		var defaultLevel any
		for _, level := range r.ReasoningLevels {
			levels = append(levels, map[string]any{"effort": level, "description": level})
		}
		if len(r.ReasoningLevels) > 0 {
			defaultLevel = r.ReasoningLevels[0]
		}
		models = append(models, map[string]any{"slug": r.Name, "display_name": r.DisplayName, "description": "Baseten Route", "base_instructions": codexNativeInstructions, "default_reasoning_level": defaultLevel, "supported_reasoning_levels": levels, "shell_type": "shell_command", "visibility": "list", "supported_in_api": true, "priority": i, "supports_reasoning_summaries": false, "support_verbosity": false, "default_verbosity": nil, "apply_patch_tool_type": nil, "truncation_policy": map[string]any{"mode": "tokens", "limit": 10000}, "context_window": r.ContextWindow, "input_modalities": r.InputModalities, "supports_parallel_tool_calls": r.ParallelTools, "experimental_supported_tools": []any{}})
	}
	catalog, err := prepareSettings(CatalogPath(path), routes, replaceExisting, func(map[string]any, *Journal) ([]Setting, error) {
		return []Setting{desired([]string{"models"}, models)}, nil
	})
	if err != nil {
		return nil, err
	}
	return []*Plan{catalog, p}, nil
}
func policyPaths(name, path string) []string {
	if name == "codex" {
		return []string{filepath.Join(filepath.Dir(path), "managed_config.toml"), "/etc/codex/managed_config.toml", "/etc/codex/requirements.toml"}
	}
	return []string{filepath.Join(filepath.Dir(path), "opencode.jsonc"), "/etc/opencode/opencode.json", "/etc/opencode/opencode.jsonc"}
}
func PrepareHarnessTeardown(name, path string) ([]*Plan, error) {
	p, e := PrepareTeardown(path)
	if e != nil {
		return nil, e
	}
	plans := []*Plan{p}
	if name == "claude-code" {
		mcpPath, e := claudeMCPPath()
		if e != nil {
			return nil, e
		}
		mcp, e := PrepareTeardown(mcpPath)
		if e != nil {
			return nil, e
		}
		plans = []*Plan{mcp, p}
	}
	if name == "codex" {
		catalog, e := PrepareTeardown(CatalogPath(path))
		if e != nil {
			return nil, e
		}
		if len(p.Conflicts) > 0 {
			return nil, errors.New("Codex settings have user edits; resolve them before removing its managed catalog")
		}
		plans = append(plans, catalog)
	}
	return plans, nil
}
func CheckPlans(plans []*Plan) error {
	for _, p := range plans {
		if err := p.snapshot.Check(); err != nil {
			return err
		}
		if err := p.journalSnapshot.Check(); err != nil {
			return err
		}
	}
	return nil
}

func ApplyPlans(plans []*Plan) error {
	if err := CheckPlans(plans); err != nil {
		return err
	}
	for _, p := range plans {
		if err := p.Apply(); err != nil {
			return err
		}
	}
	return nil
}
