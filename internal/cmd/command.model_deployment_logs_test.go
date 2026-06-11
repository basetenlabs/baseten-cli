package cmd_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/basetenlabs/baseten-cli/internal/cmd"
)

const (
	logsLogsPath   = "/v1/models/m/deployments/d/logs"
	logsDeployPath = "/v1/models/m/deployments/d"
)

func logsDeployment(status string) map[string]any {
	return map[string]any{
		"id":         "dep-1",
		"model_id":   "model-1",
		"name":       "v1",
		"status":     status,
		"created_at": "2026-05-14T12:00:00Z",
	}
}

func logsResponse(logs ...map[string]any) map[string]any {
	if logs == nil {
		logs = []map[string]any{}
	}
	return map[string]any{"logs": logs}
}

func Test_Model_Deployment_Logs_TailWithStartRejected(t *testing.T) {
	h := NewCommandHarness(t)
	err := h.Execute("model", "deployment", "logs",
		"--model-id", "m", "--deployment-id", "d",
		"--tail", "--start", "2026-05-14")
	h.Require.Error(err)
	h.Require.Contains(err.Error(), "--tail cannot be combined")
}

func Test_Model_Deployment_Logs_SinceWithStartRejected(t *testing.T) {
	h := NewCommandHarness(t)
	err := h.Execute("model", "deployment", "logs",
		"--model-id", "m", "--deployment-id", "d",
		"--since", "30m", "--start", "2026-05-14")
	h.Require.Error(err)
	h.Require.Contains(err.Error(), "--since cannot be combined")
}

func Test_Model_Deployment_Logs_StartAfterEndRejected(t *testing.T) {
	h := NewCommandHarness(t)
	err := h.Execute("model", "deployment", "logs",
		"--model-id", "m", "--deployment-id", "d",
		"--start", "2026-05-14T13:00:00", "--end", "2026-05-14T12:00:00")
	h.Require.Error(err)
	h.Require.Contains(err.Error(), "--start must be earlier")
}

func Test_Model_Deployment_Logs_WindowOver7DaysRejected(t *testing.T) {
	h := NewCommandHarness(t)
	err := h.Execute("model", "deployment", "logs",
		"--model-id", "m", "--deployment-id", "d",
		"--start", "2026-05-01", "--end", "2026-05-10")
	h.Require.Error(err)
	h.Require.Contains(err.Error(), "at most 7 days")
}

func Test_Model_Deployment_Logs_SinceOver7DaysRejected(t *testing.T) {
	h := NewCommandHarness(t)
	err := h.Execute("model", "deployment", "logs",
		"--model-id", "m", "--deployment-id", "d",
		"--since", "8d")
	h.Require.Error(err)
	h.Require.Contains(err.Error(), "at most 7d")
}

func Test_Model_Deployment_Logs_FiltersSent(t *testing.T) {
	h := NewCommandHarness(t)
	api := h.MockManagementAPI()
	api.SetRoute("POST", logsLogsPath, 200, logsResponse())
	err := h.Execute("model", "deployment", "logs",
		"--model-id", "m", "--deployment-id", "d",
		"--min-level", "info",
		"--includes", "warn", "--includes", "fail",
		"--excludes", "healthz",
		"--search-pattern", ".*timeout",
		"--replica", "g00r0",
		"--request-id", "req-123")
	h.Require.NoError(err)
	req := api.FindCall("POST", logsLogsPath).BodyJSON(h.T)
	// --min-level is uppercased before sending.
	h.Require.Equal("INFO", req["min_level"])
	h.Require.Equal([]any{"warn", "fail"}, req["includes"])
	h.Require.Equal([]any{"healthz"}, req["excludes"])
	h.Require.Equal(".*timeout", req["search_pattern"])
	h.Require.Equal("g00r0", req["replica"])
	h.Require.Equal("req-123", req["request_id"])
}

func Test_Model_Deployment_Logs_FiltersOmittedWhenUnset(t *testing.T) {
	h := NewCommandHarness(t)
	api := h.MockManagementAPI()
	api.SetRoute("POST", logsLogsPath, 200, logsResponse())
	err := h.Execute("model", "deployment", "logs", "--model-id", "m", "--deployment-id", "d")
	h.Require.NoError(err)
	req := api.FindCall("POST", logsLogsPath).BodyJSON(h.T)
	for _, field := range []string{"min_level", "includes", "excludes", "search_pattern", "replica", "request_id"} {
		h.Require.Nil(req[field], "expected %s to be omitted", field)
	}
}

func Test_Model_Deployment_Logs_MinLevelInvalidRejected(t *testing.T) {
	h := NewCommandHarness(t)
	err := h.Execute("model", "deployment", "logs",
		"--model-id", "m", "--deployment-id", "d", "--min-level", "trace")
	h.Require.Error(err)
	h.Require.Contains(err.Error(), "must be one of")
}

func Test_Model_Deployment_Logs_TailWithFilterRejected(t *testing.T) {
	h := NewCommandHarness(t)
	err := h.Execute("model", "deployment", "logs",
		"--model-id", "m", "--deployment-id", "d", "--tail", "--min-level", "info")
	h.Require.Error(err)
	h.Require.Contains(err.Error(), "--tail cannot be combined")
}

func Test_Model_Deployment_Logs_OneShotText(t *testing.T) {
	h := NewCommandHarness(t)
	// 2026-05-14T12:00:00Z in nanoseconds since epoch. Display is in the
	// local timezone, so format the expected string against time.Local.
	logAt := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	ts := logAt.UnixNano()
	h.MockManagementAPI().SetRoute("POST", logsLogsPath, 200, logsResponse(
		map[string]any{"timestamp": strconv.FormatInt(ts, 10), "message": "hello", "replica": "r-1"},
		map[string]any{"timestamp": strconv.FormatInt(ts+int64(time.Second), 10), "message": "world", "replica": nil},
	))
	err := h.Execute("model", "deployment", "logs",
		"--model-id", "m", "--deployment-id", "d")
	h.Require.NoError(err)
	out := h.Stdout.String()
	h.Require.Contains(out, fmt.Sprintf("[%s]: (r-1) hello", logAt.Local().Format("2006-01-02 15:04:05")))
	h.Require.Contains(out, fmt.Sprintf("[%s]: world", logAt.Add(time.Second).Local().Format("2006-01-02 15:04:05")))
}

func Test_Model_Deployment_Logs_OneShotJSON(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("POST", logsLogsPath, 200, logsResponse(
		map[string]any{"timestamp": "1", "message": "hi", "replica": nil},
	))
	err := h.Execute("model", "deployment", "logs",
		"--model-id", "m", "--deployment-id", "d", "--output", "json")
	h.Require.NoError(err)
	out := strings.TrimSpace(h.Stdout.String())
	h.Require.True(strings.HasPrefix(out, "["))
	h.Require.True(strings.HasSuffix(out, "]"))
	h.Require.Contains(out, `"message": "hi"`)
}

func Test_Model_Deployment_Logs_OneShotJSONL(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("POST", logsLogsPath, 200, logsResponse(
		map[string]any{"timestamp": "1", "message": "a", "replica": nil},
		map[string]any{"timestamp": "2", "message": "b", "replica": nil},
	))
	err := h.Execute("model", "deployment", "logs",
		"--model-id", "m", "--deployment-id", "d", "--output", "jsonl")
	h.Require.NoError(err)
	lines := strings.Split(strings.TrimRight(h.Stdout.String(), "\n"), "\n")
	h.Require.Len(lines, 2)
	for _, l := range lines {
		var v map[string]any
		h.Require.NoError(json.Unmarshal([]byte(l), &v))
	}
}

func Test_Model_Deployment_Logs_SinceBoundsSent(t *testing.T) {
	h := NewCommandHarness(t)
	api := h.MockManagementAPI()
	api.SetRoute("POST", logsLogsPath, 200, logsResponse())
	fixed := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	h.Context = cmd.WithNow(h.Context, func() time.Time { return fixed })
	err := h.Execute("model", "deployment", "logs",
		"--model-id", "m", "--deployment-id", "d", "--since", "30m")
	h.Require.NoError(err)
	call := api.FindCall("POST", logsLogsPath)
	h.Require.NotNil(call)
	req := call.BodyJSON(h.T)
	endMs := fixed.UnixMilli()
	startMs := fixed.Add(-30 * time.Minute).UnixMilli()
	h.Require.Equal(float64(startMs), req["start_epoch_millis"])
	h.Require.Equal(float64(endMs), req["end_epoch_millis"])
}

func Test_Model_Deployment_Logs_SinceWithDaySuffix(t *testing.T) {
	h := NewCommandHarness(t)
	api := h.MockManagementAPI()
	api.SetRoute("POST", logsLogsPath, 200, logsResponse())
	fixed := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	h.Context = cmd.WithNow(h.Context, func() time.Time { return fixed })
	err := h.Execute("model", "deployment", "logs",
		"--model-id", "m", "--deployment-id", "d", "--since", "3d")
	h.Require.NoError(err)
	req := api.FindCall("POST", logsLogsPath).BodyJSON(h.T)
	startMs := fixed.Add(-3 * 24 * time.Hour).UnixMilli()
	h.Require.Equal(float64(startMs), req["start_epoch_millis"])
}

func Test_Model_Deployment_Logs_StartOnlyDefersEndToServer(t *testing.T) {
	h := NewCommandHarness(t)
	api := h.MockManagementAPI()
	api.SetRoute("POST", logsLogsPath, 200, logsResponse())
	start := time.Date(2026, 5, 13, 11, 0, 0, 0, time.Local)
	err := h.Execute("model", "deployment", "logs",
		"--model-id", "m", "--deployment-id", "d", "--start", "2026-05-13T11:00:00")
	h.Require.NoError(err)
	req := api.FindCall("POST", logsLogsPath).BodyJSON(h.T)
	// Only --start is sent; end is left for the server to backfill.
	h.Require.Equal(float64(start.UnixMilli()), req["start_epoch_millis"])
	h.Require.Nil(req["end_epoch_millis"])
}

func Test_Model_Deployment_Logs_EndOnlyDefersStartToServer(t *testing.T) {
	h := NewCommandHarness(t)
	api := h.MockManagementAPI()
	api.SetRoute("POST", logsLogsPath, 200, logsResponse())
	end := time.Date(2026, 5, 14, 12, 0, 0, 0, time.Local)
	err := h.Execute("model", "deployment", "logs",
		"--model-id", "m", "--deployment-id", "d", "--end", "2026-05-14T12:00:00")
	h.Require.NoError(err)
	req := api.FindCall("POST", logsLogsPath).BodyJSON(h.T)
	// Only --end is sent; start is left for the server to backfill.
	h.Require.Equal(float64(end.UnixMilli()), req["end_epoch_millis"])
	h.Require.Nil(req["start_epoch_millis"])
}

func Test_Model_Deployment_Logs_TailTerminatesOnStopStatus(t *testing.T) {
	h := NewCommandHarness(t)
	api := h.MockManagementAPI()
	api.SetRoute("POST", logsLogsPath, 200, logsResponse(
		map[string]any{"timestamp": "1", "message": "build started", "replica": nil},
	))
	api.SetRoute("GET", logsDeployPath, 200, logsDeployment("BUILD_FAILED"))
	h.Context = cmd.WithSleep(h.Context, func(_ context.Context, _ time.Duration) error { return nil })
	err := h.Execute("model", "deployment", "logs",
		"--model-id", "m", "--deployment-id", "d", "--tail")
	h.Require.NoError(err)
	h.Require.Contains(h.Stdout.String(), "build started")
	h.Require.Contains(h.Stderr.String(), "Tailing stopped: deployment status BUILD_FAILED")
}

func Test_Model_Deployment_Logs_TailStopsOnUnknownStatus(t *testing.T) {
	h := NewCommandHarness(t)
	api := h.MockManagementAPI()
	api.SetRoute("POST", logsLogsPath, 200, logsResponse(
		map[string]any{"timestamp": "1", "message": "alive", "replica": nil},
	))
	api.SetRoute("GET", logsDeployPath, 200, logsDeployment("SOME_NEW_STATE"))
	h.Context = cmd.WithSleep(h.Context, func(_ context.Context, _ time.Duration) error { return nil })
	err := h.Execute("model", "deployment", "logs",
		"--model-id", "m", "--deployment-id", "d", "--tail")
	h.Require.NoError(err)
	h.Require.Contains(h.Stderr.String(), "SOME_NEW_STATE")
}

func Test_Model_Deployment_Logs_TailDedupesAcrossPolls(t *testing.T) {
	h := NewCommandHarness(t)
	api := h.MockManagementAPI()
	dup := map[string]any{"timestamp": "1", "message": "same", "replica": nil}
	logsCalls := 0
	api.SetRouteFunc("POST", logsLogsPath, func(w http.ResponseWriter, _ *http.Request) {
		logsCalls++
		var payload map[string]any
		if logsCalls == 1 {
			payload = logsResponse(dup)
		} else {
			payload = logsResponse(dup, map[string]any{"timestamp": "2", "message": "next", "replica": nil})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(payload)
	})
	depCalls := 0
	api.SetRouteFunc("GET", logsDeployPath, func(w http.ResponseWriter, _ *http.Request) {
		depCalls++
		status := "ACTIVE"
		if depCalls > 1 {
			status = "BUILD_FAILED"
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(logsDeployment(status))
	})

	h.Context = cmd.WithSleep(h.Context, func(_ context.Context, _ time.Duration) error { return nil })
	err := h.Execute("model", "deployment", "logs",
		"--model-id", "m", "--deployment-id", "d", "--tail")
	h.Require.NoError(err)
	// "same" should appear once, not twice.
	h.Require.Equal(1, strings.Count(h.Stdout.String(), "same"))
	h.Require.Contains(h.Stdout.String(), "next")
}

func Test_Model_Deployment_Logs_TailJSONLStreaming(t *testing.T) {
	h := NewCommandHarness(t)
	api := h.MockManagementAPI()
	api.SetRoute("POST", logsLogsPath, 200, logsResponse(
		map[string]any{"timestamp": "1", "message": "a", "replica": nil},
	))
	api.SetRoute("GET", logsDeployPath, 200, logsDeployment("BUILD_FAILED"))
	h.Context = cmd.WithSleep(h.Context, func(_ context.Context, _ time.Duration) error { return nil })
	err := h.Execute("model", "deployment", "logs",
		"--model-id", "m", "--deployment-id", "d", "--tail", "--output", "jsonl")
	h.Require.NoError(err)
	out := strings.TrimRight(h.Stdout.String(), "\n")
	// Each record on its own line; no enclosing brackets.
	h.Require.False(strings.HasPrefix(out, "["))
	h.Require.Equal(1, strings.Count(out, "\n")+1)
}

func Test_Model_Deployment_Logs_TailJSONArrayClosesOnExit(t *testing.T) {
	h := NewCommandHarness(t)
	api := h.MockManagementAPI()
	api.SetRoute("POST", logsLogsPath, 200, logsResponse(
		map[string]any{"timestamp": "1", "message": "a", "replica": nil},
	))
	api.SetRoute("GET", logsDeployPath, 200, logsDeployment("BUILD_FAILED"))
	h.Context = cmd.WithSleep(h.Context, func(_ context.Context, _ time.Duration) error { return nil })
	err := h.Execute("model", "deployment", "logs",
		"--model-id", "m", "--deployment-id", "d", "--tail", "--output", "json")
	h.Require.NoError(err)
	out := strings.TrimSpace(h.Stdout.String())
	h.Require.True(strings.HasPrefix(out, "["))
	h.Require.True(strings.HasSuffix(out, "]"))
}
