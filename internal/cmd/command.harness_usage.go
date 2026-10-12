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
	switch {
	case f.Team != "":
		if teams, err = harnessPickTeam(teams, f.Team); err != nil {
			return err
		}
	case len(teams) == 0:
		ctx.LogLine("No usage this month.")
		return nil
	case len(teams) > 1:
		return cmd.NewErrUsagef("you're in %d teams; pass --team with one of %s", len(teams), harnessTeamNames(teams))
	}
	team := teams[0]

	// This month so far, the period spend limits apply to, in whole UTC days
	// like the API.
	today := ctx.Now().UTC().Truncate(24 * time.Hour)
	start := time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, time.UTC)
	days := int(today.Sub(start).Hours()/24) + 1
	startDate, endDate := start.Format(time.DateOnly), today.AddDate(0, 0, 1).Format(time.DateOnly)
	dims := []managementapi.RouteUsageDimension{managementapi.RouteUsageDimension_TEAM, managementapi.RouteUsageDimension_MODEL}
	// Spend limits are per user, and admins would otherwise see the whole
	// organization's usage.
	userIDs := []string{me.UserId}
	pageSize := routesUsageMaxBuckets
	params := managementapi.GetV1RoutesUsageParams{StartDate: &startDate, EndDate: &endDate, GroupBy: &dims, UserIds: &userIDs, Limit: &pageSize, TeamId: &team.TeamId}
	var jw *JSONArrayWriter
	if ctx.JSON {
		jw = ctx.NewJSONArrayWriter()
		defer jw.Close()
	}
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
			date, err := time.Parse(time.DateOnly, b.Date)
			if err != nil {
				return fmt.Errorf("routes usage returned an invalid date %q", b.Date)
			}
			day := int(date.Sub(start).Hours() / 24)
			for _, r := range b.Results {
				t, err := routesUsageResultTotal(r)
				if err != nil {
					return err
				}
				// Requests that produced no tokens, such as failed ones, cost
				// nothing and would only add empty rows.
				if t.cost.Sign() == 0 && t.input+t.output == 0 {
					continue
				}
				name := cmp.Or(deref(r.Model), "(unknown)")
				m := byModel[name]
				if m == nil {
					m = &harnessModel{name: name, routesUsageTotal: newRoutesUsageTotal(), daily: make([]float64, days)}
					byModel[name] = m
				}
				m.add(t)
				if day >= 0 && day < days {
					c, _ := t.cost.Float64()
					m.daily[day] += c
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
	ctx.LogLine(today.Format("January 2006") + " · resets " + start.AddDate(0, 1, 0).Format("Jan 2"))
	models := slices.Collect(maps.Values(byModel))
	slices.SortFunc(models, func(x, y *harnessModel) int {
		return cmp.Or(y.cost.Cmp(x.cost), strings.Compare(x.name, y.name))
	})
	harnessRender(ctx, team, start, models)
	return nil
}

// harnessTeamNames quotes the teams' names for a message.
func harnessTeamNames(teams []managementapi.RouteTeamUserSettings) string {
	names := make([]string, len(teams))
	for i, t := range teams {
		names[i] = strconv.Quote(t.TeamName)
	}
	return strings.Join(names, ", ")
}

// harnessPickTeam returns the one of your teams input names, by ID or name.
func harnessPickTeam(teams []managementapi.RouteTeamUserSettings, input string) ([]managementapi.RouteTeamUserSettings, error) {
	var matches []managementapi.RouteTeamUserSettings
	for _, t := range teams {
		switch {
		case t.TeamId == input:
			return []managementapi.RouteTeamUserSettings{t}, nil
		case t.TeamName == input:
			matches = append(matches, t)
		}
	}
	switch len(matches) {
	case 1:
		return matches, nil
	case 0:
		return nil, cmd.NewErrUsagef("no team of yours matched %q; your teams are %s", input, harnessTeamNames(teams))
	default:
		return nil, cmd.NewErrUsagef("multiple teams named %q; pass the team ID instead", input)
	}
}

// harnessModel is one model's spend in a team this month.
type harnessModel struct {
	name string
	*routesUsageTotal
	// daily is the cost by day of the month, for the chart.
	daily []float64
}

// harnessRender prints what you spent in the team this month of your limit
// there, a chart of spend per day when stdout is a terminal, and each model's
// spend and share, most expensive first, all to the nearest dollar.
func harnessRender(ctx *CommandContext, s managementapi.RouteTeamUserSettings, start time.Time, models []*harnessModel) {
	spent, limit, total := harnessDollars(harnessFloat(s.MonthSpendUsd)), "", newRoutesUsageTotal()
	if s.SpendLimit.Effective.MonthlyLimitUsd != nil {
		limit = harnessDollars(harnessFloat(*s.SpendLimit.Effective.MonthlyLimitUsd))
	}
	// One label and one amount column for the team and its models.
	labelWidth, amountWidth := ansi.StringWidth(s.TeamName)+2, len(spent)
	for _, m := range models {
		labelWidth = max(labelWidth, 4+ansi.StringWidth(m.name)+2)
		amountWidth = max(amountWidth, len(harnessMoney(m.routesUsageTotal)))
		total.add(m.routesUsageTotal)
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
	pad := func(s string, w int) string { return s + strings.Repeat(" ", max(0, w-ansi.StringWidth(s))) }
	right := func(s string, w int) string { return strings.Repeat(" ", max(0, w-ansi.StringWidth(s))) + s }

	styled := spent
	switch {
	case s.IsBlocked:
		styled = crit.Styled(spent)
	case limit != "" && harnessFloat(s.MonthSpendUsd) >= 0.75*harnessFloat(*s.SpendLimit.Effective.MonthlyLimitUsd):
		styled = warn.Styled(spent)
	}
	line := pad(bold.Styled(s.TeamName), labelWidth) + right(styled, amountWidth)
	if limit == "" {
		line += " " + dim.Styled("· no limit")
	} else {
		line += " of " + limit
		// The team default only when yours differs from it.
		if d := s.SpendLimit.TeamDefault; d != nil {
			if td := harnessDollars(harnessFloat(d.PerMemberMonthlyLimitUsd)); td != limit {
				line += " " + dim.Styled("(team default "+td+")")
			}
		}
	}
	if s.IsBlocked {
		what := "limit reached"
		if s.BlockedBy != nil && *s.BlockedBy == managementapi.RouteSpendBlockedBy_team {
			what = "team limit reached"
		}
		line += " · " + crit.Styled(what)
	}
	write(line)
	if isTerminalWriter(ctx.Stdout) && len(models) > 0 {
		width := 80
		if f, ok := ctx.Stdout.(*os.File); ok {
			if w, _, err := term.GetSize(f.Fd()); err == nil && w > 0 {
				width = w
			}
		}
		series := make([]usageChartSeries, len(models))
		for i, m := range models {
			series[i] = usageChartSeries{values: m.daily}
		}
		if c := renderUsageChart(start, series, width-2, paint); c != "" {
			write("")
			for _, l := range strings.Split(strings.TrimSuffix(c, "\n"), "\n") {
				write("  " + l)
			}
			write("")
		}
	}
	all, _ := total.cost.Float64()
	for i, m := range models {
		line := pad("  "+paint("●", i)+" "+m.name, labelWidth) + right(harnessMoney(m.routesUsageTotal), amountWidth)
		if total.priced && all > 0 {
			c, _ := m.cost.Float64()
			line += " " + dim.Styled(right("("+strconv.Itoa(int(c/all*100+0.5))+"%)", 6))
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
	n := math.Round(f)
	if n == 0 && f > 0 {
		return "<$1"
	}
	return "$" + billingGroupDigits(strconv.FormatFloat(n, 'f', 0, 64))
}

// harnessMoney renders a usage total to the nearest dollar, or "-" when some
// of it has no price.
func harnessMoney(t *routesUsageTotal) string {
	if !t.priced {
		return "-"
	}
	f, _ := t.cost.Float64()
	return harnessDollars(f)
}
