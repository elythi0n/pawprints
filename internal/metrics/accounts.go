package metrics

import (
	"sort"
	"strings"
	"time"

	"github.com/elythi0n/pawprints/internal/store"
)

// Account statuses, worst first.
const (
	StatusLimited   = "limited"
	StatusFilling   = "filling"
	StatusAvailable = "available"
	StatusUnknown   = "unknown"
)

// Window paces.
const (
	PaceUnknown = "unknown"
	PaceIdle    = "idle"
	PaceCalm    = "calm"
	PaceTight   = "tight"
	PaceFull    = "full"
)

// WindowStatus is where one usage-limit window of an account stands now.
type WindowStatus struct {
	Kind      string  `json:"kind"`
	Percent   float64 `json:"percent"`
	Assumed   bool    `json:"assumed,omitempty"`
	ResetsAt  string  `json:"resets_at,omitempty"`
	Projected bool    `json:"projected,omitempty"`
	Full      bool    `json:"full"`
	Pace      string  `json:"pace"`
	PerHour   float64 `json:"per_hour"`
	FullAt    string  `json:"full_at,omitempty"`
	UpdatedAt string  `json:"updated_at"`
}

// AccountLimits is one account's usage limits: whether it can be used now,
// when it unlocks, and when it fills at the current pace.
type AccountLimits struct {
	Account   string         `json:"account"`
	Email     string         `json:"email,omitempty"`
	Plan      string         `json:"plan,omitempty"`
	Status    string         `json:"status"`
	UnlocksAt string         `json:"unlocks_at,omitempty"`
	FullAt    string         `json:"full_at,omitempty"`
	UpdatedAt string         `json:"updated_at,omitempty"`
	Windows   []WindowStatus `json:"windows"`
}

// windowRules says how far back a pace looks, how long it must span, and
// when a window counts as idle. A five-hour window starts with the first
// message after a reset, so its next reset is unknown until it is used; a
// weekly one resets on a fixed schedule.
type windowRules struct {
	lookback, minSpan, idle time.Duration
	weekly                  bool
}

func rulesFor(kind string) windowRules {
	if strings.HasPrefix(kind, "five_hour") {
		return windowRules{lookback: time.Hour, minSpan: 15 * time.Minute, idle: 20 * time.Minute}
	}
	return windowRules{lookback: 24 * time.Hour, minSpan: 3 * time.Hour, idle: 6 * time.Hour, weekly: strings.HasPrefix(kind, "seven_day")}
}

// paceMinRise is the smallest rise a pace is computed from; readings are
// whole percents, so one step alone says little.
const paceMinRise = 2

// Limits reports every known account's usage limits as of now, from the
// readings Whiskers sent. Accounts are ordered limited first, then filling,
// available, and unknown.
func Limits(now time.Time) ([]AccountLimits, error) {
	readings, err := store.Limits(time.Time{})
	if err != nil {
		return nil, err
	}
	return accountLimits(readings, store.Accounts(), now), nil
}

func accountLimits(readings []store.LimitReading, known map[string]store.Account, now time.Time) []AccountLimits {
	byAccount := map[string]map[string][]store.LimitReading{}
	for _, r := range readings {
		if r.Kind == "" || r.Time().IsZero() || r.Time().After(now) {
			continue
		}
		if byAccount[r.Account] == nil {
			byAccount[r.Account] = map[string][]store.LimitReading{}
		}
		byAccount[r.Account][r.Kind] = append(byAccount[r.Account][r.Kind], r)
	}
	ids := map[string]bool{}
	for id := range known {
		ids[id] = true
	}
	for id := range byAccount {
		ids[id] = true
	}
	out := []AccountLimits{}
	for id := range ids {
		k := known[id]
		a := AccountLimits{Account: id, Email: k.Email, Plan: k.Plan, Status: StatusUnknown, Windows: []WindowStatus{}}
		for kind, rs := range byAccount[id] {
			a.Windows = append(a.Windows, windowStatus(kind, rs, now))
		}
		sort.Slice(a.Windows, func(i, j int) bool {
			if r1, r2 := kindRank(a.Windows[i].Kind), kindRank(a.Windows[j].Kind); r1 != r2 {
				return r1 < r2
			}
			return a.Windows[i].Kind < a.Windows[j].Kind
		})
		settle(&a)
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool {
		if r1, r2 := statusRank(out[i].Status), statusRank(out[j].Status); r1 != r2 {
			return r1 < r2
		}
		if out[i].Email != out[j].Email {
			return out[i].Email < out[j].Email
		}
		return out[i].Account < out[j].Account
	})
	return out
}

func kindRank(kind string) int {
	switch kind {
	case "five_hour":
		return 0
	case "seven_day":
		return 1
	}
	return 2
}

func statusRank(s string) int {
	switch s {
	case StatusLimited:
		return 0
	case StatusFilling:
		return 1
	case StatusAvailable:
		return 2
	}
	return 3
}

// windowStatus works out one window from its readings, oldest first.
func windowStatus(kind string, rs []store.LimitReading, now time.Time) WindowStatus {
	sort.SliceStable(rs, func(i, j int) bool { return rs[i].Time().Before(rs[j].Time()) })
	rules := rulesFor(kind)
	last := rs[len(rs)-1]
	w := WindowStatus{Kind: kind, Percent: last.Percent, ResetsAt: last.ResetsAt, Pace: PaceUnknown, UpdatedAt: store.Now(last.Time())}

	// A reset that passed with no reading since: the window is empty again.
	if reset, err := time.Parse(time.RFC3339Nano, last.ResetsAt); err == nil && !reset.After(now) {
		w.Percent, w.Assumed, w.ResetsAt = 0, true, ""
		if rules.weekly {
			for !reset.After(now) {
				reset = reset.Add(7 * 24 * time.Hour)
			}
			w.ResetsAt, w.Projected = store.Now(reset), true
		}
		return w
	}
	if w.Percent >= 100 {
		w.Full, w.Pace = true, PaceFull
		return w
	}
	if now.Sub(last.Time()) > rules.idle {
		w.Pace = PaceIdle
		return w
	}

	// The pace counts only readings since the window last reset.
	start := 0
	for i := 1; i < len(rs); i++ {
		if rs[i-1].Percent-rs[i].Percent > 5 {
			start = i
		}
	}
	first := -1
	for i := start; i < len(rs); i++ {
		if last.Time().Sub(rs[i].Time()) <= rules.lookback {
			first = i
			break
		}
	}
	if first < 0 {
		return w
	}
	span := last.Time().Sub(rs[first].Time())
	rise := last.Percent - rs[first].Percent
	if span < rules.minSpan || rise < paceMinRise {
		return w
	}
	w.PerHour = rise / span.Hours()
	full := now.Add(time.Duration((100 - w.Percent) / w.PerHour * float64(time.Hour)))
	if reset, err := time.Parse(time.RFC3339Nano, w.ResetsAt); err == nil && !full.Before(reset) {
		w.Pace = PaceCalm
		return w
	}
	w.Pace, w.FullAt = PaceTight, store.Now(full)
	return w
}

// settle derives the account's status from its windows.
func settle(a *AccountLimits) {
	var unlocks, full, updated time.Time
	for _, w := range a.Windows {
		if t, err := time.Parse(time.RFC3339Nano, w.UpdatedAt); err == nil && t.After(updated) {
			updated = t
			a.UpdatedAt = w.UpdatedAt
		}
		if w.Full {
			// Unlocking needs every full window to reset, so the latest wins.
			if t, err := time.Parse(time.RFC3339Nano, w.ResetsAt); err == nil && t.After(unlocks) {
				unlocks = t
			}
			a.Status = StatusLimited
		}
		if w.Pace == PaceTight {
			if t, err := time.Parse(time.RFC3339Nano, w.FullAt); err == nil && (full.IsZero() || t.Before(full)) {
				full = t
			}
		}
	}
	switch {
	case a.Status == StatusLimited:
		if !unlocks.IsZero() {
			a.UnlocksAt = store.Now(unlocks)
		}
	case !full.IsZero():
		a.Status, a.FullAt = StatusFilling, store.Now(full)
	case len(a.Windows) > 0:
		a.Status = StatusAvailable
	}
}
