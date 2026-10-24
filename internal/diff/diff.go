// Package diff computes line diffs between two versions of a file. It is a
// plain Myers diff over lines, enough for unified output, per-event line
// counts, and the hunk list the editor uses for lenses.
package diff

import (
	"bytes"
	"fmt"
	"strings"
)

// Op is one line of a diff.
type Op struct {
	Kind byte // ' ' equal, '-' removed, '+' added
	Text string
}

// Hunk is a run of changes with context, in unified-diff terms.
type Hunk struct {
	OldStart, OldLines int
	NewStart, NewLines int
	Ops                []Op
}

// Stats counts added and removed lines.
type Stats struct {
	Added   int `json:"added"`
	Removed int `json:"removed"`
}

// Lines splits content into lines, keeping track of a missing final newline.
func Lines(b []byte) []string {
	if len(b) == 0 {
		return nil
	}
	s := string(b)
	trailing := strings.HasSuffix(s, "\n")
	if trailing {
		s = s[:len(s)-1]
	}
	lines := strings.Split(s, "\n")
	if !trailing && len(lines) > 0 {
		lines[len(lines)-1] += "\n\\ No newline at end of file"
	}
	return lines
}

// Ops returns the full edit script between a and b.
func Ops(a, b []string) []Op {
	n, m := len(a), len(b)
	if n == 0 && m == 0 {
		return nil
	}
	maxD := n + m
	// v maps k-diagonal to furthest x; offset by maxD to allow negatives.
	v := make([]int, 2*maxD+2)
	var trace [][]int
	for d := 0; d <= maxD; d++ {
		snapshot := make([]int, len(v))
		copy(snapshot, v)
		trace = append(trace, snapshot)
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[maxD+k-1] < v[maxD+k+1]) {
				x = v[maxD+k+1]
			} else {
				x = v[maxD+k-1] + 1
			}
			y := x - k
			for x < n && y < m && a[x] == b[y] {
				x++
				y++
			}
			v[maxD+k] = x
			if x >= n && y >= m {
				return backtrack(trace, a, b, maxD)
			}
		}
	}
	return nil
}

func backtrack(trace [][]int, a, b []string, maxD int) []Op {
	x, y := len(a), len(b)
	var rev []Op
	for d := len(trace) - 1; d >= 0; d-- {
		v := trace[d]
		k := x - y
		var prevK int
		if k == -d || (k != d && v[maxD+k-1] < v[maxD+k+1]) {
			prevK = k + 1
		} else {
			prevK = k - 1
		}
		prevX := v[maxD+prevK]
		prevY := prevX - prevK
		for x > prevX && y > prevY {
			x--
			y--
			rev = append(rev, Op{' ', a[x]})
		}
		if d > 0 {
			if x == prevX {
				y--
				rev = append(rev, Op{'+', b[y]})
			} else {
				x--
				rev = append(rev, Op{'-', a[x]})
			}
		}
	}
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	return rev
}

// Count returns line stats for two versions.
func Count(a, b []byte) Stats {
	var s Stats
	for _, op := range Ops(Lines(a), Lines(b)) {
		switch op.Kind {
		case '+':
			s.Added++
		case '-':
			s.Removed++
		}
	}
	return s
}

// Hunks groups the edit script into hunks with the given context.
func Hunks(a, b []byte, context int) []Hunk {
	ops := Ops(Lines(a), Lines(b))
	var hunks []Hunk
	i := 0
	for i < len(ops) {
		if ops[i].Kind == ' ' {
			i++
			continue
		}
		start := i - context
		if start < 0 {
			start = 0
		}
		end := i
		// Extend the hunk while changes are within 2*context of each other.
		for end < len(ops) {
			if ops[end].Kind != ' ' {
				end++
				continue
			}
			j := end
			for j < len(ops) && ops[j].Kind == ' ' {
				j++
			}
			if j >= len(ops) || j-end > 2*context {
				end += context
				if end > len(ops) {
					end = len(ops)
				}
				break
			}
			end = j
		}
		if end > len(ops) {
			end = len(ops)
		}
		h := Hunk{Ops: ops[start:end]}
		oldLine, newLine := 1, 1
		for _, op := range ops[:start] {
			switch op.Kind {
			case ' ':
				oldLine++
				newLine++
			case '-':
				oldLine++
			case '+':
				newLine++
			}
		}
		h.OldStart, h.NewStart = oldLine, newLine
		for _, op := range h.Ops {
			switch op.Kind {
			case ' ':
				h.OldLines++
				h.NewLines++
			case '-':
				h.OldLines++
			case '+':
				h.NewLines++
			}
		}
		if h.OldLines == 0 {
			h.OldStart--
		}
		if h.NewLines == 0 {
			h.NewStart--
		}
		hunks = append(hunks, h)
		i = end
	}
	return hunks
}

// Unified renders a unified diff with headers.
func Unified(aName, bName string, a, b []byte, context int) string {
	if bytes.Equal(a, b) {
		return ""
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "--- %s\n+++ %s\n", aName, bName)
	for _, h := range Hunks(a, b, context) {
		fmt.Fprintf(&sb, "@@ -%s +%s @@\n", rangeStr(h.OldStart, h.OldLines), rangeStr(h.NewStart, h.NewLines))
		for _, op := range h.Ops {
			sb.WriteByte(op.Kind)
			sb.WriteString(op.Text)
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}

func rangeStr(start, n int) string {
	if n == 1 {
		return fmt.Sprintf("%d", start)
	}
	return fmt.Sprintf("%d,%d", start, n)
}
