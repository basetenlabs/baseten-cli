package harness

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func chatRoute() Route {
	r := testRoute("acme/chat", "Chat")
	r.Responses = false
	return r
}

func TestCodexChatRouteAddsChatProvider(t *testing.T) {
	path := settingsPath(t, codexHarness{})
	setup(t, codexHarness{}, path, []Route{testRoute("acme/primary", "Primary"), chatRoute()}, Selection{})
	d := load(t, path)
	require.Equal(t, providerID, d["model_provider"])
	responses := get(d, []string{"model_providers", providerID}).Data.(map[string]any)
	chat := get(d, []string{"model_providers", chatProviderID}).Data.(map[string]any)
	require.Equal(t, "responses", responses["wire_api"])
	require.Equal(t, "chat", chat["wire_api"])
	require.Equal(t, responses["base_url"], chat["base_url"])
	require.Equal(t, testToken, responses["experimental_bearer_token"])
	require.Equal(t, testToken, chat["experimental_bearer_token"])
}

func TestCodexChatPrimarySelectsChatProvider(t *testing.T) {
	path := settingsPath(t, codexHarness{})
	setup(t, codexHarness{}, path, []Route{testRoute("acme/primary", "Primary"), chatRoute()}, Selection{})
	setup(t, codexHarness{}, path, []Route{chatRoute()}, Selection{})
	d := load(t, path)
	require.Equal(t, chatProviderID, d["model_provider"])
	require.False(t, get(d, []string{"model_providers", providerID}).Exists, "refresh removes the unused provider")
	token, err := codexHarness{}.Credential(path)
	require.NoError(t, err)
	require.Equal(t, testToken, token)
	status, err := codexHarness{}.Inspect(Detection{Name: Codex, Path: path})
	require.NoError(t, err)
	require.Equal(t, StateConfigured, status.State)
	teardown(t, codexHarness{}, path)
	require.Empty(t, load(t, path))
}

func TestCodexCatalogCarriesRouteMetadata(t *testing.T) {
	path := settingsPath(t, codexHarness{})
	r := testRoute("acme/primary", "Primary")
	r.ReasoningLevels = []string{"none", "low", "medium", "high"}
	r.ParallelTools = true
	setup(t, codexHarness{}, path, []Route{r}, Selection{})
	model := load(t, catalogPath(path))["models"].([]any)[0].(map[string]any)
	require.Equal(t, []any{
		map[string]any{"effort": "none", "description": "none"},
		map[string]any{"effort": "low", "description": "low"},
		map[string]any{"effort": "medium", "description": "medium"},
		map[string]any{"effort": "high", "description": "high"},
	}, model["supported_reasoning_levels"])
	require.Equal(t, "low", model["default_reasoning_level"])
	require.Equal(t, true, model["supports_parallel_tool_calls"])
	require.Equal(t, json.Number("128000"), model["context_window"])
	require.Equal(t, []any{"text"}, model["input_modalities"])
}

func TestClaudeWideContextRoutesMintWindowAliases(t *testing.T) {
	huge := testRoute("acme/huge", "Huge")
	huge.ContextWindow = 1000000
	huge.ReasoningLevels = []string{"none", "low", "high", "xhigh"}
	mid := testRoute("acme/mid", "Mid")
	mid.ContextWindow = 200000
	path := settingsPath(t, claudeCodeHarness{})
	setup(t, claudeCodeHarness{}, path, []Route{huge, mid}, Selection{})
	d := load(t, path)
	require.Equal(t, "acme/huge[1m]", d["model"])
	require.Equal(t, []any{
		map[string]any{"model": "acme/huge[1m]", "label": "Huge"},
		map[string]any{"model": "acme/mid", "label": "Mid"},
	}, get(d, []string{"modelPicker", "options"}).Data)
	require.Equal(t, []any{"acme/huge[1m]", "acme/mid", defaultBackgroundRoute}, d["availableModels"])
	require.Equal(t, map[string]any{"acme/huge[1m]": "acme/huge"}, d["modelOverrides"])
	require.Equal(t, map[string]any{
		"acme/huge[1m]": map[string]any{"maxEffortLevel": "xhigh", "effortLevel": "low"},
	}, d["modelSettings"])
	require.Equal(t, "acme/huge[1m]", get(d, []string{"env", "ANTHROPIC_DEFAULT_OPUS_MODEL"}).Data)
	require.Equal(t, "4096", get(d, []string{"env", "CLAUDE_CODE_MAX_OUTPUT_TOKENS"}).Data)
}

func TestReasoningBoundsSkipNone(t *testing.T) {
	low, high := reasoningBounds([]string{"high", "minimal", "none"})
	require.Equal(t, "minimal", low)
	require.Equal(t, "high", high)
	low, high = reasoningBounds([]string{"none"})
	require.Nil(t, low)
	require.Nil(t, high)
}

func TestNormalizeReasoningLevelsMapsServerVocabulary(t *testing.T) {
	require.Equal(t, []string{"none", "low", "xhigh"}, NormalizeReasoningLevels([]string{"none", "low", "max", "xhigh"}))
	r := testRoute("acme/turbo", "Turbo")
	r.ReasoningLevels = NormalizeReasoningLevels([]string{"turbo"})
	require.ErrorContains(t, ValidateRoute(r), `route "acme/turbo" has an unsupported reasoning level`)
}

func TestValidateRouteRequiresLimits(t *testing.T) {
	r := testRoute("acme/primary", "Primary")
	r.OutputLimit = r.ContextWindow
	require.ErrorContains(t, ValidateRoute(r), "no usable context and output limits")
	r = testRoute("acme/primary", "Primary")
	r.Tools = false
	require.ErrorContains(t, ValidateRoute(r), "lacks tool support")
}
