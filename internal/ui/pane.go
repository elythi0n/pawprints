package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Pane is the contract every panel in the TUI implements.
type Pane interface {
	Title() string
	Init() tea.Cmd
	Update(tea.Msg) (Pane, tea.Cmd)
	View() string
	SetSize(width, height int)
	// Help is the key hint line shown under the pane.
	Help() string
}

// Capturer is an optional pane capability: while it returns true the shell
// leaves every key to the pane (text input, confirm prompts).
type Capturer interface {
	CapturingKeys() bool
}

// Leaver is an optional pane capability called when focus moves away.
type Leaver interface {
	OnLeave() tea.Cmd
}

// StatusMsg flashes a line in the shell's status row.
type StatusMsg struct {
	Text string
	Kind string // ok, warn, err
}

// OkMsg returns a command that flashes an ok status.
func OkMsg(s string) tea.Cmd { return func() tea.Msg { return StatusMsg{Text: s, Kind: "ok"} } }

// WarnMsg returns a command that flashes a warning.
func WarnMsg(s string) tea.Cmd { return func() tea.Msg { return StatusMsg{Text: s, Kind: "warn"} } }

// ErrMsg returns a command that flashes an error.
func ErrMsg(s string) tea.Cmd { return func() tea.Msg { return StatusMsg{Text: s, Kind: "err"} } }

// ClipBlock truncates every line to maxW visible columns and the block to
// maxH lines. Lipgloss Width and Height are minimums, so without this a long
// line wraps and pushes the layout off screen.
func ClipBlock(s string, maxW, maxH int) string {
	if maxW < 1 {
		maxW = 1
	}
	if maxH < 1 {
		maxH = 1
	}
	lines := strings.Split(s, "\n")
	if len(lines) > maxH {
		lines = lines[:maxH]
	}
	for i, ln := range lines {
		if lipgloss.Width(ln) > maxW {
			lines[i] = ansi.Truncate(ln, maxW, "…")
		}
	}
	return strings.Join(lines, "\n")
}

// PadRight pads s with spaces to width w, measuring visible columns.
func PadRight(s string, w int) string {
	n := lipgloss.Width(s)
	if n >= w {
		return s
	}
	return s + strings.Repeat(" ", w-n)
}

// Truncate shortens s to w visible columns with an ellipsis.
func Truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	return ansi.Truncate(s, w, "…")
}

// TruncateLeft shortens s to w columns by cutting the start, for paths
// where the file name at the end matters most.
func TruncateLeft(s string, w int) string {
	if w <= 1 || lipgloss.Width(s) <= w {
		return Truncate(s, w)
	}
	r := []rune(s)
	for len(r) > 0 && lipgloss.Width(string(r))+1 > w {
		r = r[1:]
	}
	return "…" + string(r)
}

// Keys renders a help line like "enter diff · c copy" with the keys bold.
func Keys(pairs ...string) string {
	var parts []string
	for i := 0; i+1 < len(pairs); i += 2 {
		parts = append(parts, HelpKey.Render(pairs[i])+" "+Help.Render(pairs[i+1]))
	}
	return strings.Join(parts, Help.Render(" · "))
}
