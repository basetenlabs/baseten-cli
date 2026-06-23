package cmd

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-go/client/managementapi"
)

// maxBillingRange caps the usage window; the billing endpoint rejects ranges
// longer than 31 days.
const maxBillingRange = 31 * 24 * time.Hour

// billingEarliest is the earliest date the usage endpoint can be queried for.
var billingEarliest = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func init() {
	Register("org billing usage", commandOrgBillingUsage)
}

func commandOrgBillingUsage(ctx *CommandContext, flags *cmd.OrgBillingUsageFlags) error {
	hasStart := !flags.Start.IsZero()
	hasEnd := !flags.End.IsZero()
	// Use Changed rather than the zero value so an explicit --since 0 fails the
	// positive-duration check below instead of being silently replaced by the
	// default.
	hasSince := ctx.Command.Flags().Changed("since")

	if hasSince && (hasStart || hasEnd) {
		return cmd.NewErrUsagef("--since cannot be combined with --start or --end")
	}

	var start, end time.Time
	if hasStart || hasEnd {
		// The endpoint requires both bounds; there is no server-side backfill.
		if !hasStart || !hasEnd {
			return cmd.NewErrUsagef("--start and --end must be used together")
		}
		if !flags.Start.Before(flags.End) {
			return cmd.NewErrUsagef("--start must be earlier than --end")
		}
		if flags.End.Sub(flags.Start) > maxBillingRange {
			return cmd.NewErrUsagef("usage window must be at most 31 days; narrow --start/--end")
		}
		start, end = flags.Start, flags.End
	} else {
		since := flags.Since
		if !hasSince {
			since = 7 * 24 * time.Hour
		}
		if since <= 0 {
			return cmd.NewErrUsagef("--since must be a positive duration")
		}
		if since > maxBillingRange {
			return cmd.NewErrUsagef("--since must be at most 31d")
		}
		end = ctx.Now()
		start = end.Add(-since)
	}

	if start.Before(billingEarliest) {
		return cmd.NewErrUsagef("usage is not available before %s", billingEarliest.Format("2006-01-02"))
	}

	api, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}

	summary, err := api.API().GetBillingUsageSummary(ctx, managementapi.GetV1BillingUsageSummaryParams{
		StartDate: start,
		EndDate:   end,
	})
	if err != nil {
		return fmt.Errorf("fetching billing usage: %w", err)
	}

	if ctx.JSON {
		ctx.OutputJSON(summary)
		return nil
	}

	// Print the resolved window with its time-of-day: the bounds are precise
	// instants (--since is now-relative), so a date alone would misreport the
	// edges by up to a day. Matches the model-deployment metrics window line.
	ctx.LogLine(fmt.Sprintf("Window: %s – %s local",
		start.Local().Format("2006-01-02 15:04"), end.Local().Format("2006-01-02 15:04")))

	headers := []string{"CATEGORY", "MINUTES", "TOTAL", "CREDITS", "SUBTOTAL"}
	rightAligned := []int{1, 2, 3, 4}
	var rows [][]string
	var sumTotal, sumCredits, sumSubtotal float64

	addRow := func(name string, minutes *int, total, credits, subtotal json.Marshaler) {
		t, _ := billingMoney(total)
		c, _ := billingMoney(credits)
		s, _ := billingMoney(subtotal)
		sumTotal += t
		sumCredits += c
		sumSubtotal += s
		rows = append(rows, []string{
			name,
			billingMinutes(minutes),
			billingMoneyString(total),
			billingMoneyString(credits),
			billingMoneyString(subtotal),
		})
	}

	if d := summary.DedicatedUsage; d != nil {
		addRow("Dedicated", &d.Minutes, &d.Total, &d.CreditsUsed, &d.Subtotal)
	}
	if m := summary.ModelApisUsage; m != nil {
		addRow("Model APIs", nil, &m.Total, &m.CreditsUsed, &m.Subtotal)
	}
	if tr := summary.TrainingUsage; tr != nil {
		addRow("Training", &tr.Minutes, &tr.Total, &tr.CreditsUsed, &tr.Subtotal)
	}

	if len(rows) == 0 {
		ctx.LogLine("No usage in the selected window.")
		return nil
	}

	// A trailing total row only earns its keep when more than one category is
	// present; with a single row it would just repeat it.
	if len(rows) > 1 {
		rows = append(rows, []string{
			"All", "",
			billingFormatMoney(sumTotal), billingFormatMoney(sumCredits), billingFormatMoney(sumSubtotal),
		})
	}

	ctx.OutputTable(TableOutput{Headers: headers, Rows: rows, RightAlignedColumns: rightAligned})
	return nil
}

// billingMinutes renders an optional minutes count with thousands separators,
// using "-" for categories (e.g. model APIs) that are billed by tokens rather
// than time.
func billingMinutes(minutes *int) string {
	if minutes == nil {
		return "-"
	}
	return billingGroupDigits(strconv.Itoa(*minutes))
}

// billingMoney parses a cost field into a float. The backend models costs as a
// union of number or string, so the value is recovered from its JSON form
// either way. ok is false for an absent or unparseable value.
func billingMoney(m json.Marshaler) (value float64, ok bool) {
	if m == nil {
		return 0, false
	}
	b, err := m.MarshalJSON()
	if err != nil || len(b) == 0 || string(b) == "null" {
		return 0, false
	}
	s := string(b)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = s[1 : len(s)-1]
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return f, true
}

// billingMoneyString renders a cost field as a USD amount, or "-" when absent.
func billingMoneyString(m json.Marshaler) string {
	f, ok := billingMoney(m)
	if !ok {
		return "-"
	}
	return billingFormatMoney(f)
}

// billingFormatMoney renders a dollar amount with thousands separators, e.g.
// $1,234.56 or -$1,234.56. The billing API reports every cost in dollars (there
// is no currency field), so the USD sign is not an assumption.
func billingFormatMoney(f float64) string {
	neg := f < 0
	if neg {
		f = -f
	}
	s := strconv.FormatFloat(f, 'f', 2, 64)
	intPart, frac, _ := strings.Cut(s, ".")
	out := "$" + billingGroupDigits(intPart) + "." + frac
	if neg {
		out = "-" + out
	}
	return out
}

// billingGroupDigits inserts commas every three digits into a string of digits
// (no sign, no decimal point).
func billingGroupDigits(s string) string {
	if len(s) <= 3 {
		return s
	}
	var b strings.Builder
	if pre := len(s) % 3; pre > 0 {
		b.WriteString(s[:pre])
		b.WriteByte(',')
		s = s[pre:]
	}
	for i := 0; i < len(s); i += 3 {
		b.WriteString(s[i : i+3])
		if i+3 < len(s) {
			b.WriteByte(',')
		}
	}
	return b.String()
}
