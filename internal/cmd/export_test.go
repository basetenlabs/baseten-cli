package cmd

import "time"

// RenderUsageChart exposes the chart renderer to tests: one series per name,
// each with one value per day from first, painted by series index as <i>…</i>.
func RenderUsageChart(first time.Time, names []string, values [][]float64, width int) string {
	series := make([]usageChartSeries, len(names))
	for i := range names {
		series[i] = usageChartSeries{values: values[i]}
	}
	return renderUsageChart(first, series, width, func(s string, i int) string {
		return "<" + string(rune('0'+i)) + ">" + s + "</" + string(rune('0'+i)) + ">"
	})
}
