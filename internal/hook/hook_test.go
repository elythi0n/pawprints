package hook

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/elythi0n/pawprints/internal/store"
)

func input(t *testing.T, event, tool, useID, cwd, file string, extra map[string]any) *bytes.Reader {
	t.Helper()
	m := map[string]any{
		"session_id":      "sess-1",
		"transcript_path": "/nonexistent",
		"cwd":             cwd,
		"hook_event_name": event,
		"tool_name":       tool,
		"tool_use_id":     useID,
		"tool_input":      map[string]any{"file_path": file},
	}
	for k, v := range extra {
		m[k] = v
	}
	data, _ := json.Marshal(m)
	return bytes.NewReader(data)
}

func TestEditRoundTripAndExternalCapture(t *testing.T) {
	t.Setenv("PAWPRINTS_HOME", t.TempDir())
	root := filepath.Join(t.TempDir(), "proj")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "main.go")
	if err := os.WriteFile(file, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer

	// A prompt first, so the edit lands in a turn.
	if code := Run("prompt", input(t, "UserPromptSubmit", "", "", root, "", map[string]any{"prompt": "  add a   main function  "}), &stderr); code != 0 {
		t.Fatal("hooks must exit 0")
	}
	Run("pre", input(t, "PreToolUse", "Edit", "toolu_1", root, file, nil), &stderr)
	if err := os.WriteFile(file, []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	Run("post", input(t, "PostToolUse", "Edit", "toolu_1", root, file, nil), &stderr)
	if stderr.Len() != 0 {
		t.Fatalf("stderr: %s", stderr.String())
	}

	p, ok := store.FindProject(root)
	if !ok {
		t.Fatal("project not created")
	}
	evs, _ := p.Events(store.Query{})
	if len(evs) != 1 {
		t.Fatalf("expected 1 event, got %d", len(evs))
	}
	ev := evs[0]
	if ev.Kind != store.KindEdit || ev.Path != "main.go" || ev.Tool != "Edit" || ev.Turn == "" {
		t.Fatalf("event %+v", ev)
	}
	if ev.LinesAdded != 2 || ev.LinesRemoved != 0 {
		t.Fatalf("line stats %d/%d", ev.LinesAdded, ev.LinesRemoved)
	}
	before, _ := p.Get(ev.BeforeHash())
	after, _ := p.Get(ev.AfterHash())
	if string(before) != "package main\n" || string(after) != "package main\n\nfunc main() {}\n" {
		t.Fatalf("blobs %q %q", before, after)
	}
	rec, _ := p.LoadSession("sess-1")
	if rec.Edits != 1 || len(rec.Files) != 1 || rec.LastPrompt != "add a main function" || len(rec.Turns) != 1 || rec.LinesAdded != 2 {
		t.Fatalf("record %+v", rec)
	}
	if _, found := p.TakePending("toolu_1"); found {
		t.Fatal("pending record must be consumed")
	}

	// Something else edits the file (sed, a formatter): the next pre hook records it.
	if err := os.WriteFile(file, []byte("package main\n\nfunc main() { println(1) }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	Run("pre", input(t, "PreToolUse", "Write", "toolu_2", root, file, nil), &stderr)
	if err := os.WriteFile(file, []byte("rewritten\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	Run("post", input(t, "PostToolUse", "Write", "toolu_2", root, file, nil), &stderr)
	evs, _ = p.Events(store.Query{})
	if len(evs) != 3 {
		t.Fatalf("expected external + edit, got %d events", len(evs))
	}
	if evs[1].Kind != store.KindExternal || evs[1].BeforeHash() != ev.AfterHash() {
		t.Fatalf("external event wrong: %+v", evs[1])
	}
	if evs[0].BeforeHash() != evs[1].AfterHash() {
		t.Fatal("the edit must start from the externally changed content")
	}

	// Session lifecycle.
	Run("stop", input(t, "Stop", "", "", root, "", nil), &stderr)
	Run("session-end", input(t, "SessionEnd", "", "", root, "", map[string]any{"reason": "exit"}), &stderr)
	rec, _ = p.LoadSession("sess-1")
	if rec.EndedAt == "" || rec.EndReason != "exit" || rec.LastStopAt == "" {
		t.Fatalf("lifecycle %+v", rec)
	}
	Run("session-start", input(t, "SessionStart", "", "", root, "", map[string]any{"source": "resume"}), &stderr)
	rec, _ = p.LoadSession("sess-1")
	if rec.EndedAt != "" || rec.StartSource != "resume" {
		t.Fatalf("resume must clear the end: %+v", rec)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr: %s", stderr.String())
	}
}

func TestIgnoredAndMissingPaths(t *testing.T) {
	t.Setenv("PAWPRINTS_HOME", t.TempDir())
	root := t.TempDir()
	var stderr bytes.Buffer
	Run("pre", input(t, "PreToolUse", "Edit", "x", root, filepath.Join(root, "node_modules", "a.js"), nil), &stderr)
	Run("post", input(t, "PostToolUse", "Edit", "x", root, filepath.Join(root, "node_modules", "a.js"), nil), &stderr)
	Run("post", input(t, "PostToolUse", "Bash", "y", root, "", nil), &stderr)
	if code := Run("post", bytes.NewReader([]byte("not json")), &stderr); code != 0 {
		t.Fatal("bad input must still exit 0")
	}
	if !bytes.Contains(stderr.Bytes(), []byte("hook input")) {
		t.Fatal("bad input should be reported on stderr")
	}
	if p, ok := store.FindProject(root); ok {
		evs, _ := p.Events(store.Query{})
		if len(evs) != 0 {
			t.Fatalf("ignored path produced events: %+v", evs)
		}
	}
}

func TestReadablePrompt(t *testing.T) {
	cases := map[string]string{
		"<task-notification> <task-id>x</task-id></task-notification>":                                                 "(background task finished)",
		"<command-message>ask</command-message>\n<command-name>/ask</command-name>\n<command-args>why?</command-args>": "/ask why?",
		"plain words":                  "plain words",
		"<system>hello</system> there": "hello there",
	}
	for in, want := range cases {
		if got := truncatePrompt(in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}

// With the plugin active, a call that did not come through the plugin's
// wrapper records nothing, so settings.json hooks and the plugin never both
// record the same edit.
func TestNoDoubleRecordingWithPlugin(t *testing.T) {
	t.Setenv("PAWPRINTS_HOME", t.TempDir())
	claude := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", claude)
	os.MkdirAll(filepath.Join(claude, "plugins"), 0o755)
	os.WriteFile(filepath.Join(claude, "plugins", "installed_plugins.json"), []byte(`{"plugins":{"pawprints@pawprints":[{}]}}`), 0o644)
	root := t.TempDir()
	file := filepath.Join(root, "a.txt")
	os.WriteFile(file, []byte("a\n"), 0o644)
	var stderr bytes.Buffer
	for _, via := range []string{"", "1"} {
		t.Setenv("PAWPRINTS_VIA_PLUGIN", via)
		id := "t" + via
		Run("pre", input(t, "PreToolUse", "Edit", id, root, file, nil), &stderr)
		os.WriteFile(file, []byte("a\n"+id+"\n"), 0o644)
		Run("post", input(t, "PostToolUse", "Edit", id, root, file, nil), &stderr)
	}
	p, ok := store.FindProject(root)
	if !ok {
		t.Fatal("the plugin's call should have recorded")
	}
	evs, _ := p.Events(store.Query{})
	if len(evs) != 1 || evs[0].ToolUseID != "t1" {
		t.Fatalf("expected only the plugin's edit, got %d events", len(evs))
	}
}
