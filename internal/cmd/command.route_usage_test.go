package cmd_test

import (
	"encoding/json"
	"strings"
	"testing"
)

func Test_Route_Usage_DefaultsMatchAPI(t *testing.T) {
	h, api := newHarnessUsageHarness(t, harnessUsageNow, harnessUsageMonth())

	h.Require.NoError(h.Execute("route", "usage"))
	q := harnessUsageLastQuery(h, api)
	h.Require.Equal([]string{"2026-09-24"}, q["start_date"], "the previous UTC day")
	h.Require.Equal([]string{"2026-09-26"}, q["end_date"], "through today")
	h.Require.Equal([]string{"MODEL"}, q["group_by"])
	h.Require.Empty(q["user_ids"], "no default user filter")
	h.Require.Nil(api.FindCall("GET", "/v1/users/me"))
}

func Test_Route_Usage_EndOnlySpansTheDayBefore(t *testing.T) {
	h, api := newHarnessUsageHarness(t, harnessUsageNow, harnessUsageMonth())

	h.Require.NoError(h.Execute("route", "usage", "--end", "2026-09-10T00:00:00Z"))
	q := harnessUsageLastQuery(h, api)
	h.Require.Equal([]string{"2026-09-09"}, q["start_date"])
	h.Require.Equal([]string{"2026-09-10"}, q["end_date"])
}

func Test_Route_Usage_TimeSeriesTable(t *testing.T) {
	h, _ := newHarnessUsageHarness(t, harnessUsageNow, harnessUsageMonth())

	h.Require.NoError(h.Execute("route", "usage", "--since", "7d"))
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

func Test_Route_Usage_KeepsRowsWithNoTokens(t *testing.T) {
	body := harnessUsageBody(false, nil, harnessUsageBucket("2026-09-24",
		harnessUsageModel("claude-opus-5-5", "0.14", 10, 0, 1),
		harnessUsageModel("gpt-6-luna", "0", 0, 0, 0),
	))
	h, _ := newHarnessUsageHarness(t, harnessUsageNow, body)

	h.Require.NoError(h.Execute("route", "usage"))
	h.Require.Equal([]string{"gpt-6-luna", "0", "0", "0", "$0.00"}, harnessUsageRow(h, h.Stdout.String(), "gpt-6-luna"), "shows what the API returns")
}

func Test_Route_Usage_FiltersPassThrough(t *testing.T) {
	h, api := newHarnessUsageHarness(t, harnessUsageNow, harnessUsageMonth())

	h.Require.NoError(h.Execute("route", "usage", "--user-id", "user-b", "--group-by", "user,provider", "--model", "glm-5.2", "--provider", "xai"))
	q := harnessUsageLastQuery(h, api)
	h.Require.Equal([]string{"user-b"}, q["user_ids"])
	h.Require.Equal([]string{"USER", "PROVIDER"}, q["group_by"])
	h.Require.Equal([]string{"glm-5.2"}, q["models"])
	h.Require.Equal([]string{"XAI"}, q["providers"])
}

func Test_Route_Usage_JSONStreamsBuckets(t *testing.T) {
	h, _ := newHarnessUsageHarness(t, harnessUsageNow, harnessUsageMonth())

	h.Require.NoError(h.Execute("route", "usage", "--output", "json"))
	var got []struct {
		Date    string            `json:"date"`
		Results []json.RawMessage `json:"results"`
	}
	h.Require.NoError(json.Unmarshal(h.Stdout.Bytes(), &got))
	h.Require.Len(got, 3)
	h.Require.Len(got[0].Results, 2)
	h.Require.Empty(got[1].Results)
}

func Test_Route_Usage_EmptyHasNoAPIKeyHint(t *testing.T) {
	body := harnessUsageBody(false, nil, harnessUsageBucket("2026-09-24"))
	h, _ := newHarnessUsageHarness(t, harnessUsageNow, body)

	h.Require.NoError(h.Execute("route", "usage"))
	h.Require.Empty(h.Stdout.String())
	h.Require.Contains(h.Stderr.String(), "No usage in the selected window.")
	h.Require.NotContains(h.Stderr.String(), "team or workspace API key", "there's no default user filter to explain")
}
