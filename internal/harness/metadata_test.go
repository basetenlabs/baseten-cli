package harness

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

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

	setup(t, claudeCodeHarness{}, path, []Route{mid, huge}, Selection{Background: "acme/huge"})
	d = load(t, path)
	require.Equal(t, []any{"acme/mid", "acme/huge[1m]"}, d["availableModels"])
	require.Equal(t, "acme/huge[1m]", get(d, []string{"env", "ANTHROPIC_SMALL_FAST_MODEL"}).Data)
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

func TestOpenCodeCarriesRouteCost(t *testing.T) {
	cacheRead := 0.3
	priced := testRoute("acme/primary", "Primary")
	priced.Cost = &Cost{Input: 3, Output: 15, CacheRead: &cacheRead, LongContext: &Cost{Input: 6, Output: 22.5}}
	path := settingsPath(t, openCodeHarness{})
	setup(t, openCodeHarness{}, path, []Route{priced, testRoute("acme/unpriced", "Unpriced")}, Selection{})
	models := get(load(t, path), []string{"provider", providerID, "models"}).Data.(map[string]any)
	require.Equal(t, map[string]any{
		"input": json.Number("3"), "output": json.Number("15"), "cache_read": json.Number("0.3"),
		"context_over_200k": map[string]any{"input": json.Number("6"), "output": json.Number("22.5")},
	}, models["acme/primary"].(map[string]any)["cost"])
	require.NotContains(t, models["acme/unpriced"], "cost")
}
