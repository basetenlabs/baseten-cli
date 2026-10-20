package cmd_test

import (
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
