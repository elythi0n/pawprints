package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elythi0n/pawprints/internal/store"
)

func run(t *testing.T, args ...string) (string, int) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := RunWith(args, strings.NewReader(""), &out, &errOut)
	return out.String(), code
}

func lines(t *testing.T, s string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(s), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("not JSON: %q: %v", line, err)
		}
		out = append(out, m)
	}
	return out
}

func seed(t *testing.T) (*store.Project, string) {
	t.Helper()
	t.Setenv("PAWPRINTS_HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := filepath.Join(t.TempDir(), "proj")
	os.MkdirAll(root, 0o755)
	p, err := store.OpenProject(root)
	if err != nil {
		t.Fatal(err)
	}
	h1, _ := p.PutBytes([]byte("one\n"))
	h2, _ := p.PutBytes([]byte("one\ntwo\n"))
	os.WriteFile(filepath.Join(root, "f.txt"), []byte("one\ntwo\n"), 0o644)
	base := time.Now().Add(-time.Minute)
	ev := store.Event{TS: store.Now(base), Session: "sess", Turn: "TURN1", Tool: "Edit", Kind: store.KindEdit, Path: "f.txt", Before: store.HashPtr(h1), After: store.HashPtr(h2), LinesAdded: 1}
	ev.ID = store.NewID(base)
	if err := p.AppendEvent(ev); err != nil {
		t.Fatal(err)
	}
	p.SetLast("f.txt", store.LastKnown{After: h2, Event: ev.ID, TS: ev.TS})
	p.UpdateSession("sess", func(r *store.SessionRecord) {
		r.Edits = 1
		r.AddFile("f.txt")
		r.Turns = []store.Turn{{ID: "TURN1", At: store.Now(base), Text: "add two"}}
		r.LastPrompt = "add two"
	})
	return p, root
}

func TestJSONContract(t *testing.T) {
	_, root := seed(t)
	proj := "--project=" + root

	out, code := run(t, "log", "--json", proj)
	if code != 0 {
		t.Fatalf("log exit %d: %s", code, out)
	}
	evs := lines(t, out)
	if len(evs) != 1 {
		t.Fatalf("log lines %d", len(evs))
	}
	for _, key := range []string{"v", "id", "ts", "session", "turn", "tool", "kind", "path", "before", "after", "lines_added", "lines_removed", "project_root"} {
		if _, ok := evs[0][key]; !ok {
			t.Fatalf("log event missing %q: %v", key, evs[0])
		}
	}
	id := evs[0]["id"].(string)

	out, code = run(t, "show", id[:8], "--json", proj)
	if code != 0 {
		t.Fatalf("show: %s", out)
	}
	show := lines(t, out)[0]
	if show["before"] != "one\n" || show["after"] != "one\ntwo\n" || !strings.Contains(show["diff"].(string), "+two") {
		t.Fatalf("show %v", show)
	}

	out, _ = run(t, "diff", id, "--against", "current", "--json", proj)
	d := lines(t, out)[0]
	if d["against"] != "current" || d["diff"] != "" {
		t.Fatalf("diff against current should be empty: %v", d)
	}

	out, _ = run(t, "sessions", "--json", proj)
	s := lines(t, out)[0]
	if s["id"] != "sess" || s["edits"].(float64) != 1 || s["status"] == "" {
		t.Fatalf("sessions %v", s)
	}

	out, _ = run(t, "turns", "--json", "--session", "sess", proj)
	tu := lines(t, out)[0]
	if tu["id"] != "TURN1" || tu["edits"].(float64) != 1 {
		t.Fatalf("turns %v", tu)
	}

	out, code = run(t, "pin", id, "-m", "keep", "--json", proj)
	if code != 0 || lines(t, out)[0]["message"] != "keep" {
		t.Fatalf("pin %s", out)
	}
	out, _ = run(t, "pins", "--json", proj)
	if len(lines(t, out)) != 1 {
		t.Fatal("pins")
	}

	out, _ = run(t, "export", "--session", "sess", "--json", proj)
	ex := lines(t, out)[0]
	if ex["files"].(float64) != 1 || !strings.Contains(ex["patch"].(string), "+two") {
		t.Fatalf("export %v", ex)
	}

	out, _ = run(t, "stats", "--json", proj)
	st := lines(t, out)[0]
	if st["edits"].(float64) != 1 || st["sessions"].(float64) != 1 || st["lines_added"].(float64) != 1 {
		t.Fatalf("stats %v", st)
	}

	out, code = run(t, "undo", "--turn", "TURN1", "--dry-run", "--json", proj)
	if code != 0 {
		t.Fatalf("undo dry: %s", out)
	}
	u := lines(t, out)[0]
	if u["applied"] != false || len(u["changes"].([]any)) != 1 {
		t.Fatalf("undo plan %v", u)
	}
	out, code = run(t, "undo", "--turn", "TURN1", "--yes", "--json", proj)
	if code != 0 {
		t.Fatalf("undo: %s", out)
	}
	got, _ := os.ReadFile(filepath.Join(root, "f.txt"))
	if string(got) != "one\n" {
		t.Fatalf("after undo %q", got)
	}

	out, code = run(t, "clear", "--session", "sess", "--dry-run", "--json", proj)
	if code != 0 {
		t.Fatalf("clear: %s", out)
	}
	cl := lines(t, out)[0]
	if cl["scope"] != "session sess" || cl["kept_pins"].(float64) != 1 {
		t.Fatalf("clear %v", cl)
	}
	if _, has := cl["cut_at"]; has {
		t.Fatal("cut_at must be omitted when there is no cut")
	}

	out, _ = run(t, "info", "--json", proj)
	if lines(t, out)[0]["tracked"] != true {
		t.Fatalf("info %s", out)
	}

	out, code = run(t, "show", "NOPE", "--json", proj)
	e := lines(t, out)[0]
	if code != 1 || e["exit"].(float64) != 1 || !strings.Contains(e["error"].(string), "no event") {
		t.Fatalf("error envelope %v %d", e, code)
	}

	out, code = run(t, "doctor", "--json", proj)
	if _, ok := lines(t, out)[0]["checks"]; !ok {
		t.Fatalf("doctor %s", out)
	}
	_ = code
}

func TestFlagsAfterPositionals(t *testing.T) {
	fs := newFlags("x")
	n := fs.Int("limit", 0, "")
	b := fs.Bool("yes", false, "")
	s := fs.String("m", "", "")
	if err := parseArgs(fs, []string{"ID", "--limit", "5", "--yes", "-m", "hello world", "--", "-literal"}); err != nil {
		t.Fatal(err)
	}
	if *n != 5 || !*b || *s != "hello world" || fs.NArg() != 2 || fs.Arg(1) != "-literal" {
		t.Fatalf("parsed %d %v %q %v", *n, *b, *s, fs.Args())
	}
}

func TestParseSpan(t *testing.T) {
	cases := map[string]time.Duration{
		"30min": 30 * time.Minute, "2h": 2 * time.Hour, "3d": 72 * time.Hour,
		"2w": 14 * 24 * time.Hour, "3m": 90 * 24 * time.Hour, "1y": 365 * 24 * time.Hour, "1h30m": 0,
	}
	for in, want := range cases {
		got, err := parseSpan(in)
		if in == "1h30m" {
			// "m" means months here, so this reads as 1h + 30 months is not
			// what anyone wants; the parser falls back to Go's syntax.
			if err != nil || got != 90*time.Minute {
				t.Fatalf("%s: %v %v", in, got, err)
			}
			continue
		}
		if err != nil || got != want {
			t.Fatalf("%s: got %v want %v (%v)", in, got, want, err)
		}
	}
}
