package hook

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/elythi0n/pawprints/internal/diff"
	"github.com/elythi0n/pawprints/internal/store"
)

// Commands change files without saying which: Bash, and MCP tools whose
// servers write files. The pre hook notes when a command started; the post
// hook, which runs async so Claude never waits, scans for files changed since
// the previous scan and credits each change by when it happened:
//
//   - during the command: to the command
//   - while a backgrounded command of the session may be running: to it
//   - while another Claude call in the project is running: to nobody here,
//     since that call records it
//   - otherwise, for files of this project only: "external", a change made
//     outside Claude between its commands
//
// The end of each turn and each new prompt scan too, so output from
// background commands and Claude Code's /rewind are recorded before the next
// prompt. A scan walks the project, folders outside it the session worked in
// (worktrees), and the configured watch_dirs; it also checks paths named in
// the command and the files Claude Code reports a Bash call changed.

const (
	// maxWindow bounds how far back a scan looks. After a long break the
	// next scan covers only the command that triggered it, so a pull or
	// checkout made in between is not recorded as a burst of changes.
	maxWindow = 6 * time.Hour
	// maxScanChanges caps one scan; the newest changes win.
	maxScanChanges = 400
)

// trigger is the command a scan runs for; nil for a scan between turns.
type trigger struct {
	tool      string
	command   string
	start     time.Time
	toolUseID string
	turn      string
	agent     string
	agentType string
}

// commandText is what an event shows as the command.
func commandText(in Input) string {
	if in.ToolName != "Bash" {
		return ""
	}
	var ti struct {
		Command string `json:"command"`
	}
	_ = jsonUnmarshal(in.ToolInput, &ti)
	return truncateCommand(ti.Command)
}

func runsInBackground(in Input) bool {
	var ti struct {
		Background bool `json:"run_in_background"`
	}
	_ = jsonUnmarshal(in.ToolInput, &ti)
	return ti.Background
}

func truncateCommand(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 200 {
		return s[:199] + "…"
	}
	return s
}

func runCommandPre(in Input) error {
	p, err := projectFor(in)
	if err != nil {
		return err
	}
	noteDir(p, in, in.CWD)
	rec, _ := p.LoadSession(in.SessionID)
	now := time.Now()
	return p.WritePending(in.ToolUseID, store.Pending{
		TS:         store.Now(now),
		StartNano:  now.UnixNano(),
		Session:    in.SessionID,
		Turn:       rec.TurnFor(in.AgentID),
		Tool:       in.ToolName,
		Command:    commandText(in),
		Agent:      in.AgentID,
		AgentType:  in.AgentType,
		Background: runsInBackground(in),
		CWD:        in.CWD,
	})
}

func runCommandPost(in Input) error {
	p, err := projectFor(in)
	if err != nil {
		return err
	}
	// The record is removed only once the command's changes are recorded,
	// so other sessions' scans leave them alone until then.
	defer p.DonePending(in.ToolUseID)
	pe, ok := p.ReadPending(in.ToolUseID)
	t := &trigger{tool: in.ToolName, command: commandText(in), toolUseID: in.ToolUseID, agent: in.AgentID, agentType: in.AgentType}
	if ok && pe.StartNano != 0 {
		t.start = time.Unix(0, pe.StartNano)
		t.turn, t.command = pe.Turn, pe.Command
	} else {
		// No pre record: credit only what the response itself names.
		t.start = time.Now()
		rec, _ := p.LoadSession(in.SessionID)
		t.turn = rec.TurnFor(in.AgentID)
	}
	return scan(p, in, t, commandPaths(t.command, in.CWD), editDiffFiles(in.ToolResponse))
}

// scanBetween records what changed since the last scan with no command to
// credit: background output, /rewind, a person's own edits.
func scanBetween(in Input) error {
	p, err := projectFor(in)
	if err != nil {
		return err
	}
	return scan(p, in, nil, nil, nil)
}

type candidate struct {
	path   string
	mtime  time.Time
	forced bool // named by Claude Code's own record of the command
}

// scan finds changed files and records them. named are paths the command
// mentions, checked by modification time; reported are files Claude Code
// says the command changed, recorded whatever their time.
func scan(p *store.Project, in Input, t *trigger, named, reported []string) error {
	now := time.Now()
	cfg, err := store.LoadConfig()
	if err != nil {
		return err
	}
	from := time.Time{}
	if t != nil {
		from = t.start
	}
	if last := p.LastScan(); !last.IsZero() && now.Sub(last) < maxWindow && (from.IsZero() || last.Before(from)) {
		from = last
	}
	if err := p.MarkScanned(now); err != nil {
		return err
	}
	if from.IsZero() {
		return nil // the first scan of a project only sets the watermark
	}
	rec, _ := p.LoadSession(in.SessionID)
	roots := store.KnownRoots()

	found := map[string]candidate{}
	add := func(path string, mtime time.Time, forced bool) {
		if excluded(path) {
			return
		}
		if c, ok := found[path]; ok && (c.forced || !forced) {
			return
		}
		found[path] = candidate{path: path, mtime: mtime, forced: forced}
	}
	for _, root := range scanRoots(p, rec, cfg, roots) {
		var ig *store.Ignore
		if root == p.Root {
			ig = store.LoadProjectIgnore(root, cfg.Ignore)
		} else {
			ig = store.NewIgnore(cfg.Ignore)
		}
		for _, path := range dropGitIgnored(root, modifiedSince(root, from, ig)) {
			if info, err := os.Stat(path); err == nil {
				add(path, info.ModTime(), false)
			}
		}
	}
	for _, path := range named {
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() && info.ModTime().After(from) {
			add(path, info.ModTime(), false)
		}
	}
	for _, path := range reported {
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			add(path, info.ModTime(), true)
		}
	}
	list := make([]candidate, 0, len(found))
	for _, c := range found {
		list = append(list, c)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].mtime.After(list[j].mtime) })
	if len(list) > maxScanChanges {
		fmt.Fprintf(os.Stderr, "pawprints: %d files changed at once; recording the newest %d\n", len(list), maxScanChanges)
		list = list[:maxScanChanges]
	}

	// Running calls are read before the last-known contents. A call that
	// finished in between has already recorded its changes there, so either
	// way no other session's change is taken for an outside one.
	who := newAttribution(p, in.SessionID, rec.CurrentTurn(), t, roots, now)
	lock, err := p.Acquire()
	if err != nil {
		return err
	}
	last := p.LoadLast()
	lock.Release()

	var events []store.Event
	for _, c := range list {
		credit, ok := who.credit(c.path, c.mtime, c.forced)
		if !ok {
			continue
		}
		ev, ok, err := changeEvent(p, cfg, last, c.path)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		credit(&ev)
		ev.TS = store.Now(c.mtime)
		events = append(events, ev)
	}
	// Files the store knew that are gone now.
	for rel, known := range last {
		if known.After == "" {
			continue
		}
		abs := p.Abs(rel)
		if _, err := os.Stat(abs); !errors.Is(err, os.ErrNotExist) {
			continue
		}
		credit, ok := who.credit(abs, now, t != nil)
		if !ok {
			continue
		}
		ev := store.Event{Kind: store.KindEdit, Path: rel, Before: store.HashPtr(known.After)}
		credit(&ev)
		ev.TS = store.Now(now)
		events = append(events, ev)
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].TS < events[j].TS })
	for i := range events {
		ev := events[i]
		at := ev.Time().Add(time.Duration(i) * time.Microsecond)
		ev.ID, ev.TS, ev.CWD, ev.Session = store.NewID(at), store.Now(at), in.CWD, in.SessionID
		if err := p.AppendEvent(ev); err != nil {
			return err
		}
		tg := target{project: p, rel: ev.Path}
		if err := tg.updateSession(in.SessionID, ev.Path, ev, at); err != nil {
			return err
		}
	}
	return nil
}

// changeEvent builds the event for one changed file, or ok=false when its
// content is what the store already has.
func changeEvent(p *store.Project, cfg store.Config, last map[string]store.LastKnown, abs string) (store.Event, bool, error) {
	info, err := os.Stat(abs)
	if err != nil || info.Size() > cfg.MaxFileBytes {
		return store.Event{}, false, nil
	}
	content, err := os.ReadFile(abs)
	if err != nil {
		return store.Event{}, false, nil
	}
	rel := p.Rel(abs)
	after, err := p.PutBytes(content)
	if err != nil {
		return store.Event{}, false, err
	}
	known, seen := last[rel]
	if seen && known.After == after {
		return store.Event{}, false, nil
	}
	ev := store.Event{Kind: store.KindEdit, Path: rel, After: store.HashPtr(after), SizeAfter: int64(len(content)), Binary: store.IsBinary(content)}
	var before []byte
	switch {
	case seen:
		ev.Before = store.HashPtr(known.After)
		before, _ = p.Get(known.After)
	default:
		limit := p.Root
		if !store.Within(p.Root, abs) {
			limit = string(filepath.Separator)
		}
		if b, ok := gitBaseline(limit, abs); ok {
			h, err := p.PutBytes(b)
			if err != nil {
				return store.Event{}, false, err
			}
			if h == after {
				return store.Event{}, false, nil // matches git: touched, not changed
			}
			ev.Before, before = store.HashPtr(h), b
		} else {
			ev.BeforeUnknown = true
		}
	}
	ev.SizeBefore = int64(len(before))
	// With no earlier copy there is nothing to count lines against.
	if !ev.BeforeUnknown && !ev.Binary && !store.IsBinary(before) && len(before) <= maxDiffBytes && len(content) <= maxDiffBytes {
		st := diff.Count(before, content)
		ev.LinesAdded, ev.LinesRemoved = st.Added, st.Removed
	}
	return ev, true, nil
}

// attribution decides who made the changes one scan finds.
type attribution struct {
	trigger *trigger // the command the scan runs for; nil between turns
	session string
	turn    string
	project string   // this project's root
	roots   []string // every known project's root, this one's included
	running map[string]store.Pending
}

func newAttribution(p *store.Project, session, turn string, t *trigger, roots []string, now time.Time) attribution {
	return attribution{
		trigger: t, session: session, turn: turn, project: p.Root,
		roots:   append(append([]string{}, roots...), p.Root),
		running: p.Running(now),
	}
}

// credit says who made the change to path at mtime, as a function that
// fills in an event. ok is false when this session must not record it: the
// change is another Claude call's, which records it, or it was made outside
// Claude in a file this project does not own.
func (a attribution) credit(path string, mtime time.Time, forced bool) (fill func(*store.Event), ok bool) {
	if t := a.trigger; t != nil && (forced || !mtime.Before(t.start)) {
		return func(ev *store.Event) {
			ev.Tool, ev.Command, ev.ToolUseID, ev.Turn = t.tool, t.command, t.toolUseID, t.turn
			ev.Agent, ev.AgentType = t.agent, t.agentType
		}, true
	}
	if id, job, found := a.backgroundJob(mtime); found {
		return func(ev *store.Event) {
			ev.Kind, ev.Tool, ev.Command, ev.ToolUseID, ev.Turn = store.KindBackground, "Bash", job.Command, id, job.Turn
			ev.Agent, ev.AgentType = job.Agent, job.AgentType
		}, true
	}
	if !a.outsideClaude(path, mtime) {
		return nil, false
	}
	return func(ev *store.Event) {
		ev.Kind, ev.Tool, ev.Turn = store.KindExternal, "external", a.turn
	}, true
}

// outsideClaude reports whether a change to path at mtime can be called one
// made outside Claude: the file is this project's, and no other Claude call
// in the project was running when it changed.
func (a attribution) outsideClaude(path string, mtime time.Time) bool {
	if store.Owner(a.roots, path) != a.project {
		return false
	}
	for id, pe := range a.running {
		if a.trigger != nil && id == a.trigger.toolUseID {
			continue
		}
		if !pe.Start().After(mtime) {
			return false
		}
	}
	return true
}

// backgroundJob is the session's newest background command started before
// the change.
func (a attribution) backgroundJob(at time.Time) (string, store.Pending, bool) {
	var bestID string
	var best store.Pending
	for id, pe := range a.running {
		if !pe.Background || pe.Session != a.session || pe.Start().After(at) || at.Sub(pe.Start()) > store.BackgroundLife {
			continue
		}
		if bestID == "" || pe.Start().After(best.Start()) {
			bestID, best = id, pe
		}
	}
	return bestID, best, bestID != ""
}

// scanRoots is the project, the folders outside it the session worked in,
// and the configured watch_dirs, each only if it still exists.
func scanRoots(p *store.Project, rec store.SessionRecord, cfg store.Config, known []string) []string {
	roots := []string{p.Root}
	seen := map[string]bool{p.Root: true}
	var extra []string
	for _, d := range rec.ExtraRoots {
		// Older versions could note a parent folder of several projects.
		if !store.HoldsProjects(d, p.Root, known) {
			extra = append(extra, d)
		}
	}
	for _, d := range cfg.WatchDirs {
		extra = append(extra, expandHome(d))
	}
	for _, d := range extra {
		d = filepath.Clean(d)
		if seen[d] || !walkable(d) {
			continue
		}
		if info, err := os.Stat(d); err != nil || !info.IsDir() {
			continue
		}
		seen[d] = true
		roots = append(roots, d)
	}
	return roots
}

// walkable refuses folders too broad to walk after every command.
func walkable(dir string) bool {
	home, _ := os.UserHomeDir()
	return dir != "/" && dir != home && strings.Count(dir, string(filepath.Separator)) >= 2
}

// noteDir records a folder outside the project the session works in, such
// as a git worktree, so command scans walk it too. A folder holding this or
// another project, such as their common parent, is never one: walking it
// would reach into every project below it.
func noteDir(p *store.Project, in Input, dir string) {
	if dir == "" || store.Within(p.Root, dir) {
		return
	}
	root := store.ResolveRoot(dir)
	if !walkable(root) || excluded(root) || store.HoldsProjects(root, p.Root, store.KnownRoots()) {
		return
	}
	rec, _ := p.LoadSession(in.SessionID)
	for _, d := range rec.ExtraRoots {
		if d == root {
			return
		}
	}
	_ = p.UpdateSession(in.SessionID, func(r *store.SessionRecord) { r.AddExtraRoot(root) })
}

// excluded keeps out files no one wants in an edit history: Claude Code's
// own state, Pawprints' store, caches, and the kernel's virtual files.
func excluded(path string) bool {
	home, _ := os.UserHomeDir()
	for _, prefix := range []string{
		filepath.Join(home, ".claude"),
		store.Home(),
		filepath.Join(home, ".cache"),
		filepath.Join(home, ".local", "share", "Trash"),
		"/proc", "/sys", "/dev", "/run",
	} {
		if prefix != "" && (path == prefix || strings.HasPrefix(path, prefix+string(filepath.Separator))) {
			return true
		}
	}
	return false
}

// commandPaths pulls file paths out of a shell command: absolute paths,
// "~/" paths, and relative paths with a slash or a dot, such as
// `sed -i ~/.bashrc` or `> /tmp/out.txt`. Text in single quotes is taken as a
// pattern or a script (sed, awk, grep) unless it is an absolute path. It
// cannot see paths a script builds itself; those are found only inside the
// scanned folders. Names that do not exist are dropped when they are checked.
func commandPaths(command, cwd string) []string {
	if command == "" {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, tok := range shellWords(command) {
		f := strings.TrimLeft(tok.text, "<>0123456789")
		if i := strings.Index(f, "="); i > 0 && !strings.HasPrefix(f, "/") && !strings.HasPrefix(f, "~") {
			f = f[i+1:] // --out=/path, VAR=/path
		}
		if f == "" || strings.HasPrefix(f, "-") || strings.ContainsAny(f, "$*?`{}") {
			continue
		}
		var path string
		switch {
		case strings.HasPrefix(f, "/"):
			path = f
		case strings.HasPrefix(f, "~/"):
			path = expandHome(f)
		case tok.singleQuoted:
			continue
		case strings.Contains(f, "/") || strings.Contains(f, "."):
			if cwd == "" {
				continue
			}
			path = filepath.Join(cwd, f)
		default:
			continue
		}
		path = filepath.Clean(path)
		if !seen[path] {
			seen[path] = true
			out = append(out, path)
		}
	}
	return out
}

type word struct {
	text         string
	singleQuoted bool
}

// shellWords splits a command the way a shell would, enough to find
// arguments: quotes group, and whitespace and ; | & ( ) separate.
func shellWords(command string) []word {
	var words []word
	var cur strings.Builder
	single, double, quotedSingle, has := false, false, false, false
	flush := func() {
		if has {
			words = append(words, word{text: cur.String(), singleQuoted: quotedSingle})
		}
		cur.Reset()
		quotedSingle, has = false, false
	}
	for _, r := range command {
		switch {
		case single:
			if r == '\'' {
				single = false
			} else {
				cur.WriteRune(r)
			}
		case double:
			if r == '"' {
				double = false
			} else {
				cur.WriteRune(r)
			}
		case r == '\'':
			single, quotedSingle, has = true, true, true
		case r == '"':
			double, has = true, true
		case strings.ContainsRune(" \t\n;|&()", r):
			flush()
		default:
			cur.WriteRune(r)
			has = true
		}
	}
	flush()
	return words
}

// editDiffFiles reads the files Claude Code reports a Bash call changed
// (tool_response.bashEditDiff.changedFiles). The list is best effort and
// only present in some modes; it is one source among several.
func editDiffFiles(response json.RawMessage) []string {
	if len(response) == 0 {
		return nil
	}
	var r struct {
		BashEditDiff *struct {
			ChangedFiles []string `json:"changedFiles"`
			Skipped      bool     `json:"skipped"`
		} `json:"bashEditDiff"`
	}
	if json.Unmarshal(response, &r) != nil || r.BashEditDiff == nil || r.BashEditDiff.Skipped {
		return nil
	}
	return r.BashEditDiff.ChangedFiles
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}
