package cmd_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/basetenlabs/baseten-cli/internal/cmd"
)

func harnessUsageResult(dims map[string]any, cost any, requests, input, cached, output int) map[string]any {
	r := map[string]any{
		"cost_usd":              cost,
		"request_count":         requests,
		"input_tokens":          input,
		"cached_input_tokens":   cached,
		"uncached_input_tokens": input - cached,
		"output_tokens":         output,
	}
	for k, v := range dims {
		r[k] = v
	}
	return r
}

func harnessUsageBody(hasMore bool, cursor any, buckets ...map[string]any) map[string]any {
	items := make([]any, 0, len(buckets))
	for _, b := range buckets {
		items = append(items, b)
	}
	return map[string]any{"items": items, "pagination": map[string]any{"has_more": hasMore, "cursor": cursor}}
}

func harnessUsageBucket(date string, results ...map[string]any) map[string]any {
	items := make([]any, 0, len(results))
	for _, r := range results {
		items = append(items, r)
	}
	return map[string]any{"date": date, "results": items}
}

var (
	usageSonnet = map[string]any{"model": "claude-sonnet-5", "provider": "ANTHROPIC"}
	usageGpt    = map[string]any{"model": "gpt-5.2", "provider": "OPENAI"}
	usageCustom = map[string]any{"model": "qwen-custom", "provider": "OPENAI_COMPATIBLE"}
)

func newHarnessUsageHarness(t *testing.T, now time.Time, body map[string]any) (*CommandHarness, *MockManagementAPI) {
	h := NewCommandHarness(t)
	h.Context = cmd.WithNow(h.Context, func() time.Time { return now })
	api := h.MockManagementAPI()
	api.SetRoute("GET", "/v1/users/me", 200, map[string]string{"user_id": "user-a"})
	api.SetRoute("GET", "/v1/routes/usage", 200, body)
	return h, api
}

func harnessUsageMonth() map[string]any {
	return harnessUsageBody(false, nil,
		harnessUsageBucket("2026-09-01",
			harnessUsageResult(usageSonnet, "1.25", 10, 1000, 800, 100),
			harnessUsageResult(usageGpt, "0.50", 4, 400, 0, 40),
		),
		harnessUsageBucket("2026-09-02"),
		harnessUsageBucket("2026-09-03",
			harnessUsageResult(usageSonnet, "0.0000000001", 1, 100, 0, 10),
			harnessUsageResult(usageCustom, nil, 2, 50, 0, 5),
		),
	)
}

func Test_Harness_Usage_QueriesCurrentMonthForCurrentUser(t *testing.T) {
	h, api := newHarnessUsageHarness(t, time.Date(2026, 9, 25, 15, 0, 0, 0, time.UTC), harnessUsageMonth())

	h.Require.NoError(h.Execute("harness", "usage"))
	q := api.FindCall("GET", "/v1/routes/usage").Query()
	h.Require.Equal("2026-09-01", q.Get("start_date"))
	h.Require.Equal("2026-09-26", q.Get("end_date"), "the current month includes today")
	h.Require.Equal([]string{"user-a"}, q["user_ids"], "admins must not see other users' usage")
	h.Require.Equal([]string{"MODEL", "PROVIDER"}, q["group_by"])
	h.Require.Equal("31", q.Get("limit"))
	h.Require.Contains(h.Stderr.String(), "Harness usage for September 2026 (UTC, through Sep 25)")
}

func Test_Harness_Usage_PastMonthSpansWholeMonth(t *testing.T) {
	h, api := newHarnessUsageHarness(t, time.Date(2027, 1, 10, 0, 0, 0, 0, time.UTC), harnessUsageMonth())

	h.Require.NoError(h.Execute("harness", "usage", "--month", "2026-12", "--group-by", "route"))
	q := api.FindCall("GET", "/v1/routes/usage").Query()
	h.Require.Equal("2026-12-01", q.Get("start_date"))
	h.Require.Equal("2027-01-01", q.Get("end_date"))
	h.Require.Equal([]string{"ROUTE"}, q["group_by"])
	h.Require.Contains(h.Stderr.String(), "Harness usage for December 2026 (UTC)\n")
}

func Test_Harness_Usage_RejectsBadMonth(t *testing.T) {
	h, _ := newHarnessUsageHarness(t, time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC), harnessUsageMonth())

	h.Require.ErrorContains(h.Execute("harness", "usage", "--month", "2026-9-1"), "use YYYY-MM")
	h.Require.ErrorContains(h.Execute("harness", "usage", "--month", "2026-10"), "in the future")
}

func Test_Harness_Usage_JSONSumsBucketsExactly(t *testing.T) {
	h, _ := newHarnessUsageHarness(t, time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC), harnessUsageMonth())

	h.Require.NoError(h.Execute("harness", "usage", "--output", "json"))
	var got struct {
		Month  string `json:"month"`
		Totals struct {
			CostUSD      string `json:"cost_usd"`
			CostComplete bool   `json:"cost_complete"`
			RequestCount int    `json:"request_count"`
			Cached       int    `json:"cached_input_tokens"`
		} `json:"totals"`
		Items []struct {
			Model        string  `json:"model"`
			Provider     string  `json:"provider"`
			CostUSD      *string `json:"cost_usd"`
			RequestCount int     `json:"request_count"`
		} `json:"items"`
	}
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &got))
	h.Require.Equal("2026-09", got.Month)
	h.Require.Equal("1.7500000001", got.Totals.CostUSD, "costs are summed as exact decimals")
	h.Require.False(got.Totals.CostComplete)
	h.Require.Equal(17, got.Totals.RequestCount)
	h.Require.Equal(800, got.Totals.Cached)
	h.Require.Len(got.Items, 3)
	h.Require.Equal("claude-sonnet-5", got.Items[0].Model, "most expensive first")
	h.Require.Equal("ANTHROPIC", got.Items[0].Provider)
	h.Require.Equal("1.2500000001", *got.Items[0].CostUSD)
	h.Require.Equal(11, got.Items[0].RequestCount)
	h.Require.Equal("qwen-custom", got.Items[2].Model, "unpriced items sort last")
	h.Require.Nil(got.Items[2].CostUSD)
}

func Test_Harness_Usage_Table(t *testing.T) {
	h, _ := newHarnessUsageHarness(t, time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC), harnessUsageMonth())

	h.Require.NoError(h.Execute("harness", "usage"))
	out := h.Stdout.String()
	h.Require.Contains(out, "Spend     $1.75 (some usage could not be priced and is not included)")
	h.Require.Contains(out, "Requests  17")
	h.Require.Contains(out, "Tokens    1.6K input (800 cached), 155 output")
	h.Require.Contains(out, "MODEL")
	h.Require.Contains(out, "PROVIDER")
	h.Require.Contains(out, "COST")
	h.Require.Equal("anthropic", modelAPIUsageCell(h, out, "claude-sonnet-5", 1))
	h.Require.Equal("$1.25", modelAPIUsageCell(h, out, "claude-sonnet-5", 6))
	h.Require.Equal("openai-compatible", modelAPIUsageCell(h, out, "qwen-custom", 1))
	h.Require.Equal("-", modelAPIUsageCell(h, out, "qwen-custom", 6))
}

func Test_Harness_Usage_TinyCostStaysVisible(t *testing.T) {
	body := harnessUsageBody(false, nil, harnessUsageBucket("2026-09-01", harnessUsageResult(usageGpt, "0.0012", 1, 10, 0, 1)))
	h, _ := newHarnessUsageHarness(t, time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC), body)

	h.Require.NoError(h.Execute("harness", "usage"))
	h.Require.Contains(h.Stdout.String(), "Spend     <$0.01\n")
}

func Test_Harness_Usage_Empty(t *testing.T) {
	body := harnessUsageBody(false, nil, harnessUsageBucket("2026-09-01"))
	h, _ := newHarnessUsageHarness(t, time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC), body)

	h.Require.NoError(h.Execute("harness", "usage"))
	h.Require.Empty(h.Stdout.String())
	h.Require.Contains(h.Stderr.String(), "No harness usage in September 2026.")

	h.Require.NoError(h.Execute("harness", "usage", "--jq", ".totals.cost_usd"))
	h.Require.Equal("\"0\"\n", h.Stdout.String())
}

func Test_Harness_Usage_FollowsCursor(t *testing.T) {
	h, api := newHarnessUsageHarness(t, time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC), nil)
	api.SetRouteFunc("GET", "/v1/routes/usage", func(w http.ResponseWriter, r *http.Request) {
		body := harnessUsageBody(true, "next", harnessUsageBucket("2026-09-01", harnessUsageResult(usageGpt, "1", 1, 10, 0, 1)))
		if r.URL.Query().Get("cursor") == "next" {
			body = harnessUsageBody(false, nil, harnessUsageBucket("2026-09-02", harnessUsageResult(usageGpt, "2", 2, 20, 0, 2)))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	})

	h.Require.NoError(h.Execute("harness", "usage", "--jq", ".totals.cost_usd"))
	h.Require.Equal("\"3\"\n", h.Stdout.String())
}

func Test_Harness_Usage_CompactTokenCounts(t *testing.T) {
	for _, tc := range []struct {
		input int
		want  string
	}{
		{950, "950"},
		{1000, "1.0K"},
		{62219, "62.2K"},
		{999960, "1.0M"},
		{2090689, "2.1M"},
		{1234567890, "1.2B"},
	} {
		body := harnessUsageBody(false, nil, harnessUsageBucket("2026-09-01", harnessUsageResult(usageGpt, "1", 1, tc.input, 0, 0)))
		h, _ := newHarnessUsageHarness(t, time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC), body)
		h.Require.NoError(h.Execute("harness", "usage"))
		h.Require.Contains(h.Stdout.String(), "Tokens    "+tc.want+" input", "%d tokens", tc.input)
	}
}

func Test_Harness_Usage_RouteDisplayNames(t *testing.T) {
	body := harnessUsageBody(false, nil, harnessUsageBucket("2026-09-01",
		harnessUsageResult(map[string]any{"route_id": "r1", "route_name": "salib/code-2qjd24q-anthropic-0059"}, "2", 2, 10, 0, 1),
		harnessUsageResult(map[string]any{"route_id": "gone", "route_name": "salib/deleted-route"}, "1", 1, 10, 0, 1),
	))
	h, api := newHarnessUsageHarness(t, time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC), body)
	route := routeFixture(map[string]any{"type": "ANTHROPIC", "model": "claude-opus-5-5"})
	route["id"], route["display_name"] = "r1", "Claude Opus 5.5"
	api.SetRoute("GET", "/v1/routes", 200, routePage([]any{route}, nil))

	h.Require.NoError(h.Execute("harness", "usage", "--group-by", "route"))
	out := h.Stdout.String()
	h.Require.Contains(out, "Claude Opus 5.5")
	h.Require.NotContains(out, "salib/code-2qjd24q-anthropic-0059")
	h.Require.Contains(out, "salib/deleted-route", "deleted routes fall back to their name")
	h.Require.Empty(api.FindCall("GET", "/v1/routes").Query().Get("team_id"), "routes from every team")

	h.Require.NoError(h.Execute("harness", "usage", "--group-by", "route", "--jq", "[.items[].route_display_name]"))
	h.Require.JSONEq(`["Claude Opus 5.5", null]`, h.Stdout.String())
}

func Test_Harness_Usage_ModelGroupingSkipsRouteLookup(t *testing.T) {
	h, api := newHarnessUsageHarness(t, time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC), harnessUsageMonth())

	h.Require.NoError(h.Execute("harness", "usage"))
	h.Require.Nil(api.FindCall("GET", "/v1/routes"))
}
