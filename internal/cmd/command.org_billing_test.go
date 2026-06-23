package cmd_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/basetenlabs/baseten-cli/internal/cmd"
)

// usageSummaryBody is a representative billing usage_summary payload exercising
// all three categories and both the numeric and string cost encodings the
// backend may return.
func usageSummaryBody() map[string]any {
	return map[string]any{
		"dedicated_usage": map[string]any{
			"minutes":      1234,
			"total":        12.34,
			"credits_used": 0,
			"subtotal":     12.34,
		},
		"model_apis_usage": map[string]any{
			// String-encoded costs: the union accepts either form. Also has no
			// "minutes" field, so it should render "-" in the MINUTES column.
			"total":        "5.00",
			"credits_used": "1.00",
			"subtotal":     "4.00",
		},
		"training_usage": map[string]any{
			"minutes":      525600,
			"total":        3,
			"credits_used": 0,
			"subtotal":     3,
		},
	}
}

// findRow returns the rendered table line whose first column equals name.
func findRow(t *testing.T, out, name string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), name) {
			return line
		}
	}
	t.Fatalf("no row starting with %q in:\n%s", name, out)
	return ""
}

func Test_Org_Billing_Usage_Table(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/billing/usage_summary", 200, usageSummaryBody())

	h.Require.NoError(h.Execute("org", "billing", "usage"))

	out := h.Stdout.String()
	h.Require.Contains(out, "CATEGORY")
	h.Require.Contains(out, "MINUTES")
	h.Require.Contains(out, "SUBTOTAL")

	// Per-row values, anchored to their row so a sum bug can't pass by matching
	// a coincidental substring elsewhere.
	dedicated := findRow(t, out, "Dedicated")
	h.Require.Contains(dedicated, "1,234") // minutes grouped
	h.Require.Contains(dedicated, "$12.34")

	modelAPIs := findRow(t, out, "Model APIs")
	h.Require.Contains(modelAPIs, "$5.00")
	h.Require.Contains(modelAPIs, "$4.00")
	// Model APIs are token-billed: no minutes, rendered "-".
	fields := strings.Fields(modelAPIs)
	h.Require.Equal("-", fields[2], "model-apis MINUTES column should be '-'")

	training := findRow(t, out, "Training")
	h.Require.Contains(training, "525,600") // grouped at scale

	h.Require.Contains(h.Stderr.String(), "Window:")
}

func Test_Org_Billing_Usage_TotalRowArithmetic(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/billing/usage_summary", 200, usageSummaryBody())

	h.Require.NoError(h.Execute("org", "billing", "usage"))

	all := findRow(t, h.Stdout.String(), "All")
	h.Require.Contains(all, "$20.34") // total: 12.34 + 5.00 + 3
	h.Require.Contains(all, "$1.00")  // credits: 0 + 1.00 + 0
	h.Require.Contains(all, "$19.34") // subtotal: 12.34 + 4.00 + 3
}

func Test_Org_Billing_Usage_SingleCategoryNoTotalRow(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/billing/usage_summary", 200, map[string]any{
		"dedicated_usage": map[string]any{
			"minutes": 10, "total": 1.5, "credits_used": 0, "subtotal": 1.5,
		},
	})

	h.Require.NoError(h.Execute("org", "billing", "usage"))
	out := h.Stdout.String()
	h.Require.Contains(out, "Dedicated")
	// With a single category the "All" total row is suppressed.
	h.Require.NotContains(out, "All")
}

func Test_Org_Billing_Usage_UnparseableCostRendersDash(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/billing/usage_summary", 200, map[string]any{
		"dedicated_usage": map[string]any{
			"minutes": 5, "total": "not-a-number", "credits_used": 0, "subtotal": 1.0,
		},
	})

	h.Require.NoError(h.Execute("org", "billing", "usage"))
	dedicated := findRow(t, h.Stdout.String(), "Dedicated")
	fields := strings.Fields(dedicated)
	// CATEGORY MINUTES TOTAL CREDITS SUBTOTAL -> TOTAL is index 2.
	h.Require.Equal("-", fields[2], "an unparseable cost should render '-'")
}

func Test_Org_Billing_Usage_DefaultsToSevenDays(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", "/v1/billing/usage_summary", 200, usageSummaryBody())
	now := time.Date(2026, 6, 23, 12, 0, 0, 0, time.UTC)
	h.Context = cmd.WithNow(h.Context, func() time.Time { return now })

	h.Require.NoError(h.Execute("org", "billing", "usage"))

	call := m.FindCall("GET", "/v1/billing/usage_summary")
	h.Require.NotNil(call)
	start, end := parseUsageWindow(t, call)
	h.Require.Equal(now, end.UTC())
	h.Require.Equal(now.Add(-7*24*time.Hour), start.UTC())
}

func Test_Org_Billing_Usage_SinceDays(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", "/v1/billing/usage_summary", 200, usageSummaryBody())
	now := time.Date(2026, 6, 23, 12, 0, 0, 0, time.UTC)
	h.Context = cmd.WithNow(h.Context, func() time.Time { return now })

	h.Require.NoError(h.Execute("org", "billing", "usage", "--since", "30d"))

	call := m.FindCall("GET", "/v1/billing/usage_summary")
	h.Require.NotNil(call)
	start, end := parseUsageWindow(t, call)
	h.Require.Equal(now, end.UTC())
	h.Require.Equal(now.Add(-30*24*time.Hour), start.UTC())
}

func Test_Org_Billing_Usage_ExplicitRange(t *testing.T) {
	h := NewCommandHarness(t)
	m := h.MockManagementAPI()
	m.SetRoute("GET", "/v1/billing/usage_summary", 200, usageSummaryBody())

	h.Require.NoError(h.Execute("org", "billing", "usage",
		"--start", "2026-05-01T00:00:00Z", "--end", "2026-05-08T00:00:00Z"))

	call := m.FindCall("GET", "/v1/billing/usage_summary")
	h.Require.NotNil(call)
	start, end := parseUsageWindow(t, call)
	h.Require.Equal("2026-05-01T00:00:00Z", start.UTC().Format(time.RFC3339))
	h.Require.Equal("2026-05-08T00:00:00Z", end.UTC().Format(time.RFC3339))
}

func Test_Org_Billing_Usage_Empty(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/billing/usage_summary", 200, map[string]any{})

	h.Require.NoError(h.Execute("org", "billing", "usage"))
	h.Require.Equal("", h.Stdout.String())
	h.Require.Contains(h.Stderr.String(), "No usage in the selected window.")
}

func Test_Org_Billing_Usage_JSON(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/billing/usage_summary", 200, usageSummaryBody())

	h.Require.NoError(h.Execute("org", "billing", "usage", "--output", "json"))
	out := h.Stdout.String()
	h.Require.True(strings.HasPrefix(strings.TrimSpace(out), "{"), "JSON output should be an object")
	h.Require.Contains(out, `"dedicated_usage"`)
	h.Require.Contains(out, `"model_apis_usage"`)
}

func Test_Org_Billing_Usage_APIError(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetRoute("GET", "/v1/billing/usage_summary", 500,
		map[string]any{"error": "boom"})

	err := h.Execute("org", "billing", "usage")
	h.Require.Error(err)
	h.Require.Contains(err.Error(), "fetching billing usage")
}

func Test_Org_Billing_Usage_SinceWithStartIsError(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetHandlerFallback(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatalf("server should not be hit on usage error")
	})

	err := h.Execute("org", "billing", "usage", "--since", "7d", "--start", "2026-05-01")
	h.Require.Error(err)
	h.Require.Contains(err.Error(), "--since cannot be combined with --start or --end")
}

func Test_Org_Billing_Usage_StartWithoutEndIsError(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetHandlerFallback(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatalf("server should not be hit on usage error")
	})

	err := h.Execute("org", "billing", "usage", "--start", "2026-05-01")
	h.Require.Error(err)
	h.Require.Contains(err.Error(), "--start and --end must be used together")
}

func Test_Org_Billing_Usage_EqualStartEndIsError(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetHandlerFallback(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatalf("server should not be hit on usage error")
	})

	err := h.Execute("org", "billing", "usage",
		"--start", "2026-05-01T00:00:00Z", "--end", "2026-05-01T00:00:00Z")
	h.Require.Error(err)
	h.Require.Contains(err.Error(), "earlier")
}

func Test_Org_Billing_Usage_ExplicitRangeTooLongIsError(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetHandlerFallback(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatalf("server should not be hit on usage error")
	})

	err := h.Execute("org", "billing", "usage",
		"--start", "2026-05-01T00:00:00Z", "--end", "2026-06-15T00:00:00Z")
	h.Require.Error(err)
	h.Require.Contains(err.Error(), "at most 31 days")
}

func Test_Org_Billing_Usage_SinceRangeTooLongIsError(t *testing.T) {
	h := NewCommandHarness(t)
	h.MockManagementAPI().SetHandlerFallback(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatalf("server should not be hit on usage error")
	})

	err := h.Execute("org", "billing", "usage", "--since", "60d")
	h.Require.Error(err)
	h.Require.Contains(err.Error(), "at most 31d")
}

func Test_Org_Billing_Usage_SinceZeroOrNegativeIsError(t *testing.T) {
	// Use the --flag=value form for the negative case so pflag doesn't read the
	// leading '-' as another flag.
	for _, arg := range []string{"--since=0s", "--since=-5m"} {
		h := NewCommandHarness(t)
		h.MockManagementAPI().SetHandlerFallback(func(_ http.ResponseWriter, _ *http.Request) {
			t.Fatalf("server should not be hit on usage error")
		})

		err := h.Execute("org", "billing", "usage", arg)
		h.Require.Error(err)
		h.Require.Contains(err.Error(), "positive duration")
	}
}

// parseUsageWindow extracts the start_date/end_date query params from a
// recorded request.
func parseUsageWindow(t *testing.T, call *MockAPICall) (start, end time.Time) {
	t.Helper()
	q := call.Query()
	start, err := time.Parse(time.RFC3339, q.Get("start_date"))
	if err != nil {
		t.Fatalf("parsing start_date %q: %v", q.Get("start_date"), err)
	}
	end, err = time.Parse(time.RFC3339, q.Get("end_date"))
	if err != nil {
		t.Fatalf("parsing end_date %q: %v", q.Get("end_date"), err)
	}
	return start, end
}
