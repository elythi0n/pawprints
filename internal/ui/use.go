package ui

import (
	"os"

	"github.com/charmbracelet/lipgloss"

	"github.com/elythi0n/pawprints/internal/store"
	"github.com/elythi0n/pawprints/internal/theme"
)

// ThemeChoice resolves which theme to use: the explicit override (a flag),
// then PAWPRINTS_THEME, then config.json, then auto.
func ThemeChoice(override string) string {
	if override != "" {
		return override
	}
	if env := os.Getenv("PAWPRINTS_THEME"); env != "" {
		return env
	}
	if cfg, err := store.LoadConfig(); err == nil && cfg.Theme != "" {
		return cfg.Theme
	}
	return theme.Auto
}

var (
	darkKnown bool
	dark      = true
)

// SetDarkBackground records the terminal background so auto never has to
// ask the terminal again. Tests use it to stay off the terminal entirely.
func SetDarkBackground(d bool) {
	dark, darkKnown = d, true
}

// DetectBackground asks the terminal once. It must run before the TUI takes
// over the terminal: afterwards the TUI reads the terminal's reply as input
// and the question would never be answered.
func DetectBackground() {
	if !darkKnown {
		SetDarkBackground(lipgloss.HasDarkBackground())
	}
}

// UseTheme loads and applies a theme by id.
func UseTheme(id string) error {
	if theme.ID(id) == theme.Auto || id == "" {
		DetectBackground()
	}
	p, err := theme.Load(id, store.ThemeDir(), dark)
	if err != nil {
		return err
	}
	Apply(p)
	return nil
}
