package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// PruneReport says what prune did.
type PruneReport struct {
	Project         string `json:"project"`
	MonthsRemoved   int    `json:"months_removed"`
	PendingRemoved  int    `json:"pending_removed"`
	SessionsRemoved int    `json:"sessions_removed"`
	BytesBefore     int64  `json:"bytes_before"`
	BytesAfter      int64  `json:"bytes_after"`
	GCRan           bool   `json:"gc_ran"`
}

// Prune applies retention: drops whole months older than the window, stale
// pending records, session records with nothing left in the window, then
// garbage-collects blobs. Pinned content is always kept.
func (p *Project) Prune(cfg Config, now time.Time) (PruneReport, error) {
	rep := PruneReport{Project: p.Root, BytesBefore: p.Size()}
	cutoff := now.AddDate(0, 0, -cfg.RetentionDays)
	n, err := p.dropMonthsBefore(cutoff, false)
	if err != nil {
		return rep, err
	}
	rep.MonthsRemoved = n
	rep.PendingRemoved = p.StalePending()
	rep.SessionsRemoved = p.dropStaleSessions(cutoff)
	if err := p.gcUnreferenced(); err != nil {
		return rep, err
	}
	rep.GCRan = true
	rep.BytesAfter = p.Size()
	// Over the size cap: drop the oldest month and gc again, until under.
	for rep.BytesAfter > cfg.MaxProjectBytes {
		months, _ := p.Months()
		if len(months) <= 1 {
			break
		}
		if err := p.removeMonth(months[0], false); err != nil {
			return rep, err
		}
		rep.MonthsRemoved++
		if err := p.gcUnreferenced(); err != nil {
			return rep, err
		}
		rep.BytesAfter = p.Size()
	}
	_ = p.touchPruneStamp(now)
	return rep, nil
}

// dropMonthsBefore removes event files whose whole month ends before cutoff.
// With keepPins false, pinned events in those months are rewritten into a
// "pinned" file so they survive; with true the month is removed outright.
func (p *Project) dropMonthsBefore(cutoff time.Time, dropPins bool) (int, error) {
	months, err := p.Months()
	if err != nil {
		return 0, err
	}
	n := 0
	for _, m := range months {
		if !monthEnd(m).Before(cutoff) {
			break
		}
		if err := p.removeMonth(m, dropPins); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// removeMonth deletes a month file. Pinned events are re-appended to the
// current month so their history stays browsable, unless dropPins is set.
func (p *Project) removeMonth(month string, dropPins bool) error {
	if !dropPins {
		evs, err := p.readMonth(month)
		if err != nil {
			return err
		}
		pins, _ := p.Pins()
		pinned := map[string]bool{}
		for _, pin := range pins {
			pinned[pin.Event] = true
		}
		current := monthFile(time.Now())
		for _, ev := range evs {
			if pinned[ev.ID] && current != month+".jsonl" {
				if err := p.appendToMonth(ev, current); err != nil {
					return err
				}
			}
		}
	}
	err := os.Remove(filepath.Join(p.eventsDir(), month+".jsonl"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (p *Project) dropStaleSessions(cutoff time.Time) int {
	recs, err := p.Sessions()
	if err != nil {
		return 0
	}
	n := 0
	for _, rec := range recs {
		if rec.LastActivity().Before(cutoff) && rec.EndedAt != "" {
			if p.DeleteSession(rec.Session) == nil {
				n++
			}
		}
	}
	return n
}

func (p *Project) gcUnreferenced() error {
	keep, err := p.ReferencedHashes()
	if err != nil {
		return err
	}
	return p.GC(keep)
}

type pruneStamp struct {
	LastRun time.Time `json:"last_run"`
}

func (p *Project) pruneStampPath() string { return filepath.Join(p.Dir, "state", "prune.json") }

func (p *Project) touchPruneStamp(now time.Time) error {
	data, _ := json.Marshal(pruneStamp{LastRun: now})
	return writeAtomic(p.pruneStampPath(), data)
}

// PruneDue reports whether more than interval has passed since the last prune.
func (p *Project) PruneDue(interval time.Duration, now time.Time) bool {
	data, err := os.ReadFile(p.pruneStampPath())
	if err != nil {
		return true
	}
	var st pruneStamp
	if json.Unmarshal(data, &st) != nil {
		return true
	}
	return now.Sub(st.LastRun) > interval
}
