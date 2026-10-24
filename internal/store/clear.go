package store

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ClearScope selects what clear removes. Exactly one of the fields is set.
type ClearScope struct {
	OlderThan time.Duration
	Session   string
	Project   bool
	All       bool
	DropPins  bool
}

// ClearSummary is what would be, or was, removed.
type ClearSummary struct {
	Scope     string    `json:"scope"`
	Projects  int       `json:"projects"`
	Sessions  int       `json:"sessions"`
	Events    int       `json:"events"`
	Files     int       `json:"files"`
	Bytes     int64     `json:"bytes"`
	CutAt     time.Time `json:"-"`
	CutAtText string    `json:"cut_at,omitempty"`
	KeptPins  int       `json:"kept_pins"`
}

// Describe returns the human form of a scope.
func (s ClearScope) Describe() string {
	switch {
	case s.All:
		return "everything in every project"
	case s.Project:
		return "everything in this project"
	case s.Session != "":
		return "session " + s.Session
	case s.OlderThan > 0:
		return "history older than " + FormatDuration(s.OlderThan)
	}
	return "nothing"
}

// FormatDuration prints a duration in the units clear accepts.
func FormatDuration(d time.Duration) string {
	days := int(d.Hours() / 24)
	switch {
	case days >= 365 && days%365 == 0:
		return fmt.Sprintf("%dy", days/365)
	case days >= 30 && days%30 == 0:
		return fmt.Sprintf("%dm", days/30)
	case days >= 7 && days%7 == 0:
		return fmt.Sprintf("%dw", days/7)
	case days > 0:
		return fmt.Sprintf("%dd", days)
	}
	return d.String()
}

// PlanClear computes the summary without removing anything.
func (p *Project) PlanClear(scope ClearScope, now time.Time) (ClearSummary, error) {
	sum := ClearSummary{Scope: scope.Describe(), Projects: 1}
	pins, _ := p.Pins()
	pinned := map[string]bool{}
	for _, pin := range pins {
		pinned[pin.Event] = true
	}
	var events []Event
	var err error
	switch {
	case scope.All, scope.Project:
		events, err = p.Events(Query{})
		sum.Bytes = p.Size()
	case scope.Session != "":
		events, err = p.Events(Query{Session: scope.Session})
	case scope.OlderThan > 0:
		cut := now.Add(-scope.OlderThan)
		// Months are the unit: the cut is the start of the first kept month.
		sum.CutAt = time.Date(cut.Year(), cut.Month(), 1, 0, 0, 0, 0, cut.Location())
		sum.CutAtText = sum.CutAt.Format("2006-01-02")
		events, err = p.Events(Query{Until: sum.CutAt})
	}
	if err != nil {
		return sum, err
	}
	sessions := map[string]bool{}
	files := map[string]bool{}
	for _, ev := range events {
		if pinned[ev.ID] && !scope.DropPins {
			sum.KeptPins++
			continue
		}
		sum.Events++
		sessions[ev.Session] = true
		files[ev.Path] = true
	}
	if scope.Session != "" {
		sessions[scope.Session] = true
	}
	if scope.All || scope.Project {
		recs, _ := p.Sessions()
		for _, r := range recs {
			sessions[r.Session] = true
		}
	}
	sum.Sessions = len(sessions)
	sum.Files = len(files)
	if sum.Bytes == 0 {
		sum.Bytes = p.estimateBytes(events)
	}
	return sum, nil
}

// estimateBytes approximates reclaimable space from event sizes.
func (p *Project) estimateBytes(events []Event) int64 {
	var total int64
	seen := map[string]bool{}
	for _, ev := range events {
		if h := ev.AfterHash(); h != "" && !seen[h] {
			seen[h] = true
			total += ev.SizeAfter
		}
		if h := ev.BeforeHash(); h != "" && !seen[h] {
			seen[h] = true
			total += ev.SizeBefore
		}
	}
	return total / 3 // git compresses text roughly 3:1
}

// Clear removes history for the scope. Order is events, then session
// records, then gc, so an interruption leaves orphan blobs and never
// dangling events.
func (p *Project) Clear(scope ClearScope, now time.Time) (ClearSummary, error) {
	sum, err := p.PlanClear(scope, now)
	if err != nil {
		return sum, err
	}
	switch {
	case scope.All, scope.Project:
		// With the project's history gone, its sessions' pins point nowhere.
		UnpinSessionsOf(p.Root)
		if scope.DropPins || sum.KeptPins == 0 {
			return sum, os.RemoveAll(p.Dir)
		}
		if err := p.rewriteEvents(func(ev Event) bool { return p.IsPinned(ev.ID) }); err != nil {
			return sum, err
		}
		if err := p.removeAllSessions(); err != nil {
			return sum, err
		}
	case scope.Session != "":
		if err := p.rewriteEvents(func(ev Event) bool {
			return ev.Session != scope.Session || (p.IsPinned(ev.ID) && !scope.DropPins)
		}); err != nil {
			return sum, err
		}
		if err := p.DeleteSession(scope.Session); err != nil {
			return sum, err
		}
	case scope.OlderThan > 0:
		if _, err := p.dropMonthsBefore(sum.CutAt, scope.DropPins); err != nil {
			return sum, err
		}
		recs, _ := p.Sessions()
		for _, r := range recs {
			if r.LastActivity().Before(sum.CutAt) {
				_ = p.DeleteSession(r.Session)
			}
		}
	}
	if scope.DropPins {
		_ = os.Remove(p.pinsPath())
	}
	_ = os.Remove(p.lastPath())
	return sum, p.gcUnreferenced()
}

// rewriteEvents keeps only events for which keep returns true.
func (p *Project) rewriteEvents(keep func(Event) bool) error {
	return p.editEvents(func(ev *Event) bool { return keep(*ev) })
}

// editEvents passes every event to fn, which may change it in place and
// returns false to drop it. Only months where something changed are
// written back.
func (p *Project) editEvents(fn func(*Event) bool) error {
	lock, err := p.eventsLock(true)
	if err != nil {
		return err
	}
	defer lock.Release()
	months, err := p.Months()
	if err != nil {
		return err
	}
	for _, m := range months {
		evs, err := p.readMonth(m)
		if err != nil {
			return err
		}
		path := filepath.Join(p.eventsDir(), m+".jsonl")
		var buf []byte
		changed := false
		for _, ev := range evs {
			old, err := jsonLine(ev)
			if err != nil {
				return err
			}
			if !fn(&ev) {
				changed = true
				continue
			}
			line, err := jsonLine(ev)
			if err != nil {
				return err
			}
			changed = changed || !bytes.Equal(old, line)
			buf = append(buf, line...)
		}
		if !changed {
			continue
		}
		if len(buf) == 0 {
			if err := os.Remove(path); err != nil {
				return err
			}
			continue
		}
		if err := writeAtomic(path, buf); err != nil {
			return err
		}
	}
	return nil
}

func (p *Project) removeAllSessions() error {
	recs, err := p.Sessions()
	if err != nil {
		return err
	}
	for _, r := range recs {
		if err := p.DeleteSession(r.Session); err != nil {
			return err
		}
	}
	return nil
}

// ClearAll removes every project. Config and the format file stay.
func ClearAll() (ClearSummary, error) {
	projects, err := ListProjects()
	if err != nil {
		return ClearSummary{}, err
	}
	sum := ClearSummary{Scope: "everything in every project"}
	for _, p := range projects {
		s, err := p.PlanClear(ClearScope{All: true, DropPins: true}, time.Now())
		if err != nil {
			return sum, err
		}
		sum.Projects++
		sum.Sessions += s.Sessions
		sum.Events += s.Events
		sum.Files += s.Files
		sum.Bytes += s.Bytes
	}
	if err := os.RemoveAll(filepath.Join(Home(), "session-roots")); err != nil {
		return sum, err
	}
	return sum, os.RemoveAll(filepath.Join(Home(), "projects"))
}

// PlanClearAll summarizes ClearAll without removing anything.
func PlanClearAll() (ClearSummary, error) {
	projects, err := ListProjects()
	if err != nil {
		return ClearSummary{}, err
	}
	sum := ClearSummary{Scope: "everything in every project"}
	for _, p := range projects {
		s, err := p.PlanClear(ClearScope{All: true, DropPins: true}, time.Now())
		if err != nil {
			return sum, err
		}
		sum.Projects++
		sum.Sessions += s.Sessions
		sum.Events += s.Events
		sum.Files += s.Files
		sum.Bytes += s.Bytes
	}
	return sum, nil
}
