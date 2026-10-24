package store

import (
	"testing"
	"time"
)

func TestFoldTurns(t *testing.T) {
	t.Setenv("PAWPRINTS_HOME", t.TempDir())
	p, _ := OpenProject(t.TempDir())
	base := time.Now().Add(-time.Hour)
	at := func(m int) string { return Now(base.Add(time.Duration(m) * time.Minute)) }
	turns := []Turn{
		{ID: "T1", At: at(0), Text: "/goal scan the manager"},
		{ID: "T2", At: at(1), Text: "/goal scan the manager"},
		{ID: "T3", At: at(20), Text: `<agent-message from="A1"> [Subagent hand-back] The text below…`},
		{ID: "T4", At: at(21), Text: "(background task finished)"},
		{ID: "T5", At: at(30), Text: "explain X"},
	}
	p.UpdateSession("S", func(r *SessionRecord) {
		r.Turns = turns
		r.LastPrompt = "explain X"
		r.Agents = []Agent{{ID: "A1", Type: "general-purpose", StartedAt: at(5)}, {ID: "A2", Type: "Explore", StartedAt: at(6), Turn: "T2"}}
	})
	add := func(m int, turn, agent string) {
		ts := base.Add(time.Duration(m) * time.Minute)
		p.AppendEvent(Event{ID: NewID(ts), TS: Now(ts), Session: "S", Kind: KindEdit, Tool: "Write", Path: "f", Turn: turn, Agent: agent})
	}
	add(10, "T2", "A1") // already under the goal prompt, but by its repeat
	add(25, "T4", "A1") // landed in a delivered turn
	add(31, "T5", "A2") // landed in the person's next prompt
	add(22, "T3", "")   // the main conversation reacting to a report
	add(32, "T5", "")   // the person's next prompt: stays

	plan, err := FoldTurns(true)
	if err != nil || plan.Turns != 3 || plan.Events != 4 {
		t.Fatalf("plan %+v %v", plan, err)
	}
	if _, err := FoldTurns(false); err != nil {
		t.Fatal(err)
	}
	rec, _ := p.LoadSession("S")
	if len(rec.Turns) != 2 || rec.Turns[0].ID != "T1" || rec.Turns[1].ID != "T5" {
		t.Fatalf("turns %+v", rec.Turns)
	}
	if rec.Agents[0].Turn != "T1" || rec.Agents[1].Turn != "T1" {
		t.Fatalf("agents %+v", rec.Agents)
	}
	evs, _ := p.Events(Query{})
	var got []string
	for i := len(evs) - 1; i >= 0; i-- {
		got = append(got, evs[i].Turn)
	}
	want := []string{"T1", "T1", "T1", "T1", "T5"} // by time: 10, 22, 25, 31, 32
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("turns of events %v, want %v", got, want)
		}
	}
	if again, _ := FoldTurns(true); again.Turns != 0 || again.Events != 0 {
		t.Fatalf("nothing left: %+v", again)
	}
}

func TestRepeatOf(t *testing.T) {
	base := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	prev := func(text string) Turn { return Turn{ID: "T", At: Now(base), Text: text} }
	cases := []struct {
		prev  string
		text  string
		after time.Duration
		want  bool
	}{
		{"yes", "yes", 10 * time.Second, true},
		{"yes", "yes", 5 * time.Minute, false},
		{"/goal finish the release", "/goal finish the release", 40 * time.Minute, true},
		{"please rewrite the transcript reader so it streams", "please rewrite the transcript reader so it streams", time.Hour, false},
		{"please rewrite the transcript reader so it streams", "please rewrite the transcript reader so it streams", 20 * time.Second, true},
		{"yes", "no", time.Second, false},
		{"", "", time.Second, false},
	}
	for _, c := range cases {
		if got := RepeatOf(prev(c.prev), c.text, base.Add(c.after)); got != c.want {
			t.Errorf("RepeatOf(%q, %q, +%v) = %v, want %v", c.prev, c.text, c.after, got, c.want)
		}
	}
}
