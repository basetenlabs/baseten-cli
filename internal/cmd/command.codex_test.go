package cmd_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/basetenlabs/baseten-cli/internal/cmd"
)

var codexInferenceModels = []map[string]any{
	{
		"id": "zai-org/GLM-5.3-Flash", "name": "GLM 5.3 Flash", "description": "",
		"context_length": 1048576, "max_completion_tokens": 131072,
		"supported_features": []string{"tools", "reasoning", "reasoning_effort"},
		"input_modalities":   []string{"text"},
	},
	{
		"id": "moonshotai/Kimi-K3", "name": "Kimi K3", "description": "Agentic coding",
		"context_length": 262144, "max_completion_tokens": 300000,
		"supported_features": []string{"tools", "reasoning"},
		"input_modalities":   []string{"text", "image", "video"},
	},
	{"id": "vllmtest/llama", "name": "test", "context_length": 8192, "supported_features": []string{"tools"}},
	{"id": "decision-models/joey", "name": "Joey", "context_length": 8192, "supported_features": []string{}},
}

// codexHarness serves models from a fake inference endpoint and points
// CODEX_HOME at a temporary directory.
func codexHarness(t *testing.T, models []map[string]any) (*CommandHarness, string, *[]*http.Request) {
	h := NewCommandHarness(t)
	var requests []*http.Request
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r)
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": models})
	}))
	t.Cleanup(server.Close)
	target, _ := url.Parse(server.URL)
	h.Context = cmd.WithHTTPClient(h.Context, &http.Client{Transport: rewriteHost{target}})
	t.Setenv("BASETEN_REMOTE_URL", "")
	t.Setenv("BASETEN_MANAGEMENT_API_URL_OVERRIDE", "")
	dir := filepath.Join(t.TempDir(), "codex")
	t.Setenv("CODEX_HOME", dir)
	t.Setenv("OPENAI_MARKETPLACE_ID", "")
	return h, dir, &requests
}

type rewriteHost struct{ target *url.URL }

func (r rewriteHost) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.URL.Scheme, req.URL.Host = r.target.Scheme, r.target.Host
	return http.DefaultTransport.RoundTrip(req)
}

func readCodexConfig(t *testing.T, dir string) map[string]any {
	t.Helper()
	return readCodexFile(t, filepath.Join(dir, "config.toml"))
}

func readCodexFile(t *testing.T, path string) map[string]any {
	t.Helper()
	config := map[string]any{}
	_, err := toml.DecodeFile(path, &config)
	if err != nil {
		t.Fatal(err)
	}
	return config
}

func Test_Codex_Setup_FreshConfig(t *testing.T) {
	h, dir, requests := codexHarness(t, codexInferenceModels)
	h.Require.NoError(h.Execute("codex", "setup"))

	h.Require.Len(*requests, 1)
	h.Require.Equal("Api-Key test-key", (*requests)[0].Header.Get("Authorization"))
	h.Require.Equal("inference.baseten.co", (*requests)[0].Host)

	catalogPath := filepath.Join(dir, "baseten-mapi-models.json")
	data, err := os.ReadFile(catalogPath)
	h.Require.NoError(err)
	var catalog struct {
		Models []map[string]any `json:"models"`
	}
	h.Require.NoError(json.Unmarshal(data, &catalog))
	h.Require.Len(catalog.Models, 2)
	glm, _ := json.Marshal(catalog.Models[0])
	h.Require.JSONEq(`{
		"slug": "zai-org/GLM-5.3-Flash",
		"display_name": "GLM 5.3 Flash",
		"description": "",
		"context_window": 1048576,
		"max_context_window": 1048576,
		"supported_in_api": true,
		"visibility": "list",
		"priority": 1,
		"supported_reasoning_levels": [
			{"effort": "low", "description": "Fast responses with lighter reasoning"},
			{"effort": "medium", "description": "Balances speed and reasoning depth for everyday tasks"},
			{"effort": "high", "description": "Greater reasoning depth for complex problems"},
			{"effort": "xhigh", "description": "Extra high reasoning depth for complex problems"},
			{"effort": "max", "description": "Maximum reasoning depth for the hardest problems"}
		],
		"shell_type": "unified_exec",
		"support_verbosity": true,
		"truncation_policy": {"mode": "tokens", "limit": 10000},
		"experimental_supported_tools": [],
		"model_messages": {"instructions_template": ""},
		"input_modalities": ["text"]
	}`, string(glm))
	kimi := catalog.Models[1]
	h.Require.Equal("Agentic coding", kimi["description"])
	h.Require.EqualValues(2, kimi["priority"])
	h.Require.EqualValues(300000, kimi["max_context_window"], "max_completion_tokens wins when larger")
	h.Require.Equal([]any{map[string]any{"effort": "medium", "description": "Default reasoning effort"}}, kimi["supported_reasoning_levels"])
	h.Require.Equal([]any{"text", "image"}, kimi["input_modalities"], "Codex rejects unknown modalities")

	config := readCodexConfig(t, dir)
	h.Require.NotContains(config, "model_catalog_json", "plain codex keeps its provider")
	h.Require.NotContains(config, "model_provider")
	h.Require.NotContains(config, "model")
	h.Require.NotContains(config, "model_reasoning_effort")
	h.Require.Equal(map[string]any{
		"name":         "Baseten Model APIs",
		"base_url":     "https://inference.baseten.co/v1",
		"env_key":      "BASETEN_API_KEY",
		"wire_api":     "responses",
		"http_headers": map[string]any{},
	}, config["model_providers"].(map[string]any)["baseten-mapi"])

	out := h.Stdout.String()
	h.Require.Contains(out, "Models: 4 found, 2 included, 2 excluded")
	h.Require.Contains(out, "excluded vllmtest/llama (internal test endpoint)")
	h.Require.Contains(out, "excluded decision-models/joey (no supported features)")
	h.Require.Contains(out, "2 added, 0 removed, 0 modified")
	h.Require.Contains(out, "5 keys added, 0 replaced, 0 preserved")
	h.Require.Contains(out, "codex --profile baseten --no-daemon")
	h.Require.Contains(out, "run 'baseten codex use --provider baseten'")
	h.Require.Equal(map[string]any{"model_provider": "baseten-mapi", "model_catalog_json": catalogPath, "model": "zai-org/GLM-5.3-Flash"}, readCodexFile(t, filepath.Join(dir, "baseten.config.toml")),
		"the profile sets a Baseten model so it never inherits config.toml's")
	h.Require.Contains(out, "No OPENAI_MARKETPLACE_ID found. If you are in the OpenAI marketplace program, set it and re-run.")
}

func Test_Codex_Setup_Idempotent(t *testing.T) {
	h, dir, _ := codexHarness(t, codexInferenceModels)
	h.Require.NoError(h.Execute("codex", "setup"))
	catalog, _ := os.ReadFile(filepath.Join(dir, "baseten-mapi-models.json"))
	config, _ := os.ReadFile(filepath.Join(dir, "config.toml"))

	h.Require.NoError(h.Execute("codex", "setup", "--output", "json"))
	var result map[string]any
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &result))
	h.Require.Equal(map[string]any{"path": filepath.Join(dir, "baseten-mapi-models.json"), "added": []any{}, "removed": []any{}, "modified": []any{}}, result["catalog"])
	h.Require.Equal([]any{}, result["config"].(map[string]any)["added"])

	catalog2, _ := os.ReadFile(filepath.Join(dir, "baseten-mapi-models.json"))
	config2, _ := os.ReadFile(filepath.Join(dir, "config.toml"))
	h.Require.Equal(string(catalog), string(catalog2))
	h.Require.Equal(string(config), string(config2))
}

func Test_Codex_Setup_PreservesExistingAndGridConfig(t *testing.T) {
	h, dir, _ := codexHarness(t, codexInferenceModels)
	gridCatalog := filepath.Join(dir, "baseten-models.json")
	h.Require.NoError(os.MkdirAll(dir, 0o700))
	h.Require.NoError(os.WriteFile(gridCatalog, []byte(`{"models":[]}`), 0o600))
	existing := `model = "grid-route"
model_provider = "baseten"
model_catalog_json = '` + gridCatalog + `'

[model_providers.baseten]
name = "Baseten"
base_url = "https://grid.example/v1"

[model_providers.baseten-mapi]
name = "My Name"

[model_providers.baseten-mapi.http_headers]
OpenAI-Marketplace-Id = "mkt-123"
`
	h.Require.NoError(os.WriteFile(filepath.Join(dir, "config.toml"), []byte(existing), 0o600))

	h.Require.NoError(h.Execute("codex", "setup"))

	config := readCodexConfig(t, dir)
	h.Require.Equal("grid-route", config["model"])
	h.Require.Equal("baseten", config["model_provider"])
	h.Require.Equal(gridCatalog, config["model_catalog_json"])
	providers := config["model_providers"].(map[string]any)
	h.Require.Equal(map[string]any{"name": "Baseten", "base_url": "https://grid.example/v1"}, providers["baseten"])
	h.Require.Equal(map[string]any{
		"name":         "My Name",
		"base_url":     "https://inference.baseten.co/v1",
		"env_key":      "BASETEN_API_KEY",
		"wire_api":     "responses",
		"http_headers": map[string]any{"OpenAI-Marketplace-Id": "mkt-123"},
	}, providers["baseten-mapi"])
	grid, _ := os.ReadFile(gridCatalog)
	h.Require.Equal(`{"models":[]}`, string(grid))
	h.Require.FileExists(filepath.Join(dir, "baseten-mapi-models.json"))
	h.Require.Contains(h.Stdout.String(), "3 keys added, 0 replaced, 2 preserved")
	h.Require.NotContains(h.Stdout.String(), "No OPENAI_MARKETPLACE_ID found")
}

func Test_Codex_Setup_DryRun(t *testing.T) {
	h, dir, _ := codexHarness(t, codexInferenceModels)
	catalogPath := filepath.Join(t.TempDir(), "custom.json")
	h.Require.NoError(h.Execute("codex", "setup", "--dry-run", "--catalog-path", catalogPath))
	h.Require.NoFileExists(catalogPath)
	h.Require.NoFileExists(filepath.Join(dir, "config.toml"))
	out := h.Stdout.String()
	h.Require.Contains(out, "Dry run: no files were written.")
	h.Require.Contains(out, "added model_catalog_json")
	h.Require.Contains(out, "Catalog "+catalogPath)
}

func Test_Codex_Setup_ReportsCatalogChanges(t *testing.T) {
	h, dir, _ := codexHarness(t, codexInferenceModels[:2])
	h.Require.NoError(h.Execute("codex", "setup"))

	// Drop GLM: Kimi moves to priority 1, so it is modified.
	h2, _, _ := codexHarness(t, codexInferenceModels[1:2])
	t.Setenv("CODEX_HOME", dir)
	h2.Require.NoError(h2.Execute("codex", "setup"))
	out := h2.Stdout.String()
	h2.Require.Contains(out, "0 added, 1 removed, 1 modified")
	h2.Require.Contains(out, "removed zai-org/GLM-5.3-Flash")
	h2.Require.Contains(out, "modified moonshotai/Kimi-K3")
}

func Test_Codex_Setup_MissingAPIKey(t *testing.T) {
	h, dir, requests := codexHarness(t, codexInferenceModels)
	t.Setenv("BASETEN_API_KEY", "")
	err := h.Execute("codex", "setup")
	h.Require.Error(err)
	h.Require.Contains(err.Error(), "Call Model APIs")
	h.Require.Empty(*requests)
	h.Require.NoDirExists(dir)
}

func Test_Codex_Setup_RejectedAPIKey(t *testing.T) {
	h := NewCommandHarness(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(server.Close)
	target, _ := url.Parse(server.URL)
	h.Context = cmd.WithHTTPClient(h.Context, &http.Client{Transport: rewriteHost{target}})
	t.Setenv("BASETEN_REMOTE_URL", "")
	t.Setenv("BASETEN_MANAGEMENT_API_URL_OVERRIDE", "")
	t.Setenv("CODEX_HOME", t.TempDir())
	err := h.Execute("codex", "setup")
	h.Require.Error(err)
	h.Require.True(strings.Contains(err.Error(), "401"), err.Error())
	h.Require.NotContains(err.Error(), "test-key")
}

func Test_Codex_Setup_MarketplaceIDFromEnv(t *testing.T) {
	h, dir, _ := codexHarness(t, codexInferenceModels)
	t.Setenv("OPENAI_MARKETPLACE_ID", "mkt-1")
	h.Require.NoError(h.Execute("codex", "setup"))
	headers := func() any {
		return readCodexConfig(t, dir)["model_providers"].(map[string]any)["baseten-mapi"].(map[string]any)["http_headers"]
	}
	h.Require.Equal(map[string]any{"OpenAI-Marketplace-Id": "mkt-1"}, headers())
	h.Require.Contains(h.Stdout.String(), "added model_providers.baseten-mapi.http_headers.OpenAI-Marketplace-Id")
	h.Require.NotContains(h.Stdout.String(), "No OPENAI_MARKETPLACE_ID found")

	t.Setenv("OPENAI_MARKETPLACE_ID", "mkt-2")
	h.Require.NoError(h.Execute("codex", "setup"))
	h.Require.Equal(map[string]any{"OpenAI-Marketplace-Id": "mkt-2"}, headers())
	h.Require.Contains(h.Stdout.String(), "replaced model_providers.baseten-mapi.http_headers.OpenAI-Marketplace-Id")

	// Unset: the existing header is preserved.
	t.Setenv("OPENAI_MARKETPLACE_ID", "")
	h.Require.NoError(h.Execute("codex", "setup"))
	h.Require.Equal(map[string]any{"OpenAI-Marketplace-Id": "mkt-2"}, headers())
	h.Require.Contains(h.Stdout.String(), "preserved model_providers.baseten-mapi.http_headers.OpenAI-Marketplace-Id")
	h.Require.NotContains(h.Stdout.String(), "No OPENAI_MARKETPLACE_ID found")
}

func Test_Codex_Setup_RejectsCatalogPathAtConfig(t *testing.T) {
	h, dir, _ := codexHarness(t, codexInferenceModels)
	h.Require.NoError(os.MkdirAll(dir, 0o700))
	config := filepath.Join(dir, "config.toml")
	h.Require.NoError(os.WriteFile(config, []byte("model = \"gpt-5\"\n"), 0o600))
	link := filepath.Join(t.TempDir(), "link.json")
	h.Require.NoError(os.Link(config, link))

	for _, path := range []string{config, link} {
		err := h.Execute("codex", "setup", "--catalog-path", path)
		h.Require.Error(err)
		h.Require.Contains(err.Error(), "--catalog-path must not be")
	}
	data, err := os.ReadFile(config)
	h.Require.NoError(err)
	h.Require.Equal("model = \"gpt-5\"\n", string(data))
}

func Test_Codex_Setup_Default(t *testing.T) {
	h, dir, _ := codexHarness(t, codexInferenceModels)
	h.Require.NoError(os.MkdirAll(dir, 0o700))
	h.Require.NoError(os.WriteFile(filepath.Join(dir, "config.toml"), []byte("model_provider = \"openai\"\nmodel = \"gpt-6\"\n"), 0o600))
	h.Require.NoError(h.Execute("codex", "setup", "--default"))
	config := readCodexConfig(t, dir)
	h.Require.Equal("baseten-mapi", config["model_provider"])
	h.Require.Equal(filepath.Join(dir, "baseten-mapi-models.json"), config["model_catalog_json"])
	h.Require.Equal("zai-org/GLM-5.3-Flash", config["model"], "an OpenAI model is replaced with the profile's Baseten model")
	out := h.Stdout.String()
	h.Require.Contains(out, "replaced model_provider")
	h.Require.Contains(out, "replaced model\n")
	h.Require.Contains(out, "Baseten Model APIs are the default provider")
}

func Test_Codex_Use(t *testing.T) {
	h, dir, _ := codexHarness(t, codexInferenceModels)
	err := h.Execute("codex", "use", "--provider", "baseten")
	h.Require.Error(err)
	h.Require.Contains(err.Error(), "run 'baseten codex setup' first")
	h.Require.Contains(err.Error(), "baseten.config.toml has no model_catalog_json")

	h.Require.NoError(os.MkdirAll(dir, 0o700))
	h.Require.NoError(os.WriteFile(filepath.Join(dir, "config.toml"), []byte("model_provider = \"openai\"\n"), 0o600))
	h.Require.NoError(h.Execute("codex", "setup"))

	h.Require.NoError(os.WriteFile(filepath.Join(dir, "auth.json"), []byte(`{"auth_mode":"chatgpt"}`), 0o600))
	h.Require.NoError(os.WriteFile(filepath.Join(dir, "cloud-config-bundle-cache.json"), []byte(`{}`), 0o600))
	h.Require.NoError(h.Execute("codex", "use", "--provider", "baseten"))
	h.Require.Contains(h.Stdout.String(), "Codex now uses baseten")
	h.Require.NoFileExists(filepath.Join(dir, "auth.json"), "a ChatGPT login is signed out")
	h.Require.NoFileExists(filepath.Join(dir, "cloud-config-bundle-cache.json"))
	config := readCodexConfig(t, dir)
	h.Require.Equal("baseten-mapi", config["model_provider"])
	h.Require.Equal(filepath.Join(dir, "baseten-mapi-models.json"), config["model_catalog_json"])
	h.Require.Equal("zai-org/GLM-5.3-Flash", config["model"], "a Baseten model is selected so Codex never starts on an OpenAI default")
	h.Require.Equal("api", config["forced_login_method"], "a ChatGPT login would apply the workspace default model")

	h.Require.NoError(h.Execute("codex", "use", "--provider", "baseten"))
	h.Require.Contains(h.Stdout.String(), "Codex already uses baseten")

	h.Require.NoError(h.Execute("codex", "use", "--provider", "openai"))
	// Signed out: the background server restarts after sign-in, or it keeps
	// reporting no login.
	h.Require.Contains(h.Stdout.String(), "Run 'codex login && codex app-server daemon restart'")
	h.Require.NotContains(h.Stdout.String(), "Codex now uses openai")
	config = readCodexConfig(t, dir)
	h.Require.NotContains(config, "forced_login_method")
	h.Require.NotContains(config, "model", "a Baseten model is cleared for OpenAI")
	h.Require.NotContains(config, "model_provider")
	h.Require.NotContains(config, "model_catalog_json")
	h.Require.Contains(config["model_providers"], "baseten-mapi", "the provider stays defined for the profile")
}

func Test_Codex_Setup_InvalidConfigWritesNothing(t *testing.T) {
	h, dir, _ := codexHarness(t, codexInferenceModels)
	h.Require.NoError(os.MkdirAll(dir, 0o700))
	h.Require.NoError(os.WriteFile(filepath.Join(dir, "config.toml"), []byte("model = \"a\"\nmodel = \"b\"\n"), 0o600))
	err := h.Execute("codex", "setup")
	h.Require.Error(err)
	h.Require.Contains(err.Error(), "repair it before continuing")
	h.Require.NoFileExists(filepath.Join(dir, "baseten-mapi-models.json"))
	h.Require.NoFileExists(filepath.Join(dir, "baseten.config.toml"))
}

func Test_Codex_Setup_ProfileModel(t *testing.T) {
	h, dir, _ := codexHarness(t, codexInferenceModels)
	profile := filepath.Join(dir, "baseten.config.toml")
	h.Require.NoError(os.MkdirAll(dir, 0o700))

	// Codex saves a model picked in a profile session to the profile; keep it.
	h.Require.NoError(os.WriteFile(profile, []byte("model = \"moonshotai/Kimi-K3\"\n"), 0o600))
	h.Require.NoError(h.Execute("codex", "setup"))
	h.Require.Equal("moonshotai/Kimi-K3", readCodexFile(t, profile)["model"])

	// A model no longer in the catalog is replaced with the first one.
	h.Require.NoError(os.WriteFile(profile, []byte("model = \"gpt-6\"\n"), 0o600))
	h.Require.NoError(h.Execute("codex", "setup"))
	h.Require.Equal("zai-org/GLM-5.3-Flash", readCodexFile(t, profile)["model"])
}

func Test_Codex_Setup_InvalidProfileWritesNothing(t *testing.T) {
	h, dir, _ := codexHarness(t, codexInferenceModels)
	h.Require.NoError(os.MkdirAll(dir, 0o700))
	h.Require.NoError(os.WriteFile(filepath.Join(dir, "baseten.config.toml"), []byte("model = \n"), 0o600))
	err := h.Execute("codex", "setup")
	h.Require.Error(err)
	h.Require.Contains(err.Error(), "repair it before continuing")
	h.Require.NoFileExists(filepath.Join(dir, "baseten-mapi-models.json"))
	h.Require.NoFileExists(filepath.Join(dir, "config.toml"))
}

func Test_Codex_Setup_DryRunReportsActive(t *testing.T) {
	h, dir, _ := codexHarness(t, codexInferenceModels)
	h.Require.NoError(os.MkdirAll(dir, 0o700))
	h.Require.NoError(os.WriteFile(filepath.Join(dir, "config.toml"), []byte("model_provider = \"baseten-mapi\"\n"), 0o600))
	h.Require.NoError(h.Execute("codex", "setup", "--dry-run", "--output", "json"))
	var result map[string]any
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &result))
	h.Require.Equal(true, result["active"])
}

func Test_Codex_Use_OpenAIKeepsOtherProvider(t *testing.T) {
	h, dir, _ := codexHarness(t, codexInferenceModels)
	h.Require.NoError(h.Execute("codex", "setup"))
	config := filepath.Join(dir, "config.toml")
	data, err := os.ReadFile(config)
	h.Require.NoError(err)
	data = append([]byte("model_provider = \"baseten\"\n"), data...)
	h.Require.NoError(os.WriteFile(config, data, 0o600))

	err = h.Execute("codex", "use", "--provider", "openai")
	h.Require.Error(err)
	h.Require.Contains(err.Error(), "Codex uses the baseten provider")
	after, err := os.ReadFile(config)
	h.Require.NoError(err)
	h.Require.Equal(string(data), string(after))
}

func Test_Codex_Use_OpenAIAlreadyExplicit(t *testing.T) {
	h, dir, _ := codexHarness(t, codexInferenceModels)
	h.Require.NoError(os.MkdirAll(dir, 0o700))
	h.Require.NoError(os.WriteFile(filepath.Join(dir, "config.toml"), []byte("model_provider = \"openai\"\n"), 0o600))
	h.Require.NoError(h.Execute("codex", "use", "--provider", "openai"))
	h.Require.Contains(h.Stdout.String(), "Codex already uses openai")
	h.Require.Equal("openai", readCodexConfig(t, dir)["model_provider"])
}

func Test_Codex_Teardown(t *testing.T) {
	h, dir, _ := codexHarness(t, codexInferenceModels)
	h.Require.NoError(os.MkdirAll(dir, 0o700))
	config := filepath.Join(dir, "config.toml")
	h.Require.NoError(os.WriteFile(config, []byte("model_provider = \"openai\"\n[model_providers.baseten]\nname = \"Grid\"\n"), 0o600))
	h.Require.NoError(h.Execute("codex", "setup", "--default"))

	h.Require.NoError(h.Execute("codex", "teardown", "--dry-run"))
	h.Require.Contains(h.Stdout.String(), "would delete ")
	h.Require.FileExists(filepath.Join(dir, "baseten.config.toml"))

	h.Require.NoError(h.Execute("codex", "teardown"))
	out := h.Stdout.String()
	h.Require.Contains(out, "removed model_providers.baseten-mapi")
	h.Require.Contains(out, "removed model_provider ")
	h.Require.Contains(out, "Run 'codex login && codex app-server daemon restart'", "setup --default signed Codex out")
	h.Require.NoFileExists(filepath.Join(dir, "baseten.config.toml"))
	h.Require.NoFileExists(filepath.Join(dir, "baseten-mapi-models.json"))
	h.Require.Equal(map[string]any{"model_providers": map[string]any{"baseten": map[string]any{"name": "Grid"}}}, readCodexConfig(t, dir), "other providers stay")

	h.Require.NoError(h.Execute("codex", "teardown"))
	h.Require.Contains(h.Stdout.String(), "nothing to remove")
}
