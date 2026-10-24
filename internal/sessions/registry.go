// Package sessions answers "which Claude Code sessions exist, which are
// running, and what are they doing" by merging Claude Code's own session
// registry with the records Pawprints hooks keep.
package sessions

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/elythi0n/pawprints/internal/store"
)

// RegistryEntry is one ~/.claude/sessions/<pid>.json, parsed leniently. The
// format is internal to Claude Code; every field may be missing.
type RegistryEntry struct {
	PID             int    `json:"pid"`
	SessionID       string `json:"sessionId"`
	CWD             string `json:"cwd"`
	StartedAt       int64  `json:"startedAt"`
	ProcStart       string `json:"procStart"`
	Version         string `json:"version"`
	Kind            string `json:"kind"`
	Entrypoint      string `json:"entrypoint"`
	Name            string `json:"name"`
	NameSource      string `json:"nameSource"`
	Status          string `json:"status"`
	StatusUpdatedAt int64  `json:"statusUpdatedAt"`
	UpdatedAt       int64  `json:"updatedAt"`
	WaitingFor      string `json:"waitingFor"`

	Alive bool   `json:"-"`
	File  string `json:"-"`
}

// ClaudeHome is ~/.claude, overridable for tests with CLAUDE_CONFIG_DIR.
func ClaudeHome() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".claude"
	}
	return filepath.Join(home, ".claude")
}

// RegistryDir is where Claude Code writes per-process session files.
func RegistryDir() string { return filepath.Join(ClaudeHome(), "sessions") }

// ClaudeHomes is every Claude Code config folder to read: this process's,
// the ones in config.json, and every folder a hook has run under. Two
// accounts often run from two folders, so each has its own sessions and
// transcripts.
func ClaudeHomes() []string {
	var out []string
	seen := map[string]bool{}
	add := func(d string) {
		d = filepath.Clean(expandHome(d))
		if d == "." || seen[d] {
			return
		}
		seen[d] = true
		out = append(out, d)
	}
	add(ClaudeHome())
	cfg, _ := store.LoadConfig()
	for _, d := range cfg.ClaudeHomes {
		add(d)
	}
	for _, d := range store.SeenClaudeHomes() {
		add(d)
	}
	return out
}

// RegistryDirs is the registry folder of every config folder.
func RegistryDirs() []string {
	homes := ClaudeHomes()
	out := make([]string, 0, len(homes))
	for _, h := range homes {
		out = append(out, filepath.Join(h, "sessions"))
	}
	return out
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

// registryFiles lists every registry entry file across the config folders.
func registryFiles() []string {
	var out []string
	for _, dir := range RegistryDirs() {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".json") {
				out = append(out, filepath.Join(dir, e.Name()))
			}
		}
	}
	return out
}

// ReadRegistry parses every entry and marks the ones whose process is alive.
// A missing directory is not an error: the result is simply empty.
func ReadRegistry() ([]RegistryEntry, error) {
	if _, err := os.Stat(RegistryDir()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	var out []RegistryEntry
	for _, path := range registryFiles() {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var re RegistryEntry
		if json.Unmarshal(data, &re) != nil || re.SessionID == "" {
			continue
		}
		re.File = path
		re.Alive = processAlive(re.PID, re.ProcStart)
		out = append(out, re)
	}
	return out, nil
}

// processAlive checks the pid exists and, when the registry recorded the
// process start time, that it matches /proc so a reused pid does not count.
func processAlive(pid int, procStart string) bool {
	if pid <= 0 {
		return false
	}
	if err := syscall.Kill(pid, 0); err != nil && !errors.Is(err, syscall.EPERM) {
		return false
	}
	if procStart == "" {
		return true
	}
	start, ok := procStartTime(pid)
	if !ok {
		return true // cannot verify; trust the pid
	}
	return start == procStart
}

// procStartTime reads field 22 of /proc/<pid>/stat, the start time in clock
// ticks since boot, which is what Claude Code stores as procStart.
func procStartTime(pid int) (string, bool) {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return "", false
	}
	s := string(data)
	// The command name is in parentheses and may contain spaces; skip past it.
	end := strings.LastIndex(s, ")")
	if end < 0 {
		return "", false
	}
	fields := strings.Fields(s[end+1:])
	// fields[0] is state (field 3); start time is field 22, so index 19.
	if len(fields) < 20 {
		return "", false
	}
	return fields[19], true
}

// StatusTime returns when the status was last set.
func (r RegistryEntry) StatusTime() time.Time {
	ms := r.StatusUpdatedAt
	if ms == 0 {
		ms = r.UpdatedAt
	}
	if ms == 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}

// Started returns the process start time.
func (r RegistryEntry) Started() time.Time {
	if r.StartedAt == 0 {
		return time.Time{}
	}
	return time.UnixMilli(r.StartedAt)
}

// LaunchDir returns the directory a session was started in, from its
// registry entry. Liveness is not checked; the hook only needs the answer
// for a session that is calling it.
func LaunchDir(sessionID string) (string, bool) {
	if sessionID == "" {
		return "", false
	}
	for _, path := range registryFiles() {
		data, err := os.ReadFile(path)
		if err != nil || !strings.Contains(string(data), sessionID) {
			continue
		}
		var re RegistryEntry
		if json.Unmarshal(data, &re) == nil && re.SessionID == sessionID && re.CWD != "" {
			return re.CWD, true
		}
	}
	return "", false
}

// ProjectRoots maps every session id the registry knows to the project root
// of its launch directory, plus every pin the hooks recorded. Pins win.
func ProjectRoots() map[string]string {
	out := map[string]string{}
	for _, path := range registryFiles() {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var re RegistryEntry
		if json.Unmarshal(data, &re) == nil && re.SessionID != "" && re.CWD != "" {
			out[re.SessionID] = store.ResolveRoot(re.CWD)
		}
	}
	for id, root := range store.SessionRoots() {
		out[id] = root
	}
	return out
}
