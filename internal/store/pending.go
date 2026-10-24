package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Pending is written by the pre hook before a tool runs and removed by the
// post hook once the tool's changes are recorded. While it exists, the call
// counts as running: a scan by another session leaves the files it may be
// changing to it. A background command keeps its record until its session
// ends or BackgroundLife passes, since nothing reports when it finishes.
type Pending struct {
	TS         string `json:"ts"`
	Session    string `json:"session"`
	Turn       string `json:"turn,omitempty"`
	Tool       string `json:"tool"`
	Path       string `json:"path"`
	Before     string `json:"before"`
	SizeBefore int64  `json:"size_before"`
	Binary     bool   `json:"binary"`
	Skipped    string `json:"skipped,omitempty"`
	StartNano  int64  `json:"start_nano,omitempty"`
	Command    string `json:"command,omitempty"`
	Agent      string `json:"agent_id,omitempty"`
	AgentType  string `json:"agent_type,omitempty"`
	Background bool   `json:"background,omitempty"`
	CWD        string `json:"cwd"`
}

// BackgroundLife is how long a background command is assumed to run.
const BackgroundLife = 2 * time.Hour

// pendingLife is how long a call may run before its record is taken as left
// behind, such as by a Claude Code that was killed mid-call.
const pendingLife = time.Hour

// Start is when the pre hook ran.
func (pe Pending) Start() time.Time {
	if pe.StartNano != 0 {
		return time.Unix(0, pe.StartNano)
	}
	t, _ := time.Parse(time.RFC3339Nano, pe.TS)
	return t
}

// expired reports whether the record no longer stands for a running call.
func (pe Pending) expired(now time.Time) bool {
	if pe.Background {
		return now.Sub(pe.Start()) > BackgroundLife
	}
	return now.Sub(pe.Start()) > pendingLife
}

func (p *Project) pendingPath(toolUseID string) string {
	return filepath.Join(p.Dir, "pending", sanitize(toolUseID)+".json")
}

// WritePending records the pre-edit snapshot for a tool call.
func (p *Project) WritePending(toolUseID string, pe Pending) error {
	data, err := json.Marshal(pe)
	if err != nil {
		return err
	}
	return os.WriteFile(p.pendingPath(toolUseID), data, 0o644)
}

// ReadPending reads a pending record without removing it.
func (p *Project) ReadPending(toolUseID string) (Pending, bool) {
	data, err := os.ReadFile(p.pendingPath(toolUseID))
	if err != nil {
		return Pending{}, false
	}
	var pe Pending
	if json.Unmarshal(data, &pe) != nil {
		return Pending{}, false
	}
	return pe, true
}

// DonePending removes the record of a call whose changes are recorded. A
// background command's record stays, since the command may still be running.
func (p *Project) DonePending(toolUseID string) {
	if pe, ok := p.ReadPending(toolUseID); ok && pe.Background {
		return
	}
	_ = os.Remove(p.pendingPath(toolUseID))
}

// TakePending reads and removes the pending record.
func (p *Project) TakePending(toolUseID string) (Pending, bool) {
	pe, ok := p.ReadPending(toolUseID)
	if ok {
		_ = os.Remove(p.pendingPath(toolUseID))
	}
	return pe, ok
}

// Running lists the calls running in the project, keyed by tool use id.
// Records left behind are removed.
func (p *Project) Running(now time.Time) map[string]Pending {
	entries, err := os.ReadDir(filepath.Join(p.Dir, "pending"))
	if err != nil {
		return nil
	}
	out := map[string]Pending{}
	for _, e := range entries {
		id, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok || strings.HasPrefix(id, ".") {
			continue
		}
		pe, ok := p.ReadPending(id)
		if !ok {
			continue
		}
		if pe.expired(now) {
			_ = os.Remove(p.pendingPath(id))
			continue
		}
		out[id] = pe
	}
	return out
}

// EndBackground removes the records of a session's background commands
// when the session ends.
func (p *Project) EndBackground(session string) {
	for id, pe := range p.Running(time.Now()) {
		if pe.Background && pe.Session == session {
			_ = os.Remove(p.pendingPath(id))
		}
	}
}

// StalePending removes records that no longer stand for a running call and
// returns the count.
func (p *Project) StalePending() int {
	entries, err := os.ReadDir(filepath.Join(p.Dir, "pending"))
	if errors.Is(err, os.ErrNotExist) || err != nil {
		return 0
	}
	n := 0
	now := time.Now()
	for _, e := range entries {
		id, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok {
			continue
		}
		pe, ok := p.ReadPending(id)
		if ok && !pe.expired(now) {
			continue
		}
		if os.Remove(filepath.Join(p.Dir, "pending", e.Name())) == nil {
			n++
		}
	}
	return n
}

func sanitize(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_', c == '.':
			out = append(out, c)
		default:
			out = append(out, '_')
		}
	}
	return string(out)
}
