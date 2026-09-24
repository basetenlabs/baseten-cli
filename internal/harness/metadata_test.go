package harness

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func metadataRoutes() []Route {
	return []Route{
		{Name: "acme/primary", DisplayName: "Primary", ContextWindow: 128000, OutputLimit: 4096, InputModalities: []string{"text"}, Tools: true, Messages: true, Responses: true, ChatCompletions: true, ReasoningLevels: []string{"low", "medium", "high"}, ParallelTools: true},
		{Name: "acme/background", DisplayName: "Background", ContextWindow: 64000, OutputLimit: 8192, InputModalities: []string{"text"}, Tools: true, Messages: true, Responses: true, ChatCompletions: true},
	}
}

func chatOnlyRoute() Route {
	return Route{Name: "acme/chat", DisplayName: "Chat only", Messages: false, Responses: false, ChatCompletions: true, Tools: true, ContextWindow: 64000, OutputLimit: 8192, InputModalities: []string{"text"}}
}

func unknownFormatsRoute() Route {
	return Route{Name: "acme/unknown", DisplayName: "Unknown formats", Tools: true, ContextWindow: 64000, OutputLimit: 8192, InputModalities: []string{"text"}}
}

func TestCodexChatOnlyRouteAddsChatProvider(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	routes := append(metadataRoutes()[:1:1], chatOnlyRoute())
	responses, chat := WireFamilies(routes, Selection{Primary: "acme/primary", Background: "acme/chat"})
	require.True(t, responses)
	require.True(t, chat)
	plans, err := codexHarness{}.Prepare(path, routes, nil, Selection{Primary: "acme/primary"}, testEndpoint, "baseten-harness-pending-key")
	require.NoError(t, err)
	require.NoError(t, ApplyPlans(plans, testToken))
	d := load(t, path)
	require.Equal(t, providerID, get(d, []string{"model_provider"}).Data)
	responsesBlock := get(d, []string{"model_providers", providerID}).Data.(map[string]any)
	chatBlock := get(d, []string{"model_providers", chatProviderID}).Data.(map[string]any)
	require.Equal(t, "responses", responsesBlock["wire_api"])
	require.Equal(t, "chat", chatBlock["wire_api"])
	require.Equal(t, responsesBlock["base_url"], chatBlock["base_url"])
	require.Equal(t, testToken, responsesBlock["experimental_bearer_token"])
	require.Equal(t, testToken, chatBlock["experimental_bearer_token"])
}

func TestCodexChatOnlyPrimarySelectsChatProvider(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	plans, err := codexHarness{}.Prepare(path, []Route{chatOnlyRoute()}, nil, Selection{Primary: "acme/chat"}, testEndpoint, testToken)
	require.NoError(t, err)
	require.NoError(t, ApplyPlans(plans, testToken))
	d := load(t, path)
	require.Equal(t, chatProviderID, get(d, []string{"model_provider"}).Data)
	require.False(t, get(d, []string{"model_providers", providerID}).Exists)
	require.Equal(t, "chat", get(d, []string{"model_providers", chatProviderID, "wire_api"}).Data)
}

func TestCodexUnknownFormatsRouteUsesChatProvider(t *testing.T) {
	responses, chat := WireFamilies([]Route{unknownFormatsRoute()}, Selection{Primary: "acme/unknown"})
	require.False(t, responses)
	require.True(t, chat)
	path := filepath.Join(t.TempDir(), "config.toml")
	plans, err := codexHarness{}.Prepare(path, []Route{unknownFormatsRoute()}, nil, Selection{Primary: "acme/unknown"}, testEndpoint, testToken)
	require.NoError(t, err)
	require.NoError(t, ApplyPlans(plans, testToken))
	d := load(t, path)
	require.Equal(t, chatProviderID, get(d, []string{"model_provider"}).Data)
	require.False(t, get(d, []string{"model_providers", providerID}).Exists)
	require.Equal(t, "chat", get(d, []string{"model_providers", chatProviderID, "wire_api"}).Data)
}

func TestClaudeIncludesAllRoutesInPicker(t *testing.T) {
	routes := append(metadataRoutes(), chatOnlyRoute(), unknownFormatsRoute())
	path := filepath.Join(t.TempDir(), "settings.json")
	plans, err := claudeCodeHarness{}.Prepare(path, routes, nil, Selection{Primary: "acme/primary"}, testEndpoint, testToken)
	require.NoError(t, err)
	require.NoError(t, ApplyPlans(plans, testToken))
	d := load(t, path)
	require.Len(t, get(d, []string{"modelPicker", "options"}).Data, 4)
	require.Contains(t, get(d, []string{"availableModels"}).Data, "acme/chat")
	require.Contains(t, get(d, []string{"availableModels"}).Data, "acme/unknown")
	require.Equal(t, "128000", get(d, []string{"env", "CLAUDE_CODE_MAX_CONTEXT_TOKENS"}).Data)
	require.Equal(t, "4096", get(d, []string{"env", "CLAUDE_CODE_MAX_OUTPUT_TOKENS"}).Data)
	_, err = claudeCodeHarness{}.Prepare(path, routes, nil, Selection{Primary: "acme/chat"}, testEndpoint, testToken)
	require.NoError(t, err)
	_, err = claudeCodeHarness{}.Prepare(path, routes, nil, Selection{Primary: "acme/unknown"}, testEndpoint, testToken)
	require.NoError(t, err)
}

func TestCodexCatalogCarriesRouteMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	plans, err := codexHarness{}.Prepare(path, metadataRoutes(), nil, Selection{Primary: "acme/primary"}, testEndpoint, testToken)
	require.NoError(t, err)
	require.NoError(t, ApplyPlans(plans, testToken))
	catalog := load(t, catalogPath(path))
	model := catalog["models"].([]any)[0].(map[string]any)
	require.Equal(t, "acme/primary", model["slug"])
	require.Equal(t, []any{
		map[string]any{"effort": "low", "description": "low"},
		map[string]any{"effort": "medium", "description": "medium"},
		map[string]any{"effort": "high", "description": "high"},
	}, model["supported_reasoning_levels"])
	require.Equal(t, "low", model["default_reasoning_level"])
	require.Equal(t, true, model["supports_parallel_tool_calls"])
	require.Equal(t, json.Number("128000"), model["context_window"])
	require.Equal(t, []any{"text"}, model["input_modalities"])
}

func TestDefaultReasoningLevelPicksLowestRealEffort(t *testing.T) {
	require.Equal(t, "minimal", defaultReasoningLevel([]string{"high", "minimal", "none"}))
	require.Equal(t, "low", defaultReasoningLevel([]string{"medium", "low"}))
	require.Nil(t, defaultReasoningLevel([]string{"none"}))
	require.Nil(t, defaultReasoningLevel(nil))
}

func TestNormalizeReasoningLevelsMapsServerVocabulary(t *testing.T) {
	require.Equal(t, []string{"none", "low", "high", "xhigh"}, NormalizeReasoningLevels([]string{"none", "low", "high", "max"}))
	require.Equal(t, []string{"low", "xhigh"}, NormalizeReasoningLevels([]string{"low", "max", "xhigh"}))
	require.Empty(t, NormalizeReasoningLevels(nil))
	_, err := ValidateCatalog([]Route{{Name: "acme/turbo", DisplayName: "Turbo", Tools: true, ReasoningLevels: NormalizeReasoningLevels([]string{"turbo"})}})
	require.ErrorContains(t, err, `Route "acme/turbo" has an unsupported reasoning level`)
}
