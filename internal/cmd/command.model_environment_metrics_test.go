package cmd_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/basetenlabs/baseten-cli/internal/cmd"
)

const envMetricsPath = "/v1/models/m/environments/production/metrics"

func Test_Model_Environment_Metrics_CurrentWithSinceRejected(t *testing.T) {
	h := NewCommandHarness(t)
	err := h.Execute("model", "environment", "metrics",
		"--model-id", "m", "--environment", "production", "--since", "1h")
	h.Require.Error(err)
	h.Require.Contains(err.Error(), "only valid with --mode summary or series")
}

func Test_Model_Environment_Metrics_ModeAndMetricsSent(t *testing.T) {
	h := NewCommandHarness(t)
	api := h.MockManagementAPI()
	var gotQuery url.Values
	api.SetRouteFunc("GET", envMetricsPath, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(metricsResponse("SUMMARY", 0, 3600000, []any{}, []any{}))
	})
	fixed := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	h.Context = cmd.WithNow(h.Context, func() time.Time { return fixed })
	err := h.Execute("model", "environment", "metrics",
		"--model-id", "m", "--environment", "production", "--mode", "summary", "--since", "1h",
		"--metric", "baseten_inference_requests_total", "--metric", "baseten_replicas_active")
	h.Require.NoError(err)
	// Mode is uppercased before sending.
	h.Require.Equal("SUMMARY", gotQuery.Get("mode"))
	h.Require.Equal(int(fixed.Add(-time.Hour).UnixMilli()), mustAtoi(t, gotQuery.Get("start_epoch_millis")))
	h.Require.Equal(int(fixed.UnixMilli()), mustAtoi(t, gotQuery.Get("end_epoch_millis")))
	call := api.FindCall("GET", envMetricsPath)
	h.Require.NotNil(call)
	raw := gotQuery.Encode()
	h.Require.Contains(raw, "baseten_inference_requests_total")
	h.Require.Contains(raw, "baseten_replicas_active")
}

func Test_Model_Environment_Metrics_CurrentSnapshotTable(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", envMetricsPath, 200, metricsResponse("CURRENT", 0, 0,
		[]any{
			metricsDescriptor("baseten_replicas_active", "GAUGE", "COUNT", map[string]string{}),
			metricsDescriptor("baseten_end_to_end_response_time_seconds", "HISTOGRAM", "SECONDS",
				map[string]string{"quantile": "0.5"}, map[string]string{"quantile": "0.99"}),
		},
		[]any{metricsValueSet(0, []any{3}, []any{0.12, 0.55})},
	))
	err := h.Execute("model", "environment", "metrics",
		"--model-id", "m", "--environment", "production")
	h.Require.NoError(err)
	out := h.Stdout.String()
	h.Require.Contains(out, "METRIC")
	h.Require.Contains(out, "QUANTILE")
	h.Require.Contains(out, "baseten_replicas_active")
	h.Require.Contains(out, "0.12s")
	// current is a point-in-time snapshot: no window line.
	h.Require.NotContains(h.Stderr.String(), "Window:")
}

func Test_Model_Environment_Metrics_JSONPassthrough(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", envMetricsPath, 200, metricsResponse("CURRENT", 0, 0,
		[]any{metricsDescriptor("baseten_replicas_active", "GAUGE", "COUNT", map[string]string{})},
		[]any{metricsValueSet(0, []any{3})},
	))
	err := h.Execute("model", "environment", "metrics",
		"--model-id", "m", "--environment", "production", "--output", "json")
	h.Require.NoError(err)
	out := strings.TrimSpace(h.Stdout.String())
	h.Require.True(strings.HasPrefix(out, "{"))
	h.Require.Contains(out, `"metric_descriptors"`)
	h.Require.Contains(out, "baseten_replicas_active")
}
