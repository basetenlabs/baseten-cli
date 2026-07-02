package cmd_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/basetenlabs/baseten-cli/internal/cmd"
)

const (
	envLogsPath     = "/v1/models/m/environments/production/logs"
	envDescribePath = "/v1/models/m/environments/production"
)

// envLogsEnvironment is a minimal environment describe payload whose current
// deployment carries the given status, used to drive the tail stop condition.
func envLogsEnvironment(status string) map[string]any {
	return map[string]any{
		"name":       "production",
		"model_id":   "m",
		"created_at": "2026-05-14T12:00:00Z",
		"current_deployment": map[string]any{
			"id":         "dep-1",
			"model_id":   "m",
			"name":       "v1",
			"status":     status,
			"created_at": "2026-05-14T12:00:00Z",
		},
	}
}

func Test_Model_Environment_Logs_TailWithStartRejected(t *testing.T) {
	h := NewCommandHarness(t)
	err := h.Execute("model", "environment", "logs",
		"--model-id", "m", "--environment", "production",
		"--tail", "--start", "2026-05-14")
	h.Require.Error(err)
	h.Require.Contains(err.Error(), "--tail cannot be combined")
}

func Test_Model_Environment_Logs_SinceWithStartRejected(t *testing.T) {
	h := NewCommandHarness(t)
	err := h.Execute("model", "environment", "logs",
		"--model-id", "m", "--environment", "production",
		"--since", "30m", "--start", "2026-05-14")
	h.Require.Error(err)
	h.Require.Contains(err.Error(), "--since cannot be combined")
}

func Test_Model_Environment_Logs_WindowOver7DaysRejected(t *testing.T) {
	h := NewCommandHarness(t)
	err := h.Execute("model", "environment", "logs",
		"--model-id", "m", "--environment", "production",
		"--start", "2026-05-01", "--end", "2026-05-10")
	h.Require.Error(err)
	h.Require.Contains(err.Error(), "at most 7 days")
}

func Test_Model_Environment_Logs_FiltersSent(t *testing.T) {
	h := NewCommandHarness(t)
	var gotQuery url.Values
	captureLogsQuery(h.MockManagementAPI(), envLogsPath, &gotQuery)
	err := h.Execute("model", "environment", "logs",
		"--model-id", "m", "--environment", "production",
		"--min-level", "info",
		"--includes", "warn", "--includes", "fail",
		"--excludes", "healthz",
		"--search-pattern", ".*timeout",
		"--replica", "g00r0",
		"--request-id", "req-123")
	h.Require.NoError(err)
	h.Require.Equal("INFO", gotQuery.Get("min_level"))
	h.Require.Equal([]string{"warn", "fail"}, gotQuery["includes"])
	h.Require.Equal([]string{"healthz"}, gotQuery["excludes"])
	h.Require.Equal(".*timeout", gotQuery.Get("search_pattern"))
	h.Require.Equal("g00r0", gotQuery.Get("replica"))
	h.Require.Equal("req-123", gotQuery.Get("request_id"))
}

func Test_Model_Environment_Logs_SinceBoundsSent(t *testing.T) {
	h := NewCommandHarness(t)
	var gotQuery url.Values
	captureLogsQuery(h.MockManagementAPI(), envLogsPath, &gotQuery)
	fixed := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	h.Context = cmd.WithNow(h.Context, func() time.Time { return fixed })
	err := h.Execute("model", "environment", "logs",
		"--model-id", "m", "--environment", "production", "--since", "30m")
	h.Require.NoError(err)
	h.Require.Equal(int(fixed.UnixMilli()), mustAtoi(t, gotQuery.Get("end_epoch_millis")))
	h.Require.Equal(int(fixed.Add(-30*time.Minute).UnixMilli()), mustAtoi(t, gotQuery.Get("start_epoch_millis")))
}

func Test_Model_Environment_Logs_OneShotText(t *testing.T) {
	h := NewCommandHarness(t)
	logAt := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	h.MockManagementAPI().SetRoute("GET", envLogsPath, 200, logsResponse(
		map[string]any{"timestamp": strconv.FormatInt(logAt.UnixNano(), 10), "message": "hello", "replica": "r-1"},
	))
	err := h.Execute("model", "environment", "logs",
		"--model-id", "m", "--environment", "production")
	h.Require.NoError(err)
	h.Require.Contains(h.Stdout.String(), "(r-1) hello")
}

func Test_Model_Environment_Logs_OneShotJSONL(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", envLogsPath, 200, logsResponse(
		map[string]any{"timestamp": "1", "message": "a", "replica": nil},
		map[string]any{"timestamp": "2", "message": "b", "replica": nil},
	))
	err := h.Execute("model", "environment", "logs",
		"--model-id", "m", "--environment", "production", "--output", "jsonl")
	h.Require.NoError(err)
	lines := strings.Split(strings.TrimRight(h.Stdout.String(), "\n"), "\n")
	h.Require.Len(lines, 2)
	for _, l := range lines {
		var v map[string]any
		h.Require.NoError(json.Unmarshal([]byte(l), &v))
	}
}

func Test_Model_Environment_Logs_TailTerminatesOnCurrentDeploymentStatus(t *testing.T) {
	h := NewCommandHarness(t)
	api := h.MockManagementAPI()
	api.SetRoute("GET", envLogsPath, 200, logsResponse(
		map[string]any{"timestamp": "1", "message": "build started", "replica": nil},
	))
	// Tail stop condition resolves the environment's current deployment.
	api.SetRoute("GET", envDescribePath, 200, envLogsEnvironment("BUILD_FAILED"))
	h.Context = cmd.WithSleep(h.Context, func(_ context.Context, _ time.Duration) error { return nil })
	err := h.Execute("model", "environment", "logs",
		"--model-id", "m", "--environment", "production", "--tail")
	h.Require.NoError(err)
	h.Require.Contains(h.Stdout.String(), "build started")
	h.Require.Contains(h.Stderr.String(), "Tailing stopped: deployment status BUILD_FAILED")
}

func Test_Model_Environment_Logs_TailDedupesAcrossPolls(t *testing.T) {
	h := NewCommandHarness(t)
	api := h.MockManagementAPI()
	dup := map[string]any{"timestamp": "1", "message": "same", "replica": nil}
	logsCalls := 0
	api.SetRouteFunc("GET", envLogsPath, func(w http.ResponseWriter, _ *http.Request) {
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
	envCalls := 0
	api.SetRouteFunc("GET", envDescribePath, func(w http.ResponseWriter, _ *http.Request) {
		envCalls++
		status := "ACTIVE"
		if envCalls > 1 {
			status = "BUILD_FAILED"
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(envLogsEnvironment(status))
	})

	h.Context = cmd.WithSleep(h.Context, func(_ context.Context, _ time.Duration) error { return nil })
	err := h.Execute("model", "environment", "logs",
		"--model-id", "m", "--environment", "production", "--tail")
	h.Require.NoError(err)
	h.Require.Equal(1, strings.Count(h.Stdout.String(), "same"))
	h.Require.Contains(h.Stdout.String(), "next")
}
