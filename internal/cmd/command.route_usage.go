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
	Register("route usage", commandRouteUsage)
}

// routesUsageMaxBuckets is the most daily buckets the endpoint returns per
// page.
const routesUsageMaxBuckets = 31

// routesUsageMe is the --user-id value that stands for the caller.
const routesUsageMe = "me"

// routesUsageRetentionDays is how many days of usage, including today, the
// endpoint keeps.
const routesUsageRetentionDays = 92

func commandRouteUsage(ctx *CommandContext, f *cmd.RouteUsageFlags) error {
	return runRoutesUsage(ctx, routesUsageRequest{
		start:   f.Start,
		end:     f.End,
		since:   f.Since,
		userIDs: f.UserIDs,
		query:   f.RouteUsageQueryFlags,
		// Like the API: the previous UTC day through today.
		defaultStart: func(end time.Time) time.Time { return end.AddDate(0, 0, -1) },
		newTable: func(dims []managementapi.RouteUsageDimension) routesUsageTable {
			return &routeUsageSeries{dims: dims, all: newRoutesUsageTotal()}
		},
	})
}

// routesUsageRequest is one run of a routes usage command.
type routesUsageRequest struct {
	start, end time.Time
	since      time.Duration
	userIDs    []string
	query      cmd.RouteUsageQueryFlags
	// defaultStart is the window start when --start and --since are omitted.
	defaultStart func(end time.Time) time.Time
	// defaultToCaller narrows usage to the caller when --user-id is omitted.
	defaultToCaller bool
	newTable        func(dims []managementapi.RouteUsageDimension) routesUsageTable
}

// routesUsageTable renders the text output from daily buckets.
type routesUsageTable interface {
	addBucket(managementapi.RoutesUsageBucket) error
	// empty reports whether no bucket had usage.
	empty() bool
	// unpriced reports whether any usage came back without a cost.
	unpriced() bool
	render(ctx *CommandContext)
}

func runRoutesUsage(ctx *CommandContext, req routesUsageRequest) error {
	q := req.query
	if q.Limit < 0 {
		return cmd.NewErrUsagef("--limit must be zero (no limit) or a positive number")
	}
	pageSize := cmp.Or(q.PageSize, routesUsageMaxBuckets)
	if pageSize < 1 || pageSize > routesUsageMaxBuckets {
		return cmd.NewErrUsagef("--page-size must be between 1 and %d", routesUsageMaxBuckets)
	}
	dims := routesUsageGroupBy(q.GroupBy)
	startDate, endDate, err := routesUsageWindow(ctx, req)
	if err != nil {
		return err
	}

	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	api := cl.API()
	userIDs := slices.Clone(req.userIDs)
	if len(userIDs) == 0 && req.defaultToCaller {
		userIDs = []string{routesUsageMe}
	}
	// "me" stands for the caller's own user ID.
	if slices.Contains(userIDs, routesUsageMe) {
		me, err := api.GetUsersMe(ctx)
		if err != nil {
			return fmt.Errorf("getting current user: %w", err)
		}
		for i := range userIDs {
			if userIDs[i] == routesUsageMe {
				userIDs[i] = me.UserId
			}
		}
	}
	params := managementapi.GetV1RoutesUsageParams{
		StartDate: &startDate,
		EndDate:   &endDate,
		GroupBy:   &dims,
	}
	if len(userIDs) > 0 {
		params.UserIds = &userIDs
	}
	if len(q.Models) > 0 {
		params.Models = &q.Models
	}
	if providers := routesUsageProviders(q.Providers); len(providers) > 0 {
		params.Providers = &providers
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
		resp, err := api.GetRoutesUsage(ctx, params)
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
		// A team or workspace key authenticates as a service account, which
		// never creates routes keys, so its own usage is always empty.
		if req.defaultToCaller && len(req.userIDs) == 0 {
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
		firstDate, lastDate, strings.Join(names, ", ")))
	if table.unpriced() {
		ctx.LogLine("A cost of \"-\" means some of that usage couldn't be priced.")
	}
	table.render(ctx)
	if hitLimit {
		ctx.Logf("Reached the --limit of %d buckets; more exist. Increase --limit or use --limit 0 for no limit.\n", q.Limit)
	}
	return nil
}

// routesUsageGroupBy maps the --group-by values, already validated by the
// flag's enum, onto the backend dimension enum, dropping repeats.
func routesUsageGroupBy(values []string) []managementapi.RouteUsageDimension {
	dims := make([]managementapi.RouteUsageDimension, 0, len(values))
	for _, v := range values {
		dim := managementapi.RouteUsageDimension(strings.ToUpper(v))
		if !slices.Contains(dims, dim) {
			dims = append(dims, dim)
		}
	}
	return dims
}

// routesUsageProviders maps the --provider values, already validated by the
// flag's enum, onto the backend provider enum.
func routesUsageProviders(values []string) []managementapi.RouteProvider {
	providers := make([]managementapi.RouteProvider, 0, len(values))
	for _, v := range values {
		providers = append(providers, managementapi.RouteProvider(strings.ToUpper(strings.ReplaceAll(v, "-", "_"))))
	}
	return providers
}

// routesUsageWindow resolves the query's UTC start and end dates, widened to
// whole days and clamped to retention.
func routesUsageWindow(ctx *CommandContext, req routesUsageRequest) (startDate, endDate string, err error) {
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
	if retained := today.AddDate(0, 0, 1-routesUsageRetentionDays); startDay.Before(retained) {
		if !endDay.After(retained) {
			return "", "", cmd.NewErrUsagef("the window is older than the %d days of usage Baseten keeps", routesUsageRetentionDays)
		}
		ctx.Logf("Usage is kept for %d days, so this only includes usage from %s on.\n",
			routesUsageRetentionDays, retained.Format(time.DateOnly))
		startDay = retained
	}
	return startDay.Format(time.DateOnly), endDay.Format(time.DateOnly), nil
}

// routesUsageTotal sums usage, adding costs as exact decimals.
type routesUsageTotal struct {
	input, cached, output int64
	cost                  *big.Rat
	// priced is false once any of the usage came back without a cost.
	priced bool
}

func newRoutesUsageTotal() *routesUsageTotal {
	return &routesUsageTotal{cost: new(big.Rat), priced: true}
}

// routesUsageResultTotal converts one result, rejecting a cost that doesn't
// parse.
func routesUsageResultTotal(r managementapi.RoutesUsageResult) (*routesUsageTotal, error) {
	t := newRoutesUsageTotal()
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

func (t *routesUsageTotal) add(o *routesUsageTotal) {
	t.input += o.input
	t.cached += o.cached
	t.output += o.output
	t.cost.Add(t.cost, o.cost)
	t.priced = t.priced && o.priced
}

// cells renders the token counts and cost columns.
func (t *routesUsageTotal) cells() []string {
	return []string{
		billingGroupDigits(strconv.FormatInt(t.input, 10)),
		billingGroupDigits(strconv.FormatInt(t.cached, 10)),
		billingGroupDigits(strconv.FormatInt(t.output, 10)),
		t.money(),
	}
}

// money renders the cost in dollars and cents, keeping tiny nonzero costs
// visible, or "-" when some of it couldn't be priced.
func (t *routesUsageTotal) money() string {
	if !t.priced {
		return "-"
	}
	f, _ := t.cost.Float64()
	if f > 0 && f < 0.005 {
		return "<$0.01"
	}
	return billingFormatMoney(f)
}

func routesUsageDimensionCell(d managementapi.RouteUsageDimension, r managementapi.RoutesUsageResult) string {
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

// routesUsageHeaders are the table headers for the given leading columns and
// dimensions, with the numeric columns right-aligned.
func routesUsageHeaders(leading []string, dims []managementapi.RouteUsageDimension) (headers []string, rightAligned []int) {
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

// routeUsageSeries renders one row per day and --group-by combination, like
// model-api usage, with a trailing ALL row.
type routeUsageSeries struct {
	dims     []managementapi.RouteUsageDimension
	rows     [][]string
	dataRows int
	all      *routesUsageTotal
}

// addBucket appends this day's rows. A day with no usage still gets a row so
// gaps in the series stay visible.
func (s *routeUsageSeries) addBucket(b managementapi.RoutesUsageBucket) error {
	stamp := b.Date
	if len(b.Results) == 0 {
		row := make([]string, 1+len(s.dims)+4)
		row[0] = stamp
		row[1] = "(no usage)"
		s.rows = append(s.rows, row)
		return nil
	}
	for _, r := range b.Results {
		t, err := routesUsageResultTotal(r)
		if err != nil {
			return err
		}
		row := []string{stamp}
		for _, d := range s.dims {
			row = append(row, routesUsageDimensionCell(d, r))
		}
		s.rows = append(s.rows, append(row, t.cells()...))
		s.dataRows++
		s.all.add(t)
		// Blank the repeated date so a day's several rows read as one group.
		stamp = ""
	}
	return nil
}

func (s *routeUsageSeries) empty() bool    { return s.dataRows == 0 }
func (s *routeUsageSeries) unpriced() bool { return !s.all.priced }

func (s *routeUsageSeries) render(ctx *CommandContext) {
	headers, rightAligned := routesUsageHeaders([]string{"DATE"}, s.dims)
	rows := s.rows
	// A totals row only earns its keep once there is more than one data row to
	// total; with a single row it would just repeat it.
	if s.dataRows > 1 {
		rows = append(rows, append(append([]string{"ALL"}, make([]string, len(s.dims))...), s.all.cells()...))
	}
	ctx.OutputTable(TableOutput{Headers: headers, Rows: rows, RightAlignedColumns: rightAligned})
}
