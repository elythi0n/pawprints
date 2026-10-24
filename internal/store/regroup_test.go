package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRegroupMovesScatteredSession(t *testing.T) {
	t.Setenv("PAWPRINTS_HOME", t.TempDir())
	ws := filepath.Join(t.TempDir(), "ws")
	sub := filepath.Join(ws, "capture")
	os.MkdirAll(sub, 0o755)
	parent, _ := OpenProject(ws)
	child, _ := OpenProject(sub)
	base := time.Now().Add(-time.Hour)

	h1, _ := child.PutBytes([]byte("one\n"))
	h2, _ := child.PutBytes([]byte("two\n"))
	evChild := Event{ID: NewID(base), TS: Now(base), Session: "S", Kind: KindEdit, Tool: "Edit", Path: "app.env", Before: HashPtr(h1), After: HashPtr(h2)}
	child.AppendEvent(evChild)
	child.UpdateSession("S", func(r *SessionRecord) { r.Edits = 1; r.AddFile("app.env"); r.CWD = sub })
	child.SetLast("app.env", LastKnown{After: h2, Event: evChild.ID, TS: evChild.TS})
	child.AddPin(evChild, "keep")
	// An unrelated session in the child stays put.
	child.AppendEvent(Event{ID: NewID(base.Add(time.Second)), TS: Now(base.Add(time.Second)), Session: "OTHER", Kind: KindEdit, Tool: "Edit", Path: "x"})

	hp, _ := parent.PutBytes([]byte("p\n"))
	later := base.Add(2 * time.Minute)
	parent.AppendEvent(Event{ID: NewID(later), TS: Now(later), Session: "S", Kind: KindEdit, Tool: "Write", Path: "README.md", After: HashPtr(hp)})
	parent.UpdateSession("S", func(r *SessionRecord) { r.Edits = 1; r.AddFile("README.md") })

	plan, err := Regroup(map[string]string{"S": ws}, true)
	if err != nil || plan.Events != 1 {
		t.Fatalf("plan %+v %v", plan, err)
	}
	rep, err := Regroup(map[string]string{"S": ws}, false)
	if err != nil || rep.Events != 1 || rep.Sessions != 1 || rep.Pins != 1 {
		t.Fatalf("report %+v %v", rep, err)
	}
	evs, _ := parent.Events(Query{Session: "S"})
	if len(evs) != 2 || evs[1].Path != "capture/app.env" || evs[0].Path != "README.md" {
		t.Fatalf("parent events %+v", evs)
	}
	if got, err := parent.Get(h2); err != nil || string(got) != "two\n" {
		t.Fatalf("blob not copied: %q %v", got, err)
	}
	rec, _ := parent.LoadSession("S")
	if rec.Edits != 2 || len(rec.Files) != 2 {
		t.Fatalf("merged record %+v", rec)
	}
	if !parent.IsPinned(evChild.ID) || parent.LoadLast()["capture/app.env"].After != h2 {
		t.Fatal("pin or last-known not moved")
	}
	left, _ := child.Events(Query{})
	if len(left) != 1 || left[0].Session != "OTHER" {
		t.Fatalf("child should keep only OTHER: %+v", left)
	}
	if again, _ := Regroup(map[string]string{"S": ws}, true); again.Events != 0 {
		t.Fatal("regroup must be idempotent")
	}
}

func TestFindProjectForAncestor(t *testing.T) {
	t.Setenv("PAWPRINTS_HOME", t.TempDir())
	ws := filepath.Join(t.TempDir(), "ws")
	os.MkdirAll(filepath.Join(ws, "a", "b"), 0o755)
	OpenProject(ws)
	p, ok := FindProjectFor(filepath.Join(ws, "a", "b"))
	if !ok || p.Root != ws {
		t.Fatalf("got %+v %v", p, ok)
	}
}

func TestRemoveEmptyProjects(t *testing.T) {
	t.Setenv("PAWPRINTS_HOME", t.TempDir())
	empty, _ := OpenProject(filepath.Join(t.TempDir(), "empty"))
	used, _ := OpenProject(filepath.Join(t.TempDir(), "used"))
	used.AppendEvent(Event{Session: "s", Kind: KindEdit, Path: "a"})
	old := time.Now().Add(-48 * time.Hour)
	os.Chtimes(filepath.Join(empty.Dir, "project.json"), old, old)
	os.Chtimes(filepath.Join(used.Dir, "project.json"), old, old)
	removed := RemoveEmptyProjects(24 * time.Hour)
	if len(removed) != 1 || removed[0] != empty.Root {
		t.Fatalf("removed %v", removed)
	}
	if _, ok := FindProject(used.Root); !ok {
		t.Fatal("a project with history must stay")
	}
}

// A moved session keeps its per-minute usage, how far each transcript was
// read, its account, and its subagents.
func TestRegroupKeepsUsageHistory(t *testing.T) {
	t.Setenv("PAWPRINTS_HOME", t.TempDir())
	src, _ := OpenProject(t.TempDir())
	dstRoot := t.TempDir()
	at := time.Now().Add(-time.Hour)
	src.AppendEvent(Event{ID: NewID(at), TS: Now(at), Session: "S", Kind: KindEdit, Tool: "Edit", Path: "a.go"})
	src.UpdateSession("S", func(r *SessionRecord) {
		r.TranscriptOffset, r.LastMessageID = 500, "msg_9"
		r.SubagentReads = map[string]TranscriptRead{"A1": {Offset: 120, LastID: "msg_x"}}
		r.Account = "acct"
		r.Usage = map[string]Usage{"m": {Output: 42}}
		r.Agents = []Agent{{ID: "A1", Type: "Explore", Turn: "T1"}}
	})
	bucket := UsageBucket{Minute: at.UTC().Truncate(time.Minute).Format(time.RFC3339), Model: "m", Usage: Usage{Output: 42}}
	if err := src.AppendUsage("S", []UsageBucket{bucket}); err != nil {
		t.Fatal(err)
	}
	if _, err := Regroup(map[string]string{"S": dstRoot}, false); err != nil {
		t.Fatal(err)
	}
	dst, ok := FindProject(dstRoot)
	if !ok {
		t.Fatal("destination project")
	}
	rec, _ := dst.LoadSession("S")
	if rec.TranscriptOffset != 500 || rec.LastMessageID != "msg_9" || rec.SubagentReads["A1"].Offset != 120 || rec.Account != "acct" {
		t.Fatalf("read state carried over: %+v", rec)
	}
	if len(rec.Agents) != 1 || rec.Agents[0].Turn != "T1" {
		t.Fatalf("subagents carried over: %+v", rec.Agents)
	}
	got, _ := dst.SessionUsage("S")
	if len(got) != 1 || got[0].Output != 42 {
		t.Fatalf("usage log moved: %+v", got)
	}
}
