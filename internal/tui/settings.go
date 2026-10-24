package tui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/elythi0n/pawprints/internal/diff"
	"github.com/elythi0n/pawprints/internal/store"
	"github.com/elythi0n/pawprints/internal/theme"
	"github.com/elythi0n/pawprints/internal/ui"
	"github.com/elythi0n/pawprints/internal/view"
)

// restyleMsg tells the root a theme was applied, so every pane re-renders
// with the new colors.
type restyleMsg struct{}

type settingsMode int

const (
	settingsList settingsMode = iota
	settingsNewName
	settingsImportPath
)

// SettingsPane holds the theme picker. Moving through the list previews a
// theme on the whole UI; enter keeps it, and leaving without enter returns
// to the saved one.
type SettingsPane struct {
	themes  []theme.Info
	cursor  int
	scroll  int
	saved   string // id stored in config.json
	shown   string // id currently applied
	mode    settingsMode
	input   textinput.Model
	w, h    int
	loadErr string
}

// NewSettingsPane creates the pane.
func NewSettingsPane(_ *view.Source) *SettingsPane {
	in := textinput.New()
	in.CharLimit = 200
	s := &SettingsPane{input: in}
	s.reload()
	return s
}

func (s *SettingsPane) reload() {
	s.themes = theme.List(store.ThemeDir())
	s.saved = savedThemeID()
	if s.shown == "" {
		s.shown = s.saved
	}
	for i, t := range s.themes {
		if t.ID == s.shown {
			s.cursor = i
		}
	}
	if s.cursor >= len(s.themes) {
		s.cursor = len(s.themes) - 1
	}
}

func savedThemeID() string {
	if env := os.Getenv("PAWPRINTS_THEME"); env != "" {
		return theme.ID(env)
	}
	cfg, _ := store.LoadConfig()
	if cfg.Theme == "" {
		return theme.Auto
	}
	return theme.ID(cfg.Theme)
}

// Title implements ui.Pane.
func (s *SettingsPane) Title() string { return "Settings" }

// Init implements ui.Pane.
func (s *SettingsPane) Init() tea.Cmd { return nil }

// SetSize implements ui.Pane.
func (s *SettingsPane) SetSize(w, h int) {
	s.w, s.h = w, h
	s.input.Width = w - 20
}

// CapturingKeys implements ui.Capturer.
func (s *SettingsPane) CapturingKeys() bool { return s.mode != settingsList }

// TypingText reports a text field has focus.
func (s *SettingsPane) TypingText() bool { return s.mode != settingsList }

// OnLeave implements ui.Leaver: an unsaved preview is undone.
func (s *SettingsPane) OnLeave() tea.Cmd {
	s.mode = settingsList
	if s.shown != s.saved {
		return s.apply(s.saved)
	}
	return nil
}

func (s *SettingsPane) selected() (theme.Info, bool) {
	if s.cursor < 0 || s.cursor >= len(s.themes) {
		return theme.Info{}, false
	}
	return s.themes[s.cursor], true
}

// apply makes a theme active for the whole UI.
func (s *SettingsPane) apply(id string) tea.Cmd {
	if err := ui.UseTheme(id); err != nil {
		s.loadErr = err.Error()
		return flash("err", err.Error())
	}
	s.loadErr = ""
	s.shown = id
	return func() tea.Msg { return restyleMsg{} }
}

// Update implements ui.Pane.
func (s *SettingsPane) Update(msg tea.Msg) (ui.Pane, tea.Cmd) {
	switch m := msg.(type) {
	case editorClosedMsg:
		s.reload()
		return s, s.apply(s.shown)
	case tea.KeyMsg:
		if s.mode != settingsList {
			return s.updateInput(m)
		}
		return s.updateList(m)
	}
	return s, nil
}

func (s *SettingsPane) move(d int) tea.Cmd {
	n := s.cursor + d
	if n < 0 || n >= len(s.themes) {
		return nil
	}
	s.cursor = n
	s.ensureVisible()
	t := s.themes[n]
	if t.Err != "" {
		return nil
	}
	return s.apply(t.ID)
}

func (s *SettingsPane) ensureVisible() {
	rows := s.h - 12
	if rows < 3 {
		rows = 3
	}
	if s.cursor < s.scroll {
		s.scroll = s.cursor
	}
	if s.cursor >= s.scroll+rows {
		s.scroll = s.cursor - rows + 1
	}
}

func (s *SettingsPane) updateList(m tea.KeyMsg) (ui.Pane, tea.Cmd) {
	switch m.String() {
	case "down", "j":
		return s, s.move(1)
	case "up", "k":
		return s, s.move(-1)
	case "g", "home":
		return s, s.move(-s.cursor)
	case "G", "end":
		return s, s.move(len(s.themes) - 1 - s.cursor)
	case "enter", " ":
		t, ok := s.selected()
		if !ok {
			return s, nil
		}
		if t.Err != "" {
			return s, flash("err", t.Err)
		}
		if err := store.SetConfigValue("theme", t.ID); err != nil {
			return s, flash("err", err.Error())
		}
		s.saved = t.ID
		return s, tea.Batch(s.apply(t.ID), flash("ok", "theme set to "+t.Name))
	case "esc":
		if s.shown != s.saved {
			s.reload()
			for i, t := range s.themes {
				if t.ID == s.saved {
					s.cursor = i
				}
			}
			return s, tea.Batch(s.apply(s.saved), flash("ok", "back to "+s.saved))
		}
	case "n":
		s.mode = settingsNewName
		s.input.Placeholder = "name for the new theme"
		s.input.SetValue("")
		s.input.Focus()
		return s, textinput.Blink
	case "i":
		s.mode = settingsImportPath
		s.input.Placeholder = "a .json or kitty .conf file, or a folder of them"
		s.input.SetValue("~/.config/kitty/themes")
		s.input.CursorEnd()
		s.input.Focus()
		return s, textinput.Blink
	case "e":
		t, ok := s.selected()
		if !ok || t.Source == "builtin" {
			return s, flash("warn", "built-in themes are read-only; press n to make an editable copy")
		}
		return s, editFile(t.Source)
	case "r":
		s.reload()
		return s, tea.Batch(s.apply(s.shown), flash("ok", fmt.Sprintf("%d themes", len(s.themes))))
	case "b", "L":
		return s, toggleLayout()
	case "S":
		return s, func() tea.Msg { return openSetupMsg{} }
	case "v":
		next := "each"
		if !combineVersions() {
			next = "combined"
		}
		if err := store.SetConfigValue("file_versions", next); err != nil {
			return s, flash("err", err.Error())
		}
		msg := "files under a prompt: one combined change each"
		if next == "each" {
			msg = "files under a prompt: every version listed"
		}
		// The trees are built from config, so a reload shows the new layout.
		return s, tea.Batch(flash("ok", msg), func() tea.Msg { return RequestReloadMsg{} })
	}
	return s, nil
}

func combineVersions() bool {
	cfg, _ := store.LoadConfig()
	return cfg.CombineVersions()
}

func (s *SettingsPane) updateInput(m tea.KeyMsg) (ui.Pane, tea.Cmd) {
	switch m.String() {
	case "esc":
		s.mode = settingsList
		s.input.Blur()
		return s, nil
	case "enter":
		value := strings.TrimSpace(s.input.Value())
		mode := s.mode
		s.mode = settingsList
		s.input.Blur()
		if value == "" {
			return s, nil
		}
		switch mode {
		case settingsNewName:
			base := s.shown
			path, err := theme.WriteFile(store.ThemeDir(), value, base)
			if err != nil {
				return s, flash("err", err.Error())
			}
			s.reload()
			s.selectID(theme.ID(value))
			return s, tea.Batch(s.apply(theme.ID(value)), editFile(path))
		case settingsImportPath:
			ids, skipped, errs := theme.ImportAll(expandHome(value), store.ThemeDir())
			s.reload()
			if len(ids) > 0 {
				s.selectID(ids[0])
			}
			switch {
			case len(errs) > 0 && len(ids) == 0:
				return s, flash("err", errs[0].Error())
			case len(errs) > 0:
				return s, flash("warn", fmt.Sprintf("imported %d, %d failed: %v", len(ids), len(errs), errs[0]))
			}
			if len(ids) == 0 && len(skipped) > 0 {
				return s, flash("ok", "nothing new: "+strings.Join(skipped, ", ")+" are built in")
			}
			msg := "imported " + strings.Join(ids, ", ")
			if len(skipped) > 0 {
				msg += fmt.Sprintf("; skipped %d built in", len(skipped))
			}
			return s, flash("ok", msg)
		}
	}
	var cmd tea.Cmd
	s.input, cmd = s.input.Update(m)
	return s, cmd
}

func (s *SettingsPane) selectID(id string) {
	for i, t := range s.themes {
		if t.ID == id {
			s.cursor = i
			s.ensureVisible()
		}
	}
}

type editorClosedMsg struct{}

// editFile opens a theme file in $EDITOR and reloads it afterwards.
func editFile(path string) tea.Cmd {
	editor := editorCommand()
	if editor == "" {
		return flash("warn", "set $EDITOR to edit themes; the file is "+path)
	}
	parts := append(strings.Fields(editor), path)
	return tea.ExecProcess(exec.Command(parts[0], parts[1:]...), func(error) tea.Msg { return editorClosedMsg{} })
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

// View implements ui.Pane.
func (s *SettingsPane) View() string {
	if s.mode != settingsList {
		label := "new theme, starting from " + s.shown
		if s.mode == settingsImportPath {
			label = "import themes"
		}
		return ui.PaneTitle.Render(label) + "\n\n" + s.input.View() + "\n\n" + ui.Dim.Render("enter confirm · esc cancel")
	}
	listW := 34
	if s.w < 90 {
		listW = 28
	}
	var sb strings.Builder
	mode := "combined change"
	if !combineVersions() {
		mode = "each version"
	}
	layout := "list"
	if cfg, _ := store.LoadConfig(); cfg.TreeLayout() {
		layout = "tree"
	}
	sb.WriteString(ui.Soft.Render("Files under a prompt") + "\n")
	sb.WriteString("  " + ui.Strong.Render(mode) + "  " + ui.Dim.Render("v switch") + "\n")
	sb.WriteString("  " + ui.Strong.Render("view as "+layout) + "  " + ui.Dim.Render("L switch") + "\n\n")
	sb.WriteString(ui.Soft.Render("Integrations") + "  " + ui.Dim.Render("S hooks, editors, completions") + "\n\n")
	sb.WriteString(ui.Soft.Render("Theme") + "\n\n")
	rows := s.h - 12
	end := s.scroll + rows
	if end > len(s.themes) {
		end = len(s.themes)
	}
	for i := s.scroll; i < end; i++ {
		t := s.themes[i]
		mark := "  "
		if t.ID == s.saved {
			mark = ui.Ok.Render("● ")
		}
		name := t.Name
		if t.Source != "builtin" {
			name += ui.Dim.Render(" custom")
		}
		if t.Err != "" {
			name = ui.Err.Render("✗ ") + t.ID
		}
		line := mark + ui.Truncate(name, listW-4)
		if i == s.cursor {
			line = ui.Cursor.Render("▍") + lipgloss.NewStyle().Background(lipgloss.Color(ui.Surface)).Render(ui.PadRight(line, listW-2))
		} else {
			line = " " + line
		}
		sb.WriteString(line + "\n")
	}
	if len(s.themes) > end {
		sb.WriteString(ui.Dim.Render(fmt.Sprintf("  … %d more", len(s.themes)-end)) + "\n")
	}
	left := lipgloss.NewStyle().Width(listW).Render(strings.TrimRight(sb.String(), "\n"))
	rightW := s.w - listW - 3
	right := ui.Preview.Width(rightW - 2).Height(s.h - 2).Render(ui.ClipBlock(s.preview(rightW-4), rightW-4, s.h-2))
	return lipgloss.JoinHorizontal(lipgloss.Top, left, " ", right)
}

// preview shows the highlighted theme as the UI would draw it.
func (s *SettingsPane) preview(width int) string {
	t, ok := s.selected()
	if !ok {
		return ""
	}
	if t.Err != "" {
		return ui.Err.Render("this theme does not load") + "\n\n" + wrap(t.Err, width) + "\n\n" + ui.Dim.Render(t.Source) + "\n\n" + ui.Dim.Render("e edit · r reload")
	}
	p := ui.Current
	var sb strings.Builder
	sb.WriteString(ui.Title.Render(" "+p.Name+" ") + "  " + ui.Swatches(p) + "\n")
	source := "built in"
	if t.Source != "builtin" {
		source = t.Source
	}
	if t.ID == theme.Auto {
		source = "picks Catppuccin Mocha or Latte from the terminal background"
	}
	sb.WriteString(ui.Dim.Render(source) + "\n\n")
	sb.WriteString(ui.DotBusy.Render("●") + " " + ui.Strong.Render("api-a9") + "  " + ui.Soft.Render("busy · 3 files · 7 edits") + "\n")
	sb.WriteString("    " + ui.Dim.Render("“add retry to the webhook client”") + "\n")
	sb.WriteString("  " + ui.Cursor.Render("▍") + lipgloss.NewStyle().Background(lipgloss.Color(ui.Surface)).Render(" · 2 min ago   Edit  "+ui.Added.Render("+12")+" "+ui.Removed.Render("-3")+" ") + "\n")
	sb.WriteString("   " + ui.DotWaiting.Render("◐") + " " + ui.Strong.Render("web-6d") + "  " + ui.Soft.Render("waiting: input needed") + "\n\n")
	before := []byte("func greet() string {\n\treturn \"hi\"\n}\n")
	after := []byte("func greet(name string) string {\n\treturn \"hi \" + name\n}\n")
	body, _ := renderLines(diff.Numbered(before, after, 1), width, true)
	sb.WriteString(body + "\n\n")
	sb.WriteString(ui.Ok.Render("● restored 1 file") + "   " + ui.Warn.Render("● edited since") + "   " + ui.Err.Render("● failed") + "\n\n")
	sb.WriteString(ui.Dim.Render("custom themes live in "+store.ThemeDir()) + "\n")
	sb.WriteString(ui.Dim.Render("JSON roles or kitty .conf files; `pawprints theme show <id>` lists the roles"))
	return sb.String()
}

// Help implements ui.Pane.
func (s *SettingsPane) Help() string {
	if s.mode != settingsList {
		return ""
	}
	return ui.Keys("j/k", "preview", "enter", "use", "esc", "revert", "v", "file rows", "L", "list/tree", "S", "setup", "n", "new", "e", "edit", "i", "import", "?", "all keys")
}

// KeyHelp lists every key for the help overlay.
func (s *SettingsPane) KeyHelp() [][2]string {
	return [][2]string{
		{"j/k  ↑/↓", "preview a theme on the whole UI"},
		{"enter  space", "use it and save to config.json"},
		{"esc", "go back to the saved theme"},
		{"n", "new theme starting from the one shown, opened in $EDITOR"},
		{"e", "edit the highlighted custom theme in $EDITOR"},
		{"i", "import a .json or kitty .conf file, or a whole folder of them"},
		{"r", "reload themes from " + store.ThemeDir()},
		{"v", "files under a prompt: one combined change, or every version"},
		{"L", "files under a prompt as a flat list or inside their folders"},
		{"S", "set up integrations: Claude Code hooks, editor extensions, shell completions"},
	}
}

// toggleLayout switches files under a prompt between a flat list and their
// folder tree, saves it, and rebuilds the trees.
func toggleLayout() tea.Cmd {
	cfg, _ := store.LoadConfig()
	next, msg := "tree", "files under a prompt: inside their folders"
	if cfg.TreeLayout() {
		next, msg = "list", "files under a prompt: one flat list"
	}
	if err := store.SetConfigValue("file_layout", next); err != nil {
		return flash("err", err.Error())
	}
	return tea.Batch(flash("ok", msg), func() tea.Msg { return RequestReloadMsg{} })
}
