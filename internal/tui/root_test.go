package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/elythi0n/pawprints/internal/store"
	"github.com/elythi0n/pawprints/internal/ui"
	"github.com/elythi0n/pawprints/internal/view"
)

func seed(t *testing.T) *view.Source {
	t.Helper()
	sparkleInterval, statusFlash, clockInterval = time.Millisecond, time.Millisecond, time.Hour
	ui.SetDarkBackground(true)
	t.Setenv("PAWPRINTS_HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := filepath.Join(t.TempDir(), "proj")
	os.MkdirAll(root, 0o755)
	p, err := store.OpenProject(root)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().Add(-2 * time.Hour)
	prev := ""
	for i := 0; i < 12; i++ {
		content := strings.Repeat("line\n", i+1) + "tail " + strings.Repeat("x", i*7) + "\n"
		h, _ := p.PutBytes([]byte(content))
		path := "src/file.go"
		if i%3 == 0 {
			path = "README.md"
		}
		os.MkdirAll(filepath.Dir(filepath.Join(root, path)), 0o755)
		os.WriteFile(filepath.Join(root, path), []byte(content), 0o644)
		ev := store.Event{TS: store.Now(base.Add(time.Duration(i) * 5 * time.Minute)), Session: "sess-" + string(rune('a'+i%2)), Turn: "TURN" + string(rune('0'+i/4)), Tool: "Edit", Kind: store.KindEdit, Path: path, Before: store.HashPtr(prev), After: store.HashPtr(h), LinesAdded: 1}
		if err := p.AppendEvent(ev); err != nil {
			t.Fatal(err)
		}
		prev = h
	}
	for _, s := range []string{"sess-a", "sess-b"} {
		p.UpdateSession(s, func(r *store.SessionRecord) {
			r.Edits = 6
			r.AddFile("src/file.go")
			r.Turns = []store.Turn{{ID: "TURN0", At: store.Now(base), Text: "make the thing"}, {ID: "TURN1", At: store.Now(base.Add(time.Hour)), Text: "now the other thing with a longer prompt that should wrap in the preview"}}
			r.LastPrompt = "now the other thing"
		})
	}
	evs, _ := p.Events(store.Query{Limit: 1})
	p.AddPin(evs[0], "keep")
	src := view.NewSource(root)
	src.Reload()
	return src
}

// drive feeds a message, then runs every command it produced until no
// messages are left, so async previews and actions land synchronously.
func drive(t *testing.T, r *root, msg tea.Msg) {
	t.Helper()
	queue := []tea.Msg{msg}
	for len(queue) > 0 && len(queue) < 200 {
		m := queue[0]
		queue = queue[1:]
		_, cmd := r.Update(m)
		queue = append(queue, collect(cmd)...)
	}
}

func collect(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	switch m := msg.(type) {
	case nil:
		return nil
	case tea.BatchMsg:
		var out []tea.Msg
		for _, c := range m {
			out = append(out, collect(c)...)
		}
		return out
	}
	if _, isTick := msg.(sparkleTick); isTick {
		return nil
	}
	if _, isClear := msg.(clearStatus); isClear {
		return nil
	}
	if _, isClock := msg.(clockTick); isClock {
		return nil
	}
	return []tea.Msg{msg}
}

func key(s string) tea.KeyMsg {
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEscape}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "left":
		return tea.KeyMsg{Type: tea.KeyLeft}
	case "right":
		return tea.KeyMsg{Type: tea.KeyRight}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func checkFrame(t *testing.T, r *root, w, h int, where string) {
	t.Helper()
	frame := r.View()
	lines := strings.Split(frame, "\n")
	if len(lines) != h {
		t.Fatalf("%s: frame has %d lines, want %d\n%s", where, len(lines), h, ansi.Strip(frame))
	}
	for i, ln := range lines {
		if lipgloss.Width(ln) > w {
			t.Fatalf("%s: line %d is %d wide, want <= %d: %q", where, i, lipgloss.Width(ln), w, ansi.Strip(ln))
		}
	}
}

func TestLayoutAcrossPanesAndSizes(t *testing.T) {
	lipgloss.SetColorProfile(termenv.Ascii)
	src := seed(t)
	for _, size := range [][2]int{{80, 24}, {120, 40}, {200, 60}, {72, 14}} {
		w, h := size[0], size[1]
		r := newRoot(src)
		drive(t, r, tea.WindowSizeMsg{Width: w, Height: h})
		for i := range r.panes {
			drive(t, r, key(string(rune('1'+i))))
			checkFrame(t, r, w, h, r.panes[r.idx].Title())
			for j := 0; j < 6; j++ {
				drive(t, r, key("down"))
				drive(t, r, key("right"))
			}
			checkFrame(t, r, w, h, r.panes[r.idx].Title()+" expanded")
			if b, ok := r.panes[r.idx].(*Browser); ok {
				drive(t, r, key("enter"))
				checkFrame(t, r, w, h, b.name+" enter")
				if b.mode == modeDiff {
					drive(t, r, key("j"))
					drive(t, r, key("a"))
					checkFrame(t, r, w, h, b.name+" diff")
					for _, k := range []string{"f", "n", "N", "w", "l", "h", "w", "?"} {
						drive(t, r, key(k))
						checkFrame(t, r, w, h, b.name+" full "+k)
					}
					drive(t, r, key("x")) // closes help
					drive(t, r, key("esc"))
					if b.mode != modeList {
						t.Fatalf("esc should leave the full view")
					}
				}
				drive(t, r, key("J"))
				drive(t, r, tea.MouseMsg{X: w - 5, Y: 10, Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown})
				checkFrame(t, r, w, h, b.name+" scrolled preview")
				drive(t, r, key("/"))
				drive(t, r, key("file"))
				checkFrame(t, r, w, h, b.name+" filter")
				drive(t, r, key("esc"))
				drive(t, r, key("D"))
				checkFrame(t, r, w, h, b.name+" clear menu")
				drive(t, r, key("esc"))
			}
		}
		drive(t, r, key("left"))
		drive(t, r, key("left"))
		checkFrame(t, r, w, h, "sidebar focus")
		drive(t, r, key("down"))
		drive(t, r, key("enter"))
		checkFrame(t, r, w, h, "after sidebar")
	}
}

func TestTooSmall(t *testing.T) {
	src := seed(t)
	r := newRoot(src)
	drive(t, r, tea.WindowSizeMsg{Width: 40, Height: 10})
	if !strings.Contains(r.View(), "too small") {
		t.Fatal("expected the too-small notice")
	}
}

func TestWelcomeWhenEmpty(t *testing.T) {
	lipgloss.SetColorProfile(termenv.Ascii)
	t.Setenv("PAWPRINTS_HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	src := view.NewSource(t.TempDir())
	src.Reload()
	r := newRoot(src)
	drive(t, r, tea.WindowSizeMsg{Width: 100, Height: 30})
	frame := ansi.Strip(r.View())
	if !strings.Contains(frame, "Nothing recorded yet") || !strings.Contains(frame, "press i") {
		t.Fatalf("welcome missing:\n%s", frame)
	}
	checkFrame(t, r, 100, 30, "welcome")
}

func TestBrowserActionsRoundTrip(t *testing.T) {
	lipgloss.SetColorProfile(termenv.Ascii)
	src := seed(t)
	r := newRoot(src)
	drive(t, r, tea.WindowSizeMsg{Width: 120, Height: 40})
	drive(t, r, key("3")) // Sessions
	b := r.panes[r.idx].(*Browser)
	// Expand session → turn, select a file row (a version), pin it, undo it.
	drive(t, r, key("right"))
	drive(t, r, key("down"))
	drive(t, r, key("right"))
	drive(t, r, key("down"))
	n := b.selected()
	if n == nil || n.Kind != view.KindVersion {
		t.Fatalf("expected a version row, got %+v", n)
	}
	drive(t, r, key("p"))
	if b.mode != modeInput {
		t.Fatal("pin should ask for a message")
	}
	drive(t, r, key("why"))
	drive(t, r, key("enter"))
	if !src.Pinned(n.Event.ID) {
		t.Fatal("pin not recorded")
	}
	drive(t, r, key("u"))
	if b.mode != modeConfirm {
		t.Fatalf("undo should ask, mode=%d status=%s", b.mode, r.status)
	}
	drive(t, r, key("yes"))
	drive(t, r, key("enter"))
	if r.statusKind != "ok" {
		t.Fatalf("undo status: %s %s", r.statusKind, r.status)
	}
	undos, _ := src.Project.Events(store.Query{Kinds: []string{store.KindUndo, store.KindRestore}})
	if len(undos) != 1 {
		t.Fatalf("expected one restore event, got %d", len(undos))
	}
	// Jump message selects a node in another pane.
	drive(t, r, JumpMsg{Pane: "Files", Key: "file:README.md"})
	if r.panes[r.idx].Title() != "Files" || r.panes[r.idx].(*Browser).selectedKey() != "file:README.md" {
		t.Fatal("jump failed")
	}
	checkFrame(t, r, 120, 40, "after jump")
}

func TestFullViewModes(t *testing.T) {
	lipgloss.SetColorProfile(termenv.Ascii)
	src := seed(t)
	r := newRoot(src)
	drive(t, r, tea.WindowSizeMsg{Width: 120, Height: 40})
	drive(t, r, key("4")) // Files
	b := r.panes[r.idx].(*Browser)
	drive(t, r, key("right"))
	drive(t, r, key("down"))
	drive(t, r, key("enter"))
	if b.mode != modeDiff {
		t.Fatalf("enter on a version should open the full view")
	}
	frame := ansi.Strip(r.View())
	if !strings.Contains(frame, "changes") || !strings.Contains(frame, "│") {
		t.Fatalf("full view should show numbered changes:\n%s", frame)
	}
	drive(t, r, key("f"))
	frame = ansi.Strip(r.View())
	if !strings.Contains(frame, "whole file") {
		t.Fatalf("f should switch to the whole file:\n%s", frame)
	}
	if len(b.full.hunks) == 0 {
		t.Fatal("whole file view should know where changes are")
	}
	drive(t, r, key("?"))
	if !strings.Contains(ansi.Strip(r.View()), "next / previous change") {
		t.Fatal("help overlay should list the viewer keys")
	}
	drive(t, r, key("q"))
	if r.showHelp {
		t.Fatal("any key closes help")
	}
	drive(t, r, key("q"))
	if b.mode != modeList {
		t.Fatal("q in the full view goes back, it does not quit")
	}
}
