package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/elythi0n/pawprints/internal/store"
)

func TestMetricsJSON(t *testing.T) {
	p, root := seed(t)
	minute := time.Now().Add(-30 * time.Second).UTC().Truncate(time.Minute).Format(time.RFC3339)
	if err := p.AppendUsage("sess", []store.UsageBucket{{Minute: minute, Model: "claude-opus-5-5", Usage: store.Usage{Input: 10, Output: 1e6}}}); err != nil {
		t.Fatal(err)
	}
	out, code := run(t, "metrics", "--json", "--project="+root, "--since", "1h")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	m := lines(t, out)[0]
	totals := m["totals"].(map[string]any)
	tokens := totals["tokens"].(map[string]any)
	if tokens["counted"].(float64) != 1e6+10 || totals["edits"].(float64) != 1 || totals["api_cost"].(float64) < 20 {
		t.Fatalf("totals %v", totals)
	}
	for _, key := range []string{"range", "timeline", "spikes", "projects", "sessions", "models", "tools", "top_files", "accounts", "limits", "missing_history"} {
		if _, ok := m[key]; !ok {
			t.Fatalf("metrics has no %q", key)
		}
	}
	if m["range"].(map[string]any)["bucket"] != "1m" {
		t.Fatalf("an hour in minutes: %v", m["range"])
	}

	// The prompt's own tokens come from the same per-minute log.
	out, _ = run(t, "turns", "--json", "--project="+root, "--session", "sess")
	if tu := lines(t, out)[0]; tu["tokens"].(float64) != 1e6+10 {
		t.Fatalf("turn tokens %v", tu)
	}
}

func TestLimitsRecord(t *testing.T) {
	_, root := seed(t)
	out, code := run(t, "limits", "record", "--json", "--project="+root, "--session", "sess", "--kind", "five_hour", "--percent", "42", "--resets-at", "2026-10-23T20:00:00Z")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	r := lines(t, out)[0]
	if r["kind"] != "five_hour" || r["percent"].(float64) != 42 {
		t.Fatalf("reading %v", r)
	}
	got, err := store.Limits(time.Now().Add(-time.Hour))
	if err != nil || len(got) != 1 || got[0].Percent != 42 {
		t.Fatalf("stored %v %v", got, err)
	}
	if _, code := run(t, "limits", "record", "--kind", "five_hour"); code != 2 {
		t.Fatal("a reading needs a percent")
	}
}

func TestDiffFrom(t *testing.T) {
	p, root := seed(t)
	first, _ := p.Events(store.Query{})
	h3, _ := p.PutBytes([]byte("one\ntwo\nthree\n"))
	at := time.Now()
	ev := store.Event{ID: store.NewID(at), TS: store.Now(at), Session: "sess", Turn: "TURN1", Tool: "Edit", Kind: store.KindEdit, Path: "f.txt", Before: first[0].After, After: store.HashPtr(h3), LinesAdded: 1}
	if err := p.AppendEvent(ev); err != nil {
		t.Fatal(err)
	}
	out, code := run(t, "diff", ev.ID, "--from", first[0].ID, "--json", "--project="+root)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	d := lines(t, out)[0]
	stats := d["stats"].(map[string]any)
	if stats["added"].(float64) != 2 || !strings.Contains(d["diff"].(string), "+three") {
		t.Fatalf("both edits as one change: %v", d)
	}
}

func TestDiffFromRefusesAnotherFile(t *testing.T) {
	p, root := seed(t)
	evs, _ := p.Events(store.Query{})
	at := time.Now()
	h, _ := p.PutBytes([]byte("x\n"))
	other := store.Event{ID: store.NewID(at), TS: store.Now(at), Session: "sess", Turn: "TURN1", Tool: "Write", Kind: store.KindEdit, Path: "g.txt", After: store.HashPtr(h)}
	if err := p.AppendEvent(other); err != nil {
		t.Fatal(err)
	}
	if _, code := run(t, "diff", other.ID, "--from", evs[0].ID, "--json", "--project="+root); code != 2 {
		t.Fatalf("a --from on another file is refused, got exit %d", code)
	}
	if _, code := run(t, "diff", evs[0].ID, "--from", other.ID, "--json", "--project="+root); code != 2 {
		t.Fatalf("a newer --from is refused, got exit %d", code)
	}
}

func TestTurnTokensFollowTheSubagentsPrompt(t *testing.T) {
	p, root := seed(t)
	now := time.Now()
	p.UpdateSession("sess", func(r *store.SessionRecord) {
		r.Turns = append(r.Turns, store.Turn{ID: "TURN2", At: store.Now(now.Add(-10 * time.Second)), Text: "next"})
		r.Agents = []store.Agent{{ID: "bg", Type: "Explore", Turn: "TURN1"}}
	})
	minute := now.UTC().Truncate(time.Minute).Format(time.RFC3339)
	if err := p.AppendUsage("sess", []store.UsageBucket{
		{Minute: minute, Model: "m", Agent: "bg", Usage: store.Usage{Output: 700}},
		{Minute: minute, Model: "m", Usage: store.Usage{Output: 50}},
	}); err != nil {
		t.Fatal(err)
	}
	out, _ := run(t, "turns", "--json", "--project="+root, "--session", "sess")
	got := map[string]float64{}
	for _, tu := range lines(t, out) {
		got[tu["id"].(string)] = tu["tokens"].(float64)
	}
	if got["TURN1"] != 700 || got["TURN2"] != 50 {
		t.Fatalf("a background subagent's tokens stay with the prompt that started it: %v", got)
	}
}

func TestMetricsAllWithSession(t *testing.T) {
	p, root := seed(t)
	_ = root
	minute := time.Now().Add(-30 * time.Second).UTC().Truncate(time.Minute).Format(time.RFC3339)
	p.AppendUsage("sess", []store.UsageBucket{{Minute: minute, Model: "m", Usage: store.Usage{Output: 7}}})
	p.UpdateSession("other", func(r *store.SessionRecord) { r.Turns = []store.Turn{{ID: "X", At: store.Now(time.Now())}} })
	p.AppendUsage("other", []store.UsageBucket{{Minute: minute, Model: "m", Usage: store.Usage{Output: 1000}}})
	out, code := run(t, "metrics", "--json", "--all", "--session", "ses", "--since", "1h")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	tok := lines(t, out)[0]["totals"].(map[string]any)["tokens"].(map[string]any)
	if tok["counted"].(float64) != 7 {
		t.Fatalf("--all --session narrows to the session: %v", tok)
	}
	if _, code := run(t, "metrics", "--json", "--all", "--session", "nope"); code != 1 {
		t.Fatal("an unknown session is an error")
	}
}
