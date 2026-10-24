package store

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// KnownRoots lists the root folder of every project in the store.
func KnownRoots() []string {
	entries, err := os.ReadDir(filepath.Join(Home(), "projects"))
	if err != nil {
		return nil
	}
	var roots []string
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(Home(), "projects", e.Name(), "project.json"))
		if err != nil {
			continue
		}
		var info ProjectInfo
		if json.Unmarshal(data, &info) == nil && info.Root != "" {
			roots = append(roots, info.Root)
		}
	}
	return roots
}

// Owner is the project a file belongs to: the deepest of roots that holds
// it, or "" when it lies in none.
func Owner(roots []string, path string) string {
	owner := ""
	for _, r := range roots {
		if Within(r, path) && len(r) > len(owner) {
			owner = r
		}
	}
	return owner
}

// HoldsProjects reports whether dir contains root or any of roots other
// than dir itself, such as a parent folder of several projects.
func HoldsProjects(dir, root string, roots []string) bool {
	if Within(dir, root) {
		return true
	}
	for _, r := range roots {
		if r != dir && Within(dir, r) {
			return true
		}
	}
	return false
}
