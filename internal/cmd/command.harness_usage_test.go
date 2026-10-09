package cmd_test

import (
	"encoding/json"
	"regexp"
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

// harnessUsageModel is a model's usage in team-a.
func harnessUsageModel(model string, cost any, input, cached, output int) map[string]any {
	return harnessUsageResult(map[string]any{"team_id": "team-a", "model": model}, cost, input, cached, output)
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

// harnessTeamSettings is your settings in a team with limit, spent of it;
// extra overrides fields.
func harnessTeamSettings(id, name, spent string, limit any, extra map[string]any) map[string]any {
	s := map[string]any{
		"team_id": id, "team_name": name, "user_id": "user-a", "month_spend_usd": spent, "is_blocked": false, "blocked_by": nil,
		"spend_limit": map[string]any{
			"effective":    map[string]any{"monthly_limit_usd": limit, "source": "team"},
			"team_default": map[string]any{"team_id": id, "per_member_monthly_limit_usd": limit},
		},
	}
	for k, v := range extra {
		s[k] = v
	}
	return s
}

// harnessUsageNow is the pinned clock for these tests, mid-afternoon UTC.
var harnessUsageNow = time.Date(2026, 9, 25, 15, 0, 0, 0, time.UTC)

func newHarnessUsageHarness(t *testing.T, now time.Time, body map[string]any) (*CommandHarness, *MockManagementAPI) {
	h := NewCommandHarness(t)
	h.Context = cmd.WithNow(h.Context, func() time.Time { return now })
	api := h.MockManagementAPI()
	api.SetRoute("GET", "/v1/users/me", 200, map[string]string{"user_id": "user-a"})
	api.SetRoute("GET", "/v1/routes/usage", 200, body)
	api.SetRoute("GET", "/v1/routes/settings/users/me/teams", 200, map[string]any{"teams": []map[string]any{
		harnessTeamSettings("team-a", "Engineering", "1.75", "100", nil),
	}})
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

func harnessUsageMonth(more ...map[string]any) map[string]any {
	return harnessUsageBody(false, nil, append([]map[string]any{
		harnessUsageBucket("2026-09-01",
			harnessUsageModel("claude-sonnet-5", "1.25", 1000, 800, 100),
			harnessUsageModel("gpt-5.2", "0.50", 400, 0, 40),
		),
		harnessUsageBucket("2026-09-02"),
		harnessUsageBucket("2026-09-03",
			harnessUsageModel("claude-sonnet-5", "0.0000000001", 100, 0, 10),
			harnessUsageResult(map[string]any{"team_id": nil, "model": "glm-5.2"}, "0.50", 50, 0, 5),
		),
	}, more...)...)
}

func Test_Harness_Usage_DefaultsToMonthToDateForCurrentUser(t *testing.T) {
	h, api := newHarnessUsageHarness(t, harnessUsageNow, harnessUsageMonth())

	h.Require.NoError(h.Execute("harness", "usage"))
	q := harnessUsageLastQuery(h, api)
	h.Require.Equal([]string{"2026-09-01"}, q["start_date"])
	h.Require.Equal([]string{"2026-09-26"}, q["end_date"], "today's usage is included")
	h.Require.Equal([]string{"user-a"}, q["user_ids"], "admins must not see other users' usage by default")
	h.Require.Equal([]string{"TEAM", "MODEL"}, q["group_by"])
	h.Require.Equal([]string{"31"}, q["limit"])
	h.Require.Empty(q["team_id"], "every team by default")
	h.Require.Equal("September 2026 · resets Oct 1\n", h.Stderr.String())
	h.Require.NotContains(h.Stdout.String(), "September")
}

func Test_Harness_Usage_BlocksPerTeam(t *testing.T) {
	h, api := newHarnessUsageHarness(t, harnessUsageNow, harnessUsageMonth())
	api.SetRoute("GET", "/v1/routes/settings/users/me/teams", 200, map[string]any{"teams": []map[string]any{
		harnessTeamSettings("team-a", "Engineering", "1.75", "100", nil),
		harnessTeamSettings("team-b", "Research", "12.5", "10", map[string]any{
			"is_blocked": true, "blocked_by": "member",
			"spend_limit": map[string]any{
				"effective":    map[string]any{"monthly_limit_usd": "10", "source": "user"},
				"team_default": map[string]any{"team_id": "team-b", "per_member_monthly_limit_usd": "100.00"},
			},
		}),
		harnessTeamSettings("team-c", "FDE", "80", "100", map[string]any{"is_blocked": true, "blocked_by": "team"}),
		harnessTeamSettings("team-d", "Infra", "0", nil, map[string]any{"spend_limit": map[string]any{"effective": map[string]any{}}}),
	}})

	h.Require.NoError(h.Execute("harness", "usage"))
	out := h.Stdout.String()
	h.Require.Equal([]string{"Engineering", "$2", "of", "$100"}, harnessUsageRow(h, out, "Engineering"), "whole dollars")
	h.Require.Equal([]string{"●", "claude-sonnet-5", "$1", "(71%)"}, harnessUsageRow(h, out, "claude-sonnet-5"), "days are summed")
	h.Require.Equal([]string{"●", "gpt-5.2", "$1", "(29%)"}, harnessUsageRow(h, out, "gpt-5.2"), "half a dollar rounds up")
	h.Require.Equal([]string{"Research", "$13", "of", "$10", "(team", "default", "$100)", "·", "limit", "reached"},
		harnessUsageRow(h, out, "Research"), "the team default only when yours differs")
	h.Require.Equal([]string{"FDE", "$80", "of", "$100", "·", "team", "limit", "reached"}, harnessUsageRow(h, out, "FDE"))
	h.Require.Equal([]string{"Infra", "$0", "·", "no", "limit"}, harnessUsageRow(h, out, "Infra"))
	h.Require.Equal([]string{"(no", "team)", "$1"}, harnessUsageRow(h, out, "(no team)"), "usage metered before team attribution")
	h.Require.Equal([]string{"●", "glm-5.2", "$1", "(100%)"}, harnessUsageRow(h, out, "glm-5.2"))
	h.Require.Regexp(`(?s)Engineering.*claude-sonnet-5.*gpt-5.2.*\n\nResearch.*\n\nFDE.*\n\nInfra.*\n\n\(no team\).*glm-5.2`, out,
		"teams in the API's order, models most expensive first, legacy usage last")
	h.Require.Regexp(`(?m)^Engineering +\$2 of \$100$`, out)
	h.Require.Regexp(`\n  ● gpt-5.2 +\$1 +\(29%\)\n`, out, "amounts line up")
	h.Require.NotContains(out, "┤", "no chart when stdout isn't a terminal")
	h.Require.NotContains(out, "\x1b[", "no color when stdout isn't a terminal")
}

func Test_Harness_Usage_TeamFlag(t *testing.T) {
	h, api := newHarnessUsageHarness(t, harnessUsageNow, harnessUsageMonth())
	api.SetRoute("GET", "/v1/routes/settings/users/me/teams", 200, map[string]any{"teams": []map[string]any{
		harnessTeamSettings("team-a", "Engineering", "1.75", "100", nil),
		harnessTeamSettings("team-b", "Research", "0", "100", nil),
	}})

	h.Require.NoError(h.Execute("harness", "usage", "--team", "Research"))
	h.Require.Equal([]string{"team-b"}, harnessUsageLastQuery(h, api)["team_id"], "--team also narrows the usage")
	h.Require.Contains(h.Stdout.String(), "Research")
	h.Require.NotContains(h.Stdout.String(), "Engineering")

	h.Require.NoError(h.Execute("harness", "usage", "--team", "team-a"))
	h.Require.Equal([]string{"team-a"}, harnessUsageLastQuery(h, api)["team_id"], "by ID too")

	h.Require.ErrorContains(h.Execute("harness", "usage", "--team", "Sales"),
		`no team of yours matched "Sales"; your teams are "Engineering", "Research"`)
}

func Test_Harness_Usage_RejectsBadFlags(t *testing.T) {
	h, _ := newHarnessUsageHarness(t, harnessUsageNow, harnessUsageMonth())

	h.Require.ErrorContains(h.Execute("route", "usage", "--since", "1d", "--start", "2026-09-01T00:00:00Z"), "--since cannot be combined")
	h.Require.ErrorContains(h.Execute("route", "usage", "--start", "2026-09-10T00:00:00Z", "--end", "2026-09-01T00:00:00Z"), "--start must be earlier than --end")
	h.Require.ErrorContains(h.Execute("route", "usage", "--start", "2026-10-01T00:00:00Z"), "--start must be in the past")
	h.Require.ErrorContains(h.Execute("route", "usage", "--limit", "-1"), "--limit must be zero")
	h.Require.ErrorContains(h.Execute("route", "usage", "--page-size", "32"), "--page-size must be between 1 and 31")
	for _, flag := range []string{"--since", "--start", "--group-by", "--user-id", "--model"} {
		h.Require.ErrorContains(h.Execute("harness", "usage", flag, "x"), "unknown flag: "+flag, "only this month, every model, your own usage")
	}
}

func Test_Harness_Usage_JSONStreamsBucketsAsReturned(t *testing.T) {
	h, _ := newHarnessUsageHarness(t, harnessUsageNow, harnessUsageMonth())

	h.Require.NoError(h.Execute("harness", "usage", "--output", "json"))
	var got []struct {
		Date    string `json:"date"`
		Results []struct {
			TeamID  string `json:"team_id"`
			Model   string `json:"model"`
			CostUSD string `json:"cost_usd"`
			Input   int    `json:"input_tokens"`
		} `json:"results"`
	}
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &got))
	h.Require.Len(got, 3, "one record per day, including days with no usage")
	h.Require.Equal("2026-09-02", got[1].Date)
	h.Require.Empty(got[1].Results)
	h.Require.Equal("team-a", got[0].Results[0].TeamID)
	h.Require.Equal("claude-sonnet-5", got[0].Results[0].Model)
	h.Require.Equal("1.25", got[0].Results[0].CostUSD, "costs pass through as exact decimal strings")
	h.Require.Equal(1000, got[0].Results[0].Input, "tokens stay in JSON")
	h.Require.Empty(h.Stderr.String())

	h.Require.NoError(h.Execute("harness", "usage", "--output", "jsonl", "--jq", ".date"))
	h.Require.Equal("\"2026-09-01\"\n\"2026-09-02\"\n\"2026-09-03\"\n", h.Stdout.String())
}

func Test_Harness_Usage_NullCostIsUnpriced(t *testing.T) {
	body := harnessUsageBody(false, nil, harnessUsageBucket("2026-09-01",
		harnessUsageModel("claude-opus-5-5", "0.0012", 10, 0, 1),
		harnessUsageModel("qwen-custom", nil, 50, 0, 5),
		harnessUsageModel("gpt-6-luna", "0", 0, 0, 0),
	))
	h, _ := newHarnessUsageHarness(t, harnessUsageNow, body)

	h.Require.NoError(h.Execute("harness", "usage"))
	out := h.Stdout.String()
	h.Require.Equal([]string{"●", "claude-opus-5-5", "<$1"}, harnessUsageRow(h, out, "claude-opus-5-5"), "tiny costs stay visible; no share when some usage is unpriced")
	h.Require.Equal([]string{"●", "qwen-custom", "-"}, harnessUsageRow(h, out, "qwen-custom"))
	h.Require.NotContains(out, "gpt-6-luna", "usage without tokens or cost is hidden")

	body["items"].([]any)[0].(map[string]any)["results"] = []any{harnessUsageModel("gpt-5.2", "abc", 10, 0, 1)}
	h.Require.ErrorContains(h.Execute("harness", "usage"), `invalid cost "abc"`)
}

func Test_Harness_Usage_Empty(t *testing.T) {
	h, api := newHarnessUsageHarness(t, harnessUsageNow, harnessUsageBody(false, nil, harnessUsageBucket("2026-09-01")))
	api.SetRoute("GET", "/v1/routes/settings/users/me/teams", 200, map[string]any{"teams": []any{}})

	h.Require.NoError(h.Execute("harness", "usage"))
	h.Require.Empty(h.Stdout.String())
	h.Require.Equal("No usage this month.\n", h.Stderr.String())

	api.SetRoute("GET", "/v1/routes/settings/users/me/teams", 404, map[string]string{"message": "not found"})
	h.Require.ErrorContains(h.Execute("harness", "usage"), "getting your teams: ")
	h.Require.Contains(h.Stderr.String(), "If you're using a team or workspace API key, run 'baseten auth login'",
		"a service-account key has no teams or routes usage of its own")
}

func Test_Harness_Usage_Chart(t *testing.T) {
	first := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	out := cmd.RenderUsageChart(first, []string{"a", "b"}, [][]float64{{1, 2, 0.5}, {0.25, 0.25, 0.25}}, 24)
	h := NewCommandHarness(t)
	h.Require.Contains(out, "<0>╭</0>", "the first series in the first color")
	h.Require.Contains(out, "<1>─</1>", "the second in the second")
	plain := regexp.MustCompile(`</?[0-9]>`).ReplaceAllString(out, "")
	h.Require.Equal(strings.Join([]string{
		"$2 ┤      ╭─────╮",
		"$1 ┤──────╯─────╰─────",
		"$0 ┼",
		"    Sep 1       Sep 3",
		"",
	}, "\n"), plain, "a step per day, a row per dollar, the largest series on top, labels every other day")

	h.Require.Empty(cmd.RenderUsageChart(first, []string{"a"}, [][]float64{{1, 2, 3}}, 9), "too narrow for two columns per day")
	h.Require.Contains(cmd.RenderUsageChart(first, []string{"a"}, [][]float64{{0.001}}, 40), "$1 ┤", "at least a dollar tall")
	h.Require.Contains(cmd.RenderUsageChart(first, []string{"a"}, [][]float64{{1500}}, 60), "$1,500 ┤", "large amounts in whole dollars")
	wide := regexp.MustCompile(`</?[0-9]>`).ReplaceAllString(cmd.RenderUsageChart(first, []string{"a"}, [][]float64{{30}}, 200), "")
	h.Require.Contains(wide, "$30 ┤"+strings.Repeat("─", 48)+"\n", "no wider than it needs to be on a wide terminal")
}

func Test_Route_Usage_WindowFlags(t *testing.T) {
	for _, tc := range []struct {
		name       string
		args       []string
		start, end string
	}{
		{"since snaps to whole days", []string{"--since", "7d"}, "2026-09-18", "2026-09-26"},
		{"start and end on day boundaries", []string{"--start", "2026-08-01T00:00:00Z", "--end", "2026-09-01T00:00:00Z"}, "2026-08-01", "2026-09-01"},
		{"end mid-day includes that day", []string{"--start", "2026-09-10T00:00:00Z", "--end", "2026-09-12T06:00:00Z"}, "2026-09-10", "2026-09-13"},
		{"start only runs to now", []string{"--start", "2026-09-20T12:00:00Z"}, "2026-09-20", "2026-09-26"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, api := newHarnessUsageHarness(t, harnessUsageNow, harnessUsageMonth())
			h.Require.NoError(h.Execute(append([]string{"route", "usage"}, tc.args...)...))
			q := harnessUsageLastQuery(h, api)
			h.Require.Equal([]string{tc.start}, q["start_date"])
			h.Require.Equal([]string{tc.end}, q["end_date"])
		})
	}
}

func Test_Route_Usage_ClampsToRetention(t *testing.T) {
	h, api := newHarnessUsageHarness(t, time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC), harnessUsageMonth())

	h.Require.NoError(h.Execute("route", "usage", "--start", "2026-06-01T00:00:00Z", "--end", "2026-07-01T00:00:00Z"))
	h.Require.Equal([]string{"2026-06-29"}, harnessUsageLastQuery(h, api)["start_date"], "92 days including today")
	h.Require.Contains(h.Stderr.String(), "Usage is kept for 92 days, so this only includes usage from 2026-06-29 on.")

	h.Require.ErrorContains(h.Execute("route", "usage", "--start", "2026-05-01T00:00:00Z", "--end", "2026-06-01T00:00:00Z"),
		"older than the 92 days of usage Baseten keeps")

	h.Require.NoError(h.Execute("route", "usage", "--since", "30d"))
	h.Require.NotContains(h.Stderr.String(), "Usage is kept")
}
