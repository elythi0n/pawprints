// Package usage reads token counts out of Claude Code transcripts. It only
// ever reads from the byte offset recorded on the session, so repeated calls
// cost as much as the new lines since last time.
package usage

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/elythi0n/pawprints/internal/store"
)

type transcriptLine struct {
	Type      string `json:"type"`
	Timestamp string `json:"timestamp"`
	Message   struct {
		ID    string `json:"id"`
		Model string `json:"model"`
		Usage *struct {
			Input         int64 `json:"input_tokens"`
			CacheWrite    int64 `json:"cache_creation_input_tokens"`
			CacheRead     int64 `json:"cache_read_input_tokens"`
			Output        int64 `json:"output_tokens"`
			CacheCreation *struct {
				OneHour int64 `json:"ephemeral_1h_input_tokens"`
			} `json:"cache_creation"`
			Details *struct {
				Thinking int64 `json:"thinking_tokens"`
			} `json:"output_tokens_details"`
		} `json:"usage"`
	} `json:"message"`
}

// reply is one counted model reply.
type reply struct {
	at    time.Time
	model string
	usage store.Usage
}

// readFrom reads complete lines from offset and calls fn once per reply.
// Claude Code writes a reply with several content blocks as several lines,
// each repeating the reply's usage; lastID carries the previous reply's id
// across calls so it is never counted twice. reset is true when the file is
// shorter than offset, which means it was rewritten and read from the start.
func readFrom(path string, offset int64, lastID string, fn func(reply)) (newOffset int64, newLastID string, reset bool) {
	f, err := os.Open(path)
	if err != nil {
		return offset, lastID, false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return offset, lastID, false
	}
	if offset > info.Size() {
		offset, lastID, reset = 0, "", true
	}
	if offset == info.Size() {
		return offset, lastID, reset
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return offset, lastID, reset
	}
	r := bufio.NewReaderSize(f, 1<<20)
	consumed := offset
	for {
		line, err := r.ReadBytes('\n')
		if err != nil {
			// A partial last line belongs to the next call.
			break
		}
		consumed += int64(len(line))
		line = bytes.TrimSpace(line)
		if len(line) == 0 || !bytes.Contains(line, []byte(`"usage"`)) {
			continue
		}
		var tl transcriptLine
		if json.Unmarshal(line, &tl) != nil || tl.Type != "assistant" || tl.Message.Usage == nil {
			continue
		}
		if tl.Message.ID != "" {
			if tl.Message.ID == lastID {
				continue
			}
			lastID = tl.Message.ID
		}
		model := tl.Message.Model
		if model == "" {
			model = "unknown"
		}
		src := tl.Message.Usage
		u := store.Usage{Input: src.Input, Output: src.Output, CacheRead: src.CacheRead, CacheWrite: src.CacheWrite, Messages: 1}
		if src.CacheCreation != nil {
			u.CacheWrite1h = src.CacheCreation.OneHour
		}
		if src.Details != nil {
			u.Thinking = src.Details.Thinking
		}
		at, _ := time.Parse(time.RFC3339Nano, tl.Timestamp)
		fn(reply{at: at, model: model, usage: u})
	}
	return consumed, lastID, reset
}

// SubagentTranscripts maps each subagent id to its own transcript, found
// next to the session's: <transcript without .jsonl>/subagents/agent-<id>.jsonl.
func SubagentTranscripts(sessionTranscript string) map[string]string {
	out := map[string]string{}
	if sessionTranscript == "" {
		return out
	}
	dir := filepath.Join(strings.TrimSuffix(sessionTranscript, ".jsonl"), "subagents")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "agent-") || !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		out[strings.TrimSuffix(strings.TrimPrefix(name, "agent-"), ".jsonl")] = filepath.Join(dir, name)
	}
	return out
}

// AccountAt names the Claude account a reply at the given time is credited
// to: the account of the prompt it answers.
type AccountAt func(time.Time) string

// buckets groups replies by minute, model, agent, and account.
type buckets struct {
	account AccountAt
	byKey   map[string]*store.UsageBucket
}

func newBuckets(account AccountAt) *buckets {
	if account == nil {
		account = func(time.Time) string { return "" }
	}
	return &buckets{account: account, byKey: map[string]*store.UsageBucket{}}
}

func (b *buckets) add(agent string, r reply) {
	if r.at.IsZero() {
		r.at = time.Now()
	}
	minute := r.at.UTC().Truncate(time.Minute).Format(time.RFC3339)
	acct := b.account(r.at)
	key := minute + "\x00" + r.model + "\x00" + agent + "\x00" + acct
	cur, ok := b.byKey[key]
	if !ok {
		cur = &store.UsageBucket{Minute: minute, Model: r.model, Account: acct, Agent: agent}
		b.byKey[key] = cur
	}
	cur.Add(r.usage)
}

func (b *buckets) list() []store.UsageBucket {
	out := make([]store.UsageBucket, 0, len(b.byKey))
	for _, v := range b.byKey {
		out = append(out, *v)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Minute != out[j].Minute {
			return out[i].Minute < out[j].Minute
		}
		return out[i].Model+out[i].Agent+out[i].Account < out[j].Model+out[j].Agent+out[j].Account
	})
	return out
}

// transcriptsShrank reports whether any transcript of the session is shorter
// than what was already read from it.
func transcriptsShrank(rec *store.SessionRecord, subagents map[string]string) bool {
	if size := fileSize(rec.TranscriptPath); size >= 0 && size < rec.TranscriptOffset {
		return true
	}
	for id, path := range subagents {
		if size := fileSize(path); size >= 0 && size < rec.SubagentReads[id].Offset {
			return true
		}
	}
	return false
}

func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return -1
	}
	return info.Size()
}

// Accumulate reads the lines added since the last call to the session's
// transcript and to each subagent's, adds their usage to the record, and
// returns it per minute for the usage log, each reply credited to the
// account account names for its time.
//
// rebuilt is true when the session's transcript or a subagent's was
// rewritten, which leaves it shorter than what was read: every transcript is
// then counted again from the start, and the returned buckets replace the
// session's usage log rather than add to it. Any problem leaves the record as it was; usage is best
// effort.
func Accumulate(rec *store.SessionRecord, account AccountAt) (fresh []store.UsageBucket, rebuilt bool) {
	if rec.TranscriptPath == "" {
		return nil, false
	}
	subagents := SubagentTranscripts(rec.TranscriptPath)
	if transcriptsShrank(rec, subagents) {
		rebuilt = true
		rec.Usage, rec.TranscriptOffset, rec.LastMessageID, rec.SubagentReads = nil, 0, "", nil
	}
	if rec.Usage == nil {
		rec.Usage = map[string]store.Usage{}
	}
	acc := newBuckets(account)
	add := func(agent string) func(reply) {
		return func(r reply) {
			u := rec.Usage[r.model]
			u.Add(r.usage)
			rec.Usage[r.model] = u
			acc.add(agent, r)
		}
	}
	rec.TranscriptOffset, rec.LastMessageID, _ = readFrom(rec.TranscriptPath, rec.TranscriptOffset, rec.LastMessageID, add(""))
	for id, path := range subagents {
		if rec.SubagentReads == nil {
			rec.SubagentReads = map[string]store.TranscriptRead{}
		}
		prev := rec.SubagentReads[id]
		off, lastID, _ := readFrom(path, prev.Offset, prev.LastID, add(id))
		rec.SubagentReads[id] = store.TranscriptRead{Offset: off, LastID: lastID}
	}
	return acc.list(), rebuilt
}

// Total sums every model's usage on a record.
func Total(rec store.SessionRecord) store.Usage {
	var t store.Usage
	for _, u := range rec.Usage {
		t.Add(u)
	}
	return t
}
