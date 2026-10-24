package store

import (
	"bufio"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Ignore matches stored paths against gitignore-style patterns: "*", "?",
// "**", a leading "/" to anchor at the root, a trailing "/" for directories,
// and "!" to negate. The last matching pattern wins, as in git.
type Ignore struct {
	rules []ignoreRule
}

type ignoreRule struct {
	pattern  string
	negate   bool
	anchored bool
	dirOnly  bool
}

// NewIgnore compiles patterns.
func NewIgnore(patterns []string) *Ignore {
	ig := &Ignore{}
	for _, raw := range patterns {
		p := strings.TrimSpace(raw)
		if p == "" || strings.HasPrefix(p, "#") {
			continue
		}
		r := ignoreRule{}
		if strings.HasPrefix(p, "!") {
			r.negate = true
			p = p[1:]
		}
		if strings.HasSuffix(p, "/") {
			r.dirOnly = true
			p = strings.TrimSuffix(p, "/")
		}
		if strings.HasPrefix(p, "/") {
			r.anchored = true
			p = p[1:]
		} else if strings.Contains(p, "/") {
			r.anchored = true
		}
		r.pattern = p
		ig.rules = append(ig.rules, r)
	}
	return ig
}

// LoadProjectIgnore reads .pawprintsignore at the root and appends it to the
// global patterns.
func LoadProjectIgnore(root string, global []string) *Ignore {
	patterns := append([]string{}, global...)
	f, err := os.Open(filepath.Join(root, ".pawprintsignore"))
	if err == nil {
		defer f.Close()
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			patterns = append(patterns, sc.Text())
		}
	}
	return NewIgnore(patterns)
}

// Match reports whether a slash-separated relative path is ignored.
func (ig *Ignore) Match(rel string) bool {
	rel = strings.TrimPrefix(rel, "./")
	ignored := false
	for _, r := range ig.rules {
		if r.matches(rel) {
			ignored = !r.negate
		}
	}
	return ignored
}

func (r ignoreRule) matches(rel string) bool {
	pat := r.pattern
	if r.dirOnly {
		// A directory pattern matches anything beneath it.
		pat += "/**"
	}
	if r.anchored {
		return globMatch(pat, rel)
	}
	// Unanchored: match against the full path or any suffix of segments.
	segs := strings.Split(rel, "/")
	for i := range segs {
		if globMatch(pat, strings.Join(segs[i:], "/")) {
			return true
		}
	}
	return false
}

// globMatch supports "**" across segments on top of path.Match.
func globMatch(pattern, name string) bool {
	if !strings.Contains(pattern, "**") {
		ok, _ := path.Match(pattern, name)
		return ok
	}
	pSegs := strings.Split(pattern, "/")
	nSegs := strings.Split(name, "/")
	return matchSegs(pSegs, nSegs)
}

func matchSegs(p, n []string) bool {
	for len(p) > 0 {
		if p[0] == "**" {
			if len(p) == 1 {
				return true
			}
			for i := 0; i <= len(n); i++ {
				if matchSegs(p[1:], n[i:]) {
					return true
				}
			}
			return false
		}
		if len(n) == 0 {
			return false
		}
		if ok, _ := path.Match(p[0], n[0]); !ok {
			return false
		}
		p, n = p[1:], n[1:]
	}
	return len(n) == 0
}
