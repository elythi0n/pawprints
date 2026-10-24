package panes

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/elythi0n/pawprints/internal/store"
	"github.com/elythi0n/pawprints/internal/view"
)

// dir is one folder touched by a set of edits, with totals for everything
// below it.
type dir struct {
	name, path string
	files      []*view.Node
	dirs       []*dir
	fileCount  int
	added      int
	removed    int
}

// FolderTree lays a prompt's files out in their folders: every folder its
// edits touched, with file and line counts at each level, and each file once
// as its combined change. It is the tree layout of the rows under a prompt.
func FolderTree(s *view.Source, evs []store.Event, prefix string) []*view.Node {
	root := &dir{}
	for _, row := range groupByFile(s, evs, prefix+"d:", true) {
		ev := row.Event
		if ev == nil {
			continue
		}
		absolute := strings.HasPrefix(ev.Path, "/")
		parts := strings.Split(strings.TrimPrefix(ev.Path, "/"), "/")
		node := root
		p := ""
		for i, part := range parts[:len(parts)-1] {
			name := part
			if i == 0 && absolute {
				name = "/" + part
			}
			if p == "" {
				p = name
			} else {
				p += "/" + part
			}
			var child *dir
			for _, d := range node.dirs {
				if d.path == p {
					child = d
				}
			}
			if child == nil {
				child = &dir{name: name, path: p}
				node.dirs = append(node.dirs, child)
			}
			node = child
		}
		row.Label = path.Base(ev.Path)
		node.files = append(node.files, row)
		node.added += ev.LinesAdded
		node.removed += ev.LinesRemoved
	}
	totals(root)
	for i, d := range root.dirs {
		root.dirs[i] = compactDir(d)
	}
	sortDirs(root)
	return dirChildren(root, prefix)
}

// dirChildren lists a folder's subfolders, then its own files.
func dirChildren(d *dir, prefix string) []*view.Node {
	var out []*view.Node
	for _, sub := range d.dirs {
		out = append(out, &view.Node{
			Kind:     view.KindGroup,
			Key:      prefix + "dir:" + sub.path,
			Label:    sub.name + "/",
			Desc:     dirSummary(sub),
			Children: dirChildren(sub, prefix),
		})
	}
	return append(out, d.files...)
}

func dirSummary(d *dir) string {
	s := fmt.Sprintf("%d file", d.fileCount)
	if d.fileCount != 1 {
		s += "s"
	}
	if d.added > 0 || d.removed > 0 {
		s += fmt.Sprintf(" · +%d -%d", d.added, d.removed)
	}
	return s
}

func totals(d *dir) {
	d.fileCount = len(d.files)
	for _, sub := range d.dirs {
		totals(sub)
		d.fileCount += sub.fileCount
		d.added += sub.added
		d.removed += sub.removed
	}
}

// compactDir merges a folder that holds no files and one subfolder into it,
// so a deep, narrow path reads as one row.
func compactDir(d *dir) *dir {
	for len(d.files) == 0 && len(d.dirs) == 1 {
		child := d.dirs[0]
		child.name = d.name + "/" + child.name
		d = child
	}
	for i, sub := range d.dirs {
		d.dirs[i] = compactDir(sub)
	}
	return d
}

// sortDirs orders folders by files changed, project folders before ones
// outside the project.
func sortDirs(d *dir) {
	sort.SliceStable(d.dirs, func(i, j int) bool {
		a, b := d.dirs[i], d.dirs[j]
		ao, bo := strings.HasPrefix(a.path, "/"), strings.HasPrefix(b.path, "/")
		if ao != bo {
			return !ao
		}
		if a.fileCount != b.fileCount {
			return a.fileCount > b.fileCount
		}
		return a.name < b.name
	})
	for _, sub := range d.dirs {
		sortDirs(sub)
	}
}
