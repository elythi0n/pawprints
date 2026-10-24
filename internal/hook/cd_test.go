package hook

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/elythi0n/pawprints/internal/store"
)

// A session launched in a non-git workspace that cd's into subfolders must
// keep every edit in one project.
func TestSessionStaysInLaunchProject(t *testing.T) {
	t.Setenv("PAWPRINTS_HOME", t.TempDir())
	claude := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", claude)
	ws := filepath.Join(t.TempDir(), "workspace")
	sub := filepath.Join(ws, "go", "services", "billing")
	other := filepath.Join(ws, "capture")
	for _, d := range []string{sub, other} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Claude Code's registry says the session was launched in the workspace.
	os.MkdirAll(filepath.Join(claude, "sessions"), 0o755)
	reg, _ := json.Marshal(map[string]any{"pid": 1, "sessionId": "S", "cwd": ws})
	os.WriteFile(filepath.Join(claude, "sessions", "1.json"), reg, 0o644)

	edit := func(cwd, file, id string) {
		var stderr bytes.Buffer
		in := func() *bytes.Reader {
			data, _ := json.Marshal(map[string]any{"session_id": "S", "cwd": cwd, "tool_name": "Write", "tool_use_id": id, "tool_input": map[string]any{"file_path": file}})
			return bytes.NewReader(data)
		}
		Run("pre", in(), &stderr)
		os.WriteFile(file, []byte(id+"\n"), 0o644)
		Run("post", in(), &stderr)
		if stderr.Len() > 0 {
			t.Fatal(stderr.String())
		}
	}
	edit(sub, filepath.Join(sub, "main.go"), "a")
	edit(other, filepath.Join(other, "app.env"), "b")
	edit(ws, filepath.Join(ws, "README.md"), "c")
	outside := filepath.Join(t.TempDir(), "scratch.txt")
	edit(other, outside, "d")

	projects, _ := store.ListProjects()
	if len(projects) != 1 || projects[0].Root != ws {
		var roots []string
		for _, p := range projects {
			roots = append(roots, p.Root)
		}
		t.Fatalf("expected one project at %s, got %v", ws, roots)
	}
	evs, _ := projects[0].Events(store.Query{})
	want := map[string]bool{"go/services/billing/main.go": true, "capture/app.env": true, "README.md": true, outside: true}
	for _, ev := range evs {
		delete(want, ev.Path)
	}
	if len(want) != 0 {
		t.Fatalf("missing paths %v", want)
	}
	rec, _ := projects[0].LoadSession("S")
	if rec.Edits != 4 || len(rec.Files) != 4 {
		t.Fatalf("record %+v", rec)
	}
	last := projects[0].LoadLast()
	if last["README.md"].Event == "" {
		t.Fatal("last-known entries must name their event")
	}
}
