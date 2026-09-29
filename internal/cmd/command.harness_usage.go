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

// harnessUsageMaxBuckets is the most daily buckets the endpoint returns per
// page.
const harnessUsageMaxBuckets = 31

// harnessUsageRetentionDays is how many days of usage, including today, the
// endpoint keeps.
const harnessUsageRetentionDays = 92

func commandHarnessUsage(ctx *CommandContext, f *cmd.HarnessUsageFlags) error {
	// Month to date by default, the period spend limits apply to.
	start, end, err := usageWindow(ctx, f.Start, f.End, f.Since, func(end time.Time) time.Time {
		end = end.UTC()
		return time.Date(end.Year(), end.Month(), 1, 0, 0, 0, 0, time.UTC)
	})
	if err != nil {
		return err
	}
	// Usage comes in whole UTC days, so widen the window to cover them.
	startDay := start.UTC().Truncate(24 * time.Hour)
	endDay := end.UTC().Truncate(24 * time.Hour)
	if endDay.Before(end) {
		endDay = endDay.AddDate(0, 0, 1)
	}
	today := ctx.Now().UTC().Truncate(24 * time.Hour)
	if retained := today.AddDate(0, 0, 1-harnessUsageRetentionDays); startDay.Before(retained) {
		if !endDay.After(retained) {
			return cmd.NewErrUsagef("the window is older than the %d days of usage Baseten keeps", harnessUsageRetentionDays)
		}
		ctx.Logf("Usage is kept for %d days, so this only includes usage from %s on.\n",
			harnessUsageRetentionDays, retained.Format(time.DateOnly))
		startDay = retained
	}

	cl, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	api := cl.API()
	// Admins see the whole organization's usage, so always narrow to our own.
	me, err := api.GetUsersMe(ctx)
	if err != nil {
		return fmt.Errorf("getting current user: %w", err)
	}
	startDate := startDay.Format(time.DateOnly)
	endDate := endDay.Format(time.DateOnly)
	groupBy := []managementapi.RouteUsageDimension{managementapi.RouteUsageDimension_MODEL}
	userIDs := []string{me.UserId}
	pageSize := harnessUsageMaxBuckets
	params := managementapi.GetV1RoutesUsageParams{
		StartDate: &startDate,
		EndDate:   &endDate,
		GroupBy:   &groupBy,
		UserIds:   &userIDs,
		Limit:     &pageSize,
	}
	byModel := map[string]*harnessUsageEntry{}
	for {
		resp, err := api.GetRoutesUsage(ctx, params)
		if err != nil {
			return fmt.Errorf("getting routes usage: %w", err)
		}
		for _, bucket := range resp.Items {
			for _, r := range bucket.Results {
				if err := addHarnessUsage(byModel, r); err != nil {
					return err
				}
			}
		}
		if !resp.Pagination.HasMore || resp.Pagination.Cursor == nil {
			break
		}
		params.Cursor = resp.Pagination.Cursor
	}

	summary := summarizeHarnessUsage(byModel)
	if ctx.JSON {
		out := summary.output()
		out.StartDate = startDate
		out.EndDate = endDate
		ctx.OutputJSON(out)
		return nil
	}
	if len(summary.entries) == 0 {
		ctx.LogLine("No usage in the selected window.")
		// A team or workspace key authenticates as a service account, which
		// never creates routes keys, so its usage is always empty.
		if session, err := ctx.authInfo.Session(); err == nil && session.UsesAPIKey() {
			ctx.LogLine("If you're using a team or workspace API key, run 'baseten auth login' to see your own usage.")
		}
		return nil
	}
	ctx.LogLine(fmt.Sprintf("Window: %s through %s UTC · grouped by model · can lag by up to 15 minutes",
		startDate, endDay.AddDate(0, 0, -1).Format(time.DateOnly)))
	if !summary.priced {
		ctx.LogLine("A cost of \"-\" means some of that usage couldn't be priced.")
	}
	renderHarnessUsage(ctx, summary)
	return nil
}

// harnessUsageEntry is one model's usage, summed across daily buckets.
type harnessUsageEntry struct {
	model  string
	tokens cmd.HarnessUsageTokens
	cost   *big.Rat
	// priced is false once any of the model's usage came back without a cost.
	priced bool
}

// addHarnessUsage adds one daily result to its model's entry, summing costs as
// exact decimals.
func addHarnessUsage(byModel map[string]*harnessUsageEntry, r managementapi.RoutesUsageResult) error {
	model := deref(r.Model)
	e, ok := byModel[model]
	if !ok {
		e = &harnessUsageEntry{model: model, cost: new(big.Rat), priced: true}
		byModel[model] = e
	}
	addHarnessTokens(&e.tokens, cmd.HarnessUsageTokens{
		InputTokens:         int64(r.InputTokens),
		CachedInputTokens:   int64(r.CachedInputTokens),
		UncachedInputTokens: int64(r.UncachedInputTokens),
		OutputTokens:        int64(r.OutputTokens),
	})
	// Before basetenlabs/baseten#30972, the endpoint returned a null cost for
	// usage it couldn't price, which decodes as empty.
	if r.CostUsd == "" {
		e.priced = false
		return nil
	}
	cost, ok := new(big.Rat).SetString(r.CostUsd)
	if !ok {
		return fmt.Errorf("routes usage returned an invalid cost %q", r.CostUsd)
	}
	e.cost.Add(e.cost, cost)
	return nil
}

func addHarnessTokens(dst *cmd.HarnessUsageTokens, src cmd.HarnessUsageTokens) {
	dst.InputTokens += src.InputTokens
	dst.CachedInputTokens += src.CachedInputTokens
	dst.UncachedInputTokens += src.UncachedInputTokens
	dst.OutputTokens += src.OutputTokens
}

// harnessUsageSummary is the window's usage, most expensive model first.
type harnessUsageSummary struct {
	entries []*harnessUsageEntry
	tokens  cmd.HarnessUsageTokens
	// cost sums the priced usage; priced is false when some was left out.
	cost   *big.Rat
	priced bool
}

func summarizeHarnessUsage(byModel map[string]*harnessUsageEntry) harnessUsageSummary {
	s := harnessUsageSummary{cost: new(big.Rat), priced: true}
	for _, e := range byModel {
		// Requests that produced no tokens, such as failed ones, cost nothing
		// and would only add empty rows.
		if e.cost.Sign() == 0 && e.tokens.InputTokens+e.tokens.OutputTokens == 0 {
			continue
		}
		s.entries = append(s.entries, e)
		addHarnessTokens(&s.tokens, e.tokens)
		s.cost.Add(s.cost, e.cost)
		s.priced = s.priced && e.priced
	}
	slices.SortFunc(s.entries, func(x, y *harnessUsageEntry) int {
		if c := y.cost.Cmp(x.cost); c != 0 {
			return c
		}
		return cmp.Compare(x.model, y.model)
	})
	return s
}

func (s harnessUsageSummary) output() cmd.HarnessUsage {
	out := cmd.HarnessUsage{
		Totals: cmd.HarnessUsageTotals{CostUSD: formatHarnessCost(s.cost), CostComplete: s.priced, HarnessUsageTokens: s.tokens},
		Items:  make([]cmd.HarnessUsageItem, 0, len(s.entries)),
	}
	for _, e := range s.entries {
		item := cmd.HarnessUsageItem{Model: e.model, HarnessUsageTokens: e.tokens}
		if e.priced {
			cost := formatHarnessCost(e.cost)
			item.CostUSD = &cost
		}
		out.Items = append(out.Items, item)
	}
	return out
}

// formatHarnessCost renders an exact decimal without trailing zeros. Costs
// arrive with at most 10 decimal places, so sums of them fit too.
func formatHarnessCost(r *big.Rat) string {
	s := r.FloatString(10)
	s = strings.TrimRight(s, "0")
	return strings.TrimSuffix(s, ".")
}

// harnessMoney renders a cost in dollars and cents, keeping tiny nonzero costs
// visible, or "-" when some of it couldn't be priced.
func harnessMoney(r *big.Rat, priced bool) string {
	if !priced {
		return "-"
	}
	f, _ := r.Float64()
	if f > 0 && f < 0.005 {
		return "<$0.01"
	}
	return billingFormatMoney(f)
}

func renderHarnessUsage(ctx *CommandContext, s harnessUsageSummary) {
	row := func(name string, t cmd.HarnessUsageTokens, cost string) []string {
		return []string{
			name,
			billingGroupDigits(strconv.FormatInt(t.InputTokens, 10)),
			billingGroupDigits(strconv.FormatInt(t.CachedInputTokens, 10)),
			billingGroupDigits(strconv.FormatInt(t.OutputTokens, 10)),
			cost,
		}
	}
	rows := make([][]string, 0, len(s.entries)+1)
	for _, e := range s.entries {
		rows = append(rows, row(cmp.Or(e.model, "(unknown)"), e.tokens, harnessMoney(e.cost, e.priced)))
	}
	// A totals row only earns its keep once there is more than one row to
	// total; with a single row it would just repeat it.
	if len(s.entries) > 1 {
		rows = append(rows, row("ALL", s.tokens, harnessMoney(s.cost, s.priced)))
	}
	ctx.OutputTable(TableOutput{
		Headers:             []string{"MODEL", "INPUT", "CACHED", "OUTPUT", "COST"},
		Rows:                rows,
		RightAlignedColumns: []int{1, 2, 3, 4},
	})
}
