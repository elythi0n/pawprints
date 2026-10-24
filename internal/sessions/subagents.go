package sessions

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/elythi0n/pawprints/internal/store"
)

// Claude Code keeps each subagent's transcript, and a small file saying what
// it was asked to do, next to the session's transcript:
//
//	<session transcript without .jsonl>/subagents/agent-<id>.jsonl
//	<session transcript without .jsonl>/subagents/agent-<id>.meta.json
//
// Both are Claude Code's own formats, so everything read here is optional:
// when they are missing or change, views fall back to the agent's type.

// MaxReport caps the report kept on the session record.
const MaxReport = 4000

// SubagentTranscript is the path of a subagent's transcript, from the
// session's transcript, or "".
func SubagentTranscript(sessionTranscript, agentID string) string {
	if sessionTranscript == "" || agentID == "" {
		return ""
	}
	if strings.HasPrefix(filepath.Base(sessionTranscript), "agent-") {
		return sessionTranscript
	}
	return filepath.Join(strings.TrimSuffix(sessionTranscript, ".jsonl"), "subagents", "agent-"+agentID+".jsonl")
}

// SubagentDescription is the short task the subagent was launched with.
func SubagentDescription(transcript string) string {
	if transcript == "" {
		return ""
	}
	data, err := os.ReadFile(strings.TrimSuffix(transcript, ".jsonl") + ".meta.json")
	if err != nil {
		return ""
	}
	var meta struct {
		Description string `json:"description"`
	}
	if json.Unmarshal(data, &meta) != nil {
		return ""
	}
	return strings.Join(strings.Fields(meta.Description), " ")
}

// SubagentReport is what the subagent handed back: the message of its last
// hand-back call, else the text of its last answer.
func SubagentReport(transcript string) string {
	lines := tailLines(transcript, 1<<20)
	for i := len(lines) - 1; i >= 0; i-- {
		var entry struct {
			Type    string `json:"type"`
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(lines[i], &entry) != nil || entry.Type != "assistant" {
			continue
		}
		var blocks []struct {
			Type  string `json:"type"`
			Name  string `json:"name"`
			Text  string `json:"text"`
			Input struct {
				Message string `json:"message"`
			} `json:"input"`
		}
		if json.Unmarshal(entry.Message.Content, &blocks) != nil {
			continue
		}
		for j := len(blocks) - 1; j >= 0; j-- {
			b := blocks[j]
			switch {
			case b.Type == "tool_use" && b.Name == "SubagentHandback" && b.Input.Message != "":
				return Shorten(b.Input.Message, MaxReport)
			case b.Type == "text" && strings.TrimSpace(b.Text) != "":
				return Shorten(b.Text, MaxReport)
			}
		}
	}
	return ""
}

// tailLines reads up to limit bytes from the end of a file, as lines.
func tailLines(path string, limit int64) [][]byte {
	if path == "" {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	if info, err := f.Stat(); err == nil && info.Size() > limit {
		if _, err := f.Seek(-limit, io.SeekEnd); err != nil {
			return nil
		}
	}
	var lines [][]byte
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), int(limit))
	for sc.Scan() {
		lines = append(lines, bytes.Clone(sc.Bytes()))
	}
	return lines
}

// Shorten cuts s to at most n bytes, on a character boundary.
func Shorten(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	cut := n - 1
	for cut > 0 && !isRuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

func isRuneStart(b byte) bool { return b&0xC0 != 0x80 }

// FillAgents adds what Claude Code's subagent files say to subagents
// recorded without it: the task each was given and the report it handed
// back. It returns how many subagents it filled.
func FillAgents(dryRun bool) (int, error) {
	// Tasks and reports carry the person's words, like prompts.
	if cfg, _ := store.LoadConfig(); !cfg.PromptsEnabled() {
		return 0, nil
	}
	projects, err := store.ListProjects()
	if err != nil {
		return 0, err
	}
	filled := 0
	for _, p := range projects {
		recs, err := p.Sessions()
		if err != nil {
			return filled, err
		}
		for _, r := range recs {
			found := map[string]store.Agent{}
			for _, a := range r.Agents {
				if a.Description != "" && (a.Report != "" || a.StoppedAt == "") && (a.Report == "" || a.Summary == ReportSummary(a.Report)) {
					continue
				}
				transcript := SubagentTranscript(r.TranscriptPath, a.ID)
				next := a
				if next.Description == "" {
					next.Description = SubagentDescription(transcript)
				}
				if next.Report == "" && next.StoppedAt != "" {
					next.Report = SubagentReport(transcript)
				}
				if next.Report != "" {
					next.Summary = ReportSummary(next.Report)
				}
				if next != a {
					found[a.ID] = next
				}
			}
			filled += len(found)
			if dryRun || len(found) == 0 {
				continue
			}
			if err := p.UpdateSession(r.Session, func(r *store.SessionRecord) {
				for i, a := range r.Agents {
					if next, ok := found[a.ID]; ok {
						r.Agents[i].Description, r.Agents[i].Report, r.Agents[i].Summary = next.Description, next.Report, next.Summary
					}
				}
			}); err != nil {
				return filled, err
			}
		}
	}
	return filled, nil
}

// markdownMarks are the markdown signs a one-line summary leaves out.
var markdownMarks = strings.NewReplacer("**", "", "__", "", "`", "")

// ReportSummary is the start of a report as one plain line: headings,
// emphasis and code marks removed.
func ReportSummary(report string) string {
	var words []string
	for _, line := range strings.Split(report, "\n") {
		line = strings.TrimLeft(strings.TrimSpace(line), "#> ")
		words = append(words, strings.Fields(markdownMarks.Replace(line))...)
	}
	return Shorten(strings.Join(words, " "), 200)
}
