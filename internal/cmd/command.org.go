package cmd

import (
	"encoding/json"
	"fmt"
	"slices"
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
	Register("org audit-logs", commandOrgAuditLogs)
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
		return cmd.NewErrUsagef("usage is not available before %s UTC", billingEarliest.Format("2006-01-02"))
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
	// Track whether every contributing value in a column parsed. A single
	// unparseable cost makes that column's grand total an understatement, so we
	// render "-" rather than a confident-looking partial sum.
	okTotal, okCredits, okSubtotal := true, true, true

	addRow := func(name string, minutes *int, total, credits, subtotal json.Marshaler) {
		if t, ok := billingMoney(total); ok {
			sumTotal += t
		} else {
			okTotal = false
		}
		if c, ok := billingMoney(credits); ok {
			sumCredits += c
		} else {
			okCredits = false
		}
		if s, ok := billingMoney(subtotal); ok {
			sumSubtotal += s
		} else {
			okSubtotal = false
		}
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
			billingSumCell(sumTotal, okTotal),
			billingSumCell(sumCredits, okCredits),
			billingSumCell(sumSubtotal, okSubtotal),
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

// billingSumCell renders a column's grand total, or "-" when any contributing
// value failed to parse (in which case the sum would understate the truth).
func billingSumCell(sum float64, ok bool) string {
	if !ok {
		return "-"
	}
	return billingFormatMoney(sum)
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

func commandOrgAuditLogs(ctx *CommandContext, flags *cmd.OrgAuditLogsFlags) error {
	api, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	fetch := func(q auditLogQuery) (*managementapi.ListAuditLogsResponse, error) {
		return api.API().GetAuditLogs(ctx, orgAuditLogParams(q))
	}
	return runAuditLogs(ctx, &flags.AuditLogFlags, fetch)
}

// Allowed lowercase-kebab CLI values, mapped to the ALL_CAPS backend enums by
// auditEnumValues (uppercasing and swapping '-' for '_').
var (
	auditEventTypeGroups = []string{
		"activated-deactivated", "api-keys", "autoscaling-settings", "deleted",
		"deployed", "directory-group-management", "environment-settings", "gateway",
		"instance-type-changed", "promoted", "replica-terminated", "secrets", "ssh",
		"user-management", "webhook-signing-secrets",
	}
	auditSources = []string{"ui", "api", "mcp", "other"}
)

// auditLogQuery is the transport-neutral set of audit-log query parameters
// shared by the org and model endpoints. Each command maps it onto its own
// generated query-params type.
type auditLogQuery struct {
	Cursor           *string
	Limit            *int
	Direction        *managementapi.AuditLogSortDirection
	Search           *string
	EventTypeGroups  *[]managementapi.AuditLogEventTypeGroup
	UserIds          *[]string
	DeploymentIds    *[]string
	EnvironmentNames *[]string
	Sources          *[]managementapi.AuditLogSource
	StartEpochMillis *int
	EndEpochMillis   *int
}

// auditLogFetcher fetches one page of audit-log entries for the given query.
type auditLogFetcher func(q auditLogQuery) (*managementapi.ListAuditLogsResponse, error)

// orgAuditLogParams maps a transport-neutral auditLogQuery onto the org
// audit-logs GET query-params type.
func orgAuditLogParams(q auditLogQuery) managementapi.GetV1AuditLogsParams {
	return managementapi.GetV1AuditLogsParams{
		Cursor:           q.Cursor,
		Limit:            q.Limit,
		Direction:        q.Direction,
		Search:           q.Search,
		EventTypeGroups:  q.EventTypeGroups,
		UserIds:          q.UserIds,
		DeploymentIds:    q.DeploymentIds,
		EnvironmentNames: q.EnvironmentNames,
		Sources:          q.Sources,
		StartEpochMillis: q.StartEpochMillis,
		EndEpochMillis:   q.EndEpochMillis,
	}
}

// runAuditLogs implements the shared audit-logs command flow for both the org
// and model commands. It validates flags, resolves the time window and filters
// into a query, then pages through the cursor-based endpoint via fetch until
// --limit entries are collected or the results are exhausted.
func runAuditLogs(ctx *CommandContext, flags *cmd.AuditLogFlags, fetch auditLogFetcher) error {
	hasStart := !flags.Start.IsZero()
	hasEnd := !flags.End.IsZero()
	// Use Changed rather than the zero value so an explicit --since 0 fails the
	// positive-duration check below instead of being silently dropped.
	hasSince := ctx.Command.Flags().Changed("since")
	if hasSince && (hasStart || hasEnd) {
		return cmd.NewErrUsagef("--since cannot be combined with --start or --end")
	}
	if flags.Limit < 0 {
		return cmd.NewErrUsagef("--limit must be zero (no limit) or a positive number")
	}
	// page-size is capped at the backend's max page limit: requesting more would
	// be clamped server-side, making a full page look short and ending
	// pagination one page early.
	if flags.PageSize < 1 || flags.PageSize > maxAuditLogPageSize {
		return cmd.NewErrUsagef("--page-size must be between 1 and %d", maxAuditLogPageSize)
	}

	var q auditLogQuery

	// Resolve the optional [start, end] window. Unlike the logs command there is
	// no default window and no maximum: an unset bound is left nil so the server
	// applies its own default (history start / now).
	if hasSince {
		if flags.Since <= 0 {
			return cmd.NewErrUsagef("--since must be a positive duration")
		}
		now := ctx.Now()
		start := int(now.Add(-flags.Since).UnixMilli())
		end := int(now.UnixMilli())
		q.StartEpochMillis = &start
		q.EndEpochMillis = &end
	} else {
		if hasStart && hasEnd && !flags.Start.Before(flags.End) {
			return cmd.NewErrUsagef("--start must be earlier than --end")
		}
		if hasStart {
			start := int(flags.Start.UnixMilli())
			q.StartEpochMillis = &start
		}
		if hasEnd {
			end := int(flags.End.UnixMilli())
			q.EndEpochMillis = &end
		}
	}

	direction := managementapi.AuditLogSortDirection(strings.ToUpper(flags.Direction))
	q.Direction = &direction
	if flags.Search != "" {
		q.Search = &flags.Search
	}
	if len(flags.EventTypeGroups) > 0 {
		groups, err := auditEnumValues[managementapi.AuditLogEventTypeGroup]("event-type-group", flags.EventTypeGroups, auditEventTypeGroups)
		if err != nil {
			return err
		}
		q.EventTypeGroups = &groups
	}
	if len(flags.Sources) > 0 {
		sources, err := auditEnumValues[managementapi.AuditLogSource]("source", flags.Sources, auditSources)
		if err != nil {
			return err
		}
		q.Sources = &sources
	}
	if len(flags.UserIDs) > 0 {
		q.UserIds = &flags.UserIDs
	}
	if len(flags.DeploymentIDs) > 0 {
		q.DeploymentIds = &flags.DeploymentIDs
	}
	if len(flags.Environments) > 0 {
		q.EnvironmentNames = &flags.Environments
	}

	return paginateAuditLogs(ctx, q, flags.Limit, flags.PageSize, fetch)
}

const maxAuditLogPageSize = 200

// paginateAuditLogs pages through the cursor-based audit-logs endpoint,
// emitting entries newest-first (subject to --direction) until limit entries
// have been emitted (limit 0 means no limit) or the results are exhausted. For
// --output json/jsonl it streams each entry via a JSON array writer; for text
// it collects rows and renders a single table.
func paginateAuditLogs(ctx *CommandContext, q auditLogQuery, limit, pageSize int, fetch auditLogFetcher) error {
	var jw *JSONArrayWriter
	if ctx.JSON {
		jw = ctx.NewJSONArrayWriter()
		defer jw.Close()
	}
	var rows [][]string

	remaining := limit
	hitLimit := false
	var cursor *string
pages:
	for {
		pageLimit := pageSize
		if remaining > 0 && remaining < pageLimit {
			pageLimit = remaining
		}
		q.Cursor = cursor
		q.Limit = &pageLimit
		resp, err := fetch(q)
		if err != nil {
			return fmt.Errorf("listing audit logs: %w", err)
		}
		for i := range resp.Items {
			entry := resp.Items[i]
			if ctx.JSON {
				jw.Write(entry)
			} else {
				rows = append(rows, auditLogRow(entry))
			}
			if remaining > 0 {
				remaining--
				if remaining == 0 {
					// More entries exist if this page reported another page or
					// still had unemitted items of its own.
					hitLimit = resp.Pagination.HasMore || i < len(resp.Items)-1
					break pages
				}
			}
		}
		if !resp.Pagination.HasMore || resp.Pagination.Cursor == nil {
			break
		}
		cursor = resp.Pagination.Cursor
	}

	if ctx.JSON {
		return nil
	}
	if len(rows) == 0 {
		ctx.LogLine("No audit-log entries found.")
		return nil
	}
	ctx.OutputTable(TableOutput{
		Headers: []string{"TIME", "ACTOR", "EVENT", "SOURCE"},
		Rows:    rows,
	})
	if hitLimit {
		ctx.Logf("Reached the --limit of %d entries; more exist. Increase --limit or use --limit 0 for no limit.\n", limit)
	}
	return nil
}

// auditLogRow renders one audit-log entry as a text-table row.
func auditLogRow(e managementapi.AuditLogEntry) []string {
	source := ""
	if e.Source != nil {
		source = string(*e.Source)
	}
	return []string{
		e.Created.Local().Format("2006-01-02 15:04:05"),
		auditLogActor(e.Actor),
		string(e.EventType),
		source,
	}
}

// auditLogActor renders the acting party: the user email when present, else the
// API-key prefix, else the raw actor type.
func auditLogActor(a managementapi.AuditLogActor) string {
	if a.Email != nil && *a.Email != "" {
		return *a.Email
	}
	if a.ApiKeyPrefix != nil && *a.ApiKeyPrefix != "" {
		return *a.ApiKeyPrefix + "****"
	}
	return string(a.Type)
}

// auditEnumValues validates lowercase-kebab CLI values against an allow-list
// and maps them to the ALL_CAPS backend enum form (uppercasing and swapping
// '-' for '_'). flagName names the flag for error messages.
func auditEnumValues[T ~string](flagName string, values, allowed []string) ([]T, error) {
	out := make([]T, 0, len(values))
	for _, v := range values {
		if !slices.Contains(allowed, v) {
			return nil, cmd.NewErrUsagef("invalid --%s %q; allowed values: %s", flagName, v, strings.Join(allowed, ", "))
		}
		out = append(out, T(strings.ToUpper(strings.ReplaceAll(v, "-", "_"))))
	}
	return out, nil
}
