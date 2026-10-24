package metrics

import (
	"math"
	"testing"
	"time"

	"github.com/elythi0n/pawprints/internal/store"
)

func reading(account, kind string, ago time.Duration, percent float64, resetsIn time.Duration) store.LimitReading {
	r := store.LimitReading{At: store.Now(now.Add(-ago)), Account: account, Kind: kind, Percent: percent}
	if resetsIn != 0 {
		r.ResetsAt = store.Now(now.Add(resetsIn))
	}
	return r
}

func only(t *testing.T, out []AccountLimits, id string) AccountLimits {
	t.Helper()
	for _, a := range out {
		if a.Account == id {
			return a
		}
	}
	t.Fatalf("no account %q in %+v", id, out)
	return AccountLimits{}
}

func window(t *testing.T, a AccountLimits, kind string) WindowStatus {
	t.Helper()
	for _, w := range a.Windows {
		if w.Kind == kind {
			return w
		}
	}
	t.Fatalf("no %s window in %+v", kind, a.Windows)
	return WindowStatus{}
}

func TestFullWindowLimitsUntilItsReset(t *testing.T) {
	out := accountLimits([]store.LimitReading{
		reading("a", "five_hour", 30*time.Minute, 97, 2*time.Hour),
		reading("a", "five_hour", 10*time.Minute, 100, 2*time.Hour),
	}, nil, now)
	a := only(t, out, "a")
	if a.Status != StatusLimited || a.UnlocksAt != store.Now(now.Add(2*time.Hour)) {
		t.Fatalf("limited until the reset: %+v", a)
	}
	if w := window(t, a, "five_hour"); !w.Full || w.Pace != PaceFull {
		t.Fatalf("window %+v", w)
	}
}

func TestFullWeekWinsOverAnEmptyFiveHours(t *testing.T) {
	out := accountLimits([]store.LimitReading{
		reading("a", "five_hour", 5*time.Minute, 3, 4*time.Hour),
		reading("a", "seven_day", 5*time.Minute, 100, 50*time.Hour),
	}, nil, now)
	a := only(t, out, "a")
	if a.Status != StatusLimited || a.UnlocksAt != store.Now(now.Add(50*time.Hour)) {
		t.Fatalf("the week decides: %+v", a)
	}
}

func TestPassedResetIsAssumedEmpty(t *testing.T) {
	out := accountLimits([]store.LimitReading{
		reading("a", "five_hour", 3*time.Hour, 100, -time.Hour),
		reading("a", "seven_day", 8*24*time.Hour, 70, -24*time.Hour),
	}, nil, now)
	a := only(t, out, "a")
	if a.Status != StatusAvailable {
		t.Fatalf("both windows reset since: %+v", a)
	}
	five := window(t, a, "five_hour")
	if five.Percent != 0 || !five.Assumed || five.ResetsAt != "" || five.Full {
		t.Fatalf("five hours starts again with the next message: %+v", five)
	}
	week := window(t, a, "seven_day")
	if week.Percent != 0 || !week.Assumed || !week.Projected || week.ResetsAt != store.Now(now.Add(6*24*time.Hour)) {
		t.Fatalf("the week carries its reset forward: %+v", week)
	}
}

func TestSteadyRiseGivesPaceAndFullAt(t *testing.T) {
	out := accountLimits([]store.LimitReading{
		reading("a", "five_hour", 40*time.Minute, 40, 3*time.Hour),
		reading("a", "five_hour", 20*time.Minute, 50, 3*time.Hour),
		reading("a", "five_hour", 0, 60, 3*time.Hour),
	}, nil, now)
	a := only(t, out, "a")
	w := window(t, a, "five_hour")
	if math.Abs(w.PerHour-30) > 0.01 || w.Pace != PaceTight {
		t.Fatalf("20 points in 40 minutes is 30%%/h, full before the reset: %+v", w)
	}
	if w.FullAt != store.Now(now.Add(80*time.Minute)) || a.Status != StatusFilling || a.FullAt != w.FullAt {
		t.Fatalf("40 points left at 30%%/h is 80 minutes: %+v", a)
	}
}

func TestResetBeforeFullIsCalm(t *testing.T) {
	out := accountLimits([]store.LimitReading{
		reading("a", "five_hour", 30*time.Minute, 10, 20*time.Minute),
		reading("a", "five_hour", 0, 14, 20*time.Minute),
	}, nil, now)
	a := only(t, out, "a")
	if w := window(t, a, "five_hour"); w.Pace != PaceCalm || w.FullAt != "" || a.Status != StatusAvailable {
		t.Fatalf("8%%/h will not fill 86 points in 20 minutes: %+v", a)
	}
}

func TestPaceNeedsEnoughReadings(t *testing.T) {
	for name, rs := range map[string][]store.LimitReading{
		"one reading": {reading("a", "five_hour", 0, 30, time.Hour)},
		"short span":  {reading("a", "five_hour", 5*time.Minute, 30, time.Hour), reading("a", "five_hour", 0, 33, time.Hour)},
		"small rise":  {reading("a", "five_hour", 40*time.Minute, 30, time.Hour), reading("a", "five_hour", 0, 31, time.Hour)},
		"older than the lookback": {
			reading("a", "five_hour", 3*time.Hour, 10, time.Hour),
			reading("a", "five_hour", 0, 40, time.Hour),
		},
	} {
		w := window(t, only(t, accountLimits(rs, nil, now), "a"), "five_hour")
		if w.Pace != PaceUnknown || w.PerHour != 0 {
			t.Errorf("%s: %+v", name, w)
		}
	}
}

func TestPaceStartsAfterAReset(t *testing.T) {
	out := accountLimits([]store.LimitReading{
		reading("a", "five_hour", 50*time.Minute, 90, 4*time.Hour),
		reading("a", "five_hour", 30*time.Minute, 2, 4*time.Hour),
		reading("a", "five_hour", 0, 12, 4*time.Hour),
	}, nil, now)
	w := window(t, only(t, out, "a"), "five_hour")
	if math.Abs(w.PerHour-20) > 0.01 {
		t.Fatalf("the drop from 90 is a reset; the pace is from 2 to 12 in 30 minutes: %+v", w)
	}
}

func TestOldReadingIsIdle(t *testing.T) {
	out := accountLimits([]store.LimitReading{
		reading("a", "five_hour", 90*time.Minute, 20, 2*time.Hour),
		reading("a", "five_hour", time.Hour, 40, 2*time.Hour),
	}, nil, now)
	if w := window(t, only(t, out, "a"), "five_hour"); w.Pace != PaceIdle || w.FullAt != "" {
		t.Fatalf("nobody has spent for an hour: %+v", w)
	}
}

func TestKnownAccountWithoutReadings(t *testing.T) {
	known := map[string]store.Account{"b": {ID: "b", Email: "b@example.com", Plan: "Pro"}}
	out := accountLimits([]store.LimitReading{reading("a", "five_hour", 0, 30, time.Hour)}, known, now)
	b := only(t, out, "b")
	if b.Status != StatusUnknown || len(b.Windows) != 0 || b.Email != "b@example.com" {
		t.Fatalf("a card with no data: %+v", b)
	}
	if out[len(out)-1].Account != "b" {
		t.Fatalf("unknown accounts come last: %+v", out)
	}
}

func TestAccountsNeverMix(t *testing.T) {
	out := accountLimits([]store.LimitReading{
		reading("a", "five_hour", 30*time.Minute, 10, 3*time.Hour),
		reading("b", "five_hour", 20*time.Minute, 100, 2*time.Hour),
		reading("a", "five_hour", 0, 20, 3*time.Hour),
	}, nil, now)
	if len(out) != 2 || out[0].Account != "b" || out[0].Status != StatusLimited {
		t.Fatalf("limited first, each from its own readings: %+v", out)
	}
	if a := only(t, out, "a"); window(t, a, "five_hour").Percent != 20 || a.Status == StatusLimited {
		t.Fatalf("a is not b: %+v", a)
	}
}

func TestLimitsReadsTheStore(t *testing.T) {
	setup(t)
	if err := store.AppendLimit(reading("a", "seven_day", time.Minute, 64, 72*time.Hour)); err != nil {
		t.Fatal(err)
	}
	out, err := Limits(now)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || window(t, out[0], "seven_day").Percent != 64 {
		t.Fatalf("from limits.jsonl: %+v", out)
	}
}
