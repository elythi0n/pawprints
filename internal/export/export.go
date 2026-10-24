// Package export turns a slice of history into a unified patch: the net
// change per file from the earliest "before" to the latest "after".
package export

import (
	"sort"
	"strings"

	"github.com/elythi0n/pawprints/internal/diff"
	"github.com/elythi0n/pawprints/internal/store"
)

// FileChange is the net change of one file.
type FileChange struct {
	Path   string
	Before string // hash, "" when created
	After  string // hash, "" when deleted
	Events int
}

// Collect reduces events (newest first) to one net change per path.
func Collect(events []store.Event) []FileChange {
	byPath := map[string]*FileChange{}
	var order []string
	for i := len(events) - 1; i >= 0; i-- { // oldest first
		ev := events[i]
		fc, ok := byPath[ev.Path]
		if !ok {
			fc = &FileChange{Path: ev.Path, Before: ev.BeforeHash()}
			byPath[ev.Path] = fc
			order = append(order, ev.Path)
		}
		fc.After = ev.AfterHash()
		fc.Events++
	}
	sort.Strings(order)
	out := make([]FileChange, 0, len(order))
	for _, path := range order {
		fc := byPath[path]
		if fc.Before == fc.After {
			continue
		}
		out = append(out, *fc)
	}
	return out
}

// Patch renders the net changes as a unified diff that `git apply` or
// `patch -p1` can read.
func Patch(p *store.Project, changes []FileChange) (string, error) {
	var sb strings.Builder
	for _, fc := range changes {
		before, err := p.Get(fc.Before)
		if err != nil {
			return "", err
		}
		after, err := p.Get(fc.After)
		if err != nil {
			return "", err
		}
		if store.IsBinary(before) || store.IsBinary(after) {
			sb.WriteString("diff --git a/" + fc.Path + " b/" + fc.Path + "\nBinary files differ\n")
			continue
		}
		aName, bName := "a/"+fc.Path, "b/"+fc.Path
		if fc.Before == "" {
			aName = "/dev/null"
		}
		if fc.After == "" {
			bName = "/dev/null"
		}
		sb.WriteString("diff --git a/" + fc.Path + " b/" + fc.Path + "\n")
		switch {
		case fc.Before == "":
			sb.WriteString("new file mode 100644\n")
		case fc.After == "":
			sb.WriteString("deleted file mode 100644\n")
		}
		sb.WriteString(diff.Unified(aName, bName, before, after, 3))
	}
	return sb.String(), nil
}
