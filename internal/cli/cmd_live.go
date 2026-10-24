package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/elythi0n/pawprints/internal/completion"
	"github.com/elythi0n/pawprints/internal/install"
	"github.com/elythi0n/pawprints/internal/sessions"
	"github.com/elythi0n/pawprints/internal/stats"
	"github.com/elythi0n/pawprints/internal/store"
	"github.com/elythi0n/pawprints/internal/ui"
	"github.com/elythi0n/pawprints/internal/watch"
)

func (c *cli) cmdWatch(args []string) int {
	fs := newFlags("watch")
	all := fs.Bool("all", false, "")
	if err := parseArgs(fs, args); err != nil {
		return c.fail(err.Error(), 2)
	}
	opts := watch.Options{All: *all}
	if !*all {
		p, err := c.openProject(true)
		if err != nil {
			return c.failErr(err)
		}
		opts.Projects = []*store.Project{p}
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	ch := make(chan watch.Change, 64)
	go func() { _ = watch.New(opts).Run(ctx, ch) }()
	now := time.Now
	for change := range ch {
		if c.json {
			c.emit(change)
			continue
		}
		switch change.Kind {
		case watch.KindEvent:
			ev := change.Event
			fmt.Fprintf(c.out, "%s %s %s %s %s\n", ui.Dim.Render(ev.Time().Local().Format("15:04:05")), toolLabel(*ev), ev.Path, lineStats(*ev), ui.Dim.Render(shortID(ev.Session)))
		case watch.KindSession:
			s := change.Session
			status := s.Status
			if s.WaitingFor != "" {
				status += ": " + s.WaitingFor
			}
			fmt.Fprintf(c.out, "%s %s %s %s\n", ui.Dim.Render(now().Format("15:04:05")), statusDot(*s), ui.Strong.Render(s.Name), ui.Soft.Render(status))
		}
	}
	return 0
}

func (c *cli) cmdStats(args []string) int {
	fs := newFlags("stats")
	since := fs.String("since", "", "")
	all := fs.Bool("all", false, "")
	daily := fs.Bool("daily", false, "")
	top := fs.Int("top", 10, "")
	if err := parseArgs(fs, args); err != nil {
		return c.fail(err.Error(), 2)
	}
	now := time.Now()
	opts := stats.Options{TopN: *top, Now: now}
	var err error
	if opts.Since, err = sinceFlag(*since, now); err != nil {
		return c.fail(err.Error(), 2)
	}
	if *all {
		if opts.Projects, err = store.ListProjects(); err != nil {
			return c.failErr(err)
		}
	} else {
		p, err := c.openProject(false)
		if err != nil {
			return c.fail(err.Error(), 1)
		}
		opts.Projects = []*store.Project{p}
	}
	rep, err := stats.Build(opts)
	if err != nil {
		return c.failErr(err)
	}
	if c.json {
		c.emit(rep)
		return 0
	}
	c.printStats(rep, *daily)
	return 0
}

func (c *cli) printStats(rep stats.Report, daily bool) {
	scope := "this project"
	if rep.Projects != 1 {
		scope = fmt.Sprintf("%d projects", rep.Projects)
	}
	if rep.Since != "" {
		scope += " since " + rep.Since[:10]
	}
	fmt.Fprintln(c.out, ui.Title.Render(" stats ")+" "+ui.Dim.Render(scope))
	fmt.Fprintln(c.out)
	w := tabwriter.NewWriter(c.out, 0, 0, 3, ' ', 0)
	fmt.Fprintf(w, "  %s\t%d (%d live)\n", ui.Dim.Render("sessions"), rep.Sessions, rep.LiveSessions)
	fmt.Fprintf(w, "  %s\t%d\n", ui.Dim.Render("turns"), rep.Turns)
	fmt.Fprintf(w, "  %s\t%d by Claude, %d external\n", ui.Dim.Render("edits"), rep.Edits, rep.External)
	fmt.Fprintf(w, "  %s\t%d\n", ui.Dim.Render("files"), rep.Files)
	fmt.Fprintf(w, "  %s\t%s %s\n", ui.Dim.Render("lines"), ui.Added.Render(fmt.Sprintf("+%d", rep.LinesAdded)), ui.Removed.Render(fmt.Sprintf("-%d", rep.LinesRemoved)))
	w.Flush()
	if len(rep.Usage) > 0 {
		fmt.Fprintln(c.out)
		fmt.Fprintln(c.out, ui.PaneTitle.Render("tokens"))
		w = tabwriter.NewWriter(c.out, 0, 0, 3, ' ', 0)
		fmt.Fprintf(w, "  %s\t%s\t%s\t%s\t%s\t%s\n", ui.Dim.Render("model"), ui.Dim.Render("input"), ui.Dim.Render("output"), ui.Dim.Render("cache read"), ui.Dim.Render("cache write"), ui.Dim.Render("messages"))
		for model, u := range rep.Usage {
			fmt.Fprintf(w, "  %s\t%s\t%s\t%s\t%s\t%d\n", model, humanCount(u.Input), humanCount(u.Output), humanCount(u.CacheRead), humanCount(u.CacheWrite), u.Messages)
		}
		w.Flush()
		if rep.Cost != nil {
			note := "if paid at API prices"
			if !rep.CostComplete {
				note = "at API prices; some models have no price and are left out"
			}
			fmt.Fprintf(c.out, "  %s %s %s\n", ui.Dim.Render("API cost"), ui.Accent.Render(fmt.Sprintf("$%.2f", *rep.Cost)), ui.Dim.Render(note))
		}
	}
	if st := rep.Storage; st.Bytes > 0 {
		fmt.Fprintln(c.out)
		fmt.Fprintln(c.out, ui.PaneTitle.Render("storage"))
		fmt.Fprintf(c.out, "  %s on disk, about %s per edit, %d edits per active day\n", humanBytes(st.Bytes), humanBytes(st.BytesPerEdit), st.EditsPerDay)
		fmt.Fprintf(c.out, "  %s at this pace over the %d-day retention window %s\n", humanBytes(st.Projected), st.RetentionDays,
			ui.Dim.Render(fmt.Sprintf("(cap %s per project; git packing makes the real figure smaller)", humanBytes(st.CapBytes))))
	}
	if len(rep.TopFiles) > 0 {
		fmt.Fprintln(c.out)
		fmt.Fprintln(c.out, ui.PaneTitle.Render("most touched"))
		for _, f := range rep.TopFiles {
			fmt.Fprintf(c.out, "  %4d  %s\n", f.Edits, f.Path)
		}
	}
	if daily && len(rep.Days) > 0 {
		fmt.Fprintln(c.out)
		fmt.Fprintln(c.out, ui.PaneTitle.Render("daily"))
		w = tabwriter.NewWriter(c.out, 0, 0, 3, ' ', 0)
		header := "  " + ui.Dim.Render("date") + "\t" + ui.Dim.Render("edits") + "\t" + ui.Dim.Render("sessions") + "\t" + ui.Dim.Render("files") + "\t" + ui.Dim.Render("lines")
		if rep.ClaudeCache {
			header += "\t" + ui.Dim.Render("claude msgs") + "\t" + ui.Dim.Render("tool calls")
		}
		fmt.Fprintln(w, header)
		for _, d := range rep.Days {
			row := fmt.Sprintf("  %s\t%d\t%d\t%d\t%s %s", d.Date, d.Edits, d.Sessions, d.Files, ui.Added.Render(fmt.Sprintf("+%d", d.Added)), ui.Removed.Render(fmt.Sprintf("-%d", d.Removed)))
			if rep.ClaudeCache {
				row += fmt.Sprintf("\t%d\t%d", d.ClaudeMessages, d.ClaudeToolCalls)
			}
			fmt.Fprintln(w, row)
		}
		w.Flush()
	} else if len(rep.Days) > 0 {
		busiest := rep.Days[0]
		for _, d := range rep.Days {
			if d.Edits > busiest.Edits {
				busiest = d
			}
		}
		fmt.Fprintf(c.out, "\n  %s %s with %d edits %s\n", ui.Dim.Render("busiest day"), busiest.Date, busiest.Edits, ui.Dim.Render("(--daily for the table)"))
	}
}

func humanCount(n int64) string {
	switch {
	case n >= 1_000_000_000:
		return fmt.Sprintf("%.1fB", float64(n)/1e9)
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1e3)
	}
	return fmt.Sprintf("%d", n)
}

type check struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
	Fix    string `json:"fix,omitempty"`
}

func (c *cli) cmdDoctor(args []string) int {
	fs := newFlags("doctor")
	fix := fs.Bool("fix", false, "")
	if err := parseArgs(fs, args); err != nil {
		return c.fail(err.Error(), 2)
	}
	var checks []check

	if out, err := exec.Command("git", "--version").Output(); err == nil {
		checks = append(checks, check{"git", true, strings.TrimSpace(string(out)), ""})
	} else {
		checks = append(checks, check{"git", false, "git not found on PATH; the content store needs it", "install git"})
	}

	home, err := store.EnsureHome()
	if err != nil {
		checks = append(checks, check{"store", false, err.Error(), ""})
	} else {
		checks = append(checks, check{"store", true, home, ""})
	}

	st := install.Check()
	switch {
	case st.PluginInstalled:
		checks = append(checks, check{"hooks", true, "provided by the pawprints plugin", ""})
	case st.SettingsHooks:
		checks = append(checks, check{"hooks", true, "registered in " + st.SettingsPath, ""})
	default:
		ck := check{"hooks", false, "not registered; Claude's edits are not being recorded", "pawprints install"}
		if *fix {
			if _, err := c.installQuiet(); err == nil {
				ck = check{"hooks", true, "registered in " + st.SettingsPath + " (fixed)", ""}
			} else {
				ck.Detail += "; fix failed: " + err.Error()
			}
		}
		checks = append(checks, ck)
	}

	registry, rerr := sessions.ReadRegistry()
	switch {
	case rerr != nil:
		checks = append(checks, check{"session registry", false, rerr.Error(), ""})
	case len(registry) == 0:
		checks = append(checks, check{"session registry", true, "no entries in " + sessions.RegistryDir() + " (no Claude running, or an older Claude Code)", ""})
	default:
		live, version := 0, ""
		for _, r := range registry {
			if r.Alive {
				live++
				version = r.Version
			}
		}
		detail := fmt.Sprintf("%d entries, %d live", len(registry), live)
		if version != "" {
			detail += ", Claude Code " + version
		}
		ok := true
		if live > 0 && !claudeProcessSeen() {
			ok = false
			detail += "; registry says live but no claude process found: the format may have changed"
		}
		checks = append(checks, check{"session registry", ok, detail, ""})
	}

	if _, err := os.Stat(sessions.ClaudeHome()); err == nil {
		checks = append(checks, check{"claude home", true, sessions.ClaudeHome(), ""})
	} else {
		checks = append(checks, check{"claude home", false, sessions.ClaudeHome() + " not found; is Claude Code installed?", ""})
	}

	if rep, err := store.Regroup(sessions.ProjectRoots(), true); err == nil {
		ck := check{"grouping", true, "every edit is in its session's project", ""}
		if rep.Events > 0 {
			ck = check{"grouping", false, fmt.Sprintf("%d edits sit outside the project their session was launched in", rep.Events), "pawprints regroup"}
			if *fix {
				if done, err := store.Regroup(sessions.ProjectRoots(), false); err == nil {
					ck = check{"grouping", true, fmt.Sprintf("moved %d edits into %d project(s) (fixed)", done.Events, len(done.Into)), ""}
				} else {
					ck.Detail += "; fix failed: " + err.Error()
				}
			}
		}
		checks = append(checks, ck)
	}
	if rep, err := store.FoldTurns(true); err == nil {
		ck := check{"prompts", true, "subagents' changes sit under the prompt that started them", ""}
		if rep.Turns > 0 || rep.Events > 0 {
			ck = check{"prompts", false, fmt.Sprintf("%d subagent reports or repeated prompts recorded as prompts, %d changes under the wrong prompt", rep.Turns, rep.Events), "pawprints regroup"}
			if *fix {
				if done, err := store.FoldTurns(false); err == nil {
					_, _ = sessions.FillAgents(false)
					ck = check{"prompts", true, fmt.Sprintf("folded %d turns and moved %d changes (fixed)", done.Turns, done.Events), ""}
				} else {
					ck.Detail += "; fix failed: " + err.Error()
				}
			}
		}
		checks = append(checks, ck)
	}
	if rep, err := store.Untangle(true); err == nil {
		ck := check{"project lines", true, "changes outside Claude are only recorded in their own project", ""}
		if rep.Events > 0 || rep.Roots > 0 {
			ck = check{"project lines", false, fmt.Sprintf("%d changes marked outside Claude in files another project owns, %d scan folders holding whole projects", rep.Events, rep.Roots), "pawprints regroup"}
			if *fix {
				if done, err := store.Untangle(false); err == nil {
					ck = check{"project lines", true, fmt.Sprintf("dropped %d changes and %d scan folders (fixed)", done.Events, done.Roots), ""}
				} else {
					ck.Detail += "; fix failed: " + err.Error()
				}
			}
		}
		checks = append(checks, ck)
	}

	root := c.root()
	if p, ok := store.FindProjectFor(root); ok {
		months, _ := p.Months()
		checks = append(checks, check{"project", true, fmt.Sprintf("%s, %d months of history, %s", root, len(months), humanBytes(p.Size())), ""})
	} else {
		checks = append(checks, check{"project", true, root + " has no history yet", ""})
	}

	if c.json {
		c.emit(map[string]any{"checks": checks})
		return exitFor(checks)
	}
	fmt.Fprintln(c.out, ui.Title.Render(" doctor "))
	for _, ck := range checks {
		mark := ui.Ok.Render("✓")
		if !ck.OK {
			mark = ui.Err.Render("✗")
		}
		fmt.Fprintf(c.out, "  %s %-18s %s\n", mark, ck.Name, ck.Detail)
		if ck.Fix != "" && !ck.OK {
			fmt.Fprintf(c.out, "    %s %s\n", ui.Dim.Render("fix:"), ui.Strong.Render(ck.Fix))
		}
	}
	return exitFor(checks)
}

func exitFor(checks []check) int {
	for _, ck := range checks {
		if !ck.OK {
			return 1
		}
	}
	return 0
}

func (c *cli) installQuiet() (install.Status, error) {
	bin, err := os.Executable()
	if err != nil {
		return install.Status{}, err
	}
	return install.Install(bin)
}

func claudeProcessSeen() bool {
	out, err := exec.Command("pgrep", "-x", "claude").Output()
	return err == nil && len(strings.TrimSpace(string(out))) > 0
}

func (c *cli) cmdCompletion(args []string) int {
	if len(args) < 1 {
		return c.fail("usage: pawprints completion bash|zsh|fish", 2)
	}
	switch args[0] {
	case "bash":
		fmt.Fprint(c.out, completion.Bash)
	case "zsh":
		fmt.Fprint(c.out, completion.Zsh)
	case "fish":
		fmt.Fprint(c.out, completion.Fish)
	default:
		return c.fail("unknown shell "+args[0], 2)
	}
	return 0
}
