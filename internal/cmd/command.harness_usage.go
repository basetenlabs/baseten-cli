package cmd

import (
	"cmp"
	"fmt"
	"math"
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

// harnessUsageDimensions maps --group-by onto the backend dimensions. Models
// carry their provider, since one model name can be served by several.
var harnessUsageDimensions = map[string][]managementapi.RouteUsageDimension{
	"model":    {managementapi.RouteUsageDimension_MODEL, managementapi.RouteUsageDimension_PROVIDER},
	"route":    {managementapi.RouteUsageDimension_ROUTE},
	"provider": {managementapi.RouteUsageDimension_PROVIDER},
}

// harnessUsageMaxBuckets is the most daily buckets the endpoint returns per
// page, which covers any month.
const harnessUsageMaxBuckets = 31

func commandHarnessUsage(ctx *CommandContext, f *cmd.HarnessUsageFlags) error {
	groupBy := cmp.Or(f.GroupBy, "model")
	dims, ok := harnessUsageDimensions[groupBy]
	if !ok {
		return cmd.NewErrUsagef("invalid --group-by %q; must be one of: model, route, provider", f.GroupBy)
	}
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
		StartDate: new(month.Format(time.DateOnly)),
		EndDate:   new(end.Format(time.DateOnly)),
		GroupBy:   &dims,
		UserIds:   &[]string{me.UserId},
		Limit:     new(harnessUsageMaxBuckets),
	}
	agg := newHarnessUsageAggregate()
	for {
		resp, err := api.GetRoutesUsage(ctx, params)
		if err != nil {
			return fmt.Errorf("getting routes usage: %w", err)
		}
		for _, bucket := range resp.Items {
			for _, r := range bucket.Results {
				agg.add(r)
			}
		}
		if !resp.Pagination.HasMore || resp.Pagination.Cursor == nil {
			break
		}
		params = managementapi.GetV1RoutesUsageParams{Cursor: resp.Pagination.Cursor}
	}

	out := agg.result()
	if groupBy == "route" {
		nameHarnessUsageRoutes(ctx, api, out.Items)
	}
	out.Month = month.Format("2006-01")
	out.StartDate = month.Format(time.DateOnly)
	out.EndDate = end.Format(time.DateOnly)
	out.GroupBy = groupBy
	if ctx.JSON {
		ctx.OutputJSON(out)
		return nil
	}
	monthName := month.Format("January 2006")
	if len(out.Items) == 0 {
		ctx.Logf("No harness usage in %s.\n", monthName)
		return nil
	}
	through := ""
	if end.Before(month.AddDate(0, 1, 0)) {
		through = ", through " + today.Format("Jan 2")
	}
	ctx.Logf("Harness usage for %s (UTC%s)\n\n", monthName, through)
	renderHarnessUsage(ctx, out, dims)
	return nil
}

// harnessUsageKey identifies one --group-by value across daily buckets.
type harnessUsageKey struct {
	model, provider, routeID string
}

type harnessUsageEntry struct {
	item cmd.HarnessUsageItem
	cost *big.Rat
	// priced is false once any of the entry's usage could not be priced.
	priced bool
}

// harnessUsageAggregate sums daily results into one entry per group, adding
// costs as exact decimals.
type harnessUsageAggregate struct {
	entries map[harnessUsageKey]*harnessUsageEntry
	order   []harnessUsageKey
}

func newHarnessUsageAggregate() *harnessUsageAggregate {
	return &harnessUsageAggregate{entries: map[harnessUsageKey]*harnessUsageEntry{}}
}

func (a *harnessUsageAggregate) add(r managementapi.RoutesUsageResult) {
	var provider *string
	if r.Provider != nil {
		provider = new(string(*r.Provider))
	}
	key := harnessUsageKey{model: deref(r.Model), provider: deref(provider), routeID: deref(r.RouteId)}
	e, ok := a.entries[key]
	if !ok {
		e = &harnessUsageEntry{
			item:   cmd.HarnessUsageItem{Model: r.Model, Provider: provider, RouteID: r.RouteId, RouteName: r.RouteName},
			cost:   new(big.Rat),
			priced: true,
		}
		a.entries[key] = e
		a.order = append(a.order, key)
	}
	e.item.RequestCount += int64(r.RequestCount)
	e.item.InputTokens += int64(r.InputTokens)
	e.item.CachedInputTokens += int64(r.CachedInputTokens)
	e.item.UncachedInputTokens += int64(r.UncachedInputTokens)
	e.item.OutputTokens += int64(r.OutputTokens)
	cost, ok := parseHarnessCost(r.CostUsd)
	if !ok {
		e.priced = false
		return
	}
	e.cost.Add(e.cost, cost)
}

func (a *harnessUsageAggregate) result() cmd.HarnessUsage {
	out := cmd.HarnessUsage{Items: []cmd.HarnessUsageItem{}, Totals: cmd.HarnessUsageTotals{CostComplete: true}}
	total := new(big.Rat)
	for _, key := range a.order {
		e := a.entries[key]
		total.Add(total, e.cost)
		if e.priced {
			e.item.CostUSD = new(formatHarnessCost(e.cost))
		} else {
			out.Totals.CostComplete = false
		}
		t := &out.Totals.HarnessUsageCounts
		t.RequestCount += e.item.RequestCount
		t.InputTokens += e.item.InputTokens
		t.CachedInputTokens += e.item.CachedInputTokens
		t.UncachedInputTokens += e.item.UncachedInputTokens
		t.OutputTokens += e.item.OutputTokens
		out.Items = append(out.Items, e.item)
	}
	out.Totals.CostUSD = formatHarnessCost(total)
	// Most expensive first; unpriced items follow, busiest first.
	slices.SortStableFunc(out.Items, func(x, y cmd.HarnessUsageItem) int {
		if c := cmp.Compare(harnessCostOrder(y), harnessCostOrder(x)); c != 0 {
			return c
		}
		return cmp.Compare(y.RequestCount, x.RequestCount)
	})
	return out
}

// harnessCostOrder sorts an item by cost, with unpriced items below every
// priced one.
func harnessCostOrder(item cmd.HarnessUsageItem) float64 {
	cost, ok := parseHarnessCost(item.CostUSD)
	if !ok {
		return -1
	}
	f, _ := cost.Float64()
	return f
}

func parseHarnessCost(s *string) (*big.Rat, bool) {
	if s == nil {
		return nil, false
	}
	return new(big.Rat).SetString(*s)
}

// formatHarnessCost renders an exact decimal without trailing zeros. Costs
// arrive with at most 10 decimal places, so sums of them fit too.
func formatHarnessCost(r *big.Rat) string {
	s := r.FloatString(10)
	s = strings.TrimRight(s, "0")
	return strings.TrimSuffix(s, ".")
}

// harnessMoney renders a cost string in dollars and cents, keeping tiny
// nonzero costs visible.
func harnessMoney(s string) string {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		return "-"
	}
	f, _ := r.Float64()
	if f > 0 && f < 0.005 {
		return "<$0.01"
	}
	return billingFormatMoney(f)
}

func renderHarnessUsage(ctx *CommandContext, u cmd.HarnessUsage, dims []managementapi.RouteUsageDimension) {
	t := u.Totals
	spend := harnessMoney(t.CostUSD)
	if !t.CostComplete {
		spend += " (some usage could not be priced and is not included)"
	}
	ctx.Outputf("Spend     %s\n", spend)
	ctx.Outputf("Requests  %s\n", billingGroupDigits(strconv.FormatInt(t.RequestCount, 10)))
	ctx.Outputf("Tokens    %s input (%s cached), %s output\n\n",
		harnessTokens(t.InputTokens),
		harnessTokens(t.CachedInputTokens),
		harnessTokens(t.OutputTokens))

	var headers []string
	for _, d := range dims {
		headers = append(headers, string(d))
	}
	first := len(headers)
	headers = append(headers, "REQUESTS", "INPUT", "CACHED", "OUTPUT", "COST")
	var right []int
	for col := first; col < len(headers); col++ {
		right = append(right, col)
	}
	rows := make([][]string, 0, len(u.Items))
	for _, item := range u.Items {
		var row []string
		for _, d := range dims {
			switch d {
			case managementapi.RouteUsageDimension_MODEL:
				row = append(row, cmp.Or(deref(item.Model), "(unknown)"))
			case managementapi.RouteUsageDimension_PROVIDER:
				row = append(row, harnessProviderLabel(item.Provider))
			case managementapi.RouteUsageDimension_ROUTE:
				row = append(row, cmp.Or(deref(item.RouteDisplayName), deref(item.RouteName), deref(item.RouteID), "(unknown)"))
			}
		}
		cost := "-"
		if item.CostUSD != nil {
			cost = harnessMoney(*item.CostUSD)
		}
		row = append(row,
			billingGroupDigits(strconv.FormatInt(item.RequestCount, 10)),
			harnessTokens(item.InputTokens),
			harnessTokens(item.CachedInputTokens),
			harnessTokens(item.OutputTokens),
			cost,
		)
		rows = append(rows, row)
	}
	ctx.OutputTable(TableOutput{Headers: headers, Rows: rows, RightAlignedColumns: right})
}

// harnessProviderLabel renders a provider enum like OPENAI_COMPATIBLE as
// openai-compatible.
func harnessProviderLabel(p *string) string {
	if p == nil {
		return "(unknown)"
	}
	return strings.ToLower(strings.ReplaceAll(*p, "_", "-"))
}

// nameHarnessUsageRoutes fills in route display names, which usage doesn't
// carry. Deleted routes keep only their name.
func nameHarnessUsageRoutes(ctx *CommandContext, api *managementapi.Client, items []cmd.HarnessUsageItem) {
	routes, err := listRoutes(ctx, api, managementapi.GetV1RoutesParams{})
	if err != nil {
		ctx.VerboseLogf("warning: showing route names instead of display names: %v\n", err)
		return
	}
	displayNames := make(map[string]string, len(routes))
	for _, r := range routes {
		displayNames[r.Id] = r.DisplayName
	}
	for i := range items {
		if name := displayNames[deref(items[i].RouteID)]; name != "" {
			items[i].RouteDisplayName = &name
		}
	}
}

// harnessTokens renders a token count compactly, like 1.7M or 62.2K.
func harnessTokens(n int64) string {
	if n < 1000 {
		return strconv.FormatInt(n, 10)
	}
	v := float64(n)
	suffixes := []string{"K", "M", "B"}
	i := 0
	v /= 1e3
	// Move up a unit when rounding would print 1000.0K rather than 1.0M.
	for math.Round(v*10)/10 >= 1000 && i < len(suffixes)-1 {
		v /= 1e3
		i++
	}
	return strconv.FormatFloat(v, 'f', 1, 64) + suffixes[i]
}
