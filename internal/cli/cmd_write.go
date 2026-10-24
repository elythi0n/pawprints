package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/elythi0n/pawprints/internal/export"
	"github.com/elythi0n/pawprints/internal/install"
	"github.com/elythi0n/pawprints/internal/restore"
	"github.com/elythi0n/pawprints/internal/sessions"
	"github.com/elythi0n/pawprints/internal/store"
	"github.com/elythi0n/pawprints/internal/ui"
)

func (c *cli) writeOpts(fs *flagSet) (force, threeWay, dryRun, yes *bool) {
	force = fs.Bool("force", false, "")
	threeWay = fs.Bool("three-way", false, "")
	dryRun = fs.Bool("dry-run", false, "")
	yes = fs.Bool("yes", false, "")
	return
}

type flagSet = flagSetAlias

func (c *cli) printChanges(res restore.Result) {
	for _, ch := range res.Changes {
		var what string
		switch {
		case ch.From == ch.To:
			what = ui.Dim.Render("already there")
		case ch.To == "":
			what = ui.Removed.Render("delete")
		case ch.From == "":
			what = ui.Added.Render("create")
		default:
			what = ui.Accent.Render("rewrite")
		}
		note := ""
		if ch.Modified {
			note = " " + ui.Warn.Render("(file had been edited since)")
		}
		switch {
		case ch.Conflicts > 0 && ch.To == "" || ch.Conflicts > 0 && ch.From == ch.To:
			what = ui.Warn.Render("kept")
			note = " " + ui.Warn.Render("(edited since and the target was deletion; left as is)")
		case ch.Conflicts > 0:
			note += " " + ui.Err.Render(fmt.Sprintf("%d conflicts left as markers", ch.Conflicts))
		}
		fmt.Fprintf(c.out, "  %s %s%s\n", what, ch.Path, note)
	}
}

func (c *cli) reportWrite(res restore.Result, dryRun bool) int {
	if c.json {
		c.emit(res)
		return 0
	}
	if dryRun {
		fmt.Fprintln(c.out, ui.PaneTitle.Render("would "+res.Kind))
	} else {
		fmt.Fprintln(c.out, ui.Ok.Render(res.Kind+" done"))
	}
	c.printChanges(res)
	return 0
}

func (c *cli) cmdRestore(args []string) int {
	fs := newFlags("restore")
	before := fs.Bool("before", false, "")
	force, threeWay, dryRun, yes := c.writeOpts(fs)
	if err := parseArgs(fs, args); err != nil {
		return c.fail(err.Error(), 2)
	}
	if fs.NArg() < 1 {
		return c.fail("usage: pawprints restore <event-id> [--before] [--three-way|--force] [--dry-run] [--yes]", 2)
	}
	p, err := c.openProject(false)
	if err != nil {
		return c.fail(err.Error(), 1)
	}
	ev, err := p.EventByPrefix(fs.Arg(0))
	if err != nil {
		return c.fail(err.Error(), 1)
	}
	which := restore.After
	if *before {
		which = restore.Before
	}
	opts := restore.Options{Force: *force, ThreeWay: *threeWay, DryRun: true}
	plan, err := restore.Restore(p, ev, which, opts)
	if err != nil {
		return c.failErr(err)
	}
	if *dryRun {
		return c.reportWrite(plan, true)
	}
	if !*yes && !c.json {
		c.printChanges(plan)
		if !c.confirm(fmt.Sprintf("restore %s to its %s version?", ev.Path, which)) {
			return c.fail("cancelled", 1)
		}
	}
	opts.DryRun = false
	res, err := restore.Restore(p, ev, which, opts)
	if err != nil {
		return c.failErr(err)
	}
	return c.reportWrite(res, false)
}

func (c *cli) cmdUndo(args []string) int {
	fs := newFlags("undo")
	turn := fs.String("turn", "", "")
	turnSet := false
	session := fs.String("session", "", "")
	force, threeWay, dryRun, yes := c.writeOpts(fs)
	// --turn with no value means the latest turn; flag needs the value, so
	// detect the bare form first.
	cleaned := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		if args[i] == "--turn" && (i+1 >= len(args) || strings.HasPrefix(args[i+1], "-")) {
			turnSet = true
			continue
		}
		if args[i] == "--turn" || strings.HasPrefix(args[i], "--turn=") {
			turnSet = true
		}
		cleaned = append(cleaned, args[i])
	}
	if err := parseArgs(fs, cleaned); err != nil {
		return c.fail(err.Error(), 2)
	}
	p, err := c.openProject(false)
	if err != nil {
		return c.fail(err.Error(), 1)
	}
	scope := restore.UndoScope{}
	label := "the last edit"
	switch {
	case turnSet:
		sid, err := c.resolveSession(p, *session)
		if err != nil {
			return c.fail(err.Error(), 1)
		}
		t, _, err := c.resolveTurn(p, *turn, sid)
		if err != nil {
			return c.fail(err.Error(), 1)
		}
		scope.Turn = t.ID
		label = "turn " + shortID(t.ID)
		if t.Text != "" {
			label += " “" + clip(t.Text, 50) + "”"
		}
	case *session != "":
		sid, err := c.resolveSession(p, *session)
		if err != nil {
			return c.fail(err.Error(), 1)
		}
		scope.Session = sid
		label = "session " + shortID(sid)
	default:
		scope.Last = true
	}
	opts := restore.Options{Force: *force, ThreeWay: *threeWay, DryRun: true}
	plan, err := restore.Undo(p, scope, opts)
	if err != nil {
		return c.failErr(err)
	}
	if *dryRun {
		return c.reportWrite(plan, true)
	}
	if !*yes && !c.json {
		fmt.Fprintf(c.out, "%s %s\n", ui.PaneTitle.Render("undo"), label)
		c.printChanges(plan)
		if !c.confirm(fmt.Sprintf("revert %d file(s)?", len(plan.Changes))) {
			return c.fail("cancelled", 1)
		}
	}
	opts.DryRun = false
	res, err := restore.Undo(p, scope, opts)
	if err != nil {
		return c.failErr(err)
	}
	return c.reportWrite(res, false)
}

func (c *cli) cmdPin(args []string) int {
	fs := newFlags("pin")
	msg := fs.String("m", "", "")
	if err := parseArgs(fs, args); err != nil {
		return c.fail(err.Error(), 2)
	}
	if fs.NArg() < 1 {
		return c.fail("usage: pawprints pin <event-id> [-m message]", 2)
	}
	p, err := c.openProject(false)
	if err != nil {
		return c.fail(err.Error(), 1)
	}
	ev, err := p.EventByPrefix(fs.Arg(0))
	if err != nil {
		return c.fail(err.Error(), 1)
	}
	pin, err := p.AddPin(ev, *msg)
	if err != nil {
		return c.fail(err.Error(), 1)
	}
	if c.json {
		c.emit(pin)
		return 0
	}
	fmt.Fprintf(c.out, "%s %s %s\n", ui.Ok.Render("pinned"), ev.Path, ui.Dim.Render(shortID(ev.ID)))
	return 0
}

func (c *cli) cmdUnpin(args []string) int {
	if len(args) < 1 {
		return c.fail("usage: pawprints unpin <event-id>", 2)
	}
	p, err := c.openProject(false)
	if err != nil {
		return c.fail(err.Error(), 1)
	}
	pin, err := p.RemovePin(args[0])
	if err != nil {
		return c.fail(err.Error(), 1)
	}
	if c.json {
		c.emit(pin)
		return 0
	}
	fmt.Fprintf(c.out, "%s %s %s\n", ui.Ok.Render("unpinned"), pin.Path, ui.Dim.Render(shortID(pin.Event)))
	return 0
}

func (c *cli) cmdExport(args []string) int {
	fs := newFlags("export")
	session := fs.String("session", "", "")
	turn := fs.String("turn", "", "")
	since := fs.String("since", "", "")
	if err := parseArgs(fs, args); err != nil {
		return c.fail(err.Error(), 2)
	}
	if *session == "" && *turn == "" && *since == "" {
		return c.fail("usage: pawprints export --session ID | --turn ID | --since T", 2)
	}
	p, err := c.openProject(false)
	if err != nil {
		return c.fail(err.Error(), 1)
	}
	q := store.Query{Kinds: []string{store.KindEdit, store.KindExternal, store.KindRestore, store.KindUndo}}
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
	if q.Since, err = sinceFlag(*since, time.Now()); err != nil {
		return c.fail(err.Error(), 2)
	}
	events, err := p.Events(q)
	if err != nil {
		return c.failErr(err)
	}
	changes := export.Collect(events)
	patch, err := export.Patch(p, changes)
	if err != nil {
		return c.failErr(err)
	}
	if c.json {
		c.emit(map[string]any{"files": len(changes), "patch": patch})
		return 0
	}
	if patch == "" {
		fmt.Fprintln(c.err, ui.Dim.Render("no net changes in that scope"))
		return 0
	}
	fmt.Fprint(c.out, patch)
	return 0
}

func (c *cli) cmdPrune(_ []string) int {
	p, err := c.openProject(false)
	if err != nil {
		return c.fail(err.Error(), 1)
	}
	cfg, err := store.LoadConfig()
	if err != nil {
		return c.failErr(err)
	}
	rep, err := p.Prune(cfg, time.Now())
	if err != nil {
		return c.failErr(err)
	}
	removed := store.RemoveEmptyProjects(24 * time.Hour)
	if c.json {
		c.emit(rep)
		return 0
	}
	fmt.Fprintf(c.out, "%s %d months, %d pending, %d sessions removed; %s → %s\n",
		ui.Ok.Render("pruned"), rep.MonthsRemoved, rep.PendingRemoved, rep.SessionsRemoved,
		humanBytes(rep.BytesBefore), humanBytes(rep.BytesAfter))
	for _, r := range removed {
		fmt.Fprintf(c.out, "  %s %s\n", ui.Dim.Render("removed empty project"), r)
	}
	return 0
}

func (c *cli) cmdClear(args []string) int {
	fs := newFlags("clear")
	older := fs.String("older-than", "", "")
	session := fs.String("session", "", "")
	project := fs.Bool("project", false, "")
	all := fs.Bool("all", false, "")
	pins := fs.Bool("pins", false, "")
	dryRun := fs.Bool("dry-run", false, "")
	yes := fs.Bool("yes", false, "")
	if err := parseArgs(fs, args); err != nil {
		return c.fail(err.Error(), 2)
	}
	set := 0
	for _, on := range []bool{*older != "", *session != "", *project, *all} {
		if on {
			set++
		}
	}
	if set != 1 {
		return c.fail("usage: pawprints clear --older-than 3m | --session ID | --project | --all [--pins] [--dry-run] [--yes]", 2)
	}
	now := time.Now()
	if *all {
		sum, err := store.PlanClearAll()
		if err != nil {
			return c.failErr(err)
		}
		if *dryRun {
			return c.reportClear(sum, true)
		}
		if !*yes && !c.json {
			c.printClearSummary(sum)
			if !c.confirm("delete history for every project?") {
				return c.fail("cancelled", 1)
			}
		}
		sum, err = store.ClearAll()
		if err != nil {
			return c.failErr(err)
		}
		return c.reportClear(sum, false)
	}
	p, err := c.openProject(false)
	if err != nil {
		return c.fail(err.Error(), 1)
	}
	scope := store.ClearScope{Project: *project, DropPins: *pins}
	if *older != "" {
		d, err := parseSpan(*older)
		if err != nil {
			return c.fail(err.Error(), 2)
		}
		scope.OlderThan = d
	}
	if *session != "" {
		if scope.Session, err = c.resolveSession(p, *session); err != nil {
			return c.fail(err.Error(), 1)
		}
	}
	sum, err := p.PlanClear(scope, now)
	if err != nil {
		return c.failErr(err)
	}
	if *dryRun {
		return c.reportClear(sum, true)
	}
	if sum.Events == 0 && sum.Sessions == 0 {
		if c.json {
			c.emit(sum)
			return 0
		}
		fmt.Fprintln(c.out, ui.Dim.Render("nothing to clear for "+scope.Describe()))
		return 0
	}
	if !*yes && !c.json {
		c.printClearSummary(sum)
		if !c.confirm("delete " + scope.Describe() + "?") {
			return c.fail("cancelled", 1)
		}
	}
	sum, err = p.Clear(scope, now)
	if err != nil {
		return c.failErr(err)
	}
	return c.reportClear(sum, false)
}

func (c *cli) printClearSummary(sum store.ClearSummary) {
	fmt.Fprintf(c.out, "%s %s\n", ui.PaneTitle.Render("clear"), sum.Scope)
	if !sum.CutAt.IsZero() {
		fmt.Fprintf(c.out, "  %s everything before %s\n", ui.Dim.Render("cut"), sum.CutAt.Format("2006-01-02"))
	}
	fmt.Fprintf(c.out, "  %d sessions, %d events, %d files, about %s\n", sum.Sessions, sum.Events, sum.Files, humanBytes(sum.Bytes))
	if sum.KeptPins > 0 {
		fmt.Fprintf(c.out, "  %d pinned versions kept (pass --pins to drop them)\n", sum.KeptPins)
	}
}

func (c *cli) reportClear(sum store.ClearSummary, dryRun bool) int {
	if c.json {
		c.emit(sum)
		return 0
	}
	if dryRun {
		c.printClearSummary(sum)
		fmt.Fprintln(c.out, ui.Dim.Render("dry run, nothing removed"))
		return 0
	}
	fmt.Fprintf(c.out, "%s %s: %d sessions, %d events, %d files\n", ui.Ok.Render("cleared"), sum.Scope, sum.Sessions, sum.Events, sum.Files)
	return 0
}

func (c *cli) cmdInstall(_ []string) int {
	bin, err := install.BinaryPath()
	if err != nil {
		return c.failErr(err)
	}
	st, err := install.Install(bin)
	if err != nil {
		return c.fail(err.Error(), 1)
	}
	if c.json {
		c.emit(st)
		return 0
	}
	fmt.Fprintf(c.out, "%s hooks registered in %s\n", ui.Ok.Render("installed"), st.SettingsPath)
	fmt.Fprintln(c.out, ui.Dim.Render("new Claude Code sessions will be recorded; running ones pick it up on their next tool call"))
	return 0
}

func (c *cli) cmdUninstall(_ []string) int {
	st, err := install.Uninstall()
	if err != nil {
		return c.fail(err.Error(), 1)
	}
	if c.json {
		c.emit(st)
		return 0
	}
	fmt.Fprintf(c.out, "%s hooks removed from %s\n", ui.Ok.Render("uninstalled"), st.SettingsPath)
	return 0
}

// cmdMaintain is what the plugin runs on SessionStart: prune when due.
func (c *cli) cmdMaintain(_ []string) int {
	p, ok := store.FindProjectFor(c.root())
	if !ok {
		return 0
	}
	if !p.PruneDue(24*time.Hour, time.Now()) {
		return 0
	}
	cfg, err := store.LoadConfig()
	if err != nil {
		return 0
	}
	if _, err := p.Prune(cfg, time.Now()); err != nil && !errors.Is(err, os.ErrNotExist) {
		fmt.Fprintln(c.err, "pawprints maintain:", err)
	}
	store.RemoveEmptyProjects(24 * time.Hour)
	return 0
}

// cmdRegroup repairs how older versions grouped history: it moves edits
// recorded under the wrong project, which happened when Claude changed
// directory mid-session; drops changes outside Claude recorded in files
// another project owns, which were often another session's work; and folds
// subagent reports and repeated prompts back into the prompt they belong
// to, with each subagent's changes under the prompt that started it.
func (c *cli) cmdRegroup(args []string) int {
	fs := newFlags("regroup")
	dryRun := fs.Bool("dry-run", false, "")
	if err := parseArgs(fs, args); err != nil {
		return c.fail(err.Error(), 2)
	}
	rep, err := store.Regroup(sessions.ProjectRoots(), *dryRun)
	if err != nil {
		return c.failErr(err)
	}
	untangled, err := store.Untangle(*dryRun)
	if err != nil {
		return c.failErr(err)
	}
	folded, err := store.FoldTurns(*dryRun)
	if err != nil {
		return c.failErr(err)
	}
	filled, err := sessions.FillAgents(*dryRun)
	if err != nil {
		return c.failErr(err)
	}
	if c.json {
		c.emit(struct {
			store.RegroupReport
			Untangle store.UntangleReport `json:"untangle"`
			Turns    store.FoldReport     `json:"turns"`
			Agents   int                  `json:"agents_filled"`
		}{rep, untangled, folded, filled})
		return 0
	}
	if rep.Events == 0 && untangled.Events == 0 && untangled.Roots == 0 && folded.Turns == 0 && folded.Events == 0 && filled == 0 {
		fmt.Fprintln(c.out, ui.Ok.Render("nothing to regroup")+" "+ui.Dim.Render("every edit is in its session's project"))
		return 0
	}
	verb := ui.Ok.Render("moved")
	dropped := ui.Ok.Render("dropped")
	if *dryRun {
		verb, dropped = ui.PaneTitle.Render("would move"), ui.PaneTitle.Render("would drop")
	}
	if rep.Events > 0 {
		if *dryRun {
			fmt.Fprintf(c.out, "%s %d edits into %s\n", verb, rep.Events, strings.Join(rep.Into, ", "))
		} else {
			fmt.Fprintf(c.out, "%s %d edits and %d session records into %s\n", verb, rep.Events, rep.Sessions, strings.Join(rep.Into, ", "))
		}
		for _, r := range rep.Removed {
			fmt.Fprintf(c.out, "  %s %s\n", ui.Dim.Render("removed empty"), r)
		}
	}
	if untangled.Events > 0 {
		fmt.Fprintf(c.out, "%s %d changes marked outside Claude in files another project owns\n", dropped, untangled.Events)
	}
	if untangled.Roots > 0 {
		fmt.Fprintf(c.out, "%s %d scan folders that held whole projects\n", dropped, untangled.Roots)
	}
	if folded.Turns > 0 || folded.Events > 0 {
		fold := ui.Ok.Render("folded")
		if *dryRun {
			fold = ui.PaneTitle.Render("would fold")
		}
		fmt.Fprintf(c.out, "%s %d subagent reports and repeated prompts into their prompt; %d changes now sit under the prompt that made them\n", fold, folded.Turns, folded.Events)
	}
	if filled > 0 {
		fill := ui.Ok.Render("named")
		if *dryRun {
			fill = ui.PaneTitle.Render("would name")
		}
		fmt.Fprintf(c.out, "%s %d subagents with their task and report\n", fill, filled)
	}
	return 0
}
