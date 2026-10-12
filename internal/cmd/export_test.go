package cmd

import (
	"fmt"
	"time"
)

// RenderUsageChart exposes the chart renderer to tests, painting series i's
// characters as <i>…</i>.
func RenderUsageChart(first time.Time, series [][]float64, width int) string {
	return renderUsageChart(first, series, width, func(s string, i int) string { return fmt.Sprintf("<%d>%s</%d>", i, s, i) })
}
