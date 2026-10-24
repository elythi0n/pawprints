// Package theme defines the colors the terminal UI is drawn with. A theme is
// a palette of roles (background, text, accent, added, removed, ...). Themes
// come from the built-in set, from JSON files that set roles and may extend
// another theme, or from kitty color files, so a terminal theme drops in as
// it is.
package theme

import (
	"fmt"
	"sort"
	"strings"
)

// Palette is one theme's colors by role. Values are "#RRGGBB" hex, an ANSI
// color number "0" to "15" (the terminal's own palette), or "" for none,
// which only the row tints accept.
type Palette struct {
	Name string `json:"name"`
	Dark bool   `json:"dark"`

	Base    string `json:"base"`    // window background
	Mantle  string `json:"mantle"`  // a shade darker than base
	Surface string `json:"surface"` // selected rows, raised areas
	Overlay string `json:"overlay"` // borders, gutters
	Text    string `json:"text"`
	Subtext string `json:"subtext"` // secondary text
	Muted   string `json:"muted"`   // hints, timestamps

	Accent    string `json:"accent"`    // titles, focused borders
	Highlight string `json:"highlight"` // selection badge
	Blue      string `json:"blue"`      // hunk headers, links
	Green     string `json:"green"`     // added lines, ok, busy
	Red       string `json:"red"`       // removed lines, errors
	Yellow    string `json:"yellow"`    // warnings, waiting
	Peach     string `json:"peach"`     // secondary accent
	Teal      string `json:"teal"`      // badges

	AddedBg   string `json:"added_bg"`   // tint behind added lines
	RemovedBg string `json:"removed_bg"` // tint behind removed lines
}

// Roles lists every role name, in the order `theme show` uses.
var Roles = []string{
	"base", "mantle", "surface", "overlay", "text", "subtext", "muted",
	"accent", "highlight", "blue", "green", "red", "yellow", "peach", "teal",
	"added_bg", "removed_bg",
}

// Get returns a role's value by name.
func (p *Palette) Get(role string) string { return *p.field(role) }

// Set assigns a role by name and reports whether the role exists.
func (p *Palette) Set(role, value string) bool {
	f := p.field(role)
	if f == nil {
		return false
	}
	*f = value
	return true
}

func (p *Palette) field(role string) *string {
	switch strings.ToLower(strings.ReplaceAll(role, "-", "_")) {
	case "base", "background":
		return &p.Base
	case "mantle":
		return &p.Mantle
	case "surface", "selection":
		return &p.Surface
	case "overlay", "border":
		return &p.Overlay
	case "text", "foreground":
		return &p.Text
	case "subtext":
		return &p.Subtext
	case "muted":
		return &p.Muted
	case "accent":
		return &p.Accent
	case "highlight":
		return &p.Highlight
	case "blue":
		return &p.Blue
	case "green":
		return &p.Green
	case "red":
		return &p.Red
	case "yellow":
		return &p.Yellow
	case "peach":
		return &p.Peach
	case "teal":
		return &p.Teal
	case "added_bg":
		return &p.AddedBg
	case "removed_bg":
		return &p.RemovedBg
	}
	return nil
}

// Terminal colors in the kitty sense: background, foreground, selection,
// and the 16 ANSI colors. Most themes in the wild are published this way.
type Terminal struct {
	Background string
	Foreground string
	Selection  string
	Colors     [16]string
}

// FromTerminal derives a full palette from terminal colors. Roles a
// terminal palette has no slot for are mixed from the ones it has.
func FromTerminal(name string, t Terminal) Palette {
	c := fillColors(t.Colors)
	pick := func(vals ...string) string {
		for _, v := range vals {
			if v != "" {
				return v
			}
		}
		return ""
	}
	bg, fg := t.Background, t.Foreground
	dark := luminance(bg) < 0.5
	p := Palette{
		Name:      name,
		Dark:      dark,
		Base:      bg,
		Mantle:    mix(bg, "#000000", 0.25),
		Surface:   mix(bg, fg, 0.12),
		Overlay:   mix(bg, fg, 0.28),
		Text:      fg,
		Subtext:   mix(fg, bg, 0.22),
		Muted:     pick(readable(c[8], bg, fg), mix(fg, bg, 0.5)),
		Accent:    pick(c[5], c[13], c[4]),
		Highlight: pick(c[12], c[4], c[5]),
		Blue:      clearer(bg, c[4], c[12]),
		Green:     clearer(bg, c[2], c[10]),
		Red:       clearer(bg, c[1], c[9]),
		Yellow:    clearer(bg, c[3], c[11]),
		Peach:     pick(c[11], c[3]),
		Teal:      pick(c[6], c[14]),
	}
	tint := 0.18
	if !dark {
		tint = 0.22
	}
	p.AddedBg = mix(bg, p.Green, tint)
	p.RemovedBg = mix(bg, p.Red, tint)
	return p
}

// defaultANSI fills colors a terminal theme leaves out (Catppuccin Mocha's
// terminal colors), so a file that only sets a few still makes a theme.
var defaultANSI = [16]string{
	"#45475A", "#F38BA8", "#A6E3A1", "#F9E2AF", "#89B4FA", "#CBA6F7", "#94E2D5", "#BAC2DE",
	"#585B70", "#F38BA8", "#A6E3A1", "#F9E2AF", "#89B4FA", "#CBA6F7", "#94E2D5", "#A6ADC8",
}

// fillColors completes a 16-color set: a missing color takes its bright or
// normal twin first, then the default.
func fillColors(in [16]string) [16]string {
	out := in
	for i := range out {
		if out[i] != "" {
			continue
		}
		if twin := in[(i+8)%16]; twin != "" {
			out[i] = twin
		} else {
			out[i] = defaultANSI[i]
		}
	}
	return out
}

// clearer prefers the normal ANSI color and switches to its bright twin
// only when the normal one is hard to read on the background.
func clearer(bg, normal, bright string) string {
	if contrast(normal, bg) >= 3 || contrast(bright, bg) <= contrast(normal, bg) {
		return normal
	}
	return bright
}

// readable keeps a muted color only when it stands out from the background
// enough to read; some themes set bright black almost equal to it.
func readable(c, bg, fg string) string {
	if c == "" {
		return ""
	}
	if abs(luminance(c)-luminance(bg)) < 0.18 {
		return mix(fg, bg, 0.5)
	}
	return c
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

// Merge returns base with every non-empty role of over applied.
func Merge(base, over Palette) Palette {
	out := base
	for _, role := range Roles {
		if v := over.Get(role); v != "" {
			out.Set(role, v)
		}
	}
	if over.Name != "" {
		out.Name = over.Name
	}
	return out
}

// Validate checks every role holds something the renderer understands.
func (p Palette) Validate() error {
	var bad []string
	for _, role := range Roles {
		v := p.Get(role)
		if v == "" && role != "added_bg" && role != "removed_bg" {
			bad = append(bad, role+" is empty")
			continue
		}
		if v != "" && !isHex(v) && !isANSI(v) {
			bad = append(bad, fmt.Sprintf("%s=%q is not #RRGGBB or 0-15", role, v))
		}
	}
	if len(bad) > 0 {
		sort.Strings(bad)
		return fmt.Errorf("theme %s: %s", p.Name, strings.Join(bad, "; "))
	}
	return nil
}
