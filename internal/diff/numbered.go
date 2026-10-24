package diff

import (
	"fmt"
	"strings"
)

// Line is one row of a line-numbered diff. Old and New are 1-based and zero
// when the line does not exist on that side. Kind '@' is a separator between
// hunks with its header in Text.
type Line struct {
	Kind      byte
	Old, New  int
	Text      string
	HunkStart bool // first changed line of a run of changes
}

// plainLines splits content without the "no newline" marker Lines adds.
func plainLines(b []byte) []string {
	if len(b) == 0 {
		return nil
	}
	return strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
}

// Numbered returns the diff of a and b with line numbers. A negative context
// returns the whole file: every line of b, with removed lines interleaved
// where they were.
func Numbered(a, b []byte, context int) []Line {
	ops := Ops(plainLines(a), plainLines(b))
	all := make([]Line, 0, len(ops))
	oldN, newN := 0, 0
	prevChanged := false
	for _, op := range ops {
		l := Line{Kind: op.Kind, Text: op.Text}
		switch op.Kind {
		case ' ':
			oldN++
			newN++
			l.Old, l.New = oldN, newN
		case '-':
			oldN++
			l.Old = oldN
		case '+':
			newN++
			l.New = newN
		}
		changed := op.Kind != ' '
		l.HunkStart = changed && !prevChanged
		prevChanged = changed
		all = append(all, l)
	}
	if context < 0 {
		return all
	}
	keep := make([]bool, len(all))
	for i, l := range all {
		if l.Kind == ' ' {
			continue
		}
		for j := i - context; j <= i+context; j++ {
			if j >= 0 && j < len(all) {
				keep[j] = true
			}
		}
	}
	var out []Line
	for i := 0; i < len(all); i++ {
		if !keep[i] {
			continue
		}
		start := i
		for i < len(all) && keep[i] {
			i++
		}
		out = append(out, Line{Kind: '@', Text: header(all[start:i])})
		out = append(out, all[start:i]...)
	}
	return out
}

func header(run []Line) string {
	oldStart, newStart, oldN, newN := 0, 0, 0, 0
	for _, l := range run {
		if l.Old > 0 {
			if oldStart == 0 {
				oldStart = l.Old
			}
			oldN++
		}
		if l.New > 0 {
			if newStart == 0 {
				newStart = l.New
			}
			newN++
		}
	}
	return fmt.Sprintf("@@ -%d,%d +%d,%d @@", oldStart, oldN, newStart, newN)
}
