package cmd_test

import (
	"encoding/json"
	"net/http"
	"strings"
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
	usageSonnet = map[string]any{"route_id": "r-sonnet", "route_name": "salib/anthropic-claude-sonnet-5-2qjd24q"}
	usageGpt    = map[string]any{"route_id": "r-gpt", "route_name": "salib/openai-gpt-5-2-2qjd24q"}
	usageCustom = map[string]any{"route_id": "r-custom", "route_name": "salib/qwen-custom-2qjd24q"}
)

// harnessUsageRoute is a live route from /v1/routes.
func harnessUsageRoute(id, displayName string) map[string]any {
	r := routeFixture(map[string]any{"type": "ANTHROPIC", "model": "claude-sonnet-5"})
	r["id"], r["display_name"] = id, displayName
	return r
}

func newHarnessUsageHarness(t *testing.T, now time.Time, body map[string]any) (*CommandHarness, *MockManagementAPI) {
	h := NewCommandHarness(t)
	h.Context = cmd.WithNow(h.Context, func() time.Time { return now })
	api := h.MockManagementAPI()
	api.SetRoute("GET", "/v1/users/me", 200, map[string]string{"user_id": "user-a"})
	api.SetRoute("GET", "/v1/routes/usage", 200, body)
	api.SetRoute("GET", "/v1/routes", 200, routePage([]any{
		harnessUsageRoute("r-sonnet", "Claude Sonnet 5"),
		harnessUsageRoute("r-gpt", "GPT-5.2"),
		harnessUsageRoute("r-custom", "Qwen Custom"),
	}, nil))
	return h, api
}

// harnessUsageRow returns the fields of the first output line containing
// marker, so tests can index columns from the end past multi-word names.
func harnessUsageRow(h *CommandHarness, out, marker string) []string {
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, marker) {
			return strings.Fields(line)
		}
	}
	h.Require.Fail("no output line contains " + marker)
	return nil
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
	h.Require.Equal([]string{"ROUTE"}, q["group_by"])
	h.Require.Equal("31", q.Get("limit"))
	h.Require.Contains(h.Stderr.String(), "Harness usage for September 2026 (UTC, through Sep 25)")
}

func Test_Harness_Usage_PastMonthSpansWholeMonth(t *testing.T) {
	h, api := newHarnessUsageHarness(t, time.Date(2027, 1, 10, 0, 0, 0, 0, time.UTC), harnessUsageMonth())

	h.Require.NoError(h.Execute("harness", "usage", "--month", "2026-12"))
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
			RouteID          string  `json:"route_id"`
			RouteName        string  `json:"route_name"`
			RouteDisplayName string  `json:"route_display_name"`
			CostUSD          *string `json:"cost_usd"`
			RequestCount     int     `json:"request_count"`
		} `json:"items"`
	}
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &got))
	h.Require.Equal("2026-09", got.Month)
	h.Require.Equal("1.7500000001", got.Totals.CostUSD, "costs are summed as exact decimals")
	h.Require.False(got.Totals.CostComplete)
	h.Require.Equal(17, got.Totals.RequestCount)
	h.Require.Equal(800, got.Totals.Cached)
	h.Require.Len(got.Items, 3)
	h.Require.Equal("r-sonnet", got.Items[0].RouteID, "most expensive first")
	h.Require.Equal("salib/anthropic-claude-sonnet-5-2qjd24q", got.Items[0].RouteName)
	h.Require.Equal("Claude Sonnet 5", got.Items[0].RouteDisplayName)
	h.Require.Equal("1.2500000001", *got.Items[0].CostUSD)
	h.Require.Equal(11, got.Items[0].RequestCount)
	h.Require.Equal("r-custom", got.Items[2].RouteID, "unpriced items sort last")
	h.Require.Nil(got.Items[2].CostUSD)
}

func Test_Harness_Usage_Table(t *testing.T) {
	h, _ := newHarnessUsageHarness(t, time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC), harnessUsageMonth())

	h.Require.NoError(h.Execute("harness", "usage"))
	out := h.Stdout.String()
	h.Require.Contains(out, "Spend     $1.75 (some usage could not be priced and is not included)")
	h.Require.Contains(out, "Requests  17")
	h.Require.Contains(out, "Tokens    1.6K input (800 cached), 155 output")
	h.Require.Equal([]string{"ROUTE", "REQUESTS", "INPUT", "CACHED", "OUTPUT", "COST"}, harnessUsageRow(h, out, "ROUTE"))
	h.Require.Equal([]string{"Claude", "Sonnet", "5", "11", "1.1K", "800", "110", "$1.25"}, harnessUsageRow(h, out, "Claude Sonnet 5"))
	h.Require.Equal([]string{"Qwen", "Custom", "2", "50", "0", "5", "-"}, harnessUsageRow(h, out, "Qwen Custom"))
	h.Require.NotContains(out, "salib/", "routes show display names")
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

func Test_Harness_Usage_DeletedRouteFallsBackToName(t *testing.T) {
	body := harnessUsageBody(false, nil, harnessUsageBucket("2026-09-01",
		harnessUsageResult(usageSonnet, "2", 2, 10, 0, 1),
		harnessUsageResult(map[string]any{"route_id": "gone", "route_name": "salib/deleted-route"}, "1", 1, 10, 0, 1),
	))
	h, api := newHarnessUsageHarness(t, time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC), body)

	h.Require.NoError(h.Execute("harness", "usage"))
	out := h.Stdout.String()
	h.Require.Contains(out, "Claude Sonnet 5")
	h.Require.Contains(out, "salib/deleted-route")
	h.Require.Empty(api.FindCall("GET", "/v1/routes").Query().Get("team_id"), "routes from every team")

	h.Require.NoError(h.Execute("harness", "usage", "--jq", "[.items[].route_display_name]"))
	h.Require.JSONEq(`["Claude Sonnet 5", null]`, h.Stdout.String())
}

func Test_Harness_Usage_RouteLookupFailureFallsBackToNames(t *testing.T) {
	h, api := newHarnessUsageHarness(t, time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC), harnessUsageMonth())
	api.SetRoute("GET", "/v1/routes", 500, map[string]any{"error": "boom"})

	h.Require.NoError(h.Execute("harness", "usage"))
	h.Require.Contains(h.Stdout.String(), "salib/anthropic-claude-sonnet-5-2qjd24q")
}
