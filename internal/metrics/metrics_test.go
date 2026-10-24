package metrics

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elythi0n/pawprints/internal/store"
)

// now is a fixed moment; every fixture is placed relative to it.
var now = time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)

func setup(t *testing.T) {
	t.Helper()
	t.Setenv("PAWPRINTS_HOME", t.TempDir())
	// No running Claude sessions leak in from the machine.
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
}

func project(t *testing.T, name string) *store.Project {
	t.Helper()
	root := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	p, err := store.OpenProject(root)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func minute(ago time.Duration) string {
	return now.Add(-ago).Truncate(time.Minute).Format(time.RFC3339)
}

func session(t *testing.T, p *store.Project, id, account string, turns ...store.Turn) {
	t.Helper()
	rec := store.SessionRecord{Session: id, StartedAt: store.Now(now.Add(-3 * time.Hour)), Account: account, Turns: turns, Usage: map[string]store.Usage{"x": {Output: 1}}}
	if err := p.SaveSession(rec); err != nil {
		t.Fatal(err)
	}
}

func usageAt(t *testing.T, p *store.Project, id string, buckets ...store.UsageBucket) {
	t.Helper()
	if err := p.AppendUsage(id, buckets); err != nil {
		t.Fatal(err)
	}
}

func edit(t *testing.T, p *store.Project, sess, path string, ago time.Duration, added, removed int) {
	t.Helper()
	ev := store.Event{ID: store.NewID(now.Add(-ago)), TS: store.Now(now.Add(-ago)), Session: sess, Tool: "Edit", Kind: store.KindEdit, Path: path, LinesAdded: added, LinesRemoved: removed}
	if err := p.AppendEvent(ev); err != nil {
		t.Fatal(err)
	}
}

func build(t *testing.T, opts Options) Report {
	t.Helper()
	opts.Now = now
	if opts.Since.IsZero() {
		opts.Since = now.Add(-6 * time.Hour)
	}
	rep, err := Build(opts)
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

func TestTotalsTimelineAndCost(t *testing.T) {
	setup(t)
	p := project(t, "app")
	session(t, p, "s1", "acct-a", store.Turn{ID: "T1", At: store.Now(now.Add(-2 * time.Hour)), Text: "make the tests pass"})
	usageAt(t, p, "s1",
		store.UsageBucket{Minute: minute(90 * time.Minute), Model: "claude-opus-5-5", Usage: store.Usage{Input: 1000, Output: 2000, CacheRead: 50000, CacheWrite: 3000}},
		store.UsageBucket{Minute: minute(30 * time.Minute), Model: "claude-opus-5-5", Usage: store.Usage{Output: 500}},
		// Older than the range: left out.
		store.UsageBucket{Minute: minute(10 * time.Hour), Model: "claude-opus-5-5", Usage: store.Usage{Output: 99999}},
	)
	edit(t, p, "s1", "main.go", 80*time.Minute, 10, 2)
	edit(t, p, "s1", "main.go", 20*time.Minute, 3, 1)
	rep := build(t, Options{Projects: []*store.Project{p}})

	if got := rep.Totals.Tokens.Counted; got != 6500 {
		t.Fatalf("counted %d", got)
	}
	if rep.Totals.Tokens.CacheRead != 50000 {
		t.Fatalf("cache reads %d", rep.Totals.Tokens.CacheRead)
	}
	// 1000*4 + 2500*20 + 50000*0.2 + 3000*5 per million.
	if want := (4000 + 50000 + 10000 + 15000) / 1e6; abs(rep.Totals.APICost-want) > 1e-9 {
		t.Fatalf("api cost %v, want %v", rep.Totals.APICost, want)
	}
	if rep.Totals.Edits != 2 || rep.Totals.LinesAdded != 13 || rep.Totals.Files != 1 {
		t.Fatalf("edits %+v", rep.Totals)
	}
	if rep.Range.Bucket != "5m" || len(rep.Timeline) != 72 {
		t.Fatalf("6 hours in 5 minute buckets: %s, %d", rep.Range.Bucket, len(rep.Timeline))
	}
	var counted int64
	for _, pt := range rep.Timeline {
		counted += pt.Counted
	}
	if counted != 6500 {
		t.Fatalf("timeline sums to %d", counted)
	}
	if len(rep.Sessions) != 1 || rep.Sessions[0].Edits != 2 || rep.Sessions[0].Model != "claude-opus-5-5" {
		t.Fatalf("sessions %+v", rep.Sessions)
	}
	if len(rep.TopFiles) != 1 || rep.TopFiles[0].Edits != 2 || rep.TopFiles[0].Sessions != 1 {
		t.Fatalf("top files %+v", rep.TopFiles)
	}
	if rep.MissingHistory != 0 {
		t.Fatalf("a session with a usage log is not missing history: %d", rep.MissingHistory)
	}
}

func TestAccountFilterAndLimits(t *testing.T) {
	setup(t)
	p := project(t, "app")
	session(t, p, "work", "acct-a")
	session(t, p, "home", "acct-b")
	usageAt(t, p, "work", store.UsageBucket{Minute: minute(time.Hour), Model: "m", Account: "acct-a", Usage: store.Usage{Output: 100}})
	usageAt(t, p, "home", store.UsageBucket{Minute: minute(time.Hour), Model: "m", Account: "acct-b", Usage: store.Usage{Output: 40}})
	for _, r := range []store.LimitReading{
		{At: store.Now(now.Add(-2 * time.Hour)), Account: "acct-a", Kind: "five_hour", Percent: 20},
		{At: store.Now(now.Add(-time.Hour)), Account: "acct-a", Kind: "five_hour", Percent: 35},
		{At: store.Now(now.Add(-time.Hour)), Account: "acct-b", Kind: "five_hour", Percent: 70},
	} {
		if err := store.AppendLimit(r); err != nil {
			t.Fatal(err)
		}
	}
	all := build(t, Options{Projects: []*store.Project{p}})
	if all.Totals.Tokens.Counted != 140 || len(all.Accounts) != 2 {
		t.Fatalf("all accounts add up: %d, %+v", all.Totals.Tokens.Counted, all.Accounts)
	}
	if len(all.Limits) != 2 {
		t.Fatalf("one limit line per account, never summed: %+v", all.Limits)
	}
	one := build(t, Options{Projects: []*store.Project{p}, Account: "acct-a"})
	if one.Totals.Tokens.Counted != 100 || len(one.Sessions) != 1 || one.Sessions[0].ID != "work" {
		t.Fatalf("filtered: %d %+v", one.Totals.Tokens.Counted, one.Sessions)
	}
	if len(one.Limits) != 1 || one.Limits[0].Latest != 35 || len(one.Limits[0].Points) != 2 {
		t.Fatalf("filtered limits %+v", one.Limits)
	}
}

// An older version could leave one session's record and usage in two
// projects. Its tokens must count once, in the project it is pinned to.
func TestSessionInTwoProjectsCountsOnce(t *testing.T) {
	setup(t)
	a := project(t, "a")
	b := project(t, "b")
	for _, p := range []*store.Project{a, b} {
		session(t, p, "dup", "")
		usageAt(t, p, "dup", store.UsageBucket{Minute: minute(time.Hour), Model: "m", Usage: store.Usage{Output: 1000}})
	}
	edit(t, b, "dup", "b.go", time.Hour, 1, 0)
	if err := store.PinSessionRoot("dup", b.Root, true); err != nil {
		t.Fatal(err)
	}
	rep := build(t, Options{Projects: []*store.Project{a, b}})
	if rep.Totals.Tokens.Counted != 1000 {
		t.Fatalf("counted once: %d", rep.Totals.Tokens.Counted)
	}
	if len(rep.Sessions) != 1 || rep.Sessions[0].ProjectID != b.ID {
		t.Fatalf("one row, under the pinned project: %+v", rep.Sessions)
	}
}

func TestSpikesNameThePrompt(t *testing.T) {
	setup(t)
	p := project(t, "app")
	session(t, p, "s", "", store.Turn{ID: "T1", At: store.Now(now.Add(-5 * time.Hour)), Text: "warm up"}, store.Turn{ID: "T2", At: store.Now(now.Add(-62 * time.Minute)), Text: "rewrite the parser"})
	var buckets []store.UsageBucket
	for m := 5 * time.Hour; m > 2*time.Hour; m -= 10 * time.Minute {
		buckets = append(buckets, store.UsageBucket{Minute: minute(m), Model: "m", Usage: store.Usage{Output: 6000}})
	}
	buckets = append(buckets, store.UsageBucket{Minute: minute(time.Hour), Model: "m", Usage: store.Usage{Output: 90000}})
	usageAt(t, p, "s", buckets...)
	rep := build(t, Options{Projects: []*store.Project{p}})
	if len(rep.Spikes) != 1 {
		t.Fatalf("one spike: %+v", rep.Spikes)
	}
	sp := rep.Spikes[0]
	if sp.Turn != "T2" || sp.Prompt != "rewrite the parser" || sp.TimesNormal < 3 {
		t.Fatalf("spike %+v", sp)
	}
}

func TestEmptyStoreGivesAnEmptyReport(t *testing.T) {
	setup(t)
	rep := build(t, Options{})
	if rep.Totals.Tokens.Counted != 0 || rep.Sessions == nil || rep.Projects == nil || rep.Spikes == nil || rep.Limits == nil {
		t.Fatalf("lists are empty, never null: %+v", rep)
	}
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

// Day buckets stay on local midnight across a daylight saving change.
func TestDayBucketsAcrossDST(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Helsinki")
	if err != nil {
		t.Skip("no time zone data")
	}
	saved := time.Local
	time.Local = loc
	defer func() { time.Local = saved }()
	// Summer time in Finland ends on October 25, 2026.
	until := time.Date(2026, 11, 5, 12, 0, 0, 0, loc)
	g := newGrid(until.AddDate(0, 0, -30), until, 24*time.Hour)
	for _, s := range g.starts {
		if l := s.In(loc); l.Hour() != 0 || l.Minute() != 0 {
			t.Fatalf("bucket starts at %v, not midnight", l)
		}
	}
	late := time.Date(2026, 11, 1, 23, 30, 0, 0, loc)
	if i := g.index(late); g.starts[i].In(loc).Day() != 1 {
		t.Fatalf("23:30 on Nov 1 lands in the bucket of %v", g.starts[i].In(loc))
	}
}
