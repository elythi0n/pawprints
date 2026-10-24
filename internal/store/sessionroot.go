package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// A session is pinned to the project it was launched in. Claude changes
// directory freely during a session; without the pin every `cd` into a
// subfolder that is not its own git repository would start a new project and
// scatter one session's edits across many.

func sessionRootPath(id string) string {
	return filepath.Join(Home(), "session-roots", sanitize(id))
}

// SessionRoot returns the pinned root of a session.
func SessionRoot(id string) (string, bool) {
	if id == "" {
		return "", false
	}
	data, err := os.ReadFile(sessionRootPath(id))
	if err != nil {
		return "", false
	}
	root := strings.TrimSpace(string(data))
	return root, root != ""
}

// PinSessionRoot records a session's root. An existing pin is kept unless
// replace is set, so the first answer for a session wins.
func PinSessionRoot(id, root string, replace bool) error {
	if id == "" || root == "" {
		return nil
	}
	if _, ok := SessionRoot(id); ok && !replace {
		return nil
	}
	if err := os.MkdirAll(filepath.Join(Home(), "session-roots"), 0o755); err != nil {
		return err
	}
	return writeAtomic(sessionRootPath(id), []byte(root+"\n"))
}

// UnpinSessionsOf removes the pins of every session pinned to root, for when
// that project's history is cleared.
func UnpinSessionsOf(root string) {
	for id, r := range SessionRoots() {
		if r == root {
			_ = os.Remove(sessionRootPath(id))
		}
	}
}

// SessionRoots lists every pin.
func SessionRoots() map[string]string {
	out := map[string]string{}
	entries, err := os.ReadDir(filepath.Join(Home(), "session-roots"))
	if err != nil {
		return out
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if root, ok := SessionRoot(e.Name()); ok {
			out[e.Name()] = root
		}
	}
	return out
}

// FindProjectFor opens the project for a directory: the project rooted at
// it, else the nearest ancestor that has history. That way the TUI and CLI
// work from any subfolder of a project.
func FindProjectFor(dir string) (*Project, bool) {
	dir = filepath.Clean(dir)
	for {
		if p, ok := FindProject(dir); ok {
			return p, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil, false
		}
		dir = parent
	}
}

// Within reports whether path is root or inside it.
func Within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || (!filepath.IsAbs(rel) && rel != ".." && !strings.HasPrefix(rel, "../"))
}

// RemoveIfEmpty deletes a project slice that holds no events, sessions, or pins.
func (p *Project) RemoveIfEmpty() (bool, error) {
	months, err := p.Months()
	if err != nil {
		return false, err
	}
	recs, _ := p.Sessions()
	pins, _ := p.Pins()
	if len(months) > 0 || len(recs) > 0 || len(pins) > 0 {
		return false, nil
	}
	err = os.RemoveAll(p.Dir)
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	return err == nil, err
}

// RemoveEmptyProjects deletes project slices that never recorded anything
// and are older than minAge, such as one opened by `watch` in a folder where
// Claude never edited. It returns the roots removed.
func RemoveEmptyProjects(minAge time.Duration) []string {
	projects, err := ListProjects()
	if err != nil {
		return nil
	}
	var removed []string
	for _, p := range projects {
		info, err := os.Stat(filepath.Join(p.Dir, "project.json"))
		if err != nil || time.Since(info.ModTime()) < minAge {
			continue
		}
		if ok, _ := p.RemoveIfEmpty(); ok {
			removed = append(removed, p.Root)
		}
	}
	return removed
}
