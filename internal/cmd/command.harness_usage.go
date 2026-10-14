package cmd

import (
	"cmp"
	"fmt"
	"maps"
	"math"
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
	Register("harness usage", commandHarnessUsage)
}

// commandHarnessUsage shows your Code spend this month in one of your teams,
// per day and per model, against your spend limit there.
func commandHarnessUsage(ctx *CommandContext, f *cmd.HarnessUsageFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	api := cl.API()
	me, err := api.GetUsersMe(ctx)
	if err != nil {
		return fmt.Errorf("getting current user: %w", err)
	}
	listing, err := api.GetRoutesSettingsUsersTeams(ctx, "me")
	if err != nil {
		// A team or workspace key authenticates as a service account, which
		// has no teams or routes keys of its own.
		if session, serr := ctx.authInfo.Session(); serr == nil && session.UsesAPIKey() {
			ctx.LogLine("If you're using a team or workspace API key, run 'baseten auth login' to see your own usage.")
		}
		return fmt.Errorf("getting your teams: %w", err)
	}
	teams := listing.Teams
	names := make([]string, len(teams))
	for i, t := range teams {
		names[i] = strconv.Quote(t.TeamName)
	}
	// Opened before any early return, so JSON output is at least [].
	var jw *JSONArrayWriter
	if ctx.JSON {
		jw = ctx.NewJSONArrayWriter()
		defer jw.Close()
	}
	var team managementapi.RouteTeamUserSettings
	switch {
	case f.Team != "":
		i := slices.IndexFunc(teams, func(t managementapi.RouteTeamUserSettings) bool { return t.TeamId == f.Team || t.TeamName == f.Team })
		if i < 0 {
			return cmd.NewErrUsagef("no team of yours matched %q; your teams are %s", f.Team, strings.Join(names, ", "))
		}
		team = teams[i]
	case len(teams) == 0:
		ctx.LogLine("No usage this month.")
		return nil
	case len(teams) > 1:
		return cmd.NewErrUsagef("you're in %d teams; pass --team with one of %s", len(teams), strings.Join(names, ", "))
	default:
		team = teams[0]
	}

	// This month so far by default, the period spend limits apply to, in
	// whole UTC days like the API.
	startDate, endDate, err := routesUsageWindow(ctx, routesUsageRequest{start: f.Start, end: f.End, since: f.Since,
		defaultStart: func(end time.Time) time.Time {
			end = end.UTC()
			return time.Date(end.Year(), end.Month(), 1, 0, 0, 0, 0, time.UTC)
		}})
	if err != nil {
		return err
	}
	start, _ := time.Parse(time.DateOnly, startDate)
	end, _ := time.Parse(time.DateOnly, endDate)
	days := int(end.Sub(start).Hours() / 24)
	// The limit, and the settings' spend and blocked state, are this month's.
	today := ctx.Now().UTC()
	current := startDate == time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, time.UTC).Format(time.DateOnly) &&
		endDate == today.AddDate(0, 0, 1).Format(time.DateOnly)
	dims := []managementapi.RouteUsageDimension{managementapi.RouteUsageDimension_TEAM, managementapi.RouteUsageDimension_MODEL}
	// Spend limits are per user, and admins would otherwise see the whole
	// organization's usage.
	userIDs := []string{me.UserId}
	pageSize := routesUsageMaxBuckets
	params := managementapi.GetV1RoutesUsageParams{StartDate: &startDate, EndDate: &endDate, GroupBy: &dims, UserIds: &userIDs, Limit: &pageSize, TeamId: &team.TeamId}
	byModel := map[string]*harnessModel{}
	for {
		resp, err := api.GetRoutesUsage(ctx, params)
		if err != nil {
			return fmt.Errorf("getting routes usage: %w", err)
		}
		for _, b := range resp.Items {
			if ctx.JSON {
				jw.Write(b)
				continue
			}
			date, _ := time.Parse(time.DateOnly, b.Date)
			for _, r := range b.Results {
				t, err := routesUsageResultTotal(r)
				if err != nil {
					return err
				}
				cost, _ := t.cost.Float64()
				// Requests that produced no tokens, such as failed ones, cost
				// nothing and would only add empty rows.
				if cost == 0 && t.input+t.output == 0 {
					continue
				}
				name := cmp.Or(deref(r.Model), "(unknown)")
				m := byModel[name]
				if m == nil {
					m = &harnessModel{name: name, priced: true, daily: make([]float64, days)}
					byModel[name] = m
				}
				m.cost += cost
				m.daily[int(date.Sub(start).Hours()/24)] += cost
				m.priced = m.priced && t.priced
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
	if current {
		ctx.LogLine(start.Format("January 2006") + " · resets " + start.AddDate(0, 1, 0).Format("Jan 2"))
	} else {
		ctx.LogLine(start.Format("Jan 2") + " to " + end.AddDate(0, 0, -1).Format("Jan 2, 2006"))
	}
	models := slices.SortedFunc(maps.Values(byModel), func(x, y *harnessModel) int {
		return cmp.Or(cmp.Compare(y.cost, x.cost), strings.Compare(x.name, y.name))
	})
	harnessRender(ctx, team, start, current, models)
	return nil
}

// harnessModel is one model's spend in a team this month. Whole dollars don't
// need the API's exact decimals.
type harnessModel struct {
	name string
	cost float64
	// priced is false once any of the usage came back without a cost.
	priced bool
	// daily is the cost by day of the window, for the chart.
	daily []float64
}

// harnessRender prints what you spent in the team over the window from start
// of your limit there, a chart of spend per day when stdout is a terminal,
// and each model's spend and share, most expensive first, all to the nearest
// dollar. The limit, and the settings' spend and blocked state, are this
// month's, so any other window shows only what the usage adds up to.
func harnessRender(ctx *CommandContext, s managementapi.RouteTeamUserSettings, start time.Time, current bool, models []*harnessModel) {
	all, priced := 0.0, true
	for _, m := range models {
		all += m.cost
		priced = priced && m.priced
	}
	spentUSD, limitUSD, limit := all, 0.0, ""
	if current {
		spentUSD = harnessFloat(s.MonthSpendUsd)
		if l := s.SpendLimit.Effective.MonthlyLimitUsd; l != nil {
			limitUSD = harnessFloat(*l)
			limit = harnessDollars(limitUSD)
		}
	}
	spent := harnessDollars(spentUSD)
	// One label and one amount column for the team and its models.
	labelWidth, amountWidth := ansi.StringWidth(s.TeamName)+2, len(spent)
	for _, m := range models {
		labelWidth = max(labelWidth, 4+ansi.StringWidth(m.name)+2)
		amountWidth = max(amountWidth, len(harnessMoney(m)))
	}
	// The writer drops the styling stdout can't show, so there is no need to
	// query the terminal for it.
	out := colorprofile.NewWriter(ctx.Stdout, os.Environ())
	write := func(line string) { panicOnOutputError(fmt.Fprintln(out, line)) }
	bold, dim := ansi.Style{}.Bold(), ansi.Style{}.Faint()
	warn, crit := ansi.Style{}.ForegroundColor(ansi.Yellow), ansi.Style{}.ForegroundColor(ansi.Red).Bold()
	paint := func(s string, i int) string {
		return ansi.Style{}.ForegroundColor(usageChartColors[i%len(usageChartColors)]).Styled(s)
	}
	pad := func(s string, w int) string { return s + strings.Repeat(" ", w-ansi.StringWidth(s)) }
	right := func(s string, w int) string { return strings.Repeat(" ", w-ansi.StringWidth(s)) + s }

	styled := spent
	switch {
	case current && s.IsBlocked:
		styled = crit.Styled(spent)
	case limit != "" && spentUSD >= 0.75*limitUSD:
		styled = warn.Styled(spent)
	}
	line := pad(bold.Styled(s.TeamName), labelWidth) + right(styled, amountWidth)
	if current && limit == "" {
		line += " " + dim.Styled("· no limit")
	} else if current {
		line += " of " + limit
		// The team default only when yours differs from it.
		if d := s.SpendLimit.TeamDefault; d != nil {
			if td := harnessDollars(harnessFloat(d.PerMemberMonthlyLimitUsd)); td != limit {
				line += " " + dim.Styled("(team default "+td+")")
			}
		}
	}
	if current && s.IsBlocked {
		what := "limit reached"
		if s.BlockedBy != nil && *s.BlockedBy == managementapi.RouteSpendBlockedBy_team {
			what = "team limit reached"
		}
		line += " · " + crit.Styled(what)
	}
	write(line)
	if f, ok := ctx.Stdout.(*os.File); ok && term.IsTerminal(f.Fd()) && len(models) > 0 {
		width, _, _ := term.GetSize(f.Fd())
		series := make([][]float64, len(models))
		for i, m := range models {
			series[i] = m.daily
		}
		if c := renderUsageChart(start, series, cmp.Or(width, 80)-2, paint); c != "" {
			write("\n  " + strings.ReplaceAll(strings.TrimSuffix(c, "\n"), "\n", "\n  ") + "\n")
		}
	}
	for i, m := range models {
		line := pad("  "+paint("●", i)+" "+m.name, labelWidth) + right(harnessMoney(m), amountWidth)
		if priced && all > 0 {
			line += " " + dim.Styled(right("("+strconv.Itoa(int(m.cost/all*100+0.5))+"%)", 6))
		}
		write(line)
	}
}

// harnessFloat parses an exact decimal amount from the API; whole dollars
// don't need its precision.
func harnessFloat(s string) float64 {
	f, _ := strconv.ParseFloat(s, 64)
	return f
}

// harnessDollars renders an amount to the nearest dollar, like $1,234, keeping
// anything under half a dollar visible as <$1.
func harnessDollars(f float64) string {
	if f > 0 && f < 0.5 {
		return "<$1"
	}
	return "$" + billingGroupDigits(strconv.FormatFloat(math.Round(f), 'f', 0, 64))
}

// harnessMoney renders a model's spend, or "-" when some of it has no price.
func harnessMoney(m *harnessModel) string {
	if !m.priced {
		return "-"
	}
	return harnessDollars(m.cost)
}

// usageChartHeight is the most rows above the chart's zero row. There is at
// most one per dollar, so the whole-dollar labels never repeat.
const usageChartHeight = 7

// usageChartWidth is the columns the plot aims for, so it isn't spread across
// a wide terminal; a full month at two columns a day runs a little past it.
const usageChartWidth = 48

// usageChartColors are the colors of the series, in order.
var usageChartColors = []ansi.BasicColor{ansi.Blue, ansi.Green, ansi.Magenta, ansi.Cyan, ansi.Yellow, ansi.Red}

// renderUsageChart renders series, each with one dollar amount per day from
// first, as an asciichart-style step chart with a y-axis in whole dollars and
// date labels below, fitting width columns, or "" when two columns per day
// don't fit. Each day is a run of equal width with its date centered under
// it, like Claude Code's, so the last day's run ends with its date, which can
// stick out two columns past the plot. paint colors a series' characters by
// its index. It is
// hand-rolled rather than asciigraph's, which interpolates between points
// instead of stepping, spaces its date ticks evenly rather than at day
// starts, and paints with its own escapes rather than the terminal palette;
// the glue to work around that is most of this code anyway.
func renderUsageChart(first time.Time, series [][]float64, width int, paint func(s string, i int) string) string {
	days := len(series[0])
	top := 1.0 // At least a dollar, so the top label isn't $0.
	for _, s := range series {
		for _, v := range s {
			top = max(top, v)
		}
	}
	height := min(usageChartHeight, int(top))
	labels := make([]string, height+1)
	labelWidth := 0
	for r := range labels {
		labels[r] = harnessDollars(top * float64(r) / float64(height))
		labelWidth = max(labelWidth, len(labels[r]))
	}
	// Each day spans step columns, its first one stepping from the day before.
	step := max(2, min(width-labelWidth-4, usageChartWidth)/days)
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
