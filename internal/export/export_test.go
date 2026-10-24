package export

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elythi0n/pawprints/internal/store"
)

func TestCollectAndPatch(t *testing.T) {
	t.Setenv("PAWPRINTS_HOME", t.TempDir())
	root := filepath.Join(t.TempDir(), "p")
	os.MkdirAll(root, 0o755)
	p, err := store.OpenProject(root)
	if err != nil {
		t.Fatal(err)
	}
	h1, _ := p.PutBytes([]byte("a\n"))
	h2, _ := p.PutBytes([]byte("a\nb\n"))
	h3, _ := p.PutBytes([]byte("a\nb\nc\n"))
	base := time.Now().Add(-time.Hour)
	evs := []store.Event{
		{TS: store.Now(base), Session: "s", Kind: store.KindEdit, Path: "f", Before: store.HashPtr(h1), After: store.HashPtr(h2)},
		{TS: store.Now(base.Add(time.Minute)), Session: "s", Kind: store.KindEdit, Path: "f", Before: store.HashPtr(h2), After: store.HashPtr(h3)},
		{TS: store.Now(base.Add(2 * time.Minute)), Session: "s", Kind: store.KindEdit, Path: "g", Before: nil, After: store.HashPtr(h1)},
		{TS: store.Now(base.Add(3 * time.Minute)), Session: "s", Kind: store.KindEdit, Path: "same", Before: store.HashPtr(h1), After: store.HashPtr(h1)},
	}
	for _, ev := range evs {
		if err := p.AppendEvent(ev); err != nil {
			t.Fatal(err)
		}
	}
	all, _ := p.Events(store.Query{Session: "s"})
	changes := Collect(all)
	if len(changes) != 2 || changes[0].Path != "f" || changes[0].Before != h1 || changes[0].After != h3 || changes[0].Events != 2 {
		t.Fatalf("changes %+v", changes)
	}
	patch, err := Patch(p, changes)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"diff --git a/f b/f", "--- a/f", "+++ b/f", "+b", "+c", "new file mode", "--- /dev/null", "+++ b/g"} {
		if !strings.Contains(patch, want) {
			t.Fatalf("patch missing %q:\n%s", want, patch)
		}
	}
}
