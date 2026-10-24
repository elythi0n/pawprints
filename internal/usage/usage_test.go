package usage

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elythi0n/pawprints/internal/store"
)

func appendTo(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(strings.Join(lines, "")); err != nil {
		t.Fatal(err)
	}
}

func TestAccumulateIncremental(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.jsonl")
	line1 := `{"type":"assistant","timestamp":"2026-10-23T10:00:05Z","message":{"id":"a","model":"m1","usage":{"input_tokens":10,"cache_creation_input_tokens":5,"cache_read_input_tokens":100,"output_tokens":20,"output_tokens_details":{"thinking_tokens":7}}}}` + "\n"
	user := `{"type":"user","message":{"content":"hi"}}` + "\n"
	appendTo(t, path, line1, user)
	rec := store.SessionRecord{TranscriptPath: path}
	got, _ := Accumulate(&rec, func(time.Time) string { return "acct" })
	u := rec.Usage["m1"]
	if u.Input != 10 || u.Output != 20 || u.CacheRead != 100 || u.CacheWrite != 5 || u.Thinking != 7 || u.Messages != 1 {
		t.Fatalf("usage %+v", u)
	}
	if len(got) != 1 || got[0].Minute != "2026-10-23T10:00:00Z" || got[0].Account != "acct" || got[0].Input != 10 {
		t.Fatalf("buckets %+v", got)
	}
	if rec.TranscriptOffset != int64(len(line1)+len(user)) {
		t.Fatalf("offset %d", rec.TranscriptOffset)
	}
	// A second reply plus a partial line; only the complete one counts.
	line2 := `{"type":"assistant","timestamp":"2026-10-23T10:01:00Z","message":{"id":"b","model":"m1","usage":{"input_tokens":1,"output_tokens":2}}}` + "\n"
	appendTo(t, path, line2, `{"type":"assistant","message":{"model":"m1","usage":{"input_tokens":99`)
	got, _ = Accumulate(&rec, func(time.Time) string { return "acct" })
	u = rec.Usage["m1"]
	if u.Input != 11 || u.Output != 22 || u.Messages != 2 {
		t.Fatalf("after append %+v", u)
	}
	if len(got) != 1 || got[0].Minute != "2026-10-23T10:01:00Z" {
		t.Fatalf("only the new minute is returned: %+v", got)
	}
	if rec.TranscriptOffset != int64(len(line1)+len(user)+len(line2)) {
		t.Fatalf("offset after partial %d", rec.TranscriptOffset)
	}
}

// Claude Code writes a reply with several content blocks as several lines
// that all carry the reply's usage. It must count once, also across calls.
func TestSplitReplyCountsOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.jsonl")
	block := `{"type":"assistant","timestamp":"2026-10-23T10:00:00Z","message":{"id":"r1","model":"m","usage":{"input_tokens":3,"output_tokens":40}}}` + "\n"
	appendTo(t, path, block, block)
	rec := store.SessionRecord{TranscriptPath: path}
	Accumulate(&rec, nil)
	appendTo(t, path, block)
	Accumulate(&rec, nil)
	if u := rec.Usage["m"]; u.Output != 40 || u.Messages != 1 {
		t.Fatalf("one reply in three lines over two reads: %+v", u)
	}
}

func TestSubagentTranscripts(t *testing.T) {
	dir := t.TempDir()
	main := filepath.Join(dir, "s1.jsonl")
	appendTo(t, main, `{"type":"assistant","timestamp":"2026-10-23T10:00:00Z","message":{"id":"m1","model":"m","usage":{"input_tokens":1,"output_tokens":1}}}`+"\n")
	sub := filepath.Join(dir, "s1", "subagents", "agent-abc.jsonl")
	appendTo(t, sub, `{"type":"assistant","isSidechain":true,"timestamp":"2026-10-23T10:00:30Z","message":{"id":"x1","model":"m","usage":{"input_tokens":5,"output_tokens":7}}}`+"\n")
	rec := store.SessionRecord{TranscriptPath: main}
	got, _ := Accumulate(&rec, nil)
	if u := rec.Usage["m"]; u.Input != 6 || u.Output != 8 || u.Messages != 2 {
		t.Fatalf("subagent usage is added: %+v", u)
	}
	var agentBucket *store.UsageBucket
	for i := range got {
		if got[i].Agent == "abc" {
			agentBucket = &got[i]
		}
	}
	if agentBucket == nil || agentBucket.Output != 7 {
		t.Fatalf("subagent bucket %+v", got)
	}
	if rec.SubagentReads["abc"].Offset == 0 {
		t.Fatal("subagent offset kept")
	}
	if more, _ := Accumulate(&rec, nil); len(more) != 0 {
		t.Fatalf("nothing new, nothing returned: %+v", more)
	}
}

func TestAPICost(t *testing.T) {
	u := store.Usage{Input: 1e6, Output: 1e6, CacheRead: 1e6, CacheWrite: 2e6, CacheWrite1h: 1e6}
	// Opus 5.5: 4 in, 20 out, 0.20 read, 5m write 5, 1h write 8.
	cost, ok := APICost("claude-opus-5-5", u, nil)
	if !ok || math.Abs(cost-(4+20+0.20+5+8)) > 1e-9 {
		t.Fatalf("cost %v %v", cost, ok)
	}
	if _, ok := APICost("us.anthropic.claude-sonnet-4-5-20250929-v1:0", u, nil); !ok {
		t.Fatal("provider and dated ids resolve")
	}
	if _, ok := APICost("mystery-model", u, nil); ok {
		t.Fatal("unknown model has no price")
	}
	over := map[string]store.Price{"mystery-model": {Input: 1, Output: 1}}
	if c, ok := APICost("mystery-model", store.Usage{Input: 1e6}, over); !ok || c != 1 {
		t.Fatalf("config price %v %v", c, ok)
	}
	total, complete := Cost(map[string]store.Usage{"claude-opus-5-5": {Output: 1e6}, "mystery-model": {Output: 1}}, nil)
	if complete || total != 20 {
		t.Fatalf("partial total %v %v", total, complete)
	}
}

func TestTailFollowsPastTheRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.jsonl")
	appendTo(t, path, `{"type":"assistant","timestamp":"2026-10-23T10:00:00Z","message":{"id":"a","model":"m","usage":{"output_tokens":5}}}`+"\n")
	rec := store.SessionRecord{TranscriptPath: path}
	Accumulate(&rec, nil)
	tail := NewTail()
	if got := tail.Pending(rec); len(got) != 0 {
		t.Fatalf("nothing past the record yet: %+v", got)
	}
	appendTo(t, path, `{"type":"assistant","timestamp":"2026-10-23T10:02:00Z","message":{"id":"b","model":"m","usage":{"output_tokens":9}}}`+"\n")
	size := func(p string) int64 { info, _ := os.Stat(p); return info.Size() }
	if !tail.Grew(rec, size) {
		t.Fatal("growth is seen")
	}
	got := tail.Pending(rec)
	if len(got) != 1 || got[0].Output != 9 {
		t.Fatalf("pending %+v", got)
	}
	if tail.Grew(rec, size) {
		t.Fatal("read bytes are not growth")
	}
	// The stop hook records the line; the tail lets go of it.
	Accumulate(&rec, nil)
	if got := tail.Pending(rec); len(got) != 0 {
		t.Fatalf("recorded lines leave the tail: %+v", got)
	}
}

// A rewritten transcript, shorter than what was read, is counted again from
// the start exactly once, and the caller is told to replace the usage log.
func TestRewrittenTranscriptCountsOnce(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.jsonl")
	line := func(id string, out int) string {
		return `{"type":"assistant","timestamp":"2026-10-23T10:00:00Z","message":{"id":"` + id + `","model":"m","usage":{"output_tokens":` + itoa(out) + `}}}` + "\n"
	}
	appendTo(t, path, line("a", 10), line("b", 20), line("c", 30))
	sub := filepath.Join(dir, "s", "subagents", "agent-x.jsonl")
	appendTo(t, sub, line("x", 5))
	rec := store.SessionRecord{TranscriptPath: path}
	if _, rebuilt := Accumulate(&rec, nil); rebuilt {
		t.Fatal("a first read is not a rebuild")
	}
	if err := os.WriteFile(path, []byte(line("a", 10)), 0o644); err != nil {
		t.Fatal(err)
	}
	got, rebuilt := Accumulate(&rec, nil)
	if !rebuilt {
		t.Fatal("a shorter transcript is a rebuild")
	}
	if u := rec.Usage["m"]; u.Output != 15 || u.Messages != 2 {
		t.Fatalf("the rewritten file and the subagent, once each: %+v", u)
	}
	var total int64
	for _, b := range got {
		total += b.Output
	}
	if total != 15 {
		t.Fatalf("buckets that replace the log hold everything once: %d", total)
	}
}

// Each reply goes to the account of the prompt it answers, so logging out
// and in as someone else between prompts splits a session.
func TestRepliesFollowTheirPromptsAccount(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	appendTo(t, path,
		`{"type":"assistant","timestamp":"2026-10-23T10:00:30Z","message":{"id":"a","model":"m","usage":{"output_tokens":10}}}`+"\n",
		`{"type":"assistant","timestamp":"2026-10-23T10:05:30Z","message":{"id":"b","model":"m","usage":{"output_tokens":20}}}`+"\n")
	rec := store.SessionRecord{TranscriptPath: path, Account: "B", Turns: []store.Turn{
		{ID: "T1", At: "2026-10-23T10:00:00Z", Account: "A"},
		{ID: "T2", At: "2026-10-23T10:05:00Z", Account: "B"},
	}}
	got, _ := Accumulate(&rec, rec.AccountAt)
	by := map[string]int64{}
	for _, b := range got {
		by[b.Account] += b.Output
	}
	if by["A"] != 10 || by["B"] != 20 {
		t.Fatalf("per account %v", by)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// A rewritten subagent transcript rebuilds the whole session once, so its
// earlier replies are not counted again on top.
func TestRewrittenSubagentTranscriptCountsOnce(t *testing.T) {
	dir := t.TempDir()
	main := filepath.Join(dir, "s.jsonl")
	appendTo(t, main, `{"type":"assistant","timestamp":"2026-10-23T10:00:00Z","message":{"id":"m1","model":"m","usage":{"output_tokens":1}}}`+"\n")
	sub := filepath.Join(dir, "s", "subagents", "agent-x.jsonl")
	line := func(id string) string {
		return `{"type":"assistant","timestamp":"2026-10-23T10:00:00Z","message":{"id":"` + id + `","model":"m","usage":{"output_tokens":10}}}` + "\n"
	}
	appendTo(t, sub, line("x1"), line("x2"), line("x3"))
	rec := store.SessionRecord{TranscriptPath: main}
	Accumulate(&rec, nil)
	if err := os.WriteFile(sub, []byte(line("x1")), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, rebuilt := Accumulate(&rec, nil); !rebuilt {
		t.Fatal("a shorter subagent transcript rebuilds the session")
	}
	if u := rec.Usage["m"]; u.Output != 11 || u.Messages != 2 {
		t.Fatalf("each reply once: %+v", u)
	}
}

// The tail credits pending replies by the record's accounts at each call, so
// an account switch shows up without the transcript offset changing.
func TestTailUsesTheCurrentAccounts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	rec := store.SessionRecord{TranscriptPath: path, Account: "A"}
	appendTo(t, path, `{"type":"assistant","timestamp":"2026-10-23T10:05:30Z","message":{"id":"a","model":"m","usage":{"output_tokens":5}}}`+"\n")
	tail := NewTail()
	if got := tail.Pending(rec); len(got) != 1 || got[0].Account != "A" {
		t.Fatalf("first %+v", got)
	}
	rec.Turns = []store.Turn{{ID: "T", At: "2026-10-23T10:05:00Z", Account: "B"}}
	if got := tail.Pending(rec); len(got) != 1 || got[0].Account != "B" {
		t.Fatalf("after the prompt's account is known %+v", got)
	}
}
