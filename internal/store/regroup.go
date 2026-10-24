package store

import (
	"os"
	"sort"
)

// RegroupReport says what moved.
type RegroupReport struct {
	Events   int      `json:"events"`
	Sessions int      `json:"sessions"`
	Pins     int      `json:"pins"`
	Removed  []string `json:"removed_projects"`
	Into     []string `json:"into_projects"`
}

// Regroup moves every event whose session is pinned to a different root
// into that root's project, with its blobs, session record, pins, and
// last-known entries. Projects left empty are removed. roots maps session id
// to project root.
func Regroup(roots map[string]string, dryRun bool) (RegroupReport, error) {
	var rep RegroupReport
	projects, err := ListProjects()
	if err != nil {
		return rep, err
	}
	into := map[string]bool{}
	for _, src := range projects {
		evs, err := src.Events(Query{})
		if err != nil {
			return rep, err
		}
		moving := map[string][]Event{} // target root → events
		for _, ev := range evs {
			root, ok := roots[ev.Session]
			if !ok || root == src.Root {
				continue
			}
			moving[root] = append(moving[root], ev)
		}
		if len(moving) == 0 {
			continue
		}
		for root, list := range moving {
			rep.Events += len(list)
			into[root] = true
			if dryRun {
				continue
			}
			dst, err := OpenProject(root)
			if err != nil {
				return rep, err
			}
			n, err := moveEvents(src, dst, list)
			if err != nil {
				return rep, err
			}
			rep.Sessions += n.sessions
			rep.Pins += n.pins
		}
		if dryRun {
			continue
		}
		if removed, err := src.RemoveIfEmpty(); err != nil {
			return rep, err
		} else if removed {
			rep.Removed = append(rep.Removed, src.Root)
		}
	}
	for root := range into {
		rep.Into = append(rep.Into, root)
	}
	sort.Strings(rep.Into)
	return rep, nil
}

type moved struct{ sessions, pins int }

func moveEvents(src, dst *Project, list []Event) (moved, error) {
	var n moved
	ids := map[string]bool{}
	sessionsSeen := map[string]bool{}
	pathMap := map[string]string{}
	for _, ev := range list {
		ids[ev.ID] = true
		sessionsSeen[ev.Session] = true
		newPath := dst.Rel(src.Abs(ev.Path))
		pathMap[ev.Path] = newPath
		for _, h := range []string{ev.BeforeHash(), ev.AfterHash()} {
			if h == "" || dst.Has(h) {
				continue
			}
			content, err := src.Get(h)
			if err != nil {
				continue // a pruned blob stays missing; the event still moves
			}
			if _, err := dst.PutBytes(content); err != nil {
				return n, err
			}
		}
		ev.Path = newPath
		if err := dst.appendToMonth(ev, monthFile(ev.Time())); err != nil {
			return n, err
		}
	}
	if err := src.rewriteEvents(func(ev Event) bool { return !ids[ev.ID] }); err != nil {
		return n, err
	}
	for id := range sessionsSeen {
		rec, ok := src.LoadSession(id)
		if !ok {
			continue
		}
		srcReadFurther := false
		if err := dst.UpdateSession(id, func(r *SessionRecord) {
			srcReadFurther = rec.TranscriptOffset > r.TranscriptOffset
			mergeRecord(r, rec, pathMap)
		}); err != nil {
			return n, err
		}
		// The per-minute log goes with the copy whose token counts are kept.
		if srcReadFurther || !dst.HasUsageLog(id) {
			if err := dst.copyUsageFrom(src, id); err != nil {
				return n, err
			}
		}
		if err := src.DeleteSession(id); err != nil {
			return n, err
		}
		n.sessions++
	}
	pins, _ := src.Pins()
	var keep []Pin
	for _, pin := range pins {
		if !ids[pin.Event] {
			keep = append(keep, pin)
			continue
		}
		pin.Path = pathMap[pin.Path]
		if err := dst.appendPin(pin); err != nil {
			return n, err
		}
		n.pins++
	}
	if n.pins > 0 {
		if err := src.writePins(keep); err != nil {
			return n, err
		}
	}
	srcLast := src.LoadLast()
	if len(srcLast) > 0 {
		dstLast := dst.LoadLast()
		changed := false
		for old, lk := range srcLast {
			newPath, ok := pathMap[old]
			if !ok {
				continue
			}
			if cur, exists := dstLast[newPath]; !exists || cur.TS < lk.TS {
				dstLast[newPath] = lk
			}
			delete(srcLast, old)
			changed = true
		}
		if changed {
			if err := dst.SaveLast(dstLast); err != nil {
				return n, err
			}
			if len(srcLast) == 0 {
				_ = os.Remove(src.lastPath())
			} else if err := src.SaveLast(srcLast); err != nil {
				return n, err
			}
		}
	}
	return n, nil
}

// mergeRecord folds a session record from another project into r.
func mergeRecord(r *SessionRecord, o SessionRecord, pathMap map[string]string) {
	if r.Session == "" {
		r.Session = o.Session
	}
	if r.StartedAt == "" || (o.StartedAt != "" && o.StartedAt < r.StartedAt) {
		r.StartedAt = o.StartedAt
	}
	if r.StartSource == "" {
		r.StartSource = o.StartSource
	}
	if r.TranscriptPath == "" {
		r.TranscriptPath = o.TranscriptPath
	}
	if r.FirstPrompt == "" {
		r.FirstPrompt = o.FirstPrompt
	}
	if o.LastPromptAt > r.LastPromptAt {
		r.LastPrompt, r.LastPromptAt = o.LastPrompt, o.LastPromptAt
	}
	if o.LastStopAt > r.LastStopAt {
		r.LastStopAt = o.LastStopAt
	}
	if o.LastEventAt > r.LastEventAt {
		r.LastEventAt = o.LastEventAt
	}
	if o.EndedAt > r.EndedAt {
		r.EndedAt, r.EndReason = o.EndedAt, o.EndReason
	}
	seen := map[string]bool{}
	for _, t := range r.Turns {
		seen[t.ID] = true
	}
	for _, t := range o.Turns {
		if !seen[t.ID] {
			r.Turns = append(r.Turns, t)
		}
	}
	sort.Slice(r.Turns, func(i, j int) bool { return r.Turns[i].ID < r.Turns[j].ID })
	r.Edits += o.Edits
	r.LinesAdded += o.LinesAdded
	r.LinesRemoved += o.LinesRemoved
	for _, f := range o.Files {
		if mapped, ok := pathMap[f]; ok {
			f = mapped
		}
		r.AddFile(f)
	}
	// Usage is read from the same transcripts by every copy; keep the copy
	// that read furthest, with how far it read each one, instead of adding
	// them up.
	if o.TranscriptOffset > r.TranscriptOffset {
		r.Usage, r.TranscriptOffset, r.LastMessageID, r.SubagentReads = o.Usage, o.TranscriptOffset, o.LastMessageID, o.SubagentReads
	}
	if r.Account == "" || (o.Account != "" && o.LastActivity().After(r.LastActivity())) {
		r.Account = o.Account
	}
	for _, a := range o.Agents {
		cur, ok := r.AgentByID(a.ID)
		if !ok {
			r.Agents = append(r.Agents, a)
			continue
		}
		if cur.Turn == "" {
			cur.Turn = a.Turn
		}
		if cur.Type == "" {
			cur.Type = a.Type
		}
		if cur.StartedAt == "" {
			cur.StartedAt = a.StartedAt
		}
		if cur.StoppedAt == "" {
			cur.StoppedAt = a.StoppedAt
		}
		if cur.Description == "" {
			cur.Description = a.Description
		}
		if cur.Report == "" {
			cur.Summary, cur.Report = a.Summary, a.Report
		}
	}
	for _, d := range o.ExtraRoots {
		r.AddExtraRoot(d)
	}
}
