package cmd

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-go/client/managementapi"
)

func init() {
	Register("model-api usage", commandModelAPIUsage)
}

// modelAPIUsageDimensions are the allowed lowercase-kebab --group-by values,
// mapped to the snake_case backend enum by swapping '-' for '_'.
var modelAPIUsageDimensions = []string{"api-key", "user", "model"}

// modelAPIUsageDefaultDimension is pinned client-side so the column set stays
// stable if the backend changes which dimension it defaults to.
const modelAPIUsageDefaultDimension = managementapi.UsageDimension_model

func commandModelAPIUsage(ctx *CommandContext, flags *cmd.ModelAPIUsageFlags) error {
	api, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}

	width := managementapi.BucketWidth(flags.BucketWidth)
	bucket, maxPageSize := modelAPIUsageWidth(width)
	if flags.Limit < 0 {
		return cmd.NewErrUsagef("--limit must be zero (no limit) or a positive number")
	}
	// A page holds at most maxPageSize buckets; asking for more would be clamped
	// server-side, making a full page look short and ending pagination early.
	pageSize := flags.PageSize
	if pageSize == 0 {
		pageSize = maxPageSize
	} else if pageSize < 1 || pageSize > maxPageSize {
		return cmd.NewErrUsagef("--page-size must be between 1 and %d for --bucket-width %s", maxPageSize, width)
	}

	dims, err := modelAPIUsageGroupBy(flags.GroupBy)
	if err != nil {
		return err
	}

	start, end, err := modelAPIUsageWindow(ctx, flags, bucket)
	if err != nil {
		return err
	}

	params := managementapi.GetV1ModelApisUsageParams{
		StartTime:   &start,
		EndTime:     &end,
		BucketWidth: &width,
		GroupBy:     &dims,
	}
	if len(flags.APIKeyPrefixes) > 0 {
		params.ApiKeys = &flags.APIKeyPrefixes
	}
	if len(flags.UserIDs) > 0 {
		params.UserIds = &flags.UserIDs
	}
	if len(flags.Models) > 0 {
		params.Models = &flags.Models
	}

	var jw *JSONArrayWriter
	if ctx.JSON {
		jw = ctx.NewJSONArrayWriter()
		defer jw.Close()
	}

	table := newModelAPIUsageTable(width, dims)
	remaining := flags.Limit
	hitLimit := false
buckets:
	for {
		params.Limit = &pageSize
		resp, err := api.API().GetModelApisUsage(ctx, params)
		if err != nil {
			return fmt.Errorf("getting Model APIs usage: %w", err)
		}
		for i := range resp.Items {
			if ctx.JSON {
				jw.Write(resp.Items[i])
			} else {
				table.addBucket(resp.Items[i])
			}
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
	// Every bucket in the window can come back empty, which is a table of
	// nothing but gap markers. Say it instead of drawing it.
	if table.dataRows == 0 {
		ctx.LogLine("No usage in the selected window.")
		return nil
	}
	ctx.LogLine(table.windowLine())
	if table.sawNullUser {
		ctx.LogLine("Rows with no user are usage from workspace or other non-user-scoped " +
			"credentials, or from before user attribution was recorded.")
	}
	headers, rows, rightAligned := table.render()
	ctx.OutputTable(TableOutput{Headers: headers, Rows: rows, RightAlignedColumns: rightAligned})
	if hitLimit {
		ctx.Logf("Reached the --limit of %d buckets; more exist. Increase --limit or use --limit 0 for no limit.\n", flags.Limit)
	}
	return nil
}

// modelAPIUsageWidth returns the duration of one bucket and the backend's
// maximum buckets per page for a bucket width.
func modelAPIUsageWidth(width managementapi.BucketWidth) (bucket time.Duration, maxPageSize int) {
	switch width {
	case managementapi.BucketWidth_1m:
		return time.Minute, 1440
	case managementapi.BucketWidth_1h:
		return time.Hour, 168
	default:
		return 24 * time.Hour, 31
	}
}

// modelAPIUsageGroupBy validates the --group-by values and maps them onto the
// backend dimension enum, defaulting to a single dimension when unset.
func modelAPIUsageGroupBy(values []string) ([]managementapi.UsageDimension, error) {
	if len(values) == 0 {
		return []managementapi.UsageDimension{modelAPIUsageDefaultDimension}, nil
	}
	dims := make([]managementapi.UsageDimension, 0, len(values))
	for _, v := range values {
		if !slices.Contains(modelAPIUsageDimensions, v) {
			return nil, cmd.NewErrUsagef("invalid --group-by %q; must be one of: %s",
				v, strings.Join(modelAPIUsageDimensions, ", "))
		}
		dim := managementapi.UsageDimension(strings.ReplaceAll(v, "-", "_"))
		if !slices.Contains(dims, dim) {
			dims = append(dims, dim)
		}
	}
	return dims, nil
}

// modelAPIUsageWindow resolves the query range from --start/--end/--since. With
// none of them set the window spans the bucket width's default bucket count,
// matching what the endpoint returns for a single default page.
func modelAPIUsageWindow(ctx *CommandContext, flags *cmd.ModelAPIUsageFlags, bucket time.Duration) (start, end time.Time, err error) {
	hasStart := !flags.Start.IsZero()
	hasEnd := !flags.End.IsZero()
	// Use Changed rather than the zero value so an explicit --since 0 fails the
	// positive-duration check below instead of being silently dropped.
	hasSince := ctx.Command.Flags().Changed("since")
	if hasSince && (hasStart || hasEnd) {
		return start, end, cmd.NewErrUsagef("--since cannot be combined with --start or --end")
	}

	now := ctx.Now()
	switch {
	case hasSince:
		if flags.Since <= 0 {
			return start, end, cmd.NewErrUsagef("--since must be a positive duration")
		}
		return now.Add(-flags.Since), now, nil
	case hasStart && hasEnd:
		if !flags.Start.Before(flags.End) {
			return start, end, cmd.NewErrUsagef("--start must be earlier than --end")
		}
		return flags.Start, flags.End, nil
	case hasStart:
		// --end defaults to now, so name that rather than a flag the user never
		// passed.
		if !flags.Start.Before(now) {
			return start, end, cmd.NewErrUsagef("--start must be in the past when --end is omitted")
		}
		return flags.Start, now, nil
	case hasEnd:
		// start_time is required on the first page, so an --end-only window still
		// needs a start; span the same default bucket count back from --end.
		return flags.End.Add(-bucket * time.Duration(modelAPIUsageDefaultBuckets(bucket))), flags.End, nil
	default:
		return now.Add(-bucket * time.Duration(modelAPIUsageDefaultBuckets(bucket))), now, nil
	}
}

// modelAPIUsageDefaultBuckets is the backend's default bucket count for a
// bucket width, used to size an unspecified window.
func modelAPIUsageDefaultBuckets(bucket time.Duration) int {
	switch bucket {
	case time.Minute:
		return 60
	case time.Hour:
		return 24
	default:
		return 7
	}
}

// windowLine summarizes the returned series on stderr. The bounds come from the
// buckets themselves rather than the requested window, which the backend snaps
// down to a bucket boundary.
func (t *modelAPIUsageTable) windowLine() string {
	names := make([]string, len(t.dims))
	for i, d := range t.dims {
		names[i] = string(d)
	}
	return fmt.Sprintf("Window: %s through %s UTC · %d buckets of %s · grouped by %s",
		t.timestamp(t.firstStart), t.timestamp(t.lastStart), t.bucketCount, t.width,
		strings.Join(names, ", "))
}

// modelAPIUsageTable accumulates one row per bucket-and-dimension-combination,
// tracking column totals for the trailing ALL row.
type modelAPIUsageTable struct {
	width managementapi.BucketWidth
	dims  []managementapi.UsageDimension
	rows  [][]string

	firstStart  time.Time
	lastStart   time.Time
	bucketCount int
	totals      modelAPIUsageTotals
	dataRows    int
	sawNullUser bool
}

type modelAPIUsageTotals struct {
	requests int64
	input    int64
	cached   int64
	output   int64
}

func newModelAPIUsageTable(width managementapi.BucketWidth, dims []managementapi.UsageDimension) *modelAPIUsageTable {
	return &modelAPIUsageTable{width: width, dims: dims}
}

// addBucket appends this bucket's result rows. An empty bucket still gets a row
// so gaps in the series stay visible.
func (t *modelAPIUsageTable) addBucket(b managementapi.ModelApisUsageBucket) {
	if t.bucketCount == 0 {
		t.firstStart = b.StartTime
	}
	t.lastStart = b.StartTime
	t.bucketCount++

	stamp := t.timestamp(b.StartTime)
	if b.Results == nil || len(*b.Results) == 0 {
		row := make([]string, 1+len(t.dims)+4)
		row[0] = stamp
		row[1] = "(no usage)"
		t.rows = append(t.rows, row)
		return
	}
	for _, r := range *b.Results {
		row := make([]string, 0, 1+len(t.dims)+4)
		row = append(row, stamp)
		for _, d := range t.dims {
			row = append(row, t.dimensionCell(d, r))
		}
		row = append(row,
			billingGroupDigits(strconv.Itoa(r.RequestCount)),
			billingGroupDigits(strconv.Itoa(r.InputTokens)),
			billingGroupDigits(strconv.Itoa(r.CachedInputTokens)),
			billingGroupDigits(strconv.Itoa(r.OutputTokens)),
		)
		t.rows = append(t.rows, row)
		t.dataRows++
		t.totals.requests += int64(r.RequestCount)
		t.totals.input += int64(r.InputTokens)
		t.totals.cached += int64(r.CachedInputTokens)
		t.totals.output += int64(r.OutputTokens)
		// Only the row's own bucket stamp repeats; blank it so a bucket's
		// several rows read as one group.
		stamp = ""
	}
}

// dimensionCell renders one grouped dimension. An absent value is real data, not
// a missing field: usage with no user attribution, for instance. The backend
// reports absence as either null or an empty string depending on the dimension.
func (t *modelAPIUsageTable) dimensionCell(d managementapi.UsageDimension, r managementapi.ModelApisUsageResult) string {
	switch d {
	case managementapi.UsageDimension_api_key:
		return modelAPIUsageCell(r.ApiKeyPrefix)
	case managementapi.UsageDimension_user:
		cell := modelAPIUsageCell(r.UserId)
		if cell == "-" {
			t.sawNullUser = true
		}
		return cell
	case managementapi.UsageDimension_model:
		return modelAPIUsageCell(r.Model)
	default:
		return "-"
	}
}

func modelAPIUsageCell(v *string) string {
	if v == nil || *v == "" {
		return "-"
	}
	return *v
}

// timestamp renders a bucket start, dropping the clock for daily buckets where
// it is always midnight. Buckets are aligned to UTC, so rendering them in local
// time would shift a daily bucket onto the wrong calendar date.
func (t *modelAPIUsageTable) timestamp(start time.Time) string {
	if t.width == managementapi.BucketWidth_1d {
		return start.UTC().Format("2006-01-02")
	}
	return start.UTC().Format("2006-01-02 15:04")
}

func (t *modelAPIUsageTable) render() (headers []string, rows [][]string, rightAligned []int) {
	timeHeader := "TIME"
	if t.width == managementapi.BucketWidth_1d {
		timeHeader = "DATE"
	}
	headers = append(headers, timeHeader)
	for _, d := range t.dims {
		headers = append(headers, strings.ToUpper(strings.ReplaceAll(string(d), "_", " ")))
	}
	headers = append(headers, "REQUESTS", "INPUT", "CACHED", "OUTPUT")
	for col := 1 + len(t.dims); col < len(headers); col++ {
		rightAligned = append(rightAligned, col)
	}

	rows = t.rows
	// A totals row only earns its keep once there is more than one data row to
	// total; with a single row it would just repeat it.
	if t.dataRows > 1 {
		total := make([]string, 0, len(headers))
		total = append(total, "ALL")
		for range t.dims {
			total = append(total, "")
		}
		total = append(total,
			billingGroupDigits(strconv.FormatInt(t.totals.requests, 10)),
			billingGroupDigits(strconv.FormatInt(t.totals.input, 10)),
			billingGroupDigits(strconv.FormatInt(t.totals.cached, 10)),
			billingGroupDigits(strconv.FormatInt(t.totals.output, 10)),
		)
		rows = append(rows, total)
	}
	return headers, rows, rightAligned
}
