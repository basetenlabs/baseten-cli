package harness

import (
	"encoding/json"
	"maps"
	"slices"
	"testing"

	"github.com/basetenlabs/baseten-go/client/managementapi"
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
		"acme/huge[1m]": map[string]any{"maxEffortLevel": "xhigh"},
	}, d["modelSettings"])
	require.Equal(t, "acme/huge[1m]", get(d, []string{"env", "ANTHROPIC_DEFAULT_OPUS_MODEL"}).Data)
	require.Equal(t, "4096", get(d, []string{"env", "CLAUDE_CODE_MAX_OUTPUT_TOKENS"}).Data)
	require.Equal(t, d["model"], get(d, []string{"env", "ANTHROPIC_MODEL"}).Data)
	require.Equal(t, "acme/huge[1m]", get(d, []string{"env", "ANTHROPIC_MODEL"}).Data)

	setup(t, claudeCodeHarness{}, path, []Route{mid, huge}, Selection{Background: "acme/huge"})
	d = load(t, path)
	require.Equal(t, []any{"acme/mid", "acme/huge[1m]"}, d["availableModels"])
	require.Equal(t, "acme/huge[1m]", get(d, []string{"env", "ANTHROPIC_SMALL_FAST_MODEL"}).Data)
	require.Equal(t, d["model"], get(d, []string{"env", "ANTHROPIC_MODEL"}).Data)
}

func TestReasoningBoundsSkipNone(t *testing.T) {
	low, high := reasoningBounds([]string{"high", "minimal", "none"})
	require.Equal(t, "minimal", low)
	require.Equal(t, "high", high)
	low, high = reasoningBounds([]string{"none"})
	require.Nil(t, low)
	require.Nil(t, high)
}

func TestMaxEffortOnlyForClaudeCodeAndPi(t *testing.T) {
	r := testRoute("acme/primary", "Primary")
	r.ReasoningLevels = []string{"none", "low", "high", "xhigh", "max"}
	claude := settingsPath(t, claudeCodeHarness{})
	setup(t, claudeCodeHarness{}, claude, []Route{r}, Selection{})
	require.Equal(t, map[string]any{"acme/primary": map[string]any{"maxEffortLevel": "max"}}, load(t, claude)["modelSettings"])

	codex := settingsPath(t, codexHarness{})
	setup(t, codexHarness{}, codex, []Route{r}, Selection{})
	model := load(t, catalogPath(codex))["models"].([]any)[0].(map[string]any)
	efforts := []any{}
	for _, level := range model["supported_reasoning_levels"].([]any) {
		efforts = append(efforts, level.(map[string]any)["effort"])
	}
	require.Equal(t, []any{"none", "low", "high", "xhigh"}, efforts)
	require.Equal(t, "low", model["default_reasoning_level"])

	opencode := settingsPath(t, openCodeHarness{})
	setup(t, openCodeHarness{}, opencode, []Route{r}, Selection{})
	variants := get(load(t, opencode), []string{"provider", providerID, "models", "acme/primary", "variants"}).Data.(map[string]any)
	require.ElementsMatch(t, []string{"none", "low", "high", "xhigh"}, slices.Collect(maps.Keys(variants)))

	pi := settingsPath(t, piHarness{})
	setup(t, piHarness{}, pi, []Route{r}, Selection{})
	model = get(load(t, pi), piProviderPath).Data.(map[string]any)["models"].([]any)[0].(map[string]any)
	require.Equal(t, map[string]any{
		"off": "none", "minimal": nil, "low": "low", "medium": nil, "high": "high", "xhigh": "xhigh", "max": "max",
	}, model["thinkingLevelMap"])
}

func TestPiThinkingLevels(t *testing.T) {
	path := settingsPath(t, piHarness{})
	effort := testRoute("acme/effort", "Effort")
	effort.ReasoningLevels = []string{"low", "medium", "high"}
	off := testRoute("acme/off", "Off")
	off.ReasoningLevels = []string{"none"}
	setup(t, piHarness{}, path, []Route{effort, off, testRoute("acme/plain", "Plain")}, Selection{})
	models := get(load(t, path), piProviderPath).Data.(map[string]any)["models"].([]any)
	effortModel := models[0].(map[string]any)
	require.Equal(t, true, effortModel["reasoning"])
	require.Equal(t, map[string]any{
		"off": nil, "minimal": nil, "low": "low", "medium": "medium", "high": "high", "xhigh": nil, "max": nil,
	}, effortModel["thinkingLevelMap"], "a route without none can't turn reasoning off")
	require.Equal(t, true, get(effortModel, []string{"compat", "supportsReasoningEffort"}).Data)
	for _, m := range models[1:] {
		require.Equal(t, false, m.(map[string]any)["reasoning"], "none alone is not a reasoning level")
		require.NotContains(t, m, "thinkingLevelMap")
		require.Equal(t, false, get(m.(map[string]any), []string{"compat", "supportsReasoningEffort"}).Data)
	}
}

func TestValidateRouteRejectsUnknownReasoningLevels(t *testing.T) {
	r := testRoute("acme/turbo", "Turbo")
	r.ReasoningLevels = []string{"max"}
	require.NoError(t, validateRoute(r))
	r.ReasoningLevels = []string{"turbo"}
	require.ErrorContains(t, validateRoute(r), `route "acme/turbo" has an unsupported reasoning level`)
}

func TestValidateRouteRequiresLimits(t *testing.T) {
	r := testRoute("acme/primary", "Primary")
	r.OutputLimit = r.ContextWindow
	require.ErrorContains(t, validateRoute(r), "no usable context and output limits")
	r = testRoute("acme/primary", "Primary")
	r.Tools = false
	require.ErrorContains(t, validateRoute(r), "lacks tool support")
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

func TestPiCarriesCompleteRouteCost(t *testing.T) {
	cacheRead, cacheWrite := 0.3, 3.75
	priced := testRoute("acme/primary", "Primary")
	priced.Cost = &Cost{Input: 3, Output: 15, CacheRead: &cacheRead, CacheWrite: &cacheWrite,
		LongContext: &Cost{Input: 6, Output: 22.5, CacheRead: &cacheRead, CacheWrite: &cacheWrite}}
	partial := testRoute("acme/partial", "Partial")
	partial.Cost = &Cost{Input: 3, Output: 15, CacheRead: &cacheRead, LongContext: priced.Cost.LongContext}
	shortOnly := testRoute("acme/short", "Short")
	shortOnly.Cost = &Cost{Input: 3, Output: 15, CacheRead: &cacheRead, CacheWrite: &cacheWrite, LongContext: &Cost{Input: 6, Output: 22.5}}
	path := settingsPath(t, piHarness{})
	setup(t, piHarness{}, path, []Route{priced, partial, shortOnly, testRoute("acme/unpriced", "Unpriced")}, Selection{})
	models := get(load(t, path), piProviderPath).Data.(map[string]any)["models"].([]any)
	require.Equal(t, map[string]any{
		"input": json.Number("3"), "output": json.Number("15"), "cacheRead": json.Number("0.3"), "cacheWrite": json.Number("3.75"),
		"tiers": []any{map[string]any{
			"inputTokensAbove": json.Number("200000"),
			"input":            json.Number("6"), "output": json.Number("22.5"), "cacheRead": json.Number("0.3"), "cacheWrite": json.Number("3.75"),
		}},
	}, models[0].(map[string]any)["cost"])
	require.NotContains(t, models[1], "cost", "Pi requires every price")
	require.NotContains(t, models[2].(map[string]any)["cost"], "tiers", "an incomplete long-context price is dropped")
	require.NotContains(t, models[3], "cost")
}

func TestNewRoute(t *testing.T) {
	_, err := NewRoute(managementapi.Route{Name: "acme/bare"})
	require.ErrorContains(t, err, `route "acme/bare" has no model metadata`)

	ptr := func(v float32) *float32 { return &v }
	tools, ctx, out := true, 128000, 4096
	r, err := NewRoute(managementapi.Route{Name: "acme/primary", DisplayName: "Primary", Metadata: &managementapi.ExploreMetadata{
		ContextWindow: &ctx, MaxOutputTokens: &out, InputModalities: []string{"text"}, Tools: &tools,
		Cost: &managementapi.ExploreCost{Input: ptr(0.3), Output: ptr(1.2), LongContext: &managementapi.ExploreCostValues{Input: ptr(0.6)}},
	}})
	require.NoError(t, err)
	require.True(t, r.Messages && r.Responses, "a route without API formats is listed in every harness")
	require.Equal(t, &Cost{Input: 0.3, Output: 1.2}, r.Cost, "a long-context price without output is dropped")
}
