package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Turn is one user prompt within a session.
type Turn struct {
	ID   string `json:"id"`
	At   string `json:"at"`
	Text string `json:"text"`
	// Account is the Claude account the prompt ran under, when known.
	Account string `json:"account,omitempty"`
}

// Usage holds token counts for one model.
type Usage struct {
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	CacheRead  int64 `json:"cache_read"`
	CacheWrite int64 `json:"cache_write"`
	Thinking   int64 `json:"thinking"`
	Messages   int64 `json:"messages"`
	// CacheWrite1h is the part of CacheWrite kept for an hour, which the API
	// prices higher than the five-minute kind.
	CacheWrite1h int64 `json:"cache_write_1h,omitempty"`
}

// Counted is the tokens that count toward usage limits: input, cache
// writes, and output. Cache reads are left out.
func (u Usage) Counted() int64 { return u.Input + u.CacheWrite + u.Output }

// Add sums another usage into u.
func (u *Usage) Add(o Usage) {
	u.Input += o.Input
	u.Output += o.Output
	u.CacheRead += o.CacheRead
	u.CacheWrite += o.CacheWrite
	u.Thinking += o.Thinking
	u.Messages += o.Messages
	u.CacheWrite1h += o.CacheWrite1h
}

// TranscriptRead is how far a transcript has been read for usage.
type TranscriptRead struct {
	Offset int64 `json:"offset"`
	// LastID is the last reply counted. Claude Code writes one reply as
	// several lines that repeat its usage, so the id keeps it counted once.
	LastID string `json:"last_id,omitempty"`
}

// Agent is one subagent a session started.
type Agent struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	StartedAt string `json:"started_at,omitempty"`
	StoppedAt string `json:"stopped_at,omitempty"`
	// Turn is the prompt the subagent was started in. Its changes belong
	// there, however late they land.
	Turn string `json:"turn,omitempty"`
	// Description is the short task the subagent was given.
	Description string `json:"description,omitempty"`
	// Summary is the start of the subagent's final report; Report is the
	// report itself, shortened when long.
	Summary string `json:"summary,omitempty"`
	Report  string `json:"report,omitempty"`
}

// Name is how views show the subagent: its task, else its type.
func (a Agent) Name() string {
	if a.Description != "" {
		return a.Description
	}
	if a.Type != "" {
		return a.Type + " agent"
	}
	return "subagent"
}

// SessionRecord is sessions/<id>.json, maintained by the hooks.
type SessionRecord struct {
	V                int              `json:"v"`
	Session          string           `json:"session"`
	StartedAt        string           `json:"started_at,omitempty"`
	StartSource      string           `json:"start_source,omitempty"`
	CWD              string           `json:"cwd,omitempty"`
	TranscriptPath   string           `json:"transcript_path,omitempty"`
	FirstPrompt      string           `json:"first_prompt,omitempty"`
	LastPrompt       string           `json:"last_prompt,omitempty"`
	LastPromptAt     string           `json:"last_prompt_at,omitempty"`
	LastStopAt       string           `json:"last_stop_at,omitempty"`
	EndedAt          string           `json:"ended_at,omitempty"`
	EndReason        string           `json:"end_reason,omitempty"`
	Turns            []Turn           `json:"turns,omitempty"`
	Edits            int              `json:"edits"`
	Files            []string         `json:"files"`
	LinesAdded       int              `json:"lines_added"`
	LinesRemoved     int              `json:"lines_removed"`
	LastEventAt      string           `json:"last_event_at,omitempty"`
	Usage            map[string]Usage `json:"usage,omitempty"`
	TranscriptOffset int64            `json:"transcript_offset,omitempty"`
	// LastMessageID is the last reply counted from the session's transcript.
	LastMessageID string `json:"last_message_id,omitempty"`
	// SubagentReads is how far each subagent's own transcript has been read,
	// by agent id. Their usage is not in the session's transcript.
	SubagentReads map[string]TranscriptRead `json:"subagent_reads,omitempty"`
	// Account is the Claude account the session last ran under.
	Account string  `json:"account,omitempty"`
	Agents  []Agent `json:"agents,omitempty"`
	// ExtraRoots are folders outside the project the session worked in, such
	// as git worktrees; command scans walk them too.
	ExtraRoots []string `json:"extra_roots,omitempty"`
}

// AgentByID finds a subagent of the session.
func (r *SessionRecord) AgentByID(id string) (*Agent, bool) {
	for i := range r.Agents {
		if r.Agents[i].ID == id {
			return &r.Agents[i], true
		}
	}
	return nil, false
}

// AddExtraRoot records a folder outside the project once.
func (r *SessionRecord) AddExtraRoot(dir string) bool {
	for _, d := range r.ExtraRoots {
		if d == dir {
			return false
		}
	}
	r.ExtraRoots = append(r.ExtraRoots, dir)
	return true
}

// TurnFor is the prompt a tool call belongs to: for a subagent's call the
// prompt the subagent was started in, else the latest one.
func (r *SessionRecord) TurnFor(agentID string) string {
	if agentID == "" {
		return r.CurrentTurn()
	}
	a, ok := r.AgentByID(agentID)
	if !ok {
		return r.CurrentTurn()
	}
	if a.Turn != "" {
		return a.Turn
	}
	if t := r.TurnAt(a.StartedAt); t != "" {
		return t
	}
	return r.CurrentTurn()
}

// TurnAt is the latest turn submitted at or before the time, or "".
func (r *SessionRecord) TurnAt(at string) string {
	when := parseTS(at)
	if when.IsZero() {
		return ""
	}
	id := ""
	for _, t := range r.Turns {
		if parseTS(t.At).After(when) {
			break
		}
		id = t.ID
	}
	return id
}

// AccountAt is the account a moment of the session ran under: the account of
// the turn running then, else the session's own.
func (r *SessionRecord) AccountAt(at time.Time) string {
	acct := r.Account
	for _, t := range r.Turns {
		if !at.IsZero() && parseTS(t.At).After(at) {
			break
		}
		if t.Account != "" {
			acct = t.Account
		}
	}
	return acct
}

// CurrentTurn returns the id of the latest turn, or "".
func (r *SessionRecord) CurrentTurn() string {
	if len(r.Turns) == 0 {
		return ""
	}
	return r.Turns[len(r.Turns)-1].ID
}

// TurnByPrefix resolves a turn id or prefix within the session.
func (r *SessionRecord) TurnByPrefix(prefix string) (Turn, bool) {
	prefix = strings.ToUpper(prefix)
	for i := len(r.Turns) - 1; i >= 0; i-- {
		if strings.HasPrefix(r.Turns[i].ID, prefix) {
			return r.Turns[i], true
		}
	}
	return Turn{}, false
}

// AddFile records a touched path once.
func (r *SessionRecord) AddFile(path string) {
	for _, f := range r.Files {
		if f == path {
			return
		}
	}
	r.Files = append(r.Files, path)
}

// Time helpers tolerate empty strings.
func parseTS(s string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, s)
	return t
}

// Started returns the start time or zero.
func (r *SessionRecord) Started() time.Time { return parseTS(r.StartedAt) }

// LastActivity is the newest of the known timestamps.
func (r *SessionRecord) LastActivity() time.Time {
	latest := parseTS(r.StartedAt)
	for _, s := range []string{r.LastPromptAt, r.LastStopAt, r.LastEventAt, r.EndedAt} {
		if t := parseTS(s); t.After(latest) {
			latest = t
		}
	}
	return latest
}

func (p *Project) sessionPath(id string) string {
	return filepath.Join(p.Dir, "sessions", sanitize(id)+".json")
}

// LoadSession reads a record. A missing record returns an empty one with ok=false.
func (p *Project) LoadSession(id string) (SessionRecord, bool) {
	data, err := os.ReadFile(p.sessionPath(id))
	if err != nil {
		return SessionRecord{V: 1, Session: id, Files: []string{}}, false
	}
	var rec SessionRecord
	if json.Unmarshal(data, &rec) != nil {
		return SessionRecord{V: 1, Session: id, Files: []string{}}, false
	}
	if rec.Files == nil {
		rec.Files = []string{}
	}
	return rec, true
}

// SaveSession writes the record atomically.
func (p *Project) SaveSession(rec SessionRecord) error {
	if rec.V == 0 {
		rec.V = 1
	}
	if rec.Files == nil {
		rec.Files = []string{}
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(p.sessionPath(rec.Session), append(data, '\n'))
}

// UpdateSession applies fn under the project lock.
func (p *Project) UpdateSession(id string, fn func(*SessionRecord)) error {
	lock, err := p.Acquire()
	if err != nil {
		return err
	}
	defer lock.Release()
	rec, _ := p.LoadSession(id)
	fn(&rec)
	return p.SaveSession(rec)
}

// Sessions returns every record, most recently active first.
func (p *Project) Sessions() ([]SessionRecord, error) {
	entries, err := os.ReadDir(filepath.Join(p.Dir, "sessions"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []SessionRecord
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(p.Dir, "sessions", e.Name()))
		if err != nil {
			continue
		}
		var rec SessionRecord
		if json.Unmarshal(data, &rec) != nil {
			continue
		}
		if rec.Files == nil {
			rec.Files = []string{}
		}
		out = append(out, rec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastActivity().After(out[j].LastActivity()) })
	return out, nil
}

// DeleteSession removes a record and its usage log.
func (p *Project) DeleteSession(id string) error {
	_ = p.ResetUsage(id)
	err := os.Remove(p.sessionPath(id))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, path)
}
