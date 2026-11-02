package cmd

import (
	"cmp"
	"fmt"
	"maps"
	"math"
	"math/big"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-go/client/managementapi"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
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
			return &gridRouteUsageChart{dims: dims, series: map[string]*gridUsageSeries{}, all: newGridUsageTotal()}
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
		ctx.LogLine("Harness \"Unknown\" means no harness was recorded, including usage from before harness tracking.")
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
		return "Unknown"
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

// gridRouteUsageChart renders the window's spend per --group-by value: the
// total, a chart of spend per day when stdout is a terminal, and each value's
// spend and share, most expensive first, with all but the top few folded into
// "other".
type gridRouteUsageChart struct {
	dims []managementapi.GridRouteUsageDimension
	// first is the first bucket's day, and days the number of days from it
	// through the last bucket's.
	first          time.Time
	days           int
	series         map[string]*gridUsageSeries
	all            *gridUsageTotal
	missingHarness bool
}

// gridUsageSeries is one --group-by value's usage over the window.
type gridUsageSeries struct {
	name  string
	total *gridUsageTotal
	// daily is the cost by day of the window, for the chart. Whole dollars
	// don't need the API's exact decimals.
	daily []float64
}

func (c *gridRouteUsageChart) addBucket(b managementapi.GridRouteUsageBucket) error {
	date, err := time.Parse(time.DateOnly, b.Date)
	if err != nil {
		return fmt.Errorf("routes usage returned an invalid date %q", b.Date)
	}
	if c.days == 0 {
		c.first = date
	}
	day := int(date.Sub(c.first).Hours() / 24)
	c.days = max(c.days, day+1)
	for _, r := range b.Results {
		t, err := gridUsageResultTotal(r)
		if err != nil {
			return err
		}
		cells := make([]string, len(c.dims))
		for i, d := range c.dims {
			cells[i] = gridUsageDimensionCell(d, r)
		}
		name := strings.Join(cells, " · ")
		s := c.series[name]
		if s == nil {
			s = &gridUsageSeries{name: name, total: newGridUsageTotal()}
			c.series[name] = s
		}
		s.total.add(t)
		c.all.add(t)
		for len(s.daily) <= day {
			s.daily = append(s.daily, 0)
		}
		cost, _ := t.cost.Float64()
		s.daily[day] += cost
		if slices.Contains(c.dims, managementapi.GridRouteUsageDimension_HARNESS) && r.Harness == nil {
			c.missingHarness = true
		}
	}
	return nil
}

func (c *gridRouteUsageChart) empty() bool     { return len(c.series) == 0 }
func (c *gridRouteUsageChart) unpriced() bool  { return !c.all.priced }
func (c *gridRouteUsageChart) noHarness() bool { return c.missingHarness }

func (c *gridRouteUsageChart) render(ctx *CommandContext) {
	series := slices.SortedFunc(maps.Values(c.series), func(x, y *gridUsageSeries) int {
		return cmp.Or(y.total.cost.Cmp(x.total.cost), strings.Compare(x.name, y.name))
	})
	for _, s := range series {
		s.daily = append(s.daily, make([]float64, c.days-len(s.daily))...)
	}
	series = gridUsageFoldSeries(series, c.days)

	// One label and one amount column shared by the total and every series.
	labelWidth, amountWidth := len("Total")+2, len(c.all.money())
	for _, s := range series {
		labelWidth = max(labelWidth, 4+ansi.StringWidth(s.name)+2)
		amountWidth = max(amountWidth, len(s.total.money()))
	}
	// The writer drops the styling stdout can't show, so there is no need to
	// query the terminal for it.
	out := colorprofile.NewWriter(ctx.Stdout, os.Environ())
	write := func(line string) { panicOnOutputError(fmt.Fprintln(out, line)) }
	bold, dim := ansi.Style{}.Bold(), ansi.Style{}.Faint()
	paint := func(s string, i int) string {
		return ansi.Style{}.ForegroundColor(gridUsageChartColors[i%len(gridUsageChartColors)]).Styled(s)
	}
	pad := func(s string, w int) string { return s + strings.Repeat(" ", w-ansi.StringWidth(s)) }
	right := func(s string, w int) string { return strings.Repeat(" ", w-ansi.StringWidth(s)) + s }

	write(pad(bold.Styled("Total"), labelWidth) + right(c.all.money(), amountWidth))
	charted := false
	// A single day has no trend to chart.
	if f, ok := ctx.Stdout.(*os.File); ok && term.IsTerminal(f.Fd()) && c.days > 1 {
		w, _, _ := term.GetSize(f.Fd())
		daily := make([][]float64, len(series))
		for i, s := range series {
			daily[i] = s.daily
		}
		if chart := renderGridUsageChart(c.first, daily, cmp.Or(w, 80)-2, paint); chart != "" {
			write("\n  " + strings.ReplaceAll(strings.TrimSuffix(chart, "\n"), "\n", "\n  ") + "\n")
			charted = true
		}
	}
	all, _ := c.all.cost.Float64()
	for i, s := range series {
		// The colored dot is the chart's legend, so it only shows with the
		// chart; scripts and greps see just the name.
		label := "  " + s.name
		if charted {
			label = "  " + paint("●", i) + " " + s.name
		}
		line := pad(label, labelWidth) + right(s.total.money(), amountWidth)
		if c.all.priced && all > 0 {
			cost, _ := s.total.cost.Float64()
			line += " " + dim.Styled(right("("+strconv.Itoa(int(cost/all*100+0.5))+"%)", 6))
		}
		write(line)
	}
}

// gridUsageTopSeries is how many --group-by values the chart and list show on
// their own; the rest are summed into one "other" row so neither grows past a
// glance.
const gridUsageTopSeries = 5

// gridUsageFoldSeries folds every series after the top gridUsageTopSeries,
// which are sorted most expensive first, into one "other" series with their
// summed usage over the window's days.
func gridUsageFoldSeries(series []*gridUsageSeries, days int) []*gridUsageSeries {
	if len(series) <= gridUsageTopSeries {
		return series
	}
	other := &gridUsageSeries{name: "other", total: newGridUsageTotal(), daily: make([]float64, days)}
	for _, s := range series[gridUsageTopSeries:] {
		other.total.add(s.total)
		for d, v := range s.daily {
			other.daily[d] += v
		}
	}
	return append(series[:gridUsageTopSeries:gridUsageTopSeries], other)
}

// gridUsageDollars renders an amount to the nearest dollar, like $1,234, keeping
// anything under half a dollar visible as <$1.
func gridUsageDollars(f float64) string {
	if f > 0 && f < 0.5 {
		return "<$1"
	}
	return "$" + billingGroupDigits(strconv.FormatFloat(math.Round(f), 'f', 0, 64))
}

// gridUsageChartHeight is the most rows above the chart's zero row. There is at
// most one per dollar, so the whole-dollar labels never repeat.
const gridUsageChartHeight = 7

// gridUsageChartWidth is the columns the plot aims for, so it isn't spread across
// a wide terminal; a full month at two columns a day runs a little past it.
const gridUsageChartWidth = 48

// gridUsageChartColors are the colors of the series, in order.
var gridUsageChartColors = []ansi.BasicColor{ansi.Blue, ansi.Green, ansi.Magenta, ansi.Cyan, ansi.Yellow, ansi.Red}

// renderGridUsageChart renders series, each with one dollar amount per day from
// first, as an asciichart-style step chart with a y-axis in whole dollars and
// date labels below, fitting width columns, or "" when two columns per day
// don't fit. Each day is a run of equal width with its date centered under
// it, so the last day's run ends with its date, which can stick out two
// columns past the plot. paint colors a series' characters by its index. It is
// hand-rolled rather than asciigraph's, which interpolates between points
// instead of stepping, spaces its date ticks evenly rather than at day
// starts, and paints with its own escapes rather than the terminal palette;
// the glue to work around that is most of this code anyway.
func renderGridUsageChart(first time.Time, series [][]float64, width int, paint func(s string, i int) string) string {
	days := len(series[0])
	top := 1.0 // At least a dollar, so the top label isn't $0.
	for _, s := range series {
		for _, v := range s {
			top = max(top, v)
		}
	}
	height := min(gridUsageChartHeight, int(top))
	labels := make([]string, height+1)
	labelWidth := 0
	for r := range labels {
		labels[r] = gridUsageDollars(top * float64(r) / float64(height))
		labelWidth = max(labelWidth, len(labels[r]))
	}
	// Each day spans step columns, its first one stepping from the day before.
	step := max(2, min(width-labelWidth-4, gridUsageChartWidth)/days)
	cols := days * step
	if width < labelWidth+4+cols {
		return ""
	}
	grid := make([][]string, height+1) // grid[0] is the zero row.
	for r := range grid {
		grid[r] = slices.Repeat([]string{" "}, cols)
	}
	// Paint the last series first so the first, the largest, ends up on top.
	for i, s := range slices.Backward(series) {
		prev := -1
		for d, v := range s {
			y := int(math.Round(v / top * float64(height)))
			if v > 0 {
				y = max(y, 1) // Keep any spend off the zero line.
			}
			for c := d * step; c < (d+1)*step; c++ {
				grid[y][c] = paint("─", i)
			}
			if c := d * step; prev >= 0 && prev != y {
				for r := min(prev, y) + 1; r < max(prev, y); r++ {
					grid[r][c] = paint("│", i)
				}
				if y > prev {
					grid[prev][c], grid[y][c] = paint("╯", i), paint("╭", i)
				} else {
					grid[prev][c], grid[y][c] = paint("╮", i), paint("╰", i)
				}
			}
			prev = y
		}
	}

	var b strings.Builder
	for r := height; r >= 0; r-- {
		tick := " ┤"
		if r == 0 {
			tick = " ┼"
		}
		b.WriteString(strings.Repeat(" ", labelWidth-len(labels[r])) + labels[r] + tick)
		b.WriteString(strings.TrimRight(strings.Join(grid[r], ""), " ") + "\n")
	}
	// Center the date labels, at most 6 wide, under days at least 8 columns
	// apart, counting back from the last day so it always has one.
	axis := []byte(strings.Repeat(" ", labelWidth+4+cols))
	every := (8 + step - 1) / step
	for d := (days - 1) % every; d < days; d += every {
		label := first.AddDate(0, 0, d).Format("Jan 2")
		copy(axis[labelWidth+2+max(0, d*step+(step-len(label))/2):], label)
	}
	b.WriteString(strings.TrimRight(string(axis), " ") + "\n")
	return b.String()
}
