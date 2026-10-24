package store

import (
	"strings"
	"time"
)

// repeatWindow is how soon a prompt must be sent again to count as the same
// turn. Prompts are stored cut short, so text alone can't tell a re-send
// from a new prompt that starts the same way.
const repeatWindow = time.Minute

// RepeatOf reports whether a prompt with this text, sent at the time, is the
// previous turn sent again.
func RepeatOf(prev Turn, text string, at time.Time) bool {
	if text == "" || prev.Text != text {
		return false
	}
	// /goal sends its prompt again each time Claude tries to stop.
	if strings.HasPrefix(text, "/goal ") {
		return true
	}
	sent := parseTS(prev.At)
	return !sent.IsZero() && at.Sub(sent) < repeatWindow
}

// FoldReport says what FoldTurns changed.
type FoldReport struct {
	Turns  int `json:"turns"`  // turns folded into the prompt they belong to
	Events int `json:"events"` // changes moved to another turn
}

// FoldTurns repairs prompts older versions split. A subagent's report or a
// task's notice that Claude Code delivered into the conversation became a
// turn of its own, as did the same prompt sent again right away, and a
// subagent's late changes landed in whichever turn was latest. Delivered
// and repeated turns fold into the prompt before them, and each subagent's
// changes move to the prompt it was started in.
func FoldTurns(dryRun bool) (FoldReport, error) {
	var rep FoldReport
	projects, err := ListProjects()
	if err != nil {
		return rep, err
	}
	for _, p := range projects {
		if err := p.foldTurns(dryRun, &rep); err != nil {
			return rep, err
		}
	}
	return rep, nil
}

// foldTurns repairs one project while holding its lock, so a prompt hook
// cannot add a turn between the plan and the write, and the plan is applied
// to the records it was made from.
func (p *Project) foldTurns(dryRun bool, rep *FoldReport) error {
	lock, err := p.Acquire()
	if err != nil {
		return err
	}
	defer lock.Release()
	recs, err := p.Sessions()
	if err != nil {
		return err
	}
	plans := map[string]foldPlan{}
	for _, r := range recs {
		if plan := planFold(r); plan.changes() {
			plans[r.Session] = plan
			rep.Turns += len(r.Turns) - len(plan.kept)
		}
	}
	if len(plans) == 0 {
		return nil
	}
	evs, err := p.Events(Query{})
	if err != nil {
		return err
	}
	for _, ev := range evs {
		if plan, ok := plans[ev.Session]; ok && plan.turnFor(ev) != ev.Turn {
			rep.Events++
		}
	}
	if dryRun {
		return nil
	}
	if err := p.editEvents(func(ev *Event) bool {
		if plan, ok := plans[ev.Session]; ok {
			ev.Turn = plan.turnFor(*ev)
		}
		return true
	}); err != nil {
		return err
	}
	for id, plan := range plans {
		rec, ok := p.LoadSession(id)
		if !ok {
			continue
		}
		plan.apply(&rec)
		if err := p.SaveSession(rec); err != nil {
			return err
		}
	}
	return nil
}

// foldPlan is how one session's turns are repaired.
type foldPlan struct {
	kept   []Turn
	remap  map[string]string // folded turn → the turn it folds into
	agents map[string]string // subagent → the turn it was started in
	moved  bool              // some subagent's turn changes
}

func planFold(r SessionRecord) foldPlan {
	plan := foldPlan{remap: map[string]string{}, agents: map[string]string{}}
	for _, t := range r.Turns {
		if n := len(plan.kept); n > 0 && (deliveredTurn(t.Text) || RepeatOf(plan.kept[n-1], t.Text, parseTS(t.At))) {
			plan.remap[t.ID] = plan.kept[n-1].ID
			continue
		}
		plan.kept = append(plan.kept, t)
	}
	kept := SessionRecord{Turns: plan.kept}
	for _, a := range r.Agents {
		turn := a.Turn
		if to, ok := plan.remap[turn]; ok {
			turn = to
		}
		if turn == "" {
			turn = kept.TurnAt(a.StartedAt)
		}
		if turn != "" {
			plan.agents[a.ID] = turn
			plan.moved = plan.moved || turn != a.Turn
		}
	}
	return plan
}

func (f foldPlan) changes() bool { return len(f.remap) > 0 || f.moved }

// turnFor is the turn an event belongs to after the repair.
func (f foldPlan) turnFor(ev Event) string {
	if t, ok := f.agents[ev.Agent]; ok && ev.Agent != "" {
		return t
	}
	if t, ok := f.remap[ev.Turn]; ok {
		return t
	}
	return ev.Turn
}

func (f foldPlan) apply(r *SessionRecord) {
	r.Turns = f.kept
	for i := range r.Agents {
		if t, ok := f.agents[r.Agents[i].ID]; ok {
			r.Agents[i].Turn = t
		}
	}
	if n := len(r.Turns); n > 0 {
		r.LastPrompt, r.LastPromptAt = r.Turns[n-1].Text, r.Turns[n-1].At
	}
}

// deliveredTurn reports whether a stored turn's text is a message Claude
// Code delivered itself; older versions stored those as prompts.
func deliveredTurn(text string) bool {
	return Delivered(text) || text == "(background task finished)"
}
