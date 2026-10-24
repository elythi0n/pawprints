package tui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/elythi0n/pawprints/internal/buildinfo"
	"github.com/elythi0n/pawprints/internal/install"
	"github.com/elythi0n/pawprints/internal/logfile"
	"github.com/elythi0n/pawprints/internal/panes"
	"github.com/elythi0n/pawprints/internal/setup"
	"github.com/elythi0n/pawprints/internal/store"
	"github.com/elythi0n/pawprints/internal/ui"
	"github.com/elythi0n/pawprints/internal/view"
	"github.com/elythi0n/pawprints/internal/watch"
)

// Layout constants. Lipgloss Width and Height set the content extent; the
// rounded border adds one row and column on each side on top of that.
const (
	sidebarOuterW = 22
	borderSize    = 2
	bodyPadH      = 2 // PaneBox Padding(0, 1)
	titleStatusH  = 2
	chromeH       = 2 // pane title row + help row inside the body
	minWidth      = 72
	minHeight     = 14
)

var sparkleGlyphs = [...]string{"❖", "❇", "✳", "❇"}

// Tick intervals; tests shorten them.
var (
	sparkleInterval = 800 * time.Millisecond
	statusFlash     = 4 * time.Second
)

type sparkleTick struct{}

// clockTick keeps relative times like "4 min ago" true while the UI is open.
type clockTick struct{}

var clockInterval = 30 * time.Second

type clearStatus struct{}
type watchMsg struct{ changes []watch.Change }

type root struct {
	width, height int
	src           *view.Source
	panes         []ui.Pane
	idx           int
	focusSidebar  bool

	status      string
	statusKind  string
	statusUntil time.Time
	sparkle     int
	touching    int
	waiting     int

	program  *tea.Program
	cancel   context.CancelFunc
	showHelp bool
	setup    *setupModel
	// upgradeCheck asks Init to look for outdated integrations quietly.
	upgradeCheck bool
}

// keyHelper is a pane that can list all its keys for the help overlay.
type keyHelper interface {
	KeyHelp() [][2]string
}

type textTyper interface {
	TypingText() bool
}

func newRoot(src *view.Source) *root {
	live := NewBrowser("Live", src, panes.Live)
	live.allKey = true
	r := &root{
		src: src,
		panes: []ui.Pane{
			live,
			NewBrowser("Recent", src, panes.Recent),
			NewBrowser("Sessions", src, panes.Sessions),
			NewBrowser("Files", src, panes.Files),
			NewBrowser("Pins", src, panes.Pins),
			NewStatsPane(src),
			NewSettingsPane(src),
		},
	}
	if src.Empty() && !r.anyLive() {
		r.idx = 1 // Recent shows the welcome text when empty
	}
	r.countLive()
	return r
}

func (r *root) anyLive() bool {
	for _, s := range r.src.Sessions {
		if s.Alive {
			return true
		}
	}
	return false
}

func (r *root) countLive() {
	r.touching, r.waiting = 0, 0
	for _, s := range r.src.Sessions {
		if s.Touching {
			r.touching++
		}
		if s.Status == "waiting" && s.Alive {
			r.waiting++
		}
	}
}

func (r *root) Init() tea.Cmd {
	cmds := []tea.Cmd{
		tea.Tick(sparkleInterval, func(time.Time) tea.Msg { return sparkleTick{} }),
		tea.Tick(clockInterval, func(time.Time) tea.Msg { return clockTick{} }),
	}
	for _, p := range r.panes {
		if c := p.Init(); c != nil {
			cmds = append(cmds, c)
		}
	}
	if r.setup != nil {
		cmds = append(cmds, r.setup.Init())
	}
	if r.upgradeCheck {
		cmds = append(cmds, func() tea.Msg { return upgradeScanMsg{items: setup.Scan()} })
	}
	return tea.Batch(cmds...)
}

// startWatch streams store and registry changes into the program.
func (r *root) startWatch() {
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	opts := watch.Options{All: r.src.AllProjects, PollInterval: 2 * time.Second}
	if !r.src.AllProjects {
		p := r.src.Project
		if p == nil {
			if created, err := store.OpenProject(r.src.Root); err == nil {
				p = created
			}
		}
		if p != nil {
			opts.Projects = []*store.Project{p}
		} else {
			opts.All = true
		}
	}
	ch := make(chan watch.Change, 64)
	go func() { _ = watch.New(opts).Run(ctx, ch) }()
	go func() {
		// Coalesce bursts so a parallel edit batch becomes one reload.
		var pending []watch.Change
		timer := time.NewTimer(time.Hour)
		timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case c, ok := <-ch:
				if !ok {
					return
				}
				if c.Kind == watch.KindHeartbeat {
					continue
				}
				pending = append(pending, c)
				timer.Reset(120 * time.Millisecond)
			case <-timer.C:
				if len(pending) > 0 && r.program != nil {
					r.program.Send(watchMsg{changes: pending})
					pending = nil
				}
			}
		}
	}()
}

func (r *root) restartWatch() {
	if r.cancel != nil {
		r.cancel()
	}
	r.startWatch()
}

func (r *root) reload() tea.Cmd {
	r.src.Reload()
	r.countLive()
	var cmds []tea.Cmd
	for i, p := range r.panes {
		np, cmd := p.Update(ReloadMsg{})
		r.panes[i] = np
		cmds = append(cmds, cmd)
	}
	return tea.Batch(cmds...)
}

func (r *root) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		r.width, r.height = m.Width, m.Height
		r.resizePanes()
		return r, nil
	case sparkleTick:
		r.sparkle = (r.sparkle + 1) % len(sparkleGlyphs)
		return r, tea.Tick(sparkleInterval, func(time.Time) tea.Msg { return sparkleTick{} })
	case clockTick:
		return r, tea.Batch(r.reload(), tea.Tick(clockInterval, func(time.Time) tea.Msg { return clockTick{} }))
	case clearStatus:
		if time.Now().After(r.statusUntil) {
			r.status = ""
		}
		return r, nil
	case ui.StatusMsg:
		if m.Kind == "err" {
			logfile.Errorf("tui: %s", m.Text)
		}
		r.status, r.statusKind = m.Text, m.Kind
		r.statusUntil = time.Now().Add(statusFlash)
		return r, tea.Tick(statusFlash, func(time.Time) tea.Msg { return clearStatus{} })
	case watchMsg:
		touched := false
		for _, c := range m.changes {
			if c.Kind == watch.KindEvent {
				touched = true
			}
		}
		cmd := r.reload()
		if touched && r.status == "" {
			return r, tea.Batch(cmd, flash("ok", "new edit recorded"))
		}
		return r, cmd
	case RequestReloadMsg:
		return r, r.reload()
	case upgradeScanMsg:
		if r.setup == nil && setup.ShouldOffer(m.items) {
			r.setup = newSetupModel(false, false)
			r.setup.scanned(m.items)
		}
		return r, nil
	case openSetupMsg:
		r.setup = newSetupModel(false, false)
		return r, r.setup.Init()
	case setupClosedMsg:
		r.setup = nil
		if m.quit {
			return r, tea.Quit
		}
		return r, r.reload()
	case setupScannedMsg, setupAppliedMsg, spinner.TickMsg:
		if r.setup != nil {
			return r, r.setup.Update(msg)
		}
		return r, nil
	case restyleMsg:
		// Rendered text carries the old colors; rebuild it without rereading the store.
		var cmds []tea.Cmd
		for i, p := range r.panes {
			np, cmd := p.Update(ReloadMsg{})
			r.panes[i] = np
			cmds = append(cmds, cmd)
		}
		return r, tea.Batch(cmds...)
	case editorClosedMsg:
		for i, p := range r.panes {
			if _, ok := p.(*SettingsPane); ok {
				np, cmd := p.Update(msg)
				r.panes[i] = np
				return r, cmd
			}
		}
		return r, nil
	case ToggleAllProjectsMsg:
		r.src.AllProjects = !r.src.AllProjects
		r.restartWatch()
		label := "this project"
		if r.src.AllProjects {
			label = "all projects"
		}
		return r, tea.Batch(r.reload(), flash("ok", "showing "+label))
	case JumpMsg:
		for i, p := range r.panes {
			if p.Title() == m.Pane {
				r.idx = i
				if b, ok := p.(*Browser); ok {
					b.SelectKey(m.Key)
					return r, b.renderPreview()
				}
			}
		}
		return r, nil
	case previewMsg, statsMsg, actionDoneMsg, confirmRequestMsg:
		// Deliver to every pane; each ignores what is not its own.
		var cmds []tea.Cmd
		for i, p := range r.panes {
			np, cmd := p.Update(msg)
			r.panes[i] = np
			cmds = append(cmds, cmd)
		}
		return r, tea.Batch(cmds...)
	case tea.KeyMsg:
		if r.setup != nil {
			return r, r.setup.Update(m)
		}
		if r.showHelp {
			r.showHelp = false
			if m.String() == "ctrl+c" {
				return r, tea.Quit
			}
			return r, nil
		}
		if m.String() == "?" {
			if t, ok := r.panes[r.idx].(textTyper); !ok || !t.TypingText() {
				r.showHelp = true
				return r, nil
			}
		}
		if r.focusSidebar {
			return r.sidebarKey(m)
		}
		if !r.capturing() {
			switch m.String() {
			case "ctrl+c", "q":
				return r, tea.Quit
			case "h", "left":
				if b, ok := r.panes[r.idx].(*Browser); ok && b.mode == modeList {
					if n := b.selected(); n != nil && n.Depth > 0 {
						break // let the pane collapse toward its parent
					}
				}
				r.focusSidebar = true
				return r, r.leave()
			case "tab":
				return r, r.switchTo((r.idx + 1) % len(r.panes))
			case "shift+tab":
				return r, r.switchTo((r.idx - 1 + len(r.panes)) % len(r.panes))
			case "1", "2", "3", "4", "5", "6", "7", "8", "9":
				if i := int(m.String()[0] - '1'); i < len(r.panes) {
					return r, r.switchTo(i)
				}
			case "i":
				if r.src.Empty() && !r.anyLive() {
					return r, func() tea.Msg { return openSetupMsg{} }
				}
			}
		} else if m.String() == "ctrl+c" {
			return r, tea.Quit
		}
	}
	np, cmd := r.panes[r.idx].Update(msg)
	r.panes[r.idx] = np
	return r, cmd
}

func (r *root) sidebarKey(m tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.String() {
	case "ctrl+c", "q":
		return r, tea.Quit
	case "up", "k":
		if r.idx > 0 {
			r.idx--
		}
	case "down", "j":
		if r.idx < len(r.panes)-1 {
			r.idx++
		}
	case "right", "l", "enter":
		r.focusSidebar = false
	case "tab":
		r.idx = (r.idx + 1) % len(r.panes)
	case "shift+tab":
		r.idx = (r.idx - 1 + len(r.panes)) % len(r.panes)
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		if i := int(m.String()[0] - '1'); i < len(r.panes) {
			r.idx = i
			r.focusSidebar = false
		}
	}
	if b, ok := r.panes[r.idx].(*Browser); ok {
		return r, b.renderPreview()
	}
	return r, nil
}

func (r *root) switchTo(i int) tea.Cmd {
	leave := r.leave()
	r.idx = i
	if b, ok := r.panes[r.idx].(*Browser); ok {
		return tea.Batch(leave, b.renderPreview())
	}
	return leave
}

func (r *root) leave() tea.Cmd {
	if l, ok := r.panes[r.idx].(ui.Leaver); ok {
		return l.OnLeave()
	}
	return nil
}

func (r *root) capturing() bool {
	if c, ok := r.panes[r.idx].(ui.Capturer); ok {
		return c.CapturingKeys()
	}
	return false
}

func (r *root) resizePanes() {
	bodyW := r.width - sidebarOuterW - borderSize - bodyPadH
	bodyH := r.height - titleStatusH - borderSize - chromeH
	if bodyW < 30 {
		bodyW = 30
	}
	if bodyH < 5 {
		bodyH = 5
	}
	for _, p := range r.panes {
		p.SetSize(bodyW, bodyH)
	}
}

func (r *root) View() string {
	if r.width == 0 {
		return "loading…"
	}
	if r.width < minWidth || r.height < minHeight {
		return fmt.Sprintf("terminal too small: need %d×%d, have %d×%d", minWidth, minHeight, r.width, r.height)
	}
	title := ui.Title.Render(" pawprints ") + " " + ui.TitlePath.Render(shortHome(r.src.Root))
	if r.src.AllProjects {
		title += " " + ui.Badge.Render("all projects")
	}
	if r.touching > 0 {
		title += "  " + ui.DotBusy.Render("◉ "+fmt.Sprintf("%d touching", r.touching))
	}
	if r.waiting > 0 {
		title += "  " + ui.DotWaiting.Render("◐ "+fmt.Sprintf("%d waiting", r.waiting))
	}

	rowH := r.height - titleStatusH
	innerH := rowH - borderSize
	var items []string
	for i, p := range r.panes {
		style := ui.SidebarItem
		marker := "  "
		if i == r.idx {
			marker = "▸ "
			style = ui.SidebarItemSelected
			if r.focusSidebar {
				style = ui.SidebarItemActive
			}
		}
		label := fmt.Sprintf("%d %s", i+1, p.Title())
		if p.Title() == "Live" && r.touching+r.waiting > 0 {
			label += " " + ui.DotBusy.Render("●")
		}
		items = append(items, style.Render(ui.Truncate(marker+label, sidebarOuterW-8)))
	}
	sidebarStyle := ui.Sidebar
	if r.focusSidebar {
		sidebarStyle = ui.SidebarFocused
	}
	sidebar := sidebarStyle.Width(sidebarOuterW - borderSize - 2).Height(innerH).Render(strings.Join(items, "\n"))

	pane := r.panes[r.idx]
	bodyW := r.width - sidebarOuterW - borderSize - bodyPadH
	bodyH := innerH
	var body string
	switch {
	case r.setup != nil:
		body = r.setup.View(bodyW)
	case r.showHelp:
		body = r.helpView(pane)
	case r.src.Empty() && !r.anyLive() && pane.Title() != "Stats" && pane.Title() != "Settings":
		body = r.welcome()
	default:
		body = pane.View()
	}
	paneTitle, help := pane.Title(), pane.Help()
	if r.setup != nil {
		paneTitle, help = "Setup", ""
	}
	inner := ui.ClipBlock(ui.PaneTitle.Render(paneTitle)+"\n"+body+"\n"+help, bodyW, bodyH)
	boxStyle := ui.PaneBox
	if !r.focusSidebar {
		boxStyle = ui.PaneBoxFocused
	}
	box := boxStyle.Width(r.width - sidebarOuterW - borderSize).Height(innerH).Render(inner)
	row := lipgloss.JoinHorizontal(lipgloss.Top, sidebar, box)

	status := r.statusLine()
	sparkle := ui.Dim.Render(sparkleGlyphs[r.sparkle] + " " + buildinfo.Version)
	maxStatus := r.width - lipgloss.Width(sparkle) - 1
	if lipgloss.Width(status) > maxStatus && maxStatus > 0 {
		status = ansi.Truncate(status, maxStatus, "…")
	}
	gap := r.width - lipgloss.Width(status) - lipgloss.Width(sparkle)
	if gap < 1 {
		gap = 1
	}
	return lipgloss.JoinVertical(lipgloss.Left, title, row, status+strings.Repeat(" ", gap)+sparkle)
}

func (r *root) statusLine() string {
	if r.status != "" {
		var sty lipgloss.Style
		switch r.statusKind {
		case "ok":
			sty = ui.Ok
		case "warn":
			sty = ui.Warn
		default:
			sty = ui.Err
		}
		return sty.Render("● ") + r.status
	}
	if r.focusSidebar {
		return ui.Keys("↑/↓", "select", "→/enter", "open", "tab", "cycle", "q", "quit")
	}
	return ui.Keys("←/h", "sidebar", "tab", "next pane", "1-7", "jump", "?", "help", "q", "quit")
}

// welcome is the first-run screen shown while nothing has been recorded.
func (r *root) welcome() string {
	st := install.Check()
	var sb strings.Builder
	sb.WriteString("\n" + ui.Strong.Render("Nothing recorded yet for "+shortHome(r.src.Root)) + "\n\n")
	switch {
	case st.PluginInstalled:
		sb.WriteString("  " + ui.Ok.Render("✓") + " hooks come from the pawprints Claude Code plugin\n")
		sb.WriteString("  " + ui.Dim.Render("start a Claude Code session here and make an edit; it appears within a second") + "\n")
	case st.SettingsHooks:
		sb.WriteString("  " + ui.Ok.Render("✓") + " hooks are registered in " + st.SettingsPath + "\n")
		sb.WriteString("  " + ui.Dim.Render("start a Claude Code session here and make an edit; it appears within a second") + "\n")
	default:
		sb.WriteString("  " + ui.Err.Render("✗") + " hooks are not registered, so Claude's edits are not being recorded\n\n")
		sb.WriteString("  press " + ui.HelpKey.Render("i") + " to set up hooks, editors, and completions\n")
		sb.WriteString("  " + ui.Dim.Render("or install the plugin: /plugin marketplace add elythi0n/pawprints") + "\n")
	}
	sb.WriteString("\n  " + ui.Dim.Render("data lives in "+store.Home()+"  ·  `pawprints doctor` checks everything") + "\n")
	return sb.String()
}

func shortHome(p string) string {
	home, err := os.UserHomeDir()
	if err == nil && strings.HasPrefix(p, home) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}

// Run launches the TUI for the working directory's project.
func Run() int {
	if _, off := os.LookupEnv("NO_COLOR"); off {
		lipgloss.SetColorProfile(termenv.Ascii)
	}
	cfg, _ := store.LoadConfig()
	if cfg.Log || os.Getenv("PAWPRINTS_LOG") != "" {
		logfile.SetEnabled(true, store.Home())
		if p := logfile.Path(); p != "" {
			fmt.Fprintln(os.Stderr, "pawprints log:", p)
		}
	}
	if _, err := store.EnsureHome(); err != nil {
		fmt.Fprintln(os.Stderr, "pawprints:", err)
		return 1
	}
	if _, off := os.LookupEnv("NO_COLOR"); !off {
		ui.DetectBackground()
		if err := ui.UseTheme(ui.ThemeChoice(themeFlag())); err != nil {
			fmt.Fprintln(os.Stderr, "pawprints theme:", err)
		}
	}
	cwd, _ := os.Getwd()
	src := view.NewSource(store.ResolveRoot(cwd))
	src.Reload()
	r := newRoot(src)
	r.offerSetup()
	p := tea.NewProgram(r, tea.WithAltScreen(), tea.WithMouseCellMotion())
	r.program = p
	r.startWatch()
	defer func() {
		if r.cancel != nil {
			r.cancel()
		}
	}()
	if _, err := p.Run(); err != nil {
		logfile.Errorf("tui: %v", err)
		fmt.Fprintln(os.Stderr, "pawprints:", err)
		return 1
	}
	return 0
}

// helpView lists the shell's keys and the current pane's keys.
func (r *root) helpView(pane ui.Pane) string {
	var sb strings.Builder
	section := func(title string, rows [][2]string) {
		sb.WriteString("\n" + ui.PaneTitle.Render(title) + "\n")
		for _, row := range rows {
			sb.WriteString("  " + ui.HelpKey.Render(ui.PadRight(row[0], 18)) + " " + ui.Soft.Render(row[1]) + "\n")
		}
	}
	if h, ok := pane.(keyHelper); ok {
		section(pane.Title(), h.KeyHelp())
	}
	section("everywhere", [][2]string{
		{"tab  shift+tab", "next / previous pane"},
		{"1-7", "jump to a pane"},
		{"←  h", "focus the sidebar"},
		{"?", "this help"},
		{"q  ctrl+c", "quit"},
	})
	sb.WriteString("\n" + ui.Dim.Render("press any key to close"))
	return sb.String()
}

// themeFlag reads --theme for the TUI, which takes no other arguments.
func themeFlag() string {
	for i, a := range os.Args[1:] {
		if a == "--theme" && i+2 < len(os.Args) {
			return os.Args[i+2]
		}
		if strings.HasPrefix(a, "--theme=") {
			return strings.TrimPrefix(a, "--theme=")
		}
	}
	return ""
}

type upgradeScanMsg struct{ items []setup.Item }

// offerSetup opens the checklist on the very first run. After an upgrade it
// checks in the background and opens only if something can be updated.
func (r *root) offerSetup() {
	if _, ok := setup.LoadMarker(); !ok {
		r.setup = newSetupModel(false, true)
		return
	}
	r.upgradeCheck = true
}

// RunSetup shows only the checklist, for `pawprints setup`, and exits after.
func RunSetup() int {
	if _, err := store.EnsureHome(); err != nil {
		fmt.Fprintln(os.Stderr, "pawprints:", err)
		return 1
	}
	if _, off := os.LookupEnv("NO_COLOR"); !off {
		ui.DetectBackground()
		_ = ui.UseTheme(ui.ThemeChoice(""))
	}
	cwd, _ := os.Getwd()
	src := view.NewSource(store.ResolveRoot(cwd))
	src.Reload()
	r := newRoot(src)
	_, seen := setup.LoadMarker()
	r.setup = newSetupModel(true, !seen)
	p := tea.NewProgram(r, tea.WithAltScreen())
	r.program = p
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "pawprints:", err)
		return 1
	}
	return 0
}
