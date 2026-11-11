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
	require.Equal(t, true, model["supports_search_tool"])
	require.Equal(t, json.Number("128000"), model["context_window"])
	require.Equal(t, []any{"text"}, model["input_modalities"])
}

func TestClaudeWideContextRoutesMintWindowAliases(t *testing.T) {
	huge := testRoute("acme/huge", "Huge")
	huge.ContextWindow = 1000000
	huge.ReasoningLevels = []string{"none", "low", "high", "xhigh"}
	mid := testRoute("acme/mid", "Mid")
	mid.ContextWindow = 200000
	path := settingsPath(t, claudeHarness{})
	setup(t, claudeHarness{}, path, []Route{huge, mid}, Selection{})
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

	setup(t, claudeHarness{}, path, []Route{mid, huge}, Selection{Background: "acme/huge"})
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
	claude := settingsPath(t, claudeHarness{})
	setup(t, claudeHarness{}, claude, []Route{r}, Selection{})
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

func TestPiAdaptiveThinking(t *testing.T) {
	for _, tc := range []struct {
		model    string
		adaptive bool
	}{
		{"claude-fable-5-1", true},
		{"claude-mythos-5-1", true},
		{"claude-fable-5", true},
		{"claude-mythos-5", true},
		{"claude-mythos-preview", true},
		{"claude-opus-5-5", true},
		{"claude-opus-5", true},
		{"claude-opus-4-8", true},
		{"claude-opus-4-7", true},
		{"claude-sonnet-5-5", true},
		{"claude-sonnet-5", true},
		{"claude-haiku-5-5", true},
		{"claude-opus-4-6", true},
		{"claude-sonnet-4-6", true},
		{"claude-opus-4-5", false},
		{"claude-haiku-4-5", false},
		{"claude-sonnet-4-5", false},
		{"claude-opus-4-5-20251101", false},
		{"claude-haiku-4-5-20251001", false},
		{"claude-sonnet-4-5-20250929", false},
		{"anthropic.claude-opus-4-5-20251101-v1:0", false},
		{"anthropic.claude-haiku-4-5-20251001-v1:0", false},
		{"anthropic.claude-sonnet-4-5-20250929-v1:0", false},
		{"claude-opus-4-5@20251101", false},
		{"claude-haiku-4-5@20251001", false},
		{"claude-sonnet-4-5@20250929", false},
	} {
		t.Run(tc.model, func(t *testing.T) {
			require.Equal(t, tc.adaptive, piAdaptiveThinking(tc.model))
		})
	}
	t.Run("latest suffix normalization", func(t *testing.T) {
		require.False(t, piAdaptiveThinking("claude-opus-4-5-latest"))
	})
	t.Run("unknown models default to adaptive", func(t *testing.T) {
		for _, model := range []string{"", "claude-opus-99-1"} {
			require.True(t, piAdaptiveThinking(model), model)
		}
	})
}

func TestPiAnthropicThinkingCompatibility(t *testing.T) {
	for _, tc := range []struct {
		model           string
		contextWindow   int
		outputLimit     int
		reasoningLevels []string
		adaptive        bool
	}{
		{"claude-opus-4-5", 200000, 64000, []string{"low", "medium", "high"}, false},
		{"claude-opus-4-5-20251101", 200000, 64000, []string{"low", "medium", "high"}, false},
		{"claude-opus-5-5", 1000000, 128000, []string{"low", "medium", "high", "xhigh", "max"}, true},
	} {
		t.Run(tc.model, func(t *testing.T) {
			tools := true
			api := managementapi.GridRoute{
				Name: "acme/custom", DisplayName: "Custom",
				Metadata: managementapi.GridRouteMetadata{
					ContextWindow: &tc.contextWindow, MaxOutputTokens: &tc.outputLimit,
					InputModalities: []string{"text", "image"}, Tools: &tools,
					ReasoningEffortLevels: &tc.reasoningLevels,
					SupportedApiFormats:   []managementapi.GridRouteMetadataSupportedApiFormats{managementapi.GridRouteMetadataSupportedApiFormats_messages},
				},
			}
			require.NoError(t, json.Unmarshal([]byte(`{"type":"CONNECTION","connection_id":"conn-a","model":"`+tc.model+`"}`), &api.Target))
			route, err := NewRoute(api, Providers{"conn-a": ProviderAnthropic})
			require.NoError(t, err)
			path := settingsPath(t, piHarness{})
			setup(t, piHarness{}, path, []Route{route}, Selection{})
			models := get(load(t, path), piProviderPath).Data.(map[string]any)["models"].([]any)
			model := models[0].(map[string]any)
			require.Equal(t, "acme/custom", model["id"])
			require.Equal(t, "anthropic-messages", model["api"])
			require.Equal(t, true, model["reasoning"])
			require.Equal(t, "high", get(model, []string{"thinkingLevelMap", "high"}).Data)
			if tc.adaptive {
				require.Equal(t, true, get(model, []string{"compat", "forceAdaptiveThinking"}).Data)
			} else {
				require.NotContains(t, model, "compat", "legacy models must retain Pi's budget-based thinking")
			}
		})
	}
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
	r.OutputLimit = 0
	require.ErrorContains(t, validateRoute(r), "no usable context and output limits")
	r = testRoute("acme/primary", "Primary")
	r.OutputLimit = r.ContextWindow
	require.NoError(t, validateRoute(r))
	r.OutputLimit = r.ContextWindow + 1
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

func TestOpenCodeV2CostAndVariants(t *testing.T) {
	cacheRead, cacheWrite := 0.3, 3.75
	priced := testRoute("acme/primary", "Primary")
	priced.ReasoningLevels = []string{"none", "low", "high", "xhigh", "max"}
	priced.Cost = &Cost{Input: 3, Output: 15, CacheRead: &cacheRead, CacheWrite: &cacheWrite, LongContext: &Cost{Input: 6, Output: 22.5}}
	path := settingsPath(t, openCodeHarness{})
	save(t, path, map[string]any{"providers": map[string]any{}})
	setup(t, openCodeHarness{}, path, []Route{priced, testRoute("acme/unpriced", "Unpriced")}, Selection{})
	models := get(load(t, path), []string{"providers", providerID, "models"}).Data.(map[string]any)
	primary := models["acme/primary"].(map[string]any)
	require.Equal(t, []any{
		map[string]any{"input": json.Number("3"), "output": json.Number("15"), "cache": map[string]any{"read": json.Number("0.3"), "write": json.Number("3.75")}},
		map[string]any{"tier": map[string]any{"type": "context", "size": json.Number("200000")}, "input": json.Number("6"), "output": json.Number("22.5")},
	}, primary["cost"])
	ids := []any{}
	for _, v := range primary["variants"].([]any) {
		ids = append(ids, v.(map[string]any)["id"])
	}
	require.Equal(t, []any{"none", "low", "high", "xhigh"}, ids, "max becomes xhigh, in route order")
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
	_, err := NewRoute(managementapi.GridRoute{Name: "acme/bare"}, nil)
	require.ErrorContains(t, err, `route "acme/bare" has no supported API formats`)

	ptr := func(v float32) *float32 { return &v }
	tools, ctx, out := true, 128000, 4096
	r, err := NewRoute(managementapi.GridRoute{Name: "acme/primary", DisplayName: "Primary", Metadata: managementapi.GridRouteMetadata{
		ContextWindow: &ctx, MaxOutputTokens: &out, InputModalities: []string{"text"}, Tools: &tools, SupportedApiFormats: chatCompletionsFormats,
		Cost: &managementapi.ExploreCost{Input: ptr(0.3), Output: ptr(1.2), LongContext: &managementapi.ExploreCostValues{Input: ptr(0.6)}},
	}}, nil)
	require.NoError(t, err)
	require.Equal(t, &Cost{Input: 0.3, Output: 1.2}, r.Cost, "a long-context price without output is dropped")
}

func TestRouteTargetComesFromConnections(t *testing.T) {
	connection := func(id, provider string) managementapi.GridConnection {
		c := managementapi.GridConnection{Id: id}
		require.NoError(t, json.Unmarshal([]byte(`{"provider": "`+provider+`", "secret_id": "s", "secret_name": "key"}`), &c.Config))
		return c
	}
	providers := NewProviders([]managementapi.GridConnection{connection("conn-a", "ANTHROPIC"), connection("conn-o", "OPENAI"), connection("conn-x", "XAI")})
	require.Equal(t, Providers{"conn-a": ProviderAnthropic, "conn-o": ProviderOpenAI, "conn-x": "XAI"}, providers)

	tools, ctx, out := true, 128000, 4096
	metadata := managementapi.GridRouteMetadata{ContextWindow: &ctx, MaxOutputTokens: &out, InputModalities: []string{"text"}, Tools: &tools, SupportedApiFormats: chatCompletionsFormats}
	for _, tc := range []struct {
		name         string
		target       string
		wantProvider string
		wantModel    string
	}{
		{"anthropic connection", `{"type": "CONNECTION", "connection_id": "conn-a", "model": "claude"}`, ProviderAnthropic, "claude"},
		{"openai connection", `{"type": "CONNECTION", "connection_id": "conn-o", "model": "gpt"}`, ProviderOpenAI, "gpt"},
		{"xai connection", `{"type": "CONNECTION", "connection_id": "conn-x", "model": "grok"}`, "XAI", "grok"},
		{"unknown connection", `{"type": "CONNECTION", "connection_id": "conn-gone", "model": "gpt"}`, "", "gpt"},
		{"baseten model api", `{"type": "BASETEN_MODEL_API", "model": "deepseek"}`, ProviderBaseten, ""},
		{"intelligent", `{"type": "INTELLIGENT"}`, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api := managementapi.GridRoute{Name: "acme/route", DisplayName: "Route", Metadata: metadata}
			require.NoError(t, json.Unmarshal([]byte(tc.target), &api.Target))
			r, err := NewRoute(api, providers)
			require.NoError(t, err)
			require.Equal(t, tc.wantProvider, r.Provider)
			require.Equal(t, tc.wantModel, r.UpstreamModel)
		})
	}
}
