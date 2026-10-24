package hook

import (
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/elythi0n/pawprints/internal/diff"
	"github.com/elythi0n/pawprints/internal/store"
)

// maxDiffBytes caps the size of files whose line counts are computed in the
// hook; a Myers diff of a huge change could otherwise stall Claude.
const maxDiffBytes = 1 << 20

type target struct {
	project *store.Project
	abs     string
	rel     string
	cfg     store.Config
}

func resolveTarget(in Input) (target, bool, error) {
	path := in.TargetPath()
	if path == "" {
		return target{}, false, nil
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(in.CWD, path)
	}
	path = filepath.Clean(path)
	cfg, err := store.LoadConfig()
	if err != nil {
		return target{}, false, err
	}
	// Files outside the session's project stay with the session and are
	// stored under their absolute path, so one session reads as one story.
	root := rootFor(in)
	p, err := store.OpenProject(root)
	if err != nil {
		return target{}, false, err
	}
	rel := p.Rel(path)
	if store.LoadProjectIgnore(root, cfg.Ignore).Match(rel) {
		return target{}, false, nil
	}
	return target{project: p, abs: path, rel: rel, cfg: cfg}, true, nil
}

// snapshot stores the file's current content and describes it.
func (t target) snapshot() (hash string, size int64, binary bool, skipped string, content []byte, err error) {
	info, statErr := os.Stat(t.abs)
	if errors.Is(statErr, os.ErrNotExist) {
		return "", 0, false, "", nil, nil
	}
	if statErr != nil {
		return "", 0, false, "", nil, statErr
	}
	if info.Size() > t.cfg.MaxFileBytes {
		return "", info.Size(), false, "too_large", nil, nil
	}
	content, err = os.ReadFile(t.abs)
	if err != nil {
		return "", 0, false, "", nil, err
	}
	hash, err = t.project.PutBytes(content)
	if err != nil {
		return "", 0, false, "", nil, err
	}
	return hash, int64(len(content)), store.IsBinary(content), "", content, nil
}

func runPre(in Input) error {
	t, ok, err := resolveTarget(in)
	if err != nil || !ok {
		return err
	}
	noteDir(t.project, in, in.CWD)
	rec, _ := t.project.LoadSession(in.SessionID)
	turn := rec.TurnFor(in.AgentID)
	now := time.Now()

	hash, size, binary, skipped, content, err := t.snapshot()
	if err != nil {
		return err
	}
	if err := t.recordExternal(in, turn, hash, size, binary, content, now); err != nil {
		return err
	}
	return t.project.WritePending(in.ToolUseID, store.Pending{
		TS:         store.Now(now),
		StartNano:  now.UnixNano(),
		Session:    in.SessionID,
		Turn:       turn,
		Tool:       in.ToolName,
		Path:       t.rel,
		Before:     hash,
		SizeBefore: size,
		Binary:     binary,
		Skipped:    skipped,
		CWD:        in.CWD,
		Agent:      in.AgentID,
		AgentType:  in.AgentType,
	})
}

// recordExternal writes an event when the disk no longer matches what the
// store last saw for this path, such as after a person edited the file. It
// follows the same rules as a command scan, so a change another Claude call
// is making, or one in a file another project owns, is left alone.
func (t target) recordExternal(in Input, turn, current string, size int64, binary bool, content []byte, now time.Time) error {
	running := newAttribution(t.project, in.SessionID, turn, nil, store.KnownRoots(), now)
	last, known := t.project.LoadLast()[t.rel]
	if !known || last.After == current {
		return nil
	}
	mtime := now
	if info, err := os.Stat(t.abs); err == nil {
		mtime = info.ModTime()
	}
	if !running.outsideClaude(t.abs, mtime) {
		return nil
	}
	ev := store.Event{
		TS:        store.Now(now.Add(-time.Millisecond)),
		Session:   in.SessionID,
		Turn:      turn,
		Tool:      "external",
		Kind:      store.KindExternal,
		Path:      t.rel,
		Before:    store.HashPtr(last.After),
		After:     store.HashPtr(current),
		SizeAfter: size,
		Binary:    binary,
		CWD:       in.CWD,
	}
	before, err := t.project.Get(last.After)
	if err == nil {
		ev.SizeBefore = int64(len(before))
		if !binary && !store.IsBinary(before) && len(before) <= maxDiffBytes && len(content) <= maxDiffBytes {
			st := diff.Count(before, content)
			ev.LinesAdded, ev.LinesRemoved = st.Added, st.Removed
		}
	}
	if err := t.project.AppendEvent(ev); err != nil {
		return err
	}
	return t.updateSession(in.SessionID, t.rel, ev, now)
}

func runPost(in Input) error {
	t, ok, err := resolveTarget(in)
	if err != nil || !ok {
		return err
	}
	now := time.Now()
	// The record is removed only once the edit is recorded, so other
	// sessions' scans leave the file alone until then.
	defer t.project.DonePending(in.ToolUseID)
	pe, hadPre := t.project.ReadPending(in.ToolUseID)
	if !hadPre {
		// The pre hook did not run or failed: fall back to the last known
		// content so the event still has a usable "before".
		rec, _ := t.project.LoadSession(in.SessionID)
		pe = store.Pending{Session: in.SessionID, Turn: rec.TurnFor(in.AgentID), Tool: in.ToolName, Path: t.rel, CWD: in.CWD}
		if last, known := t.project.LoadLast()[t.rel]; known {
			pe.Before = last.After
		}
	}
	after, size, binary, skipped, content, err := t.snapshot()
	if err != nil {
		return err
	}
	if pe.Skipped != "" {
		skipped = pe.Skipped
	}
	ev := store.Event{
		ID:         store.NewID(now),
		TS:         store.Now(now),
		Session:    in.SessionID,
		Turn:       pe.Turn,
		ToolUseID:  in.ToolUseID,
		Tool:       in.ToolName,
		Kind:       store.KindEdit,
		Path:       t.rel,
		Before:     store.HashPtr(pe.Before),
		After:      store.HashPtr(after),
		SizeBefore: pe.SizeBefore,
		SizeAfter:  size,
		Binary:     binary || pe.Binary,
		Skipped:    skipped,
		CWD:        in.CWD,
		Agent:      in.AgentID,
		AgentType:  in.AgentType,
	}
	if !ev.Binary && skipped == "" && pe.Before != after && len(content) <= maxDiffBytes {
		before, err := t.project.Get(pe.Before)
		if err == nil && len(before) <= maxDiffBytes {
			st := diff.Count(before, content)
			ev.LinesAdded, ev.LinesRemoved = st.Added, st.Removed
		}
	}
	if err := t.project.AppendEvent(ev); err != nil {
		return err
	}
	return t.updateSession(in.SessionID, t.rel, ev, now)
}

// updateSession bumps the counters on the session record and refreshes the
// last-known map.
func (t target) updateSession(session, rel string, ev store.Event, now time.Time) error {
	if err := t.project.UpdateSession(session, func(r *store.SessionRecord) {
		r.Edits++
		r.AddFile(rel)
		r.LinesAdded += ev.LinesAdded
		r.LinesRemoved += ev.LinesRemoved
		r.LastEventAt = store.Now(now)
		if r.CWD == "" {
			r.CWD = ev.CWD
		}
	}); err != nil {
		return err
	}
	return t.project.SetLast(rel, store.LastKnown{After: ev.AfterHash(), Event: ev.ID, TS: ev.TS})
}
