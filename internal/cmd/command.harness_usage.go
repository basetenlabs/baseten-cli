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

// commandHarnessUsage shows your Code spend this month in each of your teams,
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
	if f.Team != "" {
		if teams, err = harnessPickTeam(teams, f.Team); err != nil {
			return err
		}
	}

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
	params := managementapi.GetV1RoutesUsageParams{StartDate: &startDate, EndDate: &endDate, GroupBy: &dims, UserIds: &userIDs, Limit: &pageSize}
	if f.Team != "" {
		params.TeamId = &teams[0].TeamId
	}
	var jw *JSONArrayWriter
	if ctx.JSON {
		jw = ctx.NewJSONArrayWriter()
		defer jw.Close()
	}
	// Usage by team ID, then model. "" is usage metered before it was
	// attributed to teams.
	usage := map[string]map[string]*harnessModel{}
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
				models := usage[deref(r.TeamId)]
				if models == nil {
					models = map[string]*harnessModel{}
					usage[deref(r.TeamId)] = models
				}
				name := cmp.Or(deref(r.Model), "(unknown)")
				m := models[name]
				if m == nil {
					m = &harnessModel{name: name, routesUsageTotal: newRoutesUsageTotal(), daily: make([]float64, days)}
					models[name] = m
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
	if len(teams) == 0 && len(usage) == 0 {
		ctx.LogLine("No usage this month.")
		return nil
	}
	ctx.LogLine(today.Format("January 2006") + " · resets " + start.AddDate(0, 1, 0).Format("Jan 2"))
	var blocks []harnessBlock
	for i := range teams {
		blocks = append(blocks, newHarnessBlock(&teams[i], usage[teams[i].TeamId]))
	}
	if legacy, ok := usage[""]; ok {
		blocks = append(blocks, newHarnessBlock(nil, legacy))
	}
	harnessRender(ctx, start, blocks)
	return nil
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
		names := make([]string, len(teams))
		for i, t := range teams {
			names[i] = strconv.Quote(t.TeamName)
		}
		return nil, cmd.NewErrUsagef("no team of yours matched %q; your teams are %s", input, strings.Join(names, ", "))
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

// harnessBlock is one team's output: your settings there, when you're in it,
// and your usage there, when there is any, most expensive model first.
type harnessBlock struct {
	settings *managementapi.RouteTeamUserSettings
	models   []*harnessModel
	total    *routesUsageTotal
}

func newHarnessBlock(settings *managementapi.RouteTeamUserSettings, byName map[string]*harnessModel) harnessBlock {
	b := harnessBlock{settings: settings, models: slices.Collect(maps.Values(byName)), total: newRoutesUsageTotal()}
	slices.SortFunc(b.models, func(x, y *harnessModel) int {
		return cmp.Or(y.cost.Cmp(x.cost), strings.Compare(x.name, y.name))
	})
	for _, m := range b.models {
		b.total.add(m.routesUsageTotal)
	}
	return b
}

func (b harnessBlock) name() string {
	if b.settings == nil {
		return "(no team)"
	}
	return b.settings.TeamName
}

// spent is what you spent in the team: the metered spend limits are checked
// against, or the usage's total before team attribution.
func (b harnessBlock) spent() string {
	if b.settings == nil {
		return harnessMoney(b.total)
	}
	return harnessDollars(harnessFloat(b.settings.MonthSpendUsd))
}

// harnessRender prints a block per team: what you spent there of your limit
// there, a chart of spend per day when stdout is a terminal, and each model's
// spend, all to the nearest dollar.
func harnessRender(ctx *CommandContext, start time.Time, blocks []harnessBlock) {
	// One label and one amount column across every block.
	labelWidth, amountWidth := 0, 0
	for _, b := range blocks {
		labelWidth = max(labelWidth, ansi.StringWidth(b.name())+2)
		amountWidth = max(amountWidth, len(b.spent()))
		for _, m := range b.models {
			labelWidth = max(labelWidth, 4+ansi.StringWidth(m.name)+2)
			amountWidth = max(amountWidth, len(harnessMoney(m.routesUsageTotal)))
		}
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
	chart, width := isTerminalWriter(ctx.Stdout), 80
	if f, ok := ctx.Stdout.(*os.File); ok {
		if w, _, err := term.GetSize(f.Fd()); err == nil && w > 0 {
			width = w
		}
	}
	pad := func(s string, w int) string { return s + strings.Repeat(" ", max(0, w-ansi.StringWidth(s))) }
	right := func(s string, w int) string { return strings.Repeat(" ", max(0, w-ansi.StringWidth(s))) + s }

	for i, b := range blocks {
		if i > 0 {
			write("")
		}
		s, spent, limit := b.settings, b.spent(), ""
		if s != nil && s.SpendLimit.Effective.MonthlyLimitUsd != nil {
			limit = harnessDollars(harnessFloat(*s.SpendLimit.Effective.MonthlyLimitUsd))
		}
		styled := spent
		switch {
		case s != nil && s.IsBlocked:
			styled = crit.Styled(spent)
		case limit != "" && harnessFloat(s.MonthSpendUsd) >= 0.75*harnessFloat(*s.SpendLimit.Effective.MonthlyLimitUsd):
			styled = warn.Styled(spent)
		}
		line := pad(bold.Styled(b.name()), labelWidth) + right(styled, amountWidth)
		switch {
		case s == nil:
		case limit == "":
			line += " " + dim.Styled("· no limit")
		default:
			line += " of " + limit
			// The team default only when yours differs from it.
			if d := s.SpendLimit.TeamDefault; d != nil {
				if td := harnessDollars(harnessFloat(d.PerMemberMonthlyLimitUsd)); td != limit {
					line += " " + dim.Styled("(team default "+td+")")
				}
			}
		}
		if s != nil && s.IsBlocked {
			what := "limit reached"
			if s.BlockedBy != nil && *s.BlockedBy == managementapi.RouteSpendBlockedBy_team {
				what = "team limit reached"
			}
			line += " · " + crit.Styled(what)
		}
		write(line)
		if chart && len(b.models) > 0 {
			series := make([]usageChartSeries, len(b.models))
			for i, m := range b.models {
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
		total, _ := b.total.cost.Float64()
		for i, m := range b.models {
			line := pad("  "+paint("●", i)+" "+m.name, labelWidth) + right(harnessMoney(m.routesUsageTotal), amountWidth)
			if b.total.priced && total > 0 {
				c, _ := m.cost.Float64()
				line += " " + dim.Styled(right("("+strconv.Itoa(int(c/total*100+0.5))+"%)", 6))
			}
			write(line)
		}
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
