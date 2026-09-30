package cmd

import (
	"slices"
	"strings"
	"time"

	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-go/client/managementapi"
)

func init() {
	Register("harness usage", commandHarnessUsage)
}

// commandHarnessUsage is route usage preset for checking your own monthly
// spend: your usage, month to date, totaled per group.
func commandHarnessUsage(ctx *CommandContext, f *cmd.HarnessUsageFlags) error {
	return runRoutesUsage(ctx, routesUsageRequest{
		start:   f.Start,
		end:     f.End,
		since:   f.Since,
		userIDs: f.UserIDs,
		query:   f.RouteUsageQueryFlags,
		// Month to date by default, the period spend limits apply to.
		defaultStart: func(end time.Time) time.Time {
			end = end.UTC()
			return time.Date(end.Year(), end.Month(), 1, 0, 0, 0, 0, time.UTC)
		},
		// Spend limits are per user, and admins would otherwise see the whole
		// organization's usage.
		defaultToCaller: true,
		newTable: func(dims []managementapi.RouteUsageDimension) routesUsageTable {
			return &harnessUsageTotals{dims: dims, byKey: map[string]*harnessUsageRow{}}
		},
	})
}

// harnessUsageRow is one --group-by combination's usage, summed across days.
type harnessUsageRow struct {
	key []string
	*routesUsageTotal
}

// harnessUsageTotals renders one row per --group-by combination, totaled over
// the window, most expensive first, with a trailing ALL row.
type harnessUsageTotals struct {
	dims  []managementapi.RouteUsageDimension
	byKey map[string]*harnessUsageRow
	// sorted and all are computed once from byKey, after the last bucket.
	sorted []*harnessUsageRow
	all    *routesUsageTotal
}

func (t *harnessUsageTotals) addBucket(b managementapi.RoutesUsageBucket) error {
	for _, r := range b.Results {
		usage, err := routesUsageResultTotal(r)
		if err != nil {
			return err
		}
		key := make([]string, len(t.dims))
		for i, d := range t.dims {
			key[i] = routesUsageDimensionCell(d, r)
		}
		id := strings.Join(key, "\x00")
		row, ok := t.byKey[id]
		if !ok {
			row = &harnessUsageRow{key: key, routesUsageTotal: newRoutesUsageTotal()}
			t.byKey[id] = row
		}
		row.add(usage)
	}
	return nil
}

// rows returns every combination with usage, most expensive first. Requests
// that produced no tokens, such as failed ones, cost nothing and would only
// add empty rows.
func (t *harnessUsageTotals) rows() []*harnessUsageRow {
	if t.all != nil {
		return t.sorted
	}
	t.all = newRoutesUsageTotal()
	for _, r := range t.byKey {
		if r.cost.Sign() == 0 && r.input+r.output == 0 {
			continue
		}
		t.sorted = append(t.sorted, r)
		t.all.add(r.routesUsageTotal)
	}
	slices.SortFunc(t.sorted, func(x, y *harnessUsageRow) int {
		if c := y.cost.Cmp(x.cost); c != 0 {
			return c
		}
		return slices.Compare(x.key, y.key)
	})
	return t.sorted
}

func (t *harnessUsageTotals) empty() bool { return len(t.rows()) == 0 }

func (t *harnessUsageTotals) unpriced() bool {
	t.rows()
	return !t.all.priced
}

func (t *harnessUsageTotals) render(ctx *CommandContext) {
	headers, rightAligned := routesUsageHeaders(nil, t.dims)
	rows := make([][]string, 0, len(t.rows())+1)
	for _, r := range t.rows() {
		rows = append(rows, append(slices.Clone(r.key), r.cells()...))
	}
	// A totals row only earns its keep once there is more than one row to
	// total; with a single row it would just repeat it.
	if len(t.rows()) > 1 {
		key := make([]string, len(t.dims))
		key[0] = "ALL"
		rows = append(rows, append(key, t.all.cells()...))
	}
	ctx.OutputTable(TableOutput{Headers: headers, Rows: rows, RightAlignedColumns: rightAligned})
}
