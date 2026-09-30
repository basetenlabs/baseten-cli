package cmd_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func modelAPIFixture(name, display, family string) map[string]any {
	return map[string]any{
		"name":                           name,
		"display_name":                   display,
		"description":                    "A test Model API.",
		"model_family":                   family,
		"release_date":                   "2025-01-01",
		"context_length":                 8192,
		"cost_per_million_input_tokens":  0.5,
		"cost_per_million_output_tokens": 1.5,
		"invoke_url":                     "https://inference.baseten.co",
		"rate_limits":                    []any{},
	}
}

func Test_ModelApi_List_Empty(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/model_apis", 200,
		map[string]any{"items": []any{}, "pagination": map[string]any{"has_more": false}})

	h.Require.NoError(h.Execute("model-api", "list"))
	h.Require.Equal("", h.Stdout.String())
	h.Require.Contains(h.Stderr.String(), "No Model APIs found.")
}

func Test_ModelApi_List_Rows(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/model_apis", 200, map[string]any{
		"items":      []any{modelAPIFixture("llama-3", "Llama 3", "llama")},
		"pagination": map[string]any{"has_more": false},
	})

	h.Require.NoError(h.Execute("model-api", "list"))
	out := h.Stdout.String()
	h.Require.Contains(out, "NAME")
	h.Require.Contains(out, "CONTEXT")
	h.Require.Contains(out, "$/1M IN")
	h.Require.Contains(out, "$/1M OUT")
	h.Require.Contains(out, "RELEASED")
	h.Require.Contains(out, "llama-3")
	h.Require.Contains(out, "8192")
	h.Require.Contains(out, "0.5")
	h.Require.Contains(out, "1.5")
	h.Require.Contains(out, "2025-01-01")
	// Display name and family were dropped from the list table.
	h.Require.NotContains(out, "DISPLAY NAME")
	h.Require.NotContains(out, "Llama 3")
	// The added flag column was replaced by RELEASED.
	h.Require.NotContains(out, "ADDED")
}

func Test_ModelApi_List_SortsUsedThenAddedThenCatalog(t *testing.T) {
	usedNewest := modelAPIFixture("used-newest", "Used Newest", "fam")
	usedNewest["org_details"] = map[string]any{
		"added_at": "2025-01-01T00:00:00Z", "last_used_at": "2026-05-01T00:00:00Z",
	}
	usedOlder := modelAPIFixture("used-older", "Used Older", "fam")
	usedOlder["org_details"] = map[string]any{
		"added_at": "2025-01-01T00:00:00Z", "last_used_at": "2026-01-01T00:00:00Z",
	}
	addedUnused := modelAPIFixture("added-never-used", "Added Never Used", "fam")
	addedUnused["org_details"] = map[string]any{"added_at": "2025-01-01T00:00:00Z"}
	catalogNew := modelAPIFixture("catalog-new", "Catalog New", "fam")
	catalogNew["release_date"] = "2026-03-01"
	catalogOld := modelAPIFixture("catalog-old", "Catalog Old", "fam")
	catalogOld["release_date"] = "2024-03-01"

	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/model_apis", 200, map[string]any{
		"items":      []any{catalogOld, addedUnused, catalogNew, usedOlder, usedNewest},
		"pagination": map[string]any{"has_more": false},
	})

	h.Require.NoError(h.Execute("model-api", "list"))
	out := h.Stdout.String()
	want := []string{"used-newest", "used-older", "added-never-used", "catalog-new", "catalog-old"}
	positions := make([]int, len(want))
	for i, name := range want {
		positions[i] = strings.Index(out, name)
		h.Require.NotEqual(-1, positions[i], "%s missing from table", name)
	}
	h.Require.IsIncreasing(positions)
}

func Test_ModelApi_List_JSONKeepsBackendOrder(t *testing.T) {
	usedNewest := modelAPIFixture("used-newest", "Used Newest", "fam")
	usedNewest["org_details"] = map[string]any{
		"added_at": "2025-01-01T00:00:00Z", "last_used_at": "2026-05-01T00:00:00Z",
	}
	catalogOld := modelAPIFixture("catalog-old", "Catalog Old", "fam")

	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/model_apis", 200, map[string]any{
		"items":      []any{catalogOld, usedNewest},
		"pagination": map[string]any{"has_more": false},
	})

	// Sorting is presentation only, so JSON stays in the order the backend sent.
	h.Require.NoError(h.Execute("model-api", "list", "--output", "json"))
	out := h.Stdout.String()
	h.Require.Less(strings.Index(out, "catalog-old"), strings.Index(out, "used-newest"))
}

func Test_ModelApi_List_JSON(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/model_apis", 200, map[string]any{
		"items":      []any{modelAPIFixture("llama-3", "Llama 3", "llama")},
		"pagination": map[string]any{"has_more": false},
	})

	h.Require.NoError(h.Execute("model-api", "list", "--output", "json"))
	h.Require.Contains(h.Stdout.String(), `"name": "llama-3"`)
}

func Test_ModelApi_List_DefaultBrowsesCatalog(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	var query []string
	m.SetRouteFunc("GET", "/v1/model_apis", func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.Query()["added_only"]
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{}, "pagination": map[string]any{"has_more": false}})
	})

	h.Require.NoError(h.Execute("model-api", "list"))
	h.Require.Equal([]string{"false"}, query)
}

func Test_ModelApi_List_AddedOnlyRestrictsToAdded(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	var query []string
	m.SetRouteFunc("GET", "/v1/model_apis", func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.Query()["added_only"]
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{}, "pagination": map[string]any{"has_more": false}})
	})

	h.Require.NoError(h.Execute("model-api", "list", "--added-only"))
	h.Require.Equal([]string{"true"}, query)
}

func Test_ModelApi_List_Paginates(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRouteFunc("GET", "/v1/model_apis", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("cursor") == "" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"items":      []any{modelAPIFixture("a", "A", "fam")},
				"pagination": map[string]any{"has_more": true, "cursor": "page2"},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"items":      []any{modelAPIFixture("b", "B", "fam")},
			"pagination": map[string]any{"has_more": false},
		})
	})

	h.Require.NoError(h.Execute("model-api", "list"))
	out := h.Stdout.String()
	h.Require.Contains(out, "a")
	h.Require.Contains(out, "b")
}

func Test_ModelApi_Describe_Text(t *testing.T) {
	h := NewCommandHarness(t)
	fixture := modelAPIFixture("llama-3", "Llama 3", "llama")
	fixture["rate_limits"] = []any{map[string]any{"threshold": 100, "type": "requests", "unit": "minute"}}
	h.MockManagementAPI().SetRoute("GET", "/v1/model_apis/llama-3", 200, fixture)

	h.Require.NoError(h.Execute("model-api", "describe", "--model", "llama-3"))
	out := h.Stdout.String()
	h.Require.Contains(out, "Name:")
	h.Require.Contains(out, "llama-3")
	h.Require.Contains(out, "Context Length:")
	h.Require.Contains(out, "8192")
	h.Require.Contains(out, "$0.5 / 1M tokens")
	h.Require.Contains(out, "100 per minute (requests)")
}

func Test_ModelApi_Describe_TextOrgDetails(t *testing.T) {
	h := NewCommandHarness(t)
	fixture := modelAPIFixture("llama-3", "Llama 3", "llama")
	fixture["org_details"] = map[string]any{
		"added_at": "2025-02-03T04:05:06Z", "last_used_at": "2026-02-03T04:05:06Z",
	}
	h.MockManagementAPI().SetRoute("GET", "/v1/model_apis/llama-3", 200, fixture)

	h.Require.NoError(h.Execute("model-api", "describe", "--model", "llama-3"))
	out := h.Stdout.String()
	// added_at is when the workspace first invoked the Model API, so it reads as
	// first use rather than an explicit add.
	h.Require.Contains(out, "First Used:      2025-02-03T04:05:06Z")
	h.Require.Contains(out, "Last Used:       2026-02-03T04:05:06Z")
	h.Require.NotContains(out, "Added:")
}

func Test_ModelApi_Describe_JSON(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/model_apis/llama-3", 200,
		modelAPIFixture("llama-3", "Llama 3", "llama"))

	h.Require.NoError(h.Execute("model-api", "describe", "--model", "llama-3", "--output", "json"))
	h.Require.Contains(h.Stdout.String(), `"name": "llama-3"`)
}

func Test_ModelApi_Predict_Content(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("POST", "/v1/chat/completions", 200, map[string]any{
		"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "hello there"}}},
	})

	err := h.Execute("model-api", "predict", "--url", m.URL+"/v1/chat/completions",
		"--model", "llama-3", "--content", "hi")
	h.Require.NoError(err)
	h.Require.Equal("hello there\n", h.Stdout.String())

	call := m.FindCall("POST", "/v1/chat/completions")
	h.Require.NotNil(call)
	body := call.BodyJSON(t)
	h.Require.Equal("llama-3", body["model"])
}

func Test_ModelApi_Predict_ContentJSONFullEnvelope(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("POST", "/v1/chat/completions", 200, map[string]any{
		"choices": []any{map[string]any{"message": map[string]any{"content": "hi"}}},
	})

	err := h.Execute("model-api", "predict", "--url", m.URL+"/v1/chat/completions",
		"--model", "llama-3", "--content", "hi", "--output", "json")
	h.Require.NoError(err)
	h.Require.Contains(h.Stdout.String(), `"choices"`)
}

func Test_ModelApi_Predict_ContentRequiresModel(t *testing.T) {
	h := NewCommandHarness(t)
	err := h.Execute("model-api", "predict", "--content", "hi")
	h.Require.ErrorContains(err, "--content requires --model")
}

func Test_ModelApi_Predict_DataVerbatim(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("POST", "/v1/chat/completions", 200, map[string]any{"ok": true})

	err := h.Execute("model-api", "predict", "--url", m.URL+"/v1/chat/completions",
		"--data", `{"model":"x","messages":[]}`)
	h.Require.NoError(err)

	call := m.FindCall("POST", "/v1/chat/completions")
	h.Require.NotNil(call)
	h.Require.Equal(`{"model":"x","messages":[]}`, call.Body)
	h.Require.Contains(h.Stdout.String(), `"ok"`)
}

func Test_ModelApi_Predict_InputRequired(t *testing.T) {
	h := NewCommandHarness(t)
	err := h.Execute("model-api", "predict", "--model", "llama-3")
	h.Require.Error(err)
}

func Test_ModelApi_Predict_InputExclusive(t *testing.T) {
	h := NewCommandHarness(t)
	err := h.Execute("model-api", "predict", "--model", "llama-3", "--content", "hi", "--data", "{}")
	h.Require.Error(err)
}
