// Package ui carries the active theme's colors and every shared Lipgloss
// style. Apply rebuilds all of them from a palette, so switching theme
// restyles the whole TUI at once.
package ui

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/elythi0n/pawprints/internal/theme"
)

// Colors of the active theme, by role. Read them through lipgloss.Color.
var (
	Base, Mantle, Surface, Overlay string
	Text, Subtext, Muted           string
	Mauve, Lavender                string // accent and highlight
	Blue, Green, Red, Yellow       string
	Peach, Teal                    string
	AddedTint, RemovedTint         string
)

// Current is the palette the styles were last built from.
var Current theme.Palette

// Shared styles, rebuilt by Apply.
var (
	Title, TitlePath                                     lipgloss.Style
	Sidebar, SidebarFocused                              lipgloss.Style
	SidebarItem, SidebarItemSelected, SidebarItemActive  lipgloss.Style
	PaneBox, PaneBoxFocused, PaneTitle, Preview          lipgloss.Style
	Help, HelpKey                                        lipgloss.Style
	Ok, Warn, Err                                        lipgloss.Style
	Hl, Accent, Dim, Soft, Strong, Added, Removed        lipgloss.Style
	Selected, Cursor                                     lipgloss.Style
	DotBusy, DotWaiting, DotIdle, DotEnded               lipgloss.Style
	Badge                                                lipgloss.Style
	AddedRow, RemovedRow, AddedMark, RemovedMark, Gutter lipgloss.Style
	HunkHeader, Swatch                                   lipgloss.Style
)

func init() {
	p, _ := theme.Load(theme.Default, "", true)
	Apply(p)
}

func fg(c string) lipgloss.Style { return lipgloss.NewStyle().Foreground(lipgloss.Color(c)) }

// tinted gives a row its background tint when the theme has one; themes on
// the terminal's own palette have none and fall back to colored text.
func tinted(text, tint string) lipgloss.Style {
	s := fg(text)
	if tint != "" {
		s = s.Background(lipgloss.Color(tint))
	}
	return s
}

// Apply makes p the active theme.
func Apply(p theme.Palette) {
	Current = p
	Base, Mantle, Surface, Overlay = p.Base, p.Mantle, p.Surface, p.Overlay
	Text, Subtext, Muted = p.Text, p.Subtext, p.Muted
	Mauve, Lavender = p.Accent, p.Highlight
	Blue, Green, Red, Yellow, Peach, Teal = p.Blue, p.Green, p.Red, p.Yellow, p.Peach, p.Teal
	AddedTint, RemovedTint = p.AddedBg, p.RemovedBg

	Title = lipgloss.NewStyle().Foreground(lipgloss.Color(Base)).Background(lipgloss.Color(Mauve)).Bold(true).Padding(0, 1)
	TitlePath = fg(Muted)

	Sidebar = lipgloss.NewStyle().Foreground(lipgloss.Color(Text)).Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color(Overlay)).Padding(1, 1)
	SidebarFocused = Sidebar.BorderForeground(lipgloss.Color(Mauve))
	SidebarItem = fg(Subtext).Padding(0, 1)
	SidebarItemSelected = fg(Text).Background(lipgloss.Color(Surface)).Padding(0, 1)
	SidebarItemActive = fg(Base).Background(lipgloss.Color(Mauve)).Bold(true).Padding(0, 1)

	PaneBox = lipgloss.NewStyle().Foreground(lipgloss.Color(Text)).Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color(Overlay)).Padding(0, 1)
	PaneBoxFocused = PaneBox.BorderForeground(lipgloss.Color(Mauve))
	PaneTitle = fg(Mauve).Bold(true)
	Preview = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color(Overlay)).Padding(0, 1)

	Help = fg(Muted)
	HelpKey = fg(Subtext).Bold(true)
	Ok = fg(Green).Bold(true)
	Warn = fg(Yellow).Bold(true)
	Err = fg(Red).Bold(true)

	Hl = fg(Blue)
	Accent = fg(Peach)
	Dim = fg(Muted)
	Soft = fg(Subtext)
	Strong = fg(Text).Bold(true)
	Added = fg(Green)
	Removed = fg(Red)
	Selected = fg(Base).Background(lipgloss.Color(Lavender)).Bold(true)
	Cursor = fg(Mauve).Bold(true)

	DotBusy = fg(Green)
	DotWaiting = fg(Yellow)
	DotIdle = fg(Muted)
	DotEnded = fg(Overlay)
	Badge = fg(Base).Background(lipgloss.Color(Teal)).Padding(0, 1)

	AddedRow = tinted(Text, AddedTint)
	RemovedRow = tinted(Text, RemovedTint)
	if AddedTint == "" {
		AddedRow = fg(Green)
		RemovedRow = fg(Red)
	}
	AddedMark = tinted(Green, AddedTint).Bold(true)
	RemovedMark = tinted(Red, RemovedTint).Bold(true)
	Gutter = fg(Overlay)
	HunkHeader = fg(Blue).Faint(true)
	Swatch = lipgloss.NewStyle()
}

// Swatches renders the palette as colored blocks, for previews.
func Swatches(p theme.Palette) string {
	var out string
	for _, c := range []string{p.Accent, p.Highlight, p.Blue, p.Teal, p.Green, p.Yellow, p.Peach, p.Red} {
		out += lipgloss.NewStyle().Foreground(lipgloss.Color(c)).Render("██")
	}
	return out
}
