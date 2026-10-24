package sessions

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/elythi0n/pawprints/internal/store"
)

// historyTailBytes bounds how much of ~/.claude/history.jsonl is read when
// titling sessions that have no hook record. The file grows for months.
const historyTailBytes = 1 << 20

type historyLine struct {
	Display   string `json:"display"`
	Timestamp int64  `json:"timestamp"`
	Project   string `json:"project"`
	SessionID string `json:"sessionId"`
}

// HistoryTitles returns the first prompt seen in the tail of history.jsonl
// for each of the wanted session ids. Sessions whose prompts are older than
// the tail are simply absent.
func HistoryTitles(wanted map[string]bool) map[string]string {
	out := map[string]string{}
	if len(wanted) == 0 {
		return out
	}
	f, err := os.Open(filepath.Join(ClaudeHome(), "history.jsonl"))
	if err != nil {
		return out
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return out
	}
	start := info.Size() - historyTailBytes
	if start < 0 {
		start = 0
	}
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return out
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return out
	}
	lines := bytes.Split(data, []byte("\n"))
	if start > 0 && len(lines) > 0 {
		lines = lines[1:] // the first line is cut mid-way
	}
	// Walk forward so the earliest prompt in the tail wins for each session.
	for _, line := range lines {
		if len(line) == 0 {
			continue
		}
		var hl historyLine
		if json.Unmarshal(line, &hl) != nil || hl.SessionID == "" || !wanted[hl.SessionID] {
			continue
		}
		if _, done := out[hl.SessionID]; done {
			continue
		}
		text := strings.Join(strings.Fields(hl.Display), " ")
		if strings.HasPrefix(text, "/") && len(text) < 40 {
			continue // a slash command is not a title
		}
		if text != "" {
			out[hl.SessionID] = text
		}
	}
	return out
}

// historyCache keeps the parsed tail of history.jsonl between calls; the TUI
// lists sessions every few seconds and the file rarely changes.
var historyCache struct {
	mu    sync.Mutex
	size  int64
	mtime time.Time
	lines []historyLine
}

func historyTail() []historyLine {
	path := filepath.Join(ClaudeHome(), "history.jsonl")
	info, err := os.Stat(path)
	if err != nil {
		return nil
	}
	historyCache.mu.Lock()
	defer historyCache.mu.Unlock()
	if info.Size() == historyCache.size && info.ModTime().Equal(historyCache.mtime) {
		return historyCache.lines
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	start := info.Size() - historyTailBytes
	if start < 0 {
		start = 0
	}
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return nil
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return nil
	}
	raw := bytes.Split(data, []byte("\n"))
	if start > 0 && len(raw) > 0 {
		raw = raw[1:]
	}
	var lines []historyLine
	for _, line := range raw {
		var hl historyLine
		if len(line) > 0 && json.Unmarshal(line, &hl) == nil && hl.SessionID != "" {
			lines = append(lines, hl)
		}
	}
	historyCache.size, historyCache.mtime, historyCache.lines = info.Size(), info.ModTime(), lines
	return lines
}

// HistoryTurns returns the prompts Claude Code's own history holds for a
// session from before the hooks recorded their first one. They fill the gap
// when Pawprints was installed partway through a session.
func HistoryTurns(sessionID string, before time.Time) []store.Turn {
	var out []store.Turn
	for _, hl := range historyTail() {
		if hl.SessionID != sessionID || hl.Timestamp == 0 {
			continue
		}
		at := time.UnixMilli(hl.Timestamp)
		if !before.IsZero() && !at.Before(before) {
			continue
		}
		text := strings.Join(strings.Fields(store.ReadablePrompt(hl.Display)), " ")
		// A bare slash command such as /usage or /model runs locally and
		// never reaches the model, so it cannot be the cause of an edit.
		if strings.HasPrefix(text, "/") && !strings.Contains(text, " ") {
			continue
		}
		if len(text) > 200 {
			text = text[:199] + "…"
		}
		out = append(out, store.Turn{ID: store.TimeID(at), At: store.Now(at), Text: text})
	}
	return out
}
