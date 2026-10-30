package cmd_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/basetenlabs/baseten-cli/internal/cmd"
)

func Test_Grid_Route_Usage_DefaultsMatchAPI(t *testing.T) {
	h, api := newGridUsageHarness(t, gridUsageNow, gridUsageMonth())

	h.Require.NoError(h.Execute("grid", "route", "usage"))
	q := gridUsageLastQuery(h, api)
	h.Require.Equal([]string{"2026-09-24"}, q["start_date"], "the previous UTC day")
	h.Require.Equal([]string{"2026-09-26"}, q["end_date"], "through today")
	h.Require.Equal([]string{"MODEL"}, q["group_by"])
	h.Require.Empty(q["user_ids"], "no default user filter")
	h.Require.Nil(api.FindCall("GET", "/v1/users/me"))
}

func Test_Grid_Route_Usage_EndOnlySpansTheDayBefore(t *testing.T) {
	h, api := newGridUsageHarness(t, gridUsageNow, gridUsageMonth())

	h.Require.NoError(h.Execute("grid", "route", "usage", "--end", "2026-09-10T00:00:00Z"))
	q := gridUsageLastQuery(h, api)
	h.Require.Equal([]string{"2026-09-09"}, q["start_date"])
	h.Require.Equal([]string{"2026-09-10"}, q["end_date"])
}

func Test_Grid_Route_Usage_TimeSeriesTable(t *testing.T) {
	h, _ := newGridUsageHarness(t, gridUsageNow, gridUsageMonth())

	h.Require.NoError(h.Execute("grid", "route", "usage", "--since", "7d"))
	lines := strings.Split(strings.TrimRight(h.Stdout.String(), "\n"), "\n")
	fields := func(i int) []string { return strings.Fields(lines[i]) }
	h.Require.Equal([]string{"DATE", "MODEL", "INPUT", "CACHED", "OUTPUT", "COST"}, fields(0))
	h.Require.Equal([]string{"2026-09-01", "claude-sonnet-5", "1,000", "800", "100", "$1.25"}, fields(1))
	h.Require.Equal([]string{"gpt-5.2", "400", "0", "40", "$0.50"}, fields(2), "a day's later rows blank the date")
	h.Require.Equal([]string{"2026-09-02", "(no", "usage)"}, fields(3), "an empty day keeps its row")
	h.Require.Equal([]string{"2026-09-03", "claude-sonnet-5", "100", "0", "10", "<$0.01"}, fields(4))
	h.Require.Equal([]string{"glm-5.2", "50", "0", "5", "$0.50"}, fields(5))
	h.Require.Equal([]string{"ALL", "1,550", "800", "155", "$2.25"}, fields(6))
	h.Require.Contains(h.Stderr.String(), "Window: 2026-09-01 through 2026-09-03 UTC · grouped by model")
}

func Test_Grid_Route_Usage_KeepsRowsWithNoTokens(t *testing.T) {
	body := gridUsageBody(gridUsageBucket("2026-09-24",
		gridUsageModel("claude-opus-5-5", "0.14", 10, 0, 1),
		gridUsageModel("gpt-6-luna", "0", 0, 0, 0),
	))
	h, _ := newGridUsageHarness(t, gridUsageNow, body)

	h.Require.NoError(h.Execute("grid", "route", "usage"))
	h.Require.Equal([]string{"gpt-6-luna", "0", "0", "0", "$0.00"}, gridUsageRow(h, h.Stdout.String(), "gpt-6-luna"), "shows what the API returns")
}

func Test_Grid_Route_Usage_FiltersPassThrough(t *testing.T) {
	h, api := newGridUsageHarness(t, gridUsageNow, gridUsageMonth())

	h.Require.NoError(h.Execute("grid", "route", "usage", "--user-id", "user-b", "--group-by", "user,provider", "--model", "glm-5.2", "--provider", "xai", "--harness", "codex"))
	q := gridUsageLastQuery(h, api)
	h.Require.Equal([]string{"user-b"}, q["user_ids"])
	h.Require.Equal([]string{"USER", "PROVIDER"}, q["group_by"])
	h.Require.Equal([]string{"glm-5.2"}, q["models"])
	h.Require.Equal([]string{"XAI"}, q["providers"])
	h.Require.Equal([]string{"CODEX"}, q["harnesses"], "kebab flags map to the API enum")
}

func Test_Grid_Route_Usage_JSONStreamsBuckets(t *testing.T) {
	h, _ := newGridUsageHarness(t, gridUsageNow, gridUsageMonth())

	h.Require.NoError(h.Execute("grid", "route", "usage", "--output", "json"))
	var got []struct {
		Date    string            `json:"date"`
		Results []json.RawMessage `json:"results"`
	}
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &got))
	h.Require.Len(got, 3)
	h.Require.Len(got[0].Results, 2)
	h.Require.Empty(got[1].Results)
}

func Test_Grid_Route_Usage_EmptyHasNoAPIKeyHint(t *testing.T) {
	body := gridUsageBody(gridUsageBucket("2026-09-24"))
	h, _ := newGridUsageHarness(t, gridUsageNow, body)

	h.Require.NoError(h.Execute("grid", "route", "usage"))
	h.Require.Empty(h.Stdout.String())
	h.Require.Contains(h.Stderr.String(), "No usage in the selected window.")
	h.Require.NotContains(h.Stderr.String(), "team or workspace API key", "there's no default user filter to explain")
}

func Test_Grid_Route_Usage_PassesUserIDsThrough(t *testing.T) {
	h, api := newGridUsageHarness(t, gridUsageNow, gridUsageMonth())

	h.Require.NoError(h.Execute("grid", "route", "usage", "--user-id", "me", "--user-id", "user-b"))
	h.Require.Equal([]string{"me", "user-b"}, gridUsageLastQuery(h, api)["user_ids"], "sent as given, like the API")
	h.Require.Nil(api.FindCall("GET", "/v1/users/me"))
}

func Test_Grid_Route_Usage_WindowFlags(t *testing.T) {
	h, api := newGridUsageHarness(t, gridUsageNow, gridUsageMonth())
	window := func(args ...string) []string {
		h.Require.NoError(h.Execute(append([]string{"grid", "route", "usage"}, args...)...))
		q := gridUsageLastQuery(h, api)
		return []string{q["start_date"][0], q["end_date"][0]}
	}

	h.Require.Equal([]string{"2026-09-18", "2026-09-26"}, window("--since", "7d"), "whole days")
	h.Require.Equal([]string{"2026-08-01", "2026-09-01"}, window("--start", "2026-08-01T00:00:00Z", "--end", "2026-09-01T00:00:00Z"))
	h.Require.Equal([]string{"2026-09-10", "2026-09-13"}, window("--start", "2026-09-10T00:00:00Z", "--end", "2026-09-12T06:00:00Z"), "end mid-day includes that day")
	h.Require.Equal([]string{"2026-09-20", "2026-09-26"}, window("--start", "2026-09-20T12:00:00Z"), "start only runs to now")
}

func Test_Grid_Route_Usage_RejectsBadFlags(t *testing.T) {
	h, _ := newGridUsageHarness(t, gridUsageNow, gridUsageMonth())

	h.Require.ErrorContains(h.Execute("grid", "route", "usage", "--since", "1d", "--start", "2026-09-01T00:00:00Z"), "--since cannot be combined")
	h.Require.ErrorContains(h.Execute("grid", "route", "usage", "--start", "2026-09-10T00:00:00Z", "--end", "2026-09-01T00:00:00Z"), "--start must be earlier than --end")
	h.Require.ErrorContains(h.Execute("grid", "route", "usage", "--start", "2026-10-01T00:00:00Z"), "--start must be in the past")
	h.Require.ErrorContains(h.Execute("grid", "route", "usage", "--limit", "-1"), "--limit must be zero")
	h.Require.ErrorContains(h.Execute("grid", "route", "usage", "--page-size", "32"), "--page-size must be between 1 and 31")
}

func Test_Grid_Route_Usage_ClampsToRetention(t *testing.T) {
	h, api := newGridUsageHarness(t, time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC), gridUsageMonth())

	h.Require.NoError(h.Execute("grid", "route", "usage", "--start", "2026-06-01T00:00:00Z", "--end", "2026-07-01T00:00:00Z"))
	h.Require.Equal([]string{"2026-06-29"}, gridUsageLastQuery(h, api)["start_date"], "92 days including today")
	h.Require.Contains(h.Stderr.String(), "Usage is kept for 92 days, so this only includes usage from 2026-06-29 on.")

	h.Require.ErrorContains(h.Execute("grid", "route", "usage", "--start", "2026-05-01T00:00:00Z", "--end", "2026-06-01T00:00:00Z"),
		"older than the 92 days of usage Baseten keeps")
}

func Test_Grid_Route_Usage_TeamAndRouterDimensions(t *testing.T) {
	row := gridUsageModel("test-model-api", "0.25", 100, 50, 20)
	row["team_id"] = "team-a"
	row["team_name"] = "Engineering"
	row["router_id"] = "acme/router"
	body := gridUsageBody(gridUsageBucket("2026-09-24", row))
	for _, output := range []string{"text", "json", "jsonl"} {
		t.Run(output, func(t *testing.T) {
			h, m := newGridUsageHarness(t, gridUsageNow, body)
			h.Require.NoError(h.Execute("grid", "route", "usage", "--team", "Engineering", "--group-by", "team,router,model", "--output", output))
			query := gridUsageLastQuery(h, m)
			h.Require.Equal([]string{"TEAM", "ROUTER", "MODEL"}, query["group_by"])
			h.Require.Equal([]string{"team-a"}, query["team_id"])
			h.Require.Contains(h.Stdout.String(), "team-a")
			h.Require.Contains(h.Stdout.String(), "acme/router")
			if output == "text" {
				h.Require.Contains(h.Stdout.String(), "TEAM")
				h.Require.Contains(h.Stdout.String(), "ROUTER")
			} else {
				h.Require.Contains(h.Stdout.String(), "Engineering")
			}
		})
	}
}

func Test_Grid_Route_Usage_HarnessDimension(t *testing.T) {
	untracked := gridUsageModel("claude-sonnet-5", "1.25", 1000, 800, 100)
	untracked["harness"] = nil
	codex := gridUsageModel("gpt-5.2", "0.50", 400, 0, 40)
	codex["harness"] = "CODEX"
	h, m := newGridUsageHarness(t, gridUsageNow, gridUsageBody(gridUsageBucket("2026-09-24", untracked, codex)))

	h.Require.NoError(h.Execute("grid", "route", "usage", "--group-by", "harness"))

	h.Require.Equal([]string{"HARNESS"}, gridUsageLastQuery(h, m)["group_by"])
	h.Require.Contains(h.Stdout.String(), "HARNESS")
	h.Require.Equal([]string{"2026-09-24", "Unknown", "1,000", "800", "100", "$1.25"}, gridUsageRow(h, h.Stdout.String(), "Unknown"))
	h.Require.Equal([]string{"codex", "400", "0", "40", "$0.50"}, gridUsageRow(h, h.Stdout.String(), "codex"))
	h.Require.Contains(h.Stderr.String(), `Harness "Unknown" means no harness was recorded, including usage from before harness tracking.`)
}

func Test_Grid_Route_Usage_HarnessFilterPassesThrough(t *testing.T) {
	h, m := newGridUsageHarness(t, gridUsageNow, gridUsageMonth())

	h.Require.NoError(h.Execute("grid", "route", "usage",
		"--harness", "codex", "--harness", "claude-code"))

	h.Require.Equal([]string{"CODEX", "CLAUDE_CODE"}, gridUsageLastQuery(h, m)["harnesses"])
}

func Test_Grid_Route_Usage_OmittedTeamIsNotResolved(t *testing.T) {
	h, m := newGridUsageHarness(t, gridUsageNow, gridUsageMonth())
	h.Require.NoError(h.Execute("grid", "route", "usage", "--group-by", "team"))
	h.Require.NotContains(gridUsageLastQuery(h, m), "team_id")
	h.Require.Nil(m.FindCall("GET", "/v1/teams"))
}

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

// gridUsageNow is the pinned clock for these tests, mid-afternoon UTC.
var gridUsageNow = time.Date(2026, 9, 25, 15, 0, 0, 0, time.UTC)

func newGridUsageHarness(t *testing.T, now time.Time, body map[string]any) (*CommandHarness, *MockManagementAPI) {
	h := NewCommandHarness(t)
	h.Context = cmd.WithNow(h.Context, func() time.Time { return now })
	api := h.MockManagementAPI()
	api.SetRoute("GET", "/v1/users/me", 200, map[string]string{"user_id": "user-a"})
	api.SetRoute("GET", "/v1/grid/routes/usage", 200, body)
	api.SetRoute("GET", "/v1/teams", 200, map[string]any{"teams": []any{
		map[string]any{"id": "team-a", "name": "Engineering", "default": false, "created_at": "2026-01-01T00:00:00Z"},
	}})
	return h, api
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
