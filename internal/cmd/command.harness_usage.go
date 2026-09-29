package cmd

import (
	"cmp"
	"fmt"
	"math/big"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-go/client/managementapi"
)

func init() {
	Register("harness usage", commandHarnessUsage)
}

// harnessUsageDimensions are the allowed --group-by values, mapped to the
// backend enum by uppercasing.
var harnessUsageDimensions = []string{"user", "model", "provider"}

// harnessUsageProviders are the allowed --provider values, mapped to the
// backend enum by uppercasing and swapping '-' for '_'.
var harnessUsageProviders = []string{"baseten-model-api", "openai", "anthropic", "xai", "vertex", "openai-compatible"}

// harnessUsageMaxBuckets is the most daily buckets the endpoint returns per
// page.
const harnessUsageMaxBuckets = 31

// harnessUsageRetentionDays is how many days of usage, including today, the
// endpoint keeps.
const harnessUsageRetentionDays = 92

func commandHarnessUsage(ctx *CommandContext, f *cmd.HarnessUsageFlags) error {
	if f.Limit < 0 {
		return cmd.NewErrUsagef("--limit must be zero (no limit) or a positive number")
	}
	pageSize := cmp.Or(f.PageSize, harnessUsageMaxBuckets)
	if pageSize < 1 || pageSize > harnessUsageMaxBuckets {
		return cmd.NewErrUsagef("--page-size must be between 1 and %d", harnessUsageMaxBuckets)
	}
	dims, err := harnessUsageGroupBy(f.GroupBy)
	if err != nil {
		return err
	}
	providers, err := harnessUsageProviderFilter(f.Providers)
	if err != nil {
		return err
	}
	startDate, endDate, err := harnessUsageWindow(ctx, f)
	if err != nil {
		return err
	}

	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	api := cl.API()
	// Default to the caller's own usage: admins would otherwise see the whole
	// organization's, and spend limits are per user.
	userIDs := f.UserIDs
	if len(userIDs) == 0 {
		me, err := api.GetUsersMe(ctx)
		if err != nil {
			return fmt.Errorf("getting current user: %w", err)
		}
		userIDs = []string{me.UserId}
	}
	params := managementapi.GetV1RoutesUsageParams{
		StartDate: &startDate,
		EndDate:   &endDate,
		GroupBy:   &dims,
		UserIds:   &userIDs,
	}
	if len(f.Models) > 0 {
		params.Models = &f.Models
	}
	if len(providers) > 0 {
		params.Providers = &providers
	}

	var jw *JSONArrayWriter
	if ctx.JSON {
		jw = ctx.NewJSONArrayWriter()
		defer jw.Close()
	}
	totals := newHarnessUsageTotals(dims)
	remaining := f.Limit
	hitLimit := false
	var lastDate string
buckets:
	for {
		params.Limit = &pageSize
		resp, err := api.GetRoutesUsage(ctx, params)
		if err != nil {
			return fmt.Errorf("getting routes usage: %w", err)
		}
		for i := range resp.Items {
			if ctx.JSON {
				jw.Write(resp.Items[i])
			} else if err := totals.addBucket(resp.Items[i]); err != nil {
				return err
			}
			lastDate = resp.Items[i].Date
			if remaining > 0 {
				remaining--
				if remaining == 0 {
					// More buckets exist if this page reported another page or
					// still had unemitted buckets of its own.
					hitLimit = resp.Pagination.HasMore || i < len(resp.Items)-1
					break buckets
				}
			}
		}
		if !resp.Pagination.HasMore || resp.Pagination.Cursor == nil {
			break
		}
		params.Cursor = resp.Pagination.Cursor
	}

	if ctx.JSON {
		return nil
	}
	rows := totals.rows()
	if len(rows) == 0 {
		ctx.LogLine("No usage in the selected window.")
		// A team or workspace key authenticates as a service account, which
		// never creates routes keys, so its own usage is always empty.
		if len(f.UserIDs) == 0 {
			if session, err := ctx.authInfo.Session(); err == nil && session.UsesAPIKey() {
				ctx.LogLine("If you're using a team or workspace API key, run 'baseten auth login' to see your own usage.")
			}
		}
		return nil
	}
	names := make([]string, len(dims))
	for i, d := range dims {
		names[i] = strings.ToLower(string(d))
	}
	ctx.LogLine(fmt.Sprintf("Window: %s through %s UTC · grouped by %s · can lag by up to 15 minutes",
		startDate, lastDate, strings.Join(names, ", ")))
	if slices.ContainsFunc(rows, func(r *harnessUsageRow) bool { return !r.priced }) {
		ctx.LogLine("A cost of \"-\" means some of that usage couldn't be priced.")
	}
	renderHarnessUsage(ctx, dims, rows, totals.all)
	if hitLimit {
		ctx.Logf("Reached the --limit of %d buckets; more exist. Increase --limit or use --limit 0 for no limit.\n", f.Limit)
	}
	return nil
}

// harnessUsageGroupBy validates the --group-by values and maps them onto the
// backend dimension enum, defaulting to model.
func harnessUsageGroupBy(values []string) ([]managementapi.RouteUsageDimension, error) {
	if len(values) == 0 {
		return []managementapi.RouteUsageDimension{managementapi.RouteUsageDimension_MODEL}, nil
	}
	dims := make([]managementapi.RouteUsageDimension, 0, len(values))
	for _, v := range values {
		if !slices.Contains(harnessUsageDimensions, v) {
			return nil, cmd.NewErrUsagef("invalid --group-by %q; must be one of: %s", v, strings.Join(harnessUsageDimensions, ", "))
		}
		dim := managementapi.RouteUsageDimension(strings.ToUpper(v))
		if !slices.Contains(dims, dim) {
			dims = append(dims, dim)
		}
	}
	return dims, nil
}

// harnessUsageProviderFilter validates the --provider values and maps them
// onto the backend provider enum.
func harnessUsageProviderFilter(values []string) ([]managementapi.RouteProvider, error) {
	providers := make([]managementapi.RouteProvider, 0, len(values))
	for _, v := range values {
		if !slices.Contains(harnessUsageProviders, v) {
			return nil, cmd.NewErrUsagef("invalid --provider %q; must be one of: %s", v, strings.Join(harnessUsageProviders, ", "))
		}
		providers = append(providers, managementapi.RouteProvider(strings.ToUpper(strings.ReplaceAll(v, "-", "_"))))
	}
	return providers, nil
}

// harnessUsageWindow resolves the query's UTC start and end dates, month to
// date by default, widened to whole days and clamped to retention.
func harnessUsageWindow(ctx *CommandContext, f *cmd.HarnessUsageFlags) (startDate, endDate string, err error) {
	start, end, err := usageWindow(ctx, f.Start, f.End, f.Since, func(end time.Time) time.Time {
		end = end.UTC()
		return time.Date(end.Year(), end.Month(), 1, 0, 0, 0, 0, time.UTC)
	})
	if err != nil {
		return "", "", err
	}
	startDay := start.UTC().Truncate(24 * time.Hour)
	endDay := end.UTC().Truncate(24 * time.Hour)
	if endDay.Before(end) {
		endDay = endDay.AddDate(0, 0, 1)
	}
	today := ctx.Now().UTC().Truncate(24 * time.Hour)
	if retained := today.AddDate(0, 0, 1-harnessUsageRetentionDays); startDay.Before(retained) {
		if !endDay.After(retained) {
			return "", "", cmd.NewErrUsagef("the window is older than the %d days of usage Baseten keeps", harnessUsageRetentionDays)
		}
		ctx.Logf("Usage is kept for %d days, so this only includes usage from %s on.\n",
			harnessUsageRetentionDays, retained.Format(time.DateOnly))
		startDay = retained
	}
	return startDay.Format(time.DateOnly), endDay.Format(time.DateOnly), nil
}

// harnessUsageRow is one --group-by combination's usage, summed across days.
type harnessUsageRow struct {
	key                   []string
	input, cached, output int64
	cost                  *big.Rat
	// priced is false once any of the usage came back without a cost.
	priced bool
}

func newHarnessUsageRow(key []string) *harnessUsageRow {
	return &harnessUsageRow{key: key, cost: new(big.Rat), priced: true}
}

func (r *harnessUsageRow) add(o *harnessUsageRow) {
	r.input += o.input
	r.cached += o.cached
	r.output += o.output
	r.cost.Add(r.cost, o.cost)
	r.priced = r.priced && o.priced
}

// harnessUsageTotals sums daily buckets per --group-by combination, adding
// costs as exact decimals.
type harnessUsageTotals struct {
	dims  []managementapi.RouteUsageDimension
	byKey map[string]*harnessUsageRow
	all   *harnessUsageRow
}

func newHarnessUsageTotals(dims []managementapi.RouteUsageDimension) *harnessUsageTotals {
	return &harnessUsageTotals{dims: dims, byKey: map[string]*harnessUsageRow{}, all: newHarnessUsageRow(nil)}
}

func (t *harnessUsageTotals) addBucket(b managementapi.RoutesUsageBucket) error {
	for _, r := range b.Results {
		key := make([]string, len(t.dims))
		for i, d := range t.dims {
			key[i] = harnessUsageDimensionCell(d, r)
		}
		usage := newHarnessUsageRow(key)
		usage.input = int64(r.InputTokens)
		usage.cached = int64(r.CachedInputTokens)
		usage.output = int64(r.OutputTokens)
		// Older versions of the endpoint return a null cost for usage they
		// can't price, which decodes as empty.
		if r.CostUsd == "" {
			usage.priced = false
		} else if _, ok := usage.cost.SetString(r.CostUsd); !ok {
			return fmt.Errorf("routes usage returned an invalid cost %q", r.CostUsd)
		}
		id := strings.Join(key, "\x00")
		row, ok := t.byKey[id]
		if !ok {
			row = newHarnessUsageRow(key)
			t.byKey[id] = row
		}
		row.add(usage)
	}
	return nil
}

// rows returns every combination with usage, most expensive first. Requests
// that produced no tokens, such as failed ones, cost nothing and would only
// add empty rows.
func (t *harnessUsageTotals) rows() []*harnessUsageRow {
	var rows []*harnessUsageRow
	for _, r := range t.byKey {
		if r.cost.Sign() == 0 && r.input+r.output == 0 {
			continue
		}
		rows = append(rows, r)
		t.all.add(r)
	}
	slices.SortFunc(rows, func(x, y *harnessUsageRow) int {
		if c := y.cost.Cmp(x.cost); c != 0 {
			return c
		}
		return slices.Compare(x.key, y.key)
	})
	return rows
}

func harnessUsageDimensionCell(d managementapi.RouteUsageDimension, r managementapi.RoutesUsageResult) string {
	var v string
	switch d {
	case managementapi.RouteUsageDimension_USER:
		v = deref(r.UserId)
	case managementapi.RouteUsageDimension_MODEL:
		v = deref(r.Model)
	case managementapi.RouteUsageDimension_PROVIDER:
		if r.Provider != nil {
			v = strings.ToLower(strings.ReplaceAll(string(*r.Provider), "_", "-"))
		}
	}
	return cmp.Or(v, "(unknown)")
}

// harnessMoney renders a cost in dollars and cents, keeping tiny nonzero costs
// visible, or "-" when some of it couldn't be priced.
func harnessMoney(r *harnessUsageRow) string {
	if !r.priced {
		return "-"
	}
	f, _ := r.cost.Float64()
	if f > 0 && f < 0.005 {
		return "<$0.01"
	}
	return billingFormatMoney(f)
}

func renderHarnessUsage(ctx *CommandContext, dims []managementapi.RouteUsageDimension, rows []*harnessUsageRow, all *harnessUsageRow) {
	headers := make([]string, 0, len(dims)+4)
	for _, d := range dims {
		headers = append(headers, string(d))
	}
	headers = append(headers, "INPUT", "CACHED", "OUTPUT", "COST")
	var rightAligned []int
	for col := len(dims); col < len(headers); col++ {
		rightAligned = append(rightAligned, col)
	}
	cells := func(key []string, r *harnessUsageRow) []string {
		return append(key,
			billingGroupDigits(strconv.FormatInt(r.input, 10)),
			billingGroupDigits(strconv.FormatInt(r.cached, 10)),
			billingGroupDigits(strconv.FormatInt(r.output, 10)),
			harnessMoney(r),
		)
	}
	table := make([][]string, 0, len(rows)+1)
	for _, r := range rows {
		table = append(table, cells(slices.Clone(r.key), r))
	}
	// A totals row only earns its keep once there is more than one row to
	// total; with a single row it would just repeat it.
	if len(rows) > 1 {
		key := make([]string, len(dims))
		key[0] = "ALL"
		table = append(table, cells(key, all))
	}
	ctx.OutputTable(TableOutput{Headers: headers, Rows: table, RightAlignedColumns: rightAligned})
}
