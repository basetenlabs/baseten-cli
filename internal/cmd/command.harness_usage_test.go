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

// harnessUsageNow is the pinned clock for these tests, mid-afternoon UTC.
var harnessUsageNow = time.Date(2026, 9, 25, 15, 0, 0, 0, time.UTC)

func newHarnessUsageHarness(t *testing.T, now time.Time, body map[string]any) (*CommandHarness, *MockManagementAPI) {
	h := NewCommandHarness(t)
	h.Context = cmd.WithNow(h.Context, func() time.Time { return now })
	api := h.MockManagementAPI()
	api.SetRoute("GET", "/v1/users/me", 200, map[string]string{"user_id": "user-a"})
	api.SetRoute("GET", "/v1/routes/usage", 200, body)
	return h, api
}

// harnessUsageDates returns the start_date and end_date of the last usage
// request.
func harnessUsageDates(api *MockManagementAPI) (string, string) {
	calls := api.Calls()
	for i := len(calls) - 1; i >= 0; i-- {
		if calls[i].Path == "/v1/routes/usage" {
			q := calls[i].Query()
			return q.Get("start_date"), q.Get("end_date")
		}
	}
	return "", ""
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

func Test_Harness_Usage_DefaultsToMonthToDateForCurrentUser(t *testing.T) {
	h, api := newHarnessUsageHarness(t, harnessUsageNow, harnessUsageMonth())

	h.Require.NoError(h.Execute("harness", "usage"))
	q := api.FindCall("GET", "/v1/routes/usage").Query()
	h.Require.Equal("2026-09-01", q.Get("start_date"))
	h.Require.Equal("2026-09-26", q.Get("end_date"), "today's usage is included")
	h.Require.Equal([]string{"user-a"}, q["user_ids"], "admins must not see other users' usage")
	h.Require.Equal([]string{"MODEL"}, q["group_by"])
	h.Require.Equal("31", q.Get("limit"))
	h.Require.Contains(h.Stderr.String(), "Window: 2026-09-01 through 2026-09-25 UTC · grouped by model · can lag by up to 15 minutes")
	h.Require.NotContains(h.Stdout.String(), "Window:")
}

func Test_Harness_Usage_WindowFlags(t *testing.T) {
	for _, tc := range []struct {
		name       string
		args       []string
		start, end string
	}{
		{"since snaps to whole days", []string{"--since", "7d"}, "2026-09-18", "2026-09-26"},
		{"start and end on day boundaries", []string{"--start", "2026-08-01T00:00:00Z", "--end", "2026-09-01T00:00:00Z"}, "2026-08-01", "2026-09-01"},
		{"end mid-day includes that day", []string{"--start", "2026-09-10T00:00:00Z", "--end", "2026-09-12T06:00:00Z"}, "2026-09-10", "2026-09-13"},
		{"start only runs to now", []string{"--start", "2026-09-20T12:00:00Z"}, "2026-09-20", "2026-09-26"},
		{"end only starts at its month", []string{"--end", "2026-08-15T00:00:00Z"}, "2026-08-01", "2026-08-15"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, api := newHarnessUsageHarness(t, harnessUsageNow, harnessUsageMonth())
			h.Require.NoError(h.Execute(append([]string{"harness", "usage"}, tc.args...)...))
			start, end := harnessUsageDates(api)
			h.Require.Equal(tc.start, start)
			h.Require.Equal(tc.end, end)
		})
	}
}

func Test_Harness_Usage_RejectsBadWindow(t *testing.T) {
	h, _ := newHarnessUsageHarness(t, harnessUsageNow, harnessUsageMonth())

	h.Require.ErrorContains(h.Execute("harness", "usage", "--since", "1d", "--start", "2026-09-01T00:00:00Z"), "--since cannot be combined")
	h.Require.ErrorContains(h.Execute("harness", "usage", "--start", "2026-09-10T00:00:00Z", "--end", "2026-09-01T00:00:00Z"), "--start must be earlier than --end")
	h.Require.ErrorContains(h.Execute("harness", "usage", "--start", "2026-10-01T00:00:00Z"), "--start must be in the past")
}

func Test_Harness_Usage_JSONSumsBucketsExactly(t *testing.T) {
	h, _ := newHarnessUsageHarness(t, harnessUsageNow, harnessUsageMonth())

	h.Require.NoError(h.Execute("harness", "usage", "--output", "json"))
	var got struct {
		StartDate string `json:"start_date"`
		EndDate   string `json:"end_date"`
		Totals    struct {
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
	h.Require.Equal("2026-09-01", got.StartDate)
	h.Require.Equal("2026-09-26", got.EndDate)
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

func Test_Harness_Usage_TableWithTotalsRow(t *testing.T) {
	body := harnessUsageBody(false, nil, harnessUsageBucket("2026-09-01",
		harnessUsageResult("claude-sonnet-5", "1.25", 1234567, 800000, 10000),
		harnessUsageResult("gpt-5.2", "0.50", 400, 0, 40),
	))
	h, _ := newHarnessUsageHarness(t, harnessUsageNow, body)

	h.Require.NoError(h.Execute("harness", "usage"))
	out := h.Stdout.String()
	h.Require.Equal([]string{"MODEL", "INPUT", "CACHED", "OUTPUT", "COST"}, harnessUsageRow(h, out, "MODEL"))
	h.Require.Equal([]string{"claude-sonnet-5", "1,234,567", "800,000", "10,000", "$1.25"}, harnessUsageRow(h, out, "claude-sonnet-5"))
	h.Require.Equal([]string{"gpt-5.2", "400", "0", "40", "$0.50"}, harnessUsageRow(h, out, "gpt-5.2"))
	h.Require.Equal([]string{"ALL", "1,234,967", "800,000", "10,040", "$1.75"}, harnessUsageRow(h, out, "ALL"))
}

func Test_Harness_Usage_SingleModelHasNoTotalsRow(t *testing.T) {
	body := harnessUsageBody(false, nil, harnessUsageBucket("2026-09-01", harnessUsageResult("gpt-5.2", "0.0012", 10, 0, 1)))
	h, _ := newHarnessUsageHarness(t, harnessUsageNow, body)

	h.Require.NoError(h.Execute("harness", "usage"))
	out := h.Stdout.String()
	h.Require.Equal([]string{"gpt-5.2", "10", "0", "1", "<$0.01"}, harnessUsageRow(h, out, "gpt-5.2"), "tiny costs stay visible")
	h.Require.NotContains(out, "ALL")
}

func Test_Harness_Usage_HidesModelsWithNoUsage(t *testing.T) {
	body := harnessUsageBody(false, nil, harnessUsageBucket("2026-09-01",
		harnessUsageResult("claude-opus-5-5", "0.14", 10, 0, 1),
		harnessUsageResult("gpt-6-luna", "0", 0, 0, 0),
	))
	h, _ := newHarnessUsageHarness(t, harnessUsageNow, body)

	h.Require.NoError(h.Execute("harness", "usage"))
	h.Require.NotContains(h.Stdout.String(), "gpt-6-luna")

	h.Require.NoError(h.Execute("harness", "usage", "--jq", "[.items[].model]"))
	h.Require.JSONEq(`["claude-opus-5-5"]`, h.Stdout.String())
}

func Test_Harness_Usage_NullCostIsUnpriced(t *testing.T) {
	body := harnessUsageBody(false, nil, harnessUsageBucket("2026-09-01",
		harnessUsageResult("claude-opus-5-5", "0.14", 10, 0, 1),
		harnessUsageResult("qwen-custom", nil, 50, 0, 5),
	))
	h, _ := newHarnessUsageHarness(t, harnessUsageNow, body)

	h.Require.NoError(h.Execute("harness", "usage"))
	out := h.Stdout.String()
	h.Require.Equal([]string{"qwen-custom", "50", "0", "5", "-"}, harnessUsageRow(h, out, "qwen-custom"))
	h.Require.Equal([]string{"ALL", "60", "0", "6", "-"}, harnessUsageRow(h, out, "ALL"), "a partial total would understate spend")
	h.Require.Contains(h.Stderr.String(), `A cost of "-" means some of that usage couldn't be priced.`)

	h.Require.NoError(h.Execute("harness", "usage", "--jq", "{total: .totals.cost_usd, complete: .totals.cost_complete, costs: [.items[].cost_usd]}"))
	h.Require.JSONEq(`{"total": "0.14", "complete": false, "costs": ["0.14", null]}`, h.Stdout.String())
}

func Test_Harness_Usage_RejectsInvalidCost(t *testing.T) {
	body := harnessUsageBody(false, nil, harnessUsageBucket("2026-09-01", harnessUsageResult("gpt-5.2", "abc", 10, 0, 1)))
	h, _ := newHarnessUsageHarness(t, harnessUsageNow, body)

	h.Require.ErrorContains(h.Execute("harness", "usage"), `invalid cost "abc"`)
}

func Test_Harness_Usage_Empty(t *testing.T) {
	body := harnessUsageBody(false, nil, harnessUsageBucket("2026-09-01"))
	h, _ := newHarnessUsageHarness(t, harnessUsageNow, body)

	h.Require.NoError(h.Execute("harness", "usage"))
	h.Require.Empty(h.Stdout.String())
	h.Require.Contains(h.Stderr.String(), "No usage in the selected window.")
	h.Require.Contains(h.Stderr.String(), "If you're using a team or workspace API key, run 'baseten auth login'",
		"a service-account key has no routes usage")

	h.Require.NoError(h.Execute("harness", "usage", "--jq", ".totals.cost_usd"))
	h.Require.Equal("\"0\"\n", h.Stdout.String())
}

func Test_Harness_Usage_FollowsCursor(t *testing.T) {
	h, api := newHarnessUsageHarness(t, harnessUsageNow, nil)
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
	calls := api.Calls()
	last := calls[len(calls)-1].Query()
	h.Require.Equal("next", last.Get("cursor"))
	h.Require.Equal([]string{"user-a"}, last["user_ids"], "later pages keep the user filter")
}

func Test_Harness_Usage_ClampsToRetention(t *testing.T) {
	h, api := newHarnessUsageHarness(t, time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC), harnessUsageMonth())

	h.Require.NoError(h.Execute("harness", "usage", "--start", "2026-06-01T00:00:00Z", "--end", "2026-07-01T00:00:00Z", "--output", "json"))
	start, _ := harnessUsageDates(api)
	h.Require.Equal("2026-06-29", start, "92 days including today")
	h.Require.Contains(h.Stderr.String(), "Usage is kept for 92 days, so this only includes usage from 2026-06-29 on.")
	h.Require.Contains(h.Stdout.String(), `"start_date": "2026-06-29"`)

	h.Require.ErrorContains(h.Execute("harness", "usage", "--start", "2026-05-01T00:00:00Z", "--end", "2026-06-01T00:00:00Z"),
		"older than the 92 days of usage Baseten keeps")

	h.Require.NoError(h.Execute("harness", "usage", "--since", "30d"))
	h.Require.NotContains(h.Stderr.String(), "Usage is kept")
}
