package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/elythi0n/pawprints/internal/store"
	"github.com/elythi0n/pawprints/internal/ui"
)

func TestSettingsPreviewSaveRevert(t *testing.T) {
	lipgloss.SetColorProfile(termenv.Ascii)
	src := seed(t)
	t.Setenv("PAWPRINTS_THEME", "")
	os.WriteFile(filepath.Join(store.Home(), "config.json"), []byte(`{"retention_days": 30, "unknown_key": "kept"}`), 0o644)
	ui.UseTheme("catppuccin-mocha")
	r := newRoot(src)
	drive(t, r, tea.WindowSizeMsg{Width: 120, Height: 40})
	drive(t, r, key("7"))
	s, ok := r.panes[r.idx].(*SettingsPane)
	if !ok {
		t.Fatal("pane 7 should be Settings")
	}
	checkFrame(t, r, 120, 40, "settings")
	if !strings.Contains(ansi.Strip(r.View()), "Tokyo Night") {
		t.Fatal("the theme list should include Tokyo Night")
	}
	// Move to Tokyo Night: the whole UI previews it.
	pick(t, r, s, "tokyo-night")
	if ui.Current.Name != "Tokyo Night" {
		t.Fatalf("preview not applied: %s", ui.Current.Name)
	}
	checkFrame(t, r, 120, 40, "tokyo preview")
	// Leaving without enter reverts.
	drive(t, r, key("tab"))
	if ui.Current.Name != "Catppuccin Mocha" {
		t.Fatalf("leaving should revert, got %s", ui.Current.Name)
	}
	drive(t, r, key("7"))
	pick(t, r, s, "dracula")
	drive(t, r, key("enter"))
	cfg, _ := store.LoadConfig()
	if cfg.Theme != "dracula" || cfg.RetentionDays != 30 {
		t.Fatalf("saved config %+v", cfg)
	}
	data, _ := os.ReadFile(filepath.Join(store.Home(), "config.json"))
	if !strings.Contains(string(data), "unknown_key") {
		t.Fatal("saving the theme must keep other config keys")
	}
	drive(t, r, key("tab"))
	if ui.Current.Name != "Dracula" {
		t.Fatal("a saved theme stays after leaving")
	}
	// Import a folder of kitty themes and a new theme from the current one.
	themes := t.TempDir()
	os.WriteFile(filepath.Join(themes, "Ocean.conf"), []byte("background #001122\nforeground #ddeeff\ncolor1 #ff5555\ncolor2 #55ff55\ncolor5 #ff79c6\n"), 0o644)
	drive(t, r, key("7"))
	drive(t, r, key("i"))
	s.input.SetValue(themes)
	drive(t, r, key("enter"))
	if s.themes[s.cursor].ID != "ocean" {
		t.Fatalf("imported theme should be selected, got %s", s.themes[s.cursor].ID)
	}
	t.Setenv("EDITOR", "true")
	t.Setenv("VISUAL", "")
	drive(t, r, key("n"))
	s.input.SetValue("Deep Ocean")
	drive(t, r, key("enter"))
	if _, err := os.Stat(filepath.Join(store.ThemeDir(), "deep-ocean.json")); err != nil {
		t.Fatal("n should write a theme file")
	}
	drive(t, r, key("?"))
	if !strings.Contains(ansi.Strip(r.View()), "import a .json or kitty .conf") {
		t.Fatal("help should list the settings keys")
	}
	drive(t, r, key("x"))
	ui.UseTheme("catppuccin-mocha")
}

// pick moves the Settings cursor to a theme the way a person would.
func pick(t *testing.T, r *root, s *SettingsPane, id string) {
	t.Helper()
	target := -1
	for i, th := range s.themes {
		if th.ID == id {
			target = i
		}
	}
	if target < 0 {
		t.Fatalf("no theme %s", id)
	}
	for step := 0; s.cursor != target; step++ {
		if step > len(s.themes) {
			t.Fatalf("could not reach %s", id)
		}
		if s.cursor < target {
			drive(t, r, key("down"))
		} else {
			drive(t, r, key("up"))
		}
	}
}
