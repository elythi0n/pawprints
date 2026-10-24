package hook

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/elythi0n/pawprints/internal/store"
)

type rig struct {
	t    *testing.T
	root string
	home string
}

// newRig makes an isolated store, Claude home, HOME, and a git project.
func newRig(t *testing.T) *rig {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PAWPRINTS_HOME", filepath.Join(home, ".pawprints"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	t.Setenv("PAWPRINTS_VIA_PLUGIN", "")
	root := filepath.Join(home, "work", "proj")
	os.MkdirAll(root, 0o755)
	exec.Command("git", "init", "-q", root).Run()
	return &rig{t: t, root: root, home: home}
}

func (r *rig) send(event string, fields map[string]any) {
	r.t.Helper()
	r.sendAs("S", event, fields)
}

// sendAs sends a hook event from another session.
func (r *rig) sendAs(session, event string, fields map[string]any) {
	r.t.Helper()
	m := map[string]any{"session_id": session, "cwd": r.root}
	for k, v := range fields {
		m[k] = v
	}
	data, _ := json.Marshal(m)
	var stderr bytes.Buffer
	Run(event, bytes.NewReader(data), &stderr)
	if stderr.Len() > 0 {
		r.t.Fatalf("%s: %s", event, stderr.String())
	}
}

// write sets content and an explicit modification time.
func (r *rig) write(path, content string, at time.Time) {
	r.t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		r.t.Fatal(err)
	}
	os.Chtimes(path, at, at)
}

func (r *rig) project() *store.Project {
	r.t.Helper()
	p, ok := store.FindProject(r.root)
	if !ok {
		r.t.Fatal("no project")
	}
	return p
}

func (r *rig) events() map[string]store.Event {
	evs, _ := r.project().Events(store.Query{})
	out := map[string]store.Event{}
	for i := len(evs) - 1; i >= 0; i-- { // newest wins
		out[evs[i].Path] = evs[i]
	}
	return out
}

// warm sets the scan watermark, as the first prompt of a session does.
func (r *rig) warm() {
	r.send("prompt", map[string]any{"prompt": "go"})
}

func TestMCPToolWritesAreRecorded(t *testing.T) {
	r := newRig(t)
	r.warm()
	tool := map[string]any{"tool_name": "mcp__files__write_file", "tool_use_id": "m1", "tool_input": map[string]any{"path": "notes.md"}}
	r.send("command-pre", tool)
	r.write(filepath.Join(r.root, "notes.md"), "from an MCP server\n", time.Now())
	r.send("command-post", tool)
	ev, ok := r.events()["notes.md"]
	if !ok || ev.Kind != store.KindEdit || ev.Tool != "mcp__files__write_file" || ev.ToolUseID != "m1" || ev.Command != "" {
		t.Fatalf("mcp event %+v", ev)
	}
}

func TestBackgroundCommandsAreCredited(t *testing.T) {
	r := newRig(t)
	r.warm()
	start := time.Now()
	bg := map[string]any{"tool_name": "Bash", "tool_use_id": "b1", "tool_input": map[string]any{"command": "./gen.sh > out.txt", "run_in_background": true}}
	r.send("command-pre", bg)
	r.send("command-post", bg) // returns as soon as the command is backgrounded
	if len(r.events()) != 0 {
		t.Fatal("nothing has changed yet")
	}
	// The background command writes later; the turn ends.
	r.write(filepath.Join(r.root, "out.txt"), "generated\n", start.Add(2*time.Second))
	r.send("stop", nil)
	ev, ok := r.events()["out.txt"]
	if !ok || ev.Kind != store.KindBackground || ev.Command != "./gen.sh > out.txt" || ev.ToolUseID != "b1" || ev.Tool != "Bash" || ev.Session != "S" {
		t.Fatalf("background event %+v", ev)
	}
	if _, ok := r.project().Running(time.Now())["b1"]; !ok {
		t.Fatal("a background command counts as running while it may run")
	}
	r.send("session-end", map[string]any{"reason": "exit"})
	if len(r.project().Running(time.Now())) != 0 {
		t.Fatal("session end forgets background commands")
	}
}

func TestChangesBetweenCommandsAreNotClaudes(t *testing.T) {
	r := newRig(t)
	r.warm()
	time.Sleep(5 * time.Millisecond)
	// A person edits a file between Claude's commands.
	r.write(filepath.Join(r.root, "mine.txt"), "typed by hand\n", time.Now())
	time.Sleep(5 * time.Millisecond)
	cmd := map[string]any{"tool_name": "Bash", "tool_use_id": "c1", "tool_input": map[string]any{"command": "echo hi > claude.txt"}}
	r.send("command-pre", cmd)
	r.write(filepath.Join(r.root, "claude.txt"), "hi\n", time.Now())
	r.send("command-post", cmd)
	evs := r.events()
	if ev := evs["mine.txt"]; ev.Kind != store.KindExternal || ev.Tool != "external" {
		t.Fatalf("hand edit %+v", ev)
	}
	if ev := evs["claude.txt"]; ev.Kind != store.KindEdit || ev.Tool != "Bash" || ev.Command != "echo hi > claude.txt" {
		t.Fatalf("command edit %+v", ev)
	}
	// /rewind or an editor save between turns lands before the next prompt.
	r.write(filepath.Join(r.root, "claude.txt"), "rewound\n", time.Now())
	r.send("prompt", map[string]any{"prompt": "next"})
	if ev := r.events()["claude.txt"]; ev.Kind != store.KindExternal {
		t.Fatalf("between turns %+v", ev)
	}
}

func TestFilesOutsideTheProject(t *testing.T) {
	r := newRig(t)
	r.warm()
	rc := filepath.Join(r.home, ".bashrc")
	r.write(rc, "old\n", time.Now().Add(-time.Hour))
	cmd := map[string]any{"tool_name": "Bash", "tool_use_id": "o1", "tool_input": map[string]any{"command": "sed -i 's/old/new/' ~/.bashrc && echo done > /dev/null"}}
	r.send("command-pre", cmd)
	r.write(rc, "new\n", time.Now())
	// A file under ~/.claude named in the command is never recorded.
	secret := filepath.Join(r.home, ".claude", "settings.json")
	r.write(secret, "{}\n", time.Now())
	r.send("command-post", map[string]any{"tool_name": "Bash", "tool_use_id": "o1", "tool_input": map[string]any{"command": "sed -i 's/old/new/' ~/.bashrc && cat " + secret}})
	evs := r.events()
	ev, ok := evs[rc]
	if !ok || ev.Kind != store.KindEdit || !ev.BeforeUnknown || ev.LinesAdded != 0 || ev.Session != "S" {
		t.Fatalf("outside file %+v", ev)
	}
	if _, ok := evs[secret]; ok {
		t.Fatal("~/.claude must never be recorded")
	}
	// The second change to it diffs against the first.
	cmd2 := map[string]any{"tool_name": "Bash", "tool_use_id": "o2", "tool_input": map[string]any{"command": "echo more >> ~/.bashrc"}}
	r.send("command-pre", cmd2)
	r.write(rc, "new\nmore\n", time.Now())
	r.send("command-post", cmd2)
	if ev := r.events()[rc]; ev.BeforeUnknown || ev.LinesAdded != 1 {
		t.Fatalf("second outside change %+v", ev)
	}
}

func TestWorktreesAreScanned(t *testing.T) {
	r := newRig(t)
	r.warm()
	wt := filepath.Join(r.home, "work", "proj-feature")
	os.MkdirAll(wt, 0o755)
	exec.Command("git", "init", "-q", wt).Run()
	// Claude moves into the worktree; CwdChanged notes it.
	r.send("cwd", map[string]any{"old_cwd": r.root, "new_cwd": wt})
	rec, _ := r.project().LoadSession("S")
	if len(rec.ExtraRoots) != 1 || rec.ExtraRoots[0] != wt {
		t.Fatalf("extra roots %v", rec.ExtraRoots)
	}
	cmd := map[string]any{"tool_name": "Bash", "tool_use_id": "w1", "cwd": wt, "tool_input": map[string]any{"command": "make gen"}}
	r.send("command-pre", cmd)
	r.write(filepath.Join(wt, "gen", "api.go"), "package gen\n", time.Now())
	r.send("command-post", cmd)
	path := filepath.Join(wt, "gen", "api.go")
	if ev, ok := r.events()[path]; !ok || ev.Kind != store.KindEdit {
		t.Fatalf("worktree change not recorded: %v", r.events())
	}
	// A hook call from a folder outside the project registers it too.
	other := filepath.Join(r.home, "work", "proj-fix")
	os.MkdirAll(other, 0o755)
	r.send("command-pre", map[string]any{"tool_name": "Bash", "tool_use_id": "w2", "cwd": other, "tool_input": map[string]any{"command": "ls"}})
	rec, _ = r.project().LoadSession("S")
	if len(rec.ExtraRoots) != 2 {
		t.Fatalf("extra roots after a call from another folder %v", rec.ExtraRoots)
	}
	// Home itself is never a scan root.
	r.send("cwd", map[string]any{"new_cwd": r.home})
	rec, _ = r.project().LoadSession("S")
	if len(rec.ExtraRoots) != 2 {
		t.Fatal("home must not become a scan root")
	}
}

func TestEditDiffFilesAreCreditedToTheCommand(t *testing.T) {
	r := newRig(t)
	r.warm()
	elsewhere := filepath.Join(r.home, "other-repo", "x.go")
	cmd := map[string]any{"tool_name": "Bash", "tool_use_id": "d1", "tool_input": map[string]any{"command": "./tool"}}
	r.send("command-pre", cmd)
	// The file's time says it changed before the command; Claude Code says
	// the command changed it, which wins.
	r.write(elsewhere, "package x\n", time.Now().Add(-30*time.Minute))
	r.send("command-post", map[string]any{"tool_name": "Bash", "tool_use_id": "d1", "tool_input": map[string]any{"command": "./tool"},
		"tool_response": map[string]any{"bashEditDiff": map[string]any{"changedFiles": []string{elsewhere}}}})
	if ev, ok := r.events()[elsewhere]; !ok || ev.Kind != store.KindEdit || ev.ToolUseID != "d1" {
		t.Fatalf("reported file %+v", ev)
	}
}

func TestSubagentsAreAttributed(t *testing.T) {
	r := newRig(t)
	r.warm()
	r.send("agent-start", map[string]any{"agent_id": "agent-7", "agent_type": "Explore"})
	file := filepath.Join(r.root, "a.go")
	edit := map[string]any{"tool_name": "Write", "tool_use_id": "e1", "agent_id": "agent-7", "agent_type": "Explore", "tool_input": map[string]any{"file_path": file}}
	r.send("pre", edit)
	r.write(file, "package a\n", time.Now())
	r.send("post", edit)
	cmd := map[string]any{"tool_name": "Bash", "tool_use_id": "e2", "agent_id": "agent-7", "agent_type": "Explore", "tool_input": map[string]any{"command": "gofmt -w b.go"}}
	r.send("command-pre", cmd)
	r.write(filepath.Join(r.root, "b.go"), "package b\n", time.Now())
	r.send("command-post", cmd)
	r.send("agent-stop", map[string]any{"agent_id": "agent-7", "agent_type": "Explore", "last_assistant_message": "Wrote  a.go and\nformatted b.go."})
	// Compaction runs as a typeless helper that never sends a start.
	r.send("agent-stop", map[string]any{"agent_id": "compact-1", "last_assistant_message": "Summary of the conversation"})
	evs := r.events()
	for _, p := range []string{"a.go", "b.go"} {
		if ev := evs[p]; ev.Agent != "agent-7" || ev.AgentType != "Explore" {
			t.Fatalf("%s not credited to the subagent: %+v", p, ev)
		}
	}
	rec, _ := r.project().LoadSession("S")
	if len(rec.Agents) != 1 || rec.Agents[0].Type != "Explore" || rec.Agents[0].StoppedAt == "" || rec.Agents[0].Summary != "Wrote a.go and formatted b.go." {
		t.Fatalf("agents %+v", rec.Agents)
	}
}

func TestOldWatermarkDoesNotSweepHistory(t *testing.T) {
	r := newRig(t)
	r.warm()
	p := r.project()
	// The last scan was long ago; a pull happened since.
	old := time.Now().Add(-8 * time.Hour)
	os.WriteFile(filepath.Join(p.Dir, "state", "scan.json"), []byte(`{"last_nano":`+itoa(old.UnixNano())+`}`), 0o644)
	r.write(filepath.Join(r.root, "pulled.go"), "package p\n", time.Now().Add(-2*time.Hour))
	cmd := map[string]any{"tool_name": "Bash", "tool_use_id": "x", "tool_input": map[string]any{"command": "true"}}
	r.send("command-pre", cmd)
	r.send("command-post", cmd)
	if _, ok := r.events()["pulled.go"]; ok {
		t.Fatal("a change older than the window must not be swept in")
	}
}

func itoa(n int64) string { b, _ := json.Marshal(n); return string(b) }

func TestCommandPaths(t *testing.T) {
	t.Setenv("HOME", "/home/u")
	got := commandPaths(`sed -i 's/a/b/' ~/.bashrc; cat src/x.go | tee /tmp/out.txt >> ./log.txt 2>/dev/null && cp a.txt "/srv/b c" --out=/var/x.json VAR=$HOME/y ls *.go; awk '{print $1}' data.csv; echo '/etc/hosts'`, "/w")
	want := []string{"/home/u/.bashrc", "/w/src/x.go", "/tmp/out.txt", "/w/log.txt", "/dev/null", "/w/a.txt", "/srv/b c", "/var/x.json", "/w/data.csv", "/etc/hosts"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %v\nwant %v", got, want)
	}
	if commandPaths("", "/w") != nil {
		t.Fatal("empty command")
	}
}

func TestEditDiffParsing(t *testing.T) {
	if got := editDiffFiles(json.RawMessage(`{"bashEditDiff":{"changedFiles":["/a","/b"],"moreFiles":0}}`)); len(got) != 2 {
		t.Fatalf("%v", got)
	}
	if got := editDiffFiles(json.RawMessage(`{"bashEditDiff":{"changedFiles":["/a"],"skipped":true}}`)); got != nil {
		t.Fatal("a skipped diff names nothing")
	}
	if editDiffFiles(json.RawMessage(`"plain output"`)) != nil || editDiffFiles(nil) != nil {
		t.Fatal("non-object responses name nothing")
	}
	_ = strings.TrimSpace
}

func TestAnotherSessionsChangesAreNotOutside(t *testing.T) {
	r := newRig(t)
	r.warm()
	r.sendAs("T", "prompt", map[string]any{"prompt": "go"})
	file := filepath.Join(r.root, "shared.go")
	cmd := map[string]any{"tool_name": "Bash", "tool_use_id": "t1", "tool_input": map[string]any{"command": "./gen.sh"}}
	r.sendAs("T", "command-pre", cmd)
	r.write(file, "package shared\n", time.Now())
	// S finishes its turn while T's command is still being recorded.
	r.send("stop", nil)
	if _, ok := r.events()["shared.go"]; ok {
		t.Fatal("a change another session's command is making must be left to it")
	}
	r.sendAs("T", "command-post", cmd)
	r.send("prompt", map[string]any{"prompt": "next"})
	ev := r.events()["shared.go"]
	if ev.Session != "T" || ev.Kind != store.KindEdit || ev.ToolUseID != "t1" {
		t.Fatalf("the change belongs to T's command: %+v", ev)
	}
	evs, _ := r.project().Events(store.Query{Kinds: []string{store.KindExternal}})
	if len(evs) != 0 {
		t.Fatalf("nothing here was outside Claude: %+v", evs)
	}
}

func TestEditsDuringAnotherSessionsEditAreNotOutside(t *testing.T) {
	r := newRig(t)
	file := filepath.Join(r.root, "a.go")
	edit := func(session, id string) map[string]any {
		return map[string]any{"tool_name": "Edit", "tool_use_id": id, "tool_input": map[string]any{"file_path": file}}
	}
	r.write(file, "v1\n", time.Now())
	r.send("pre", edit("S", "e1"))
	r.write(file, "v2\n", time.Now())
	r.send("post", edit("S", "e1"))
	// T's edit is under way: its pre hook ran, the file changed, its post
	// hook has not yet run. S edits the same file meanwhile.
	r.sendAs("T", "pre", edit("T", "e2"))
	r.write(file, "v3\n", time.Now())
	r.send("pre", edit("S", "e3"))
	evs, _ := r.project().Events(store.Query{Kinds: []string{store.KindExternal}})
	if len(evs) != 0 {
		t.Fatalf("T's edit is not an outside change: %+v", evs)
	}
}

func TestOutsideChangesOnlyInTheProjectsOwnFiles(t *testing.T) {
	r := newRig(t)
	other := filepath.Join(r.home, "work", "other")
	os.MkdirAll(other, 0o755)
	exec.Command("git", "init", "-q", other).Run()
	if _, err := store.OpenProject(other); err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(r.home, "wt")
	os.MkdirAll(wt, 0o755)
	r.send("cwd", map[string]any{"new_cwd": wt})
	r.send("cwd", map[string]any{"new_cwd": other})
	r.warm()
	r.write(filepath.Join(wt, "scratch.txt"), "by hand\n", time.Now())
	r.write(filepath.Join(other, "x.go"), "by another session\n", time.Now())
	r.write(filepath.Join(r.root, "mine.go"), "by hand\n", time.Now())
	r.send("stop", nil)
	evs := r.events()
	if _, ok := evs[filepath.Join(wt, "scratch.txt")]; ok {
		t.Fatal("a file outside the project is never called an outside change")
	}
	if _, ok := evs[filepath.Join(other, "x.go")]; ok {
		t.Fatal("another project's file is that project's to record")
	}
	if ev := evs["mine.go"]; ev.Kind != store.KindExternal {
		t.Fatalf("the project's own file is: %+v", ev)
	}
	// Claude's own command still gets credit outside the project.
	cmd := map[string]any{"tool_name": "Bash", "tool_use_id": "c1", "tool_input": map[string]any{"command": "make"}}
	r.send("command-pre", cmd)
	r.write(filepath.Join(wt, "built.txt"), "out\n", time.Now())
	r.send("command-post", cmd)
	if ev := r.events()[filepath.Join(wt, "built.txt")]; ev.ToolUseID != "c1" {
		t.Fatalf("command output outside the project: %+v", ev)
	}
}

func TestParentFoldersAreNotScanned(t *testing.T) {
	r := newRig(t)
	parent := filepath.Dir(r.root)
	sibling := filepath.Join(parent, "sibling")
	r.write(filepath.Join(sibling, "keep.txt"), "x\n", time.Now().Add(-time.Hour))
	r.send("cwd", map[string]any{"new_cwd": parent})
	rec, _ := r.project().LoadSession("S")
	if len(rec.ExtraRoots) != 0 {
		t.Fatalf("a folder holding the project is not a worktree: %v", rec.ExtraRoots)
	}
	// A record from an older version that noted the parent anyway.
	r.project().UpdateSession("S", func(rec *store.SessionRecord) { rec.AddExtraRoot(parent) })
	r.warm()
	cmd := map[string]any{"tool_name": "Bash", "tool_use_id": "c1", "tool_input": map[string]any{"command": "true"}}
	r.send("command-pre", cmd)
	r.write(filepath.Join(sibling, "during.txt"), "another session's\n", time.Now())
	r.send("command-post", cmd)
	if _, ok := r.events()[filepath.Join(sibling, "during.txt")]; ok {
		t.Fatal("an old parent-folder scan root must be ignored")
	}
}

func TestSubagentChangesStayUnderTheirPrompt(t *testing.T) {
	r := newRig(t)
	transcript := filepath.Join(r.home, ".claude", "projects", "p", "S.jsonl")
	dir := filepath.Join(strings.TrimSuffix(transcript, ".jsonl"), "subagents")
	r.write(filepath.Join(dir, "agent-A1.meta.json"), `{"agentType":"general-purpose","description":"Scan the commands"}`, time.Now())
	r.write(filepath.Join(dir, "agent-A1.jsonl"), `{"type":"user","message":{"role":"user","content":"go"}}
{"type":"assistant","message":{"content":[{"type":"tool_use","name":"SubagentHandback","input":{"message":"1. Wrote commands.md.\n2. Found 53 commands."}}]}}
`, time.Now())
	r.send("prompt", map[string]any{"prompt": "scan the manager", "transcript_path": transcript})
	r.send("agent-start", map[string]any{"agent_id": "A1", "agent_type": "general-purpose", "transcript_path": transcript})
	// Another agent's report arrives, then the person asks something new,
	// and only then does A1 write.
	r.send("prompt", map[string]any{"prompt": "<agent-message from=\"A0\">\n[Subagent hand-back] The report follows:\n  done\n</agent-message>"})
	r.send("prompt", map[string]any{"prompt": "while that runs, explain X"})
	file := filepath.Join(r.root, "commands.md")
	edit := map[string]any{"tool_name": "Write", "tool_use_id": "w1", "agent_id": "A1", "agent_type": "general-purpose", "tool_input": map[string]any{"file_path": file}}
	r.send("pre", edit)
	r.write(file, "# commands\n", time.Now())
	r.send("post", edit)
	cmd := map[string]any{"tool_name": "Bash", "tool_use_id": "c1", "agent_id": "A1", "agent_type": "general-purpose", "tool_input": map[string]any{"command": "./count.sh > count.txt"}}
	r.send("command-pre", cmd)
	r.write(filepath.Join(r.root, "count.txt"), "53\n", time.Now())
	r.send("command-post", cmd)
	r.send("agent-stop", map[string]any{"agent_id": "A1", "agent_type": "general-purpose", "agent_transcript_path": filepath.Join(dir, "agent-A1.jsonl")})

	rec, _ := r.project().LoadSession("S")
	if len(rec.Turns) != 2 || rec.Turns[0].Text != "scan the manager" {
		t.Fatalf("a delivered report is not a prompt: %+v", rec.Turns)
	}
	for _, p := range []string{"commands.md", "count.txt"} {
		if ev := r.events()[p]; ev.Turn != rec.Turns[0].ID {
			t.Fatalf("%s belongs to the prompt that started the agent: %+v", p, ev)
		}
	}
	a := rec.Agents[0]
	if a.Turn != rec.Turns[0].ID || a.Description != "Scan the commands" || a.Report != "1. Wrote commands.md.\n2. Found 53 commands." || a.Summary != "1. Wrote commands.md. 2. Found 53 commands." {
		t.Fatalf("agent %+v", a)
	}
	if a.Name() != "Scan the commands" {
		t.Fatal(a.Name())
	}
}

func TestRepeatedPromptIsOneTurn(t *testing.T) {
	r := newRig(t)
	for range 3 {
		r.send("prompt", map[string]any{"prompt": "/goal finish the scan"})
	}
	r.send("prompt", map[string]any{"prompt": "thanks"})
	r.send("prompt", map[string]any{"prompt": "/goal finish the scan"})
	rec, _ := r.project().LoadSession("S")
	if len(rec.Turns) != 3 {
		t.Fatalf("turns %+v", rec.Turns)
	}
}

// Logging out and in as someone else between prompts, in the same config
// folder, splits the session between the two accounts by prompt.
func TestAccountSwitchBetweenPrompts(t *testing.T) {
	r := newRig(t)
	login := func(id, email string) {
		data, _ := json.Marshal(map[string]any{"oauthAccount": map[string]any{"accountUuid": id, "emailAddress": email, "userRateLimitTier": "default_claude_max_5x"}})
		path := filepath.Join(r.home, ".claude.json")
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
		// A new mtime, as a real login has, even within one clock tick.
		later := time.Now().Add(time.Duration(len(id)) * time.Second)
		os.Chtimes(path, later, later)
	}
	transcript := filepath.Join(r.home, "t.jsonl")
	reply := func(id string, out int) {
		f, _ := os.OpenFile(transcript, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		fmt.Fprintf(f, `{"type":"assistant","timestamp":%q,"message":{"id":%q,"model":"m","usage":{"output_tokens":%d}}}`+"\n", time.Now().UTC().Format(time.RFC3339Nano), id, out)
		f.Close()
	}
	login("acct-a", "a@example.com")
	r.send("prompt", map[string]any{"prompt": "first job for account a", "transcript_path": transcript})
	time.Sleep(10 * time.Millisecond)
	reply("r1", 10)
	r.send("stop", map[string]any{"transcript_path": transcript})
	login("acct-bb", "b@example.com")
	r.send("prompt", map[string]any{"prompt": "second job for account b", "transcript_path": transcript})
	time.Sleep(10 * time.Millisecond)
	reply("r2", 20)
	r.send("stop", map[string]any{"transcript_path": transcript})

	p := r.project()
	rec, _ := p.LoadSession("S")
	if len(rec.Turns) != 2 || rec.Turns[0].Account != "acct-a" || rec.Turns[1].Account != "acct-bb" || rec.Account != "acct-bb" {
		t.Fatalf("turns carry their account: %+v (session %s)", rec.Turns, rec.Account)
	}
	buckets, _ := p.SessionUsage("S")
	by := map[string]int64{}
	for _, b := range buckets {
		by[b.Account] += b.Output
	}
	if by["acct-a"] != 10 || by["acct-bb"] != 20 {
		t.Fatalf("tokens by account %v", by)
	}
	if acct := store.Accounts()["acct-bb"]; acct.Email != "b@example.com" || acct.Plan != "Max 5x" {
		t.Fatalf("account remembered %+v", acct)
	}
}

// With prompts not recorded, a subagent's task and report are not either.
func TestSubagentTextFollowsPromptSetting(t *testing.T) {
	r := newRig(t)
	if err := os.MkdirAll(filepath.Join(r.home, ".pawprints"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.home, ".pawprints", "config.json"), []byte(`{"record_prompts": false}`), 0o644); err != nil {
		t.Fatal(err)
	}
	r.send("prompt", map[string]any{"prompt": "a private prompt"})
	r.send("agent-start", map[string]any{"agent_id": "A1", "agent_type": "Explore"})
	r.send("agent-stop", map[string]any{"agent_id": "A1", "agent_type": "Explore", "last_assistant_message": "the private findings"})
	rec, _ := r.project().LoadSession("S")
	if len(rec.Agents) != 1 {
		t.Fatalf("the agent is still recorded: %+v", rec.Agents)
	}
	if a := rec.Agents[0]; a.Description != "" || a.Report != "" || a.Summary != "" {
		t.Fatalf("no task or report text: %+v", a)
	}
	if rec.Turns[0].Text != "" {
		t.Fatalf("no prompt text: %+v", rec.Turns)
	}
}
