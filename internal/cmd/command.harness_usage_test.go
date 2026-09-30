package cmd_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/basetenlabs/baseten-cli/internal/cmd"
)

func harnessUsageResult(dims map[string]any, cost any, input, cached, output int) map[string]any {
	r := map[string]any{
		"cost_usd":              cost,
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

func harnessUsageModel(model string, cost any, input, cached, output int) map[string]any {
	return harnessUsageResult(map[string]any{"model": model}, cost, input, cached, output)
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

// harnessUsageLastQuery returns the query of the last usage request.
func harnessUsageLastQuery(h *CommandHarness, api *MockManagementAPI) map[string][]string {
	calls := api.Calls()
	for i := len(calls) - 1; i >= 0; i-- {
		if calls[i].Path == "/v1/routes/usage" {
			return calls[i].Query()
		}
	}
	h.Require.Fail("no usage request")
	return nil
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
			harnessUsageModel("claude-sonnet-5", "1.25", 1000, 800, 100),
			harnessUsageModel("gpt-5.2", "0.50", 400, 0, 40),
		),
		harnessUsageBucket("2026-09-02"),
		harnessUsageBucket("2026-09-03",
			harnessUsageModel("claude-sonnet-5", "0.0000000001", 100, 0, 10),
			harnessUsageModel("glm-5.2", "0.50", 50, 0, 5),
		),
	)
}

func Test_Harness_Usage_DefaultsToMonthToDateForCurrentUser(t *testing.T) {
	h, api := newHarnessUsageHarness(t, harnessUsageNow, harnessUsageMonth())

	h.Require.NoError(h.Execute("harness", "usage"))
	q := harnessUsageLastQuery(h, api)
	h.Require.Equal([]string{"2026-09-01"}, q["start_date"])
	h.Require.Equal([]string{"2026-09-26"}, q["end_date"], "today's usage is included")
	h.Require.Equal([]string{"user-a"}, q["user_ids"], "admins must not see other users' usage by default")
	h.Require.Equal([]string{"MODEL"}, q["group_by"])
	h.Require.Equal([]string{"31"}, q["limit"])
	h.Require.Empty(q["models"])
	h.Require.Empty(q["providers"])
	h.Require.Contains(h.Stderr.String(), "Window: 2026-09-01 through 2026-09-03 UTC · grouped by model · can lag by up to 15 minutes")
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
			q := harnessUsageLastQuery(h, api)
			h.Require.Equal([]string{tc.start}, q["start_date"])
			h.Require.Equal([]string{tc.end}, q["end_date"])
		})
	}
}

func Test_Harness_Usage_RejectsBadFlags(t *testing.T) {
	h, _ := newHarnessUsageHarness(t, harnessUsageNow, harnessUsageMonth())

	h.Require.ErrorContains(h.Execute("harness", "usage", "--since", "1d", "--start", "2026-09-01T00:00:00Z"), "--since cannot be combined")
	h.Require.ErrorContains(h.Execute("harness", "usage", "--start", "2026-09-10T00:00:00Z", "--end", "2026-09-01T00:00:00Z"), "--start must be earlier than --end")
	h.Require.ErrorContains(h.Execute("harness", "usage", "--start", "2026-10-01T00:00:00Z"), "--start must be in the past")
	h.Require.ErrorContains(h.Execute("harness", "usage", "--group-by", "route"), `"route" must be one of: user, model, provider`)
	h.Require.ErrorContains(h.Execute("harness", "usage", "--provider", "google"), `"google" must be one of: baseten-model-api, openai`)
	h.Require.ErrorContains(h.Execute("harness", "usage", "--limit", "-1"), "--limit must be zero")
	h.Require.ErrorContains(h.Execute("harness", "usage", "--page-size", "32"), "--page-size must be between 1 and 31")
}

func Test_Harness_Usage_GroupByDefaultShowsInHelp(t *testing.T) {
	h, _ := newHarnessUsageHarness(t, harnessUsageNow, harnessUsageMonth())

	h.Require.NoError(h.Execute("harness", "usage", "--help"))
	h.Require.Regexp(`--group-by[^\n]*\{user,model,provider\} \(model\)`, h.Stdout.String())
}

func Test_Harness_Usage_CommaSeparatedGroupBy(t *testing.T) {
	h, api := newHarnessUsageHarness(t, harnessUsageNow, harnessUsageMonth())

	h.Require.NoError(h.Execute("harness", "usage", "--group-by", "provider,model"))
	h.Require.Equal([]string{"PROVIDER", "MODEL"}, harnessUsageLastQuery(h, api)["group_by"], "passing values replaces the default")
}

func Test_Harness_Usage_FiltersAndGrouping(t *testing.T) {
	body := harnessUsageBody(false, nil, harnessUsageBucket("2026-09-01",
		harnessUsageResult(map[string]any{"user_id": "user-b", "provider": "ANTHROPIC"}, "2", 20, 0, 2),
		harnessUsageResult(map[string]any{"user_id": "user-c", "provider": "BASETEN_MODEL_API"}, "1", 10, 0, 1),
	))
	h, api := newHarnessUsageHarness(t, harnessUsageNow, body)

	h.Require.NoError(h.Execute("harness", "usage",
		"--group-by", "user", "--group-by", "provider", "--group-by", "user",
		"--user-id", "user-b", "--user-id", "user-c",
		"--model", "claude-opus-5-5", "--provider", "anthropic", "--provider", "baseten-model-api"))
	q := harnessUsageLastQuery(h, api)
	h.Require.Equal([]string{"USER", "PROVIDER"}, q["group_by"], "deduplicated, in order")
	h.Require.Equal([]string{"user-b", "user-c"}, q["user_ids"])
	h.Require.Equal([]string{"claude-opus-5-5"}, q["models"])
	h.Require.Equal([]string{"ANTHROPIC", "BASETEN_MODEL_API"}, q["providers"])
	h.Require.Nil(api.FindCall("GET", "/v1/users/me"), "an explicit --user-id skips the default")

	out := h.Stdout.String()
	h.Require.Equal([]string{"USER", "PROVIDER", "INPUT", "CACHED", "OUTPUT", "COST"}, harnessUsageRow(h, out, "USER"))
	h.Require.Equal([]string{"user-b", "anthropic", "20", "0", "2", "$2.00"}, harnessUsageRow(h, out, "user-b"))
	h.Require.Equal([]string{"user-c", "baseten-model-api", "10", "0", "1", "$1.00"}, harnessUsageRow(h, out, "user-c"))
	h.Require.Equal([]string{"ALL", "30", "0", "3", "$3.00"}, harnessUsageRow(h, out, "ALL"))
	h.Require.Contains(h.Stderr.String(), "grouped by user, provider")
}

func Test_Harness_Usage_JSONStreamsBucketsAsReturned(t *testing.T) {
	h, _ := newHarnessUsageHarness(t, harnessUsageNow, harnessUsageMonth())

	h.Require.NoError(h.Execute("harness", "usage", "--output", "json"))
	var got []struct {
		Date    string `json:"date"`
		Results []struct {
			Model   string `json:"model"`
			CostUSD string `json:"cost_usd"`
			Input   int    `json:"input_tokens"`
		} `json:"results"`
	}
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &got))
	h.Require.Len(got, 3, "one record per day, including days with no usage")
	h.Require.Equal("2026-09-02", got[1].Date)
	h.Require.Empty(got[1].Results)
	h.Require.Equal("claude-sonnet-5", got[0].Results[0].Model)
	h.Require.Equal("1.25", got[0].Results[0].CostUSD, "costs pass through as exact decimal strings")
	h.Require.Empty(h.Stderr.String())

	h.Require.NoError(h.Execute("harness", "usage", "--output", "jsonl"))
	h.Require.Equal(3, strings.Count(h.Stdout.String(), "\n"))

	h.Require.NoError(h.Execute("harness", "usage", "--output", "jsonl", "--jq", ".date"))
	h.Require.Equal("\"2026-09-01\"\n\"2026-09-02\"\n\"2026-09-03\"\n", h.Stdout.String())
}

func Test_Harness_Usage_TableWithTotalsRow(t *testing.T) {
	h, _ := newHarnessUsageHarness(t, harnessUsageNow, harnessUsageMonth())

	h.Require.NoError(h.Execute("harness", "usage"))
	out := h.Stdout.String()
	h.Require.Equal([]string{"MODEL", "INPUT", "CACHED", "OUTPUT", "COST"}, harnessUsageRow(h, out, "MODEL"))
	h.Require.Equal([]string{"claude-sonnet-5", "1,100", "800", "110", "$1.25"}, harnessUsageRow(h, out, "claude-sonnet-5"), "days are summed")
	h.Require.Equal([]string{"glm-5.2", "50", "0", "5", "$0.50"}, harnessUsageRow(h, out, "glm-5.2"), "equal costs sort by key")
	h.Require.Equal([]string{"ALL", "1,550", "800", "155", "$2.25"}, harnessUsageRow(h, out, "ALL"))
	lines := strings.Split(strings.TrimSpace(out), "\n")
	h.Require.Contains(lines[1], "claude-sonnet-5", "most expensive first")
}

func Test_Harness_Usage_SingleRowHasNoTotalsRow(t *testing.T) {
	body := harnessUsageBody(false, nil, harnessUsageBucket("2026-09-01", harnessUsageModel("gpt-5.2", "0.0012", 10, 0, 1)))
	h, _ := newHarnessUsageHarness(t, harnessUsageNow, body)

	h.Require.NoError(h.Execute("harness", "usage"))
	out := h.Stdout.String()
	h.Require.Equal([]string{"gpt-5.2", "10", "0", "1", "<$0.01"}, harnessUsageRow(h, out, "gpt-5.2"), "tiny costs stay visible")
	h.Require.NotContains(out, "ALL")
}

func Test_Harness_Usage_HidesRowsWithNoUsage(t *testing.T) {
	body := harnessUsageBody(false, nil, harnessUsageBucket("2026-09-01",
		harnessUsageModel("claude-opus-5-5", "0.14", 10, 0, 1),
		harnessUsageModel("gpt-6-luna", "0", 0, 0, 0),
	))
	h, _ := newHarnessUsageHarness(t, harnessUsageNow, body)

	h.Require.NoError(h.Execute("harness", "usage"))
	h.Require.NotContains(h.Stdout.String(), "gpt-6-luna")
	h.Require.NotContains(h.Stdout.String(), "ALL")

	h.Require.NoError(h.Execute("harness", "usage", "--output", "jsonl", "--jq", "[.results[].model]"))
	h.Require.JSONEq(`["claude-opus-5-5", "gpt-6-luna"]`, h.Stdout.String(), "JSON passes the API's rows through")
}

func Test_Harness_Usage_NullCostIsUnpriced(t *testing.T) {
	body := harnessUsageBody(false, nil, harnessUsageBucket("2026-09-01",
		harnessUsageModel("claude-opus-5-5", "0.14", 10, 0, 1),
		harnessUsageModel("qwen-custom", nil, 50, 0, 5),
	))
	h, _ := newHarnessUsageHarness(t, harnessUsageNow, body)

	h.Require.NoError(h.Execute("harness", "usage"))
	out := h.Stdout.String()
	h.Require.Equal([]string{"qwen-custom", "50", "0", "5", "-"}, harnessUsageRow(h, out, "qwen-custom"))
	h.Require.Equal([]string{"ALL", "60", "0", "6", "-"}, harnessUsageRow(h, out, "ALL"), "a partial total would understate spend")
	h.Require.Contains(h.Stderr.String(), `A cost of "-" means some of that usage couldn't be priced.`)
}

func Test_Harness_Usage_RejectsInvalidCost(t *testing.T) {
	body := harnessUsageBody(false, nil, harnessUsageBucket("2026-09-01", harnessUsageModel("gpt-5.2", "abc", 10, 0, 1)))
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
		"a service-account key has no routes usage of its own")

	h.Require.NoError(h.Execute("harness", "usage", "--user-id", "user-b"))
	h.Require.NotContains(h.Stderr.String(), "team or workspace API key", "only the default user filter gets the hint")
}

func Test_Harness_Usage_PaginatesKeepingParams(t *testing.T) {
	h, api := newHarnessUsageHarness(t, harnessUsageNow, nil)
	api.SetRouteFunc("GET", "/v1/routes/usage", func(w http.ResponseWriter, r *http.Request) {
		body := harnessUsageBody(true, "next", harnessUsageBucket("2026-09-01", harnessUsageModel("gpt-5.2", "1", 10, 0, 1)))
		if r.URL.Query().Get("cursor") == "next" {
			body = harnessUsageBody(false, nil, harnessUsageBucket("2026-09-02", harnessUsageModel("gpt-5.2", "2", 20, 0, 2)))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	})

	h.Require.NoError(h.Execute("harness", "usage", "--page-size", "1"))
	h.Require.Equal([]string{"gpt-5.2", "30", "0", "3", "$3.00"}, harnessUsageRow(h, h.Stdout.String(), "gpt-5.2"))
	q := harnessUsageLastQuery(h, api)
	h.Require.Equal([]string{"next"}, q["cursor"])
	h.Require.Equal([]string{"user-a"}, q["user_ids"], "later pages keep the user filter")
	h.Require.Equal([]string{"1"}, q["limit"])
}

func Test_Harness_Usage_LimitCapsBuckets(t *testing.T) {
	h, _ := newHarnessUsageHarness(t, harnessUsageNow, harnessUsageMonth())

	h.Require.NoError(h.Execute("harness", "usage", "--limit", "1"))
	out := h.Stdout.String()
	h.Require.Equal([]string{"claude-sonnet-5", "1,000", "800", "100", "$1.25"}, harnessUsageRow(h, out, "claude-sonnet-5"))
	h.Require.NotContains(out, "glm-5.2", "the third day is past the limit")
	h.Require.Contains(h.Stderr.String(), "Window: 2026-09-01 through 2026-09-01 UTC")
	h.Require.Contains(h.Stderr.String(), "Reached the --limit of 1 buckets; more exist.")

	h.Require.NoError(h.Execute("harness", "usage", "--limit", "3"))
	h.Require.NotContains(h.Stderr.String(), "Reached the --limit")
}

func Test_Harness_Usage_ClampsToRetention(t *testing.T) {
	h, api := newHarnessUsageHarness(t, time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC), harnessUsageMonth())

	h.Require.NoError(h.Execute("harness", "usage", "--start", "2026-06-01T00:00:00Z", "--end", "2026-07-01T00:00:00Z"))
	h.Require.Equal([]string{"2026-06-29"}, harnessUsageLastQuery(h, api)["start_date"], "92 days including today")
	h.Require.Contains(h.Stderr.String(), "Usage is kept for 92 days, so this only includes usage from 2026-06-29 on.")

	h.Require.ErrorContains(h.Execute("harness", "usage", "--start", "2026-05-01T00:00:00Z", "--end", "2026-06-01T00:00:00Z"),
		"older than the 92 days of usage Baseten keeps")

	h.Require.NoError(h.Execute("harness", "usage", "--since", "30d"))
	h.Require.NotContains(h.Stderr.String(), "Usage is kept")
}
