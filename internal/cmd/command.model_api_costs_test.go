package cmd_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/basetenlabs/baseten-cli/internal/cmd"
)

const modelAPICostsPath = "/v1/billing/model_apis"

// Matches ModelApisCostsResponseV1: UTC dates, decimal-string subtotals, and
// nullable dimensions, including a list-valued api_key_prefixes field.
func modelAPICostBucket(date string, results ...map[string]any) map[string]any {
	if results == nil {
		results = []map[string]any{}
	}
	return map[string]any{"date": date, "results": results}
}

func modelAPICostResult(model any, subtotal string) map[string]any {
	return map[string]any{
		"api_key_prefixes": nil, "user_id": nil, "model": model,
		"service_tier": nil, "subtotal": subtotal,
	}
}

func Test_ModelApi_Costs_TableAndExactTotals(t *testing.T) {
	h := NewCommandHarness(t)
	// Deliberately extreme values exercise precision beyond float64's mantissa.
	h.MockManagementAPI().SetRoute("GET", modelAPICostsPath, 200, modelAPIUsageBody(
		modelAPICostBucket("2026-09-01", modelAPICostResult("model-a", "9007199254740992.00000123"), modelAPICostResult("model-b", "0.00000001")),
		modelAPICostBucket("2026-09-02"),
		modelAPICostBucket("2026-09-03", modelAPICostResult("model-a", "-0.00000002")),
	))
	h.Require.NoError(h.Execute("model-api", "costs"))
	out := h.Stdout.String()
	h.Require.Contains(out, "SUBTOTAL (USD)")
	h.Require.Contains(out, "$9,007,199,254,740,992.00000123")
	h.Require.Contains(out, "$0.00000001")
	h.Require.Contains(out, "-$0.00000002")
	h.Require.Contains(out, "(no usage)")
	h.Require.Contains(out, "ALL")
	h.Require.Contains(out, "$9,007,199,254,740,992.00000122")
	h.Require.Contains(h.Stderr.String(), "2026-09-01 through 2026-09-03 UTC")
	h.Require.Contains(h.Stderr.String(), "may differ from finalized invoice")
}

func Test_ModelApi_Costs_DimensionsAndFilters(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	result := modelAPICostResult("model-a", "1.23")
	result["api_key_prefixes"] = []string{"key-a"}
	result["service_tier"] = "priority"
	m.SetRoute("GET", modelAPICostsPath, 200, modelAPIUsageBody(modelAPICostBucket("2026-09-01", result)))
	h.Require.NoError(h.Execute("model-api", "costs",
		"--group-by", "api-key", "--group-by", "user", "--group-by", "model", "--group-by", "service-tier", "--group-by", "user",
		"--api-key-prefix", "key-a", "--api-key-prefix", "key-b", "--user-id", "user-a", "--user-id", "user-b",
		"--model", "model-a", "--model", "model-b", "--service-tier", "priority", "--service-tier", "standard"))
	query := m.FindCall("GET", modelAPICostsPath).Query()
	h.Require.Equal([]string{"api_key_prefix", "user", "model", "service_tier"}, query["group_by"])
	h.Require.Equal([]string{"key-a", "key-b"}, query["api_key_prefixes"])
	h.Require.Equal([]string{"user-a", "user-b"}, query["user_ids"])
	h.Require.Equal([]string{"model-a", "model-b"}, query["models"])
	h.Require.Equal([]string{"priority", "standard"}, query["service_tiers"])
	h.Require.Contains(h.Stdout.String(), "key-a")
	h.Require.Contains(h.Stdout.String(), "priority")
	h.Require.Contains(h.Stderr.String(), "attribution is unavailable")
	h.Require.NotContains(h.Stdout.String(), "ALL")
}

func Test_ModelApi_Costs_UTCWindows(t *testing.T) {
	// The local date is September 10, while UTC is already September 11.
	now := time.Date(2026, 9, 10, 18, 30, 0, 0, time.FixedZone("local", -7*60*60))
	for _, tc := range []struct {
		name       string
		args       []string
		start, end string
	}{
		{"default", nil, "2026-09-05", "2026-09-12"},
		{"since", []string{"--since", "3d"}, "2026-09-09", "2026-09-12"},
		{"dates", []string{"--start", "2026-08-05", "--end", "2026-08-06"}, "2026-08-05", "2026-08-06"},
		{"start", []string{"--start", "2026-09-01"}, "2026-09-01", "2026-09-12"},
		{"end", []string{"--end", "2026-09-01"}, "2026-08-25", "2026-09-01"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := NewCommandHarness(t)
			h.Context = cmd.WithNow(h.Context, func() time.Time { return now })
			m := h.MockManagementAPI()
			m.SetRoute("GET", modelAPICostsPath, 200, modelAPIUsageBody())
			h.Require.NoError(h.Execute(append([]string{"model-api", "costs"}, tc.args...)...))
			q := m.FindCall("GET", modelAPICostsPath).Query()
			h.Require.Equal(tc.start, q.Get("start_date"))
			h.Require.Equal(tc.end, q.Get("end_date"))
			h.Require.Equal([]string{"model"}, q["group_by"])
			h.Require.Equal("31", q.Get("limit"))
		})
	}
}

func Test_ModelApi_Costs_InvalidFlags(t *testing.T) {
	for _, tc := range []struct {
		args    []string
		message string
	}{
		{[]string{"--since", "0"}, "positive whole number of days"},
		{[]string{"--since", "12h"}, "positive whole number of days"},
		{[]string{"--since", "-1d"}, "positive whole number of days"},
		{[]string{"--since", "91d"}, "at most 90 days"},
		{[]string{"--since", "7d", "--start", "2026-09-01"}, "cannot be combined"},
		{[]string{"--since", "7d", "--end", "2026-09-01"}, "cannot be combined"},
		{[]string{"--start", "2026-09-02", "--end", "2026-09-01"}, "earlier than"},
		{[]string{"--start", "2026-09-01", "--end", "2026-09-01"}, "earlier than"},
		{[]string{"--start", "2026-08-04", "--end", "2026-08-06"}, "not available before"},
		{[]string{"--start", "2026-09-01T12:00:00Z"}, "YYYY-MM-DD"},
		{[]string{"--end", "invalid"}, "YYYY-MM-DD"},
		{[]string{"--group-by", "invalid"}, "invalid --group-by"},
		{[]string{"--limit", "-1"}, "--limit must"},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			h := NewCommandHarness(t)
			h.MockManagementAPI().SetHandlerFallback(func(http.ResponseWriter, *http.Request) { t.Error("unexpected HTTP request") })
			err := h.Execute(append([]string{"model-api", "costs"}, tc.args...)...)
			h.Require.ErrorContains(err, tc.message)
			h.Require.Equal(2, h.ExitCode)
		})
	}
}

func Test_ModelApi_Costs_PagingAndOutputs(t *testing.T) {
	for _, output := range []string{"text", "json", "jsonl", "none"} {
		for _, limit := range []int{0, 1, 32} {
			t.Run(fmt.Sprintf("%s/limit-%d", output, limit), func(t *testing.T) {
				h := NewCommandHarness(t)
				m := h.MockManagementAPI()
				calls := 0
				m.SetRouteFunc("GET", modelAPICostsPath, func(w http.ResponseWriter, r *http.Request) {
					calls++
					first := r.URL.Query().Get("cursor") == ""
					if first {
						pageSize := 31
						if limit > 0 {
							pageSize = min(pageSize, limit)
						}
						h.Require.Equal(fmt.Sprint(pageSize), r.URL.Query().Get("limit"))
						buckets := make([]map[string]any, pageSize)
						for i := range buckets {
							date := time.Date(2026, 8, 6+i, 0, 0, 0, 0, time.UTC).Format(time.DateOnly)
							buckets[i] = modelAPICostBucket(date, modelAPICostResult(nil, "0.0000012300"))
						}
						body := modelAPIUsageBody(buckets...)
						body["pagination"] = map[string]any{"has_more": true, "cursor": "page-2"}
						_ = json.NewEncoder(w).Encode(body)
					} else {
						h.Require.Equal("cursor=page-2", r.URL.RawQuery)
						_ = json.NewEncoder(w).Encode(modelAPIUsageBody(
							modelAPICostBucket("2026-09-06"),
							modelAPICostBucket("2026-09-07", modelAPICostResult("model-a", "0.1")),
						))
					}
				})
				h.Require.NoError(h.Execute("model-api", "costs", "--start", "2026-08-06", "--end", "2026-09-08", "--output", output, "--limit", fmt.Sprint(limit)))
				wantCount := 33
				if limit > 0 {
					wantCount = limit
				}
				if limit == 1 {
					h.Require.Equal(1, calls)
				} else {
					h.Require.Equal(2, calls)
				}
				if limit > 0 {
					h.Require.Contains(h.Stderr.String(), "Reached the --limit")
				}
				switch output {
				case "json", "jsonl":
					var buckets []map[string]any
					if output == "json" {
						h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &buckets))
					} else {
						for _, line := range strings.Split(strings.TrimSpace(h.Stdout.String()), "\n") {
							var bucket map[string]any
							h.Require.NoError(json.Unmarshal([]byte(line), &bucket))
							buckets = append(buckets, bucket)
						}
					}
					h.Require.Len(buckets, wantCount)
					result := buckets[0]["results"].([]any)[0].(map[string]any)
					h.Require.Equal("0.0000012300", result["subtotal"])
					h.Require.Nil(result["api_key_prefixes"])
					h.Require.Nil(result["model"])
				case "none":
					h.Require.Empty(h.Stdout.String())
				case "text":
					h.Require.Contains(h.Stdout.String(), "$0.00000123")
				}
			})
		}
	}
}

func Test_ModelApi_Costs_JQ(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", modelAPICostsPath, 200, modelAPIUsageBody(modelAPICostBucket("2026-09-01", modelAPICostResult("model-a", "0.00000123"))))
	h.Require.NoError(h.Execute("model-api", "costs", "--jq", ".results[].subtotal"))
	h.Require.Contains(h.Stdout.String(), "0.00000123")
}

func Test_ModelApi_Costs_Empty(t *testing.T) {
	for _, buckets := range [][]map[string]any{nil, {modelAPICostBucket("2026-09-01")}} {
		t.Run(fmt.Sprint(len(buckets)), func(t *testing.T) {
			h := NewCommandHarness(t)
			h.MockManagementAPI().SetRoute("GET", modelAPICostsPath, 200, modelAPIUsageBody(buckets...))
			h.Require.NoError(h.Execute("model-api", "costs"))
			h.Require.Empty(h.Stdout.String())
			h.Require.Contains(h.Stderr.String(), "No usage in the selected window.")
		})
	}
}

func Test_ModelApi_Costs_Errors(t *testing.T) {
	t.Run("missing page fields", func(t *testing.T) {
		h := NewCommandHarness(t)
		h.MockManagementAPI().SetRoute("GET", modelAPICostsPath, 200, map[string]any{})
		h.Require.ErrorContains(h.Execute("model-api", "costs"), "missing items or pagination")
		h.Require.NotContains(h.Stderr.String(), "No usage")
	})
	t.Run("malformed JSON", func(t *testing.T) {
		h := NewCommandHarness(t)
		h.MockManagementAPI().SetRouteFunc("GET", modelAPICostsPath, func(w http.ResponseWriter, r *http.Request) {
			if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
				t.Errorf("Authorization = %q, want Bearer test-key", got)
			}
			_, _ = w.Write([]byte(`{"items":`))
		})
		h.Require.ErrorContains(h.Execute("model-api", "costs"), "decoding daily costs")
	})
	t.Run("failure on later page", func(t *testing.T) {
		h := NewCommandHarness(t)
		h.MockManagementAPI().SetRouteFunc("GET", modelAPICostsPath, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("cursor") != "" {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"message":"cost query failed"}`))
				return
			}
			body := modelAPIUsageBody(modelAPICostBucket("2026-09-01", modelAPICostResult("model-a", "0.123")))
			body["pagination"] = map[string]any{"has_more": true, "cursor": "next"}
			_ = json.NewEncoder(w).Encode(body)
		})
		h.Require.Error(h.Execute("model-api", "costs"))
		h.Require.Contains(h.Stderr.String(), "cost query failed")
		h.Require.Empty(h.Stdout.String())
	})
	t.Run("permission denied", func(t *testing.T) {
		h := NewCommandHarness(t)
		h.MockManagementAPI().SetRoute("GET", modelAPICostsPath, 403, map[string]any{"code": "PERMISSION_DENIED", "message": "Costs are not available for this organization."})
		h.Require.Error(h.Execute("model-api", "costs", "--output", "json"))
		h.Require.Contains(h.Stdout.String(), "PERMISSION_DENIED")
		h.Require.Contains(h.Stderr.String(), "Costs are not available")
	})
	t.Run("malformed decimal", func(t *testing.T) {
		h := NewCommandHarness(t)
		h.MockManagementAPI().SetRoute("GET", modelAPICostsPath, 200, modelAPIUsageBody(modelAPICostBucket("2026-09-01", modelAPICostResult("model-a", "1/2"))))
		h.Require.ErrorContains(h.Execute("model-api", "costs"), "invalid cost subtotal")
	})
	for _, cursor := range []any{nil, "", "repeated-cursor"} {
		t.Run(fmt.Sprint(cursor), func(t *testing.T) {
			h := NewCommandHarness(t)
			body := modelAPIUsageBody(modelAPICostBucket("2026-09-01"))
			body["pagination"] = map[string]any{"has_more": true, "cursor": cursor}
			h.MockManagementAPI().SetRoute("GET", modelAPICostsPath, 200, body)
			h.Require.ErrorContains(h.Execute("model-api", "costs"), "invalid pagination cursor")
		})
	}
}
