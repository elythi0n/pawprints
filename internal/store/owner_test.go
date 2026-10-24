package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOwnerAndHoldsProjects(t *testing.T) {
	roots := []string{"/w/backend/billing", "/w/backend/php-to-go", "/w/backend/php-to-go/tools"}
	cases := map[string]string{
		"/w/backend/billing/app/x.php":     "/w/backend/billing",
		"/w/backend/php-to-go/main.go":     "/w/backend/php-to-go",
		"/w/backend/php-to-go/tools/a.go":  "/w/backend/php-to-go/tools",
		"/w/backend/notes.txt":             "",
		"/w/backend/billing-old/app/x.php": "",
		"/tmp/out.txt":                     "",
	}
	for path, want := range cases {
		if got := Owner(roots, path); got != want {
			t.Errorf("Owner(%s) = %q, want %q", path, got, want)
		}
	}
	own := "/w/backend/php-to-go"
	for dir, want := range map[string]bool{
		"/w/backend":         true, // the parent of this and other projects
		"/w":                 true,
		"/w/backend/billing": false, // another project itself is fine
		"/w/wt/feature":      false, // a worktree
	} {
		if got := HoldsProjects(dir, own, roots); got != want {
			t.Errorf("HoldsProjects(%s) = %v, want %v", dir, got, want)
		}
	}
}

func TestPendingLifecycle(t *testing.T) {
	t.Setenv("PAWPRINTS_HOME", t.TempDir())
	p, _ := OpenProject(t.TempDir())
	now := time.Now()
	p.WritePending("fg", Pending{Session: "S", StartNano: now.UnixNano()})
	p.WritePending("bg", Pending{Session: "S", StartNano: now.UnixNano(), Background: true})
	p.WritePending("left", Pending{Session: "S", StartNano: now.Add(-2 * time.Hour).UnixNano()})
	p.WritePending("old-bg", Pending{Session: "T", StartNano: now.Add(-3 * time.Hour).UnixNano(), Background: true})
	p.WritePending("edit", Pending{Session: "T", TS: Now(now.Add(-time.Minute))})

	running := p.Running(now)
	if len(running) != 3 || running["edit"].Start().IsZero() {
		t.Fatalf("running %v", running)
	}
	if _, err := os.Stat(filepath.Join(p.Dir, "pending", "left.json")); !os.IsNotExist(err) {
		t.Fatal("a record left behind is removed")
	}
	p.DonePending("fg")
	p.DonePending("bg")
	if _, ok := p.ReadPending("fg"); ok {
		t.Fatal("a finished call is done")
	}
	if _, ok := p.ReadPending("bg"); !ok {
		t.Fatal("a background command may still run")
	}
	p.EndBackground("S")
	if _, ok := p.ReadPending("bg"); ok {
		t.Fatal("its session ended")
	}
	if n := p.StalePending(); n != 0 {
		t.Fatalf("the edit is still running, removed %d", n)
	}
}

func TestUntangleDropsWhatCrossedProjectLines(t *testing.T) {
	t.Setenv("PAWPRINTS_HOME", t.TempDir())
	backend := t.TempDir()
	mine := filepath.Join(backend, "php-to-go")
	theirs := filepath.Join(backend, "billing")
	p, _ := OpenProject(mine)
	if _, err := OpenProject(theirs); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-time.Hour)
	add := func(i int, kind, path string, added int) {
		ts := at.Add(time.Duration(i) * time.Second)
		p.AppendEvent(Event{ID: NewID(ts), TS: Now(ts), Session: "S", Kind: kind, Tool: "Bash", Path: path, LinesAdded: added})
	}
	add(0, KindEdit, "plan.md", 5)
	add(1, KindExternal, "notes.md", 1)                                  // a person, in this project: kept
	add(2, KindExternal, filepath.Join(theirs, "app", "Report.php"), 40) // another session's work: dropped
	add(3, KindEdit, filepath.Join(theirs, "app", "Built.php"), 2)       // Claude's own command: kept
	add(4, KindExternal, "/tmp/scratch.txt", 3)                          // outside every project: dropped
	p.UpdateSession("S", func(r *SessionRecord) {
		r.Edits, r.LinesAdded = 5, 51
		r.Files = []string{"plan.md", "notes.md", filepath.Join(theirs, "app", "Report.php"), filepath.Join(theirs, "app", "Built.php"), "/tmp/scratch.txt"}
		r.ExtraRoots = []string{backend, "/srv/wt"}
	})

	plan, err := Untangle(true)
	if err != nil || plan.Events != 2 || plan.Roots != 1 {
		t.Fatalf("plan %+v %v", plan, err)
	}
	if evs, _ := p.Events(Query{}); len(evs) != 5 {
		t.Fatal("a dry run changes nothing")
	}
	if _, err := Untangle(false); err != nil {
		t.Fatal(err)
	}
	evs, _ := p.Events(Query{})
	if len(evs) != 3 {
		t.Fatalf("left %+v", evs)
	}
	rec, _ := p.LoadSession("S")
	if rec.Edits != 3 || rec.LinesAdded != 8 || len(rec.Files) != 3 || len(rec.ExtraRoots) != 1 || rec.ExtraRoots[0] != "/srv/wt" {
		t.Fatalf("record %+v", rec)
	}
	if again, _ := Untangle(true); again.Events != 0 || again.Roots != 0 {
		t.Fatalf("nothing is left to do: %+v", again)
	}
}
