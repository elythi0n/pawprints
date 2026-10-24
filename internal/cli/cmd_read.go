package cli

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/elythi0n/pawprints/internal/buildinfo"
	"github.com/elythi0n/pawprints/internal/diff"
	"github.com/elythi0n/pawprints/internal/install"
	"github.com/elythi0n/pawprints/internal/sessions"
	"github.com/elythi0n/pawprints/internal/store"
	"github.com/elythi0n/pawprints/internal/ui"
	"github.com/elythi0n/pawprints/internal/usage"
)

// eventOut is an event as the JSON contract prints it.
type eventOut struct {
	store.Event
	ProjectRoot string `json:"project_root"`
	Pinned      bool   `json:"pinned,omitempty"`
}

// resolveSession turns a --session value into an id. "current" picks the
// live session for the project.
func (c *cli) resolveSession(p *store.Project, ref string) (string, error) {
	if ref == "" {
		return "", nil
	}
	s, ok := sessions.Resolve(p, ref)
	if !ok {
		return "", fmt.Errorf("no session matches %q", ref)
	}
	return s.ID, nil
}

// resolveTurn finds a turn by id or prefix across the project's sessions.
// An empty ref with a session means that session's latest turn.
func (c *cli) resolveTurn(p *store.Project, ref, session string) (store.Turn, string, error) {
	recs, err := p.Sessions()
	if err != nil {
		return store.Turn{}, "", err
	}
	if ref == "" {
		if session == "" {
			cur, ok := sessions.Current(p)
			if !ok {
				return store.Turn{}, "", fmt.Errorf("no session to take the latest turn from")
			}
			session = cur.ID
		}
		rec, ok := p.LoadSession(session)
		if !ok || len(rec.Turns) == 0 {
			return store.Turn{}, "", fmt.Errorf("session %s has no recorded turns", shortID(session))
		}
		return rec.Turns[len(rec.Turns)-1], session, nil
	}
	ref = strings.ToUpper(ref)
	var found []store.Turn
	var foundSession string
	for _, rec := range recs {
		for _, t := range rec.Turns {
			if t.ID == ref {
				return t, rec.Session, nil
			}
			if strings.HasPrefix(t.ID, ref) {
				found = append(found, t)
				foundSession = rec.Session
			}
		}
	}
	switch len(found) {
	case 1:
		return found[0], foundSession, nil
	case 0:
		return store.Turn{}, "", fmt.Errorf("no turn matches %q", ref)
	}
	return store.Turn{}, "", fmt.Errorf("%q matches %d turns; use a longer prefix", ref, len(found))
}

func (c *cli) cmdLog(args []string) int {
	fs := newFlags("log")
	limit := fs.Int("limit", 50, "")
	session := fs.String("session", "", "")
	turn := fs.String("turn", "", "")
	path := fs.String("path", "", "")
	since := fs.String("since", "", "")
	kind := fs.String("kind", "", "")
	all := fs.Bool("all-events", false, "")
	if err := parseArgs(fs, args); err != nil {
		return c.fail(err.Error(), 2)
	}
	p, err := c.openProject(false)
	if err != nil {
		if c.json {
			return 0
		}
		return c.fail(err.Error(), 1)
	}
	now := time.Now()
	q := store.Query{Path: *path, Limit: *limit}
	if *all {
		q.Limit = 0
	}
	if q.Session, err = c.resolveSession(p, *session); err != nil {
		return c.fail(err.Error(), 1)
	}
	if *turn != "" {
		t, _, err := c.resolveTurn(p, *turn, q.Session)
		if err != nil {
			return c.fail(err.Error(), 1)
		}
		q.Turn = t.ID
	}
	if q.Since, err = sinceFlag(*since, now); err != nil {
		return c.fail(err.Error(), 2)
	}
	if *kind != "" {
		q.Kinds = strings.Split(*kind, ",")
	}
	events, err := p.Events(q)
	if err != nil {
		return c.failErr(err)
	}
	if c.json {
		for _, ev := range events {
			c.emit(eventOut{Event: ev, ProjectRoot: p.Root, Pinned: p.IsPinned(ev.ID)})
		}
		return 0
	}
	if len(events) == 0 {
		fmt.Fprintln(c.out, ui.Dim.Render("no edits recorded"))
		return 0
	}
	c.printEvents(events, now)
	return 0
}

func (c *cli) printEvents(events []store.Event, now time.Time) {
	w := tabwriter.NewWriter(c.out, 0, 0, 2, ' ', 0)
	lastDay := ""
	for _, ev := range events {
		t := ev.Time().Local()
		day := t.Format("Mon 2 Jan")
		if day != lastDay {
			if lastDay != "" {
				fmt.Fprintln(w)
			}
			fmt.Fprintln(w, ui.PaneTitle.Render(day))
			lastDay = day
		}
		fmt.Fprintf(w, "  %s\t%s\t%s\t%s\t%s\t%s\n",
			ui.Dim.Render(shortID(ev.ID)),
			t.Format("15:04:05"),
			toolLabel(ev),
			ev.Path,
			lineStats(ev),
			ui.Dim.Render(ago(t, now)),
		)
	}
	w.Flush()
}

func toolLabel(ev store.Event) string {
	switch ev.Kind {
	case store.KindExternal:
		return ui.Warn.Render(ev.Label())
	case store.KindRestore, store.KindUndo, store.KindBackground:
		return ui.Accent.Render(ev.Label())
	}
	return ui.Soft.Render(ev.Label())
}

func lineStats(ev store.Event) string {
	if ev.Skipped != "" {
		return ui.Dim.Render("skipped: " + ev.Skipped)
	}
	if ev.Binary {
		return ui.Dim.Render("binary")
	}
	if ev.Unchanged() {
		return ui.Dim.Render("unchanged")
	}
	parts := []string{}
	switch {
	case ev.BeforeUnknown:
		parts = append(parts, ui.Dim.Render("first seen"))
	case ev.Before == nil:
		parts = append(parts, ui.Added.Render("new"))
	}
	if ev.After == nil {
		parts = append(parts, ui.Removed.Render("deleted"))
	}
	if ev.LinesAdded > 0 || ev.LinesRemoved > 0 {
		parts = append(parts, ui.Added.Render(fmt.Sprintf("+%d", ev.LinesAdded)), ui.Removed.Render(fmt.Sprintf("-%d", ev.LinesRemoved)))
	}
	return strings.Join(parts, " ")
}

// loadVersion returns the content of a blob, tolerating "" as empty.
func loadVersion(p *store.Project, hash string) ([]byte, error) {
	if hash == "" {
		return nil, nil
	}
	return p.Get(hash)
}

// renderDiff produces the diff text for a and b, through the external
// differ when one is available and output is a terminal.
func (c *cli) renderDiff(path string, aLabel, bLabel string, a, b []byte) string {
	if store.IsBinary(a) || store.IsBinary(b) {
		return ui.Dim.Render("binary content, no text diff") + "\n"
	}
	unified := diff.Unified(aLabel, bLabel, a, b, 3)
	if unified == "" {
		return ui.Dim.Render("no differences") + "\n"
	}
	if c.color {
		if name := diff.External(); name != "" {
			if out, ok := diff.RenderExternal(name, path, a, b, unified, termWidth()); ok {
				return out
			}
		}
	}
	return diff.Render(unified, c.color)
}

func termWidth() int {
	if w := os.Getenv("COLUMNS"); w != "" {
		var n int
		if _, err := fmt.Sscanf(w, "%d", &n); err == nil && n > 0 {
			return n
		}
	}
	return 120
}

func (c *cli) cmdShow(args []string) int {
	if len(args) < 1 {
		return c.fail("usage: pawprints show <event-id>", 2)
	}
	p, err := c.openProject(false)
	if err != nil {
		return c.fail(err.Error(), 1)
	}
	ev, err := p.EventByPrefix(args[0])
	if err != nil {
		return c.fail(err.Error(), 1)
	}
	before, err := loadVersion(p, ev.BeforeHash())
	if err != nil {
		return c.failErr(err)
	}
	after, err := loadVersion(p, ev.AfterHash())
	if err != nil {
		return c.failErr(err)
	}
	if c.json {
		c.emit(map[string]any{
			"event":        eventOut{Event: ev, ProjectRoot: p.Root, Pinned: p.IsPinned(ev.ID)},
			"before":       string(before),
			"after":        string(after),
			"diff":         diff.Unified("a/"+ev.Path, "b/"+ev.Path, before, after, 3),
			"project_root": p.Root,
		})
		return 0
	}
	c.printEventHeader(p, ev)
	fmt.Fprint(c.out, c.renderDiff(ev.Path, "a/"+ev.Path, "b/"+ev.Path, before, after))
	return 0
}

func (c *cli) printEventHeader(p *store.Project, ev store.Event) {
	t := ev.Time().Local()
	fmt.Fprintf(c.out, "%s %s\n", ui.PaneTitle.Render(ev.Path), ui.Dim.Render(ev.ID))
	fmt.Fprintf(c.out, "  %s %s   %s %s   %s %s\n",
		ui.Dim.Render("when"), t.Format("2006-01-02 15:04:05"),
		ui.Dim.Render("tool"), toolLabel(ev),
		ui.Dim.Render("session"), shortID(ev.Session))
	extra := []string{}
	if ev.Turn != "" {
		extra = append(extra, ui.Dim.Render("turn")+" "+shortID(ev.Turn))
	}
	extra = append(extra, ui.Dim.Render("before")+" "+hashLabel(ev.BeforeHash()), ui.Dim.Render("after")+" "+hashLabel(ev.AfterHash()))
	if p.IsPinned(ev.ID) {
		extra = append(extra, ui.Badge.Render("pinned"))
	}
	fmt.Fprintf(c.out, "  %s\n\n", strings.Join(extra, "   "))
}

func hashLabel(h string) string {
	if h == "" {
		return ui.Dim.Render("none")
	}
	if len(h) > 7 {
		return h[:7]
	}
	return h
}

func (c *cli) cmdDiff(args []string) int {
	fs := newFlags("diff")
	against := fs.String("against", "previous", "")
	from := fs.String("from", "", "")
	if err := parseArgs(fs, args); err != nil {
		return c.fail(err.Error(), 2)
	}
	if fs.NArg() < 1 {
		return c.fail("usage: pawprints "+usages["diff"], 2)
	}
	p, err := c.openProject(false)
	if err != nil {
		return c.fail(err.Error(), 1)
	}
	ev, err := p.EventByPrefix(fs.Arg(0))
	if err != nil {
		return c.fail(err.Error(), 1)
	}
	after, err := loadVersion(p, ev.AfterHash())
	if err != nil {
		return c.failErr(err)
	}
	var base []byte
	var aLabel, bLabel string
	switch {
	case *from != "":
		// Several edits as one change: from before the first to after this one.
		first, ferr := p.EventByPrefix(*from)
		if ferr != nil {
			return c.fail(ferr.Error(), 1)
		}
		if first.Path != ev.Path {
			return c.fail(fmt.Sprintf("--from %s changed %s, not %s", shortID(first.ID), first.Path, ev.Path), 2)
		}
		if first.Time().After(ev.Time()) {
			return c.fail(fmt.Sprintf("--from %s is newer than %s; give the earlier edit to --from", shortID(first.ID), shortID(ev.ID)), 2)
		}
		base, err = loadVersion(p, first.BeforeHash())
		aLabel, bLabel = "before/"+ev.Path, shortID(ev.ID)+"/"+ev.Path
		*against = "from:" + first.ID
	case *against == "previous":
		base, err = loadVersion(p, ev.BeforeHash())
		aLabel, bLabel = "previous/"+ev.Path, shortID(ev.ID)+"/"+ev.Path
	case *against == "current":
		base = after
		after, err = os.ReadFile(p.Abs(ev.Path))
		if os.IsNotExist(err) {
			after, err = nil, nil
		}
		aLabel, bLabel = shortID(ev.ID)+"/"+ev.Path, "current/"+ev.Path
	default:
		other, oerr := p.EventByPrefix(*against)
		if oerr != nil {
			return c.fail(oerr.Error(), 1)
		}
		base, err = loadVersion(p, other.AfterHash())
		aLabel, bLabel = shortID(other.ID)+"/"+ev.Path, shortID(ev.ID)+"/"+ev.Path
	}
	if err != nil {
		return c.failErr(err)
	}
	unified := diff.Unified(aLabel, bLabel, base, after, 3)
	if c.json {
		c.emit(map[string]any{"event": ev.ID, "against": *against, "diff": unified, "stats": diff.Count(base, after)})
		return 0
	}
	fmt.Fprint(c.out, c.renderDiff(ev.Path, aLabel, bLabel, base, after))
	return 0
}

func (c *cli) cmdHunks(args []string) int {
	fs := newFlags("hunks")
	against := fs.String("against", "previous", "")
	if err := parseArgs(fs, args); err != nil {
		return c.fail(err.Error(), 2)
	}
	if fs.NArg() < 1 {
		return c.fail("usage: pawprints hunks <event-id> [--against previous|current] --json", 2)
	}
	p, err := c.openProject(false)
	if err != nil {
		return c.fail(err.Error(), 1)
	}
	ev, err := p.EventByPrefix(fs.Arg(0))
	if err != nil {
		return c.fail(err.Error(), 1)
	}
	var a, b []byte
	if *against == "current" {
		a, _ = loadVersion(p, ev.AfterHash())
		b, _ = os.ReadFile(p.Abs(ev.Path))
	} else {
		a, _ = loadVersion(p, ev.BeforeHash())
		b, _ = loadVersion(p, ev.AfterHash())
	}
	type hunkOut struct {
		OldStart int `json:"old_start"`
		OldLines int `json:"old_lines"`
		NewStart int `json:"new_start"`
		NewLines int `json:"new_lines"`
		Added    int `json:"added"`
		Removed  int `json:"removed"`
	}
	var out []hunkOut
	for _, h := range diff.Hunks(a, b, 0) {
		ho := hunkOut{OldStart: h.OldStart, OldLines: h.OldLines, NewStart: h.NewStart, NewLines: h.NewLines}
		for _, op := range h.Ops {
			switch op.Kind {
			case '+':
				ho.Added++
			case '-':
				ho.Removed++
			}
		}
		out = append(out, ho)
	}
	if out == nil {
		out = []hunkOut{}
	}
	c.emit(map[string]any{"event": ev.ID, "path": ev.Path, "hunks": out})
	return 0
}

func (c *cli) cmdCat(args []string) int {
	fs := newFlags("cat")
	before := fs.Bool("before", false, "")
	if err := parseArgs(fs, args); err != nil {
		return c.fail(err.Error(), 2)
	}
	if fs.NArg() < 1 {
		return c.fail("usage: pawprints cat <hash|event-id> [--before]", 2)
	}
	p, err := c.openProject(false)
	if err != nil {
		return c.fail(err.Error(), 1)
	}
	ref := fs.Arg(0)
	hash := ref
	if ev, err := p.EventByPrefix(ref); err == nil {
		hash = ev.AfterHash()
		if *before {
			hash = ev.BeforeHash()
		}
	}
	content, err := p.Get(hash)
	if err != nil {
		return c.fail(err.Error(), 1)
	}
	_, _ = c.out.Write(content)
	return 0
}

func (c *cli) cmdSessions(args []string) int {
	fs := newFlags("sessions")
	live := fs.Bool("live", false, "")
	all := fs.Bool("all", false, "")
	since := fs.String("since", "", "")
	if err := parseArgs(fs, args); err != nil {
		return c.fail(err.Error(), 2)
	}
	now := time.Now()
	opts := sessions.Options{All: *all, Live: *live, Now: now}
	if opts.Since, _ = sinceFlag(*since, now); *since != "" && opts.Since.IsZero() {
		return c.fail("cannot read --since "+*since, 2)
	}
	if !*all {
		p, ok := store.FindProjectFor(c.root())
		if !ok {
			// No store yet; the registry may still know about live sessions here.
			p = &store.Project{Root: c.root(), ID: store.ProjectID(c.root())}
		}
		opts.Project = p
	}
	list, err := sessions.List(opts)
	if err != nil {
		return c.failErr(err)
	}
	if c.json {
		for _, s := range list {
			c.emit(s)
		}
		return 0
	}
	if len(list) == 0 {
		fmt.Fprintln(c.out, ui.Dim.Render("no sessions"))
		return 0
	}
	c.printSessions(list, now, *all)
	return 0
}

func statusDot(s sessions.Session) string {
	switch {
	case s.Touching:
		return ui.DotBusy.Render("◉")
	case s.Status == sessions.StatusBusy:
		return ui.DotBusy.Render("●")
	case s.Status == sessions.StatusWaiting:
		return ui.DotWaiting.Render("◐")
	case s.Status == sessions.StatusIdle:
		return ui.DotIdle.Render("○")
	case s.Status == sessions.StatusEnded:
		return ui.DotEnded.Render("·")
	}
	return ui.DotIdle.Render("?")
}

func (c *cli) printSessions(list []sessions.Session, now time.Time, showProject bool) {
	w := tabwriter.NewWriter(c.out, 0, 0, 2, ' ', 0)
	for _, s := range list {
		status := s.Status
		if s.Touching {
			status = "touching"
		}
		if s.WaitingFor != "" {
			status += ": " + s.WaitingFor
		}
		proj := ""
		if showProject {
			proj = ui.Dim.Render(shortPath(s.Project))
		}
		fmt.Fprintf(w, "%s %s\t%s\t%s\t%s\t%s\t%s\n",
			statusDot(s), ui.Strong.Render(s.Name), ui.Soft.Render(status), proj,
			fmt.Sprintf("%d files", len(s.Files)), fmt.Sprintf("%d edits", s.Edits),
			ui.Dim.Render(ago(s.LastActivity(), now)))
		if s.LastPrompt != "" {
			fmt.Fprintf(w, "    %s\t\t\t\t\t%s\n", ui.Dim.Render("“"+clip(s.LastPrompt, 70)+"”"), ui.Dim.Render(shortID(s.ID)))
		} else {
			fmt.Fprintf(w, "    %s\t\t\t\t\t\n", ui.Dim.Render(shortID(s.ID)))
		}
	}
	w.Flush()
}

func shortPath(p string) string {
	home, err := os.UserHomeDir()
	if err == nil && strings.HasPrefix(p, home) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

func (c *cli) cmdTurns(args []string) int {
	fs := newFlags("turns")
	session := fs.String("session", "current", "")
	if err := parseArgs(fs, args); err != nil {
		return c.fail(err.Error(), 2)
	}
	p, err := c.openProject(false)
	if err != nil {
		return c.fail(err.Error(), 1)
	}
	id, err := c.resolveSession(p, *session)
	if err != nil {
		return c.fail(err.Error(), 1)
	}
	rec, ok := p.LoadSession(id)
	if !ok {
		return c.fail("no record for session "+shortID(id), 1)
	}
	events, _ := p.Events(store.Query{Session: id})
	type turnOut struct {
		store.Turn
		Edits int      `json:"edits"`
		Files []string `json:"files"`
		// Tokens is what the prompt spent, counted toward limits, from the
		// per-minute usage log; zero before metrics existed.
		Tokens  int64   `json:"tokens"`
		APICost float64 `json:"api_cost"`
	}
	buckets, _ := p.SessionUsage(id)
	cfg, _ := store.LoadConfig()
	turnTokens := map[string]int64{}
	turnCost := map[string]float64{}
	for _, b := range buckets {
		// A subagent's tokens belong to the prompt that started it, as its
		// edits do; the main conversation's to the prompt running then.
		t := rec.TurnAt(store.Now(b.At().Add(59 * time.Second)))
		if _, known := rec.AgentByID(b.Agent); known {
			t = rec.TurnFor(b.Agent)
		}
		turnTokens[t] += b.Counted()
		c, _ := usage.APICost(b.Model, b.Usage, cfg.Prices)
		turnCost[t] += c
	}
	var out []turnOut
	for _, t := range rec.Turns {
		to := turnOut{Turn: t, Files: []string{}, Tokens: turnTokens[t.ID], APICost: turnCost[t.ID]}
		seen := map[string]bool{}
		for _, ev := range events {
			if ev.Turn == t.ID {
				to.Edits++
				if !seen[ev.Path] {
					seen[ev.Path] = true
					to.Files = append(to.Files, ev.Path)
				}
			}
		}
		sort.Strings(to.Files)
		out = append(out, to)
	}
	if c.json {
		for _, t := range out {
			c.emit(t)
		}
		return 0
	}
	now := time.Now()
	for i := len(out) - 1; i >= 0; i-- {
		t := out[i]
		at, _ := time.Parse(time.RFC3339Nano, t.At)
		text := t.Text
		if text == "" {
			text = ui.Dim.Render("(prompt not recorded)")
		}
		fmt.Fprintf(c.out, "%s %s  %s\n", ui.Dim.Render(shortID(t.ID)), ui.Strong.Render(clip(text, 80)), ui.Dim.Render(ago(at.Local(), now)))
		if t.Edits > 0 {
			fmt.Fprintf(c.out, "    %d edits in %s\n", t.Edits, ui.Soft.Render(strings.Join(t.Files, ", ")))
		}
	}
	return 0
}

func (c *cli) cmdPins(_ []string) int {
	p, err := c.openProject(false)
	if err != nil {
		return c.fail(err.Error(), 1)
	}
	pins, err := p.Pins()
	if err != nil {
		return c.failErr(err)
	}
	if c.json {
		for _, pin := range pins {
			c.emit(pin)
		}
		return 0
	}
	if len(pins) == 0 {
		fmt.Fprintln(c.out, ui.Dim.Render("no pins; `pawprints pin <event> -m \"why\"` keeps a version forever"))
		return 0
	}
	now := time.Now()
	w := tabwriter.NewWriter(c.out, 0, 0, 2, ' ', 0)
	for _, pin := range pins {
		at, _ := time.Parse(time.RFC3339Nano, pin.At)
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", ui.Dim.Render(shortID(pin.Event)), pin.Path, ui.Accent.Render(pin.Message), ui.Dim.Render(ago(at.Local(), now)))
	}
	w.Flush()
	return 0
}

func (c *cli) cmdInfo(_ []string) int {
	root := c.root()
	p, tracked := store.FindProjectFor(root)
	st := install.Check()
	registry, _ := sessions.ReadRegistry()
	live := 0
	for _, r := range registry {
		if r.Alive {
			live++
		}
	}
	out := map[string]any{
		"version":          buildinfo.Version,
		"home":             store.Home(),
		"project_root":     root,
		"project_id":       store.ProjectID(root),
		"tracked":          tracked,
		"hook_settings":    st.SettingsHooks,
		"hook_plugin":      st.PluginInstalled,
		"settings_path":    st.SettingsPath,
		"registry_dir":     sessions.RegistryDir(),
		"registry_entries": len(registry),
		"registry_live":    live,
		"differ":           diff.External(),
	}
	if tracked {
		out["project_dir"] = p.Dir
		out["project_bytes"] = p.Size()
		months, _ := p.Months()
		out["months"] = months
	}
	if c.json {
		c.emit(out)
		return 0
	}
	fmt.Fprintln(c.out, ui.Title.Render(" pawprints ")+" "+ui.Dim.Render(buildinfo.Version+" ("+buildinfo.Commit+", "+buildinfo.BuildTime+")"))
	rows := [][2]string{
		{"home", store.Home()},
		{"project", root},
		{"project id", store.ProjectID(root)},
	}
	if tracked {
		rows = append(rows, [2]string{"history", fmt.Sprintf("%s in %s", humanBytes(p.Size()), p.Dir)})
	} else {
		rows = append(rows, [2]string{"history", ui.Dim.Render("none yet")})
	}
	hookState := ui.Err.Render("not registered")
	switch {
	case st.PluginInstalled:
		hookState = ui.Ok.Render("plugin")
	case st.SettingsHooks:
		hookState = ui.Ok.Render("settings.json")
	}
	rows = append(rows,
		[2]string{"hooks", hookState},
		[2]string{"live sessions", fmt.Sprintf("%d of %d registry entries", live, len(registry))},
	)
	if d := diff.External(); d != "" {
		rows = append(rows, [2]string{"differ", d})
	} else {
		rows = append(rows, [2]string{"differ", ui.Dim.Render("built-in (install delta or difft for richer diffs)")})
	}
	for _, r := range rows {
		fmt.Fprintf(c.out, "  %-14s %s\n", ui.Dim.Render(r[0]), r[1])
	}
	return 0
}
