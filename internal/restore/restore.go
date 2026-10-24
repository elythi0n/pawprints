// Package restore writes stored versions back to disk: single restores,
// turn and session undo, and the safety check that keeps either from
// clobbering edits a person made since Claude's last write.
package restore

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"time"

	"github.com/elythi0n/pawprints/internal/diff"
	"github.com/elythi0n/pawprints/internal/store"
)

// ErrModified is returned when the working file differs from what the store
// last saw, meaning someone edited it after Claude.
type ErrModified struct {
	Path         string
	Expected     string
	Actual       string
	OtherSession string
	// Outside means the change was recorded, made outside Claude after its edits.
	Outside bool
}

func (e *ErrModified) Error() string {
	if e.Outside {
		return fmt.Sprintf("%s was changed outside Claude after these edits; pass --three-way to keep those changes or --force to overwrite them", e.Path)
	}
	msg := fmt.Sprintf("%s changed since the last recorded edit", e.Path)
	if e.OtherSession != "" {
		msg += " (last written by session " + short(e.OtherSession) + ")"
	}
	return msg + "; pass --three-way to merge or --force to overwrite"
}

// Options control how a write is applied.
type Options struct {
	Force    bool
	ThreeWay bool
	DryRun   bool
}

// Change is one file write that was planned or applied.
type Change struct {
	Path      string `json:"path"`
	From      string `json:"from"`      // hash on disk before the write
	To        string `json:"to"`        // hash written ("" means deleted)
	Event     string `json:"event"`     // the event whose content was used
	Conflicts int    `json:"conflicts"` // non-zero after a three-way merge with conflicts
	Modified  bool   `json:"modified"`  // disk had changed since the last recorded edit
}

// Result summarizes a restore or undo.
type Result struct {
	Kind    string   `json:"kind"`
	Changes []Change `json:"changes"`
	Applied bool     `json:"applied"`
}

// Check compares the file on disk with the last recorded content.
func Check(p *store.Project, rel string) (current string, modified bool, other string) {
	abs := p.Abs(rel)
	current, _ = p.HashFile(abs)
	if _, err := os.Stat(abs); errors.Is(err, os.ErrNotExist) {
		current = ""
	}
	last, known := p.LoadLast()[rel]
	if !known {
		return current, false, ""
	}
	if last.After == current {
		return current, false, ""
	}
	// Name the session that last wrote the file, when it was a different one.
	if ev, err := p.EventByPrefix(last.Event); err == nil {
		other = ev.Session
	}
	return current, true, other
}

// Version picks which side of an event to restore.
type Version string

const (
	After  Version = "after"
	Before Version = "before"
)

// Restore writes one event's content back to its path.
func Restore(p *store.Project, ev store.Event, which Version, opts Options) (Result, error) {
	hash := ev.AfterHash()
	if which == Before {
		hash = ev.BeforeHash()
	}
	res := Result{Kind: store.KindRestore}
	w := write{session: ev.Session, turn: ev.Turn, rel: ev.Path, hash: hash, source: ev.ID, kind: store.KindRestore}
	// When the newest change to the file was made outside Claude, merge
	// against the content it started from.
	if latest, ok := newestFor(p, ev.Path); ok && latest.Kind == store.KindExternal {
		w.outside, w.base = true, latest.BeforeHash()
	}
	ch, err := apply(p, w, opts)
	if err != nil {
		return res, err
	}
	res.Changes = []Change{ch}
	res.Applied = !opts.DryRun
	return res, nil
}

// UndoScope selects what undo reverts. Exactly one field is set.
type UndoScope struct {
	Last    bool
	Turn    string
	Session string
}

// claudeKinds are the changes Claude made. Undo reverts only these, never
// changes made outside Claude.
var claudeKinds = []string{store.KindEdit, store.KindBackground}

// Undo reverts every change Claude made in scope so each file ends at its
// earliest recorded "before". Events are collected newest first and the
// oldest one per path wins. A file also changed outside Claude since is
// refused unless forced; a three-way undo reverses Claude's changes one by
// one on top of the file as it is, keeping the other changes.
func Undo(p *store.Project, scope UndoScope, opts Options) (Result, error) {
	res := Result{Kind: store.KindUndo}
	q := store.Query{Kinds: claudeKinds}
	switch {
	case scope.Last:
		q.Limit = 1
	case scope.Turn != "":
		q.Turn = scope.Turn
	case scope.Session != "":
		q.Session = scope.Session
	default:
		return res, errors.New("undo needs a scope: last, --turn, or --session")
	}
	events, err := p.Events(q)
	if err != nil {
		return res, err
	}
	if len(events) == 0 {
		return res, errors.New("nothing to undo")
	}
	oldest := map[string]store.Event{}
	byPath := map[string][]store.Event{}
	for _, ev := range events { // newest first, so later entries are older
		oldest[ev.Path] = ev
		byPath[ev.Path] = append(byPath[ev.Path], ev)
	}
	outside, err := changedOutside(p, oldest)
	if err != nil {
		return res, err
	}
	paths := make([]string, 0, len(oldest))
	for path := range oldest {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	// Check every file before touching any, so a refusal leaves nothing half done.
	if !opts.Force && !opts.ThreeWay {
		for _, path := range paths {
			if outside[path] {
				return res, &ErrModified{Path: path, Outside: true}
			}
			if _, modified, other := Check(p, path); modified {
				return res, &ErrModified{Path: path, OtherSession: other}
			}
		}
	}
	for _, path := range paths {
		ev := oldest[path]
		w := write{session: ev.Session, turn: ev.Turn, rel: path, hash: ev.BeforeHash(), source: ev.ID, kind: store.KindUndo}
		if outside[path] {
			w.outside, w.revert = true, byPath[path]
		}
		ch, err := apply(p, w, opts)
		if err != nil {
			return res, err
		}
		res.Changes = append(res.Changes, ch)
	}
	res.Applied = !opts.DryRun
	return res, nil
}

// changedOutside reports, per path, whether a change outside Claude was
// recorded after the given event.
func changedOutside(p *store.Project, since map[string]store.Event) (map[string]bool, error) {
	var first time.Time
	for _, ev := range since {
		if first.IsZero() || ev.Time().Before(first) {
			first = ev.Time()
		}
	}
	external, err := p.Events(store.Query{Kinds: []string{store.KindExternal}, Since: first})
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, ext := range external {
		if ev, ok := since[ext.Path]; ok && ext.ID > ev.ID {
			out[ext.Path] = true
		}
	}
	return out, nil
}

// newestFor returns the newest event for exactly rel.
func newestFor(p *store.Project, rel string) (store.Event, bool) {
	events, err := p.Events(store.Query{Path: rel})
	if err != nil {
		return store.Event{}, false
	}
	for _, ev := range events {
		if ev.Path == rel {
			return ev, true
		}
	}
	return store.Event{}, false
}

// write is one planned file write.
type write struct {
	session, turn string
	rel           string
	hash          string // content to write; "" deletes the file
	source        string // the event whose content is used
	kind          string
	// outside means the file was changed outside Claude after its edits,
	// so it counts as modified even when the store is in sync with it.
	outside bool
	base    string        // three-way base for an outside change
	revert  []store.Event // undo: Claude's changes to reverse, newest first
}

// apply writes w.hash's content to w.rel (or deletes it when w.hash is
// empty), honoring the safety options, and records the write as an event.
func apply(p *store.Project, w write, opts Options) (Change, error) {
	session, turn, rel, hash, kind := w.session, w.turn, w.rel, w.hash, w.kind
	ch := Change{Path: rel, To: hash, Event: w.source}
	current, modified, other := Check(p, rel)
	if w.outside {
		modified, other = true, ""
	}
	ch.From = current
	ch.Modified = modified
	if modified && !opts.Force && !opts.ThreeWay {
		return ch, &ErrModified{Path: rel, Expected: p.LoadLast()[rel].After, Actual: current, OtherSession: other, Outside: w.outside}
	}
	if current == hash {
		return ch, nil // already there
	}
	if hash == "" && modified && opts.ThreeWay {
		// The target is "file did not exist" but the person changed it since:
		// there is nothing to merge into, so keep their version and say so.
		ch.To = current
		ch.Conflicts = 1
		return ch, nil
	}
	if opts.DryRun {
		return ch, nil
	}
	abs := p.Abs(rel)
	// Keep what is on disk now so the write is itself undoable and so a
	// three-way merge can read it back.
	if current != "" {
		if onDisk, err := os.ReadFile(abs); err == nil {
			if _, err := p.PutBytes(onDisk); err != nil {
				return ch, err
			}
		}
	}
	var content []byte
	var err error
	if hash != "" {
		content, err = p.Get(hash)
		if err != nil {
			return ch, err
		}
	}
	if modified && opts.ThreeWay && hash != "" {
		var merged []byte
		var conflicts int
		switch {
		case w.revert != nil:
			merged, conflicts, err = revertEach(p, current, w.revert)
		case w.base != "":
			merged, conflicts, err = threeWay(p, current, w.base, hash)
		default:
			merged, conflicts, err = threeWay(p, current, p.LoadLast()[rel].After, hash)
		}
		if err != nil {
			return ch, err
		}
		content, ch.Conflicts = merged, conflicts
	}
	if hash == "" && (!modified || !opts.ThreeWay) {
		if err := os.Remove(abs); err != nil && !errors.Is(err, os.ErrNotExist) {
			return ch, err
		}
	} else {
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return ch, err
		}
		if err := os.WriteFile(abs, content, 0o644); err != nil {
			return ch, err
		}
	}
	written := ""
	if len(content) > 0 || (hash != "" && ch.Conflicts == 0) {
		written, err = p.PutBytes(content)
		if err != nil {
			return ch, err
		}
	}
	ch.To = written
	now := time.Now()
	ev := store.Event{
		ID:        store.NewID(now),
		TS:        store.Now(now),
		Session:   session,
		Turn:      turn,
		Tool:      "pawprints",
		Kind:      kind,
		Path:      rel,
		Before:    store.HashPtr(current),
		After:     store.HashPtr(written),
		SizeAfter: int64(len(content)),
		Binary:    store.IsBinary(content),
	}
	if before, err := p.Get(current); err == nil && !ev.Binary && !store.IsBinary(before) {
		ev.SizeBefore = int64(len(before))
		st := diff.Count(before, content)
		ev.LinesAdded, ev.LinesRemoved = st.Added, st.Removed
	}
	if err := p.AppendEvent(ev); err != nil {
		return ch, err
	}
	if err := p.SetLast(rel, store.LastKnown{After: written, Event: ev.ID, TS: ev.TS}); err != nil {
		return ch, err
	}
	_ = p.UpdateSession(session, func(r *store.SessionRecord) {
		r.Edits++
		r.AddFile(rel)
		r.LastEventAt = ev.TS
	})
	return ch, nil
}

// threeWay merges the person's current file with the target content over a
// common base. The result may contain conflict markers; the count is
// returned.
func threeWay(p *store.Project, currentHash, baseHash, targetHash string) ([]byte, int, error) {
	var parts [3][]byte
	for i, hash := range []string{currentHash, baseHash, targetHash} {
		if hash == "" {
			continue
		}
		content, err := p.Get(hash)
		if err != nil {
			return nil, 0, err
		}
		parts[i] = content
	}
	return merge(parts[0], parts[1], parts[2])
}

// revertEach reverses Claude's changes one at a time, newest first, on top
// of the current file, so changes made outside Claude in between are kept.
func revertEach(p *store.Project, currentHash string, changes []store.Event) ([]byte, int, error) {
	var content []byte
	var err error
	if currentHash != "" {
		if content, err = p.Get(currentHash); err != nil {
			return nil, 0, err
		}
	}
	total := 0
	for _, ev := range changes {
		var after, before []byte
		if h := ev.AfterHash(); h != "" {
			if after, err = p.Get(h); err != nil {
				return nil, 0, err
			}
		}
		if h := ev.BeforeHash(); h != "" {
			if before, err = p.Get(h); err != nil {
				return nil, 0, err
			}
		}
		var conflicts int
		if content, conflicts, err = merge(content, after, before); err != nil {
			return nil, 0, err
		}
		total += conflicts
	}
	return content, total, nil
}

// merge runs git merge-file: the change from base to theirs applied to ours.
func merge(ours, base, theirs []byte) ([]byte, int, error) {
	dir, err := os.MkdirTemp("", "pawprints-merge-")
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	paths := make([]string, 3)
	for i, content := range [][]byte{ours, base, theirs} {
		paths[i] = filepath.Join(dir, []string{"ours", "base", "theirs"}[i])
		if err := os.WriteFile(paths[i], content, 0o644); err != nil {
			return nil, 0, err
		}
	}
	cmd := exec.Command("git", "merge-file", "-p", "-L", "yours", "-L", "base", "-L", "pawprints", paths[0], paths[1], paths[2])
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	err = cmd.Run()
	conflicts := 0
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() > 0 && exit.ExitCode() < 128 {
			conflicts = exit.ExitCode()
		} else {
			return nil, 0, fmt.Errorf("git merge-file: %s", bytes.TrimSpace(stderr.Bytes()))
		}
	}
	return out.Bytes(), conflicts, nil
}

func short(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}
