package theme

import "sort"

// Built-in names. "auto" and "terminal" are not palettes of their own:
// auto picks a dark or light theme from the terminal background, and
// terminal uses the terminal's own 16 colors so any terminal theme carries over.
const (
	Auto     = "auto"
	TermName = "terminal"
	Default  = "catppuccin-mocha"
	Light    = "catppuccin-latte"
)

func term(bg, fg, sel string, colors ...string) Terminal {
	t := Terminal{Background: bg, Foreground: fg, Selection: sel}
	copy(t.Colors[:], colors)
	return t
}

// builtins holds every shipped palette. Catppuccin is written out role by
// role because it is the reference look; the rest derive from their
// published terminal colors.
var builtins = map[string]Palette{
	"catppuccin-mocha": {
		Name: "Catppuccin Mocha", Dark: true,
		Base: "#1E1E2E", Mantle: "#181825", Surface: "#313244", Overlay: "#45475A",
		Text: "#CDD6F4", Subtext: "#A6ADC8", Muted: "#7F849C",
		Accent: "#CBA6F7", Highlight: "#B4BEFE", Blue: "#89B4FA", Green: "#A6E3A1",
		Red: "#F38BA8", Yellow: "#F9E2AF", Peach: "#FAB387", Teal: "#94E2D5",
		AddedBg: "#233A2F", RemovedBg: "#3E2532",
	},
	"catppuccin-latte": {
		Name: "Catppuccin Latte", Dark: false,
		Base: "#EFF1F5", Mantle: "#E6E9EF", Surface: "#CCD0DA", Overlay: "#9CA0B0",
		// Muted, green, and yellow are a shade darker than the published
		// Latte values so they reach 3:1 on the light background.
		Text: "#4C4F69", Subtext: "#5C5F77", Muted: "#7C7F93",
		Accent: "#8839EF", Highlight: "#7287FD", Blue: "#1E66F5", Green: "#358A22",
		Red: "#D20F39", Yellow: "#B26B0C", Peach: "#FE640B", Teal: "#179299",
		AddedBg: "#D5EBD1", RemovedBg: "#F3D2D9",
	},
	// Andromeda's editor and terminal colors, role by role: its cyan is the
	// accent (buttons, cursor), its purple the highlight.
	"andromeda": {
		Name: "Andromeda", Dark: true,
		Base: "#23262E", Mantle: "#1B1D23", Surface: "#3D4352", Overlay: "#4E5361",
		Text: "#D5CED9", Subtext: "#A0A1A7", Muted: "#8A8592",
		Accent: "#00E8C6", Highlight: "#C74DED", Blue: "#7CB7FF", Green: "#96E072",
		Red: "#EE5D43", Yellow: "#FFE66D", Peach: "#F39C12", Teal: "#00B0FF",
		AddedBg: "#34453B", RemovedBg: "#46302F",
	},
	"tokyo-night": FromTerminal("Tokyo Night", term("#1A1B26", "#C0CAF5", "#283457",
		"#15161E", "#F7768E", "#9ECE6A", "#E0AF68", "#7AA2F7", "#BB9AF7", "#7DCFFF", "#A9B1D6",
		"#414868", "#F7768E", "#9ECE6A", "#E0AF68", "#7AA2F7", "#BB9AF7", "#7DCFFF", "#C0CAF5")),
	"dracula": FromTerminal("Dracula", term("#282A36", "#F8F8F2", "#44475A",
		"#21222C", "#FF5555", "#50FA7B", "#F1FA8C", "#BD93F9", "#FF79C6", "#8BE9FD", "#F8F8F2",
		"#6272A4", "#FF6E6E", "#69FF94", "#FFFFA5", "#D6ACFF", "#FF92DF", "#A4FFFF", "#FFFFFF")),
	"eyecandy": FromTerminal("EyeCandy", term("#2B1728", "#F5D6E8", "#D4589E",
		"#3B2238", "#FF6B9D", "#A8D8A8", "#FFD4A8", "#B89FFF", "#FF85C2", "#F0A0C8", "#F5D6E8",
		"#6B3D68", "#FF9DBE", "#C0E8C0", "#FFE4C0", "#CEC0FF", "#FFB3D9", "#FFD0E8", "#FFF0F7")),
	"gruvbox-dark": FromTerminal("Gruvbox Dark", term("#282828", "#EBDBB2", "#504945",
		"#282828", "#CC241D", "#98971A", "#D79921", "#458588", "#B16286", "#689D6A", "#A89984",
		"#928374", "#FB4934", "#B8BB26", "#FABD2F", "#83A598", "#D3869B", "#8EC07C", "#EBDBB2")),
	"nord": FromTerminal("Nord", term("#2E3440", "#D8DEE9", "#4C566A",
		"#3B4252", "#BF616A", "#A3BE8C", "#EBCB8B", "#81A1C1", "#B48EAD", "#88C0D0", "#E5E9F0",
		"#4C566A", "#BF616A", "#A3BE8C", "#EBCB8B", "#81A1C1", "#B48EAD", "#8FBCBB", "#ECEFF4")),
	"rose-pine": FromTerminal("Rosé Pine", term("#191724", "#E0DEF4", "#403D52",
		"#26233A", "#EB6F92", "#31748F", "#F6C177", "#9CCFD8", "#C4A7E7", "#EBBCBA", "#E0DEF4",
		"#6E6A86", "#EB6F92", "#31748F", "#F6C177", "#9CCFD8", "#C4A7E7", "#EBBCBA", "#E0DEF4")),
	"ratatorks": FromTerminal("Ratatorks", term("#0D1B2A", "#B0CDD8", "#1480A0",
		"#0D1420", "#E84545", "#00C99A", "#C87941", "#1AA8CC", "#8855DD", "#00E8D0", "#7A9EAE",
		"#1A2A3A", "#FF5C6E", "#1AFFD0", "#FFB347", "#40CCEE", "#AA77FF", "#00FFEE", "#CCE8F0")),
	// The terminal's own palette by ANSI number. Row tints are left out
	// because a tint cannot be computed from colors we do not know.
	TermName: {
		Name: "Terminal", Dark: true,
		Base: "0", Mantle: "0", Surface: "8", Overlay: "8",
		Text: "7", Subtext: "7", Muted: "8",
		Accent: "5", Highlight: "4", Blue: "4", Green: "2",
		Red: "1", Yellow: "3", Peach: "11", Teal: "6",
	},
}

func init() {
	// Tokyo Night reads better with its blue as the accent, Dracula with its purple.
	tn := builtins["tokyo-night"]
	tn.Accent, tn.Highlight = "#BB9AF7", "#7AA2F7"
	builtins["tokyo-night"] = tn
	dr := builtins["dracula"]
	dr.Accent, dr.Highlight, dr.Blue = "#BD93F9", "#FF79C6", "#8BE9FD"
	builtins["dracula"] = dr
	// Ratatorks is built around its cyan: the cursor, active border, and tabs.
	rt := builtins["ratatorks"]
	rt.Accent, rt.Highlight, rt.Teal = "#00E8D0", "#40CCEE", "#1AFFD0"
	builtins["ratatorks"] = rt
	ec := builtins["eyecandy"]
	ec.Accent, ec.Highlight = "#FF85C2", "#B89FFF"
	builtins["eyecandy"] = ec
}

// BuiltinNames lists shipped themes in display order, auto first.
func BuiltinNames() []string {
	names := make([]string, 0, len(builtins)+1)
	for n := range builtins {
		if n != Default && n != TermName {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	return append([]string{Auto, Default, TermName}, names...)
}
