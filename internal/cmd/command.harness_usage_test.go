package cmd_test

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/basetenlabs/baseten-cli/internal/cmd"
)

func harnessUsageModel(model string, cost any, input, cached, output int) map[string]any {
	return map[string]any{
		"model":                 model,
		"cost_usd":              cost,
		"input_tokens":          input,
		"cached_input_tokens":   cached,
		"uncached_input_tokens": input - cached,
		"output_tokens":         output,
	}
}

func harnessUsageBody(buckets ...map[string]any) map[string]any {
	return map[string]any{"items": buckets, "pagination": map[string]any{"has_more": false}}
}

func harnessUsageBucket(date string, results ...map[string]any) map[string]any {
	return map[string]any{"date": date, "results": append([]map[string]any{}, results...)}
}

// harnessTeamSettings is your settings in a team: spent of limit, the team's
// per-member default (nil for none), and what blocks you (nil for nothing).
func harnessTeamSettings(id, name, spent string, limit, teamDefault, blockedBy any) map[string]any {
	var def any
	if teamDefault != nil {
		def = map[string]any{"per_member_monthly_limit_usd": teamDefault}
	}
	return map[string]any{
		"team_id": id, "team_name": name, "month_spend_usd": spent, "is_blocked": blockedBy != nil, "blocked_by": blockedBy,
		"spend_limit": map[string]any{"effective": map[string]any{"monthly_limit_usd": limit}, "team_default": def},
	}
}

// harnessUsageNow is the pinned clock for these tests, mid-afternoon UTC.
var harnessUsageNow = time.Date(2026, 9, 25, 15, 0, 0, 0, time.UTC)

func newHarnessUsageHarness(t *testing.T, now time.Time, body map[string]any) (*CommandHarness, *MockManagementAPI) {
	h := NewCommandHarness(t)
	h.Context = cmd.WithNow(h.Context, func() time.Time { return now })
	api := h.MockManagementAPI()
	api.SetRoute("GET", "/v1/users/me", 200, map[string]string{"user_id": "user-a"})
	api.SetRoute("GET", "/v1/routes/usage", 200, body)
	api.SetRoute("GET", "/v1/routes/settings/users/me/teams", 200, map[string]any{"teams": []any{
		harnessTeamSettings("team-a", "Engineering", "1.75", "100", "100", nil),
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

func harnessUsageMonth() map[string]any {
	return harnessUsageBody(
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
	h.Require.Equal([]string{"TEAM", "MODEL"}, q["group_by"])
	h.Require.Equal([]string{"team-a"}, q["team_id"], "your only team needs no --team")
	h.Require.Equal("September 2026 · resets Oct 1\n", h.Stderr.String())
}

func Test_Harness_Usage_Teams(t *testing.T) {
	h, api := newHarnessUsageHarness(t, harnessUsageNow, harnessUsageMonth())
	api.SetRoute("GET", "/v1/routes/settings/users/me/teams", 200, map[string]any{"teams": []any{
		harnessTeamSettings("team-a", "Engineering", "1.75", "100", "100", nil),
		harnessTeamSettings("team-b", "Research", "12.5", "10", "100.00", "member"),
		harnessTeamSettings("team-c", "FDE", "80", "100", "100", "team"),
		harnessTeamSettings("team-d", "Infra", "0", nil, nil, nil),
	}})

	h.Require.ErrorContains(h.Execute("harness", "usage"), `you're in 4 teams; pass --team with one of "Engineering", "Research", "FDE", "Infra"`)
	h.Require.ErrorContains(h.Execute("harness", "usage", "--team", "Sales"), `no team of yours matched "Sales"; your teams are "Engineering", "Research", "FDE", "Infra"`)

	h.Require.NoError(h.Execute("harness", "usage", "--team", "Engineering"))
	h.Require.Equal([]string{"team-a"}, harnessUsageLastQuery(h, api)["team_id"], "--team also narrows the usage")
	out := h.Stdout.String()
	h.Require.Regexp(`(?m)^Engineering +\$2 of \$100\n  ● claude-sonnet-5 +\$1 +\(56%\)\n  ● glm-5.2 +\$1 +\(22%\)\n  ● gpt-5.2 +\$1 +\(22%\)\n$`, out,
		"whole dollars, half a dollar rounds up, days summed, models most expensive first then by name, amounts lined up")
	h.Require.NotContains(out, "┤", "no chart when stdout isn't a terminal")
	h.Require.NotContains(out, "\x1b[", "no color when stdout isn't a terminal")

	h.Require.NoError(h.Execute("harness", "usage", "--team", "team-b"))
	h.Require.Equal([]string{"Research", "$13", "of", "$10", "(team", "default", "$100)", "·", "limit", "reached"},
		harnessUsageRow(h, h.Stdout.String(), "Research"), "--team by ID too; the team default only when yours differs")
	h.Require.NoError(h.Execute("harness", "usage", "--team", "FDE"))
	h.Require.Equal([]string{"FDE", "$80", "of", "$100", "·", "team", "limit", "reached"}, harnessUsageRow(h, h.Stdout.String(), "FDE"))
	h.Require.NoError(h.Execute("harness", "usage", "--team", "Infra"))
	h.Require.Equal([]string{"Infra", "$0", "·", "no", "limit"}, harnessUsageRow(h, h.Stdout.String(), "Infra"))
}

func Test_Harness_Usage_JSONStreamsBucketsAsReturned(t *testing.T) {
	h, _ := newHarnessUsageHarness(t, harnessUsageNow, harnessUsageMonth())

	h.Require.NoError(h.Execute("harness", "usage", "--output", "json"))
	var got []struct {
		Results []struct {
			CostUSD string `json:"cost_usd"`
		} `json:"results"`
	}
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &got))
	h.Require.Len(got, 3, "one record per day, including days with no usage")
	h.Require.Equal("1.25", got[0].Results[0].CostUSD, "costs pass through as exact decimal strings")
	h.Require.Empty(h.Stderr.String())

	h.Require.NoError(h.Execute("harness", "usage", "--output", "jsonl", "--jq", ".date"))
	h.Require.Equal("\"2026-09-01\"\n\"2026-09-02\"\n\"2026-09-03\"\n", h.Stdout.String())
}

func Test_Harness_Usage_NullCostIsUnpriced(t *testing.T) {
	h, api := newHarnessUsageHarness(t, harnessUsageNow, harnessUsageBody(harnessUsageBucket("2026-09-01",
		harnessUsageModel("claude-opus-5-5", "0.0012", 10, 0, 1),
		harnessUsageModel("qwen-custom", nil, 50, 0, 5),
		harnessUsageModel("gpt-6-luna", "0", 0, 0, 0),
	)))

	h.Require.NoError(h.Execute("harness", "usage"))
	out := h.Stdout.String()
	h.Require.Equal([]string{"●", "claude-opus-5-5", "<$1"}, harnessUsageRow(h, out, "claude-opus-5-5"), "tiny costs stay visible; no share when some usage is unpriced")
	h.Require.Equal([]string{"●", "qwen-custom", "-"}, harnessUsageRow(h, out, "qwen-custom"))
	h.Require.NotContains(out, "gpt-6-luna", "usage without tokens or cost is hidden")

	api.SetRoute("GET", "/v1/routes/usage", 200, harnessUsageBody(harnessUsageBucket("2026-09-01", harnessUsageModel("gpt-5.2", "abc", 10, 0, 1))))
	h.Require.ErrorContains(h.Execute("harness", "usage"), `invalid cost "abc"`)
}

func Test_Harness_Usage_Empty(t *testing.T) {
	h, api := newHarnessUsageHarness(t, harnessUsageNow, harnessUsageBody(harnessUsageBucket("2026-09-01")))
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
	strip := regexp.MustCompile(`</?[0-9]>`)
	out := cmd.RenderUsageChart(first, [][]float64{{1, 2, 0.5}, {0.25, 0.25, 0.25}}, 24)
	h := NewCommandHarness(t)
	h.Require.Contains(out, "<0>╭</0>", "the first series in the first color")
	h.Require.Contains(out, "<1>─</1>", "the second in the second")
	h.Require.Equal("$2 ┤      ╭─────╮\n$1 ┤──────╯─────╰─────\n$0 ┼\n    Sep 1       Sep 3\n", strip.ReplaceAllString(out, ""),
		"a step per day, a row per dollar, the largest series on top, labels every other day")

	h.Require.Empty(cmd.RenderUsageChart(first, [][]float64{{1, 2, 3}}, 9), "too narrow for two columns per day")
	h.Require.Contains(cmd.RenderUsageChart(first, [][]float64{{0.001}}, 40), "$1 ┤", "at least a dollar tall")
	h.Require.Contains(cmd.RenderUsageChart(first, [][]float64{{1500}}, 60), "$1,500 ┤", "large amounts in whole dollars")
	h.Require.Contains(strip.ReplaceAllString(cmd.RenderUsageChart(first, [][]float64{{30}}, 200), ""), "$30 ┤"+strings.Repeat("─", 48)+"\n",
		"no wider than it needs to be on a wide terminal")
}
