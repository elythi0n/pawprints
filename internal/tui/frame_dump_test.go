package tui

import (
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/elythi0n/pawprints/internal/view"
)

// TestDumpFrames writes plain-text frames to PAWPRINTS_FRAME_DUMP for a
// visual check; it does nothing unless that variable is set. With
// PAWPRINTS_DUMP_ROOT it renders that project from the real store instead of
// seeded data.
func TestDumpFrames(t *testing.T) {
	dir := os.Getenv("PAWPRINTS_FRAME_DUMP")
	if dir == "" {
		t.Skip("set PAWPRINTS_FRAME_DUMP=<dir> to dump frames")
	}
	lipgloss.SetColorProfile(termenv.Ascii)
	var src *view.Source
	if root := os.Getenv("PAWPRINTS_DUMP_ROOT"); root != "" {
		// Read-only look at a real store, for checking layout against real data.
		src = view.NewSource(root)
		src.Reload()
	} else {
		src = seed(t)
	}
	r := newRoot(src)
	drive(t, r, tea.WindowSizeMsg{Width: 120, Height: 36})
	os.WriteFile(dir+"/Live-top.txt", []byte(ansi.Strip(r.View())), 0o644)
	if b, ok := r.panes[r.idx].(*Browser); ok {
		// Open every folder under the first session.
		for _, row := range b.rows {
			_ = row
		}
		for i := 0; i < 40; i++ {
			if n := b.selected(); n != nil && n.IsGroup() && !n.Expanded && (n.Label == "Folders" || strings.HasSuffix(n.Label, "/")) {
				drive(t, r, key("right"))
			}
			drive(t, r, key("down"))
		}
		os.WriteFile(dir+"/Live-folders.txt", []byte(ansi.Strip(r.View())), 0o644)
	}
	drive(t, r, key("down"))
	drive(t, r, key("down"))
	drive(t, r, key("right"))
	drive(t, r, key("down"))
	drive(t, r, key("right"))
	os.WriteFile(dir+"/Live-open.txt", []byte(ansi.Strip(r.View())), 0o644)
	for i := range r.panes {
		drive(t, r, key(string(rune('1'+i))))
		for j := 0; j < 3; j++ {
			drive(t, r, key("right"))
			drive(t, r, key("down"))
		}
		os.WriteFile(dir+"/"+r.panes[r.idx].Title()+".txt", []byte(ansi.Strip(r.View())), 0o644)
	}
	drive(t, r, key("7"))
	drive(t, r, key("down"))
	drive(t, r, key("down"))
	os.WriteFile(dir+"/Settings-pane.txt", []byte(ansi.Strip(r.View())), 0o644)
	drive(t, r, key("esc"))
	drive(t, r, key("3"))
	drive(t, r, key("right"))
	drive(t, r, key("down"))
	drive(t, r, key("right"))
	drive(t, r, key("down"))
	drive(t, r, key("right"))
	drive(t, r, key("down"))
	drive(t, r, key("enter"))
	os.WriteFile(dir+"/diff.txt", []byte(ansi.Strip(r.View())), 0o644)
	drive(t, r, key("f"))
	os.WriteFile(dir+"/file.txt", []byte(ansi.Strip(r.View())), 0o644)
	drive(t, r, key("?"))
	os.WriteFile(dir+"/help.txt", []byte(ansi.Strip(r.View())), 0o644)
	drive(t, r, key("x"))
	drive(t, r, key("esc"))
	drive(t, r, key("D"))
	os.WriteFile(dir+"/clear-menu.txt", []byte(ansi.Strip(r.View())), 0o644)
}
