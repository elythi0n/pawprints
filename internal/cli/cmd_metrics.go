package cli

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/elythi0n/pawprints/internal/metrics"
	"github.com/elythi0n/pawprints/internal/sessions"
	"github.com/elythi0n/pawprints/internal/store"
	"github.com/elythi0n/pawprints/internal/ui"
	"github.com/elythi0n/pawprints/internal/usage"
)

// metricsOptions reads the flags `metrics` and the web view share.
type metricsFlags struct {
	since, until, bucket, account, session *string
	all                                    *bool
}

func addMetricsFlags(fs *flagSetAlias) metricsFlags {
	return metricsFlags{
		since:   fs.String("since", "24h", ""),
		until:   fs.String("until", "", ""),
		bucket:  fs.String("bucket", "", ""),
		account: fs.String("account", "", ""),
		session: fs.String("session", "", ""),
		all:     fs.Bool("all", false, ""),
	}
}

func (c *cli) metricsOptions(f metricsFlags, now time.Time) (metrics.Options, error) {
	opts := metrics.Options{Now: now}
	var err error
	if opts.Since, err = sinceFlag(*f.since, now); err != nil {
		return opts, err
	}
	if *f.until != "" {
		if opts.Until, err = sinceFlag(*f.until, now); err != nil {
			return opts, err
		}
	}
	if *f.bucket != "" {
		if opts.Bucket, err = parseSpan(*f.bucket); err != nil {
			return opts, fmt.Errorf("cannot read --bucket %q", *f.bucket)
		}
	}
	if opts.Account, err = resolveAccount(*f.account); err != nil {
		return opts, err
	}
	if *f.all {
		opts.Projects, err = store.ListProjects()
		if err != nil {
			return opts, err
		}
		if *f.session != "" {
			if opts.Session, err = sessionAnywhere(opts.Projects, *f.session); err != nil {
				return opts, err
			}
		}
	} else {
		p, err := c.openProject(false)
		if err != nil {
			return opts, err
		}
		opts.Projects = []*store.Project{p}
		if *f.session != "" {
			id, err := c.resolveSession(p, *f.session)
			if err != nil {
				return opts, err
			}
			opts.Session = id
		}
	}
	return opts, nil
}

// sessionAnywhere finds a session by id or unique id prefix in any project.
func sessionAnywhere(projects []*store.Project, ref string) (string, error) {
	found := map[string]bool{}
	for _, p := range projects {
		recs, err := p.Sessions()
		if err != nil {
			return "", err
		}
		for _, r := range recs {
			if r.Session == ref {
				return ref, nil
			}
			if strings.HasPrefix(r.Session, ref) {
				found[r.Session] = true
			}
		}
	}
	switch len(found) {
	case 0:
		return "", fmt.Errorf("no session matches %q", ref)
	case 1:
		for id := range found {
			return id, nil
		}
	}
	return "", fmt.Errorf("%q matches %d sessions; give more of the id", ref, len(found))
}

// resolveAccount accepts an account id, an id prefix, or an email.
func resolveAccount(ref string) (string, error) {
	if ref == "" || ref == "all" {
		return "", nil
	}
	known := store.Accounts()
	if _, ok := known[ref]; ok {
		return ref, nil
	}
	var found []string
	for id, a := range known {
		if strings.EqualFold(a.Email, ref) || strings.HasPrefix(id, ref) {
			found = append(found, id)
		}
	}
	if len(found) == 1 {
		return found[0], nil
	}
	return "", fmt.Errorf("no account matches %q", ref)
}

func (c *cli) cmdMetrics(args []string) int {
	fs := newFlags("metrics")
	f := addMetricsFlags(fs)
	if err := parseArgs(fs, args); err != nil {
		return c.fail(err.Error(), 2)
	}
	opts, err := c.metricsOptions(f, time.Now())
	if err != nil {
		return c.fail(err.Error(), 1)
	}
	opts.Tail = usage.NewTail()
	rep, err := metrics.Build(opts)
	if err != nil {
		return c.failErr(err)
	}
	if c.json {
		c.emit(rep)
		return 0
	}
	c.printMetrics(rep)
	return 0
}

func (c *cli) printMetrics(rep metrics.Report) {
	from, _ := time.Parse(time.RFC3339Nano, rep.Range.From)
	fmt.Fprintln(c.out, ui.Title.Render(" metrics ")+" "+ui.Dim.Render("since "+from.Local().Format("Jan 2 15:04")+", per "+rep.Range.Bucket))
	fmt.Fprintln(c.out)
	t := rep.Totals
	w := tabwriter.NewWriter(c.out, 0, 0, 3, ' ', 0)
	fmt.Fprintf(w, "  %s\t%s %s\n", ui.Dim.Render("tokens"), humanCount(t.Tokens.Counted), ui.Dim.Render("counted, "+humanCount(t.Tokens.CacheRead)+" cache reads"))
	cost := fmt.Sprintf("$%.2f", t.APICost)
	if !t.CostComplete {
		cost += ui.Dim.Render(" (some models have no price)")
	}
	fmt.Fprintf(w, "  %s\t%s %s\n", ui.Dim.Render("API cost"), ui.Accent.Render(cost), ui.Dim.Render("at API prices"))
	if t.PeakPerMinute > 0 {
		at, _ := time.Parse(time.RFC3339Nano, t.PeakAt)
		fmt.Fprintf(w, "  %s\t%s/min %s\n", ui.Dim.Render("peak"), humanCount(int64(t.PeakPerMinute)), ui.Dim.Render(at.Local().Format("15:04")+" "+t.PeakSession))
	}
	fmt.Fprintf(w, "  %s\t%d (%d running)\n", ui.Dim.Render("sessions"), t.Sessions, t.LiveSessions)
	fmt.Fprintf(w, "  %s\t%s %s in %d files\n", ui.Dim.Render("lines"), ui.Added.Render(fmt.Sprintf("+%d", t.LinesAdded)), ui.Removed.Render(fmt.Sprintf("-%d", t.LinesRemoved)), t.Files)
	w.Flush()
	if len(rep.Timeline) > 1 {
		series := make([]float64, len(rep.Timeline))
		for i, p := range rep.Timeline {
			series[i] = p.PerMinute
		}
		fmt.Fprintln(c.out)
		fmt.Fprintln(c.out, "  "+ui.Accent.Render(ui.Sparkline(series, 60)))
	}
	if len(rep.Spikes) > 0 {
		fmt.Fprintln(c.out)
		fmt.Fprintln(c.out, ui.PaneTitle.Render("spikes"))
		for _, s := range rep.Spikes {
			at, _ := time.Parse(time.RFC3339Nano, s.At)
			fmt.Fprintf(c.out, "  %s %s %s %s\n", ui.Dim.Render(at.Local().Format("Jan 2 15:04")), ui.Removed.Render(fmt.Sprintf("%.1f×", s.TimesNormal)), s.SessionName, ui.Dim.Render(clip(s.Prompt, 60)))
		}
	}
	if rep.MissingHistory > 0 {
		fmt.Fprintln(c.out)
		fmt.Fprintln(c.out, ui.Dim.Render(fmt.Sprintf("  %d sessions from before metrics have no timeline yet; `pawprints usage backfill` fills them in", rep.MissingHistory)))
	}
}

func (c *cli) cmdLimits(args []string) int {
	if len(args) > 0 && args[0] == "record" {
		return c.cmdLimitsRecord(args[1:])
	}
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		return c.fail("usage: pawprints "+usages["limits"], 2)
	}
	if err := parseArgs(newFlags("limits"), args); err != nil {
		return c.fail(err.Error(), 2)
	}
	now := time.Now()
	out, err := metrics.Limits(now)
	if err != nil {
		return c.failErr(err)
	}
	if c.json {
		for _, a := range out {
			c.emit(a)
		}
		return 0
	}
	c.printLimits(out, now)
	return 0
}

func (c *cli) printLimits(out []metrics.AccountLimits, now time.Time) {
	fmt.Fprintln(c.out, ui.Title.Render(" limits ")+" "+ui.Dim.Render("usage limits per Claude account, from Whiskers' readings"))
	if len(out) == 0 {
		fmt.Fprintln(c.out)
		fmt.Fprintln(c.out, ui.Dim.Render("  no accounts yet; limits are recorded while Whiskers runs in Claude Code"))
		return
	}
	for _, a := range out {
		name := a.Email
		if name == "" {
			name = "unknown account " + shortID(a.Account)
		}
		if a.Plan != "" {
			name += ui.Dim.Render(" · " + a.Plan)
		}
		updated := ""
		if t, err := time.Parse(time.RFC3339Nano, a.UpdatedAt); err == nil {
			updated = ui.Dim.Render("updated " + agoText(now.Sub(t)))
		}
		fmt.Fprintf(c.out, "\n  %s  %s  %s\n", ui.Strong.Render(name), limitStatus(a, now), updated)
		w := tabwriter.NewWriter(c.out, 0, 0, 2, ' ', 0)
		for _, win := range a.Windows {
			pct := fmt.Sprintf("%3.0f%%", win.Percent)
			switch {
			case win.Full:
				pct = ui.Err.Render(pct)
			case win.Percent >= 80:
				pct = ui.Warn.Render(pct)
			}
			if win.Assumed {
				pct += ui.Dim.Render(" (reset since)")
			}
			reset := ""
			if t, err := time.Parse(time.RFC3339Nano, win.ResetsAt); err == nil {
				reset = "resets " + clockLabel(t, now) + ui.Dim.Render(" (in "+untilText(t.Sub(now))+")")
				if win.Projected {
					reset += ui.Dim.Render(", projected")
				}
			}
			pace := win.Pace
			switch win.Pace {
			case metrics.PaceTight:
				t, _ := time.Parse(time.RFC3339Nano, win.FullAt)
				pace = ui.Warn.Render(fmt.Sprintf("↗%.1f%%/h, full %s", win.PerHour, clockLabel(t, now)))
			case metrics.PaceCalm:
				pace = fmt.Sprintf("↗%.1f%%/h", win.PerHour)
			case metrics.PaceFull:
				pace = ui.Err.Render("full")
			case metrics.PaceUnknown:
				pace = ui.Dim.Render("pace unknown")
			case metrics.PaceIdle:
				pace = ui.Dim.Render("idle")
			}
			fmt.Fprintf(w, "    %s\t%s\t%s\t%s\n", ui.Dim.Render(windowName(win.Kind)), pct, reset, pace)
		}
		_ = w.Flush()
	}
}

func limitStatus(a metrics.AccountLimits, now time.Time) string {
	switch a.Status {
	case metrics.StatusLimited:
		if t, err := time.Parse(time.RFC3339Nano, a.UnlocksAt); err == nil {
			return ui.Err.Render("limited until " + clockLabel(t, now) + " (in " + untilText(t.Sub(now)) + ")")
		}
		return ui.Err.Render("limited")
	case metrics.StatusFilling:
		t, _ := time.Parse(time.RFC3339Nano, a.FullAt)
		return ui.Warn.Render("full " + clockLabel(t, now) + " at this pace")
	case metrics.StatusAvailable:
		return ui.Ok.Render("available")
	}
	return ui.Dim.Render("no limit data (needs Whiskers)")
}

func windowName(kind string) string {
	switch kind {
	case "five_hour":
		return "5 hours"
	case "seven_day":
		return "week"
	}
	return strings.ReplaceAll(kind, "_", " ")
}

// clockLabel is a time of day, with the weekday within a week, else the date.
func clockLabel(t, now time.Time) string {
	t, now = t.Local(), now.Local()
	switch {
	case t.YearDay() == now.YearDay() && t.Year() == now.Year():
		return t.Format("15:04")
	case t.Sub(now) < 6*24*time.Hour && t.Sub(now) > -6*24*time.Hour:
		return t.Format("Mon 15:04")
	}
	return t.Format("Jan 2 15:04")
}

// untilText is a duration as "2h 14m", "3d 4h", or "5m".
func untilText(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	m := int(d.Round(time.Minute).Minutes())
	switch {
	case m >= 24*60:
		return fmt.Sprintf("%dd %dh", m/(24*60), m%(24*60)/60)
	case m >= 60:
		return fmt.Sprintf("%dh %dm", m/60, m%60)
	}
	return fmt.Sprintf("%dm", m)
}

func agoText(d time.Duration) string {
	if d < time.Minute {
		return "just now"
	}
	return untilText(d) + " ago"
}

func (c *cli) cmdLimitsRecord(args []string) int {
	fs := newFlags("limits record")
	session := fs.String("session", "", "")
	kind := fs.String("kind", "", "")
	percent := fs.String("percent", "", "")
	resets := fs.String("resets-at", "", "")
	if err := parseArgs(fs, args); err != nil {
		return c.fail(err.Error(), 2)
	}
	pct, err := strconv.ParseFloat(*percent, 64)
	if *kind == "" || err != nil {
		return c.fail("usage: pawprints "+usages["limits"], 2)
	}
	r := store.LimitReading{At: store.Now(time.Now()), Session: *session, Kind: *kind, Percent: pct, ResetsAt: *resets}
	if root, ok := store.SessionRoot(*session); ok {
		if p, found := store.FindProject(root); found {
			if rec, ok := p.LoadSession(*session); ok {
				r.Account = rec.Account
			}
		}
	}
	if r.Account == "" {
		// The caller runs inside the Claude session, so its config folder
		// is the session's.
		if acct, ok := sessions.CurrentAccount(sessions.ClaudeHome()); ok {
			r.Account = acct.ID
			_ = store.RememberAccount(acct, time.Now())
		}
	}
	if err := store.AppendLimit(r); err != nil {
		return c.failErr(err)
	}
	if c.json {
		c.emit(r)
	}
	return 0
}

func (c *cli) cmdUsage(args []string) int {
	if len(args) == 0 || args[0] != "backfill" {
		return c.fail("usage: pawprints "+usages["usage"], 2)
	}
	fs := newFlags("usage backfill")
	here := fs.Bool("here", false, "")
	if err := parseArgs(fs, args[1:]); err != nil {
		return c.fail(err.Error(), 2)
	}
	var projects []*store.Project
	if *here {
		p, err := c.openProject(false)
		if err != nil {
			return c.fail(err.Error(), 1)
		}
		projects = []*store.Project{p}
	} else {
		var err error
		if projects, err = store.ListProjects(); err != nil {
			return c.failErr(err)
		}
	}
	type result struct {
		Sessions int `json:"sessions"`
		Missing  int `json:"missing_transcripts"`
		Minutes  int `json:"minutes"`
	}
	var res result
	pins := store.SessionRoots()
	owners := map[string]string{}
	for _, p := range projects {
		recs, _ := p.Sessions()
		for _, rec := range recs {
			if _, ok := owners[rec.Session]; !ok || pins[rec.Session] == p.Root {
				owners[rec.Session] = p.ID
			}
		}
	}
	for _, p := range projects {
		recs, err := p.Sessions()
		if err != nil {
			continue
		}
		for _, rec := range recs {
			if owners[rec.Session] != p.ID {
				// Older versions could copy a session's record into a second
				// project; its tokens belong to the first, so this copy has none.
				_ = p.UpdateSession(rec.Session, func(r *store.SessionRecord) {
					r.Usage, r.TranscriptOffset, r.LastMessageID, r.SubagentReads = nil, 0, "", nil
					_ = p.ResetUsage(r.Session)
				})
				continue
			}
			if rec.TranscriptPath == "" {
				continue
			}
			if _, err := os.Stat(rec.TranscriptPath); err != nil {
				res.Missing++
				continue
			}
			_ = p.UpdateSession(rec.Session, func(r *store.SessionRecord) {
				// Every transcript is counted again from the start.
				r.Usage, r.TranscriptOffset, r.LastMessageID, r.SubagentReads = nil, 0, "", nil
				_ = p.ResetUsage(r.Session)
				// Each reply goes to the account of the prompt it answers.
				buckets, _ := usage.Accumulate(r, r.AccountAt)
				_ = p.AppendUsage(r.Session, buckets)
				res.Minutes += len(buckets)
			})
			res.Sessions++
		}
	}
	if c.json {
		c.emit(res)
		return 0
	}
	fmt.Fprintf(c.out, "rebuilt token history for %d sessions (%d minute buckets)", res.Sessions, res.Minutes)
	if res.Missing > 0 {
		fmt.Fprintf(c.out, "; %d transcripts are gone and were skipped", res.Missing)
	}
	fmt.Fprintln(c.out)
	return 0
}
