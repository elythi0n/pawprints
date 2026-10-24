package store

// UntangleReport says what Untangle removed.
type UntangleReport struct {
	Events int `json:"events"`
	Roots  int `json:"scan_roots"`
}

// Untangle removes what older versions recorded across project lines: scan
// folders that held whole projects, such as their common parent, and the
// changes outside Claude they recorded in files the project does not own.
// Those were often another Claude session's work, which that session
// recorded in its own project.
func Untangle(dryRun bool) (UntangleReport, error) {
	var rep UntangleReport
	projects, err := ListProjects()
	if err != nil {
		return rep, err
	}
	roots := KnownRoots()
	for _, p := range projects {
		foreign := func(ev Event) bool {
			return ev.Kind == KindExternal && Owner(roots, p.Abs(ev.Path)) != p.Root
		}
		external, err := p.Events(Query{Kinds: []string{KindExternal}})
		if err != nil {
			return rep, err
		}
		dropped := map[string][]Event{} // session → its dropped events
		for _, ev := range external {
			if foreign(ev) {
				dropped[ev.Session] = append(dropped[ev.Session], ev)
				rep.Events++
			}
		}
		recs, err := p.Sessions()
		if err != nil {
			return rep, err
		}
		broad := map[string][]string{} // session → scan folders to drop
		for _, r := range recs {
			for _, d := range r.ExtraRoots {
				if HoldsProjects(d, p.Root, roots) {
					broad[r.Session] = append(broad[r.Session], d)
					rep.Roots++
				}
			}
		}
		if dryRun || (len(dropped) == 0 && len(broad) == 0) {
			continue
		}
		if len(dropped) > 0 {
			if err := p.rewriteEvents(func(ev Event) bool { return !foreign(ev) }); err != nil {
				return rep, err
			}
		}
		left, err := p.sessionPaths(dropped)
		if err != nil {
			return rep, err
		}
		for _, r := range recs {
			evs, bad := dropped[r.Session], broad[r.Session]
			if len(evs) == 0 && len(bad) == 0 {
				continue
			}
			if err := p.UpdateSession(r.Session, func(r *SessionRecord) {
				for _, ev := range evs {
					r.Edits--
					r.LinesAdded -= ev.LinesAdded
					r.LinesRemoved -= ev.LinesRemoved
				}
				if len(evs) > 0 {
					r.Files = keepIf(r.Files, func(f string) bool { return left[r.Session][f] })
				}
				r.ExtraRoots = keepIf(r.ExtraRoots, func(d string) bool { return !contains(bad, d) })
			}); err != nil {
				return rep, err
			}
		}
	}
	return rep, nil
}

// sessionPaths lists, for each of the given sessions, the paths it still
// has events for.
func (p *Project) sessionPaths(sessions map[string][]Event) (map[string]map[string]bool, error) {
	out := map[string]map[string]bool{}
	if len(sessions) == 0 {
		return out, nil
	}
	evs, err := p.Events(Query{})
	if err != nil {
		return nil, err
	}
	for _, ev := range evs {
		if _, ok := sessions[ev.Session]; !ok {
			continue
		}
		if out[ev.Session] == nil {
			out[ev.Session] = map[string]bool{}
		}
		out[ev.Session][ev.Path] = true
	}
	return out, nil
}

func keepIf(list []string, keep func(string) bool) []string {
	var out []string
	for _, s := range list {
		if keep(s) {
			out = append(out, s)
		}
	}
	return out
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
