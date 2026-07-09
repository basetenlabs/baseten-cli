package cmd

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/basetenlabs/baseten-cli/cmd"
	"github.com/basetenlabs/baseten-go/client/managementapi"
)

func init() {
	Register("model deployment metrics", commandModelDeploymentMetrics)
}

func commandModelDeploymentMetrics(ctx *CommandContext, flags *cmd.ModelDeploymentMetricsFlags) error {
	api, err := ctx.NewManagementClient()
	if err != nil {
		return err
	}
	ref, err := ResolveDeploymentRef(ctx, api.API(), flags.ModelDeploymentIDFlags)
	if err != nil {
		return err
	}
	fetch := func(q metricsQuery) (*managementapi.GetModelMetricsResponse, error) {
		return api.API().GetModelsDeploymentsMetrics(ctx, ref.ModelID, ref.DeploymentID,
			managementapi.GetV1ModelsModelIdDeploymentsDeploymentIdMetricsParams{
				Mode:             &q.Mode,
				StartEpochMillis: q.StartEpochMillis,
				EndEpochMillis:   q.EndEpochMillis,
				Metrics:          q.Metrics,
			})
	}
	return runMetricsCommand(ctx, &flags.MetricsFlags, fetch)
}

// metricsQuery is the transport-neutral set of metric-query parameters shared by
// the deployment and environment metrics endpoints. Each command maps it onto
// its own generated query-params type.
type metricsQuery struct {
	Mode             managementapi.ModelMetricMode
	StartEpochMillis *int
	EndEpochMillis   *int
	Metrics          *[]string
}

// metricsFetcher fetches metrics for the given query.
type metricsFetcher func(q metricsQuery) (*managementapi.GetModelMetricsResponse, error)

// runMetricsCommand implements the shared metrics command flow for both
// deployment and environment metrics. It validates the flags, resolves the mode
// and time window into a metricsQuery, fetches via the supplied fetcher, and
// renders the response.
func runMetricsCommand(ctx *CommandContext, flags *cmd.MetricsFlags, fetch metricsFetcher) error {
	mode := managementapi.ModelMetricMode(strings.ToUpper(flags.Mode))

	hasStart := !flags.Start.IsZero()
	hasEnd := !flags.End.IsZero()
	// Use Changed rather than the zero value so explicit --since 0 fails the
	// positive-duration check below instead of being silently dropped.
	hasSince := ctx.Command.Flags().Changed("since")

	if mode == managementapi.ModelMetricMode_CURRENT && (hasStart || hasEnd || hasSince) {
		return cmd.NewErrUsagef("--start, --end, and --since are only valid with --mode summary or series")
	}
	if hasSince && (hasStart || hasEnd) {
		return cmd.NewErrUsagef("--since cannot be combined with --start or --end")
	}

	q := metricsQuery{Mode: mode}
	if hasSince {
		if flags.Since <= 0 {
			return cmd.NewErrUsagef("--since must be a positive duration")
		}
		if flags.Since > maxLogTimeRange {
			return cmd.NewErrUsagef("--since must be at most 7d")
		}
		now := ctx.Now()
		s := int(now.Add(-flags.Since).UnixMilli())
		e := int(now.UnixMilli())
		q.StartEpochMillis, q.EndEpochMillis = &s, &e
	} else if hasStart || hasEnd {
		// Send only the bounds given and leave the other nil so the server
		// backfills it. Validate the window client-side only when both are given.
		if hasStart {
			s := int(flags.Start.UnixMilli())
			q.StartEpochMillis = &s
		}
		if hasEnd {
			e := int(flags.End.UnixMilli())
			q.EndEpochMillis = &e
		}
		if hasStart && hasEnd {
			if !flags.Start.Before(flags.End) {
				return cmd.NewErrUsagef("--start must be earlier than --end")
			}
			if flags.End.Sub(flags.Start) > maxLogTimeRange {
				return cmd.NewErrUsagef("metrics time range must be at most 7 days; narrow --start/--end or use --since")
			}
		}
	}
	if len(flags.Metric) > 0 {
		q.Metrics = &flags.Metric
	}

	resp, err := fetch(q)
	if err != nil {
		return err
	}

	if ctx.JSON {
		ctx.OutputJSON(resp)
		return nil
	}

	if len(resp.MetricDescriptors) == 0 {
		ctx.LogLine("No metrics found.")
		return nil
	}

	// current is a point-in-time snapshot with no window. For the windowed modes,
	// report the resolved window (the server backfills a missing bound and it may
	// be historical) on stderr so it stays out of the table on stdout.
	if resp.Mode != managementapi.ModelMetricMode_CURRENT {
		ctx.LogLine(deploymentMetricsWindowLine(resp))
	}

	switch resp.Mode {
	case managementapi.ModelMetricMode_SERIES:
		if flags.NoChart {
			headers, rows, rightAligned := deploymentMetricsSeriesTable(resp)
			ctx.OutputTable(TableOutput{Headers: headers, Rows: rows, RightAlignedColumns: rightAligned})
			return nil
		}
		for _, line := range deploymentMetricsChartLines(resp) {
			ctx.OutputLine(line)
		}
		return nil
	case managementapi.ModelMetricMode_CURRENT, managementapi.ModelMetricMode_SUMMARY:
		headers, rows, rightAligned := deploymentMetricsSnapshotRows(resp)
		ctx.OutputTable(TableOutput{Headers: headers, Rows: rows, RightAlignedColumns: rightAligned})
		return nil
	default:
		return fmt.Errorf("unexpected metrics mode %q in response", resp.Mode)
	}
}

// deploymentMetricsWindowLine summarizes the returned window for summary/series
// modes: resolved start/end in local time plus duration, and the step interval
// when the server reports one (SERIES only).
func deploymentMetricsWindowLine(resp *managementapi.GetModelMetricsResponse) string {
	start := time.UnixMilli(int64(resp.StartEpochMillis)).Local()
	end := time.UnixMilli(int64(resp.EndEpochMillis)).Local()
	line := fmt.Sprintf("Window: %s – %s local (%s)",
		start.Format("2006-01-02 15:04"), end.Format("2006-01-02 15:04"),
		deploymentMetricsHumanizeDuration(end.Sub(start)))
	if resp.StepSeconds != nil {
		line += fmt.Sprintf(" · %s/step",
			deploymentMetricsHumanizeDuration(time.Duration(*resp.StepSeconds)*time.Second))
	}
	return line
}

// deploymentMetricsHumanizeDuration renders a duration compactly, omitting
// zero-valued units: whole days as "<N>d", otherwise non-zero h/m/s components
// (e.g. "30s", "10m", "1h30m").
func deploymentMetricsHumanizeDuration(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	if d%(24*time.Hour) == 0 {
		return fmt.Sprintf("%dd", d/(24*time.Hour))
	}
	var b strings.Builder
	if h := d / time.Hour; h > 0 {
		fmt.Fprintf(&b, "%dh", h)
		d -= h * time.Hour
	}
	if m := d / time.Minute; m > 0 {
		fmt.Fprintf(&b, "%dm", m)
		d -= m * time.Minute
	}
	if s := d / time.Second; s > 0 {
		fmt.Fprintf(&b, "%ds", s)
	}
	return b.String()
}

// deploymentMetricsSnapshotRows builds the current/summary table: one row per
// (metric, label set), with a column per label key seen across descriptors and
// a trailing VALUE column. In summary mode, COUNTER values render as
// "total (rate/s)" where the rate is the window total divided by its duration.
func deploymentMetricsSnapshotRows(resp *managementapi.GetModelMetricsResponse) (headers []string, rows [][]string, rightAligned []int) {
	labelKeys := deploymentMetricsLabelKeys(resp.MetricDescriptors)
	headers = append(headers, "METRIC")
	for _, k := range labelKeys {
		headers = append(headers, strings.ToUpper(k))
	}
	headers = append(headers, "VALUE")
	valueCol := len(headers) - 1
	rightAligned = []int{valueCol}

	windowSeconds := float64(resp.EndEpochMillis-resp.StartEpochMillis) / 1000
	var values *managementapi.ModelMetricValueSet
	if len(resp.MetricValues) > 0 {
		values = &resp.MetricValues[0]
	}

	for di, d := range resp.MetricDescriptors {
		for li, labelSet := range d.LabelSets {
			row := make([]string, len(headers))
			row[0] = d.Name
			for ki, k := range labelKeys {
				row[1+ki] = labelSet[k]
			}
			v := deploymentMetricsValueAt(values, di, li)
			if resp.Mode == managementapi.ModelMetricMode_SUMMARY &&
				d.Kind == managementapi.ModelMetricKind_COUNTER {
				row[valueCol] = deploymentMetricsFormatCounter(v, d.UnitHint, windowSeconds)
			} else {
				row[valueCol] = deploymentMetricsFormatValue(v, d.UnitHint)
			}
			rows = append(rows, row)
		}
	}
	return headers, rows, rightAligned
}

// deploymentMetricsChartLines renders the default series view: a header line per
// metric, then one sparkline per label set with its min-max range and the
// trailing ("end") value. The window may be historical, so the last point is
// labeled "end", not "now".
func deploymentMetricsChartLines(resp *managementapi.GetModelMetricsResponse) []string {
	var lines []string
	for di, d := range resp.MetricDescriptors {
		lines = append(lines, fmt.Sprintf("%s  [%s · %s]", d.Name, d.Kind, strings.ToLower(string(d.UnitHint))))

		shorts := make([]string, len(d.LabelSets))
		labelWidth := 0
		for li, labelSet := range d.LabelSets {
			shorts[li] = deploymentMetricsLabelShort(labelSet)
			if len(shorts[li]) > labelWidth {
				labelWidth = len(shorts[li])
			}
		}

		suffix := deploymentMetricsUnitSuffix(d.UnitHint)
		for li := range d.LabelSets {
			series := deploymentMetricsSeriesValues(resp.MetricValues, di, li)
			var b strings.Builder
			b.WriteString("  ")
			if labelWidth > 0 {
				fmt.Fprintf(&b, "%-*s  ", labelWidth, shorts[li])
			}
			b.WriteString(deploymentMetricsSparkline(series))
			if lo, hi, ok := deploymentMetricsRange(series); ok {
				fmt.Fprintf(&b, "   %s%s – %s%s",
					deploymentMetricsHumanize(lo), suffix, deploymentMetricsHumanize(hi), suffix)
			}
			if end := deploymentMetricsLastValue(series); end != nil {
				fmt.Fprintf(&b, "   end %s", deploymentMetricsFormatValue(end, d.UnitHint))
			}
			lines = append(lines, b.String())
		}
		lines = append(lines, "")
	}
	// Drop the trailing separator.
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// deploymentMetricsSeriesTable is the --no-chart fallback for series mode: one
// row per (metric, label set) with a value column per time step, headed by the
// step's local start time.
func deploymentMetricsSeriesTable(resp *managementapi.GetModelMetricsResponse) (headers []string, rows [][]string, rightAligned []int) {
	labelKeys := deploymentMetricsLabelKeys(resp.MetricDescriptors)
	headers = append(headers, "METRIC")
	for _, k := range labelKeys {
		headers = append(headers, strings.ToUpper(k))
	}
	firstStepCol := len(headers)
	for _, vs := range resp.MetricValues {
		headers = append(headers, time.UnixMilli(int64(vs.StartEpochMillis)).Local().Format("15:04"))
	}
	for col := firstStepCol; col < len(headers); col++ {
		rightAligned = append(rightAligned, col)
	}

	for di, d := range resp.MetricDescriptors {
		for li, labelSet := range d.LabelSets {
			row := make([]string, len(headers))
			row[0] = d.Name
			for ki, k := range labelKeys {
				row[1+ki] = labelSet[k]
			}
			for si := range resp.MetricValues {
				row[firstStepCol+si] = deploymentMetricsFormatValue(
					deploymentMetricsValueAt(&resp.MetricValues[si], di, li), d.UnitHint)
			}
			rows = append(rows, row)
		}
	}
	return headers, rows, rightAligned
}

// deploymentMetricsLabelKeys returns the union of label keys across descriptors
// in first-seen order, so the table has a stable column per label dimension.
func deploymentMetricsLabelKeys(descriptors []managementapi.ModelMetricDescriptor) []string {
	var keys []string
	seen := map[string]bool{}
	for _, d := range descriptors {
		for _, labelSet := range d.LabelSets {
			labelKeys := make([]string, 0, len(labelSet))
			for k := range labelSet {
				labelKeys = append(labelKeys, k)
			}
			slices.Sort(labelKeys)
			for _, k := range labelKeys {
				if !seen[k] {
					seen[k] = true
					keys = append(keys, k)
				}
			}
		}
	}
	return keys
}

// deploymentMetricsLabelShort renders a label set as a compact series label:
// quantiles as "p50"/"p99", a lone value as itself, otherwise sorted "k=v" pairs.
func deploymentMetricsLabelShort(labelSet map[string]string) string {
	if q, ok := labelSet["quantile"]; ok && len(labelSet) == 1 {
		if f, err := strconv.ParseFloat(q, 64); err == nil {
			return fmt.Sprintf("p%g", f*100)
		}
	}
	if len(labelSet) == 1 {
		for _, v := range labelSet {
			return v
		}
	}
	keys := make([]string, 0, len(labelSet))
	for k := range labelSet {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+labelSet[k])
	}
	return strings.Join(parts, ",")
}

// deploymentMetricsValueAt reads the value for descriptor di / label set li from
// a single value set, guarding the index-mapped slices. nil means no data.
func deploymentMetricsValueAt(values *managementapi.ModelMetricValueSet, di, li int) *float32 {
	if values == nil || di >= len(values.Values) || li >= len(values.Values[di]) {
		return nil
	}
	return values.Values[di][li]
}

// deploymentMetricsSeriesValues collects descriptor di / label set li across all
// time steps, preserving nil gaps.
func deploymentMetricsSeriesValues(steps []managementapi.ModelMetricValueSet, di, li int) []*float32 {
	series := make([]*float32, len(steps))
	for si := range steps {
		series[si] = deploymentMetricsValueAt(&steps[si], di, li)
	}
	return series
}

// deploymentMetricsLastValue returns the last non-nil point, or nil if none.
func deploymentMetricsLastValue(series []*float32) *float32 {
	for i := len(series) - 1; i >= 0; i-- {
		if series[i] != nil {
			return series[i]
		}
	}
	return nil
}

// deploymentMetricsRange returns the min and max of the non-nil points; ok is
// false when the series has no data.
func deploymentMetricsRange(series []*float32) (lo, hi float64, ok bool) {
	for _, v := range series {
		if v == nil {
			continue
		}
		f := float64(*v)
		if !ok {
			lo, hi, ok = f, f, true
			continue
		}
		lo = math.Min(lo, f)
		hi = math.Max(hi, f)
	}
	return lo, hi, ok
}

var deploymentMetricsSparkRunes = []rune("▁▂▃▄▅▆▇█")

// deploymentMetricsMaxSparkWidth caps the rune width of a sparkline. The backend
// step count is large and unbounded (a 6h window is ~360 steps), so longer series
// are downsampled to this many buckets to avoid terminal wrapping.
const deploymentMetricsMaxSparkWidth = 60

// deploymentMetricsNullRune marks a no-data point so leading gaps don't read as
// layout whitespace.
const deploymentMetricsNullRune = '·'

// deploymentMetricsDownsample aggregates series into at most max contiguous
// buckets, averaging each bucket's non-nil points. A bucket with no data stays
// nil so gaps are preserved. Series at or below max are returned unchanged.
func deploymentMetricsDownsample(series []*float32, max int) []*float32 {
	if len(series) <= max {
		return series
	}
	out := make([]*float32, max)
	for i := range out {
		start := i * len(series) / max
		end := (i + 1) * len(series) / max
		var sum float64
		var n int
		for _, v := range series[start:end] {
			if v != nil {
				sum += float64(*v)
				n++
			}
		}
		if n > 0 {
			avg := float32(sum / float64(n))
			out[i] = &avg
		}
	}
	return out
}

// deploymentMetricsSparkline renders a series as block-rune sparkline, scaled to
// the series' own min-max. Series wider than deploymentMetricsMaxSparkWidth are
// downsampled. nil points render as a gap; a flat series renders at the lowest
// level.
func deploymentMetricsSparkline(series []*float32) string {
	series = deploymentMetricsDownsample(series, deploymentMetricsMaxSparkWidth)
	lo, hi, ok := deploymentMetricsRange(series)
	if !ok {
		return "(no data)"
	}
	span := hi - lo
	var b strings.Builder
	for _, v := range series {
		if v == nil {
			b.WriteRune(deploymentMetricsNullRune)
			continue
		}
		idx := 0
		if span > 0 {
			idx = int((float64(*v)-lo)/span*float64(len(deploymentMetricsSparkRunes)-1) + 0.5)
			if idx < 0 {
				idx = 0
			}
			if idx >= len(deploymentMetricsSparkRunes) {
				idx = len(deploymentMetricsSparkRunes) - 1
			}
		}
		b.WriteRune(deploymentMetricsSparkRunes[idx])
	}
	return b.String()
}

// deploymentMetricsFormatValue formats a single value with its unit suffix; nil
// renders as "-".
func deploymentMetricsFormatValue(v *float32, unit managementapi.ModelMetricUnitHint) string {
	if v == nil {
		return "-"
	}
	return deploymentMetricsHumanize(float64(*v)) + deploymentMetricsUnitSuffix(unit)
}

// deploymentMetricsFormatCounter formats a COUNTER summary value as the window
// total plus its per-second rate, e.g. "1.2k (340.5/s)".
func deploymentMetricsFormatCounter(total *float32, unit managementapi.ModelMetricUnitHint, windowSeconds float64) string {
	if total == nil {
		return "-"
	}
	t := float64(*total)
	out := deploymentMetricsHumanize(t) + deploymentMetricsUnitSuffix(unit)
	if windowSeconds > 0 {
		out += fmt.Sprintf(" (%s/s)", deploymentMetricsHumanize(t/windowSeconds))
	}
	return out
}

// deploymentMetricsUnitSuffix is the short value suffix for a unit hint. COUNT
// and RATIO are dimensionless and carry no suffix; unknown hints are omitted.
func deploymentMetricsUnitSuffix(unit managementapi.ModelMetricUnitHint) string {
	switch unit {
	case managementapi.ModelMetricUnitHint_SECONDS:
		return "s"
	case managementapi.ModelMetricUnitHint_PER_SECOND:
		return "/s"
	case managementapi.ModelMetricUnitHint_BYTES:
		return "B"
	case managementapi.ModelMetricUnitHint_MEBIBYTES:
		return "MiB"
	case managementapi.ModelMetricUnitHint_COUNT, managementapi.ModelMetricUnitHint_RATIO:
		return ""
	default:
		return ""
	}
}

// deploymentMetricsHumanize renders a number compactly with a k/M/G suffix for
// large magnitudes, trimming trailing zeros. Display-only; not exact.
func deploymentMetricsHumanize(f float64) string {
	abs := math.Abs(f)
	switch {
	case abs >= 1e9:
		return deploymentMetricsTrim(f/1e9) + "G"
	case abs >= 1e6:
		return deploymentMetricsTrim(f/1e6) + "M"
	case abs >= 1e3:
		return deploymentMetricsTrim(f/1e3) + "k"
	default:
		return deploymentMetricsTrim(f)
	}
}

func deploymentMetricsTrim(f float64) string {
	s := strconv.FormatFloat(f, 'f', 3, 64)
	if strings.Contains(s, ".") {
		s = strings.TrimRight(s, "0")
		s = strings.TrimRight(s, ".")
	}
	if s == "-0" {
		s = "0"
	}
	return s
}
