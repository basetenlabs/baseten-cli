package cmd

import (
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
)

// usageChartSeries is one line of a usage chart: one value per day.
type usageChartSeries struct {
	values []float64
}

// usageChartHeight is the most rows above the chart's zero row. There is at
// most one per dollar, so the whole-dollar labels never repeat.
const usageChartHeight = 7

// usageChartWidth is the columns the plot aims for, so it isn't spread across
// a wide terminal; a full month at two columns a day runs a little past it.
const usageChartWidth = 48

// usageChartColors are the colors of the series, in order.
var usageChartColors = []ansi.BasicColor{ansi.Blue, ansi.Green, ansi.Magenta, ansi.Cyan, ansi.Yellow, ansi.Red}

// renderUsageChart renders series, each with one dollar amount per day from
// first, as an asciichart-style step chart with a y-axis in whole dollars and
// date labels below, fitting width columns, or "" when two columns per day
// don't fit. paint colors a series' characters by its index. It is
// hand-rolled rather than asciigraph's, which interpolates between points
// instead of stepping, spaces its date ticks evenly rather than at day
// starts, and paints with its own escapes rather than the terminal palette;
// the glue to work around that is most of this code anyway.
func renderUsageChart(first time.Time, series []usageChartSeries, width int, paint func(s string, i int) string) string {
	days := len(series[0].values)
	top := 1.0 // At least a dollar, so the top label isn't $0.
	for _, s := range series {
		for _, v := range s.values {
			top = max(top, v)
		}
	}
	height := min(usageChartHeight, int(top))
	labels := make([]string, height+1)
	labelWidth := 0
	for r := range labels {
		labels[r] = usageChartLabel(top * float64(r) / float64(height))
		labelWidth = max(labelWidth, len(labels[r]))
	}
	// Each day spans step columns, its first one stepping from the day before.
	step := max(2, min(width-labelWidth-2, usageChartWidth)/days)
	if width < labelWidth+2+days*step {
		return ""
	}
	grid := make([][]string, height+1) // grid[0] is the zero row.
	for r := range grid {
		grid[r] = slices.Repeat([]string{" "}, days*step)
	}
	// Paint the last series first so the first, the largest, ends up on top.
	for i, s := range slices.Backward(series) {
		prev := -1
		for d, v := range s.values {
			y := int(math.Round(v / top * float64(height)))
			if v > 0 {
				y = max(y, 1) // Keep any spend off the zero line.
			}
			for c := d * step; c < (d+1)*step; c++ {
				grid[y][c] = paint("─", i)
			}
			if c := d * step; prev >= 0 && prev != y {
				for r := min(prev, y) + 1; r < max(prev, y); r++ {
					grid[r][c] = paint("│", i)
				}
				if y > prev {
					grid[prev][c], grid[y][c] = paint("╯", i), paint("╭", i)
				} else {
					grid[prev][c], grid[y][c] = paint("╮", i), paint("╰", i)
				}
			}
			prev = y
		}
	}

	var b strings.Builder
	for r := height; r >= 0; r-- {
		tick := " ┤"
		if r == 0 {
			tick = " ┼"
		}
		b.WriteString(strings.Repeat(" ", labelWidth-len(labels[r])) + labels[r] + tick)
		b.WriteString(strings.TrimRight(strings.Join(grid[r], ""), " ") + "\n")
	}
	// Space the date labels, at most 6 wide, at least 2 apart.
	axis := []byte(strings.Repeat(" ", labelWidth+2+days*step+6))
	every := (8 + step - 1) / step
	for d := 0; d < days; d += every {
		copy(axis[labelWidth+2+d*step:], first.AddDate(0, 0, d).Format("Jan 2"))
	}
	b.WriteString(strings.TrimRight(string(axis), " ") + "\n")
	return b.String()
}

// usageChartLabel renders a y-axis amount to the nearest dollar, like $1,500.
func usageChartLabel(v float64) string {
	return "$" + billingGroupDigits(strconv.FormatFloat(math.Round(v), 'f', 0, 64))
}
