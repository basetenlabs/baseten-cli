package cmd_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/basetenlabs/baseten-cli/internal/cmd"
)

// logsServer is a fake management API server that captures /logs requests
// and serves scripted log + deployment responses. Use Append* to queue
// responses; the first matching call pops the next entry, with the final
// entry repeating if exhausted.
type logsServer struct {
	*httptest.Server

	t *testing.T

	logsReqBodies []string
	logsResponses [][]map[string]any
	logsIdx       int32

	deploymentResponses []map[string]any
	deploymentIdx       int32
}

func newLogsServer(t *testing.T) *logsServer {
	t.Helper()
	s := &logsServer{t: t}
	s.Server = httptest.NewServer(http.HandlerFunc(s.handle))
	t.Cleanup(s.Close)
	return s
}

func (s *logsServer) handle(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/logs"):
		body, _ := io.ReadAll(r.Body)
		s.logsReqBodies = append(s.logsReqBodies, string(body))
		var logs []map[string]any
		if len(s.logsResponses) > 0 {
			i := atomic.LoadInt32(&s.logsIdx)
			if int(i) >= len(s.logsResponses) {
				i = int32(len(s.logsResponses) - 1)
			} else {
				atomic.AddInt32(&s.logsIdx, 1)
			}
			logs = s.logsResponses[i]
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"logs": logs})
	case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/deployments/") && !strings.HasSuffix(r.URL.Path, "/logs"):
		var dep map[string]any
		if len(s.deploymentResponses) > 0 {
			i := atomic.LoadInt32(&s.deploymentIdx)
			if int(i) >= len(s.deploymentResponses) {
				i = int32(len(s.deploymentResponses) - 1)
			} else {
				atomic.AddInt32(&s.deploymentIdx, 1)
			}
			dep = s.deploymentResponses[i]
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(dep)
	default:
		http.NotFound(w, r)
	}
}

func (s *logsServer) appendLogs(logs ...map[string]any) {
	s.logsResponses = append(s.logsResponses, logs)
}

func (s *logsServer) appendDeployment(status string) {
	s.deploymentResponses = append(s.deploymentResponses, map[string]any{
		"id":         "dep-1",
		"model_id":   "model-1",
		"name":       "v1",
		"status":     status,
		"created_at": "2026-05-14T12:00:00Z",
	})
}

func newLogsHarness(t *testing.T, srv *logsServer) *CommandHarness {
	h := NewCommandHarness(t)
	t.Setenv("BASETEN_REMOTE_URL", srv.URL)
	t.Setenv("BASETEN_MANAGEMENT_API_URL_OVERRIDE", srv.URL)
	return h
}

func TestModelDeploymentLogs_TailWithStartRejected(t *testing.T) {
	srv := newLogsServer(t)
	h := newLogsHarness(t, srv)
	err := h.Execute("model", "deployment", "logs",
		"--model-id", "m", "--deployment-id", "d",
		"--tail", "--start", "2026-05-14")
	h.Require.Error(err)
	h.Require.Contains(err.Error(), "--tail cannot be combined")
}

func TestModelDeploymentLogs_SinceWithStartRejected(t *testing.T) {
	srv := newLogsServer(t)
	h := newLogsHarness(t, srv)
	err := h.Execute("model", "deployment", "logs",
		"--model-id", "m", "--deployment-id", "d",
		"--since", "30m", "--start", "2026-05-14")
	h.Require.Error(err)
	h.Require.Contains(err.Error(), "--since cannot be combined")
}

func TestModelDeploymentLogs_StartAfterEndRejected(t *testing.T) {
	srv := newLogsServer(t)
	h := newLogsHarness(t, srv)
	err := h.Execute("model", "deployment", "logs",
		"--model-id", "m", "--deployment-id", "d",
		"--start", "2026-05-14T13:00:00", "--end", "2026-05-14T12:00:00")
	h.Require.Error(err)
	h.Require.Contains(err.Error(), "--start must be earlier")
}

func TestModelDeploymentLogs_WindowOver7DaysRejected(t *testing.T) {
	srv := newLogsServer(t)
	h := newLogsHarness(t, srv)
	err := h.Execute("model", "deployment", "logs",
		"--model-id", "m", "--deployment-id", "d",
		"--start", "2026-05-01", "--end", "2026-05-10")
	h.Require.Error(err)
	h.Require.Contains(err.Error(), "at most 7 days")
}

func TestModelDeploymentLogs_SinceOver7DaysRejected(t *testing.T) {
	srv := newLogsServer(t)
	h := newLogsHarness(t, srv)
	err := h.Execute("model", "deployment", "logs",
		"--model-id", "m", "--deployment-id", "d",
		"--since", "8d")
	h.Require.Error(err)
	h.Require.Contains(err.Error(), "at most 7d")
}

func TestModelDeploymentLogs_OneShotText(t *testing.T) {
	srv := newLogsServer(t)
	// 2026-05-14T12:00:00Z in nanoseconds since epoch. Display is in the
	// local timezone, so format the expected string against time.Local.
	logAt := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	ts := logAt.UnixNano()
	srv.appendLogs(
		map[string]any{"timestamp": strconv.FormatInt(ts, 10), "message": "hello", "replica": "r-1"},
		map[string]any{"timestamp": strconv.FormatInt(ts+int64(time.Second), 10), "message": "world", "replica": nil},
	)
	h := newLogsHarness(t, srv)
	err := h.Execute("model", "deployment", "logs",
		"--model-id", "m", "--deployment-id", "d")
	h.Require.NoError(err)
	out := h.Stdout.String()
	h.Require.Contains(out, fmt.Sprintf("[%s]: (r-1) hello", logAt.Local().Format("2006-01-02 15:04:05")))
	h.Require.Contains(out, fmt.Sprintf("[%s]: world", logAt.Add(time.Second).Local().Format("2006-01-02 15:04:05")))
}

func TestModelDeploymentLogs_OneShotJSON(t *testing.T) {
	srv := newLogsServer(t)
	srv.appendLogs(map[string]any{"timestamp": "1", "message": "hi", "replica": nil})
	h := newLogsHarness(t, srv)
	err := h.Execute("model", "deployment", "logs",
		"--model-id", "m", "--deployment-id", "d", "--output", "json")
	h.Require.NoError(err)
	out := strings.TrimSpace(h.Stdout.String())
	h.Require.True(strings.HasPrefix(out, "["))
	h.Require.True(strings.HasSuffix(out, "]"))
	h.Require.Contains(out, `"message": "hi"`)
}

func TestModelDeploymentLogs_OneShotJSONL(t *testing.T) {
	srv := newLogsServer(t)
	srv.appendLogs(
		map[string]any{"timestamp": "1", "message": "a", "replica": nil},
		map[string]any{"timestamp": "2", "message": "b", "replica": nil},
	)
	h := newLogsHarness(t, srv)
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

func TestModelDeploymentLogs_SinceBoundsSent(t *testing.T) {
	srv := newLogsServer(t)
	srv.appendLogs()
	h := newLogsHarness(t, srv)
	fixed := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	h.Context = cmd.WithNow(h.Context, func() time.Time { return fixed })
	err := h.Execute("model", "deployment", "logs",
		"--model-id", "m", "--deployment-id", "d", "--since", "30m")
	h.Require.NoError(err)
	h.Require.Len(srv.logsReqBodies, 1)
	var req map[string]any
	h.Require.NoError(json.Unmarshal([]byte(srv.logsReqBodies[0]), &req))
	endMs := fixed.UnixMilli()
	startMs := fixed.Add(-30 * time.Minute).UnixMilli()
	h.Require.Equal(float64(startMs), req["start_epoch_millis"])
	h.Require.Equal(float64(endMs), req["end_epoch_millis"])
}

func TestModelDeploymentLogs_SinceWithDaySuffix(t *testing.T) {
	srv := newLogsServer(t)
	srv.appendLogs()
	h := newLogsHarness(t, srv)
	fixed := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	h.Context = cmd.WithNow(h.Context, func() time.Time { return fixed })
	err := h.Execute("model", "deployment", "logs",
		"--model-id", "m", "--deployment-id", "d", "--since", "3d")
	h.Require.NoError(err)
	var req map[string]any
	h.Require.NoError(json.Unmarshal([]byte(srv.logsReqBodies[0]), &req))
	startMs := fixed.Add(-3 * 24 * time.Hour).UnixMilli()
	h.Require.Equal(float64(startMs), req["start_epoch_millis"])
}

func TestModelDeploymentLogs_StartOnlyEndDefaultsToNow(t *testing.T) {
	srv := newLogsServer(t)
	srv.appendLogs()
	h := newLogsHarness(t, srv)
	fixed := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	h.Context = cmd.WithNow(h.Context, func() time.Time { return fixed })
	err := h.Execute("model", "deployment", "logs",
		"--model-id", "m", "--deployment-id", "d", "--start", "2026-05-13T11:00:00")
	h.Require.NoError(err)
	var req map[string]any
	h.Require.NoError(json.Unmarshal([]byte(srv.logsReqBodies[0]), &req))
	h.Require.Equal(float64(fixed.UnixMilli()), req["end_epoch_millis"])
}

func TestModelDeploymentLogs_TailTerminatesOnStopStatus(t *testing.T) {
	srv := newLogsServer(t)
	srv.appendLogs(map[string]any{"timestamp": "1", "message": "build started", "replica": nil})
	srv.appendDeployment("BUILD_FAILED")
	h := newLogsHarness(t, srv)
	h.Context = cmd.WithSleep(h.Context, func(_ context.Context, _ time.Duration) error { return nil })
	err := h.Execute("model", "deployment", "logs",
		"--model-id", "m", "--deployment-id", "d", "--tail")
	h.Require.NoError(err)
	h.Require.Contains(h.Stdout.String(), "build started")
	h.Require.Contains(h.Stderr.String(), "Tailing stopped: deployment status BUILD_FAILED")
}

func TestModelDeploymentLogs_TailStopsOnUnknownStatus(t *testing.T) {
	srv := newLogsServer(t)
	srv.appendLogs(map[string]any{"timestamp": "1", "message": "alive", "replica": nil})
	srv.appendDeployment("SOME_NEW_STATE")

	h := newLogsHarness(t, srv)
	h.Context = cmd.WithSleep(h.Context, func(_ context.Context, _ time.Duration) error { return nil })
	err := h.Execute("model", "deployment", "logs",
		"--model-id", "m", "--deployment-id", "d", "--tail")
	h.Require.NoError(err)
	h.Require.Contains(h.Stderr.String(), "SOME_NEW_STATE")
}

func TestModelDeploymentLogs_TailDedupesAcrossPolls(t *testing.T) {
	srv := newLogsServer(t)
	dup := map[string]any{"timestamp": "1", "message": "same", "replica": nil}
	srv.appendLogs(dup)
	srv.appendLogs(dup, map[string]any{"timestamp": "2", "message": "next", "replica": nil})
	srv.appendDeployment("ACTIVE")
	srv.appendDeployment("BUILD_FAILED")

	h := newLogsHarness(t, srv)
	h.Context = cmd.WithSleep(h.Context, func(_ context.Context, _ time.Duration) error { return nil })
	err := h.Execute("model", "deployment", "logs",
		"--model-id", "m", "--deployment-id", "d", "--tail")
	h.Require.NoError(err)
	// "same" should appear once, not twice.
	h.Require.Equal(1, strings.Count(h.Stdout.String(), "same"))
	h.Require.Contains(h.Stdout.String(), "next")
}

func TestModelDeploymentLogs_TailJSONLStreaming(t *testing.T) {
	srv := newLogsServer(t)
	srv.appendLogs(map[string]any{"timestamp": "1", "message": "a", "replica": nil})
	srv.appendDeployment("BUILD_FAILED")
	h := newLogsHarness(t, srv)
	h.Context = cmd.WithSleep(h.Context, func(_ context.Context, _ time.Duration) error { return nil })
	err := h.Execute("model", "deployment", "logs",
		"--model-id", "m", "--deployment-id", "d", "--tail", "--output", "jsonl")
	h.Require.NoError(err)
	out := strings.TrimRight(h.Stdout.String(), "\n")
	// Each record on its own line; no enclosing brackets.
	h.Require.False(strings.HasPrefix(out, "["))
	h.Require.Equal(1, strings.Count(out, "\n")+1)
}

func TestModelDeploymentLogs_TailJSONArrayClosesOnExit(t *testing.T) {
	srv := newLogsServer(t)
	srv.appendLogs(map[string]any{"timestamp": "1", "message": "a", "replica": nil})
	srv.appendDeployment("BUILD_FAILED")
	h := newLogsHarness(t, srv)
	h.Context = cmd.WithSleep(h.Context, func(_ context.Context, _ time.Duration) error { return nil })
	err := h.Execute("model", "deployment", "logs",
		"--model-id", "m", "--deployment-id", "d", "--tail", "--output", "json")
	h.Require.NoError(err)
	out := strings.TrimSpace(h.Stdout.String())
	h.Require.True(strings.HasPrefix(out, "["))
	h.Require.True(strings.HasSuffix(out, "]"))
}
