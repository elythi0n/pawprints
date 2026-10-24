package view

import (
	"github.com/elythi0n/pawprints/internal/sessions"
	"github.com/elythi0n/pawprints/internal/store"
)

// Kind of a tree node.
type Kind int

const (
	KindGroup   Kind = iota // a heading such as a day
	KindSession             // one Claude session
	KindTurn                // one prompt
	KindFile                // a path
	KindVersion             // one event
	KindPin                 // a pinned event
	KindNotice              // a one-line message, such as "nothing yet"
)

// Node is one row of a tree. Children are built eagerly; trees here are
// small (a few thousand rows at most).
type Node struct {
	Kind     Kind
	Key      string // stable identity, used to keep expansion across reloads
	Label    string
	Desc     string // dim text after the label
	Children []*Node
	Expanded bool
	Depth    int

	Event   *store.Event
	Edits   int // edits folded into a combined row; 0 for a single version
	Session *sessions.Session
	Turn    *store.Turn
	Path    string
	Pin     *store.Pin
	Agent   *store.Agent // set on a subagent's group
}

// IsGroup reports whether the node can expand.
func (n *Node) IsGroup() bool { return len(n.Children) > 0 }

// Flatten lists the visible rows in order, honoring expansion.
func Flatten(roots []*Node) []*Node {
	var out []*Node
	var walk func(n *Node, depth int)
	walk = func(n *Node, depth int) {
		n.Depth = depth
		out = append(out, n)
		if n.Expanded {
			for _, c := range n.Children {
				walk(c, depth+1)
			}
		}
	}
	for _, r := range roots {
		walk(r, 0)
	}
	return out
}

// ApplyExpansion re-applies a remembered set of expanded keys to new nodes.
func ApplyExpansion(roots []*Node, expanded map[string]bool) {
	var walk func(n *Node)
	walk = func(n *Node) {
		if v, ok := expanded[n.Key]; ok {
			n.Expanded = v
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	for _, r := range roots {
		walk(r)
	}
}

// Find locates a node by key.
func Find(roots []*Node, key string) *Node {
	var found *Node
	var walk func(n *Node)
	walk = func(n *Node) {
		if found != nil {
			return
		}
		if n.Key == key {
			found = n
			return
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	for _, r := range roots {
		walk(r)
	}
	return found
}

// Match reports whether a node or any descendant matches a filter string.
func Match(n *Node, filter string) bool {
	if filter == "" {
		return true
	}
	if containsFold(n.Label, filter) || containsFold(n.Desc, filter) || containsFold(n.Path, filter) {
		return true
	}
	for _, c := range n.Children {
		if Match(c, filter) {
			return true
		}
	}
	return false
}

// Filter returns a copy of the tree keeping only matching branches, fully
// expanded so the matches are visible.
func Filter(roots []*Node, filter string) []*Node {
	if filter == "" {
		return roots
	}
	var out []*Node
	for _, r := range roots {
		if !Match(r, filter) {
			continue
		}
		cp := *r
		cp.Children = Filter(r.Children, filter)
		cp.Expanded = true
		out = append(out, &cp)
	}
	return out
}

func containsFold(s, sub string) bool {
	return len(sub) == 0 || indexFold(s, sub) >= 0
}

func indexFold(s, sub string) int {
	ls, lsub := toLower(s), toLower(sub)
	for i := 0; i+len(lsub) <= len(ls); i++ {
		if ls[i:i+len(lsub)] == lsub {
			return i
		}
	}
	return -1
}

func toLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}
