// Package tui is the terminal UI: a root shell with a sidebar of panes.
package tui

import "github.com/elythi0n/pawprints/internal/store"

// ReloadMsg tells every pane the source changed.
type ReloadMsg struct{}

// RequestReloadMsg asks the root to reload the source and broadcast.
type RequestReloadMsg struct{}

// JumpMsg asks the root to switch pane and select a node.
type JumpMsg struct {
	Pane string
	Key  string
}

// ToggleAllProjectsMsg flips the source between this project and all.
type ToggleAllProjectsMsg struct{}

type previewMsg struct {
	pane string
	key  string
	text string
}

type actionDoneMsg struct {
	text string
	kind string
	ev   *store.Event
}

type fullMsg struct {
	pane   string
	key    string
	header string
	text   string
	hunks  []int
}

// fullState is the full-screen viewer's settings and position.
type fullState struct {
	mode      string // "diff" or "file"
	wrap      bool
	key       string
	header    string
	hunks     []int
	at        int
	jumpFirst bool
}
