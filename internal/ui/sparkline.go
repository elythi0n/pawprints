package ui

import "strings"

// Sparkline draws values as a one-line bar chart of block characters, at
// most width columns wide. When there are more values than columns, each
// column shows the average of its values, so columns holding more values
// than their neighbors do not stand taller. Empty values draw as spaces.
func Sparkline(values []float64, width int) string {
	if len(values) == 0 || width <= 0 {
		return ""
	}
	if len(values) > width {
		sums := make([]float64, width)
		counts := make([]int, width)
		for i, v := range values {
			c := i * width / len(values)
			sums[c] += v
			counts[c]++
		}
		for c := range sums {
			sums[c] /= float64(counts[c])
		}
		values = sums
	}
	max := 0.0
	for _, v := range values {
		if v > max {
			max = v
		}
	}
	bars := []rune("▁▂▃▄▅▆▇█")
	var b strings.Builder
	for _, v := range values {
		if max == 0 || v <= 0 {
			b.WriteRune(' ')
			continue
		}
		b.WriteRune(bars[int(v/max*float64(len(bars)-1)+0.5)])
	}
	return b.String()
}
