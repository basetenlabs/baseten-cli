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
	Register("grid route usage", commandGridRouteUsage)
}

// gridUsageMaxBuckets is the most daily buckets the endpoint returns per
// page.
const gridUsageMaxBuckets = 31

// gridUsageRetentionDays is how many days of usage, including today, the
// endpoint keeps.
const gridUsageRetentionDays = 92

func commandGridRouteUsage(ctx *CommandContext, f *cmd.GridRouteUsageFlags) error {
	return runGridRouteUsage(ctx, gridUsageRequest{
		team:    f.Team,
		start:   f.Start,
		end:     f.End,
		since:   f.Since,
		userIDs: f.UserIDs,
		query:   f.GridRouteUsageQueryFlags,
		// Like the API: the previous UTC day through today.
		defaultStart: func(end time.Time) time.Time { return end.AddDate(0, 0, -1) },
		newTable: func(dims []managementapi.GridRouteUsageDimension) gridUsageTable {
			return &gridRouteUsageSeries{dims: dims, all: newGridUsageTotal()}
		},
	})
}

// gridUsageRequest is one run of a routes usage command.
type gridUsageRequest struct {
	team       string
	start, end time.Time
	since      time.Duration
	userIDs    []string
	query      cmd.GridRouteUsageQueryFlags
	// defaultStart is the window start when --start and --since are omitted.
	defaultStart func(end time.Time) time.Time
	newTable     func(dims []managementapi.GridRouteUsageDimension) gridUsageTable
}

// gridUsageTable renders the text output from daily buckets.
type gridUsageTable interface {
	addBucket(managementapi.GridRouteUsageBucket) error
	// empty reports whether no bucket had usage.
	empty() bool
	// unpriced reports whether any usage came back without a cost.
	unpriced() bool
	// noHarness reports whether any harness grouping had no recorded harness.
	noHarness() bool
	render(ctx *CommandContext)
}

func runGridRouteUsage(ctx *CommandContext, req gridUsageRequest) error {
	q := req.query
	if q.Limit < 0 {
		return cmd.NewErrUsagef("--limit must be zero (no limit) or a positive number")
	}
	pageSize := cmp.Or(q.PageSize, gridUsageMaxBuckets)
	if pageSize < 1 || pageSize > gridUsageMaxBuckets {
		return cmd.NewErrUsagef("--page-size must be between 1 and %d", gridUsageMaxBuckets)
	}
	dims := gridUsageGroupBy(q.GroupBy)
	startDate, endDate, err := gridUsageWindow(ctx, req)
	if err != nil {
		return err
	}

	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	api := cl.API()
	team, err := gridTeamID(ctx, api, req.team)
	if err != nil {
		return err
	}
	params := managementapi.GetV1GridRoutesUsageParams{
		TeamId:    team,
		StartDate: &startDate,
		EndDate:   &endDate,
		GroupBy:   &dims,
	}
	if len(req.userIDs) > 0 {
		params.UserIds = &req.userIDs
	}
	if len(q.Models) > 0 {
		params.Models = &q.Models
	}
	if providers := gridUsageProviders(q.Providers); len(providers) > 0 {
		params.Providers = &providers
	}
	if harnesses := gridUsageHarnesses(q.Harnesses); len(harnesses) > 0 {
		params.Harnesses = &harnesses
	}

	var jw *JSONArrayWriter
	if ctx.JSON {
		jw = ctx.NewJSONArrayWriter()
		defer jw.Close()
	}
	table := req.newTable(dims)
	remaining := q.Limit
	hitLimit := false
	var firstDate, lastDate string
buckets:
	for {
		params.Limit = &pageSize
		resp, err := api.GetGridRoutesUsage(ctx, params)
		if err != nil {
			return fmt.Errorf("getting routes usage: %w", err)
		}
		for i := range resp.Items {
			if ctx.JSON {
				jw.Write(resp.Items[i])
			} else if err := table.addBucket(resp.Items[i]); err != nil {
				return err
			}
			firstDate = cmp.Or(firstDate, resp.Items[i].Date)
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
	if table.empty() {
		ctx.LogLine("No usage in the selected window.")
		return nil
	}
	names := make([]string, len(dims))
	for i, d := range dims {
		names[i] = strings.ToLower(string(d))
	}
	ctx.LogLine(fmt.Sprintf("Window: %s through %s UTC · grouped by %s · can lag by up to 15 minutes",
		firstDate, lastDate, strings.Join(names, ", ")))
	if table.noHarness() {
		ctx.LogLine("Harness \"(none)\" means no harness was recorded, including usage from before harness tracking.")
	}
	if table.unpriced() {
		ctx.LogLine("A cost of \"-\" means some of that usage couldn't be priced.")
	}
	table.render(ctx)
	if hitLimit {
		ctx.Logf("Reached the --limit of %d buckets; more exist. Increase --limit or use --limit 0 for no limit.\n", q.Limit)
	}
	return nil
}

// gridUsageGroupBy maps the --group-by values, already validated by the
// flag's enum, onto the backend dimension enum, dropping repeats.
func gridUsageGroupBy(values []string) []managementapi.GridRouteUsageDimension {
	dims := make([]managementapi.GridRouteUsageDimension, 0, len(values))
	for _, v := range values {
		dim := managementapi.GridRouteUsageDimension(strings.ToUpper(v))
		if !slices.Contains(dims, dim) {
			dims = append(dims, dim)
		}
	}
	return dims
}

// gridUsageProviders maps the --provider values, already validated by the
// flag's enum, onto the backend provider enum.
func gridUsageProviders(values []string) []managementapi.GridProvider {
	providers := make([]managementapi.GridProvider, 0, len(values))
	for _, v := range values {
		providers = append(providers, managementapi.GridProvider(strings.ToUpper(strings.ReplaceAll(v, "-", "_"))))
	}
	return providers
}

// gridUsageHarnesses maps the --harness values, already validated by the
// flag's enum, onto the backend harness enum.
func gridUsageHarnesses(values []string) []managementapi.GridRouteUsageHarness {
	harnesses := make([]managementapi.GridRouteUsageHarness, 0, len(values))
	for _, v := range values {
		harnesses = append(harnesses, managementapi.GridRouteUsageHarness(strings.ToUpper(strings.ReplaceAll(v, "-", "_"))))
	}
	return harnesses
}

// gridUsageWindow resolves the query's UTC start and end dates, widened to
// whole days and clamped to retention.
func gridUsageWindow(ctx *CommandContext, req gridUsageRequest) (startDate, endDate string, err error) {
	start, end, err := usageWindow(ctx, req.start, req.end, req.since, req.defaultStart)
	if err != nil {
		return "", "", err
	}
	startDay := start.UTC().Truncate(24 * time.Hour)
	endDay := end.UTC().Truncate(24 * time.Hour)
	if endDay.Before(end) {
		endDay = endDay.AddDate(0, 0, 1)
	}
	today := ctx.Now().UTC().Truncate(24 * time.Hour)
	if retained := today.AddDate(0, 0, 1-gridUsageRetentionDays); startDay.Before(retained) {
		if !endDay.After(retained) {
			return "", "", cmd.NewErrUsagef("the window is older than the %d days of usage Baseten keeps", gridUsageRetentionDays)
		}
		ctx.Logf("Usage is kept for %d days, so this only includes usage from %s on.\n",
			gridUsageRetentionDays, retained.Format(time.DateOnly))
		startDay = retained
	}
	return startDay.Format(time.DateOnly), endDay.Format(time.DateOnly), nil
}

// gridUsageTotal sums usage, adding costs as exact decimals.
type gridUsageTotal struct {
	input, cached, output int64
	cost                  *big.Rat
	// priced is false once any of the usage came back without a cost.
	priced bool
}

func newGridUsageTotal() *gridUsageTotal {
	return &gridUsageTotal{cost: new(big.Rat), priced: true}
}

// gridUsageResultTotal converts one result, rejecting a cost that doesn't
// parse.
func gridUsageResultTotal(r managementapi.GridRouteUsageResult) (*gridUsageTotal, error) {
	t := newGridUsageTotal()
	t.input = int64(r.InputTokens)
	t.cached = int64(r.CachedInputTokens)
	t.output = int64(r.OutputTokens)
	// Older versions of the endpoint return a null cost for usage they can't
	// price, which decodes as empty.
	if r.CostUsd == "" {
		t.priced = false
	} else if _, ok := t.cost.SetString(r.CostUsd); !ok {
		return nil, fmt.Errorf("routes usage returned an invalid cost %q", r.CostUsd)
	}
	return t, nil
}

func (t *gridUsageTotal) add(o *gridUsageTotal) {
	t.input += o.input
	t.cached += o.cached
	t.output += o.output
	t.cost.Add(t.cost, o.cost)
	t.priced = t.priced && o.priced
}

// cells renders the token counts and cost columns.
func (t *gridUsageTotal) cells() []string {
	return []string{
		billingGroupDigits(strconv.FormatInt(t.input, 10)),
		billingGroupDigits(strconv.FormatInt(t.cached, 10)),
		billingGroupDigits(strconv.FormatInt(t.output, 10)),
		t.money(),
	}
}

// money renders the cost in dollars and cents, keeping tiny nonzero costs
// visible, or "-" when some of it couldn't be priced.
func (t *gridUsageTotal) money() string {
	if !t.priced {
		return "-"
	}
	f, _ := t.cost.Float64()
	if f > 0 && f < 0.005 {
		return "<$0.01"
	}
	return billingFormatMoney(f)
}

func gridUsageDimensionCell(d managementapi.GridRouteUsageDimension, r managementapi.GridRouteUsageResult) string {
	if d == managementapi.GridRouteUsageDimension_HARNESS && r.Harness == nil {
		return "(none)"
	}
	var v string
	switch d {
	case managementapi.GridRouteUsageDimension_USER:
		v = deref(r.UserId)
	case managementapi.GridRouteUsageDimension_TEAM:
		v = deref(r.TeamId)
	case managementapi.GridRouteUsageDimension_ROUTER:
		v = deref(r.RouterId)
	case managementapi.GridRouteUsageDimension_MODEL:
		v = deref(r.Model)
	case managementapi.GridRouteUsageDimension_PROVIDER:
		if r.Provider != nil {
			v = strings.ToLower(strings.ReplaceAll(string(*r.Provider), "_", "-"))
		}
	case managementapi.GridRouteUsageDimension_HARNESS:
		if r.Harness != nil {
			v = strings.ToLower(strings.ReplaceAll(string(*r.Harness), "_", "-"))
		}
	}
	return cmp.Or(v, "(unknown)")
}

// gridUsageHeaders are the table headers for the given leading columns and
// dimensions, with the numeric columns right-aligned.
func gridUsageHeaders(leading []string, dims []managementapi.GridRouteUsageDimension) (headers []string, rightAligned []int) {
	headers = slices.Clone(leading)
	for _, d := range dims {
		headers = append(headers, string(d))
	}
	headers = append(headers, "INPUT", "CACHED", "OUTPUT", "COST")
	for col := len(leading) + len(dims); col < len(headers); col++ {
		rightAligned = append(rightAligned, col)
	}
	return headers, rightAligned
}

// gridRouteUsageSeries renders one row per day and --group-by combination, like
// model-api usage, with a trailing ALL row.
type gridRouteUsageSeries struct {
	dims           []managementapi.GridRouteUsageDimension
	rows           [][]string
	dataRows       int
	all            *gridUsageTotal
	missingHarness bool
}

// addBucket appends this day's rows. A day with no usage still gets a row so
// gaps in the series stay visible.
func (s *gridRouteUsageSeries) addBucket(b managementapi.GridRouteUsageBucket) error {
	stamp := b.Date
	if len(b.Results) == 0 {
		row := make([]string, 1+len(s.dims)+4)
		row[0] = stamp
		row[1] = "(no usage)"
		s.rows = append(s.rows, row)
		return nil
	}
	for _, r := range b.Results {
		t, err := gridUsageResultTotal(r)
		if err != nil {
			return err
		}
		row := []string{stamp}
		for _, d := range s.dims {
			row = append(row, gridUsageDimensionCell(d, r))
		}
		s.rows = append(s.rows, append(row, t.cells()...))
		s.dataRows++
		s.all.add(t)
		if slices.Contains(s.dims, managementapi.GridRouteUsageDimension_HARNESS) && r.Harness == nil {
			s.missingHarness = true
		}
		// Blank the repeated date so a day's several rows read as one group.
		stamp = ""
	}
	return nil
}

func (s *gridRouteUsageSeries) empty() bool     { return s.dataRows == 0 }
func (s *gridRouteUsageSeries) unpriced() bool  { return !s.all.priced }
func (s *gridRouteUsageSeries) noHarness() bool { return s.missingHarness }

func (s *gridRouteUsageSeries) render(ctx *CommandContext) {
	headers, rightAligned := gridUsageHeaders([]string{"DATE"}, s.dims)
	rows := s.rows
	// A totals row only earns its keep once there is more than one data row to
	// total; with a single row it would just repeat it.
	if s.dataRows > 1 {
		rows = append(rows, append(append([]string{"ALL"}, make([]string, len(s.dims))...), s.all.cells()...))
	}
	ctx.OutputTable(TableOutput{Headers: headers, Rows: rows, RightAlignedColumns: rightAligned})
}
