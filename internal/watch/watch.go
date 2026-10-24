// Package watch follows the store and Claude Code's session registry and
// emits events and session changes as they happen. fsnotify is the primary
// signal; a slow poll covers filesystems where it does not work.
package watch

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/elythi0n/pawprints/internal/logfile"
	"github.com/elythi0n/pawprints/internal/sessions"
	"github.com/elythi0n/pawprints/internal/store"
)

// Kinds of change.
const (
	KindEvent     = "event"
	KindSession   = "session"
	KindHeartbeat = "heartbeat"
)

// Change is one line of `watch --json`.
type Change struct {
	Kind    string            `json:"kind"`
	Event   *store.Event      `json:"event,omitempty"`
	Session *sessions.Session `json:"session,omitempty"`
	Project string            `json:"project,omitempty"`
}

// Options configure a watcher.
type Options struct {
	Projects     []*store.Project // nil with All means every project, re-scanned on each tick
	All          bool
	PollInterval time.Duration
	Heartbeat    time.Duration
}

// Watcher tails event files and recomputes the session list on changes.
type Watcher struct {
	opts    Options
	offsets map[string]int64
	known   map[string]sessions.Session
	first   bool
}

// New prepares a watcher. Nothing happens until Run.
func New(opts Options) *Watcher {
	if opts.PollInterval <= 0 {
		opts.PollInterval = 2 * time.Second
	}
	if opts.Heartbeat <= 0 {
		opts.Heartbeat = 30 * time.Second
	}
	return &Watcher{opts: opts, offsets: map[string]int64{}, known: map[string]sessions.Session{}, first: true}
}

// Run streams changes until the context ends. The initial session list is
// emitted once so a client can render without a separate query; existing
// events are not replayed.
func (w *Watcher) Run(ctx context.Context, out chan<- Change) error {
	defer close(out)
	projects := w.projects()
	for _, p := range projects {
		w.seedOffsets(p)
	}
	w.emitSessions(out)

	fs, err := fsnotify.NewWatcher()
	if err == nil {
		defer fs.Close()
		w.addWatches(fs, projects)
	}
	poll := time.NewTicker(w.opts.PollInterval)
	defer poll.Stop()
	beat := time.NewTicker(w.opts.Heartbeat)
	defer beat.Stop()
	debounce := time.NewTimer(time.Hour)
	debounce.Stop()
	dirty := false

	for {
		var fsEvents <-chan fsnotify.Event
		var fsErrors <-chan error
		if fs != nil {
			fsEvents, fsErrors = fs.Events, fs.Errors
		}
		select {
		case <-ctx.Done():
			return nil
		case <-fsEvents:
			if !dirty {
				dirty = true
				debounce.Reset(150 * time.Millisecond)
			}
		case err := <-fsErrors:
			// Fall back to polling for this run.
			logfile.Errorf("watch: fsnotify failed, polling instead: %v", err)
			fs.Close()
			fs = nil
		case <-debounce.C:
			dirty = false
			w.scan(out, fs)
		case <-poll.C:
			w.scan(out, fs)
		case <-beat.C:
			out <- Change{Kind: KindHeartbeat}
		}
	}
}

func (w *Watcher) projects() []*store.Project {
	if w.opts.All {
		ps, _ := store.ListProjects()
		return ps
	}
	return w.opts.Projects
}

func (w *Watcher) addWatches(fs *fsnotify.Watcher, projects []*store.Project) {
	for _, dir := range sessions.RegistryDirs() {
		_ = fs.Add(dir)
	}
	if w.opts.All {
		_ = fs.Add(filepath.Join(store.Home(), "projects"))
	}
	for _, p := range projects {
		_ = fs.Add(filepath.Join(p.Dir, "events"))
		_ = fs.Add(filepath.Join(p.Dir, "sessions"))
	}
}

func (w *Watcher) seedOffsets(p *store.Project) {
	months, _ := p.Months()
	for _, m := range months {
		path := filepath.Join(p.Dir, "events", m+".jsonl")
		if info, err := os.Stat(path); err == nil {
			w.offsets[path] = info.Size()
		}
	}
}

// scan reads any new event lines and re-diffs the session list.
func (w *Watcher) scan(out chan<- Change, fs *fsnotify.Watcher) {
	projects := w.projects()
	for _, p := range projects {
		if _, seen := w.offsets[p.Dir]; !seen && fs != nil {
			// A project that appeared after start.
			w.offsets[p.Dir] = 0
			_ = fs.Add(filepath.Join(p.Dir, "events"))
			_ = fs.Add(filepath.Join(p.Dir, "sessions"))
		}
		months, _ := p.Months()
		for _, m := range months {
			path := filepath.Join(p.Dir, "events", m+".jsonl")
			w.tail(p, path, out)
		}
	}
	w.emitSessions(out)
}

func (w *Watcher) tail(p *store.Project, path string, out chan<- Change) {
	info, err := os.Stat(path)
	if err != nil {
		delete(w.offsets, path)
		return
	}
	offset := w.offsets[path]
	if info.Size() < offset {
		offset = 0 // file was rewritten (clear or prune)
	}
	if info.Size() == offset {
		return
	}
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	if _, err := f.Seek(offset, 0); err != nil {
		return
	}
	r := bufio.NewReader(f)
	consumed := offset
	for {
		line, err := r.ReadBytes('\n')
		if err != nil {
			break
		}
		consumed += int64(len(line))
		line = []byte(strings.TrimSpace(string(line)))
		if len(line) == 0 {
			continue
		}
		var ev store.Event
		if json.Unmarshal(line, &ev) != nil {
			continue
		}
		out <- Change{Kind: KindEvent, Event: &ev, Project: p.Root}
	}
	w.offsets[path] = consumed
}

func (w *Watcher) emitSessions(out chan<- Change) {
	opts := sessions.Options{All: w.opts.All}
	if !w.opts.All && len(w.opts.Projects) > 0 {
		opts.Project = w.opts.Projects[0]
	}
	var list []sessions.Session
	if w.opts.All || opts.Project != nil {
		var err error
		list, err = sessions.List(opts)
		if err != nil {
			return
		}
	}
	if !w.opts.All && len(w.opts.Projects) > 1 {
		for _, p := range w.opts.Projects[1:] {
			more, err := sessions.List(sessions.Options{Project: p})
			if err == nil {
				list = append(list, more...)
			}
		}
	}
	seen := map[string]bool{}
	for _, s := range list {
		seen[s.ID] = true
		prev, ok := w.known[s.ID]
		if ok && reflect.DeepEqual(prev, s) {
			continue
		}
		w.known[s.ID] = s
		sc := s
		out <- Change{Kind: KindSession, Session: &sc, Project: s.Project}
	}
	for id, prev := range w.known {
		if !seen[id] {
			// Gone from every source: report it once as ended.
			prev.Alive = false
			prev.Status = sessions.StatusEnded
			delete(w.known, id)
			gone := prev
			out <- Change{Kind: KindSession, Session: &gone, Project: prev.Project}
		}
	}
	w.first = false
}
