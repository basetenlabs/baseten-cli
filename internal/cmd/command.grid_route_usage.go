package cmd

import (
	"cmp"
	"fmt"
	"maps"
	"math"
	"math/big"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-go/client/managementapi"
	"github.com/charmbracelet/lipgloss"
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
	// Spend limits are per user and team, so this needs one team: the API
	// picks yours when you belong to one and rejects the call otherwise.
	if me, err := api.GetGridTeamSettingsUsersUserId(ctx, "me", managementapi.GetV1GridTeamSettingsUsersUserIdParams{TeamId: team}); err == nil {
		ctx.OutputLine("\n" + gridUsageLimitLine(ctx, me))
	} else {
		ctx.Logf("Your spend this month against your limit isn't shown: %v\n", err)
	}
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

// gridUsageLimitLine is your spend this month in the team against your limit
// there: yellow from 75% of the limit, red once blocked.
func gridUsageLimitLine(ctx *CommandContext, s *managementapi.GridTeamUserSettings) string {
	r := lipgloss.NewRenderer(ctx.Stdout)
	red := r.NewStyle().Foreground(lipgloss.Color("1")).Bold(true)
	spent, _ := strconv.ParseFloat(s.MonthSpendUsd, 64)
	limit, hasLimit := 0.0, s.SpendLimit.Effective.MonthlyLimitUsd != nil
	if hasLimit {
		limit, _ = strconv.ParseFloat(*s.SpendLimit.Effective.MonthlyLimitUsd, 64)
	}
	amount := billingFormatMoney(spent)
	switch {
	case s.IsBlocked:
		amount = red.Render(amount)
	case hasLimit && spent >= 0.75*limit:
		amount = r.NewStyle().Foreground(lipgloss.Color("3")).Render(amount)
	}
	line := "Your spend this month in " + s.TeamName + ": " + amount
	switch {
	case !hasLimit:
		line += r.NewStyle().Faint(true).Render(" · no limit")
	case limit > 0:
		line += fmt.Sprintf(" of %s (%.0f%%)", billingFormatMoney(limit), spent/limit*100)
	default:
		line += " of " + billingFormatMoney(limit)
	}
	if s.IsBlocked {
		what := " · limit reached"
		if s.BlockedBy != nil && *s.BlockedBy == managementapi.GridSpendBlockedBy_team {
			what = " · team limit reached"
		}
		line += red.Render(what)
	}
	return line
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
		v = cmp.Or(deref(r.TeamName), deref(r.TeamId))
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

// gridRouteUsageChart renders spend per --group-by value: the total, a chart
// of spend per day on a terminal, and one line per value, most expensive first.
type gridRouteUsageChart struct {
	dims           []managementapi.GridRouteUsageDimension
	first          time.Time // the first bucket's day
	days           int       // days from first through the last bucket's
	series         map[string]*gridUsageSeries
	all            *gridUsageTotal
	missingHarness bool
}

// gridUsageSeries is one --group-by value's usage over the window.
type gridUsageSeries struct {
	name  string
	total *gridUsageTotal
	daily map[int]float64 // cost by day index, for the chart
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
			s = &gridUsageSeries{name: name, total: newGridUsageTotal(), daily: map[int]float64{}}
			c.series[name] = s
		}
		s.total.add(t)
		c.all.add(t)
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

// gridUsageTopSeries is how many values get their own line; the rest are
// summed into "other".
const gridUsageTopSeries = 5

func (c *gridRouteUsageChart) render(ctx *CommandContext) {
	series := slices.SortedFunc(maps.Values(c.series), func(x, y *gridUsageSeries) int {
		return cmp.Or(y.total.cost.Cmp(x.total.cost), strings.Compare(x.name, y.name))
	})
	if len(series) > gridUsageTopSeries {
		other := &gridUsageSeries{name: "other", total: newGridUsageTotal(), daily: map[int]float64{}}
		for _, s := range series[gridUsageTopSeries:] {
			other.total.add(s.total)
			for d, v := range s.daily {
				other.daily[d] += v
			}
		}
		series = append(series[:gridUsageTopSeries], other)
	}
	daily := make([][]float64, len(series))
	nameWidth, amountWidth := 0, len(c.all.money())
	for i, s := range series {
		daily[i] = make([]float64, c.days)
		for d, v := range s.daily {
			daily[i][d] = v
		}
		nameWidth = max(nameWidth, utf8.RuneCountInString(s.name))
		amountWidth = max(amountWidth, len(s.total.money()))
	}

	r := lipgloss.NewRenderer(ctx.Stdout)
	paint := func(s string, i int) string {
		return r.NewStyle().Foreground(gridUsageChartColors[i%len(gridUsageChartColors)]).Render(s)
	}
	chart := ""
	// A single day has no trend to chart.
	if w, ok := ctx.TerminalWidth(); ok && c.days > 1 {
		chart = renderGridUsageChart(c.first, daily, w-2, paint)
	}
	indent := "  "
	if chart != "" {
		indent = "    " // room for the legend dot
	}
	ctx.OutputLine(r.NewStyle().Bold(true).Render(fmt.Sprintf("%-*s", len(indent)+nameWidth, "Total")) +
		fmt.Sprintf("  %*s", amountWidth, c.all.money()))
	if chart != "" {
		ctx.OutputLine("\n  " + strings.ReplaceAll(strings.TrimSuffix(chart, "\n"), "\n", "\n  ") + "\n")
	}
	total, _ := c.all.cost.Float64()
	for i, s := range series {
		line := indent
		if chart != "" {
			line = "  " + paint("●", i) + " "
		}
		line += fmt.Sprintf("%-*s  %*s", nameWidth, s.name, amountWidth, s.total.money())
		if c.all.priced && total > 0 {
			cost, _ := s.total.cost.Float64()
			line += r.NewStyle().Faint(true).Render(fmt.Sprintf(" %6s", fmt.Sprintf("(%.0f%%)", cost/total*100)))
		}
		ctx.OutputLine(line)
	}
}

const (
	gridUsageChartHeight = 7  // rows above zero; at most one per dollar so labels never repeat
	gridUsageChartWidth  = 48 // plot columns to aim for, so a wide terminal doesn't stretch it
)

// gridUsageChartColors are the series colors, in order: blue, green, magenta,
// cyan, yellow, red.
var gridUsageChartColors = []lipgloss.Color{"4", "2", "5", "6", "3", "1"}

// gridUsageDollars renders f to the nearest dollar, like $1,234, or <$1.
func gridUsageDollars(f float64) string {
	if f > 0 && f < 0.5 {
		return "<$1"
	}
	return "$" + billingGroupDigits(strconv.FormatFloat(math.Round(f), 'f', 0, 64))
}

// renderGridUsageChart draws series, each one amount per day from first, as a
// step chart with whole-dollar y labels and dates below, or "" when two
// columns per day don't fit in width. paint colors a series by index.
func renderGridUsageChart(first time.Time, series [][]float64, width int, paint func(s string, i int) string) string {
	days := len(series[0])
	top := 1.0
	for _, s := range series {
		top = max(top, slices.Max(s))
	}
	height := min(gridUsageChartHeight, int(top))
	labels := make([]string, height+1)
	labelWidth := 0
	for r := range labels {
		labels[r] = gridUsageDollars(top * float64(r) / float64(height))
		labelWidth = max(labelWidth, len(labels[r]))
	}
	step := max(2, min(width-labelWidth-4, gridUsageChartWidth)/days) // columns per day
	cols := days * step
	if width < labelWidth+4+cols {
		return ""
	}
	grid := make([][]string, height+1) // grid[0] is the zero row
	for r := range grid {
		grid[r] = slices.Repeat([]string{" "}, cols)
	}
	// Draw the last series first so the largest ends up on top.
	for i, s := range slices.Backward(series) {
		prev := -1
		for d, v := range s {
			y := int(math.Round(v / top * float64(height)))
			if v > 0 {
				y = max(y, 1) // keep any spend off the zero line
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
		fmt.Fprintf(&b, "%*s%s%s\n", labelWidth, labels[r], tick, strings.TrimRight(strings.Join(grid[r], ""), " "))
	}
	// A date every 8+ columns, centered under its day, always including the last.
	axis := []byte(strings.Repeat(" ", labelWidth+4+cols))
	every := (8 + step - 1) / step
	for d := (days - 1) % every; d < days; d += every {
		label := first.AddDate(0, 0, d).Format("Jan 2")
		copy(axis[labelWidth+2+max(0, d*step+(step-len(label))/2):], label)
	}
	b.WriteString(strings.TrimRight(string(axis), " ") + "\n")
	return b.String()
}
