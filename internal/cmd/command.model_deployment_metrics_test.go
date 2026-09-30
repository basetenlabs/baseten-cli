package cmd_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/basetenlabs/baseten-cli/internal/cmd"
)

const metricsPath = "/v1/models/m/deployments/d/metrics"

// metricsResponse builds a GetModelMetricsResponse body.
func metricsResponse(mode string, startMs, endMs int, descriptors []any, valueSets []any) map[string]any {
	return map[string]any{
		"mode":               mode,
		"start_epoch_millis": startMs,
		"end_epoch_millis":   endMs,
		"step_seconds":       nil,
		"metric_descriptors": descriptors,
		"metric_values":      valueSets,
	}
}

func metricsDescriptor(name, kind, unit string, labelSets ...map[string]string) map[string]any {
	sets := make([]any, len(labelSets))
	for i, ls := range labelSets {
		sets[i] = ls
	}
	return map[string]any{"name": name, "kind": kind, "unit_hint": unit, "label_sets": sets}
}

func metricsValueSet(startMs int, values ...[]any) map[string]any {
	vs := make([]any, len(values))
	for i, v := range values {
		vs[i] = v
	}
	return map[string]any{"start_epoch_millis": startMs, "values": vs}
}

func Test_Model_Deployment_Metrics_CurrentWithSinceRejected(t *testing.T) {
	h := NewCommandHarness(t)
	err := h.Execute("model", "deployment", "metrics",
		"--model-id", "m", "--deployment-id", "d", "--since", "1h")
	h.Require.Error(err)
	h.Require.Contains(err.Error(), "only valid with --mode summary or series")
}

func Test_Model_Deployment_Metrics_CurrentWithStartRejected(t *testing.T) {
	h := NewCommandHarness(t)
	err := h.Execute("model", "deployment", "metrics",
		"--model-id", "m", "--deployment-id", "d", "--start", "2026-05-14")
	h.Require.Error(err)
	h.Require.Contains(err.Error(), "only valid with --mode summary or series")
}

func Test_Model_Deployment_Metrics_SinceWithStartRejected(t *testing.T) {
	h := NewCommandHarness(t)
	err := h.Execute("model", "deployment", "metrics",
		"--model-id", "m", "--deployment-id", "d", "--mode", "summary",
		"--since", "1h", "--start", "2026-05-14")
	h.Require.Error(err)
	h.Require.Contains(err.Error(), "--since cannot be combined")
}

func Test_Model_Deployment_Metrics_StartAfterEndRejected(t *testing.T) {
	h := NewCommandHarness(t)
	err := h.Execute("model", "deployment", "metrics",
		"--model-id", "m", "--deployment-id", "d", "--mode", "series",
		"--start", "2026-05-14T13:00:00", "--end", "2026-05-14T12:00:00")
	h.Require.Error(err)
	h.Require.Contains(err.Error(), "--start must be earlier")
}

func Test_Model_Deployment_Metrics_WindowOver7DaysRejected(t *testing.T) {
	h := NewCommandHarness(t)
	err := h.Execute("model", "deployment", "metrics",
		"--model-id", "m", "--deployment-id", "d", "--mode", "series",
		"--start", "2026-05-01", "--end", "2026-05-10")
	h.Require.Error(err)
	h.Require.Contains(err.Error(), "at most 7 days")
}

func Test_Model_Deployment_Metrics_SinceNotPositiveRejected(t *testing.T) {
	h := NewCommandHarness(t)
	err := h.Execute("model", "deployment", "metrics",
		"--model-id", "m", "--deployment-id", "d", "--mode", "summary", "--since", "0")
	h.Require.Error(err)
	h.Require.Contains(err.Error(), "must be a positive duration")
}

func Test_Model_Deployment_Metrics_ModeAndMetricsSent(t *testing.T) {
	h := NewCommandHarness(t)
	api := h.MockManagementAPI()
	var gotQuery url.Values
	api.SetRouteFunc("GET", metricsPath, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(metricsResponse("SUMMARY", 0, 3600000, []any{}, []any{}))
	})
	fixed := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	h.Context = cmd.WithNow(h.Context, func() time.Time { return fixed })
	err := h.Execute("model", "deployment", "metrics",
		"--model-id", "m", "--deployment-id", "d", "--mode", "summary", "--since", "1h",
		"--metric", "baseten_inference_requests_total", "--metric", "baseten_replicas_active")
	h.Require.NoError(err)
	// Mode is uppercased before sending.
	h.Require.Equal("SUMMARY", gotQuery.Get("mode"))
	h.Require.Equal(int(fixed.Add(-time.Hour).UnixMilli()), mustAtoi(t, gotQuery.Get("start_epoch_millis")))
	h.Require.Equal(int(fixed.UnixMilli()), mustAtoi(t, gotQuery.Get("end_epoch_millis")))
	call := api.FindCall("GET", metricsPath)
	h.Require.NotNil(call)
	h.Require.Contains(call.Path, "/metrics")
	// Both metric names are present in the query regardless of array encoding.
	raw := gotQuery.Encode()
	h.Require.Contains(raw, "baseten_inference_requests_total")
	h.Require.Contains(raw, "baseten_replicas_active")
}

func Test_Model_Deployment_Metrics_CurrentSnapshotTable(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", metricsPath, 200, metricsResponse("CURRENT", 0, 0,
		[]any{
			metricsDescriptor("baseten_replicas_active", "GAUGE", "COUNT", map[string]string{}),
			metricsDescriptor("baseten_end_to_end_response_time_seconds", "HISTOGRAM", "SECONDS",
				map[string]string{"quantile": "0.5"}, map[string]string{"quantile": "0.99"}, map[string]string{"stat": "avg"}),
		},
		[]any{metricsValueSet(0, []any{3}, []any{0.12, 0.55, 0.18})},
	))
	err := h.Execute("model", "deployment", "metrics", "--model-id", "m", "--deployment-id", "d")
	h.Require.NoError(err)
	out := h.Stdout.String()
	h.Require.Contains(out, "METRIC")
	h.Require.Contains(out, "QUANTILE")
	h.Require.Contains(out, "STAT")
	h.Require.Contains(out, "baseten_replicas_active")
	h.Require.Contains(out, "0.12s")
	h.Require.Contains(out, "0.18s")
	// current is a point-in-time snapshot: no window line.
	h.Require.NotContains(h.Stderr.String(), "Window:")
}

func Test_Model_Deployment_Metrics_SummaryCounterShowsRate(t *testing.T) {
	h := NewCommandHarness(t)
	// 3600 total over a 1h window -> 1/s, humanized total 3.6k.
	h.MockManagementAPI().SetRoute("GET", metricsPath, 200, metricsResponse("SUMMARY", 0, 3600000,
		[]any{metricsDescriptor("baseten_inference_requests_total", "COUNTER", "COUNT", map[string]string{})},
		[]any{metricsValueSet(0, []any{3600})},
	))
	err := h.Execute("model", "deployment", "metrics",
		"--model-id", "m", "--deployment-id", "d", "--mode", "summary", "--since", "1h")
	h.Require.NoError(err)
	h.Require.Contains(h.Stdout.String(), "3.6k (1/s)")
	// summary reports the resolved window on stderr, with no step (SERIES only).
	stderr := h.Stderr.String()
	h.Require.Contains(stderr, "Window:")
	h.Require.Contains(stderr, "(1h)")
	h.Require.NotContains(stderr, "steps")
}

func Test_Model_Deployment_Metrics_SeriesWindowReportsStep(t *testing.T) {
	h := NewCommandHarness(t)
	resp := metricsResponse("SERIES", 0, 3600000,
		[]any{metricsDescriptor("baseten_inference_requests_total", "COUNTER", "COUNT", map[string]string{})},
		[]any{metricsValueSet(0, []any{0}), metricsValueSet(1, []any{5}), metricsValueSet(2, []any{10})},
	)
	// 30s is the ≤1h resolution; it must render as "30s", not "3" (a naive
	// trailing-"0s" trim mangles the significant zero).
	resp["step_seconds"] = 30
	h.MockManagementAPI().SetRoute("GET", metricsPath, 200, resp)
	err := h.Execute("model", "deployment", "metrics",
		"--model-id", "m", "--deployment-id", "d", "--mode", "series", "--since", "1h")
	h.Require.NoError(err)
	stderr := h.Stderr.String()
	h.Require.Contains(stderr, "Window:")
	h.Require.Contains(stderr, "30s/step")
}

func Test_Model_Deployment_Metrics_SeriesChart(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", metricsPath, 200, metricsResponse("SERIES", 0, 3600000,
		[]any{metricsDescriptor("baseten_inference_requests_total", "COUNTER", "COUNT", map[string]string{})},
		[]any{metricsValueSet(0, []any{0}), metricsValueSet(1, []any{5}), metricsValueSet(2, []any{10})},
	))
	err := h.Execute("model", "deployment", "metrics",
		"--model-id", "m", "--deployment-id", "d", "--mode", "series", "--since", "1h")
	h.Require.NoError(err)
	out := h.Stdout.String()
	h.Require.Contains(out, "baseten_inference_requests_total  [COUNTER · count]")
	h.Require.Contains(out, "▁▅█")
	h.Require.Contains(out, "0 – 10")
	h.Require.Contains(out, "end 10")
}

func Test_Model_Deployment_Metrics_SeriesNoChartTable(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", metricsPath, 200, metricsResponse("SERIES", 0, 3600000,
		[]any{metricsDescriptor("baseten_inference_requests_total", "COUNTER", "COUNT", map[string]string{})},
		[]any{metricsValueSet(0, []any{0}), metricsValueSet(1, []any{5}), metricsValueSet(2, []any{10})},
	))
	err := h.Execute("model", "deployment", "metrics",
		"--model-id", "m", "--deployment-id", "d", "--mode", "series", "--since", "1h", "--no-chart")
	h.Require.NoError(err)
	out := h.Stdout.String()
	h.Require.Contains(out, "METRIC")
	h.Require.Contains(out, "baseten_inference_requests_total")
	h.Require.Contains(out, "10")
	// Sparklines suppressed.
	h.Require.NotContains(out, "█")
}

func Test_Model_Deployment_Metrics_SeriesChartDownsampledAndNullGap(t *testing.T) {
	h := NewCommandHarness(t)
	// 120 steps exceed the 60-rune cap, so the sparkline is downsampled. The
	// first 10 steps are no-data and must render as the null gap rune.
	steps := make([]any, 120)
	for i := range steps {
		if i < 10 {
			steps[i] = metricsValueSet(i, []any{nil})
			continue
		}
		steps[i] = metricsValueSet(i, []any{i})
	}
	h.MockManagementAPI().SetRoute("GET", metricsPath, 200, metricsResponse("SERIES", 0, 3600000,
		[]any{metricsDescriptor("baseten_inference_requests_total", "COUNTER", "COUNT", map[string]string{})},
		steps,
	))
	err := h.Execute("model", "deployment", "metrics",
		"--model-id", "m", "--deployment-id", "d", "--mode", "series", "--since", "1h")
	h.Require.NoError(err)
	spark := findSparklineLine(t, h.Stdout.String())
	h.Require.Equal(60, utf8.RuneCountInString(spark))
	h.Require.Contains(spark, "·")
}

func Test_Model_Deployment_Metrics_SeriesChartShortSeriesNotDownsampled(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", metricsPath, 200, metricsResponse("SERIES", 0, 3600000,
		[]any{metricsDescriptor("baseten_inference_requests_total", "COUNTER", "COUNT", map[string]string{})},
		[]any{metricsValueSet(0, []any{0}), metricsValueSet(1, []any{5}), metricsValueSet(2, []any{10})},
	))
	err := h.Execute("model", "deployment", "metrics",
		"--model-id", "m", "--deployment-id", "d", "--mode", "series", "--since", "1h")
	h.Require.NoError(err)
	spark := findSparklineLine(t, h.Stdout.String())
	h.Require.Equal(3, utf8.RuneCountInString(spark))
}

// findSparklineLine returns the longest contiguous run of sparkline block runes
// and the null gap rune found in the output (the sparkline shares its line with
// the range and end columns).
func findSparklineLine(t *testing.T, out string) string {
	t.Helper()
	var best, cur string
	for _, r := range out {
		if strings.ContainsRune("▁▂▃▄▅▆▇█·", r) {
			cur += string(r)
			if utf8.RuneCountInString(cur) > utf8.RuneCountInString(best) {
				best = cur
			}
			continue
		}
		cur = ""
	}
	if best == "" {
		t.Fatalf("no sparkline found in output:\n%s", out)
	}
	return best
}

func Test_Model_Deployment_Metrics_JSONPassthrough(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", metricsPath, 200, metricsResponse("CURRENT", 0, 0,
		[]any{metricsDescriptor("baseten_replicas_active", "GAUGE", "COUNT", map[string]string{})},
		[]any{metricsValueSet(0, []any{3})},
	))
	err := h.Execute("model", "deployment", "metrics",
		"--model-id", "m", "--deployment-id", "d", "--output", "json")
	h.Require.NoError(err)
	out := strings.TrimSpace(h.Stdout.String())
	h.Require.True(strings.HasPrefix(out, "{"))
	h.Require.Contains(out, `"metric_descriptors"`)
	h.Require.Contains(out, "baseten_replicas_active")
}

func Test_Model_Deployment_Metrics_NoneFound(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", metricsPath, 200, metricsResponse("CURRENT", 0, 0, []any{}, []any{}))
	err := h.Execute("model", "deployment", "metrics", "--model-id", "m", "--deployment-id", "d")
	h.Require.NoError(err)
	h.Require.Contains(h.Stderr.String(), "No metrics found.")
}

func mustAtoi(t *testing.T, s string) int {
	t.Helper()
	v, err := strconv.Atoi(s)
	if err != nil {
		t.Fatalf("not an int: %q", s)
	}
	return v
}
