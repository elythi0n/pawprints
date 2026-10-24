// Package view holds the data the TUI panes render: a snapshot of events,
// sessions, and pins for one project (or all), plus the tree nodes built
// from it. Panes read from a Source; they never touch the store directly.
package view

import (
	"sort"
	"time"

	"github.com/elythi0n/pawprints/internal/sessions"
	"github.com/elythi0n/pawprints/internal/store"
)

// MaxEvents bounds how much history a pane loads at once.
const MaxEvents = 2000

// Source is the loaded state. Reload replaces it wholesale; it is cheap for
// the sizes involved and keeps every pane consistent.
type Source struct {
	Root        string
	Project     *store.Project // nil until the first edit is recorded
	Cfg         store.Config
	AllProjects bool
	Now         time.Time

	Events   []store.Event // newest first
	Sessions []sessions.Session
	Pins     []store.Pin
	Projects []*store.Project
	Err      error
}

// NewSource prepares a source for a root directory.
func NewSource(root string) *Source {
	cfg, _ := store.LoadConfig()
	return &Source{Root: root, Cfg: cfg, Now: time.Now()}
}

// Reload reads everything again.
func (s *Source) Reload() {
	s.Now = time.Now()
	if cfg, err := store.LoadConfig(); err == nil {
		s.Cfg = cfg
	}
	s.Err = nil
	if p, ok := store.FindProjectFor(s.Root); ok {
		s.Project = p
	} else {
		s.Project = nil
	}
	s.Events = nil
	s.Pins = nil
	if s.Project != nil {
		evs, err := s.Project.Events(store.Query{Limit: MaxEvents})
		if err != nil {
			s.Err = err
		}
		s.Events = evs
		s.Pins, _ = s.Project.Pins()
	}
	opts := sessions.Options{Now: s.Now, All: s.AllProjects}
	if !s.AllProjects {
		p := s.Project
		if p == nil {
			p = &store.Project{Root: s.Root, ID: store.ProjectID(s.Root)}
		}
		opts.Project = p
	}
	list, err := sessions.List(opts)
	if err != nil && s.Err == nil {
		s.Err = err
	}
	s.Sessions = list
	s.Projects, _ = store.ListProjects()
}

// Empty reports that nothing has been recorded for this project.
func (s *Source) Empty() bool {
	return s.Project == nil || len(s.Events) == 0
}

// Session finds a merged session by id.
func (s *Source) Session(id string) (sessions.Session, bool) {
	for _, se := range s.Sessions {
		if se.ID == id {
			return se, true
		}
	}
	return sessions.Session{}, false
}

// EventsOf returns the events of one session, newest first.
func (s *Source) EventsOf(session string) []store.Event {
	var out []store.Event
	for _, ev := range s.Events {
		if ev.Session == session {
			out = append(out, ev)
		}
	}
	return out
}

// Pinned reports whether an event is pinned.
func (s *Source) Pinned(eventID string) bool {
	for _, pin := range s.Pins {
		if pin.Event == eventID {
			return true
		}
	}
	return false
}

// Previous finds the event before ev for the same path, from the loaded set.
func (s *Source) Previous(ev store.Event) (store.Event, bool) {
	for _, cand := range s.Events {
		if cand.Path == ev.Path && cand.ID < ev.ID {
			return cand, true
		}
	}
	return store.Event{}, false
}

// Turn finds a turn's text by id across loaded sessions.
func (s *Source) Turn(id string) (store.Turn, bool) {
	for _, se := range s.Sessions {
		for _, t := range se.Turns {
			if t.ID == id {
				return t, true
			}
		}
	}
	return store.Turn{}, false
}

// SessionsSorted returns sessions in display order: live first, then by
// last activity.
func (s *Source) SessionsSorted() []sessions.Session {
	out := append([]sessions.Session{}, s.Sessions...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Alive != out[j].Alive {
			return out[i].Alive
		}
		return out[i].LastActivity().After(out[j].LastActivity())
	})
	return out
}
