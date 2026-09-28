package cmd_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/basetenlabs/baseten-cli/internal/cmd"
)

func harnessUsageResult(model string, cost any, input, cached, output int) map[string]any {
	return map[string]any{
		"model":                 model,
		"cost_usd":              cost,
		"input_tokens":          input,
		"cached_input_tokens":   cached,
		"uncached_input_tokens": input - cached,
		"output_tokens":         output,
	}
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

func newHarnessUsageHarness(t *testing.T, now time.Time, body map[string]any) (*CommandHarness, *MockManagementAPI) {
	h := NewCommandHarness(t)
	h.Context = cmd.WithNow(h.Context, func() time.Time { return now })
	api := h.MockManagementAPI()
	api.SetRoute("GET", "/v1/users/me", 200, map[string]string{"user_id": "user-a"})
	api.SetRoute("GET", "/v1/routes/usage", 200, body)
	return h, api
}

// harnessUsageRow returns the fields of the first output line containing
// marker.
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
			harnessUsageResult("claude-sonnet-5", "1.25", 1000, 800, 100),
			harnessUsageResult("gpt-5.2", "0.50", 400, 0, 40),
		),
		harnessUsageBucket("2026-09-02"),
		harnessUsageBucket("2026-09-03",
			harnessUsageResult("claude-sonnet-5", "0.0000000001", 100, 0, 10),
			harnessUsageResult("glm-5.2", "0.50", 50, 0, 5),
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
	h.Require.Equal([]string{"MODEL"}, q["group_by"])
	h.Require.Equal("31", q.Get("limit"))
	h.Require.Contains(h.Stderr.String(), "Harness usage for September 2026 (UTC, through Sep 25). Numbers can lag by up to 15 minutes.")
}

func Test_Harness_Usage_PastMonthSpansWholeMonth(t *testing.T) {
	h, api := newHarnessUsageHarness(t, time.Date(2027, 1, 10, 0, 0, 0, 0, time.UTC), harnessUsageMonth())

	h.Require.NoError(h.Execute("harness", "usage", "--month", "2026-12"))
	q := api.FindCall("GET", "/v1/routes/usage").Query()
	h.Require.Equal("2026-12-01", q.Get("start_date"))
	h.Require.Equal("2027-01-01", q.Get("end_date"))
	h.Require.Contains(h.Stderr.String(), "Harness usage for December 2026 (UTC). ")
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
			Input        int    `json:"input_tokens"`
			Cached       int    `json:"cached_input_tokens"`
		} `json:"totals"`
		Items []struct {
			Model   string  `json:"model"`
			CostUSD *string `json:"cost_usd"`
			Input   int     `json:"input_tokens"`
		} `json:"items"`
	}
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &got))
	h.Require.Equal("2026-09", got.Month)
	h.Require.Equal("2.2500000001", got.Totals.CostUSD, "costs are summed as exact decimals")
	h.Require.True(got.Totals.CostComplete)
	h.Require.Equal(1550, got.Totals.Input)
	h.Require.Equal(800, got.Totals.Cached)
	h.Require.Len(got.Items, 3)
	h.Require.Equal("claude-sonnet-5", got.Items[0].Model, "most expensive first")
	h.Require.Equal("1.2500000001", *got.Items[0].CostUSD)
	h.Require.Equal(1100, got.Items[0].Input, "buckets are summed per model")
	h.Require.Equal([]string{"glm-5.2", "gpt-5.2"}, []string{got.Items[1].Model, got.Items[2].Model}, "equal costs sort by model")
}

func Test_Harness_Usage_Table(t *testing.T) {
	h, _ := newHarnessUsageHarness(t, time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC), harnessUsageMonth())

	h.Require.NoError(h.Execute("harness", "usage"))
	out := h.Stdout.String()
	h.Require.Contains(out, "Spend   $2.25\n")
	h.Require.Contains(out, "Tokens  1.6K input (800 cached), 155 output")
	h.Require.Equal([]string{"MODEL", "INPUT", "CACHED", "OUTPUT", "COST"}, harnessUsageRow(h, out, "MODEL"))
	h.Require.Equal([]string{"claude-sonnet-5", "1.1K", "800", "110", "$1.25"}, harnessUsageRow(h, out, "claude-sonnet-5"))
	h.Require.Equal([]string{"gpt-5.2", "400", "0", "40", "$0.50"}, harnessUsageRow(h, out, "gpt-5.2"))
}

func Test_Harness_Usage_TinyCostStaysVisible(t *testing.T) {
	body := harnessUsageBody(false, nil, harnessUsageBucket("2026-09-01", harnessUsageResult("gpt-5.2", "0.0012", 10, 0, 1)))
	h, _ := newHarnessUsageHarness(t, time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC), body)

	h.Require.NoError(h.Execute("harness", "usage"))
	h.Require.Contains(h.Stdout.String(), "Spend   <$0.01\n")
}

func Test_Harness_Usage_HidesModelsWithNoUsage(t *testing.T) {
	body := harnessUsageBody(false, nil, harnessUsageBucket("2026-09-01",
		harnessUsageResult("claude-opus-5-5", "0.14", 10, 0, 1),
		harnessUsageResult("gpt-6-luna", "0", 0, 0, 0),
	))
	h, _ := newHarnessUsageHarness(t, time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC), body)

	h.Require.NoError(h.Execute("harness", "usage"))
	h.Require.NotContains(h.Stdout.String(), "gpt-6-luna")

	h.Require.NoError(h.Execute("harness", "usage", "--jq", "[.items[].model]"))
	h.Require.JSONEq(`["claude-opus-5-5"]`, h.Stdout.String())
}

func Test_Harness_Usage_RejectsInvalidCost(t *testing.T) {
	body := harnessUsageBody(false, nil, harnessUsageBucket("2026-09-01", harnessUsageResult("gpt-5.2", "abc", 10, 0, 1)))
	h, _ := newHarnessUsageHarness(t, time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC), body)

	h.Require.ErrorContains(h.Execute("harness", "usage"), `invalid cost "abc"`)
}

func Test_Harness_Usage_Empty(t *testing.T) {
	body := harnessUsageBody(false, nil, harnessUsageBucket("2026-09-01"))
	h, _ := newHarnessUsageHarness(t, time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC), body)

	h.Require.NoError(h.Execute("harness", "usage"))
	h.Require.Empty(h.Stdout.String())
	h.Require.Contains(h.Stderr.String(), "No harness usage in September 2026.")
	h.Require.Contains(h.Stderr.String(), "If you're using a team or workspace API key, run 'baseten auth login'",
		"a service-account key has no routes usage")

	h.Require.NoError(h.Execute("harness", "usage", "--jq", ".totals.cost_usd"))
	h.Require.Equal("\"0\"\n", h.Stdout.String())
}

func Test_Harness_Usage_FollowsCursor(t *testing.T) {
	h, api := newHarnessUsageHarness(t, time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC), nil)
	api.SetRouteFunc("GET", "/v1/routes/usage", func(w http.ResponseWriter, r *http.Request) {
		body := harnessUsageBody(true, "next", harnessUsageBucket("2026-09-01", harnessUsageResult("gpt-5.2", "1", 10, 0, 1)))
		if r.URL.Query().Get("cursor") == "next" {
			body = harnessUsageBody(false, nil, harnessUsageBucket("2026-09-02", harnessUsageResult("gpt-5.2", "2", 20, 0, 2)))
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
		body := harnessUsageBody(false, nil, harnessUsageBucket("2026-09-01", harnessUsageResult("gpt-5.2", "1", tc.input, 0, 0)))
		h, _ := newHarnessUsageHarness(t, time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC), body)
		h.Require.NoError(h.Execute("harness", "usage"))
		h.Require.Contains(h.Stdout.String(), "Tokens  "+tc.want+" input", "%d tokens", tc.input)
	}
}

func Test_Harness_Usage_NullCostIsUnpriced(t *testing.T) {
	body := harnessUsageBody(false, nil, harnessUsageBucket("2026-09-01",
		harnessUsageResult("claude-opus-5-5", "0.14", 10, 0, 1),
		harnessUsageResult("qwen-custom", nil, 50, 0, 5),
	))
	h, _ := newHarnessUsageHarness(t, time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC), body)

	h.Require.NoError(h.Execute("harness", "usage"))
	out := h.Stdout.String()
	h.Require.Contains(out, "Spend   $0.14 (excludes usage that couldn't be priced)")
	h.Require.Equal([]string{"qwen-custom", "50", "0", "5", "-"}, harnessUsageRow(h, out, "qwen-custom"))

	h.Require.NoError(h.Execute("harness", "usage", "--jq", "{total: .totals.cost_usd, complete: .totals.cost_complete, costs: [.items[].cost_usd]}"))
	h.Require.JSONEq(`{"total": "0.14", "complete": false, "costs": ["0.14", null]}`, h.Stdout.String())
}

func Test_Harness_Usage_ClampsToRetention(t *testing.T) {
	h, api := newHarnessUsageHarness(t, time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC), harnessUsageMonth())

	h.Require.NoError(h.Execute("harness", "usage", "--month", "2026-06", "--output", "json"))
	h.Require.Equal("2026-06-29", api.FindCall("GET", "/v1/routes/usage").Query().Get("start_date"),
		"92 days including today")
	h.Require.Contains(h.Stderr.String(), "Usage is kept for 92 days, so this only includes usage from Jun 29 on.")
	var got struct {
		StartDate string `json:"start_date"`
		EndDate   string `json:"end_date"`
	}
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &got))
	h.Require.Equal("2026-06-29", got.StartDate)
	h.Require.Equal("2026-07-01", got.EndDate)

	h.Require.ErrorContains(h.Execute("harness", "usage", "--month", "2026-05"), "older than the 92 days of usage Baseten keeps")

	h.Require.NoError(h.Execute("harness", "usage", "--month", "2026-07"))
	h.Require.Equal("2026-07-01", api.Calls()[len(api.Calls())-1].Query().Get("start_date"))
	h.Require.NotContains(h.Stderr.String(), "Usage is kept")
}
