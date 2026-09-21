package harness

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/basetenlabs/baseten-cli/internal/safefile"
)

const providerID = "baseten-harness"

func CatalogPath(path string) string {
	if snapshot, err := safefile.ReadSnapshot(path); err == nil {
		path = snapshot.Target
	}
	return path + ".baseten-models.json"
}

// PrepareHarness plans all files before any are written. Catalogs are installed
// before configs that refer to them; teardown reverses that dependency order.
func PrepareHarness(name, path string, routes []Route, s Selection, endpoint, token string, replacePicker, replaceExisting bool) ([]*Plan, error) {
	if name == "claude-code" {
		p, e := Prepare(path, routes, s, endpoint, token, replacePicker, replaceExisting)
		return []*Plan{p}, e
	}
	if _, e := ValidateCatalog(routes); e != nil {
		return nil, e
	}
	if s.Fallback != "" {
		return nil, errors.New("--fallback-model is currently supported only for Claude Code")
	}
	explicitSubagent := s.Subagent != ""
	defaultOpenCodeBackground := name == "opencode" && s.Background == ""
	var err error
	s, err = s.Resolve(routes)
	if err != nil {
		return nil, err
	}
	if replacePicker {
		return nil, errors.New("picker replacement is Claude-specific; Codex catalogs replace built-ins and OpenCode adds a provider")
	}
	if name == "codex" && filepath.Ext(path) != ".toml" {
		return nil, errors.New("Codex --config must name a .toml file")
	}
	if name == "opencode" && filepath.Ext(path) != ".json" && filepath.Ext(path) != ".jsonc" {
		return nil, errors.New("OpenCode --config must name a .json or .jsonc file")
	}
	var values []Setting
	switch name {
	case "opencode":
		models := map[string]any{}
		if defaultOpenCodeBackground {
			s.Background = defaultSmallTaskModel
			models[defaultSmallTaskModel] = map[string]any{"name": "DeepSeek V4.1 Flash"}
		}
		for _, r := range routes {
			models[r.Name] = map[string]any{"name": r.DisplayName}
		}
		values = []Setting{
			desired([]string{"model"}, providerID+"/"+s.Primary),
			desired([]string{"small_model"}, providerID+"/"+s.Background),
			desired([]string{"provider", providerID}, map[string]any{
				"npm":  "@ai-sdk/openai-compatible",
				"name": "Baseten",
				"options": map[string]any{
					"baseURL": strings.TrimRight(endpoint, "/") + "/v1",
					"apiKey":  token,
				},
				"models": models,
			}),
		}
		if explicitSubagent {
			for _, role := range []string{"general", "explore"} {
				values = append(values, desired([]string{"agent", role, "model"}, providerID+"/"+s.Subagent))
			}
		}
	case "codex":
		values = []Setting{
			desired([]string{"model"}, s.Primary),
			desired([]string{"model_provider"}, providerID),
			desired([]string{"model_catalog_json"}, CatalogPath(path)),
			desired([]string{"review_model"}, s.Subagent),
			desired([]string{"memories", "extract_model"}, s.Background),
			desired([]string{"memories", "consolidation_model"}, s.Background),
			desired([]string{"model_providers", providerID}, map[string]any{
				"name":                      "Baseten",
				"base_url":                  strings.TrimRight(endpoint, "/") + "/v1",
				"wire_api":                  "responses",
				"requires_openai_auth":      false,
				"experimental_bearer_token": token,
			}),
		}
		// Subagents inherit the primary model unless the user configures a role.
		// Preserve explicit user roles instead of claiming a universal override.
		if s.Subagent != s.Primary {
			return nil, errors.New("Codex subagents inherit the primary route; separate --subagent-model requires a verified role-file adapter")
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
		policies, err := policyPaths(name, path)
		if err != nil {
			return nil, err
		}
		for _, policy := range policies {
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
	catalog, err := prepareSettings(CatalogPath(path), routes, replaceExisting, func(map[string]any, *Journal) ([]Setting, error) {
		return []Setting{desired([]string{"models"}, models)}, nil
	})
	if err != nil {
		return nil, err
	}
	return []*Plan{catalog, p}, nil
}
func policyPaths(name, path string) ([]string, error) {
	if name == "codex" {
		return []string{filepath.Join(filepath.Dir(path), "managed_config.toml"), "/etc/codex/managed_config.toml", "/etc/codex/requirements.toml"}, nil
	}
	username := ""
	if runtime.GOOS == "darwin" {
		current, err := user.Current()
		if err != nil {
			return nil, fmt.Errorf("checking OpenCode managed preferences: %w", err)
		}
		username = current.Username
	}
	return openCodePolicyPaths(runtime.GOOS, username), nil
}

func openCodePolicyPaths(platform, username string) []string {
	dir := "/etc/opencode"
	var paths []string
	if platform == "darwin" {
		dir = "/Library/Application Support/opencode"
		paths = append(paths,
			filepath.Join("/Library/Managed Preferences", username, "ai.opencode.managed.plist"),
			"/Library/Managed Preferences/ai.opencode.managed.plist",
		)
	} else if platform == "windows" {
		root := os.Getenv("ProgramData")
		if root == "" {
			root = `C:\ProgramData`
		}
		dir = filepath.Join(root, "opencode")
	}
	return append(paths, filepath.Join(dir, "opencode.json"), filepath.Join(dir, "opencode.jsonc"))
}
func PrepareHarnessTeardown(name, path string) ([]*Plan, error) {
	p, e := PrepareTeardown(path)
	if e != nil {
		return nil, e
	}
	plans := []*Plan{p}
	if name == "codex" {
		catalog, e := PrepareTeardown(CatalogPath(path))
		if e != nil {
			return nil, e
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
