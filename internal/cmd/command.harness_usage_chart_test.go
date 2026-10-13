package cmd

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func Test_Harness_Usage_Chart(t *testing.T) {
	first := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	strip := regexp.MustCompile(`</?[0-9]>`)
	paint := func(s string, i int) string { return fmt.Sprintf("<%d>%s</%d>", i, s, i) }
	out := renderUsageChart(first, [][]float64{{1, 2, 0.5}, {0.25, 0.25, 0.25}}, 24, paint)
	r := require.New(t)
	r.Contains(out, "<0>╭</0>", "the first series in the first color")
	r.Contains(out, "<1>─</1>", "the second in the second")
	r.Equal("$2 ┤      ╭─────╮\n$1 ┤──────╯─────╰─────\n$0 ┼\n    Sep 1       Sep 3\n", strip.ReplaceAllString(out, ""),
		"a run per day with its date centered under it, a row per dollar, the largest series on top")

	r.Empty(renderUsageChart(first, [][]float64{{1, 2, 3}}, 9, paint), "too narrow for two columns per day")
	r.Empty(renderUsageChart(first, [][]float64{make([]float64, 31)}, 66, paint), "too narrow once the last date sticks out past the plot")
	r.Contains(renderUsageChart(first, [][]float64{{0.001}}, 40, paint), "$1 ┤", "at least a dollar tall")
	r.Contains(renderUsageChart(first, [][]float64{{1500}}, 60, paint), "$1,500 ┤", "large amounts in whole dollars")
	r.Contains(strip.ReplaceAllString(renderUsageChart(first, [][]float64{{30}}, 200, paint), ""), "$30 ┤"+strings.Repeat("─", 48)+"\n",
		"no wider than it needs to be on a wide terminal")
}
