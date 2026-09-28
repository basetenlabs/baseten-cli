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
// page, which covers any month.
const harnessUsageMaxBuckets = 31

// harnessUsageRetentionDays is how many days of usage, including today, the
// endpoint keeps.
const harnessUsageRetentionDays = 92

func commandHarnessUsage(ctx *CommandContext, f *cmd.HarnessUsageFlags) error {
	today := ctx.Now().UTC().Truncate(24 * time.Hour)
	month := time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, time.UTC)
	if f.Month != "" {
		parsed, err := time.Parse("2006-01", f.Month)
		if err != nil {
			return cmd.NewErrUsagef("invalid --month %q; use YYYY-MM", f.Month)
		}
		if parsed.After(today) {
			return cmd.NewErrUsagef("--month %s is in the future", f.Month)
		}
		month = parsed
	}
	// The current month ends tomorrow so today's usage is included.
	end := month.AddDate(0, 1, 0)
	if tomorrow := today.AddDate(0, 0, 1); tomorrow.Before(end) {
		end = tomorrow
	}
	start := month
	if retained := today.AddDate(0, 0, 1-harnessUsageRetentionDays); start.Before(retained) {
		if !end.After(retained) {
			return cmd.NewErrUsagef("--month %s is older than the %d days of usage Baseten keeps",
				month.Format("2006-01"), harnessUsageRetentionDays)
		}
		ctx.Logf("Usage is kept for %d days, so this only includes usage from %s on.\n",
			harnessUsageRetentionDays, retained.Format("Jan 2"))
		start = retained
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
	params := managementapi.GetV1RoutesUsageParams{
		StartDate: new(start.Format(time.DateOnly)),
		EndDate:   new(end.Format(time.DateOnly)),
		GroupBy:   &[]managementapi.RouteUsageDimension{managementapi.RouteUsageDimension_MODEL},
		UserIds:   &[]string{me.UserId},
		Limit:     new(harnessUsageMaxBuckets),
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
		params = managementapi.GetV1RoutesUsageParams{Cursor: resp.Pagination.Cursor}
	}

	summary := summarizeHarnessUsage(byModel)
	if ctx.JSON {
		out := summary.output()
		out.Month = month.Format("2006-01")
		out.StartDate = start.Format(time.DateOnly)
		out.EndDate = end.Format(time.DateOnly)
		ctx.OutputJSON(out)
		return nil
	}
	monthName := month.Format("January 2006")
	if len(summary.entries) == 0 {
		ctx.Logf("No harness usage in %s.\n", monthName)
		// A team or workspace key authenticates as a service account, which
		// never creates routes keys, so its usage is always empty.
		if session, err := ctx.authInfo.Session(); err == nil && session.UsesAPIKey() {
			ctx.LogLine("If you're using a team or workspace API key, run 'baseten auth login' to see your own usage.")
		}
		return nil
	}
	through := ""
	if end.Before(month.AddDate(0, 1, 0)) {
		through = ", through " + today.Format("Jan 2")
	}
	ctx.Logf("Harness usage for %s (UTC%s). Numbers can lag by up to 15 minutes.\n\n", monthName, through)
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

// harnessUsageSummary is the month's usage, most expensive model first.
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
			item.CostUSD = new(formatHarnessCost(e.cost))
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
// visible.
func harnessMoney(r *big.Rat) string {
	f, _ := r.Float64()
	if f > 0 && f < 0.005 {
		return "<$0.01"
	}
	return billingFormatMoney(f)
}

func renderHarnessUsage(ctx *CommandContext, s harnessUsageSummary) {
	spend := harnessMoney(s.cost)
	if !s.priced {
		spend += " (excludes usage that couldn't be priced)"
	}
	ctx.Outputf("Spend   %s\n", spend)
	ctx.Outputf("Tokens  %s input (%s cached), %s output\n\n",
		harnessTokens(s.tokens.InputTokens),
		harnessTokens(s.tokens.CachedInputTokens),
		harnessTokens(s.tokens.OutputTokens))

	rows := make([][]string, 0, len(s.entries))
	for _, e := range s.entries {
		cost := "-"
		if e.priced {
			cost = harnessMoney(e.cost)
		}
		rows = append(rows, []string{
			cmp.Or(e.model, "(unknown)"),
			harnessTokens(e.tokens.InputTokens),
			harnessTokens(e.tokens.CachedInputTokens),
			harnessTokens(e.tokens.OutputTokens),
			cost,
		})
	}
	ctx.OutputTable(TableOutput{
		Headers:             []string{"MODEL", "INPUT", "CACHED", "OUTPUT", "COST"},
		Rows:                rows,
		RightAlignedColumns: []int{1, 2, 3, 4},
	})
}

// harnessTokens renders a token count compactly, like 62.2K or 1.7M.
func harnessTokens(n int64) string {
	if n < 1000 {
		return strconv.FormatInt(n, 10)
	}
	v, thousands := compactNumber(float64(n), 1)
	return strconv.FormatFloat(v, 'f', 1, 64) + []string{"", "K", "M", "B"}[thousands]
}
