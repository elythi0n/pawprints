// Package panes builds the trees each TUI pane shows from a view.Source.
// Rendering and keys live in the tui package; this is only structure.
package panes

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/elythi0n/pawprints/internal/sessions"
	"github.com/elythi0n/pawprints/internal/store"
	"github.com/elythi0n/pawprints/internal/view"
)

// Builder turns a source into the roots of a tree.
type Builder func(*view.Source) []*view.Node

// Version makes a leaf node for one event.
func Version(s *view.Source, ev store.Event) *view.Node {
	e := ev
	n := &view.Node{Kind: view.KindVersion, Key: "ev:" + ev.ID, Event: &e, Path: ev.Path}
	n.Label = When(ev.Time(), s.Now) + "  " + toolName(ev)
	n.Desc = statsText(ev)
	if s.Pinned(ev.ID) {
		n.Desc += " ⚲"
	}
	return n
}

func toolName(ev store.Event) string { return ev.Label() }

func statsText(ev store.Event) string {
	switch {
	case ev.Skipped != "":
		return "skipped " + ev.Skipped
	case ev.Binary:
		return "binary"
	case ev.Unchanged():
		return "unchanged"
	}
	var parts []string
	switch {
	case ev.BeforeUnknown:
		parts = append(parts, "first seen")
	case ev.Before == nil:
		parts = append(parts, "new")
	}
	if ev.After == nil {
		parts = append(parts, "deleted")
	}
	if ev.LinesAdded > 0 || ev.LinesRemoved > 0 {
		parts = append(parts, fmt.Sprintf("+%d -%d", ev.LinesAdded, ev.LinesRemoved))
	}
	return strings.Join(parts, " ")
}

// Live lists running sessions. Each opens on its current prompt with every
// file that prompt has touched so far; earlier prompts fold underneath.
func Live(s *view.Source) []*view.Node {
	var roots []*view.Node
	for _, se := range s.SessionsSorted() {
		if !se.Alive {
			continue
		}
		roots = append(roots, liveSession(s, se))
	}
	if len(roots) == 0 {
		msg := "no Claude Code session is running here"
		if !s.AllProjects {
			msg += "  (A shows every project)"
		}
		roots = append(roots, &view.Node{Kind: view.KindNotice, Key: "none", Label: msg})
	}
	return roots
}

// turnOf is the prompt an edit belongs to. Edits recorded without one, as
// older versions did when Claude changed directory, go to the prompt that
// was running at the time.
func turnOf(se sessions.Session, ev store.Event) string {
	if ev.Turn != "" {
		return ev.Turn
	}
	at := store.Now(ev.Time())
	turn := ""
	for _, t := range se.Turns {
		if t.At <= at {
			turn = t.ID
		}
	}
	return turn
}

func byTurnOf(se sessions.Session, evs []store.Event) map[string][]store.Event {
	out := map[string][]store.Event{}
	for _, ev := range evs {
		id := turnOf(se, ev)
		out[id] = append(out[id], ev)
	}
	return out
}

func liveSession(s *view.Source, se sessions.Session) *view.Node {
	sc := se
	n := &view.Node{Kind: view.KindSession, Key: "s:" + se.ID, Session: &sc, Label: se.Name, Expanded: true}
	n.Desc = sessionDesc(s, se)
	evs := s.EventsOf(se.ID)
	byTurn := byTurnOf(se, evs)
	current := ""
	if len(se.Turns) > 0 {
		current = se.Turns[len(se.Turns)-1].ID
	} else if len(evs) > 0 {
		current = turnOf(se, evs[0])
	}
	now := turnNode(s, se, current, byTurn[current])
	now.Key = "live:" + se.ID + ":now"
	now.Expanded = true
	if len(byTurn[current]) == 0 {
		now.Desc = "no files touched yet in this prompt"
	} else {
		now.Desc = fmt.Sprintf("%d files · %d edits so far", len(now.Children), len(byTurn[current]))
	}
	n.Children = append(n.Children, now)

	var earlier []string
	seen := map[string]bool{current: true}
	for i := len(se.Turns) - 1; i >= 0; i-- {
		if id := se.Turns[i].ID; !seen[id] && len(byTurn[id]) > 0 {
			seen[id] = true
			earlier = append(earlier, id)
		}
	}
	for _, ev := range evs {
		if id := turnOf(se, ev); !seen[id] {
			seen[id] = true
			earlier = append(earlier, id)
		}
	}
	if len(earlier) > 0 {
		group := &view.Node{Kind: view.KindGroup, Key: "live:" + se.ID + ":earlier", Label: "earlier prompts", Desc: fmt.Sprintf("%d", len(earlier))}
		for _, id := range earlier {
			group.Children = append(group.Children, turnNode(s, se, id, byTurn[id]))
		}
		n.Children = append(n.Children, group)
	}
	return n
}

func sessionDesc(s *view.Source, se sessions.Session) string {
	status := se.Status
	if se.Touching {
		status = "touching"
	}
	if se.WaitingFor != "" {
		status += ": " + se.WaitingFor
	}
	desc := fmt.Sprintf("%s · %d files · %d edits · %s", status, len(se.Files), se.Edits, Ago(se.LastActivity(), s.Now))
	if s.AllProjects {
		desc = shortPath(se.Project) + " · " + desc
	}
	return desc
}

// Recent groups the newest events under day headings.
func Recent(s *view.Source) []*view.Node {
	var roots []*view.Node
	var day *view.Node
	for _, ev := range s.Events {
		d := ev.Time().Local().Format("Mon 2 Jan")
		if day == nil || day.Label != d {
			day = &view.Node{Kind: view.KindGroup, Key: "day:" + d, Label: d, Expanded: len(roots) == 0}
			roots = append(roots, day)
		}
		c := Version(s, ev)
		c.Label = When(ev.Time(), s.Now) + "  " + ev.Path
		c.Desc = toolName(ev) + "  " + statsText(ev)
		day.Children = append(day.Children, c)
		day.Desc = fmt.Sprintf("%d edits", len(day.Children))
	}
	if len(roots) == 0 {
		roots = append(roots, &view.Node{Kind: view.KindNotice, Key: "none", Label: "no edits recorded yet"})
	}
	return roots
}

// Sessions nests sessions → turns → files → versions.
func Sessions(s *view.Source) []*view.Node {
	var roots []*view.Node
	for _, se := range s.SessionsSorted() {
		sc := se
		n := &view.Node{Kind: view.KindSession, Key: "s:" + se.ID, Session: &sc, Label: se.Name, Desc: sessionDesc(s, se)}
		evs := s.EventsOf(se.ID)
		byTurn := byTurnOf(se, evs)
		var turnOrder []string
		for id := range byTurn {
			turnOrder = append(turnOrder, id)
		}
		// Turns with no edits still show, so the conversation reads whole.
		for i := len(se.Turns) - 1; i >= 0; i-- {
			if _, ok := byTurn[se.Turns[i].ID]; !ok {
				turnOrder = append(turnOrder, se.Turns[i].ID)
				byTurn[se.Turns[i].ID] = nil
			}
		}
		sort.SliceStable(turnOrder, func(i, j int) bool { return turnOrder[i] > turnOrder[j] })
		for _, tid := range turnOrder {
			n.Children = append(n.Children, turnNode(s, se, tid, byTurn[tid]))
		}
		roots = append(roots, n)
	}
	if len(roots) == 0 {
		roots = append(roots, &view.Node{Kind: view.KindNotice, Key: "none", Label: "no sessions recorded yet"})
	}
	return roots
}

func turnNode(s *view.Source, se sessions.Session, turnID string, evs []store.Event) *view.Node {
	label := "(before first prompt)"
	var turn *store.Turn
	for i := range se.Turns {
		if se.Turns[i].ID == turnID {
			t := se.Turns[i]
			turn = &t
			label = t.Text
			if label == "" {
				label = "(prompt not recorded)"
			}
			break
		}
	}
	if turnID != "" && turn == nil {
		label = "turn " + short(turnID)
	}
	n := &view.Node{Kind: view.KindTurn, Key: "t:" + se.ID + ":" + turnID, Turn: turn, Session: &se, Label: label}
	prefix := "t:" + se.ID + ":" + turnID + ":"
	n.Children = byWho(s, se, evs, prefix)
	n.Desc = fmt.Sprintf("%d edits · %d files", len(evs), countFiles(evs))
	if turn != nil {
		if at, err := time.Parse(time.RFC3339Nano, turn.At); err == nil {
			n.Desc += " · " + Ago(at, s.Now)
		}
	}
	return n
}

// byWho lays out a prompt's files by who changed them: the main
// conversation's files first, then one group per subagent, then a group for
// what changed outside Claude between its commands. Each part follows the
// list or tree layout.
func byWho(s *view.Source, se sessions.Session, evs []store.Event, prefix string) []*view.Node {
	var main, outside []store.Event
	agents := map[string][]store.Event{}
	var order []string
	for _, ev := range evs {
		switch {
		case ev.Kind == store.KindExternal:
			outside = append(outside, ev)
		case ev.Agent != "":
			if _, ok := agents[ev.Agent]; !ok {
				order = append(order, ev.Agent)
			}
			agents[ev.Agent] = append(agents[ev.Agent], ev)
		default:
			main = append(main, ev)
		}
	}
	out := layout(s, main, prefix)
	for _, id := range order {
		list := agents[id]
		agent := store.Agent{ID: id, Type: list[0].AgentType}
		for _, a := range se.Agents {
			if a.ID == id {
				agent = a
			}
		}
		parts := []string{}
		if agent.Description != "" && agent.Type != "" {
			parts = append(parts, agent.Type)
		}
		parts = append(parts, files(countFiles(list)))
		if agent.Summary != "" {
			parts = append(parts, agent.Summary)
		}
		out = append(out, &view.Node{
			Kind: view.KindGroup, Key: prefix + "agent:" + id,
			Label: "◇ " + agent.Name(), Desc: strings.Join(parts, " · "),
			Agent:    &agent,
			Children: layout(s, list, prefix+"a:"+id+":"),
		})
	}
	if len(outside) > 0 {
		out = append(out, &view.Node{
			Kind: view.KindGroup, Key: prefix + "outside",
			Label: "outside Claude", Desc: files(countFiles(outside)) + " · changed between Claude's commands",
			Children: layout(s, outside, prefix+"x:"),
		})
	}
	return out
}

// layout is the list or tree of rows for a set of events, per the setting.
func layout(s *view.Source, evs []store.Event, prefix string) []*view.Node {
	if s.Cfg.TreeLayout() {
		return FolderTree(s, evs, prefix)
	}
	return groupByFile(s, evs, prefix+"f:", s.Cfg.CombineVersions())
}

// files says "1 file" or "N files".
func files(n int) string {
	if n == 1 {
		return "1 file"
	}
	return fmt.Sprintf("%d files", n)
}

func countFiles(evs []store.Event) int {
	seen := map[string]bool{}
	for _, ev := range evs {
		seen[ev.Path] = true
	}
	return len(seen)
}

// groupByFile turns events into one row per path, most recently touched
// first. A path with a single version is one row that opens straight to its
// diff. A path with more versions either expands into them or, with combine
// set, is one row for the whole change: from before the first of them to
// after the last. prefix keeps keys unique when the same path appears under
// several prompts.
func groupByFile(s *view.Source, evs []store.Event, prefix string, combine bool) []*view.Node {
	byPath := map[string][]store.Event{}
	var order []string
	for _, ev := range evs {
		if _, ok := byPath[ev.Path]; !ok {
			order = append(order, ev.Path)
		}
		byPath[ev.Path] = append(byPath[ev.Path], ev)
	}
	out := make([]*view.Node, 0, len(order))
	for _, path := range order {
		list := byPath[path]
		if len(list) == 1 {
			leaf := Version(s, list[0])
			leaf.Label = path
			leaf.Desc = strings.TrimSpace(When(list[0].Time(), s.Now)) + " · " + toolName(list[0]) + " · " + leaf.Desc
			out = append(out, leaf)
			continue
		}
		if combine {
			out = append(out, combined(s, list, prefix))
			continue
		}
		f := &view.Node{Kind: view.KindFile, Key: prefix + path, Path: path, Label: path}
		for _, ev := range list {
			f.Children = append(f.Children, Version(s, ev))
		}
		f.Desc = fmt.Sprintf("%d versions · %s", len(list), strings.TrimSpace(When(list[0].Time(), s.Now)))
		out = append(out, f)
	}
	return out
}

// combined folds several versions of one file (newest first) into one row
// whose event spans them: the newest event with the oldest one's "before".
// Restoring it restores the newest version; undoing it returns the file to
// before the first edit.
func combined(s *view.Source, list []store.Event, prefix string) *view.Node {
	newest, oldest := list[0], list[len(list)-1]
	ev := newest
	ev.Before = oldest.Before
	ev.SizeBefore = oldest.SizeBefore
	ev.BeforeUnknown = oldest.BeforeUnknown
	ev.LinesAdded, ev.LinesRemoved = 0, 0
	for _, e := range list {
		ev.LinesAdded += e.LinesAdded
		ev.LinesRemoved += e.LinesRemoved
	}
	n := &view.Node{Kind: view.KindVersion, Key: prefix + "c:" + newest.Path, Event: &ev, Path: newest.Path, Label: newest.Path, Edits: len(list)}
	desc := strings.TrimSpace(When(newest.Time(), s.Now)) + fmt.Sprintf(" · %d edits", len(list))
	if ev.Unchanged() {
		desc += " · back to how it was"
	}
	if s.Pinned(newest.ID) {
		desc += " ⚲"
	}
	n.Desc = desc
	return n
}

// Files lists every touched path with its versions, most recently touched first.
func Files(s *view.Source) []*view.Node {
	// Files is a file's history, so it always lists every version.
	roots := groupByFile(s, s.Events, "file:", false)
	if len(roots) == 0 {
		roots = append(roots, &view.Node{Kind: view.KindNotice, Key: "none", Label: "no files touched yet"})
	}
	return roots
}

// Pins lists pinned versions.
func Pins(s *view.Source) []*view.Node {
	var roots []*view.Node
	for _, pin := range s.Pins {
		p := pin
		n := &view.Node{Kind: view.KindPin, Key: "pin:" + pin.Event, Pin: &p, Path: pin.Path, Label: pin.Path}
		n.Desc = pin.Message
		if at, err := time.Parse(time.RFC3339Nano, pin.At); err == nil {
			n.Desc = strings.TrimSpace(n.Desc + " · " + Ago(at, s.Now))
		}
		for i := range s.Events {
			if s.Events[i].ID == pin.Event {
				n.Event = &s.Events[i]
				break
			}
		}
		if n.Event == nil && s.Project != nil {
			if ev, err := s.Project.EventByPrefix(pin.Event); err == nil {
				n.Event = &ev
			}
		}
		roots = append(roots, n)
	}
	if len(roots) == 0 {
		roots = append(roots, &view.Node{Kind: view.KindNotice, Key: "none", Label: "no pins · press p on a version to keep it forever"})
	}
	return roots
}

// whenWidth keeps time labels one width so the paths after them line up.
const whenWidth = 11

// When labels an edit time for a tree row: relative while it is recent,
// the weekday and time within a week, then the date.
func When(t, now time.Time) string {
	t = t.Local()
	d := now.Sub(t)
	var s string
	switch {
	case d < 0:
		s = t.Format("15:04")
	case d < time.Minute:
		s = "just now"
	case d < time.Hour:
		s = fmt.Sprintf("%d min ago", int(d.Minutes()))
	case d < 24*time.Hour:
		s = fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 7*24*time.Hour:
		s = t.Format("Mon 15:04")
	case t.Year() == now.Year():
		s = t.Format("2 Jan")
	default:
		s = t.Format("2 Jan 2006")
	}
	if len(s) < whenWidth {
		s += strings.Repeat(" ", whenWidth-len(s))
	}
	return s
}

// Ago formats a time relative to now.
func Ago(t, now time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
	return t.Format("2006-01-02")
}

func short(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}

func shortPath(p string) string {
	parts := strings.Split(strings.TrimSuffix(p, "/"), "/")
	if len(parts) > 2 {
		return strings.Join(parts[len(parts)-2:], "/")
	}
	return p
}
