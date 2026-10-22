package cmd_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/basetenlabs/baseten-cli/internal/cmd"
)

func gridUsageModel(model string, cost any, input, cached, output int) map[string]any {
	return map[string]any{
		"team_id":               "team-a",
		"model":                 model,
		"cost_usd":              cost,
		"input_tokens":          input,
		"cached_input_tokens":   cached,
		"uncached_input_tokens": input - cached,
		"output_tokens":         output,
	}
}

func gridUsageBody(buckets ...map[string]any) map[string]any {
	return map[string]any{"items": buckets, "pagination": map[string]any{"has_more": false}}
}

func gridUsageBucket(date string, results ...map[string]any) map[string]any {
	return map[string]any{"date": date, "results": append([]map[string]any{}, results...)}
}

// gridTeamSettings is your settings in a team: spent of limit, the team's
// per-member default (nil for none), and what blocks you (nil for nothing).
func gridTeamSettings(id, name, spent string, limit, teamDefault, blockedBy any) map[string]any {
	var def any
	if teamDefault != nil {
		def = map[string]any{"per_member_monthly_limit_usd": teamDefault}
	}
	return map[string]any{
		"team_id": id, "team_name": name, "month_spend_usd": spent, "is_blocked": blockedBy != nil, "blocked_by": blockedBy,
		"spend_limit": map[string]any{"effective": map[string]any{"monthly_limit_usd": limit}, "team_default": def},
	}
}

// gridUsageNow is the pinned clock for these tests, mid-afternoon UTC.
var gridUsageNow = time.Date(2026, 9, 25, 15, 0, 0, 0, time.UTC)

func newGridUsageHarness(t *testing.T, now time.Time, body map[string]any) (*CommandHarness, *MockManagementAPI) {
	h := NewCommandHarness(t)
	h.Context = cmd.WithNow(h.Context, func() time.Time { return now })
	api := h.MockManagementAPI()
	api.SetRoute("GET", "/v1/users/me", 200, map[string]string{"user_id": "user-a"})
	api.SetRoute("GET", "/v1/grid/routes/usage", 200, body)
	gridSetTeams(api, gridTeamSettings("team-a", "Engineering", "1.75", "100", "100", nil))
	return h, api
}

// gridSetTeams serves teams as yours, with your settings in each.
func gridSetTeams(api *MockManagementAPI, teams ...map[string]any) {
	listing := make([]any, len(teams))
	settings := make(map[string]map[string]any, len(teams))
	for i, t := range teams {
		listing[i] = map[string]any{"id": t["team_id"], "name": t["team_name"], "default": false, "created_at": "2026-01-01T00:00:00Z"}
		settings[t["team_id"].(string)] = t
	}
	api.SetRoute("GET", "/v1/teams", 200, map[string]any{"teams": listing})
	api.SetRouteFunc("GET", "/v1/grid/team_settings/users/me", func(w http.ResponseWriter, r *http.Request) {
		team, ok := settings[r.URL.Query().Get("team_id")]
		if !ok {
			http.Error(w, "unknown team", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(team)
	})
}

// gridUsageLastQuery returns the query of the last usage request.
func gridUsageLastQuery(h *CommandHarness, api *MockManagementAPI) map[string][]string {
	calls := api.Calls()
	for i := len(calls) - 1; i >= 0; i-- {
		if calls[i].Path == "/v1/grid/routes/usage" {
			return calls[i].Query()
		}
	}
	h.Require.Fail("no usage request")
	return nil
}

// gridUsageRow returns the fields of the first output line containing
// marker.
func gridUsageRow(h *CommandHarness, out, marker string) []string {
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, marker) {
			return strings.Fields(line)
		}
	}
	h.Require.Fail("no output line contains " + marker)
	return nil
}

func gridUsageMonth() map[string]any {
	return gridUsageBody(
		gridUsageBucket("2026-09-01",
			gridUsageModel("claude-sonnet-5", "1.25", 1000, 800, 100),
			gridUsageModel("gpt-5.2", "0.50", 400, 0, 40),
		),
		gridUsageBucket("2026-09-02"),
		gridUsageBucket("2026-09-03",
			gridUsageModel("claude-sonnet-5", "0.0000000001", 100, 0, 10),
			gridUsageModel("glm-5.2", "0.50", 50, 0, 5),
		),
	)
}

func Test_Grid_Usage_DefaultsToMonthToDateForCurrentUser(t *testing.T) {
	h, api := newGridUsageHarness(t, gridUsageNow, gridUsageMonth())

	h.Require.NoError(h.Execute("grid", "usage"))
	q := gridUsageLastQuery(h, api)
	h.Require.Equal([]string{"2026-09-01"}, q["start_date"])
	h.Require.Equal([]string{"2026-09-26"}, q["end_date"], "today's usage is included")
	h.Require.Equal([]string{"user-a"}, q["user_ids"], "admins must not see other users' usage by default")
	h.Require.Equal([]string{"TEAM", "MODEL"}, q["group_by"])
	h.Require.Nil(q["team_id"], "every team without --team")
	h.Require.Equal("September 2026 · resets Oct 1\n", h.Stderr.String())
}

func Test_Grid_Usage_Teams(t *testing.T) {
	h, api := newGridUsageHarness(t, gridUsageNow, gridUsageMonth())
	gridSetTeams(api,
		gridTeamSettings("team-a", "Engineering", "1.75", "100", "100", nil),
		gridTeamSettings("team-b", "Research", "12.5", "10", "100.00", "member"),
		gridTeamSettings("team-c", "FDE", "80", "100", "100", "team"),
		gridTeamSettings("team-d", "Infra", "0", nil, nil, nil),
	)

	h.Require.ErrorContains(h.Execute("grid", "usage", "--team", "Sales"), `no team of yours matched "Sales"; your teams are "Engineering", "Research", "FDE", "Infra"`)

	h.Require.NoError(h.Execute("grid", "usage", "--team", "Engineering"))
	h.Require.Equal([]string{"team-a"}, gridUsageLastQuery(h, api)["team_id"], "--team also narrows the usage")
	out := h.Stdout.String()
	h.Require.Regexp(`(?m)^Engineering +\$2 of \$100\n  ● claude-sonnet-5 +\$1 +\(56%\)\n  ● glm-5.2 +\$1 +\(22%\)\n  ● gpt-5.2 +\$1 +\(22%\)\n$`, out,
		"whole dollars, half a dollar rounds up, days summed, models most expensive first then by name, amounts lined up")
	h.Require.NotContains(out, "┤", "no chart when stdout isn't a terminal")
	h.Require.NotContains(out, "\x1b[", "no color when stdout isn't a terminal")

	body := gridUsageMonth()
	body["items"].([]map[string]any)[0]["results"].([]map[string]any)[1]["team_id"] = "team-b"
	body["items"].([]map[string]any)[2]["results"].([]map[string]any)[1]["team_id"] = "team-z"
	api.SetRoute("GET", "/v1/grid/routes/usage", 200, body)
	h.Require.NoError(h.Execute("grid", "usage"))
	h.Require.Nil(gridUsageLastQuery(h, api)["team_id"])
	h.Require.Regexp(`(?m)^Engineering +\$2 of \$100\n  ● claude-sonnet-5 +\$1 +\(100%\)\n\nResearch +\$13 of \$10 .*\n  ● gpt-5.2 +\$1 +\(100%\)\n\nFDE +\$80 of \$100 .*\n\nInfra +\$0 · no limit\n$`, h.Stdout.String(),
		"every team without --team, usage split by team, a team you're no longer in left out")

	h.Require.NoError(h.Execute("grid", "usage", "--team", "team-b"))
	h.Require.Equal([]string{"Research", "$13", "of", "$10", "(team", "default", "$100)", "·", "limit", "reached"},
		gridUsageRow(h, h.Stdout.String(), "Research"), "--team by ID too; the team default only when yours differs")
	h.Require.NotContains(h.Stdout.String(), "Engineering", "only the named team's usage")
	h.Require.NoError(h.Execute("grid", "usage", "--team", "FDE"))
	h.Require.Equal([]string{"FDE", "$80", "of", "$100", "·", "team", "limit", "reached"}, gridUsageRow(h, h.Stdout.String(), "FDE"))
	h.Require.NoError(h.Execute("grid", "usage", "--team", "Infra"))
	h.Require.Equal([]string{"Infra", "$0", "·", "no", "limit"}, gridUsageRow(h, h.Stdout.String(), "Infra"))
}

func Test_Grid_Usage_Window(t *testing.T) {
	h, api := newGridUsageHarness(t, gridUsageNow, gridUsageBody(gridUsageBucket("2026-08-31", gridUsageModel("gpt-5.2", "40", 400, 0, 40))))

	h.Require.NoError(h.Execute("grid", "usage", "--start", "2026-08-01T00:00:00Z", "--end", "2026-09-01T00:00:00Z"))
	q := gridUsageLastQuery(h, api)
	h.Require.Equal([]string{"2026-08-01"}, q["start_date"])
	h.Require.Equal([]string{"2026-09-01"}, q["end_date"])
	h.Require.Equal("Aug 1 to Aug 31, 2026\n", h.Stderr.String(), "the window, with no reset date")
	h.Require.Equal([]string{"Engineering", "$40"}, gridUsageRow(h, h.Stdout.String(), "Engineering"),
		"the usage summed, not this month's spend, and no limit, which is this month's")

	api.SetRoute("GET", "/v1/grid/routes/usage", 200, gridUsageBody(gridUsageBucket("2026-09-20", gridUsageModel("gpt-5.2", "4", 40, 0, 4))))
	h.Require.NoError(h.Execute("grid", "usage", "--since", "7d"))
	q = gridUsageLastQuery(h, api)
	h.Require.Equal([]string{"2026-09-18", "2026-09-26"}, []string{q["start_date"][0], q["end_date"][0]}, "the same window flags as route usage")
	h.Require.ErrorContains(h.Execute("grid", "usage", "--since", "1d", "--start", "2026-09-01T00:00:00Z"), "--since cannot be combined")
}

func Test_Grid_Usage_JSONStreamsBucketsAsReturned(t *testing.T) {
	h, _ := newGridUsageHarness(t, gridUsageNow, gridUsageMonth())

	h.Require.NoError(h.Execute("grid", "usage", "--output", "json"))
	var got []struct {
		Results []struct {
			CostUSD string `json:"cost_usd"`
		} `json:"results"`
	}
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &got))
	h.Require.Len(got, 3, "one record per day, including days with no usage")
	h.Require.Equal("1.25", got[0].Results[0].CostUSD, "costs pass through as exact decimal strings")
	h.Require.Empty(h.Stderr.String())

	h.Require.NoError(h.Execute("grid", "usage", "--output", "jsonl", "--jq", ".date"))
	h.Require.Equal("\"2026-09-01\"\n\"2026-09-02\"\n\"2026-09-03\"\n", h.Stdout.String())
}

func Test_Grid_Usage_FoldsModelsPastTopFiveIntoOther(t *testing.T) {
	h, _ := newGridUsageHarness(t, gridUsageNow, gridUsageBody(gridUsageBucket("2026-09-01",
		gridUsageModel("m1", "70", 10, 0, 1), gridUsageModel("m2", "60", 10, 0, 1), gridUsageModel("m3", "50", 10, 0, 1),
		gridUsageModel("m4", "40", 10, 0, 1), gridUsageModel("m5", "30", 10, 0, 1), gridUsageModel("m6", "20", 10, 0, 1),
		gridUsageModel("m7", "10", 10, 0, 1),
	)))

	h.Require.NoError(h.Execute("grid", "usage"))
	out := h.Stdout.String()
	h.Require.Regexp(`(?m)^  ● m5 +\$30 +\(11%\)\n  ● other +\$30 +\(11%\)\n$`, out, "the sixth model on is summed into a last \"other\" row")
	h.Require.NotContains(out, "m6")
}

func Test_Grid_Usage_NullCostIsUnpriced(t *testing.T) {
	h, api := newGridUsageHarness(t, gridUsageNow, gridUsageBody(gridUsageBucket("2026-09-01",
		gridUsageModel("claude-opus-5-5", "0.0012", 10, 0, 1),
		gridUsageModel("qwen-custom", nil, 50, 0, 5),
		gridUsageModel("gpt-6-luna", "0", 0, 0, 0),
	)))

	h.Require.NoError(h.Execute("grid", "usage"))
	out := h.Stdout.String()
	h.Require.Equal([]string{"●", "claude-opus-5-5", "<$1"}, gridUsageRow(h, out, "claude-opus-5-5"), "tiny costs stay visible; no share when some usage is unpriced")
	h.Require.Equal([]string{"●", "qwen-custom", "-"}, gridUsageRow(h, out, "qwen-custom"))
	h.Require.NotContains(out, "gpt-6-luna", "usage without tokens or cost is hidden")

	api.SetRoute("GET", "/v1/grid/routes/usage", 200, gridUsageBody(gridUsageBucket("2026-09-01", gridUsageModel("gpt-5.2", "abc", 10, 0, 1))))
	h.Require.ErrorContains(h.Execute("grid", "usage"), `invalid cost "abc"`)
}

func Test_Grid_Usage_Empty(t *testing.T) {
	h, api := newGridUsageHarness(t, gridUsageNow, gridUsageBody(gridUsageBucket("2026-09-01")))

	h.Require.NoError(h.Execute("grid", "usage"))
	h.Require.Equal([]string{"Engineering", "$2", "of", "$100"}, gridUsageRow(h, h.Stdout.String(), "Engineering"), "this month's spend from the settings, not the empty usage")
	gridSetTeams(api)

	h.Require.NoError(h.Execute("grid", "usage"))
	h.Require.Empty(h.Stdout.String())
	h.Require.Equal("No usage this month.\n", h.Stderr.String())
	h.Require.NoError(h.Execute("grid", "usage", "--output", "json"))
	h.Require.Equal("[]\n", h.Stdout.String(), "still valid JSON with no teams")

	gridSetTeams(api, gridTeamSettings("team-a", "Engineering", "1.75", "100", "100", nil))
	api.SetRoute("GET", "/v1/grid/team_settings/users/me", 404, map[string]string{"message": "not found"})
	h.Require.ErrorContains(h.Execute("grid", "usage"), "getting your settings in Engineering: ")
	h.Require.Contains(h.Stderr.String(), "If you're using a team or workspace API key, run 'baseten auth login'",
		"a service-account key has no teams or routes usage of its own")
}

func Test_Grid_Usage_UsesPerTeamUserSettings(t *testing.T) {
	h, api := newGridUsageHarness(t, gridUsageNow, gridUsageMonth())
	gridSetTeams(api,
		gridTeamSettings("team-a", "Engineering", "1.75", "100", "100", nil),
		gridTeamSettings("team-b", "Research", "0", nil, nil, nil),
	)
	h.Require.NoError(h.Execute("grid", "usage", "--output", "json"))
	h.Require.Equal(1, countCalls(api, "GET", "/v1/teams"))
	var teams []string
	for _, call := range api.Calls() {
		if call.Path == "/v1/grid/team_settings/users/me" {
			teams = append(teams, call.Query().Get("team_id"))
		}
		h.Require.NotContains(call.Path, "/settings/users/")
	}
	h.Require.Equal([]string{"team-a", "team-b"}, teams)
	h.Require.Nil(api.FindCall("GET", "/v1/grid/team_settings/users"))
}
