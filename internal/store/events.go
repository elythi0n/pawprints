package store

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
)

// Event kinds.
const (
	KindEdit     = "edit"     // written by Claude's edit tools
	KindExternal = "external" // changed between Claude's commands
	KindRestore  = "restore"  // pawprints restore
	KindUndo     = "undo"     // pawprints undo
	// KindBackground is a change made while a backgrounded command of the
	// session was running, credited to that command.
	KindBackground = "background"
)

// Event is one line of the event log.
type Event struct {
	V            int     `json:"v"`
	ID           string  `json:"id"`
	TS           string  `json:"ts"`
	Session      string  `json:"session"`
	Turn         string  `json:"turn,omitempty"`
	ToolUseID    string  `json:"tool_use_id,omitempty"`
	Tool         string  `json:"tool"`
	Kind         string  `json:"kind"`
	Path         string  `json:"path"`
	Before       *string `json:"before"`
	After        *string `json:"after"`
	SizeBefore   int64   `json:"size_before"`
	SizeAfter    int64   `json:"size_after"`
	Binary       bool    `json:"binary"`
	LinesAdded   int     `json:"lines_added"`
	LinesRemoved int     `json:"lines_removed"`
	Skipped      string  `json:"skipped,omitempty"`
	// BeforeUnknown marks a file first seen after a shell command changed it,
	// with no earlier copy in Pawprints or git to compare against.
	BeforeUnknown bool   `json:"before_unknown,omitempty"`
	Command       string `json:"command,omitempty"`
	// Agent and AgentType name the subagent that made the change; empty for
	// the main conversation.
	Agent     string `json:"agent_id,omitempty"`
	AgentType string `json:"agent_type,omitempty"`
	CWD       string `json:"cwd,omitempty"`
}

// Time parses the event timestamp.
func (e Event) Time() time.Time {
	t, _ := time.Parse(time.RFC3339Nano, e.TS)
	return t
}

// BeforeHash returns the before hash or "".
func (e Event) BeforeHash() string {
	if e.Before == nil {
		return ""
	}
	return *e.Before
}

// AfterHash returns the after hash or "".
func (e Event) AfterHash() string {
	if e.After == nil {
		return ""
	}
	return *e.After
}

// Unchanged reports a touch that left identical content.
func (e Event) Unchanged() bool {
	return e.Before != nil && e.After != nil && *e.Before == *e.After
}

// HashPtr turns "" into nil for the JSON null convention.
func HashPtr(h string) *string {
	if h == "" {
		return nil
	}
	return &h
}

// NewID returns a ULID for the given time.
func NewID(t time.Time) string {
	entropy := ulid.Monotonic(rand.New(rand.NewSource(t.UnixNano())), 0) //nolint:gosec // ids need ordering, not secrecy
	return ulid.MustNew(ulid.Timestamp(t), entropy).String()
}

// Now formats a time the way events store it.
func Now(t time.Time) string { return t.Format(time.RFC3339Nano) }

func (p *Project) eventsDir() string { return filepath.Join(p.Dir, "events") }

func monthFile(t time.Time) string { return t.Format("2006-01") + ".jsonl" }

// AppendEvent writes one event line. A single write under O_APPEND keeps
// lines from concurrent hooks intact.
func (p *Project) AppendEvent(ev Event) error {
	if ev.V == 0 {
		ev.V = EventVersion
	}
	t := ev.Time()
	if t.IsZero() {
		t = time.Now()
		ev.TS = Now(t)
	}
	if ev.ID == "" {
		ev.ID = NewID(t)
	}
	return p.appendToMonth(ev, monthFile(t))
}

// appendToMonth writes an event into a specific month file, used when
// pinned events are carried forward out of a month being removed.
func (p *Project) appendToMonth(ev Event, file string) error {
	line, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	lock, err := p.eventsLock(false)
	if err != nil {
		return err
	}
	defer lock.Release()
	f, err := os.OpenFile(filepath.Join(p.eventsDir(), file), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(line, '\n'))
	return err
}

// Months lists the event files present, oldest first, as "2006-01".
func (p *Project) Months() ([]string, error) {
	entries, err := os.ReadDir(p.eventsDir())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var months []string
	for _, e := range entries {
		if name := e.Name(); strings.HasSuffix(name, ".jsonl") {
			months = append(months, strings.TrimSuffix(name, ".jsonl"))
		}
	}
	sort.Strings(months)
	return months, nil
}

// Query filters events. Zero values mean no filter.
type Query struct {
	Session string
	Turn    string
	Path    string
	Since   time.Time
	Until   time.Time
	Kinds   []string
	Limit   int
}

func (q Query) matches(ev Event) bool {
	if q.Session != "" && ev.Session != q.Session {
		return false
	}
	if q.Turn != "" && ev.Turn != q.Turn {
		return false
	}
	if q.Path != "" && !strings.Contains(ev.Path, q.Path) {
		return false
	}
	if len(q.Kinds) > 0 {
		ok := false
		for _, k := range q.Kinds {
			if ev.Kind == k {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	t := ev.Time()
	if !q.Since.IsZero() && t.Before(q.Since) {
		return false
	}
	if !q.Until.IsZero() && t.After(q.Until) {
		return false
	}
	return true
}

// Events returns matching events, newest first. It reads month files from
// newest to oldest and stops as soon as the limit is satisfied, so recent
// queries never touch old months.
func (p *Project) Events(q Query) ([]Event, error) {
	months, err := p.Months()
	if err != nil {
		return nil, err
	}
	var out []Event
	for i := len(months) - 1; i >= 0; i-- {
		if !q.Since.IsZero() && monthEnd(months[i]).Before(q.Since) {
			break
		}
		if !q.Until.IsZero() && monthStart(months[i]).After(q.Until) {
			continue
		}
		evs, err := p.readMonth(months[i])
		if err != nil {
			return nil, err
		}
		for j := len(evs) - 1; j >= 0; j-- {
			if q.matches(evs[j]) {
				out = append(out, evs[j])
				if q.Limit > 0 && len(out) >= q.Limit {
					return out, nil
				}
			}
		}
	}
	return out, nil
}

// EventByPrefix resolves a full id or unique prefix.
func (p *Project) EventByPrefix(prefix string) (Event, error) {
	prefix = strings.ToUpper(prefix)
	all, err := p.Events(Query{})
	if err != nil {
		return Event{}, err
	}
	var found []Event
	for _, ev := range all {
		if ev.ID == prefix {
			return ev, nil
		}
		if strings.HasPrefix(ev.ID, prefix) {
			found = append(found, ev)
		}
	}
	switch len(found) {
	case 0:
		return Event{}, fmt.Errorf("no event matches %q", prefix)
	case 1:
		return found[0], nil
	default:
		return Event{}, fmt.Errorf("%q matches %d events; use a longer prefix", prefix, len(found))
	}
}

// PreviousVersion finds the event that set the file's content before ev.
func (p *Project) PreviousVersion(ev Event) (Event, bool) {
	all, err := p.Events(Query{Path: ev.Path, Until: ev.Time()})
	if err != nil {
		return Event{}, false
	}
	for _, cand := range all {
		if cand.Path == ev.Path && cand.ID < ev.ID {
			return cand, true
		}
	}
	return Event{}, false
}

func (p *Project) readMonth(month string) ([]Event, error) {
	f, err := os.Open(filepath.Join(p.eventsDir(), month+".jsonl"))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Event
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var ev Event
		if err := json.Unmarshal(line, &ev); err != nil {
			continue // one bad line must not hide a month of history
		}
		out = append(out, ev)
	}
	// Lines are appended in time order, except when regroup or prune carry
	// events in from elsewhere; ULIDs sort by time.
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, sc.Err()
}

func monthStart(m string) time.Time {
	t, _ := time.Parse("2006-01", m)
	return t
}

func monthEnd(m string) time.Time { return monthStart(m).AddDate(0, 1, 0) }

// ReferencedHashes collects every blob hash still referenced by events or pins.
func (p *Project) ReferencedHashes() ([]string, error) {
	seen := map[string]bool{}
	evs, err := p.Events(Query{})
	if err != nil {
		return nil, err
	}
	for _, ev := range evs {
		if h := ev.BeforeHash(); h != "" {
			seen[h] = true
		}
		if h := ev.AfterHash(); h != "" {
			seen[h] = true
		}
	}
	pins, _ := p.Pins()
	for _, pin := range pins {
		seen[pin.Hash] = true
	}
	out := make([]string, 0, len(seen))
	for h := range seen {
		out = append(out, h)
	}
	sort.Strings(out)
	return out, nil
}

// TimeID returns a ULID that depends only on the time, for ids that must
// come out the same on every read, such as prompts recovered from history.
func TimeID(t time.Time) string {
	var zero [10]byte
	id, err := ulid.New(ulid.Timestamp(t), bytes.NewReader(zero[:]))
	if err != nil {
		return NewID(t)
	}
	return id.String()
}

// Label says who or what made the change, for lists: the tool ("Edit",
// "Bash", "files:write_file" for an MCP tool), "background", or "outside
// Claude", with the subagent type when a subagent made it.
func (e Event) Label() string {
	var l string
	switch e.Kind {
	case KindExternal:
		return "outside Claude"
	case KindBackground:
		l = "background"
	case KindRestore, KindUndo:
		return e.Kind
	default:
		l = ToolName(e.Tool)
	}
	if e.AgentType != "" {
		l += " · " + e.AgentType
	}
	return l
}

// ToolName shortens an MCP tool's full name, mcp__<server>__<tool>, to
// "<server>:<tool>"; other names are returned as they are.
func ToolName(tool string) string {
	rest, ok := strings.CutPrefix(tool, "mcp__")
	if !ok {
		return tool
	}
	server, name, ok := strings.Cut(rest, "__")
	if !ok {
		return rest
	}
	return server + ":" + name
}
