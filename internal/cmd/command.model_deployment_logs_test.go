package cmd_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
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

// captureLogsQuery registers a GET logs route that records the request query
// and responds with the given logs.
func captureLogsQuery(api *MockManagementAPI, path string, gotQuery *url.Values, logs ...map[string]any) {
	api.SetRouteFunc("GET", path, func(w http.ResponseWriter, r *http.Request) {
		*gotQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(logsResponse(logs...))
	})
}

// fakeLogLine is a synthetic log line for the paginating backend simulator.
type fakeLogLine struct {
	tsNs    int64
	message string
	replica string
}

func fakeLogLineMap(l fakeLogLine) map[string]any {
	m := map[string]any{"timestamp": strconv.FormatInt(l.tsNs, 10), "message": l.message}
	if l.replica != "" {
		m["replica"] = l.replica
	} else {
		m["replica"] = nil
	}
	return m
}

// servePaginatedLogs simulates the backend logs endpoint over a fixed set of
// lines (passed newest-first): it returns the newest lines within
// [start_epoch_millis, end_epoch_millis] (millisecond-inclusive on both ends),
// capped at the requested limit, newest-first. Every request's query is
// appended to calls so tests can assert the paging window walked backward.
func servePaginatedLogs(api *MockManagementAPI, path string, lines []fakeLogLine, calls *[]url.Values) {
	api.SetRouteFunc("GET", path, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		*calls = append(*calls, q)
		limit, _ := strconv.Atoi(q.Get("limit"))
		startMs, _ := strconv.ParseInt(q.Get("start_epoch_millis"), 10, 64)
		endMs, _ := strconv.ParseInt(q.Get("end_epoch_millis"), 10, 64)
		out := []map[string]any{}
		for _, l := range lines {
			ms := l.tsNs / int64(time.Millisecond)
			if ms > endMs || ms < startMs {
				continue
			}
			out = append(out, fakeLogLineMap(l))
			if len(out) >= limit {
				break
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(logsResponse(out...))
	})
}

// jsonlMessages parses jsonl log output into the ordered list of messages.
func jsonlMessages(t *testing.T, out string) []string {
	t.Helper()
	var msgs []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		var v map[string]any
		if err := json.Unmarshal([]byte(line), &v); err != nil {
			t.Fatalf("bad jsonl line %q: %v", line, err)
		}
		msgs = append(msgs, v["message"].(string))
	}
	return msgs
}

func queryInt64(t *testing.T, q url.Values, key string) int64 {
	t.Helper()
	n, err := strconv.ParseInt(q.Get(key), 10, 64)
	if err != nil {
		t.Fatalf("parse %s=%q: %v", key, q.Get(key), err)
	}
	return n
}

func Test_Model_Deployment_Logs_PaginatesBackwardAcrossPages(t *testing.T) {
	h := NewCommandHarness(t)
	now := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	nowMs := now.UnixMilli()
	baseNs := now.UnixNano()
	// 2500 lines one millisecond apart (log-0 newest). With a 1000-line page
	// this is three pages: 1000, 1000 (one seam dup), 502 (one seam dup).
	lines := make([]fakeLogLine, 2500)
	for i := range lines {
		lines[i] = fakeLogLine{tsNs: baseNs - int64(i)*int64(time.Millisecond), message: fmt.Sprintf("log-%d", i)}
	}
	var calls []url.Values
	servePaginatedLogs(h.MockManagementAPI(), logsLogsPath, lines, &calls)
	h.Context = cmd.WithNow(h.Context, func() time.Time { return now })

	err := h.Execute("model", "deployment", "logs",
		"--model-id", "m", "--deployment-id", "d", "--limit", "0", "--output", "jsonl")
	h.Require.NoError(err)

	msgs := jsonlMessages(t, h.Stdout.String())
	// Every line exactly once, newest-first, none lost or duplicated at a seam.
	h.Require.Len(msgs, 2500)
	for i, m := range msgs {
		h.Require.Equal(fmt.Sprintf("log-%d", i), m)
	}
	// Three requests, each ending at the previous page's oldest millisecond.
	h.Require.Len(calls, 3)
	h.Require.Equal(nowMs, queryInt64(t, calls[0], "end_epoch_millis"))
	h.Require.Equal(nowMs-999, queryInt64(t, calls[1], "end_epoch_millis"))
	h.Require.Equal(nowMs-1998, queryInt64(t, calls[2], "end_epoch_millis"))
}

func Test_Model_Deployment_Logs_LosslessAcrossSeamWithinMillisecond(t *testing.T) {
	h := NewCommandHarness(t)
	now := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	baseNs := now.UnixNano()
	msNs := int64(time.Millisecond)
	// 1500 lines. Lines 990..1010 all share one millisecond, straddling the
	// 1000-line page boundary so the first page ends partway through that
	// millisecond; the rest must still be delivered exactly once.
	clusterMs := (now.UnixMilli() - 990)
	lines := make([]fakeLogLine, 1500)
	for i := range lines {
		var tsNs int64
		switch {
		case i < 990:
			tsNs = baseNs - int64(i)*msNs + 500_000
		case i <= 1010:
			// Same millisecond, distinct nanoseconds descending with i.
			tsNs = clusterMs*msNs + int64(1010-i)
		default:
			tsNs = baseNs - int64(i-20)*msNs + 500_000
		}
		lines[i] = fakeLogLine{tsNs: tsNs, message: fmt.Sprintf("log-%d", i)}
	}
	var calls []url.Values
	servePaginatedLogs(h.MockManagementAPI(), logsLogsPath, lines, &calls)
	h.Context = cmd.WithNow(h.Context, func() time.Time { return now })

	err := h.Execute("model", "deployment", "logs",
		"--model-id", "m", "--deployment-id", "d", "--limit", "0", "--output", "jsonl")
	h.Require.NoError(err)

	msgs := jsonlMessages(t, h.Stdout.String())
	h.Require.Len(msgs, 1500)
	for i, m := range msgs {
		h.Require.Equal(fmt.Sprintf("log-%d", i), m)
	}
}

func Test_Model_Deployment_Logs_LimitCapsAndNotes(t *testing.T) {
	h := NewCommandHarness(t)
	now := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	baseNs := now.UnixNano()
	lines := make([]fakeLogLine, 2500)
	for i := range lines {
		lines[i] = fakeLogLine{tsNs: baseNs - int64(i)*int64(time.Millisecond), message: fmt.Sprintf("log-%d", i)}
	}
	var calls []url.Values
	servePaginatedLogs(h.MockManagementAPI(), logsLogsPath, lines, &calls)
	h.Context = cmd.WithNow(h.Context, func() time.Time { return now })

	err := h.Execute("model", "deployment", "logs",
		"--model-id", "m", "--deployment-id", "d", "--limit", "1500", "--output", "jsonl")
	h.Require.NoError(err)

	msgs := jsonlMessages(t, h.Stdout.String())
	// Exactly the newest 1500 lines, and a stderr note that older lines were cut.
	h.Require.Len(msgs, 1500)
	h.Require.Equal("log-0", msgs[0])
	h.Require.Equal("log-1499", msgs[1499])
	h.Require.Contains(h.Stderr.String(), "Reached the --limit of 1500")
	// The second page requests a full page (1000), not the remaining 500.
	h.Require.Len(calls, 2)
	h.Require.Equal(1000, mustAtoi(t, calls[1].Get("limit")))
}

func Test_Model_Deployment_Logs_SingleMillisecondBurstFails(t *testing.T) {
	h := NewCommandHarness(t)
	now := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	ms := now.UnixMilli()
	// 1500 lines all in one millisecond: only 1000 can ever be fetched, so 500
	// are genuinely unreachable. This must fail loudly, not silently truncate.
	lines := make([]fakeLogLine, 1500)
	for i := range lines {
		lines[i] = fakeLogLine{tsNs: ms*int64(time.Millisecond) + int64(1499-i), message: fmt.Sprintf("log-%d", i)}
	}
	var calls []url.Values
	servePaginatedLogs(h.MockManagementAPI(), logsLogsPath, lines, &calls)
	h.Context = cmd.WithNow(h.Context, func() time.Time { return now })

	err := h.Execute("model", "deployment", "logs",
		"--model-id", "m", "--deployment-id", "d", "--limit", "0", "--output", "jsonl")
	h.Require.Error(err)
	h.Require.Contains(err.Error(), "single millisecond")
	// The lines it could fetch were still emitted before failing.
	h.Require.Len(jsonlMessages(t, h.Stdout.String()), 1000)
}

func Test_Model_Deployment_Logs_PageSizeDrivesPagination(t *testing.T) {
	h := NewCommandHarness(t)
	now := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	baseNs := now.UnixNano()
	// 20 lines one millisecond apart, fetched 7 at a time: 7 + 6 + 6 + 1 across
	// four pages (each page after the first re-fetches and dedups one seam line).
	lines := make([]fakeLogLine, 20)
	for i := range lines {
		lines[i] = fakeLogLine{tsNs: baseNs - int64(i)*int64(time.Millisecond), message: fmt.Sprintf("log-%d", i)}
	}
	var calls []url.Values
	servePaginatedLogs(h.MockManagementAPI(), logsLogsPath, lines, &calls)
	h.Context = cmd.WithNow(h.Context, func() time.Time { return now })

	err := h.Execute("model", "deployment", "logs",
		"--model-id", "m", "--deployment-id", "d",
		"--limit", "0", "--page-size", "7", "--output", "jsonl")
	h.Require.NoError(err)

	msgs := jsonlMessages(t, h.Stdout.String())
	h.Require.Len(msgs, 20)
	for i, m := range msgs {
		h.Require.Equal(fmt.Sprintf("log-%d", i), m)
	}
	h.Require.Len(calls, 4)
	for _, c := range calls {
		h.Require.Equal(7, mustAtoi(t, c.Get("limit")))
	}
}

func Test_Model_Deployment_Logs_PageSizeInvalidRejected(t *testing.T) {
	for _, size := range []string{"0", "1001"} {
		h := NewCommandHarness(t)
		err := h.Execute("model", "deployment", "logs",
			"--model-id", "m", "--deployment-id", "d", "--page-size", size)
		h.Require.Error(err)
		h.Require.Contains(err.Error(), "--page-size must be between 1 and 1000")
	}
}

func Test_Model_Deployment_Logs_TailWithPageSizeRejected(t *testing.T) {
	h := NewCommandHarness(t)
	err := h.Execute("model", "deployment", "logs",
		"--model-id", "m", "--deployment-id", "d", "--tail", "--page-size", "7")
	h.Require.Error(err)
	h.Require.Contains(err.Error(), "--tail cannot be combined")
}

func Test_Model_Deployment_Logs_LimitNegativeRejected(t *testing.T) {
	h := NewCommandHarness(t)
	err := h.Execute("model", "deployment", "logs",
		"--model-id", "m", "--deployment-id", "d", "--limit", "-1")
	h.Require.Error(err)
	h.Require.Contains(err.Error(), "--limit must be")
}

func Test_Model_Deployment_Logs_TailWithLimitRejected(t *testing.T) {
	h := NewCommandHarness(t)
	err := h.Execute("model", "deployment", "logs",
		"--model-id", "m", "--deployment-id", "d", "--tail", "--limit", "100")
	h.Require.Error(err)
	h.Require.Contains(err.Error(), "--tail cannot be combined")
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
	var gotQuery url.Values
	captureLogsQuery(h.MockManagementAPI(), logsLogsPath, &gotQuery)
	err := h.Execute("model", "deployment", "logs",
		"--model-id", "m", "--deployment-id", "d",
		"--min-level", "info",
		"--includes", "warn", "--includes", "fail",
		"--excludes", "healthz",
		"--search-pattern", ".*timeout",
		"--replica", "g00r0",
		"--request-id", "req-123")
	h.Require.NoError(err)
	// --min-level is uppercased before sending.
	h.Require.Equal("INFO", gotQuery.Get("min_level"))
	h.Require.Equal([]string{"warn", "fail"}, gotQuery["includes"])
	h.Require.Equal([]string{"healthz"}, gotQuery["excludes"])
	h.Require.Equal(".*timeout", gotQuery.Get("search_pattern"))
	h.Require.Equal("g00r0", gotQuery.Get("replica"))
	h.Require.Equal("req-123", gotQuery.Get("request_id"))
}

func Test_Model_Deployment_Logs_FiltersOmittedWhenUnset(t *testing.T) {
	h := NewCommandHarness(t)
	var gotQuery url.Values
	captureLogsQuery(h.MockManagementAPI(), logsLogsPath, &gotQuery)
	err := h.Execute("model", "deployment", "logs", "--model-id", "m", "--deployment-id", "d")
	h.Require.NoError(err)
	for _, field := range []string{"min_level", "includes", "excludes", "search_pattern", "replica", "request_id"} {
		_, ok := gotQuery[field]
		h.Require.False(ok, "expected %s to be omitted", field)
	}
	// The window bounds, page limit, and sort direction are always sent: the
	// 30-minute default window is resolved client-side to give pagination a
	// fixed floor, and paging requires newest-first ordering.
	_, hasStart := gotQuery["start_epoch_millis"]
	h.Require.True(hasStart)
	_, hasEnd := gotQuery["end_epoch_millis"]
	h.Require.True(hasEnd)
	h.Require.Equal("desc", gotQuery.Get("direction"))
	// Full page size (maxLogPageSize) is requested regardless of --limit.
	h.Require.Equal(1000, mustAtoi(t, gotQuery.Get("limit")))
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
	h.MockManagementAPI().SetRoute("GET", logsLogsPath, 200, logsResponse(
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
	h.MockManagementAPI().SetRoute("GET", logsLogsPath, 200, logsResponse(
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
	h.MockManagementAPI().SetRoute("GET", logsLogsPath, 200, logsResponse(
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
	var gotQuery url.Values
	captureLogsQuery(h.MockManagementAPI(), logsLogsPath, &gotQuery)
	fixed := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	h.Context = cmd.WithNow(h.Context, func() time.Time { return fixed })
	err := h.Execute("model", "deployment", "logs",
		"--model-id", "m", "--deployment-id", "d", "--since", "30m")
	h.Require.NoError(err)
	h.Require.Equal(int(fixed.UnixMilli()), mustAtoi(t, gotQuery.Get("end_epoch_millis")))
	h.Require.Equal(int(fixed.Add(-30*time.Minute).UnixMilli()), mustAtoi(t, gotQuery.Get("start_epoch_millis")))
}

func Test_Model_Deployment_Logs_SinceWithDaySuffix(t *testing.T) {
	h := NewCommandHarness(t)
	var gotQuery url.Values
	captureLogsQuery(h.MockManagementAPI(), logsLogsPath, &gotQuery)
	fixed := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	h.Context = cmd.WithNow(h.Context, func() time.Time { return fixed })
	err := h.Execute("model", "deployment", "logs",
		"--model-id", "m", "--deployment-id", "d", "--since", "3d")
	h.Require.NoError(err)
	h.Require.Equal(int(fixed.Add(-3*24*time.Hour).UnixMilli()), mustAtoi(t, gotQuery.Get("start_epoch_millis")))
}

func Test_Model_Deployment_Logs_StartOnlyDefaultsEndToNow(t *testing.T) {
	h := NewCommandHarness(t)
	var gotQuery url.Values
	captureLogsQuery(h.MockManagementAPI(), logsLogsPath, &gotQuery)
	start := time.Date(2026, 5, 13, 11, 0, 0, 0, time.Local)
	now := time.Date(2026, 5, 13, 12, 0, 0, 0, time.Local)
	h.Context = cmd.WithNow(h.Context, func() time.Time { return now })
	err := h.Execute("model", "deployment", "logs",
		"--model-id", "m", "--deployment-id", "d", "--start", "2026-05-13T11:00:00")
	h.Require.NoError(err)
	// --start is sent as given; end defaults to now, resolved client-side.
	h.Require.Equal(int(start.UnixMilli()), mustAtoi(t, gotQuery.Get("start_epoch_millis")))
	h.Require.Equal(int(now.UnixMilli()), mustAtoi(t, gotQuery.Get("end_epoch_millis")))
}

func Test_Model_Deployment_Logs_EndOnlyDefaultsStartTo30mBefore(t *testing.T) {
	h := NewCommandHarness(t)
	var gotQuery url.Values
	captureLogsQuery(h.MockManagementAPI(), logsLogsPath, &gotQuery)
	end := time.Date(2026, 5, 14, 12, 0, 0, 0, time.Local)
	err := h.Execute("model", "deployment", "logs",
		"--model-id", "m", "--deployment-id", "d", "--end", "2026-05-14T12:00:00")
	h.Require.NoError(err)
	// --end is sent as given; start defaults to 30 minutes before end.
	h.Require.Equal(int(end.UnixMilli()), mustAtoi(t, gotQuery.Get("end_epoch_millis")))
	h.Require.Equal(int(end.Add(-30*time.Minute).UnixMilli()), mustAtoi(t, gotQuery.Get("start_epoch_millis")))
}

func Test_Model_Deployment_Logs_TailTerminatesOnStopStatus(t *testing.T) {
	h := NewCommandHarness(t)
	api := h.MockManagementAPI()
	api.SetRoute("GET", logsLogsPath, 200, logsResponse(
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
	api.SetRoute("GET", logsLogsPath, 200, logsResponse(
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
	api.SetRouteFunc("GET", logsLogsPath, func(w http.ResponseWriter, _ *http.Request) {
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
	api.SetRoute("GET", logsLogsPath, 200, logsResponse(
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
	api.SetRoute("GET", logsLogsPath, 200, logsResponse(
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
