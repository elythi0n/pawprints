package sessions

import (
	"sort"
	"strings"
	"time"

	"github.com/elythi0n/pawprints/internal/store"
)

// Status values of a merged session.
const (
	StatusBusy    = "busy"
	StatusWaiting = "waiting"
	StatusIdle    = "idle"
	StatusEnded   = "ended"
	StatusUnknown = "unknown"
)

// TouchingWindow is how recent an edit must be for "touching now".
const TouchingWindow = 30 * time.Second

// Session is the merged view of one Claude Code session.
type Session struct {
	ID            string                 `json:"id"`
	Name          string                 `json:"name"`
	Project       string                 `json:"project"`
	ProjectID     string                 `json:"project_id"`
	CWD           string                 `json:"cwd"`
	PID           int                    `json:"pid,omitempty"`
	Alive         bool                   `json:"alive"`
	Status        string                 `json:"status"`
	StatusSince   string                 `json:"status_since,omitempty"`
	WaitingFor    string                 `json:"waiting_for,omitempty"`
	Touching      bool                   `json:"touching"`
	StartedAt     string                 `json:"started_at,omitempty"`
	StartSource   string                 `json:"start_source,omitempty"`
	EndedAt       string                 `json:"ended_at,omitempty"`
	EndReason     string                 `json:"end_reason,omitempty"`
	FirstPrompt   string                 `json:"first_prompt,omitempty"`
	LastPrompt    string                 `json:"last_prompt,omitempty"`
	LastPromptAt  string                 `json:"last_prompt_at,omitempty"`
	LastEventAt   string                 `json:"last_event_at,omitempty"`
	Edits         int                    `json:"edits"`
	Files         []string               `json:"files"`
	LinesAdded    int                    `json:"lines_added"`
	LinesRemoved  int                    `json:"lines_removed"`
	Turns         []store.Turn           `json:"turns,omitempty"`
	Agents        []store.Agent          `json:"agents,omitempty"`
	Usage         map[string]store.Usage `json:"usage,omitempty"`
	Transcript    string                 `json:"transcript_path,omitempty"`
	ClaudeVersion string                 `json:"claude_version,omitempty"`
	Sources       []string               `json:"sources"`
}

// LastActivity is the newest timestamp known for the session.
func (s Session) LastActivity() time.Time {
	latest := parse(s.StartedAt)
	for _, v := range []string{s.LastPromptAt, s.LastEventAt, s.EndedAt, s.StatusSince} {
		if t := parse(v); t.After(latest) {
			latest = t
		}
	}
	return latest
}

func parse(s string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, s)
	return t
}

// Options narrows a listing.
type Options struct {
	// Project limits results to one project. Nil with All=false means the
	// caller's project could not be resolved; nothing is returned.
	Project *store.Project
	All     bool
	Live    bool
	Since   time.Time
	Now     time.Time
}

// List builds merged sessions. Registry entries are the fast path; hook
// records and events fill in context; history titles sessions that have
// neither.
func List(opts Options) ([]Session, error) {
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	registry, err := ReadRegistry()
	if err != nil {
		return nil, err
	}
	projects, err := projectsFor(opts)
	if err != nil {
		return nil, err
	}
	byID := map[string]*Session{}
	var order []string

	rootCache := map[string]string{}
	pins := store.SessionRoots()
	rootOf := func(id, cwd string) string {
		if r, ok := pins[id]; ok {
			return r
		}
		if r, ok := rootCache[cwd]; ok {
			return r
		}
		r := store.ResolveRoot(cwd)
		rootCache[cwd] = r
		return r
	}

	for _, re := range registry {
		if !re.Alive {
			continue
		}
		root := rootOf(re.SessionID, re.CWD)
		if !opts.All && (opts.Project == nil || opts.Project.Root != root) {
			continue
		}
		s := &Session{
			ID:            re.SessionID,
			Name:          re.Name,
			Project:       root,
			ProjectID:     store.ProjectID(root),
			CWD:           re.CWD,
			PID:           re.PID,
			Alive:         true,
			Status:        normalizeStatus(re.Status),
			WaitingFor:    re.WaitingFor,
			ClaudeVersion: re.Version,
			Files:         []string{},
			Sources:       []string{"registry"},
		}
		if t := re.StatusTime(); !t.IsZero() {
			s.StatusSince = store.Now(t)
		}
		if t := re.Started(); !t.IsZero() {
			s.StartedAt = store.Now(t)
		}
		byID[s.ID] = s
		order = append(order, s.ID)
	}

	for _, p := range projects {
		recs, err := p.Sessions()
		if err != nil {
			continue
		}
		for _, rec := range recs {
			s, ok := byID[rec.Session]
			if !ok {
				s = &Session{
					ID:        rec.Session,
					Project:   p.Root,
					ProjectID: p.ID,
					CWD:       rec.CWD,
					Files:     []string{},
				}
				byID[s.ID] = s
				order = append(order, s.ID)
			}
			s.Sources = append(s.Sources, "hook")
			applyRecord(s, rec)
		}
	}

	wanted := map[string]bool{}
	for _, id := range order {
		if byID[id].Name == "" && byID[id].FirstPrompt == "" {
			wanted[id] = true
		}
	}
	if len(wanted) > 0 {
		for id, title := range HistoryTitles(wanted) {
			byID[id].FirstPrompt = title
			byID[id].Sources = append(byID[id].Sources, "history")
		}
	}

	var out []Session
	for _, id := range order {
		s := byID[id]
		fillTurnsFromHistory(s)
		finishStatus(s, now)
		if opts.Live && !s.Alive {
			continue
		}
		if !opts.Since.IsZero() && s.LastActivity().Before(opts.Since) {
			continue
		}
		if s.Name == "" {
			s.Name = displayName(*s)
		}
		out = append(out, *s)
	}
	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := rank(out[i]), rank(out[j])
		if ri != rj {
			return ri < rj
		}
		return out[i].LastActivity().After(out[j].LastActivity())
	})
	return out, nil
}

func projectsFor(opts Options) ([]*store.Project, error) {
	if opts.All {
		return store.ListProjects()
	}
	if opts.Project == nil {
		return nil, nil
	}
	return []*store.Project{opts.Project}, nil
}

func applyRecord(s *Session, rec store.SessionRecord) {
	if s.StartedAt == "" || (rec.StartedAt != "" && parse(rec.StartedAt).Before(parse(s.StartedAt))) {
		if rec.StartedAt != "" {
			s.StartedAt = rec.StartedAt
		}
	}
	s.StartSource = rec.StartSource
	s.EndedAt = rec.EndedAt
	s.EndReason = rec.EndReason
	s.FirstPrompt = store.ReadablePrompt(rec.FirstPrompt)
	s.LastPrompt = store.ReadablePrompt(rec.LastPrompt)
	s.LastPromptAt = rec.LastPromptAt
	s.LastEventAt = rec.LastEventAt
	s.Edits = rec.Edits
	s.Files = rec.Files
	s.LinesAdded = rec.LinesAdded
	s.LinesRemoved = rec.LinesRemoved
	s.Turns = make([]store.Turn, len(rec.Turns))
	for i, t := range rec.Turns {
		t.Text = store.ReadablePrompt(t.Text)
		s.Turns[i] = t
	}
	s.Usage = rec.Usage
	s.Agents = rec.Agents
	s.Transcript = rec.TranscriptPath
	if s.CWD == "" {
		s.CWD = rec.CWD
	}
	if !s.Alive {
		// Only hook data: derive status from what the hooks saw.
		switch {
		case rec.EndedAt != "":
			s.Status = StatusEnded
			s.StatusSince = rec.EndedAt
		case rec.LastPromptAt != "" && parse(rec.LastPromptAt).After(parse(rec.LastStopAt)):
			s.Status = StatusBusy
			s.StatusSince = rec.LastPromptAt
		case rec.LastStopAt != "":
			s.Status = StatusIdle
			s.StatusSince = rec.LastStopAt
		}
	}
}

func finishStatus(s *Session, now time.Time) {
	if t := parse(s.LastEventAt); !t.IsZero() && now.Sub(t) < TouchingWindow && s.Status != StatusEnded {
		s.Touching = true
	}
	if s.Status != "" {
		return
	}
	last := parse(s.LastEventAt)
	switch {
	case last.IsZero():
		s.Status = StatusUnknown
	case now.Sub(last) < 15*time.Second:
		s.Status = StatusBusy
	case now.Sub(last) < 24*time.Hour:
		s.Status = StatusIdle
	default:
		s.Status = StatusEnded
	}
}

// normalizeStatus maps Claude Code's registry status onto ours. Claude Code
// reports finer states while it works (such as "shell" during a command);
// anything that is not waiting or idle means it is busy.
func normalizeStatus(s string) string {
	switch strings.ToLower(s) {
	case "waiting", "blocked":
		return StatusWaiting
	case "idle", "done":
		return StatusIdle
	case "":
		return ""
	}
	return StatusBusy
}

func rank(s Session) int {
	switch {
	case s.Touching:
		return 0
	case s.Status == StatusBusy:
		return 1
	case s.Status == StatusWaiting:
		return 2
	case s.Status == StatusIdle && s.Alive:
		return 3
	case s.Status == StatusIdle:
		return 4
	case s.Status == StatusEnded:
		return 5
	}
	return 6
}

func displayName(s Session) string {
	if s.FirstPrompt != "" {
		if len(s.FirstPrompt) > 48 {
			return s.FirstPrompt[:47] + "…"
		}
		return s.FirstPrompt
	}
	if len(s.ID) > 8 {
		return s.ID[:8]
	}
	return s.ID
}

// Current resolves "--session current" for a project: the newest live
// session there, else the newest session that has not ended.
func Current(p *store.Project) (Session, bool) {
	all, err := List(Options{Project: p})
	if err != nil {
		return Session{}, false
	}
	for _, s := range all {
		if s.Alive {
			return s, true
		}
	}
	for _, s := range all {
		if s.Status != StatusEnded {
			return s, true
		}
	}
	if len(all) > 0 {
		return all[0], true
	}
	return Session{}, false
}

// Resolve finds a session by id or unique prefix, or "current".
func Resolve(p *store.Project, ref string) (Session, bool) {
	if ref == "current" {
		return Current(p)
	}
	all, err := List(Options{Project: p})
	if err != nil {
		return Session{}, false
	}
	var found []Session
	for _, s := range all {
		if s.ID == ref {
			return s, true
		}
		if strings.HasPrefix(s.ID, ref) || s.Name == ref {
			found = append(found, s)
		}
	}
	if len(found) == 1 {
		return found[0], true
	}
	return Session{}, false
}

// fillTurnsFromHistory adds prompts from Claude Code's history that came
// before the first one the hooks recorded, so edits made before Pawprints
// was installed still group under the prompt that caused them.
func fillTurnsFromHistory(s *Session) {
	var first time.Time
	if len(s.Turns) > 0 {
		first, _ = time.Parse(time.RFC3339Nano, s.Turns[0].At)
	}
	earlier := HistoryTurns(s.ID, first)
	if len(earlier) == 0 {
		return
	}
	s.Turns = append(earlier, s.Turns...)
	if s.FirstPrompt == "" || len(s.Turns) == len(earlier) {
		s.FirstPrompt = earlier[0].Text
	}
	if s.LastPrompt == "" {
		s.LastPrompt = earlier[len(earlier)-1].Text
		s.LastPromptAt = earlier[len(earlier)-1].At
	}
	s.Sources = append(s.Sources, "history")
}

// Running maps each live session to its registry entry. It reads only the
// registry, so it is cheap enough for views that refresh often.
func Running() map[string]RegistryEntry {
	out := map[string]RegistryEntry{}
	entries, err := ReadRegistry()
	if err != nil {
		return out
	}
	for _, re := range entries {
		if re.Alive {
			out[re.SessionID] = re
		}
	}
	return out
}

// LiveStatus is a running session's status: busy, waiting, or idle.
func LiveStatus(re RegistryEntry) string {
	if s := normalizeStatus(re.Status); s != "" {
		return s
	}
	return StatusIdle
}

// RecordName is how views name a session from its record and, when it is
// running, its registry entry.
func RecordName(rec store.SessionRecord, re *RegistryEntry) string {
	s := Session{ID: rec.Session, FirstPrompt: rec.FirstPrompt}
	if re != nil && re.Name != "" {
		return re.Name
	}
	return displayName(s)
}
