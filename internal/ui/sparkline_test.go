package ui

import "testing"

func TestSparkline(t *testing.T) {
	cases := []struct {
		values []float64
		width  int
		want   string
	}{
		{[]float64{0, 1, 2, 4}, 10, " ▃▅█"},
		{[]float64{1, 1, 2, 2}, 2, "▅█"},
		{[]float64{0, 0}, 5, "  "},
		{nil, 5, ""},
		// Steady use stays flat however the values fall into columns.
		{[]float64{3, 3, 3, 3, 3}, 3, "███"},
	}
	for _, c := range cases {
		if got := Sparkline(c.values, c.width); got != c.want {
			t.Errorf("Sparkline(%v, %d) = %q, want %q", c.values, c.width, got, c.want)
		}
	}
}
