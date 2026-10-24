package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Project is one tracked directory and its slice of the store.
type Project struct {
	Root string
	ID   string
	Dir  string
}

// ProjectInfo is project.json.
type ProjectInfo struct {
	Root      string    `json:"root"`
	FirstSeen time.Time `json:"first_seen"`
}

// rootCache remembers ResolveRoot answers for a while: the TUI asks for every
// running session on every refresh, and each answer costs a git process.
var rootCache = struct {
	sync.Mutex
	m map[string]rootEntry
}{m: map[string]rootEntry{}}

type rootEntry struct {
	root string
	at   time.Time
}

const rootCacheTTL = time.Minute

// ResolveRoot finds the project root for a directory: the git top-level when
// inside a repository, otherwise the directory itself.
func ResolveRoot(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	rootCache.Lock()
	e, ok := rootCache.m[abs]
	rootCache.Unlock()
	if ok && time.Since(e.at) < rootCacheTTL {
		return e.root
	}
	root := resolveRoot(abs)
	rootCache.Lock()
	rootCache.m[abs] = rootEntry{root: root, at: time.Now()}
	rootCache.Unlock()
	return root
}

func resolveRoot(abs string) string {
	cmd := exec.Command("git", "-C", abs, "rev-parse", "--show-toplevel")
	out, err := cmd.Output()
	if err == nil {
		if top := strings.TrimSpace(string(out)); top != "" {
			return top
		}
	}
	return abs
}

// ProjectID derives the directory name under projects/ from the root path.
func ProjectID(root string) string {
	sum := sha256.Sum256([]byte(root))
	return hex.EncodeToString(sum[:8])
}

// OpenProject opens or creates the project slice for a root directory.
func OpenProject(root string) (*Project, error) {
	home, err := EnsureHome()
	if err != nil {
		return nil, err
	}
	p := &Project{Root: root, ID: ProjectID(root)}
	p.Dir = filepath.Join(home, "projects", p.ID)
	for _, sub := range []string{"events", "sessions", "pending", "state"} {
		if err := os.MkdirAll(filepath.Join(p.Dir, sub), 0o755); err != nil {
			return nil, err
		}
	}
	infoPath := filepath.Join(p.Dir, "project.json")
	if _, err := os.Stat(infoPath); errors.Is(err, os.ErrNotExist) {
		info := ProjectInfo{Root: root, FirstSeen: time.Now()}
		data, _ := json.MarshalIndent(info, "", "  ")
		if err := os.WriteFile(infoPath, append(data, '\n'), 0o644); err != nil {
			return nil, err
		}
	}
	if err := p.initObjects(); err != nil {
		return nil, err
	}
	return p, nil
}

// FindProject opens a project only if it already exists in the store.
func FindProject(root string) (*Project, bool) {
	p := &Project{Root: root, ID: ProjectID(root)}
	p.Dir = filepath.Join(Home(), "projects", p.ID)
	if _, err := os.Stat(filepath.Join(p.Dir, "project.json")); err != nil {
		return nil, false
	}
	return p, true
}

// ListProjects returns every project in the store, most recently active first.
func ListProjects() ([]*Project, error) {
	entries, err := os.ReadDir(filepath.Join(Home(), "projects"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	type withTime struct {
		p *Project
		t time.Time
	}
	var all []withTime
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(Home(), "projects", e.Name())
		data, err := os.ReadFile(filepath.Join(dir, "project.json"))
		if err != nil {
			continue
		}
		var info ProjectInfo
		if json.Unmarshal(data, &info) != nil {
			continue
		}
		p := &Project{Root: info.Root, ID: e.Name(), Dir: dir}
		all = append(all, withTime{p, p.lastActivity()})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].t.After(all[j].t) })
	out := make([]*Project, len(all))
	for i, w := range all {
		out[i] = w.p
	}
	return out, nil
}

// lastActivity is the newest mtime among the event files, used for ordering.
func (p *Project) lastActivity() time.Time {
	entries, err := os.ReadDir(filepath.Join(p.Dir, "events"))
	if err != nil {
		return time.Time{}
	}
	var latest time.Time
	for _, e := range entries {
		if info, err := e.Info(); err == nil && info.ModTime().After(latest) {
			latest = info.ModTime()
		}
	}
	return latest
}

// Rel converts an absolute path to the slash-separated path stored on events.
// Paths outside the root are returned absolute so nothing is silently dropped.
func (p *Project) Rel(abs string) string {
	rel, err := filepath.Rel(p.Root, abs)
	if err != nil || strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(abs)
	}
	return filepath.ToSlash(rel)
}

// Abs converts a stored path back to an absolute one.
func (p *Project) Abs(stored string) string {
	if filepath.IsAbs(stored) {
		return stored
	}
	return filepath.Join(p.Root, filepath.FromSlash(stored))
}

// Size returns the disk usage of the project slice in bytes.
func (p *Project) Size() int64 {
	var total int64
	_ = filepath.WalkDir(p.Dir, func(_ string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, err := d.Info(); err == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}
