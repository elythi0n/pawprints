// Package setup finds the places Pawprints plugs into (Claude Code hooks,
// editors, shell completions), reports what state each is in, and installs
// the ones a person picks. The TUI shows it as a checklist on first run;
// `pawprints setup --yes` applies the defaults without asking.
package setup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/elythi0n/pawprints/internal/buildinfo"
	"github.com/elythi0n/pawprints/internal/store"
)

// States an item can be in.
const (
	Missing     = "missing"     // available but not set up
	Outdated    = "outdated"    // set up, older than this build
	Current     = "current"     // set up and up to date
	Provided    = "provided"    // already covered another way (the plugin provides the hooks)
	Unavailable = "unavailable" // the editor or shell is not on this machine
)

// Item is one row of the checklist.
type Item struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Detail string `json:"detail"` // what was found: "code found", "zsh", ...
	State  string `json:"state"`
	Note   string `json:"note"` // what applying does, or why it cannot
	// Default is whether the row starts ticked: things that are missing or
	// out of date, and that can be done.
	Default bool `json:"default"`

	kind   string // hooks, editor, completion
	editor *Editor
	shell  string
}

// Actionable reports whether the row can be ticked.
func (i Item) Actionable() bool { return i.State == Missing || i.State == Outdated }

// Scan looks at the machine and returns every item, in display order.
func Scan() []Item {
	items := []Item{hooksItem()}
	for _, e := range Editors() {
		items = append(items, editorItem(e))
	}
	items = append(items, completionItem())
	return items
}

// Apply installs one item and returns a line saying what happened.
func Apply(it Item) (string, error) {
	switch it.kind {
	case "hooks":
		return applyHooks()
	case "editor":
		return applyEditor(*it.editor)
	case "completion":
		return applyCompletion(it.shell)
	}
	return "", nil
}

// Marker records that setup ran, so the first-run screen shows once.
type Marker struct {
	Version string            `json:"version"`
	At      string            `json:"at"`
	Skipped bool              `json:"skipped,omitempty"`
	Results map[string]string `json:"results,omitempty"`
}

func markerPath() string { return filepath.Join(store.Home(), "setup.json") }

// LoadMarker reads the marker; ok is false when setup never ran.
func LoadMarker() (Marker, bool) {
	data, err := os.ReadFile(markerPath())
	if err != nil {
		return Marker{}, false
	}
	var m Marker
	if json.Unmarshal(data, &m) != nil {
		return Marker{}, false
	}
	return m, true
}

// SaveMarker records a finished or skipped setup.
func SaveMarker(skipped bool, results map[string]string) error {
	if _, err := store.EnsureHome(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(Marker{Version: buildinfo.Version, At: time.Now().Format(time.RFC3339), Skipped: skipped, Results: results}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(markerPath(), append(data, '\n'), 0o644)
}

// ShouldOffer reports whether the TUI should open the checklist on its own:
// on the very first run, and after an upgrade when an editor still has an
// older extension than the one this build carries.
func ShouldOffer(items []Item) bool {
	m, ok := LoadMarker()
	if !ok {
		return true
	}
	if m.Version == buildinfo.Version {
		return false
	}
	for _, it := range items {
		if it.State == Outdated {
			return true
		}
	}
	return false
}
