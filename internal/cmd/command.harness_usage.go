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

// harnessUsageMaxBuckets is the most daily buckets the endpoint returns per
// page, which covers any month.
const harnessUsageMaxBuckets = 31

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
		GroupBy:   &[]managementapi.RouteUsageDimension{managementapi.RouteUsageDimension_MODEL},
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
				if err := agg.add(r); err != nil {
					return err
				}
			}
		}
		if !resp.Pagination.HasMore || resp.Pagination.Cursor == nil {
			break
		}
		params = managementapi.GetV1RoutesUsageParams{Cursor: resp.Pagination.Cursor}
	}

	out := agg.result()
	out.Month = month.Format("2006-01")
	out.StartDate = month.Format(time.DateOnly)
	out.EndDate = end.Format(time.DateOnly)
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
	ctx.Logf("Harness usage for %s (UTC%s). Numbers can lag by up to 15 minutes.\n\n", monthName, through)
	renderHarnessUsage(ctx, out)
	return nil
}

type harnessUsageEntry struct {
	item cmd.HarnessUsageItem
	cost *big.Rat
}

// harnessUsageAggregate sums daily results into one entry per model, adding
// costs as exact decimals.
type harnessUsageAggregate struct {
	entries map[string]*harnessUsageEntry
	order   []string
}

func newHarnessUsageAggregate() *harnessUsageAggregate {
	return &harnessUsageAggregate{entries: map[string]*harnessUsageEntry{}}
}

func (a *harnessUsageAggregate) add(r managementapi.RoutesUsageResult) error {
	cost, ok := new(big.Rat).SetString(r.CostUsd)
	if !ok {
		return fmt.Errorf("routes usage returned an invalid cost %q", r.CostUsd)
	}
	key := deref(r.Model)
	e, ok := a.entries[key]
	if !ok {
		e = &harnessUsageEntry{item: cmd.HarnessUsageItem{Model: key}, cost: new(big.Rat)}
		a.entries[key] = e
		a.order = append(a.order, key)
	}
	e.cost.Add(e.cost, cost)
	e.item.InputTokens += int64(r.InputTokens)
	e.item.CachedInputTokens += int64(r.CachedInputTokens)
	e.item.UncachedInputTokens += int64(r.UncachedInputTokens)
	e.item.OutputTokens += int64(r.OutputTokens)
	return nil
}

func (a *harnessUsageAggregate) result() cmd.HarnessUsage {
	entries := make([]*harnessUsageEntry, 0, len(a.order))
	for _, key := range a.order {
		// Skip models the API reports with no tokens and no cost.
		if e := a.entries[key]; e.cost.Sign() != 0 || e.item.InputTokens+e.item.OutputTokens != 0 {
			entries = append(entries, e)
		}
	}
	// Most expensive first.
	slices.SortStableFunc(entries, func(x, y *harnessUsageEntry) int {
		if c := y.cost.Cmp(x.cost); c != 0 {
			return c
		}
		return cmp.Compare(x.item.Model, y.item.Model)
	})
	out := cmd.HarnessUsage{Items: make([]cmd.HarnessUsageItem, 0, len(entries))}
	total := new(big.Rat)
	for _, e := range entries {
		total.Add(total, e.cost)
		e.item.CostUSD = formatHarnessCost(e.cost)
		t := &out.Totals.HarnessUsageTokens
		t.InputTokens += e.item.InputTokens
		t.CachedInputTokens += e.item.CachedInputTokens
		t.UncachedInputTokens += e.item.UncachedInputTokens
		t.OutputTokens += e.item.OutputTokens
		out.Items = append(out.Items, e.item)
	}
	out.Totals.CostUSD = formatHarnessCost(total)
	return out
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

func renderHarnessUsage(ctx *CommandContext, u cmd.HarnessUsage) {
	t := u.Totals
	ctx.Outputf("Spend   %s\n", harnessMoney(t.CostUSD))
	ctx.Outputf("Tokens  %s input (%s cached), %s output\n\n",
		harnessTokens(t.InputTokens),
		harnessTokens(t.CachedInputTokens),
		harnessTokens(t.OutputTokens))

	rows := make([][]string, 0, len(u.Items))
	for _, item := range u.Items {
		rows = append(rows, []string{
			cmp.Or(item.Model, "(unknown)"),
			harnessTokens(item.InputTokens),
			harnessTokens(item.CachedInputTokens),
			harnessTokens(item.OutputTokens),
			harnessMoney(item.CostUSD),
		})
	}
	ctx.OutputTable(TableOutput{
		Headers:             []string{"MODEL", "INPUT", "CACHED", "OUTPUT", "COST"},
		Rows:                rows,
		RightAlignedColumns: []int{1, 2, 3, 4},
	})
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
