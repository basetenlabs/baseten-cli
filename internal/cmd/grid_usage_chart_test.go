package cmd

import (
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func Test_Grid_Route_Usage_Chart(t *testing.T) {
	first := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	paint := func(s string, i int) string { return "<" + s + ">" }
	out := renderGridUsageChart(first, [][]float64{{1, 2, 0.5}, {0.25, 0.25, 0.25}}, 24, paint)
	require.Equal(t, "$2 ┤      ╭─────╮\n$1 ┤──────╯─────╰─────\n$0 ┼\n    Sep 1       Sep 3\n",
		regexp.MustCompile(`[<>]`).ReplaceAllString(out, ""))
}
