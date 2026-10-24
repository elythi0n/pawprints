package tui

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/elythi0n/pawprints/internal/diff"
	"github.com/elythi0n/pawprints/internal/store"
	"github.com/elythi0n/pawprints/internal/ui"
	"github.com/elythi0n/pawprints/internal/view"
)

// renderLines draws a line-numbered diff. With wrap set, long lines continue
// on the next row under a blank gutter; without it they stay one row and the
// viewport scrolls sideways. It also returns the row where each run of
// changes starts, for jumping between changes.
func renderLines(lines []diff.Line, width int, wrap bool) (string, []int) {
	maxN := 1
	for _, l := range lines {
		if l.Old > maxN {
			maxN = l.Old
		}
		if l.New > maxN {
			maxN = l.New
		}
	}
	digits := len(fmt.Sprint(maxN))
	gutterW := digits*2 + 4 // "old new │" plus a space
	textW := width - gutterW - 2
	if textW < 10 {
		textW = 10
	}
	num := func(n int) string {
		if n == 0 {
			return strings.Repeat(" ", digits)
		}
		return fmt.Sprintf("%*d", digits, n)
	}
	blank := ui.Gutter.Render(strings.Repeat(" ", digits*2+1) + " ┆ ")

	var sb strings.Builder
	var starts []int
	row := 0
	emit := func(s string) {
		sb.WriteString(s)
		sb.WriteByte('\n')
		row++
	}
	for _, l := range lines {
		if l.Kind == '@' {
			emit(ui.HunkHeader.Render(strings.Repeat(" ", gutterW-2) + "┈ " + l.Text))
			continue
		}
		if l.HunkStart {
			starts = append(starts, row)
		}
		gutter := ui.Gutter.Render(num(l.Old) + " " + num(l.New) + " │ ")
		text := strings.ReplaceAll(l.Text, "\t", "    ")
		segments := []string{text}
		if wrap && lipgloss.Width(text) > textW {
			segments = strings.Split(ansi.Hardwrap(text, textW, true), "\n")
		}
		var mark, body lipgloss.Style
		switch l.Kind {
		case '+':
			mark, body = ui.AddedMark, ui.AddedRow
		case '-':
			mark, body = ui.RemovedMark, ui.RemovedRow
		default:
			mark, body = lipgloss.NewStyle(), lipgloss.NewStyle()
		}
		for i, seg := range segments {
			g := gutter
			if i > 0 {
				g = blank
			}
			marker := " "
			if i == 0 && l.Kind != ' ' {
				marker = string(l.Kind)
			}
			if l.Kind == ' ' {
				emit(g + marker + seg)
				continue
			}
			// Pad so the tint spans the row, which reads as a band.
			pad := textW - lipgloss.Width(seg)
			if pad < 0 {
				pad = 0
			}
			emit(g + mark.Render(marker) + body.Render(seg+strings.Repeat(" ", pad)))
		}
	}
	return strings.TrimSuffix(sb.String(), "\n"), starts
}

// renderFullView builds the full-screen content for one event: only the
// changes with context, or the whole file with changes marked.
func renderFullView(src *view.Source, ev store.Event, against, mode string, wrap bool, width int) (string, string, []int) {
	if src.Project == nil {
		return "", "", nil
	}
	if ev.Skipped != "" {
		return "", ui.Warn.Render("content not stored: " + ev.Skipped), nil
	}
	after, err := src.Project.Get(ev.AfterHash())
	if err != nil {
		return "", ui.Err.Render(err.Error()), nil
	}
	var base []byte
	if against == "current" {
		base = after
		after, _ = os.ReadFile(src.Project.Abs(ev.Path))
	} else {
		base, _ = src.Project.Get(ev.BeforeHash())
	}
	if store.IsBinary(base) || store.IsBinary(after) {
		return "", ui.Dim.Render("binary content"), nil
	}
	st := diff.Count(base, after)
	header := ui.Added.Render(fmt.Sprintf("+%d", st.Added)) + " " + ui.Removed.Render(fmt.Sprintf("-%d", st.Removed))
	context := 3
	if mode == "file" {
		context = -1
	}
	lines := diff.Numbered(base, after, context)
	if len(lines) == 0 {
		return header, ui.Dim.Render("no differences"), nil
	}
	text, hunks := renderLines(lines, width, wrap)
	return header, text, hunks
}
