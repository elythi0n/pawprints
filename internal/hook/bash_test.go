package hook

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/elythi0n/pawprints/internal/store"
)

func TestBashEditsAreRecorded(t *testing.T) {
	t.Setenv("PAWPRINTS_HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := filepath.Join(t.TempDir(), "repo")
	os.MkdirAll(filepath.Join(root, "bin"), 0o755)
	git := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s", args, out)
		}
	}
	exec.Command("git", "init", "-q", root).Run()
	os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("from git\n"), 0o644)
	os.WriteFile(filepath.Join(root, "gone.txt"), []byte("bye\n"), 0o644)
	os.WriteFile(filepath.Join(root, ".gitignore"), []byte("bin/\n"), 0o644)
	git("add", ".")

	send := func(event, id, command string) {
		data, _ := json.Marshal(map[string]any{"session_id": "S", "cwd": root, "tool_name": "Bash", "tool_use_id": id, "tool_input": map[string]any{"command": command}})
		var stderr bytes.Buffer
		Run(event, bytes.NewReader(data), &stderr)
		if stderr.Len() > 0 {
			t.Fatal(stderr.String())
		}
	}
	// The store has seen gone.txt, so its removal can be recorded.
	send("bash-pre", "b0", "cat gone.txt")
	send("bash-post", "b0", "cat gone.txt")
	p, _ := store.FindProject(root)
	h, _ := p.PutBytes([]byte("bye\n"))
	p.SetLast("gone.txt", store.LastKnown{After: h, Event: "X", TS: store.Now(time.Now())})

	send("bash-pre", "b1", "python3 rewrite.py")
	time.Sleep(10 * time.Millisecond)
	os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("from git\nand python\n"), 0o644)
	os.WriteFile(filepath.Join(root, "new.txt"), []byte("brand new\n"), 0o644)
	os.WriteFile(filepath.Join(root, "bin", "app"), []byte("\x00binary build output"), 0o644)
	os.Remove(filepath.Join(root, "gone.txt"))
	send("bash-post", "b1", "python3 rewrite.py")

	evs, _ := p.Events(store.Query{})
	byPath := map[string]store.Event{}
	for _, ev := range evs {
		byPath[ev.Path] = ev
	}
	tr, ok := byPath["tracked.txt"]
	if !ok || tr.Tool != "Bash" || tr.Command != "python3 rewrite.py" || tr.LinesAdded != 1 || tr.BeforeUnknown {
		t.Fatalf("tracked.txt should diff against git: %+v", tr)
	}
	if before, _ := p.Get(tr.BeforeHash()); string(before) != "from git\n" {
		t.Fatalf("before from git index: %q", before)
	}
	nw, ok := byPath["new.txt"]
	if !ok || !nw.BeforeUnknown {
		t.Fatalf("new.txt: %+v", nw)
	}
	if _, ok := byPath["bin/app"]; ok {
		t.Fatal("gitignored build output must not be recorded")
	}
	if g, ok := byPath["gone.txt"]; !ok || g.After != nil {
		t.Fatalf("deletion not recorded: %+v", g)
	}
	// A second command that changes nothing records nothing.
	send("bash-pre", "b2", "ls")
	send("bash-post", "b2", "ls")
	again, _ := p.Events(store.Query{})
	if len(again) != len(evs) {
		t.Fatalf("no-op command added %d events", len(again)-len(evs))
	}
	rec, _ := p.LoadSession("S")
	if rec.Edits != len(evs) {
		t.Fatalf("session counters %d vs %d", rec.Edits, len(evs))
	}
}
