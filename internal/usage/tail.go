package usage

import (
	"sync"
	"time"

	"github.com/elythi0n/pawprints/internal/store"
)

// Tail follows the transcripts of running sessions past what the hooks have
// recorded, so a long prompt shows up before it ends. It only reads, and it
// keeps its place: each call reads just the bytes added since the last one.
// Once a stop hook records those lines, the tail drops what it had for them.
type Tail struct {
	mu        sync.Mutex
	files     map[string]*tailFile
	lastSweep time.Time
}

// tailFile is one transcript followed past its recorded offset. It keeps the
// replies themselves, so each call can credit them by the record's accounts
// as they are then.
type tailFile struct {
	base    int64
	offset  int64
	lastID  string
	agent   string
	replies []reply
	used    time.Time
}

// tailIdle is how long a transcript nobody asked about stays followed.
const tailIdle = 10 * time.Minute

// NewTail starts an empty tail.
func NewTail() *Tail { return &Tail{files: map[string]*tailFile{}} }

// Pending is the usage written to a session's transcripts after what its
// record has counted, per minute.
func (t *Tail) Pending(rec store.SessionRecord) []store.UsageBucket {
	if t == nil || rec.TranscriptPath == "" {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	t.sweep(now)
	acc := newBuckets(rec.AccountAt)
	read := func(path, agent string, base int64, lastID string) {
		tf, ok := t.files[path]
		if !ok || tf.base != base {
			tf = &tailFile{base: base, offset: base, lastID: lastID, agent: agent}
			t.files[path] = tf
		}
		tf.used = now
		tf.offset, tf.lastID, _ = readFrom(path, tf.offset, tf.lastID, func(r reply) { tf.replies = append(tf.replies, r) })
		for _, r := range tf.replies {
			acc.add(tf.agent, r)
		}
	}
	read(rec.TranscriptPath, "", rec.TranscriptOffset, rec.LastMessageID)
	for id, path := range SubagentTranscripts(rec.TranscriptPath) {
		prev := rec.SubagentReads[id]
		read(path, id, prev.Offset, prev.LastID)
	}
	return acc.list()
}

// sweep forgets transcripts nobody asked about lately, such as those of
// sessions that ended, so a long-running server does not keep them.
func (t *Tail) sweep(now time.Time) {
	if now.Sub(t.lastSweep) < time.Minute {
		return
	}
	t.lastSweep = now
	for path, tf := range t.files {
		if now.Sub(tf.used) > tailIdle {
			delete(t.files, path)
		}
	}
}

// Grew reports whether any transcript the tail follows, or a new subagent's,
// has bytes it has not read yet. It costs a stat per file.
func (t *Tail) Grew(rec store.SessionRecord, size func(string) int64) bool {
	if t == nil || rec.TranscriptPath == "" {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	check := func(path string, base int64) bool {
		known := base
		if tf, ok := t.files[path]; ok && tf.base == base {
			known = tf.offset
		}
		return size(path) > known
	}
	if check(rec.TranscriptPath, rec.TranscriptOffset) {
		return true
	}
	for id, path := range SubagentTranscripts(rec.TranscriptPath) {
		if check(path, rec.SubagentReads[id].Offset) {
			return true
		}
	}
	return false
}
