package tui

import (
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/elythi0n/pawprints/internal/metrics"
	"github.com/elythi0n/pawprints/internal/stats"
	"github.com/elythi0n/pawprints/internal/store"
	"github.com/elythi0n/pawprints/internal/ui"
	"github.com/elythi0n/pawprints/internal/view"
)

// StatsPane renders the stats report with a few period choices.
type StatsPane struct {
	src    *view.Source
	vp     viewport.Model
	period time.Duration // 0 means all time
	rep    *stats.Report
	met    *metrics.Report
	err    error
	w, h   int
	busy   bool
}

type statsMsg struct {
	rep stats.Report
	met *metrics.Report
	err error
}

// NewStatsPane creates the pane.
func NewStatsPane(src *view.Source) *StatsPane {
	return &StatsPane{src: src, vp: viewport.New(10, 10), period: 7 * 24 * time.Hour}
}

// Title implements ui.Pane.
func (s *StatsPane) Title() string { return "Stats" }

// Init implements ui.Pane.
func (s *StatsPane) Init() tea.Cmd { return s.load() }

// SetSize implements ui.Pane.
func (s *StatsPane) SetSize(w, h int) {
	s.w, s.h = w, h
	s.vp.Width = w
	s.vp.Height = h - 2
	s.render()
}

func (s *StatsPane) load() tea.Cmd {
	src := s.src
	period := s.period
	s.busy = true
	return func() tea.Msg {
		opts := stats.Options{Now: time.Now(), TopN: 12}
		if period > 0 {
			opts.Since = time.Now().Add(-period)
		}
		if src.AllProjects {
			opts.Projects, _ = store.ListProjects()
		} else if src.Project != nil {
			opts.Projects = []*store.Project{src.Project}
		}
		rep, err := stats.Build(opts)
		// The charts need a range; "all time" draws the last 90 days.
		mopts := metrics.Options{Projects: opts.Projects, Now: opts.Now, Since: opts.Since}
		if mopts.Since.IsZero() {
			mopts.Since = opts.Now.Add(-90 * 24 * time.Hour)
		}
		var met *metrics.Report
		if m, merr := metrics.Build(mopts); merr == nil {
			met = &m
		}
		return statsMsg{rep: rep, met: met, err: err}
	}
}

// Update implements ui.Pane.
func (s *StatsPane) Update(msg tea.Msg) (ui.Pane, tea.Cmd) {
	switch m := msg.(type) {
	case ReloadMsg:
		return s, s.load()
	case statsMsg:
		s.busy = false
		s.err = m.err
		r := m.rep
		s.rep = &r
		s.met = m.met
		s.render()
		return s, nil

	case tea.KeyMsg:
		switch m.String() {
		case "d":
			s.period = 24 * time.Hour
			return s, s.load()
		case "w":
			s.period = 7 * 24 * time.Hour
			return s, s.load()
		case "m":
			s.period = 30 * 24 * time.Hour
			return s, s.load()
		case "a":
			s.period = 0
			return s, s.load()
		case "A":
			return s, func() tea.Msg { return ToggleAllProjectsMsg{} }
		case "r":
			return s, s.load()
		case "o":
			return s, openUI(s.period)
		}
	}
	var cmd tea.Cmd
	s.vp, cmd = s.vp.Update(msg)
	return s, cmd
}

func (s *StatsPane) render() {
	if s.rep == nil {
		s.vp.SetContent(ui.Dim.Render("loading…"))
		return
	}
	if s.err != nil {
		s.vp.SetContent(ui.Err.Render(s.err.Error()))
		return
	}
	r := s.rep
	var sb strings.Builder
	scope := "this project"
	if s.src.AllProjects {
		scope = fmt.Sprintf("%d projects", r.Projects)
	}
	when := "all time"
	switch s.period {
	case 24 * time.Hour:
		when = "last 24 hours"
	case 7 * 24 * time.Hour:
		when = "last 7 days"
	case 30 * 24 * time.Hour:
		when = "last 30 days"
	}
	sb.WriteString(ui.PaneTitle.Render(scope) + "  " + ui.Dim.Render(when) + "\n\n")
	row := func(k string, v string) {
		fmt.Fprintf(&sb, "  %s %s\n", ui.Dim.Render(ui.PadRight(k, 10)), v)
	}
	row("sessions", fmt.Sprintf("%d  %s", r.Sessions, ui.Dim.Render(fmt.Sprintf("%d live", r.LiveSessions))))
	row("turns", fmt.Sprint(r.Turns))
	row("edits", fmt.Sprintf("%d by Claude  %s", r.Edits, ui.Dim.Render(fmt.Sprintf("%d outside Claude", r.External))))
	row("files", fmt.Sprint(r.Files))
	row("lines", ui.Added.Render(fmt.Sprintf("+%d", r.LinesAdded))+" "+ui.Removed.Render(fmt.Sprintf("-%d", r.LinesRemoved)))
	if mt := s.met; mt != nil && len(mt.Timeline) > 1 {
		width := s.w - 14
		if width > 96 {
			width = 96
		}
		if width >= 16 {
			tok := make([]float64, len(mt.Timeline))
			lines := make([]float64, len(mt.Timeline))
			for i, p := range mt.Timeline {
				tok[i] = p.PerMinute
				lines[i] = float64(p.LinesAdded + p.LinesRemoved)
			}
			sb.WriteString("\n" + ui.PaneTitle.Render("over time") + "  " + ui.Dim.Render("per "+mt.Range.Bucket+", press o for the full charts in the browser") + "\n")
			fmt.Fprintf(&sb, "  %s %s %s\n", ui.Dim.Render(ui.PadRight("tokens", 8)), ui.Accent.Render(ui.Sparkline(tok, width)), ui.Dim.Render(humanCount(int64(mt.Totals.PeakPerMinute))+"/min peak"))
			fmt.Fprintf(&sb, "  %s %s\n", ui.Dim.Render(ui.PadRight("lines", 8)), ui.Added.Render(ui.Sparkline(lines, width)))
			if len(mt.Spikes) > 0 {
				sp := mt.Spikes[0]
				at, _ := time.Parse(time.RFC3339Nano, sp.At)
				fmt.Fprintf(&sb, "  %s %s %s %s\n", ui.Dim.Render(ui.PadRight("spike", 8)), ui.Removed.Render(fmt.Sprintf("%.1f×", sp.TimesNormal)), at.Local().Format("Jan 2 15:04"), ui.Dim.Render(sp.SessionName))
			}
		}
	}
	if len(r.Usage) > 0 {
		sb.WriteString("\n" + ui.PaneTitle.Render("tokens") + "\n")
		models := make([]string, 0, len(r.Usage))
		for m := range r.Usage {
			models = append(models, m)
		}
		sort.Strings(models)
		for _, m := range models {
			u := r.Usage[m]
			fmt.Fprintf(&sb, "  %s\n    in %s · out %s · cache read %s · cache write %s · %d messages\n",
				ui.Strong.Render(m), humanCount(u.Input), humanCount(u.Output), humanCount(u.CacheRead), humanCount(u.CacheWrite), u.Messages)
		}
		if r.Cost != nil {
			note := "if paid at API prices"
			if !r.CostComplete {
				note = "at API prices; some models have no price and are left out"
			}
			fmt.Fprintf(&sb, "  %s %s %s\n", ui.Dim.Render("API cost"), ui.Accent.Render(fmt.Sprintf("$%.2f", *r.Cost)), ui.Dim.Render(note))
		}
	}
	if len(r.TopFiles) > 0 {
		sb.WriteString("\n" + ui.PaneTitle.Render("most touched") + "\n")
		max := r.TopFiles[0].Edits
		for _, f := range r.TopFiles {
			bar := strings.Repeat("▇", scaled(f.Edits, max, 20))
			fmt.Fprintf(&sb, "  %4d %s %s\n", f.Edits, ui.Hl.Render(ui.PadRight(bar, 20)), f.Path)
		}
	}
	if len(r.Days) > 0 {
		sb.WriteString("\n" + ui.PaneTitle.Render("daily") + "\n")
		max := 0
		for _, d := range r.Days {
			if d.Edits > max {
				max = d.Edits
			}
		}
		for i, d := range r.Days {
			if i >= 31 {
				break
			}
			bar := strings.Repeat("▇", scaled(d.Edits, max, 24))
			extra := ""
			if r.ClaudeCache && d.ClaudeMessages > 0 {
				extra = ui.Dim.Render(fmt.Sprintf("  claude: %d msgs, %d tool calls", d.ClaudeMessages, d.ClaudeToolCalls))
			}
			fmt.Fprintf(&sb, "  %s %4d %s %s %s%s\n", d.Date, d.Edits, ui.Hl.Render(ui.PadRight(bar, 24)),
				ui.Added.Render(fmt.Sprintf("+%d", d.Added)), ui.Removed.Render(fmt.Sprintf("-%d", d.Removed)), extra)
		}
	}
	s.vp.SetContent(ui.ClipBlock(sb.String(), s.w, 10000))
}

func scaled(v, max, width int) int {
	if max <= 0 || v <= 0 {
		return 0
	}
	n := v * width / max
	if n == 0 {
		n = 1
	}
	return n
}

// View implements ui.Pane.
func (s *StatsPane) View() string {
	if s.busy && s.rep == nil {
		return ui.Dim.Render("computing…")
	}
	return s.vp.View()
}

// Help implements ui.Pane.
func (s *StatsPane) Help() string {
	return ui.Keys("d", "24h", "w", "7d", "m", "30d", "a", "all time", "A", "all projects", "o", "open in browser", "r", "refresh", "j/k", "scroll")
}

// openUI starts `pawprints ui` on its own, so it outlives the TUI, at the
// range the pane shows.
func openUI(period time.Duration) tea.Cmd {
	return func() tea.Msg {
		self, err := os.Executable()
		if err != nil {
			return ui.StatusMsg{Text: "could not start pawprints ui: " + err.Error(), Kind: "err"}
		}
		r := "30d"
		switch period {
		case 24 * time.Hour:
			r = "24h"
		case 7 * 24 * time.Hour:
			r = "7d"
		}
		cmd := exec.Command(self, "ui", "--range", r)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err := cmd.Start(); err != nil {
			return ui.StatusMsg{Text: "could not start pawprints ui: " + err.Error(), Kind: "err"}
		}
		go func() { _ = cmd.Wait() }()
		return ui.StatusMsg{Text: "opening the charts in your browser", Kind: "ok"}
	}
}
