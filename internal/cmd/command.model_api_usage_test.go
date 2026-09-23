package cmd_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/basetenlabs/baseten-cli/internal/cmd"
)

// modelAPIUsageBucket builds one time bucket. Results are passed pre-shaped so a
// test can express partial dimensions and null attribution directly.
func modelAPIUsageBucket(start string, results ...map[string]any) map[string]any {
	startTime, err := time.Parse(time.RFC3339, start)
	if err != nil {
		panic(err)
	}
	items := make([]any, 0, len(results))
	for _, r := range results {
		items = append(items, r)
	}
	return map[string]any{
		"start_time": start,
		"end_time":   startTime.Add(24 * time.Hour).Format(time.RFC3339),
		"results":    items,
	}
}

func modelAPIUsageResult(dims map[string]any, requests, input, cached, output int) map[string]any {
	r := map[string]any{
		"request_count":         requests,
		"input_tokens":          input,
		"cached_input_tokens":   cached,
		"uncached_input_tokens": input - cached,
		"output_tokens":         output,
	}
	for k, v := range dims {
		r[k] = v
	}
	return r
}

// modelAPIUsageCell returns whitespace-separated field col of the first output
// line containing marker, for asserting on one column rather than the whole row
// (a bare "-" would otherwise match the dashes in a date).
func modelAPIUsageCell(h *CommandHarness, out, marker string, col int) string {
	for _, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, marker) {
			continue
		}
		fields := strings.Fields(line)
		h.Require.Greater(len(fields), col, "line %q has no field %d", line, col)
		return fields[col]
	}
	h.Require.Fail("no output line contains " + marker)
	return ""
}

func modelAPIUsageBody(buckets ...map[string]any) map[string]any {
	items := make([]any, 0, len(buckets))
	for _, b := range buckets {
		items = append(items, b)
	}
	return map[string]any{
		"items":      items,
		"pagination": map[string]any{"has_more": false},
	}
}

// modelAPIUsageTwoDays is a representative series: one bucket with two models,
// one empty bucket, and one bucket with a single model.
func modelAPIUsageTwoDays() map[string]any {
	return modelAPIUsageBody(
		modelAPIUsageBucket("2026-08-20T00:00:00Z",
			modelAPIUsageResult(map[string]any{"model": "llama-3"}, 12, 3400, 1200, 890),
			modelAPIUsageResult(map[string]any{"model": "kimi-k2"}, 3, 1200, 0, 45),
		),
		modelAPIUsageBucket("2026-08-21T00:00:00Z"),
		modelAPIUsageBucket("2026-08-22T00:00:00Z",
			modelAPIUsageResult(map[string]any{"model": "llama-3"}, 1, 100, 0, 10),
		),
	)
}

func Test_ModelApi_Usage_Table(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/model_apis/usage", 200, modelAPIUsageTwoDays())

	h.Require.NoError(h.Execute("model-api", "usage"))
	out := h.Stdout.String()
	h.Require.Contains(out, "DATE")
	h.Require.Contains(out, "MODEL")
	h.Require.Contains(out, "REQUESTS")
	h.Require.Contains(out, "INPUT")
	h.Require.Contains(out, "CACHED")
	h.Require.Contains(out, "OUTPUT")
	h.Require.Contains(out, "2026-08-20")
	h.Require.Contains(out, "llama-3")
	h.Require.Contains(out, "kimi-k2")
	// Token counts carry thousands separators.
	h.Require.Contains(out, "3,400")
	h.Require.Contains(out, "1,200")
	// The window summary goes to stderr so it stays out of the piped table.
	h.Require.Contains(h.Stderr.String(), "grouped by model")
	h.Require.NotContains(out, "grouped by model")
}

func Test_ModelApi_Usage_RepeatedBucketStampBlanked(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/model_apis/usage", 200, modelAPIUsageTwoDays())

	h.Require.NoError(h.Execute("model-api", "usage"))
	lines := strings.Split(strings.TrimSpace(h.Stdout.String()), "\n")
	// A bucket's second row omits the date so the bucket reads as one group.
	var llama, kimi string
	for _, l := range lines {
		if strings.Contains(l, "llama-3") && strings.Contains(l, "3,400") {
			llama = l
		}
		if strings.Contains(l, "kimi-k2") {
			kimi = l
		}
	}
	h.Require.Contains(llama, "2026-08-20")
	h.Require.NotContains(kimi, "2026-08-20")
}

func Test_ModelApi_Usage_GapMarkerForEmptyBucket(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/model_apis/usage", 200, modelAPIUsageTwoDays())

	h.Require.NoError(h.Execute("model-api", "usage"))
	out := h.Stdout.String()
	// The empty middle bucket stays visible so the series reads as gapless.
	h.Require.Contains(out, "2026-08-21")
	h.Require.Contains(out, "(no usage)")
}

func Test_ModelApi_Usage_TotalsRow(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/model_apis/usage", 200, modelAPIUsageTwoDays())

	h.Require.NoError(h.Execute("model-api", "usage"))
	lines := strings.Split(strings.TrimSpace(h.Stdout.String()), "\n")
	total := lines[len(lines)-1]
	h.Require.Contains(total, "ALL")
	// 12 + 3 + 1 requests, 3400 + 1200 + 100 input, 1200 cached, 890 + 45 + 10 output.
	h.Require.Contains(total, "16")
	h.Require.Contains(total, "4,700")
	h.Require.Contains(total, "1,200")
	h.Require.Contains(total, "945")
}

func Test_ModelApi_Usage_SingleRowHasNoTotalsRow(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/model_apis/usage", 200, modelAPIUsageBody(
		modelAPIUsageBucket("2026-08-22T00:00:00Z",
			modelAPIUsageResult(map[string]any{"model": "llama-3"}, 1, 100, 0, 10),
		),
	))

	h.Require.NoError(h.Execute("model-api", "usage"))
	// A totals row over one data row would just repeat it.
	h.Require.NotContains(h.Stdout.String(), "ALL")
}

func Test_ModelApi_Usage_NoUsageInWindow(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/model_apis/usage", 200, modelAPIUsageBody(
		modelAPIUsageBucket("2026-08-20T00:00:00Z"),
		modelAPIUsageBucket("2026-08-21T00:00:00Z"),
	))

	h.Require.NoError(h.Execute("model-api", "usage"))
	// Buckets came back, but all empty: say so rather than draw a table of gaps.
	h.Require.Equal("", h.Stdout.String())
	h.Require.Contains(h.Stderr.String(), "No usage in the selected window.")
}

func Test_ModelApi_Usage_NoBuckets(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/model_apis/usage", 200, modelAPIUsageBody())

	h.Require.NoError(h.Execute("model-api", "usage"))
	h.Require.Equal("", h.Stdout.String())
	h.Require.Contains(h.Stderr.String(), "No usage in the selected window.")
}

func Test_ModelApi_Usage_BucketsRenderInUTC(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/model_apis/usage", 200, modelAPIUsageBody(
		modelAPIUsageBucket("2026-08-20T00:00:00Z",
			modelAPIUsageResult(map[string]any{"model": "llama-3"}, 1, 100, 0, 10),
		),
	))
	// Buckets are UTC-aligned. Pin a local zone behind UTC so local rendering
	// would visibly shift the daily bucket onto the previous calendar date.
	// TZ alone would not do it: time.Local is resolved once per process.
	local := time.Local
	time.Local = time.FixedZone("test-behind-utc", -8*60*60)
	t.Cleanup(func() { time.Local = local })

	h.Require.NoError(h.Execute("model-api", "usage"))
	h.Require.Contains(h.Stdout.String(), "2026-08-20")
	h.Require.NotContains(h.Stdout.String(), "2026-08-19")
	h.Require.Contains(h.Stderr.String(), "UTC")
}

func Test_ModelApi_Usage_HourlyTimeColumn(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/model_apis/usage", 200, modelAPIUsageBody(
		modelAPIUsageBucket("2026-08-20T13:00:00Z",
			modelAPIUsageResult(map[string]any{"model": "llama-3"}, 1, 100, 0, 10),
		),
	))

	h.Require.NoError(h.Execute("model-api", "usage", "--bucket-width", "1h"))
	out := h.Stdout.String()
	// Sub-daily buckets need the clock, and the column is headed TIME not DATE.
	h.Require.Contains(out, "TIME")
	h.Require.NotContains(out, "DATE")
	h.Require.Contains(out, "2026-08-20 13:00")
}

func Test_ModelApi_Usage_DefaultsToGroupByModel(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", "/v1/model_apis/usage", 200, modelAPIUsageTwoDays())

	h.Require.NoError(h.Execute("model-api", "usage"))

	call := m.FindCall("GET", "/v1/model_apis/usage")
	h.Require.NotNil(call)
	// Pinned client-side so the column set does not move if the backend changes
	// which dimension it defaults to.
	h.Require.Equal([]string{"model"}, call.Query()["group_by"])
}

func Test_ModelApi_Usage_GroupByKebabMapsToWireValues(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", "/v1/model_apis/usage", 200, modelAPIUsageTwoDays())

	h.Require.NoError(h.Execute("model-api", "usage",
		"--group-by", "api-key", "--group-by", "user", "--group-by", "model"))

	call := m.FindCall("GET", "/v1/model_apis/usage")
	h.Require.NotNil(call)
	h.Require.Equal([]string{"api_key", "user", "model"}, call.Query()["group_by"])
	// Column headers follow the requested dimensions, in order.
	out := h.Stdout.String()
	h.Require.Contains(out, "API KEY")
	h.Require.Contains(out, "USER")
	h.Require.Contains(out, "MODEL")
}

func Test_ModelApi_Usage_GroupByDeduplicates(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", "/v1/model_apis/usage", 200, modelAPIUsageTwoDays())

	h.Require.NoError(h.Execute("model-api", "usage", "--group-by", "user", "--group-by", "user"))

	call := m.FindCall("GET", "/v1/model_apis/usage")
	h.Require.NotNil(call)
	// A repeated dimension would otherwise duplicate the column.
	h.Require.Equal([]string{"user"}, call.Query()["group_by"])
}

func Test_ModelApi_Usage_InvalidGroupBy(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetHandlerFallback(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatalf("server should not be hit on validation error")
	})

	err := h.Execute("model-api", "usage", "--group-by", "service-tier")
	h.Require.Error(err)
	h.Require.Contains(err.Error(), `invalid --group-by "service-tier"`)
	h.Require.Contains(err.Error(), "api-key, user, model")
}

func Test_ModelApi_Usage_NullUserRendersAndIsExplained(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/model_apis/usage", 200, modelAPIUsageBody(
		modelAPIUsageBucket("2026-08-20T00:00:00Z",
			modelAPIUsageResult(map[string]any{"user_id": nil}, 2, 200, 0, 20),
			modelAPIUsageResult(map[string]any{"user_id": "usr_1"}, 5, 500, 0, 50),
		),
	))

	h.Require.NoError(h.Execute("model-api", "usage", "--group-by", "user"))
	out := h.Stdout.String()
	h.Require.Contains(out, "usr_1")
	// Unattributed usage is real data, so it gets a row of its own with the user
	// column marked absent, plus an explanation on stderr.
	h.Require.Equal("-", modelAPIUsageCell(h, out, "200", 1))
	h.Require.Contains(h.Stderr.String(), "Rows with no user")
}

func Test_ModelApi_Usage_EmptyAPIKeyPrefixRendersAsAbsent(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/model_apis/usage", 200, modelAPIUsageBody(
		modelAPIUsageBucket("2026-08-20T00:00:00Z",
			// Non-API-key credentials report an empty prefix rather than null.
			modelAPIUsageResult(map[string]any{"api_key_prefix": ""}, 3, 300, 0, 30),
		),
	))

	h.Require.NoError(h.Execute("model-api", "usage", "--group-by", "api-key"))
	h.Require.Equal("-", modelAPIUsageCell(h, h.Stdout.String(), "300", 1))
}

func Test_ModelApi_Usage_NoUserExplanationOnlyWhenGrouped(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/model_apis/usage", 200, modelAPIUsageTwoDays())

	h.Require.NoError(h.Execute("model-api", "usage"))
	// Grouping by model alone never surfaces a user, so the note would be noise.
	h.Require.NotContains(h.Stderr.String(), "Rows with no user")
}

func Test_ModelApi_Usage_Filters(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", "/v1/model_apis/usage", 200, modelAPIUsageTwoDays())

	h.Require.NoError(h.Execute("model-api", "usage",
		"--api-key-prefix", "bsnt_abc",
		"--api-key-prefix", "bsnt_def",
		"--user-id", "usr_1",
		"--model", "llama-3",
	))

	call := m.FindCall("GET", "/v1/model_apis/usage")
	h.Require.NotNil(call)
	q := call.Query()
	h.Require.Equal([]string{"bsnt_abc", "bsnt_def"}, q["api_keys"])
	h.Require.Equal([]string{"usr_1"}, q["user_ids"])
	h.Require.Equal([]string{"llama-3"}, q["models"])
}

func Test_ModelApi_Usage_DefaultWindowPerBucketWidth(t *testing.T) {
	now := time.Date(2026, 8, 25, 12, 30, 0, 0, time.UTC)
	for _, tc := range []struct {
		width string
		back  time.Duration
	}{
		{"1d", 7 * 24 * time.Hour},
		{"1h", 24 * time.Hour},
		{"1m", 60 * time.Minute},
	} {
		t.Run(tc.width, func(t *testing.T) {
			h := NewCommandHarness(t)
			m := h.MockManagementAPI()
			m.SetRoute("GET", "/v1/model_apis/usage", 200, modelAPIUsageTwoDays())
			h.Context = cmd.WithNow(h.Context, func() time.Time { return now })

			h.Require.NoError(h.Execute("model-api", "usage", "--bucket-width", tc.width))

			call := m.FindCall("GET", "/v1/model_apis/usage")
			h.Require.NotNil(call)
			q := call.Query()
			// start_time is required on the first page, so an unspecified window
			// spans the backend's default bucket count for the width.
			h.Require.Equal(now.Add(-tc.back).Format(time.RFC3339), q.Get("start_time"))
			h.Require.Equal(now.Format(time.RFC3339), q.Get("end_time"))
			h.Require.Equal(tc.width, q.Get("bucket_width"))
		})
	}
}

func Test_ModelApi_Usage_SinceWindow(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", "/v1/model_apis/usage", 200, modelAPIUsageTwoDays())
	now := time.Date(2026, 8, 25, 12, 30, 0, 0, time.UTC)
	h.Context = cmd.WithNow(h.Context, func() time.Time { return now })

	h.Require.NoError(h.Execute("model-api", "usage", "--since", "3h"))

	call := m.FindCall("GET", "/v1/model_apis/usage")
	h.Require.NotNil(call)
	h.Require.Equal(now.Add(-3*time.Hour).Format(time.RFC3339), call.Query().Get("start_time"))
	h.Require.Equal(now.Format(time.RFC3339), call.Query().Get("end_time"))
}

func Test_ModelApi_Usage_StartAndEndWindow(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", "/v1/model_apis/usage", 200, modelAPIUsageTwoDays())

	h.Require.NoError(h.Execute("model-api", "usage",
		"--start", "2026-08-01T00:00:00Z", "--end", "2026-08-05T00:00:00Z"))

	call := m.FindCall("GET", "/v1/model_apis/usage")
	h.Require.NotNil(call)
	h.Require.Equal("2026-08-01T00:00:00Z", call.Query().Get("start_time"))
	h.Require.Equal("2026-08-05T00:00:00Z", call.Query().Get("end_time"))
}

func Test_ModelApi_Usage_EndOnlyWindowGetsDefaultSpan(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", "/v1/model_apis/usage", 200, modelAPIUsageTwoDays())

	h.Require.NoError(h.Execute("model-api", "usage", "--end", "2026-08-20T00:00:00Z"))

	call := m.FindCall("GET", "/v1/model_apis/usage")
	h.Require.NotNil(call)
	// start_time is required, so --end alone still spans the default count back.
	h.Require.Equal("2026-08-13T00:00:00Z", call.Query().Get("start_time"))
	h.Require.Equal("2026-08-20T00:00:00Z", call.Query().Get("end_time"))
}

func Test_ModelApi_Usage_SinceWithStartIsError(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetHandlerFallback(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatalf("server should not be hit on validation error")
	})

	err := h.Execute("model-api", "usage", "--since", "1h", "--start", "2026-08-01")
	h.Require.Error(err)
	h.Require.Contains(err.Error(), "--since cannot be combined with --start or --end")
}

func Test_ModelApi_Usage_ZeroSinceIsError(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetHandlerFallback(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatalf("server should not be hit on validation error")
	})

	err := h.Execute("model-api", "usage", "--since", "0")
	h.Require.Error(err)
	h.Require.Contains(err.Error(), "--since must be a positive duration")
}

func Test_ModelApi_Usage_StartNotBeforeEndIsError(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetHandlerFallback(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatalf("server should not be hit on validation error")
	})

	err := h.Execute("model-api", "usage",
		"--start", "2026-08-05T00:00:00Z", "--end", "2026-08-01T00:00:00Z")
	h.Require.Error(err)
	h.Require.Contains(err.Error(), "--start must be earlier than --end")
}

func Test_ModelApi_Usage_FutureStartWithoutEndIsError(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetHandlerFallback(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatalf("server should not be hit on validation error")
	})
	now := time.Date(2026, 8, 25, 12, 30, 0, 0, time.UTC)
	h.Context = cmd.WithNow(h.Context, func() time.Time { return now })

	err := h.Execute("model-api", "usage", "--start", "2026-09-01T00:00:00Z")
	h.Require.Error(err)
	// --end defaulted to now, so the message must not blame a flag the user
	// never passed.
	h.Require.Contains(err.Error(), "--start must be in the past when --end is omitted")
}

func Test_ModelApi_Usage_NegativeLimitIsError(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetHandlerFallback(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatalf("server should not be hit on validation error")
	})

	err := h.Execute("model-api", "usage", "--limit", "-1")
	h.Require.Error(err)
	h.Require.Contains(err.Error(), "--limit must be zero (no limit) or a positive number")
}

func Test_ModelApi_Usage_PageSizeDefaultsToWidthMaximum(t *testing.T) {
	for _, tc := range []struct{ width, limit string }{
		{"1d", "31"},
		{"1h", "168"},
		{"1m", "1440"},
	} {
		t.Run(tc.width, func(t *testing.T) {
			h := NewCommandHarness(t)
			m := h.MockManagementAPI()
			m.SetRoute("GET", "/v1/model_apis/usage", 200, modelAPIUsageTwoDays())

			h.Require.NoError(h.Execute("model-api", "usage", "--bucket-width", tc.width))

			call := m.FindCall("GET", "/v1/model_apis/usage")
			h.Require.NotNil(call)
			h.Require.Equal(tc.limit, call.Query().Get("limit"))
		})
	}
}

func Test_ModelApi_Usage_PageSizeAboveWidthMaximumIsError(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetHandlerFallback(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatalf("server should not be hit on validation error")
	})

	// Above the width's maximum the backend would clamp, making a full page look
	// short and ending pagination a page early.
	err := h.Execute("model-api", "usage", "--bucket-width", "1d", "--page-size", "32")
	h.Require.Error(err)
	h.Require.Contains(err.Error(), "--page-size must be between 1 and 31")
}

func Test_ModelApi_Usage_PaginatesAllPages(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	// One bucket per page; the second page is reached only by following the
	// cursor, so seeing both models proves the pager advanced.
	m.SetRouteFunc("GET", "/v1/model_apis/usage", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("cursor") == "" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"items": []any{modelAPIUsageBucket("2026-08-20T00:00:00Z",
					modelAPIUsageResult(map[string]any{"model": "llama-3"}, 1, 100, 0, 10))},
				"pagination": map[string]any{"has_more": true, "cursor": "next-1"},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"items": []any{modelAPIUsageBucket("2026-08-21T00:00:00Z",
				modelAPIUsageResult(map[string]any{"model": "kimi-k2"}, 2, 200, 0, 20))},
			"pagination": map[string]any{"has_more": false},
		})
	})

	h.Require.NoError(h.Execute("model-api", "usage", "--page-size", "1"))
	out := h.Stdout.String()
	h.Require.Contains(out, "llama-3")
	h.Require.Contains(out, "kimi-k2")

	calls := m.Calls()
	h.Require.Equal(2, len(calls))
	h.Require.Equal("next-1", calls[1].Query().Get("cursor"))
}

func Test_ModelApi_Usage_LimitCapsBuckets(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", "/v1/model_apis/usage", 200, modelAPIUsageTwoDays())

	h.Require.NoError(h.Execute("model-api", "usage", "--limit", "1"))
	out := h.Stdout.String()
	// The first bucket's rows survive; later buckets are cut.
	h.Require.Contains(out, "2026-08-20")
	h.Require.NotContains(out, "2026-08-21")
	h.Require.NotContains(out, "2026-08-22")
	h.Require.Contains(h.Stderr.String(), "Reached the --limit of 1 buckets")
}

func Test_ModelApi_Usage_LimitAtExactCountHasNoNote(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", "/v1/model_apis/usage", 200, modelAPIUsageTwoDays())

	h.Require.NoError(h.Execute("model-api", "usage", "--limit", "3"))
	// Exactly consumed the series, so nothing was cut off.
	h.Require.NotContains(h.Stderr.String(), "Reached the --limit")
}

func Test_ModelApi_Usage_JSON(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/model_apis/usage", 200, modelAPIUsageTwoDays())

	h.Require.NoError(h.Execute("model-api", "usage", "--output", "json"))
	out := strings.TrimSpace(h.Stdout.String())
	h.Require.True(strings.HasPrefix(out, "["), "JSON output should be an array")
	h.Require.Contains(out, `"start_time"`)
	h.Require.Contains(out, `"llama-3"`)
	// The full result is passed through, including the field the table derives.
	h.Require.Contains(out, `"uncached_input_tokens"`)
}

func Test_ModelApi_Usage_JSONL(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/model_apis/usage", 200, modelAPIUsageTwoDays())

	h.Require.NoError(h.Execute("model-api", "usage", "--output", "jsonl"))
	lines := strings.Split(strings.TrimSpace(h.Stdout.String()), "\n")
	// One record per bucket, including the empty one.
	h.Require.Equal(3, len(lines))
	for _, l := range lines {
		var bucket map[string]any
		h.Require.NoError(json.Unmarshal([]byte(l), &bucket))
		h.Require.Contains(bucket, "start_time")
	}
}

func Test_ModelApi_Usage_JSONEmptyIsEmptyArray(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/model_apis/usage", 200, modelAPIUsageBody())

	h.Require.NoError(h.Execute("model-api", "usage", "--output", "json"))
	h.Require.Equal("[]", strings.TrimSpace(h.Stdout.String()))
	// The text-mode notice would corrupt a JSON consumer's stream.
	h.Require.NotContains(h.Stderr.String(), "No usage in the selected window.")
}
