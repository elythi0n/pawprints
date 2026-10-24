package panes

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elythi0n/pawprints/internal/store"
	"github.com/elythi0n/pawprints/internal/view"
)

func TestFolders(t *testing.T) {
	t.Setenv("PAWPRINTS_HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := filepath.Join(t.TempDir(), "ws")
	os.MkdirAll(root, 0o755)
	src := view.NewSource(root)
	src.Now = time.Now()
	at := time.Now().Add(-time.Minute)
	ev := func(id int, path string, added int) store.Event {
		ts := at.Add(time.Duration(id) * time.Second)
		return store.Event{ID: store.NewID(ts), TS: store.Now(ts), Session: "S", Turn: "T", Tool: "Edit", Kind: store.KindEdit, Path: path, LinesAdded: added}
	}
	evs := []store.Event{ // newest first, as the store returns them
		ev(6, "pawprints/internal/tui/root.go", 3),
		ev(5, "pawprints/internal/tui/root.go", 2),
		ev(4, "pawprints/internal/tui/settings.go", 10),
		ev(3, "pawprints/internal/theme/theme.go", 7),
		ev(2, "pawprints/plugin/hooks.json", 4),
		ev(1, "/tmp/scratch/gen.py", 1),
	}
	n := &view.Node{Children: FolderTree(src, evs, "p:")}
	if len(n.Children) != 2 || n.Children[0].Label != "pawprints/" || n.Children[1].Label != "/tmp/scratch/" {
		t.Fatalf("top level %v", labels(n.Children))
	}
	pawprints := n.Children[0]
	if pawprints.Desc != "4 files · +26 -0" {
		t.Fatalf("pawprints totals %q", pawprints.Desc)
	}
	if got := labels(pawprints.Children); len(got) != 2 || got[0] != "internal/" || got[1] != "plugin/" {
		t.Fatalf("pawprints children %v", got)
	}
	tui := pawprints.Children[0].Children[0]
	if tui.Label != "tui/" || tui.Desc != "2 files · +15 -0" {
		t.Fatalf("tui %q %q", tui.Label, tui.Desc)
	}
	rootGo := tui.Children[0]
	if rootGo.Label != "root.go" || rootGo.Edits != 2 {
		t.Fatalf("root.go row %+v", rootGo)
	}

	// A narrow chain reads as one row.
	chain := FolderTree(src, []store.Event{ev(2, "go/services/billing/testdata/a.json", 1), ev(1, "go/services/billing/testdata/b.json", 1)}, "q:")
	if len(chain) != 1 || chain[0].Label != "go/services/billing/testdata/" || len(chain[0].Children) != 2 {
		t.Fatalf("compacted chain %v", labels(chain))
	}
	deep := FolderTree(src, []store.Event{ev(2, "go/services/billing/testdata/a.json", 1), ev(1, "go/services/billing/main.go", 1)}, "r:")
	if deep[0].Label != "go/services/billing/" {
		t.Fatalf("compacted chain %v", labels(deep))
	}
}

func labels(nodes []*view.Node) []string {
	var out []string
	for _, n := range nodes {
		out = append(out, n.Label)
	}
	return out
}
