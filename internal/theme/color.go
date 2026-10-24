package theme

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

func isHex(s string) bool {
	if len(s) != 7 || s[0] != '#' {
		return false
	}
	_, err := strconv.ParseUint(s[1:], 16, 32)
	return err == nil
}

func isANSI(s string) bool {
	n, err := strconv.Atoi(s)
	return err == nil && n >= 0 && n <= 255
}

// normalize accepts #RGB, RRGGBB, and #RRGGBB, and returns #RRGGBB.
func normalize(s string) string {
	s = strings.TrimSpace(s)
	if isANSI(s) {
		return s
	}
	s = strings.TrimPrefix(s, "#")
	if len(s) == 3 {
		s = string([]byte{s[0], s[0], s[1], s[1], s[2], s[2]})
	}
	if len(s) != 6 {
		return ""
	}
	if _, err := strconv.ParseUint(s, 16, 32); err != nil {
		return ""
	}
	return "#" + strings.ToUpper(s)
}

func rgb(s string) (r, g, b float64, ok bool) {
	if !isHex(s) {
		return 0, 0, 0, false
	}
	v, _ := strconv.ParseUint(s[1:], 16, 32)
	return float64(v >> 16 & 0xff), float64(v >> 8 & 0xff), float64(v & 0xff), true
}

// mix blends a toward b by t (0 is a, 1 is b). Non-hex input, such as an
// ANSI number, returns a unchanged because it cannot be blended.
func mix(a, b string, t float64) string {
	ar, ag, ab, ok1 := rgb(a)
	br, bg, bb, ok2 := rgb(b)
	if !ok1 || !ok2 {
		return a
	}
	f := func(x, y float64) int { return int(x + (y-x)*t + 0.5) }
	return fmt.Sprintf("#%02X%02X%02X", f(ar, br), f(ag, bg), f(ab, bb))
}

// luminance is the relative brightness of a hex color, 0 dark to 1 light.
// Unknown values count as dark, which is what most terminals are.
func luminance(s string) float64 {
	r, g, b, ok := rgb(s)
	if !ok {
		return 0
	}
	return (0.2126*r + 0.7152*g + 0.0722*b) / 255
}

// contrast is the WCAG contrast ratio between two hex colors, 1 to 21.
func contrast(a, b string) float64 {
	la, lb := relLum(a), relLum(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

func relLum(s string) float64 {
	r, g, b, ok := rgb(s)
	if !ok {
		return 0
	}
	lin := func(c float64) float64 {
		c /= 255
		if c <= 0.03928 {
			return c / 12.92
		}
		return math.Pow((c+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(r) + 0.7152*lin(g) + 0.0722*lin(b)
}
