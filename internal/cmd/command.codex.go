package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-cli/internal/codex"
)

func init() {
	Register("codex setup", commandCodexSetup)
}

// codexMAPIProvider is the config.toml provider id for Model APIs. Grid uses
// "baseten", so both can be configured side by side.
const codexMAPIProvider = "baseten-mapi"

const (
	codexMarketplaceHeader = "OpenAI-Marketplace-Id"
	codexMarketplaceKey    = "model_providers." + codexMAPIProvider + ".http_headers." + codexMarketplaceHeader
)

// inferenceModel is one entry of the inference endpoint's GET /v1/models.
type inferenceModel struct {
	ID                  string   `json:"id"`
	Name                string   `json:"name"`
	Description         string   `json:"description"`
	ContextLength       int      `json:"context_length"`
	MaxCompletionTokens int      `json:"max_completion_tokens"`
	SupportedFeatures   []string `json:"supported_features"`
	InputModalities     []string `json:"input_modalities"`
}

func commandCodexSetup(ctx *CommandContext, flags *cmd.CodexSetupFlags) error {
	key := os.Getenv("BASETEN_API_KEY")
	if key == "" {
		return cmd.NewErrUsagef("BASETEN_API_KEY is not set. Create an API key with the \"Call Model APIs\" permission in your Baseten organization settings, " +
			"then run `export BASETEN_API_KEY=<key>` and rerun this command. Codex reads the key from the same variable")
	}
	remote, err := ctx.authInfo.Remote()
	if err != nil {
		return err
	}
	baseURL := remote.ModelAPIInferenceURL() + "/v1"
	models, err := fetchInferenceModels(ctx, baseURL+"/models", key)
	if err != nil {
		return err
	}

	dir, err := codex.Dir()
	if err != nil {
		return err
	}
	catalogPath := flags.CatalogPath
	if catalogPath == "" {
		catalogPath = filepath.Join(dir, "baseten-mapi-models.json")
	}
	// config.toml must reference the catalog absolutely.
	if catalogPath, err = filepath.Abs(catalogPath); err != nil {
		return err
	}

	configPath := filepath.Join(dir, "config.toml")
	if sameFile(catalogPath, configPath) {
		return cmd.NewErrUsagef("--catalog-path must not be Codex's config.toml (%s)", configPath)
	}

	result := cmd.CodexSetupResult{DryRun: flags.DryRun, Found: len(models), Included: []string{}, Excluded: []cmd.CodexExcludedModel{}}
	var catalog []map[string]any
	for _, m := range models {
		if reason := codexExclusion(m); reason != "" {
			result.Excluded = append(result.Excluded, cmd.CodexExcludedModel{ID: m.ID, Reason: reason})
			continue
		}
		result.Included = append(result.Included, m.ID)
		catalog = append(catalog, codexCatalogEntry(m, len(catalog)+1))
	}

	diff, err := codex.WriteCatalog(catalogPath, catalog, !flags.DryRun)
	if err != nil {
		return fmt.Errorf("writing %s: %w", catalogPath, err)
	}
	result.Catalog = cmd.CodexCatalogChange{Path: catalogPath, Added: diff.Added, Removed: diff.Removed, Modified: diff.Modified}

	provider := codex.Provider("Baseten Model APIs", baseURL)
	provider["env_key"] = "BASETEN_API_KEY"
	provider["http_headers"] = map[string]any{}
	// The marketplace ID is the one setting the environment overrides.
	set := map[string]any{}
	if id := os.Getenv("OPENAI_MARKETPLACE_ID"); id != "" {
		set["model_providers"] = map[string]any{codexMAPIProvider: map[string]any{"http_headers": map[string]any{codexMarketplaceHeader: id}}}
	}
	change, err := codex.Patch(configPath, map[string]any{
		"model_catalog_json": catalogPath,
		"model_provider":     codexMAPIProvider,
		"model_providers":    map[string]any{codexMAPIProvider: provider},
	}, set, !flags.DryRun)
	if err != nil {
		return fmt.Errorf("updating %s: %w", configPath, err)
	}
	result.Config = cmd.CodexConfigChange{Path: configPath, Added: change.Added, Preserved: change.Preserved, Replaced: change.Replaced}
	if len(set) == 0 {
		config := map[string]any{}
		// Patch already rejected an unreadable file.
		_, _ = toml.DecodeFile(configPath, &config)
		providers, _ := config["model_providers"].(map[string]any)
		provider, _ := providers[codexMAPIProvider].(map[string]any)
		headers, _ := provider["http_headers"].(map[string]any)
		if _, ok := headers[codexMarketplaceHeader]; ok {
			result.Config.Preserved = append(result.Config.Preserved, codexMarketplaceKey)
		} else {
			result.MarketplaceIDMissing = true
		}
	}

	if ctx.JSON {
		ctx.OutputJSON(result)
		return nil
	}
	writeCodexSetup(ctx, result)
	return nil
}

// sameFile reports whether a and b name the same file, including through
// links when both exist.
func sameFile(a, b string) bool {
	if absA, err := filepath.Abs(a); err == nil {
		if absB, err := filepath.Abs(b); err == nil && filepath.Clean(absA) == filepath.Clean(absB) {
			return true
		}
	}
	infoA, errA := os.Stat(a)
	infoB, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(infoA, infoB)
}

func fetchInferenceModels(ctx *CommandContext, url, key string) ([]inferenceModel, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Api-Key "+key)
	ctx.VerboseLogHTTPRequest(req.Method, url)
	resp, err := ctx.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("listing Model APIs: %w", err)
	}
	defer resp.Body.Close()
	ctx.VerboseLogHTTPResponse(resp)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("listing Model APIs: %w", err)
	}
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, cmd.NewErrAuth(fmt.Errorf("listing Model APIs: %s; check that BASETEN_API_KEY is valid and has the \"Call Model APIs\" permission", resp.Status))
	case resp.StatusCode >= 500:
		return nil, cmd.NewErrServer(fmt.Errorf("listing Model APIs: %s", resp.Status))
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("listing Model APIs: %s", resp.Status)
	}
	var list struct {
		Data []inferenceModel `json:"data"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, fmt.Errorf("listing Model APIs: invalid response: %w", err)
	}
	return list.Data, nil
}

// codexExclusion returns why a model is left out of the catalog, or "".
func codexExclusion(m inferenceModel) string {
	switch {
	case strings.HasPrefix(m.ID, "vllmtest/"):
		return "internal test endpoint"
	case len(m.SupportedFeatures) == 0:
		// Decision models answer with option probabilities, not text.
		return "no supported features"
	}
	return ""
}

var codexReasoningEfforts = []map[string]any{
	{"effort": "low", "description": "Fast responses with lighter reasoning"},
	{"effort": "medium", "description": "Balances speed and reasoning depth for everyday tasks"},
	{"effort": "high", "description": "Greater reasoning depth for complex problems"},
	{"effort": "xhigh", "description": "Extra high reasoning depth for complex problems"},
	{"effort": "max", "description": "Maximum reasoning depth for the hardest problems"},
}

// codexReasoningLevels lists the efforts Codex offers for a model. Codex
// requires at least one, even for models that do not reason.
func codexReasoningLevels(features []string) []map[string]any {
	if slices.Contains(features, "reasoning_effort") {
		return codexReasoningEfforts
	}
	return []map[string]any{{"effort": "medium", "description": "Default reasoning effort"}}
}

// codexCatalogEntry maps a Model API to a Codex catalog entry at the given
// 1-based priority.
func codexCatalogEntry(m inferenceModel, priority int) map[string]any {
	entry := map[string]any{
		"slug":                         m.ID,
		"display_name":                 m.Name,
		"description":                  m.Description,
		"supported_in_api":             true,
		"visibility":                   "list",
		"priority":                     priority,
		"supported_reasoning_levels":   codexReasoningLevels(m.SupportedFeatures),
		"shell_type":                   "unified_exec",
		"support_verbosity":            true,
		"truncation_policy":            map[string]any{"mode": "tokens", "limit": 10000},
		"experimental_supported_tools": []any{},
		"model_messages":               map[string]any{"instructions_template": ""},
	}
	// Codex rejects a catalog with an unknown modality, and treats a model
	// without input_modalities as accepting images.
	modalities := slices.DeleteFunc(slices.Clone(m.InputModalities), func(m string) bool {
		return !slices.Contains([]string{"text", "image", "audio"}, m)
	})
	if len(modalities) == 0 {
		modalities = []string{"text"}
	}
	entry["input_modalities"] = modalities
	if m.ContextLength > 0 {
		entry["context_window"] = m.ContextLength
		entry["max_context_window"] = max(m.ContextLength, m.MaxCompletionTokens)
	}
	return entry
}

func writeCodexSetup(ctx *CommandContext, r cmd.CodexSetupResult) {
	if r.DryRun {
		ctx.OutputLine("Dry run: no files were written.")
	}
	ctx.Outputf("Models: %d found, %d included, %d excluded\n", r.Found, len(r.Included), len(r.Excluded))
	for _, m := range r.Excluded {
		ctx.Outputf("  excluded %s (%s)\n", m.ID, m.Reason)
	}
	ctx.Outputf("Catalog %s: %d added, %d removed, %d modified\n", r.Catalog.Path, len(r.Catalog.Added), len(r.Catalog.Removed), len(r.Catalog.Modified))
	for _, change := range []struct {
		label string
		slugs []string
	}{{"added", r.Catalog.Added}, {"removed", r.Catalog.Removed}, {"modified", r.Catalog.Modified}} {
		for _, slug := range change.slugs {
			ctx.Outputf("  %s %s\n", change.label, slug)
		}
	}
	ctx.Outputf("Config %s: %d keys added, %d replaced, %d preserved\n", r.Config.Path, len(r.Config.Added), len(r.Config.Replaced), len(r.Config.Preserved))
	for _, key := range r.Config.Added {
		ctx.Outputf("  added %s\n", key)
	}
	for _, key := range r.Config.Replaced {
		ctx.Outputf("  replaced %s\n", key)
	}
	for _, key := range r.Config.Preserved {
		ctx.Outputf("  preserved %s\n", key)
	}
	if r.MarketplaceIDMissing {
		ctx.OutputLine("No OPENAI_MARKETPLACE_ID found. If you are in the OpenAI marketplace program, set it and re-run.")
	}
}
