package sessions

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/elythi0n/pawprints/internal/store"
)

func TestSubagentFiles(t *testing.T) {
	dir := t.TempDir()
	session := filepath.Join(dir, "S.jsonl")
	path := SubagentTranscript(session, "A1")
	if path != filepath.Join(dir, "S", "subagents", "agent-A1.jsonl") {
		t.Fatal(path)
	}
	if SubagentReport(path) != "" || SubagentDescription(path) != "" {
		t.Fatal("missing files mean nothing to show")
	}
	os.MkdirAll(filepath.Dir(path), 0o755)
	os.WriteFile(strings0(path, ".meta.json"), []byte(`{"description":"  Scan   the commands "}`), 0o644)
	// A foreground agent ends with a plain answer, no hand-back call.
	os.WriteFile(path, []byte(`{"type":"assistant","message":{"content":[{"type":"text","text":"first"}]}}
not json
{"type":"assistant","message":{"content":[{"type":"text","text":"All done: 3 files."},{"type":"tool_use","name":"Read","input":{}}]}}
{"type":"user","message":{"content":"tool result"}}
`), 0o644)
	if got := SubagentDescription(path); got != "Scan the commands" {
		t.Fatal(got)
	}
	if got := SubagentReport(path); got != "All done: 3 files." {
		t.Fatal(got)
	}

	t.Setenv("PAWPRINTS_HOME", t.TempDir())
	p, _ := store.OpenProject(t.TempDir())
	p.UpdateSession("S", func(r *store.SessionRecord) {
		r.TranscriptPath = session
		r.Agents = []store.Agent{{ID: "A1", Type: "general-purpose", StoppedAt: "2026-10-23T15:19:46Z"}, {ID: "A2", Type: "Explore"}}
	})
	if n, _ := FillAgents(true); n != 1 {
		t.Fatalf("one agent has files to fill from, got %d", n)
	}
	FillAgents(false)
	rec, _ := p.LoadSession("S")
	if a := rec.Agents[0]; a.Description != "Scan the commands" || a.Report != "All done: 3 files." || a.Summary == "" {
		t.Fatalf("%+v", a)
	}
	if n, _ := FillAgents(true); n != 0 {
		t.Fatal("filled once")
	}
}

func TestReportSummary(t *testing.T) {
	got := ReportSummary("## MIG phase 0 report\n\n**1. File written**\n- `notes/mig.md` (73 lines)")
	if got != "MIG phase 0 report 1. File written - notes/mig.md (73 lines)" {
		t.Fatal(got)
	}
}

func strings0(path, ext string) string { return path[:len(path)-len(".jsonl")] + ext }
