package tui

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/elythi0n/pawprints/internal/panes"
	"github.com/elythi0n/pawprints/internal/store"
	"github.com/elythi0n/pawprints/internal/view"
)

func TestCombinedFileRows(t *testing.T) {
	lipgloss.SetColorProfile(termenv.Ascii)
	t.Setenv("PAWPRINTS_HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := filepath.Join(t.TempDir(), "proj")
	os.MkdirAll(root, 0o755)
	p, _ := store.OpenProject(root)
	base := time.Now().Add(-time.Hour)
	v0, _ := p.PutBytes([]byte("one\n"))
	v1, _ := p.PutBytes([]byte("one\ntwo\n"))
	v2, _ := p.PutBytes([]byte("one\ntwo\nthree\n"))
	os.WriteFile(filepath.Join(root, "f.txt"), []byte("one\ntwo\nthree\n"), 0o644)
	for i, pair := range [][2]string{{v0, v1}, {v1, v2}} {
		at := base.Add(time.Duration(i) * time.Minute)
		ev := store.Event{ID: store.NewID(at), TS: store.Now(at), Session: "S", Turn: "T1", Tool: "Edit", Kind: store.KindEdit, Path: "f.txt", Before: store.HashPtr(pair[0]), After: store.HashPtr(pair[1]), LinesAdded: 1}
		p.AppendEvent(ev)
		p.SetLast("f.txt", store.LastKnown{After: pair[1], Event: ev.ID, TS: ev.TS})
	}
	p.UpdateSession("S", func(r *store.SessionRecord) {
		r.Turns = []store.Turn{{ID: "T1", At: store.Now(base.Add(-time.Second)), Text: "grow the file"}}
		r.Edits = 2
		r.AddFile("f.txt")
	})
	src := view.NewSource(root)
	src.Reload()

	// Default: one row for the file, spanning both edits.
	roots := panes.Sessions(src)
	turn := roots[0].Children[0]
	if len(turn.Children) != 1 {
		t.Fatalf("expected one file row, got %d", len(turn.Children))
	}
	row := turn.Children[0]
	if row.Kind != view.KindVersion || row.Edits != 2 || row.Event.BeforeHash() != v0 || row.Event.AfterHash() != v2 {
		t.Fatalf("combined row %+v", row)
	}

	// Undo on the combined row returns the file to before the first edit.
	r := newRoot(src)
	drive(t, r, tea.WindowSizeMsg{Width: 120, Height: 40})
	drive(t, r, key("3"))
	b := r.panes[r.idx].(*Browser)
	drive(t, r, key("right"))
	drive(t, r, key("down"))
	drive(t, r, key("right"))
	drive(t, r, key("down"))
	if n := b.selected(); n == nil || n.Edits != 2 {
		t.Fatalf("expected the combined row selected, got %+v", n)
	}
	drive(t, r, key("enter"))
	if b.mode != modeDiff || len(b.full.hunks) == 0 {
		t.Fatal("enter on a combined row opens its diff")
	}
	drive(t, r, key("esc"))
	drive(t, r, key("u"))
	drive(t, r, key("yes"))
	drive(t, r, key("enter"))
	got, _ := os.ReadFile(filepath.Join(root, "f.txt"))
	if string(got) != "one\n" {
		t.Fatalf("undo of the combined row left %q", got)
	}

	// Switching to "each" brings the version folders back.
	store.SetConfigValue("file_versions", "each")
	src.Reload()
	roots = panes.Sessions(src)
	if row := roots[0].Children[0].Children[0]; row.Kind != view.KindFile {
		t.Fatalf("each mode should group versions under a file, got kind %d", row.Kind)
	}
	// The Files pane always lists every version.
	store.SetConfigValue("file_versions", "combined")
	src.Reload()
	if f := panes.Files(src)[0]; f.Kind != view.KindFile {
		t.Fatal("Files keeps versions")
	}
}
