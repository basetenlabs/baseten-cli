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
	Register("grid usage", commandGridUsage)
}

// commandGridUsage shows your Grid spend this month in each of your teams,
// per day and per model, against your spend limit there.
func commandGridUsage(ctx *CommandContext, f *cmd.GridUsageFlags) error {
	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	api := cl.API()
	me, err := api.GetUsersMe(ctx)
	if err != nil {
		return fmt.Errorf("getting current user: %w", err)
	}
	// The server picks your only team, or asks for --team when you have several.
	settingsParams := managementapi.GetV1GridTeamSettingsUsersUserIdParams{}
	if f.Team != "" {
		settingsParams.TeamId = &f.Team
	}
	settings, err := api.GetGridTeamSettingsUsers(ctx, "me", settingsParams)
	if err != nil {
		// A team or workspace key authenticates as a service account, which
		// has no teams or routes keys of its own.
		if session, serr := ctx.authInfo.Session(); serr == nil && session.UsesAPIKey() {
			ctx.LogLine("If you're using a team or workspace API key, run 'baseten auth login' to see your own usage.")
		}
		return fmt.Errorf("getting your settings: %w", err)
	}
	teams := []managementapi.RouteTeamUserSettings{*settings}
	// Opened before any early return, so JSON output is at least [].
	var jw *JSONArrayWriter
	if ctx.JSON {
		jw = ctx.NewJSONArrayWriter()
		defer jw.Close()
	}
	// This month so far by default, the period spend limits apply to, in
	// whole UTC days like the API.
	startDate, endDate, err := gridUsageWindow(ctx, gridUsageRequest{start: f.Start, end: f.End, since: f.Since,
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
	pageSize := gridUsageMaxBuckets
	params := managementapi.GetV1GridRoutesUsageParams{StartDate: &startDate, EndDate: &endDate, GroupBy: &dims, UserIds: &userIDs, Limit: &pageSize}
	if f.Team != "" {
		params.TeamId = &teams[0].TeamId
	}
	// Usage in a team you're no longer in, or from before usage was attributed
	// to teams, has no settings to show it against, so it is left out.
	byTeam := map[string]map[string]*gridUsageModel{}
	for _, t := range teams {
		byTeam[t.TeamId] = map[string]*gridUsageModel{}
	}
	for {
		resp, err := api.GetGridRoutesUsage(ctx, params)
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
				t, err := gridUsageResultTotal(r)
				if err != nil {
					return err
				}
				cost, _ := t.cost.Float64()
				// Requests that produced no tokens, such as failed ones, cost
				// nothing and would only add empty rows.
				if cost == 0 && t.input+t.output == 0 {
					continue
				}
				byModel := byTeam[deref(r.TeamId)]
				if byModel == nil {
					continue
				}
				name := cmp.Or(deref(r.Model), "(unknown)")
				m := byModel[name]
				if m == nil {
					m = &gridUsageModel{name: name, priced: true, daily: make([]float64, days)}
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
	rows := make([]gridUsageTeam, len(teams))
	for i, t := range teams {
		rows[i] = gridUsageTeam{settings: t, models: gridUsageFoldModels(slices.SortedFunc(maps.Values(byTeam[t.TeamId]), func(x, y *gridUsageModel) int {
			return cmp.Or(cmp.Compare(y.cost, x.cost), strings.Compare(x.name, y.name))
		}), days)}
	}
	renderGridUsage(ctx, start, current, rows)
	return nil
}

// gridUsageTeam is your settings in a team and your spend there by model, most
// expensive first, with all but the top few folded into "other".
type gridUsageTeam struct {
	settings managementapi.RouteTeamUserSettings
	models   []*gridUsageModel
}

// spent is what you spent in the team: this month's from the settings when
// the window is this month so far, otherwise what the usage adds up to.
func (t gridUsageTeam) spent(current bool) float64 {
	if current {
		return gridUsageFloat(t.settings.MonthSpendUsd)
	}
	all := 0.0
	for _, m := range t.models {
		all += m.cost
	}
	return all
}

// gridUsageTopModels is how many models the chart and list show on their own;
// the rest are summed into one "other" row so neither grows past a glance.
const gridUsageTopModels = 5

// gridUsageFoldModels folds every model after the top gridUsageTopModels, which
// are sorted most expensive first, into one "other" model with their summed
// spend over the window's days.
func gridUsageFoldModels(models []*gridUsageModel, days int) []*gridUsageModel {
	if len(models) <= gridUsageTopModels {
		return models
	}
	other := &gridUsageModel{name: "other", priced: true, daily: make([]float64, days)}
	for _, m := range models[gridUsageTopModels:] {
		other.cost += m.cost
		other.priced = other.priced && m.priced
		for d, v := range m.daily {
			other.daily[d] += v
		}
	}
	return append(models[:gridUsageTopModels:gridUsageTopModels], other)
}

// gridUsageModel is one model's spend in a team this month. Whole dollars don't
// need the API's exact decimals.
type gridUsageModel struct {
	name string
	cost float64
	// priced is false once any of the usage came back without a cost.
	priced bool
	// daily is the cost by day of the window, for the chart.
	daily []float64
}

// renderGridUsage prints, for each team, what you spent there over the window
// from start of your limit there, a chart of spend per day when stdout is a
// terminal, and each model's spend and share, most expensive first, all to
// the nearest dollar. The limit, and the settings' spend and blocked state,
// are this month's, so any other window shows only what the usage adds up to.
func renderGridUsage(ctx *CommandContext, start time.Time, current bool, teams []gridUsageTeam) {
	// One label and one amount column shared by every team and its models.
	labelWidth, amountWidth := 0, 0
	for _, t := range teams {
		labelWidth = max(labelWidth, ansi.StringWidth(t.settings.TeamName)+2)
		amountWidth = max(amountWidth, len(gridUsageDollars(t.spent(current))))
		for _, m := range t.models {
			labelWidth = max(labelWidth, 4+ansi.StringWidth(m.name)+2)
			amountWidth = max(amountWidth, len(gridUsageMoney(m)))
		}
	}
	// The writer drops the styling stdout can't show, so there is no need to
	// query the terminal for it.
	out := colorprofile.NewWriter(ctx.Stdout, os.Environ())
	write := func(line string) { panicOnOutputError(fmt.Fprintln(out, line)) }
	bold, dim := ansi.Style{}.Bold(), ansi.Style{}.Faint()
	warn, crit := ansi.Style{}.ForegroundColor(ansi.Yellow), ansi.Style{}.ForegroundColor(ansi.Red).Bold()
	paint := func(s string, i int) string {
		return ansi.Style{}.ForegroundColor(gridUsageChartColors[i%len(gridUsageChartColors)]).Styled(s)
	}
	pad := func(s string, w int) string { return s + strings.Repeat(" ", w-ansi.StringWidth(s)) }
	right := func(s string, w int) string { return strings.Repeat(" ", w-ansi.StringWidth(s)) + s }
	width := 0
	if f, ok := ctx.Stdout.(*os.File); ok && term.IsTerminal(f.Fd()) {
		w, _, _ := term.GetSize(f.Fd())
		width = cmp.Or(w, 80)
	}

	for ti, t := range teams {
		if ti > 0 {
			write("")
		}
		s, models := t.settings, t.models
		all, priced := t.spent(false), true
		for _, m := range models {
			priced = priced && m.priced
		}
		spentUSD, limitUSD, limit := t.spent(current), 0.0, ""
		if l := s.SpendLimit.Effective.MonthlyLimitUsd; current && l != nil {
			limitUSD = gridUsageFloat(*l)
			limit = gridUsageDollars(limitUSD)
		}
		spent := gridUsageDollars(spentUSD)
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
				if td := gridUsageDollars(gridUsageFloat(d.PerMemberMonthlyLimitUsd)); td != limit {
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
		if width > 0 && len(models) > 0 {
			series := make([][]float64, len(models))
			for i, m := range models {
				series[i] = m.daily
			}
			if c := renderGridUsageChart(start, series, width-2, paint); c != "" {
				write("\n  " + strings.ReplaceAll(strings.TrimSuffix(c, "\n"), "\n", "\n  ") + "\n")
			}
		}
		for i, m := range models {
			line := pad("  "+paint("●", i)+" "+m.name, labelWidth) + right(gridUsageMoney(m), amountWidth)
			if priced && all > 0 {
				line += " " + dim.Styled(right("("+strconv.Itoa(int(m.cost/all*100+0.5))+"%)", 6))
			}
			write(line)
		}
	}
}

// gridUsageFloat parses an exact decimal amount from the API; whole dollars
// don't need its precision.
func gridUsageFloat(s string) float64 {
	f, _ := strconv.ParseFloat(s, 64)
	return f
}

// gridUsageDollars renders an amount to the nearest dollar, like $1,234, keeping
// anything under half a dollar visible as <$1.
func gridUsageDollars(f float64) string {
	if f > 0 && f < 0.5 {
		return "<$1"
	}
	return "$" + billingGroupDigits(strconv.FormatFloat(math.Round(f), 'f', 0, 64))
}

// gridUsageMoney renders a model's spend, or "-" when some of it has no price.
func gridUsageMoney(m *gridUsageModel) string {
	if !m.priced {
		return "-"
	}
	return gridUsageDollars(m.cost)
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
// it, like Claude Code's, so the last day's run ends with its date, which can
// stick out two columns past the plot. paint colors a series' characters by
// its index. It is
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
