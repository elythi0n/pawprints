package restore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elythi0n/pawprints/internal/store"
)

func setup(t *testing.T) (*store.Project, string) {
	t.Helper()
	t.Setenv("PAWPRINTS_HOME", t.TempDir())
	root := filepath.Join(t.TempDir(), "proj")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	p, err := store.OpenProject(root)
	if err != nil {
		t.Fatal(err)
	}
	return p, root
}

// edit simulates what the hooks record for one Claude edit.
func edit(t *testing.T, p *store.Project, rel, before, after, session, turn string, at time.Time) store.Event {
	t.Helper()
	return record(t, p, store.KindEdit, rel, before, after, session, turn, at)
}

// record simulates what the hooks record for one change of the given kind.
func record(t *testing.T, p *store.Project, kind, rel, before, after, session, turn string, at time.Time) store.Event {
	t.Helper()
	var bh, ah string
	if before != "" {
		bh, _ = p.PutBytes([]byte(before))
	}
	if after != "" {
		ah, _ = p.PutBytes([]byte(after))
		if err := os.WriteFile(p.Abs(rel), []byte(after), 0o644); err != nil {
			t.Fatal(err)
		}
	} else {
		os.Remove(p.Abs(rel))
	}
	ev := store.Event{TS: store.Now(at), Session: session, Turn: turn, Tool: "Edit", Kind: kind, Path: rel, Before: store.HashPtr(bh), After: store.HashPtr(ah)}
	ev.ID = store.NewID(at)
	if err := p.AppendEvent(ev); err != nil {
		t.Fatal(err)
	}
	if err := p.SetLast(rel, store.LastKnown{After: ah, Event: ev.ID, TS: ev.TS}); err != nil {
		t.Fatal(err)
	}
	return ev
}

func TestUndoTurnEndsAtEarliestBefore(t *testing.T) {
	p, _ := setup(t)
	base := time.Now().Add(-time.Hour)
	edit(t, p, "a.txt", "", "v1\n", "s", "t1", base)
	edit(t, p, "a.txt", "v1\n", "v2\n", "s", "t2", base.Add(time.Minute))
	edit(t, p, "a.txt", "v2\n", "v3\n", "s", "t2", base.Add(2*time.Minute))
	edit(t, p, "b.txt", "", "new\n", "s", "t2", base.Add(3*time.Minute))

	res, err := Undo(p, UndoScope{Turn: "t2"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Changes) != 2 {
		t.Fatalf("changes %+v", res.Changes)
	}
	got, _ := os.ReadFile(p.Abs("a.txt"))
	if string(got) != "v1\n" {
		t.Fatalf("a.txt = %q, want v1", got)
	}
	if _, err := os.Stat(p.Abs("b.txt")); !os.IsNotExist(err) {
		t.Fatal("b.txt was created in the turn and must be removed")
	}
	evs, _ := p.Events(store.Query{Kinds: []string{store.KindUndo}})
	if len(evs) != 2 {
		t.Fatalf("undo must record events, got %d", len(evs))
	}
	// Undoing the undo brings v3 back.
	res, err = Undo(p, UndoScope{Last: true}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	_ = res
}

func TestRestoreSafety(t *testing.T) {
	p, _ := setup(t)
	base := time.Now().Add(-time.Hour)
	ev := edit(t, p, "f.txt", "", "one\ntwo\n", "s", "t1", base)
	edit(t, p, "f.txt", "one\ntwo\n", "one\ntwo\nthree\n", "s", "t1", base.Add(time.Minute))
	// The person edits the file by hand afterwards.
	if err := os.WriteFile(p.Abs("f.txt"), []byte("zero\none\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Restore(p, ev, After, Options{})
	var mod *ErrModified
	if err == nil || !asModified(err, &mod) {
		t.Fatalf("expected ErrModified, got %v", err)
	}
	// Dry run reports the plan without writing.
	res, err := Restore(p, ev, After, Options{Force: true, DryRun: true})
	if err != nil || res.Applied || !res.Changes[0].Modified {
		t.Fatalf("dry run %+v %v", res, err)
	}
	got, _ := os.ReadFile(p.Abs("f.txt"))
	if string(got) != "zero\none\ntwo\nthree\n" {
		t.Fatal("dry run must not write")
	}
	// Three-way keeps the hand edit and applies the restore.
	res, err = Restore(p, ev, After, Options{ThreeWay: true})
	if err != nil {
		t.Fatal(err)
	}
	got, _ = os.ReadFile(p.Abs("f.txt"))
	if string(got) != "zero\none\ntwo\n" || res.Changes[0].Conflicts != 0 {
		t.Fatalf("three-way result %q conflicts=%d", got, res.Changes[0].Conflicts)
	}
	// Force overwrites.
	if _, err := Restore(p, ev, After, Options{Force: true}); err != nil {
		t.Fatal(err)
	}
	got, _ = os.ReadFile(p.Abs("f.txt"))
	if string(got) != "one\ntwo\n" {
		t.Fatalf("force result %q", got)
	}
	if _, modified, _ := Check(p, "f.txt"); modified {
		t.Fatal("after a recorded restore the file must be in sync")
	}
	if strings.Contains(string(got), "<<<<") {
		t.Fatal("unexpected conflict markers")
	}
}

func asModified(err error, target **ErrModified) bool {
	m, ok := err.(*ErrModified)
	if ok {
		*target = m
	}
	return ok
}

func TestUndoKeepsChangesMadeOutsideClaude(t *testing.T) {
	p, _ := setup(t)
	base := time.Now().Add(-time.Hour)
	edit(t, p, "a.txt", "one\ntwo\n", "one\ntwo\nthree\n", "s", "t1", base)
	record(t, p, store.KindBackground, "gen.txt", "", "generated\n", "s", "t1", base.Add(time.Minute))
	// The person edits a.txt in their editor; the next scan records it.
	record(t, p, store.KindExternal, "a.txt", "one\ntwo\nthree\n", "zero\none\ntwo\nthree\n", "s", "t1", base.Add(2*time.Minute))
	// A file only the person touched is not Claude's to undo.
	record(t, p, store.KindExternal, "notes.txt", "", "mine\n", "s", "t1", base.Add(3*time.Minute))

	_, err := Undo(p, UndoScope{Turn: "t1"}, Options{})
	var mod *ErrModified
	if !asModified(err, &mod) || !mod.Outside || mod.Path != "a.txt" {
		t.Fatalf("undo over an outside change must refuse, got %v", err)
	}
	if !strings.Contains(err.Error(), "outside Claude") {
		t.Fatalf("message %q", err)
	}

	res, err := Undo(p, UndoScope{Turn: "t1"}, Options{ThreeWay: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Changes) != 2 {
		t.Fatalf("changes %+v", res.Changes)
	}
	got, _ := os.ReadFile(p.Abs("a.txt"))
	if string(got) != "zero\none\ntwo\n" {
		t.Fatalf("three-way must keep the person's line and drop Claude's: %q", got)
	}
	if _, err := os.Stat(p.Abs("gen.txt")); !os.IsNotExist(err) {
		t.Fatal("a file a background command wrote is Claude's and must be removed")
	}
	if got, _ := os.ReadFile(p.Abs("notes.txt")); string(got) != "mine\n" {
		t.Fatalf("notes.txt must be untouched, got %q", got)
	}
}

func TestUndoRevertsEachChangeAroundAnOutsideEdit(t *testing.T) {
	p, _ := setup(t)
	base := time.Now().Add(-time.Hour)
	edit(t, p, "a.txt", "a\nb\nc\nd\ne\n", "A\nb\nc\nd\ne\n", "s", "t1", base)
	record(t, p, store.KindExternal, "a.txt", "A\nb\nc\nd\ne\n", "A\nb\nC\nd\ne\n", "s", "t1", base.Add(time.Minute))
	edit(t, p, "a.txt", "A\nb\nC\nd\ne\n", "A\nb\nC\nd\nE\n", "s", "t1", base.Add(2*time.Minute))

	if _, err := Undo(p, UndoScope{Turn: "t1"}, Options{ThreeWay: true}); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(p.Abs("a.txt"))
	if string(got) != "a\nb\nC\nd\ne\n" {
		t.Fatalf("only Claude's two changes should be reversed: %q", got)
	}
}

func TestRestoreOverAnOutsideChange(t *testing.T) {
	p, _ := setup(t)
	base := time.Now().Add(-time.Hour)
	ev := edit(t, p, "f.txt", "", "one\ntwo\n", "s", "t1", base)
	edit(t, p, "f.txt", "one\ntwo\n", "one\ntwo\nthree\n", "s", "t1", base.Add(time.Minute))
	record(t, p, store.KindExternal, "f.txt", "one\ntwo\nthree\n", "zero\none\ntwo\nthree\n", "s", "t1", base.Add(2*time.Minute))

	_, err := Restore(p, ev, After, Options{})
	var mod *ErrModified
	if !asModified(err, &mod) || !mod.Outside {
		t.Fatalf("restore over a recorded outside change must refuse, got %v", err)
	}
	if _, err := Restore(p, ev, After, Options{ThreeWay: true}); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(p.Abs("f.txt"))
	if string(got) != "zero\none\ntwo\n" {
		t.Fatalf("three-way restore must keep the person's line: %q", got)
	}
}
