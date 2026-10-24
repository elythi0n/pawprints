package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newTestProject(t *testing.T) *Project {
	t.Helper()
	home := t.TempDir()
	t.Setenv("PAWPRINTS_HOME", home)
	root := filepath.Join(t.TempDir(), "proj")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	p, err := OpenProject(root)
	if err != nil {
		t.Fatalf("open project: %v", err)
	}
	return p
}

func TestObjectsRoundTrip(t *testing.T) {
	p := newTestProject(t)
	h, err := p.PutBytes([]byte("hello\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(h) != 40 && len(h) != 64 {
		t.Fatalf("unexpected hash %q", h)
	}
	got, err := p.Get(h)
	if err != nil || string(got) != "hello\n" {
		t.Fatalf("get: %q %v", got, err)
	}
	h2, _ := p.PutBytes([]byte("hello\n"))
	if h2 != h {
		t.Fatal("identical content must hash identically")
	}
	if !p.Has(h) || p.Has("0000000000000000000000000000000000000000") {
		t.Fatal("Has is wrong")
	}
}

func TestEventsAppendQueryOrder(t *testing.T) {
	p := newTestProject(t)
	base := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		ts := base.Add(time.Duration(i) * time.Hour)
		if i >= 3 {
			ts = ts.AddDate(0, 1, 0) // next month
		}
		ev := Event{TS: Now(ts), Session: "s1", Tool: "Edit", Kind: KindEdit, Path: "a.go"}
		if i == 4 {
			ev.Session = "s2"
		}
		if err := p.AppendEvent(ev); err != nil {
			t.Fatal(err)
		}
	}
	months, _ := p.Months()
	if len(months) != 2 || months[0] != "2026-09" || months[1] != "2026-10" {
		t.Fatalf("months %v", months)
	}
	all, err := p.Events(Query{})
	if err != nil || len(all) != 5 {
		t.Fatalf("events: %d %v", len(all), err)
	}
	for i := 1; i < len(all); i++ {
		if all[i-1].ID < all[i].ID {
			t.Fatal("events must be newest first")
		}
	}
	s2, _ := p.Events(Query{Session: "s2"})
	if len(s2) != 1 {
		t.Fatalf("session filter: %d", len(s2))
	}
	lim, _ := p.Events(Query{Limit: 2})
	if len(lim) != 2 {
		t.Fatalf("limit: %d", len(lim))
	}
	since, _ := p.Events(Query{Since: base.AddDate(0, 1, 0)})
	if len(since) != 2 {
		t.Fatalf("since: %d", len(since))
	}
	ev, err := p.EventByPrefix(all[0].ID[:10])
	if err != nil || ev.ID != all[0].ID {
		t.Fatalf("prefix: %v", err)
	}
}

func TestPreviousVersion(t *testing.T) {
	p := newTestProject(t)
	base := time.Now().Add(-time.Hour)
	h1, h2, h3 := "a", "b", "c"
	ev1 := Event{TS: Now(base), Session: "s", Tool: "Write", Kind: KindEdit, Path: "f", After: &h1}
	ev2 := Event{TS: Now(base.Add(time.Minute)), Session: "s", Tool: "Edit", Kind: KindEdit, Path: "f", Before: &h1, After: &h2}
	ev3 := Event{TS: Now(base.Add(2 * time.Minute)), Session: "s", Tool: "Edit", Kind: KindEdit, Path: "f", Before: &h2, After: &h3}
	for _, ev := range []Event{ev1, ev2, ev3} {
		if err := p.AppendEvent(ev); err != nil {
			t.Fatal(err)
		}
	}
	all, _ := p.Events(Query{Path: "f"})
	prev, ok := p.PreviousVersion(all[0])
	if !ok || prev.AfterHash() != h2 {
		t.Fatalf("previous of newest should be h2, got %v %v", prev, ok)
	}
	_, ok = p.PreviousVersion(all[2])
	if ok {
		t.Fatal("oldest has no previous")
	}
}

func TestSessionRecordAtomicUpdate(t *testing.T) {
	p := newTestProject(t)
	for i := 0; i < 3; i++ {
		err := p.UpdateSession("abc", func(r *SessionRecord) {
			r.Edits++
			r.AddFile("x.go")
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	rec, ok := p.LoadSession("abc")
	if !ok || rec.Edits != 3 || len(rec.Files) != 1 {
		t.Fatalf("record %+v %v", rec, ok)
	}
	entries, _ := os.ReadDir(filepath.Join(p.Dir, "sessions"))
	if len(entries) != 1 {
		t.Fatalf("temp files left behind: %d", len(entries))
	}
}

func TestPinsAndPrune(t *testing.T) {
	p := newTestProject(t)
	old := time.Now().AddDate(0, -5, 0)
	hOld, _ := p.PutBytes([]byte("old\n"))
	hNew, _ := p.PutBytes([]byte("new\n"))
	hPinned, _ := p.PutBytes([]byte("pinned\n"))
	if err := p.AppendEvent(Event{TS: Now(old), Session: "s", Tool: "Write", Kind: KindEdit, Path: "o", After: &hOld}); err != nil {
		t.Fatal(err)
	}
	if err := p.AppendEvent(Event{TS: Now(old.Add(time.Minute)), Session: "s", Tool: "Write", Kind: KindEdit, Path: "p", After: &hPinned}); err != nil {
		t.Fatal(err)
	}
	if err := p.AppendEvent(Event{TS: Now(time.Now()), Session: "s", Tool: "Write", Kind: KindEdit, Path: "n", After: &hNew}); err != nil {
		t.Fatal(err)
	}
	evs, _ := p.Events(Query{Path: "p"})
	if _, err := p.AddPin(evs[0], "keep me"); err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	rep, err := p.Prune(cfg, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if rep.MonthsRemoved != 1 {
		t.Fatalf("months removed %d", rep.MonthsRemoved)
	}
	if p.Has(hOld) {
		t.Fatal("unreferenced old blob should be gone")
	}
	if !p.Has(hNew) || !p.Has(hPinned) {
		t.Fatal("referenced and pinned blobs must survive")
	}
	after, _ := p.Events(Query{})
	if len(after) != 2 {
		t.Fatalf("expected new + re-appended pinned event, got %d", len(after))
	}
	if _, err := p.RemovePin(evs[0].ID[:8]); err != nil {
		t.Fatal(err)
	}
	pins, _ := p.Pins()
	if len(pins) != 0 {
		t.Fatal("pin not removed")
	}
}

func TestClearScopes(t *testing.T) {
	p := newTestProject(t)
	h, _ := p.PutBytes([]byte("x\n"))
	for _, s := range []string{"a", "a", "b"} {
		if err := p.AppendEvent(Event{TS: Now(time.Now()), Session: s, Tool: "Write", Kind: KindEdit, Path: "f-" + s, After: &h}); err != nil {
			t.Fatal(err)
		}
		_ = p.UpdateSession(s, func(r *SessionRecord) { r.Edits++ })
	}
	plan, err := p.PlanClear(ClearScope{Session: "a"}, time.Now())
	if err != nil || plan.Events != 2 || plan.Sessions != 1 {
		t.Fatalf("plan %+v %v", plan, err)
	}
	if _, err := p.Clear(ClearScope{Session: "a"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	left, _ := p.Events(Query{})
	if len(left) != 1 || left[0].Session != "b" {
		t.Fatalf("left %+v", left)
	}
	if _, ok := p.LoadSession("a"); ok {
		t.Fatal("session a record should be gone")
	}
	if _, err := p.Clear(ClearScope{Project: true}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p.Dir); !os.IsNotExist(err) {
		t.Fatal("project dir should be removed")
	}
}

func TestIgnore(t *testing.T) {
	ig := NewIgnore([]string{".git/", "node_modules/", "dist/**", "*.lock", "build/", "/top.txt", "!keep.lock", "# comment"})
	cases := map[string]bool{
		".git/config":            true,
		"src/.git/x":             true,
		"node_modules/a/b.js":    true,
		"deep/node_modules/x.js": true,
		"go.sum":                 false,
		"dist/a/b.js":            true,
		"pkg/dist/a.js":          false,
		"pnpm.lock":              true,
		"keep.lock":              false,
		"build/out.o":            true,
		"src/build/out.o":        true,
		"top.txt":                true,
		"sub/top.txt":            false,
		"src/main.go":            false,
	}
	for path, want := range cases {
		if got := ig.Match(path); got != want {
			t.Errorf("%s: got %v want %v", path, got, want)
		}
	}
}

func TestRelAbs(t *testing.T) {
	p := newTestProject(t)
	inside := filepath.Join(p.Root, "a", "b.go")
	if rel := p.Rel(inside); rel != "a/b.go" {
		t.Fatalf("rel %q", rel)
	}
	if abs := p.Abs("a/b.go"); abs != inside {
		t.Fatalf("abs %q", abs)
	}
	outside := "/etc/hosts"
	if rel := p.Rel(outside); rel != outside {
		t.Fatalf("outside should stay absolute, got %q", rel)
	}
}

func TestFormatVersionGuard(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PAWPRINTS_HOME", home)
	if err := os.WriteFile(filepath.Join(home, "format"), []byte("99\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureHome(); err == nil {
		t.Fatal("newer format must be refused")
	}
}

// Clearing a project also removes the pins that tie its sessions to it.
func TestClearProjectUnpinsItsSessions(t *testing.T) {
	p := newTestProject(t)
	other := filepath.Join(t.TempDir(), "other")
	PinSessionRoot("mine", p.Root, true)
	PinSessionRoot("theirs", other, true)
	p.AppendEvent(Event{Session: "mine", Kind: KindEdit, Tool: "Edit", Path: "a"})
	if _, err := p.Clear(ClearScope{Project: true}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, ok := SessionRoot("mine"); ok {
		t.Fatal("the cleared project's session pin is gone")
	}
	if root, ok := SessionRoot("theirs"); !ok || root != other {
		t.Fatal("another project's pin stays")
	}
}
