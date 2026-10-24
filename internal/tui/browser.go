package tui

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/elythi0n/pawprints/internal/diff"
	"github.com/elythi0n/pawprints/internal/panes"
	"github.com/elythi0n/pawprints/internal/restore"
	"github.com/elythi0n/pawprints/internal/sessions"
	"github.com/elythi0n/pawprints/internal/store"
	"github.com/elythi0n/pawprints/internal/ui"
	"github.com/elythi0n/pawprints/internal/view"
)

type mode int

const (
	modeList mode = iota
	modeDiff
	modeFilter
	modeConfirm
	modeMenu
	modeInput
)

// Browser is the generic tree-plus-preview pane behind Live, Recent,
// Sessions, Files, and Pins. The builder decides the structure; everything
// else is shared.
type Browser struct {
	name    string
	build   panes.Builder
	src     *view.Source
	allKey  bool // the pane offers A to toggle all projects (Live)
	autoExp bool // expand the first root on load

	roots    []*view.Node
	rows     []*view.Node
	cursor   int
	scroll   int
	expanded map[string]bool

	mode       mode
	filter     textinput.Model
	filterText string
	input      textinput.Model
	inputDone  func(string) tea.Cmd
	inputLabel string

	against  string
	preview  string
	prevKey  string
	shownKey string
	cache    map[string]string
	pv       viewport.Model // the preview beside the list
	vp       viewport.Model // the full-screen viewer
	vpTitle  string
	full     fullState
	confirm  confirmState
	menu     menuState
	w, h     int
	listW    int
}

type confirmState struct {
	title string
	lines []string
	run   func() tea.Cmd
}

type menuItem struct {
	label string
	run   func() tea.Cmd
}

type menuState struct {
	title  string
	items  []menuItem
	cursor int
}

// NewBrowser creates a pane.
func NewBrowser(name string, src *view.Source, build panes.Builder) *Browser {
	f := textinput.New()
	f.Prompt = "/ "
	f.Placeholder = "filter by path, prompt, or session"
	f.CharLimit = 80
	in := textinput.New()
	in.CharLimit = 120
	b := &Browser{
		name:     name,
		build:    build,
		src:      src,
		expanded: map[string]bool{},
		filter:   f,
		input:    in,
		against:  "previous",
		cache:    map[string]string{},
		autoExp:  true,
	}
	b.vp = viewport.New(10, 10)
	b.vp.SetHorizontalStep(8)
	b.pv = viewport.New(10, 10)
	b.full = fullState{mode: "diff", wrap: true}
	b.rebuild()
	return b
}

// Title implements ui.Pane.
func (b *Browser) Title() string { return b.name }

// Init implements ui.Pane.
func (b *Browser) Init() tea.Cmd { return b.renderPreview() }

// CapturingKeys implements ui.Capturer.
func (b *Browser) CapturingKeys() bool {
	return b.mode != modeList
}

// TypingText reports a text field has focus, where "?" is a character.
func (b *Browser) TypingText() bool {
	return b.mode == modeFilter || b.mode == modeConfirm || b.mode == modeInput
}

// SetSize implements ui.Pane.
func (b *Browser) SetSize(w, h int) {
	b.w, b.h = w, h
	b.listW = w * 45 / 100
	if b.listW < 30 {
		b.listW = 30
	}
	if b.listW > w-20 {
		b.listW = w - 20
	}
	b.vp.Width = w
	b.vp.Height = h - 2
	b.pv.Width = w - b.listW - 7
	b.pv.Height = h - 2
	b.filter.Width = b.listW - 4
	b.input.Width = w - 10
	b.cache = map[string]string{}
	b.prevKey = ""
}

// SelectKey moves the cursor to a node by key, expanding ancestors.
func (b *Browser) SelectKey(key string) {
	var path []*view.Node
	var walk func(n *view.Node, trail []*view.Node) bool
	walk = func(n *view.Node, trail []*view.Node) bool {
		trail = append(trail, n)
		if n.Key == key {
			path = append([]*view.Node{}, trail...)
			return true
		}
		for _, c := range n.Children {
			if walk(c, trail) {
				return true
			}
		}
		return false
	}
	for _, r := range b.roots {
		if walk(r, nil) {
			break
		}
	}
	for _, n := range path {
		if n.Key != key {
			b.expanded[n.Key] = true
			n.Expanded = true
		}
	}
	b.flatten()
	for i, r := range b.rows {
		if r.Key == key {
			b.cursor = i
			break
		}
	}
	b.ensureVisible()
	b.prevKey = ""
}

func (b *Browser) rebuild() {
	cur := b.selectedKey()
	b.roots = b.build(b.src)
	if b.autoExp && len(b.roots) > 0 && len(b.expanded) == 0 && !b.roots[0].Expanded {
		b.roots[0].Expanded = true
		b.expanded[b.roots[0].Key] = true
	}
	view.ApplyExpansion(b.roots, b.expanded)
	b.flatten()
	if cur != "" {
		for i, r := range b.rows {
			if r.Key == cur {
				b.cursor = i
			}
		}
	}
	if b.cursor >= len(b.rows) {
		b.cursor = len(b.rows) - 1
	}
	if b.cursor < 0 {
		b.cursor = 0
	}
	b.ensureVisible()
}

func (b *Browser) flatten() {
	roots := b.roots
	if b.filterText != "" {
		roots = view.Filter(b.roots, b.filterText)
	}
	b.rows = view.Flatten(roots)
}

func (b *Browser) selected() *view.Node {
	if b.cursor < 0 || b.cursor >= len(b.rows) {
		return nil
	}
	return b.rows[b.cursor]
}

func (b *Browser) selectedKey() string {
	if n := b.selected(); n != nil {
		return n.Key
	}
	return ""
}

func (b *Browser) listHeight() int { return b.h - 2 }

func (b *Browser) ensureVisible() {
	hgt := b.listHeight()
	if hgt < 1 {
		return
	}
	if b.cursor < b.scroll {
		b.scroll = b.cursor
	}
	if b.cursor >= b.scroll+hgt {
		b.scroll = b.cursor - hgt + 1
	}
	if b.scroll < 0 {
		b.scroll = 0
	}
}

// selectedEvent returns the event behind the cursor, for version and pin rows.
func (b *Browser) selectedEvent() *store.Event {
	n := b.selected()
	if n == nil {
		return nil
	}
	return n.Event
}

// Update implements ui.Pane.
func (b *Browser) Update(msg tea.Msg) (ui.Pane, tea.Cmd) {
	switch m := msg.(type) {
	case ReloadMsg:
		b.cache = map[string]string{}
		b.rebuild()
		b.prevKey = ""
		if b.mode == modeDiff {
			return b, tea.Batch(b.renderPreview(), b.renderFull())
		}
		return b, b.renderPreview()
	case previewMsg:
		if m.pane == b.name && m.key == b.previewKey() {
			b.cache[m.key] = m.text
			b.showPreview(m.key, m.text)
		}
		return b, nil
	case fullMsg:
		if m.pane == b.name && m.key == b.full.key {
			b.vp.SetContent(m.text)
			b.full.hunks = m.hunks
			b.full.header = m.header
			if b.full.jumpFirst && len(m.hunks) > 0 {
				b.vp.SetYOffset(max(0, m.hunks[0]-3))
				b.full.at = 0
			}
			b.full.jumpFirst = false
		}
		return b, nil
	case tea.MouseMsg:
		return b.updateMouse(m)
	case actionDoneMsg:
		if m.kind == "err" && m.ev != nil && strings.Contains(m.text, "changed since") {
			return b, b.askThreeWay(*m.ev, m.text)
		}
		return b, done(m.kind, m.text)
	case confirmRequestMsg:
		if m.pane == b.name {
			return b.ask(m.title, m.lines, m.run)
		}
		return b, nil
	case tea.KeyMsg:
		switch b.mode {
		case modeFilter:
			return b.updateFilter(m)
		case modeConfirm:
			return b.updateConfirm(m)
		case modeMenu:
			return b.updateMenu(m)
		case modeInput:
			return b.updateInput(m)
		case modeDiff:
			return b.updateDiff(m)
		}
		return b.updateList(m)
	}
	if b.mode == modeDiff {
		var cmd tea.Cmd
		b.vp, cmd = b.vp.Update(msg)
		return b, cmd
	}
	return b, nil
}

func (b *Browser) updateList(m tea.KeyMsg) (ui.Pane, tea.Cmd) {
	n := b.selected()
	switch m.String() {
	case "up", "k":
		if b.cursor > 0 {
			b.cursor--
		}
	case "down", "j":
		if b.cursor < len(b.rows)-1 {
			b.cursor++
		}
	case "pgup", "ctrl+u":
		b.cursor -= b.listHeight() / 2
		if b.cursor < 0 {
			b.cursor = 0
		}
	case "pgdown", "ctrl+d":
		b.cursor += b.listHeight() / 2
		if b.cursor > len(b.rows)-1 {
			b.cursor = len(b.rows) - 1
		}
	case "g", "home":
		b.cursor = 0
	case "G", "end":
		b.cursor = len(b.rows) - 1
	case "right", "l":
		if n != nil && n.IsGroup() && !n.Expanded {
			b.toggle(n)
		}
	case "left":
		if n != nil && n.IsGroup() && n.Expanded {
			b.toggle(n)
		} else if n != nil && n.Depth > 0 {
			b.selectParent()
		}
	case "enter":
		if n == nil {
			return b, nil
		}
		if n.IsGroup() {
			b.toggle(n)
		} else if n.Event != nil {
			b.full.mode = "diff"
			return b.openDiff()
		}
	case "a":
		if b.against == "previous" {
			b.against = "current"
		} else {
			b.against = "previous"
		}
		b.cache = map[string]string{}
		b.prevKey = ""
		return b, tea.Batch(flash("ok", "diff against "+b.against), b.renderPreview())
	case "/":
		b.mode = modeFilter
		b.filter.SetValue(b.filterText)
		b.filter.Focus()
		return b, textinput.Blink
	case "esc":
		if b.filterText != "" {
			b.filterText = ""
			b.rebuild()
		}
	case "A":
		if b.allKey {
			return b, func() tea.Msg { return ToggleAllProjectsMsg{} }
		}
	case "c":
		if ev := b.selectedEvent(); ev != nil && b.src.Project != nil {
			return b, copyContent(b.src, *ev)
		}
	case "y":
		if ev := b.selectedEvent(); ev != nil {
			return b, copyReference(*ev)
		}
	case "Y":
		if ev := b.selectedEvent(); ev != nil && b.src.Project != nil {
			return b, copySnippet(b.src, *ev)
		}
	case "r":
		return b.askRestore()
	case "u":
		return b.askUndo()
	case "p":
		if ev := b.selectedEvent(); ev != nil && b.src.Project != nil {
			if b.src.Pinned(ev.ID) {
				return b, togglePin(b.src, *ev)
			}
			return b.askInput("pin message (optional)", func(text string) tea.Cmd { return pinWithMessage(b.src, *ev, text) })
		}
	case "o":
		if ev := b.selectedEvent(); ev != nil && b.src.Project != nil {
			return b, openInEditor(b.src, *ev)
		}
	case "e":
		if ev := b.selectedEvent(); ev != nil && b.src.Project != nil {
			return b, handOff(b.src, *ev)
		}
	case "t":
		if n != nil && n.Session != nil {
			return b, openTranscript(n.Session.Transcript)
		}
	case "x":
		return b.exportSelected()
	case "D":
		return b.openClearMenu()
	case "L":
		if b.name == "Live" || b.name == "Sessions" {
			return b, toggleLayout()
		}
	case "J", "shift+down", "ctrl+e":
		b.pv.ScrollDown(3)
		return b, nil
	case "K", "shift+up", "ctrl+y":
		b.pv.ScrollUp(3)
		return b, nil
	case "f":
		if b.selectedEvent() != nil {
			b.full.mode = "file"
			return b.openDiff()
		}
	}
	b.ensureVisible()
	return b, b.renderPreview()
}

func (b *Browser) toggle(n *view.Node) {
	n.Expanded = !n.Expanded
	b.expanded[n.Key] = n.Expanded
	key := n.Key
	b.flatten()
	for i, r := range b.rows {
		if r.Key == key {
			b.cursor = i
		}
	}
}

func (b *Browser) selectParent() {
	depth := b.rows[b.cursor].Depth
	for i := b.cursor - 1; i >= 0; i-- {
		if b.rows[i].Depth < depth {
			b.cursor = i
			return
		}
	}
}

func (b *Browser) updateFilter(m tea.KeyMsg) (ui.Pane, tea.Cmd) {
	switch m.String() {
	case "esc":
		b.mode = modeList
		b.filter.Blur()
		b.filterText = ""
		b.rebuild()
		return b, b.renderPreview()
	case "enter":
		b.mode = modeList
		b.filter.Blur()
		return b, b.renderPreview()
	}
	var cmd tea.Cmd
	b.filter, cmd = b.filter.Update(m)
	b.filterText = strings.TrimSpace(b.filter.Value())
	b.cursor = 0
	b.scroll = 0
	b.flatten()
	return b, tea.Batch(cmd, b.renderPreview())
}

func (b *Browser) askInput(label string, run func(string) tea.Cmd) (ui.Pane, tea.Cmd) {
	b.mode = modeInput
	b.inputLabel = label
	b.inputDone = run
	b.input.SetValue("")
	b.input.Focus()
	return b, textinput.Blink
}

func (b *Browser) updateInput(m tea.KeyMsg) (ui.Pane, tea.Cmd) {
	switch m.String() {
	case "esc":
		b.mode = modeList
		b.input.Blur()
		return b, nil
	case "enter":
		b.mode = modeList
		b.input.Blur()
		return b, b.inputDone(strings.TrimSpace(b.input.Value()))
	}
	var cmd tea.Cmd
	b.input, cmd = b.input.Update(m)
	return b, cmd
}

func (b *Browser) ask(title string, lines []string, run func() tea.Cmd) (ui.Pane, tea.Cmd) {
	b.mode = modeConfirm
	b.confirm = confirmState{title: title, lines: lines, run: run}
	b.input.SetValue("")
	b.input.Placeholder = "type yes"
	b.input.Focus()
	return b, textinput.Blink
}

func (b *Browser) updateConfirm(m tea.KeyMsg) (ui.Pane, tea.Cmd) {
	switch m.String() {
	case "esc", "ctrl+c":
		b.mode = modeList
		b.input.Blur()
		b.input.Placeholder = ""
		return b, flash("warn", "cancelled")
	case "enter":
		if strings.TrimSpace(strings.ToLower(b.input.Value())) == "yes" {
			b.mode = modeList
			b.input.Blur()
			b.input.Placeholder = ""
			run := b.confirm.run
			return b, run()
		}
		return b, nil
	}
	var cmd tea.Cmd
	b.input, cmd = b.input.Update(m)
	return b, cmd
}

func (b *Browser) openMenu(title string, items []menuItem) (ui.Pane, tea.Cmd) {
	b.mode = modeMenu
	b.menu = menuState{title: title, items: items}
	return b, nil
}

func (b *Browser) updateMenu(m tea.KeyMsg) (ui.Pane, tea.Cmd) {
	switch m.String() {
	case "esc", "q":
		b.mode = modeList
		return b, nil
	case "up", "k":
		if b.menu.cursor > 0 {
			b.menu.cursor--
		}
	case "down", "j":
		if b.menu.cursor < len(b.menu.items)-1 {
			b.menu.cursor++
		}
	case "enter":
		b.mode = modeList
		return b, b.menu.items[b.menu.cursor].run()
	default:
		if len(m.String()) == 1 && m.String()[0] >= '1' && m.String()[0] <= '9' {
			i := int(m.String()[0] - '1')
			if i < len(b.menu.items) {
				b.mode = modeList
				return b, b.menu.items[i].run()
			}
		}
	}
	return b, nil
}

func (b *Browser) openDiff() (ui.Pane, tea.Cmd) {
	ev := b.selectedEvent()
	if ev == nil {
		return b, nil
	}
	b.mode = modeDiff
	b.vpTitle = ev.Path + "  " + ui.Dim.Render(ev.Time().Local().Format("2006-01-02 15:04:05")+"  "+toolLabel(*ev)+"  "+shortID(ev.ID))
	if n := b.selected(); n != nil && n.Edits > 1 {
		b.vpTitle += "  " + ui.Accent.Render(fmt.Sprintf("%d edits combined", n.Edits))
	}
	b.vp.GotoTop()
	b.vp.SetXOffset(0)
	b.full.jumpFirst = true
	b.vp.SetContent(ui.Dim.Render("rendering…"))
	return b, b.renderFull()
}

// renderFull renders the full-screen view off the event loop.
func (b *Browser) renderFull() tea.Cmd {
	ev := b.selectedEvent()
	if ev == nil {
		return nil
	}
	e := *ev
	src, against, mode, wrap, width, name := b.src, b.against, b.full.mode, b.full.wrap, b.vp.Width, b.name
	key := fmt.Sprintf("%s|%s|%s|%v|%d", e.ID, against, mode, wrap, width)
	b.full.key = key
	return func() tea.Msg {
		header, text, hunks := renderFullView(src, e, against, mode, wrap, width)
		return fullMsg{pane: name, key: key, header: header, text: text, hunks: hunks}
	}
}

func (b *Browser) updateDiff(m tea.KeyMsg) (ui.Pane, tea.Cmd) {
	switch m.String() {
	case "esc", "q", "enter":
		b.mode = modeList
		return b, nil
	case "a":
		b.against = other(b.against)
		b.cache = map[string]string{}
		b.prevKey = ""
		b.full.jumpFirst = true
		return b, tea.Batch(flash("ok", "comparing against "+b.against), b.renderFull(), b.renderPreview())
	case "f":
		if b.full.mode == "diff" {
			b.full.mode = "file"
		} else {
			b.full.mode = "diff"
		}
		b.full.jumpFirst = true
		return b, b.renderFull()
	case "w":
		b.full.wrap = !b.full.wrap
		b.vp.SetXOffset(0)
		return b, b.renderFull()
	case "n", "tab":
		b.jumpChange(1)
	case "N", "p", "shift+tab":
		b.jumpChange(-1)
	case "c", "y", "Y", "r", "o", "e":
		b.mode = modeList
		return b.updateList(m)
	case "j", "down":
		b.vp.ScrollDown(1)
	case "k", "up":
		b.vp.ScrollUp(1)
	case "h", "left":
		if !b.full.wrap {
			b.vp.ScrollLeft(8)
		}
	case "l", "right":
		if !b.full.wrap {
			b.vp.ScrollRight(8)
		}
	case "d", "ctrl+d":
		b.vp.HalfPageDown()
	case "u", "ctrl+u":
		b.vp.HalfPageUp()
	case " ", "pgdown":
		b.vp.PageDown()
	case "b", "pgup":
		b.vp.PageUp()
	case "g", "home":
		b.vp.GotoTop()
	case "G", "end":
		b.vp.GotoBottom()
	}
	return b, nil
}

// jumpChange scrolls to the next or previous run of changes.
func (b *Browser) jumpChange(dir int) {
	if len(b.full.hunks) == 0 {
		return
	}
	top := b.vp.YOffset + 3
	target := -1
	if dir > 0 {
		for i, h := range b.full.hunks {
			if h > top {
				target = i
				break
			}
		}
		if target < 0 {
			target = 0
		}
	} else {
		for i := len(b.full.hunks) - 1; i >= 0; i-- {
			if b.full.hunks[i] < top {
				target = i
				break
			}
		}
		if target < 0 {
			target = len(b.full.hunks) - 1
		}
	}
	b.full.at = target
	b.vp.SetYOffset(max(0, b.full.hunks[target]-3))
}

// updateMouse scrolls whatever is under the wheel.
func (b *Browser) updateMouse(m tea.MouseMsg) (ui.Pane, tea.Cmd) {
	if m.Action != tea.MouseActionPress {
		return b, nil
	}
	up := m.Button == tea.MouseButtonWheelUp
	down := m.Button == tea.MouseButtonWheelDown
	if !up && !down {
		return b, nil
	}
	if b.mode == modeDiff {
		if up {
			b.vp.ScrollUp(3)
		} else {
			b.vp.ScrollDown(3)
		}
		return b, nil
	}
	if b.mode != modeList {
		return b, nil
	}
	// The list starts after the sidebar and the body's border and padding.
	if m.X > sidebarOuterW+3+b.listW {
		if up {
			b.pv.ScrollUp(3)
		} else {
			b.pv.ScrollDown(3)
		}
		return b, nil
	}
	if up && b.cursor > 0 {
		b.cursor--
	} else if down && b.cursor < len(b.rows)-1 {
		b.cursor++
	}
	b.ensureVisible()
	return b, b.renderPreview()
}

// showPreview puts rendered text in the preview, keeping the scroll position
// when the same node is re-rendered and starting at the top for a new one.
func (b *Browser) showPreview(key, text string) {
	b.preview = text
	b.pv.SetContent(text)
	node := strings.SplitN(key, "|", 2)[0]
	if node != b.shownKey {
		b.pv.GotoTop()
		b.shownKey = node
	}
}

// previewKey identifies what the preview should show.
func (b *Browser) previewKey() string {
	n := b.selected()
	if n == nil {
		return ""
	}
	return fmt.Sprintf("%s|%s|%d", n.Key, b.against, b.w)
}

// renderPreview computes the right pane content for the selection off the
// event loop and caches it per node and diff target.
func (b *Browser) renderPreview() tea.Cmd {
	key := b.previewKey()
	if key == "" || key == b.prevKey {
		return nil
	}
	b.prevKey = key
	if text, ok := b.cache[key]; ok {
		b.showPreview(key, text)
		return nil
	}
	n := b.selected()
	src := b.src
	against := b.against
	width := b.pv.Width
	name := b.name
	return func() tea.Msg {
		return previewMsg{pane: name, key: key, text: renderNode(src, n, against, width)}
	}
}

func renderNode(src *view.Source, n *view.Node, against string, width int) string {
	switch n.Kind {
	case view.KindVersion, view.KindPin:
		if n.Event == nil {
			return ui.Dim.Render("event no longer in history")
		}
		text := renderEventPreview(src, *n.Event, against, width)
		if n.Edits > 1 {
			// The note goes under the header line so the file name stays first.
			lines := strings.SplitN(text, "\n", 2)
			note := ui.Accent.Render(fmt.Sprintf("%d edits combined", n.Edits)) + ui.Dim.Render(" · from before the first to after the last; v in Settings lists each")
			if len(lines) == 2 {
				return lines[0] + "\n" + note + "\n" + lines[1]
			}
			return text + "\n" + note
		}
		return text
	case view.KindSession:
		return renderSession(src, *n.Session, width)
	case view.KindTurn:
		return renderTurn(n, width)
	case view.KindFile:
		var sb strings.Builder
		sb.WriteString(ui.PaneTitle.Render(n.Path) + "\n\n")
		for _, c := range n.Children {
			fmt.Fprintf(&sb, "%s  %s\n", c.Label, ui.Dim.Render(c.Desc))
		}
		return sb.String()
	case view.KindGroup:
		if n.Agent != nil {
			return renderAgent(*n.Agent, n.Desc, width)
		}
		return ui.Dim.Render(fmt.Sprintf("%s · %d entries", n.Label, len(n.Children)))
	}
	return ""
}

// renderAgent previews a subagent: its task, when it ran, and its report.
func renderAgent(a store.Agent, desc string, width int) string {
	var sb strings.Builder
	sb.WriteString(ui.PaneTitle.Render("◇ "+a.Name()) + "\n")
	meta := []string{}
	if a.Type != "" {
		meta = append(meta, a.Type)
	}
	if when := agentTimes(a); when != "" {
		meta = append(meta, when)
	}
	sb.WriteString(ui.Dim.Render(strings.Join(meta, " · ")) + "\n")
	switch {
	case a.Report != "":
		sb.WriteString("\n" + wrapLines(a.Report, width-2) + "\n")
	case a.Summary != "":
		sb.WriteString("\n" + wrap(a.Summary, width-2) + "\n")
	case a.StoppedAt == "":
		sb.WriteString("\n" + ui.Dim.Render("still running; its report shows here when it finishes") + "\n")
	default:
		sb.WriteString("\n" + ui.Dim.Render(desc) + "\n")
	}
	return sb.String()
}

// wrapLines wraps each line of s on its own, keeping lists and paragraphs.
func wrapLines(s string, width int) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if strings.TrimSpace(l) != "" {
			indent := l[:len(l)-len(strings.TrimLeft(l, " "))]
			lines[i] = indent + strings.ReplaceAll(wrap(l, width-len(indent)), "\n", "\n"+indent+"  ")
		}
	}
	return strings.Join(lines, "\n")
}

// agentTimes is "15:10–15:19", or "15:10 running".
func agentTimes(a store.Agent) string {
	start, err := time.Parse(time.RFC3339Nano, a.StartedAt)
	if err != nil {
		return ""
	}
	when := start.Local().Format("15:04")
	if stop, err := time.Parse(time.RFC3339Nano, a.StoppedAt); err == nil {
		return when + "–" + stop.Local().Format("15:04")
	}
	return when + " " + ui.DotBusy.Render("running")
}

func renderEventPreview(src *view.Source, ev store.Event, against string, width int) string {
	if src.Project == nil {
		return ""
	}
	var head strings.Builder
	head.WriteString(ui.PaneTitle.Render(ev.Path) + "  " + ui.Dim.Render(shortID(ev.ID)) + "\n")
	meta := []string{
		ev.Time().Local().Format("2006-01-02 15:04:05"),
		toolLabel(ev),
		"session " + shortID(ev.Session),
	}
	if t, ok := src.Turn(ev.Turn); ok && t.Text != "" {
		meta = append(meta, "“"+ui.Truncate(t.Text, 50)+"”")
	}
	if src.Pinned(ev.ID) {
		meta = append(meta, ui.Badge.Render("pinned"))
	}
	head.WriteString(ui.Dim.Render(strings.Join(meta, " · ")) + "\n")
	if ev.Command != "" {
		head.WriteString(ui.Dim.Render("$ ") + ui.Soft.Render(ui.Truncate(ev.Command, width-2)) + "\n")
	}
	if ev.BeforeUnknown {
		head.WriteString(ui.Warn.Render("first seen after a shell command; no earlier copy to compare") + "\n")
	}
	if ev.Skipped != "" {
		return head.String() + "\n" + ui.Warn.Render("content not stored: "+ev.Skipped)
	}
	after, err := src.Project.Get(ev.AfterHash())
	if err != nil {
		return head.String() + "\n" + ui.Err.Render(err.Error())
	}
	var base []byte
	var aLabel, bLabel string
	if against == "current" {
		base = after
		after, _ = os.ReadFile(src.Project.Abs(ev.Path))
		aLabel, bLabel = "this version", "working file"
		head.WriteString(ui.Dim.Render("against the working file") + "\n")
	} else {
		base, _ = src.Project.Get(ev.BeforeHash())
		aLabel, bLabel = "before", "after"
	}
	if store.IsBinary(base) || store.IsBinary(after) {
		return head.String() + "\n" + ui.Dim.Render("binary content")
	}
	_, _ = aLabel, bLabel
	lines := diff.Numbered(base, after, 3)
	if len(lines) == 0 {
		return head.String() + "\n" + ui.Dim.Render("no differences")
	}
	st := diff.Count(base, after)
	head.WriteString(ui.Added.Render(fmt.Sprintf("+%d", st.Added)) + " " + ui.Removed.Render(fmt.Sprintf("-%d", st.Removed)) + "\n\n")
	body, _ := renderLines(lines, width, true)
	return head.String() + body
}

func renderSession(src *view.Source, se sessions.Session, width int) string {
	var sb strings.Builder
	sb.WriteString(statusDot(se) + " " + ui.PaneTitle.Render(se.Name) + "  " + ui.Dim.Render(shortID(se.ID)) + "\n\n")
	row := func(k, v string) {
		if v != "" {
			fmt.Fprintf(&sb, "%s %s\n", ui.Dim.Render(ui.PadRight(k, 10)), v)
		}
	}
	status := se.Status
	if se.WaitingFor != "" {
		status += ": " + se.WaitingFor
	}
	row("status", status)
	row("project", se.Project)
	if se.PID != 0 {
		row("pid", fmt.Sprint(se.PID))
	}
	row("started", relOrEmpty(se.StartedAt, src.Now))
	row("last edit", relOrEmpty(se.LastEventAt, src.Now))
	row("ended", relOrEmpty(se.EndedAt, src.Now))
	row("edits", fmt.Sprintf("%d in %d files, %s %s", se.Edits, len(se.Files), ui.Added.Render(fmt.Sprintf("+%d", se.LinesAdded)), ui.Removed.Render(fmt.Sprintf("-%d", se.LinesRemoved))))
	if len(se.Usage) > 0 {
		for model, u := range se.Usage {
			row("tokens", fmt.Sprintf("%s: in %s · out %s · cache %s", model, humanCount(u.Input), humanCount(u.Output), humanCount(u.CacheRead)))
		}
	}
	row("sources", strings.Join(se.Sources, ", "))
	if len(se.Agents) > 0 {
		sb.WriteString("\n" + ui.Dim.Render("subagents") + "\n")
		for _, a := range se.Agents {
			kind := ""
			if a.Description != "" {
				kind = a.Type + "  "
			}
			sb.WriteString("  ◇ " + ui.Strong.Render(a.Name()) + "  " + ui.Dim.Render(kind+agentTimes(a)) + "\n")
			if a.Summary != "" {
				sb.WriteString("    " + ui.Soft.Render(ui.Truncate(a.Summary, width-6)) + "\n")
			}
		}
	}
	if se.FirstPrompt != "" {
		sb.WriteString("\n" + ui.Dim.Render("first prompt") + "\n  " + wrap(se.FirstPrompt, width-4) + "\n")
	}
	if se.LastPrompt != "" && se.LastPrompt != se.FirstPrompt {
		sb.WriteString(ui.Dim.Render("last prompt") + "\n  " + wrap(se.LastPrompt, width-4) + "\n")
	}
	if len(se.Files) > 0 {
		sb.WriteString("\n" + ui.Dim.Render("files") + "\n")
		for i, f := range se.Files {
			if i >= 20 {
				sb.WriteString(ui.Dim.Render(fmt.Sprintf("  … %d more\n", len(se.Files)-20)))
				break
			}
			sb.WriteString("  " + f + "\n")
		}
	}
	return sb.String()
}

func renderTurn(n *view.Node, width int) string {
	var sb strings.Builder
	if n.Turn != nil {
		sb.WriteString(ui.PaneTitle.Render("prompt") + "  " + ui.Dim.Render(shortID(n.Turn.ID)) + "\n\n")
		text := n.Turn.Text
		if text == "" {
			text = ui.Dim.Render("(not recorded)")
		}
		sb.WriteString(wrap(text, width-2) + "\n\n")
	} else {
		sb.WriteString(ui.PaneTitle.Render(n.Label) + "\n\n")
	}
	if len(n.Children) == 0 {
		sb.WriteString(ui.Dim.Render("no edits in this turn"))
	}
	for _, f := range n.Children {
		fmt.Fprintf(&sb, "%s  %s\n", f.Label, ui.Dim.Render(f.Desc))
	}
	sb.WriteString("\n" + ui.Dim.Render("u undo this turn · x export as patch"))
	return sb.String()
}

func relOrEmpty(ts string, now time.Time) string {
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil || t.IsZero() {
		return ""
	}
	return t.Local().Format("2006-01-02 15:04") + " " + ui.Dim.Render("("+panes.Ago(t, now)+")")
}

func wrap(s string, width int) string {
	if width < 10 {
		width = 10
	}
	words := strings.Fields(s)
	var lines []string
	line := ""
	for _, w := range words {
		if lipgloss.Width(line)+lipgloss.Width(w)+1 > width && line != "" {
			lines = append(lines, line)
			line = w
			continue
		}
		if line == "" {
			line = w
		} else {
			line += " " + w
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n  ")
}

// View implements ui.Pane.
func (b *Browser) View() string {
	switch b.mode {
	case modeDiff:
		return b.viewFull()
	case modeConfirm:
		return b.viewConfirm()
	case modeMenu:
		return b.viewMenu()
	case modeInput:
		return ui.PaneTitle.Render(b.inputLabel) + "\n\n" + b.input.View() + "\n\n" + ui.Dim.Render("enter confirm · esc cancel")
	}
	left := b.viewList()
	rightW := b.w - b.listW - 3
	right := ui.Preview.Width(rightW - 2).Height(b.h - 2).Render(ui.ClipBlock(b.pv.View(), rightW-4, b.h-2))
	return lipgloss.JoinHorizontal(lipgloss.Top, left, " ", right)
}

func (b *Browser) viewFull() string {
	var tags []string
	if b.full.mode == "file" {
		tags = append(tags, ui.Badge.Render("whole file"))
	} else {
		tags = append(tags, ui.Badge.Render("changes"))
	}
	tags = append(tags, ui.Dim.Render("vs "+b.against))
	if !b.full.wrap {
		tags = append(tags, ui.Dim.Render("no wrap"))
	}
	if n := len(b.full.hunks); n > 0 {
		tags = append(tags, ui.Dim.Render(fmt.Sprintf("change %d/%d", b.full.at+1, n)))
	}
	tags = append(tags, ui.Dim.Render(fmt.Sprintf("%3.0f%%", b.vp.ScrollPercent()*100)))
	head := b.vpTitle
	if b.full.header != "" {
		head += "  " + b.full.header
	}
	top := ui.Truncate(head, b.w) + "\n" + ui.Truncate(strings.Join(tags, "  "), b.w)
	return top + "\n" + b.vp.View()
}

func (b *Browser) viewList() string {
	var sb strings.Builder
	hgt := b.listHeight()
	if b.mode == modeFilter || b.filterText != "" {
		sb.WriteString(b.filter.View() + "\n")
		hgt--
	}
	end := b.scroll + hgt
	if end > len(b.rows) {
		end = len(b.rows)
	}
	if hgt > 0 && b.scroll < len(b.rows) {
		for i := b.scroll; i < end; i++ {
			sb.WriteString(b.renderRow(b.rows[i], i == b.cursor) + "\n")
		}
	}
	if len(b.rows) > end {
		sb.WriteString(ui.Dim.Render(fmt.Sprintf("  … %d more", len(b.rows)-end)))
	}
	return lipgloss.NewStyle().Width(b.listW).Height(b.h - 2).Render(strings.TrimRight(sb.String(), "\n"))
}

func (b *Browser) renderRow(n *view.Node, selected bool) string {
	indent := strings.Repeat("  ", n.Depth)
	marker := "  "
	switch {
	case n.IsGroup() && n.Expanded:
		marker = "▾ "
	case n.IsGroup():
		marker = "▸ "
	case n.Kind == view.KindVersion || n.Kind == view.KindPin:
		marker = "· "
	}
	label := n.Label
	if n.Kind == view.KindSession && n.Session != nil {
		marker = statusDot(*n.Session) + " "
	}
	if n.Kind == view.KindNotice {
		return ui.Dim.Render(ui.Truncate(indent+label, b.listW))
	}
	avail := b.listW - lipgloss.Width(indent) - 4
	desc := n.Desc
	text := ui.Truncate(label, avail)
	if n.Path != "" && label == n.Path {
		// Keep the file name, since long paths often share their whole
		// start, and leave room for the time and line counts after it.
		pathW := avail
		if desc != "" {
			pathW = avail - min(lipgloss.Width(desc)+2, avail*45/100)
		}
		text = ui.TruncateLeft(label, pathW)
	}
	rest := avail - lipgloss.Width(text) - 2
	if desc != "" && rest > 8 {
		text += "  " + ui.Dim.Render(ui.Truncate(desc, rest))
	}
	if selected {
		return ui.Cursor.Render(indent+"▍") + lipgloss.NewStyle().Background(lipgloss.Color(ui.Surface)).Render(ui.PadRight(marker+text, avail))
	}
	style := lipgloss.NewStyle()
	if n.Kind == view.KindVersion && n.Event != nil && n.Event.Unchanged() {
		style = ui.Dim
	}
	return indent + " " + style.Render(marker+text)
}

func (b *Browser) viewConfirm() string {
	var sb strings.Builder
	sb.WriteString(ui.Warn.Render(b.confirm.title) + "\n\n")
	for _, l := range b.confirm.lines {
		sb.WriteString("  " + l + "\n")
	}
	sb.WriteString("\n" + b.input.View() + "\n\n" + ui.Dim.Render("type yes and press enter · esc cancels"))
	return sb.String()
}

func (b *Browser) viewMenu() string {
	var sb strings.Builder
	sb.WriteString(ui.PaneTitle.Render(b.menu.title) + "\n\n")
	for i, it := range b.menu.items {
		cur := "  "
		if i == b.menu.cursor {
			cur = ui.Cursor.Render("▍ ")
		}
		fmt.Fprintf(&sb, "%s%s %s\n", cur, ui.Dim.Render(fmt.Sprintf("%d", i+1)), it.label)
	}
	sb.WriteString("\n" + ui.Dim.Render("enter choose · esc cancel"))
	return sb.String()
}

// Help implements ui.Pane.
func (b *Browser) Help() string {
	switch b.mode {
	case modeDiff:
		toggle := "whole file"
		if b.full.mode == "file" {
			toggle = "changes only"
		}
		keys := []string{"n/N", "next/prev change", "f", toggle, "w", "wrap", "a", "vs " + other(b.against)}
		if !b.full.wrap {
			keys = append(keys, "h/l", "sideways")
		}
		return ui.Keys(append(keys, "r", "restore", "esc", "back", "?", "all keys")...)
	case modeFilter:
		return ui.Keys("enter", "keep filter", "esc", "clear")
	case modeConfirm, modeInput, modeMenu:
		return ""
	}
	n := b.selected()
	if n == nil {
		return ""
	}
	switch n.Kind {
	case view.KindVersion, view.KindPin:
		return ui.Keys("enter", "full view", "f", "whole file", "J/K", "scroll preview", "r", "restore", "u", "undo", "c", "copy", "?", "all keys")
	case view.KindTurn:
		return ui.Keys("enter", "expand", "u", "undo turn", "x", "export", "J/K", "scroll preview", "?", "all keys")
	case view.KindSession:
		keys := []string{"enter", "expand", "u", "undo session", "x", "export", "t", "transcript"}
		if b.allKey {
			keys = append(keys, "A", "all projects")
		}
		return ui.Keys(append(keys, "?", "all keys")...)
	}
	return ui.Keys("enter", "expand", "/", "filter", "D", "clear", "?", "all keys")
}

// KeyHelp lists every key for the help overlay.
func (b *Browser) KeyHelp() [][2]string {
	if b.mode == modeDiff {
		return [][2]string{
			{"j/k  ↑/↓", "scroll a line"},
			{"d/u  space/b", "half page / page"},
			{"g/G", "top / bottom"},
			{"n/N  tab", "next / previous change"},
			{"f", "toggle changes ↔ whole file"},
			{"w", "toggle line wrap"},
			{"h/l  ←/→", "scroll sideways when wrap is off"},
			{"a", "compare with the previous version or the working file"},
			{"mouse wheel", "scroll"},
			{"c  y  Y", "copy content / reference / markdown snippet"},
			{"r  o  e", "restore / open in $EDITOR / open in VS Code or Cursor"},
			{"esc  q  enter", "back to the list"},
		}
	}
	keys := [][2]string{
		{"j/k  ↑/↓", "move"},
		{"→  ←", "expand / collapse; ← on a child goes to its parent"},
		{"enter", "expand, or open a version full screen"},
		{"f", "open a version as the whole file with changes marked"},
		{"J/K  shift+↑/↓", "scroll the preview"},
		{"mouse wheel", "move in the list, scroll over the preview"},
		{"a", "compare with the previous version or the working file"},
		{"/", "filter by path, prompt, or session"},
		{"c  y  Y", "copy content / reference / markdown snippet"},
		{"r", "restore this version"},
		{"u", "undo: this edit, a whole turn, or a whole session"},
		{"p", "pin or unpin"},
		{"x", "export a turn or session as a patch"},
		{"o  e", "open in $EDITOR / in VS Code or Cursor"},
		{"t", "open the session transcript"},
		{"D", "clear history"},
	}
	if b.name == "Live" || b.name == "Sessions" {
		keys = append(keys, [2]string{"L", "files under a prompt: flat list ↔ inside their folders"})
	}
	if b.allKey {
		keys = append(keys, [2]string{"A", "this project ↔ all projects"})
	}
	return keys
}

func other(against string) string {
	if against == "previous" {
		return "current"
	}
	return "previous"
}

// askRestore plans a restore for the selected version and asks.
func (b *Browser) askRestore() (ui.Pane, tea.Cmd) {
	ev := b.selectedEvent()
	if ev == nil || b.src.Project == nil {
		return b, nil
	}
	plan, err := restore.Restore(b.src.Project, *ev, restore.After, restore.Options{DryRun: true})
	if err != nil {
		return b, b.askThreeWay(*ev, err.Error())
	}
	lines := changeLines(plan)
	e := *ev
	return b.ask("restore "+ev.Path+" to this version?", lines, func() tea.Cmd {
		return doRestore(b.src, e, restore.After, restore.Options{})
	})
}

// askThreeWay offers merge or overwrite when the file changed since.
func (b *Browser) askThreeWay(ev store.Event, reason string) tea.Cmd {
	e := ev
	_, cmd := b.openMenu(reason, []menuItem{
		{"merge three-way (keeps your edits, may leave conflict markers)", func() tea.Cmd {
			return doRestore(b.src, e, restore.After, restore.Options{ThreeWay: true})
		}},
		{"overwrite with this version", func() tea.Cmd {
			return doRestore(b.src, e, restore.After, restore.Options{Force: true})
		}},
		{"cancel", func() tea.Cmd { return flash("warn", "cancelled") }},
	})
	return cmd
}

func (b *Browser) askUndo() (ui.Pane, tea.Cmd) {
	n := b.selected()
	if n == nil || b.src.Project == nil {
		return b, nil
	}
	var scope restore.UndoScope
	var title string
	switch n.Kind {
	case view.KindTurn:
		if n.Turn == nil {
			return b, flash("warn", "edits before the first prompt have no turn to undo")
		}
		scope.Turn = n.Turn.ID
		title = "undo turn “" + ui.Truncate(n.Turn.Text, 40) + "”?"
	case view.KindSession:
		scope.Session = n.Session.ID
		title = "undo everything session " + n.Session.Name + " did?"
	case view.KindVersion, view.KindPin:
		if n.Event == nil {
			return b, nil
		}
		return b.askRestoreBefore(*n.Event)
	default:
		return b, nil
	}
	plan, err := restore.Undo(b.src.Project, scope, restore.Options{DryRun: true})
	if err != nil {
		if strings.Contains(err.Error(), "changed since") {
			sc := scope
			_, cmd := b.openMenu(err.Error(), []menuItem{
				{"merge three-way", func() tea.Cmd { return doUndo(b.src, sc, restore.Options{ThreeWay: true}) }},
				{"overwrite", func() tea.Cmd { return doUndo(b.src, sc, restore.Options{Force: true}) }},
				{"cancel", func() tea.Cmd { return flash("warn", "cancelled") }},
			})
			return b, cmd
		}
		return b, flash("err", err.Error())
	}
	sc := scope
	return b.ask(title, changeLines(plan), func() tea.Cmd { return doUndo(b.src, sc, restore.Options{}) })
}

func (b *Browser) askRestoreBefore(ev store.Event) (ui.Pane, tea.Cmd) {
	plan, err := restore.Restore(b.src.Project, ev, restore.Before, restore.Options{DryRun: true})
	if err != nil {
		e := ev
		_, cmd := b.openMenu(err.Error(), []menuItem{
			{"merge three-way", func() tea.Cmd { return doRestore(b.src, e, restore.Before, restore.Options{ThreeWay: true}) }},
			{"overwrite", func() tea.Cmd { return doRestore(b.src, e, restore.Before, restore.Options{Force: true}) }},
			{"cancel", func() tea.Cmd { return flash("warn", "cancelled") }},
		})
		return b, cmd
	}
	e := ev
	return b.ask("undo this edit (restore "+ev.Path+" to before it)?", changeLines(plan), func() tea.Cmd {
		return doRestore(b.src, e, restore.Before, restore.Options{})
	})
}

func changeLines(res restore.Result) []string {
	var out []string
	for _, ch := range res.Changes {
		what := "rewrite"
		switch {
		case ch.From == ch.To:
			what = "already there"
		case ch.To == "":
			what = "delete"
		case ch.From == "":
			what = "create"
		}
		line := ui.Accent.Render(what) + " " + ch.Path
		if ch.Modified {
			line += " " + ui.Warn.Render("(edited since)")
		}
		out = append(out, line)
	}
	if len(out) == 0 {
		out = append(out, ui.Dim.Render("nothing to change"))
	}
	return out
}

func (b *Browser) exportSelected() (ui.Pane, tea.Cmd) {
	n := b.selected()
	if n == nil || b.src.Project == nil {
		return b, nil
	}
	q := store.Query{}
	name := ""
	switch n.Kind {
	case view.KindTurn:
		if n.Turn == nil {
			return b, nil
		}
		q.Turn = n.Turn.ID
		name = "turn-" + shortID(n.Turn.ID)
	case view.KindSession:
		q.Session = n.Session.ID
		name = "session-" + shortID(n.Session.ID)
	default:
		return b, flash("warn", "select a turn or a session to export")
	}
	return b, exportPatch(b.src, q, name)
}

func (b *Browser) openClearMenu() (ui.Pane, tea.Cmd) {
	if b.src.Project == nil {
		return b, flash("warn", "nothing recorded for this project")
	}
	n := b.selected()
	items := []menuItem{}
	if n != nil && n.Session != nil {
		id := n.Session.ID
		items = append(items, menuItem{"this session (" + n.Session.Name + ")", func() tea.Cmd {
			return b.confirmClear(store.ClearScope{Session: id})
		}})
	}
	for _, months := range []int{1, 3, 6} {
		m := months
		items = append(items, menuItem{fmt.Sprintf("older than %d month(s)", m), func() tea.Cmd {
			return b.confirmClear(store.ClearScope{OlderThan: time.Duration(m) * 30 * 24 * time.Hour})
		}})
	}
	items = append(items,
		menuItem{"everything in this project", func() tea.Cmd { return b.confirmClear(store.ClearScope{Project: true}) }},
		menuItem{"everything in every project", func() tea.Cmd { return b.confirmClear(store.ClearScope{All: true, DropPins: true}) }},
	)
	return b.openMenu("clear history", items)
}

// confirmClear is returned from a menu item; it switches to the confirm
// mode with the plan, so it runs as a command that mutates the pane via a
// message round-trip.
func (b *Browser) confirmClear(scope store.ClearScope) tea.Cmd {
	var sum store.ClearSummary
	var err error
	if scope.All {
		sum, err = store.PlanClearAll()
	} else {
		sum, err = b.src.Project.PlanClear(scope, time.Now())
	}
	if err != nil {
		return flash("err", err.Error())
	}
	lines := []string{fmt.Sprintf("%d sessions, %d events, %d files, about %s", sum.Sessions, sum.Events, sum.Files, humanBytes(sum.Bytes))}
	if sum.CutAtText != "" {
		lines = append(lines, "cut: everything before "+sum.CutAtText)
	}
	if sum.KeptPins > 0 {
		lines = append(lines, fmt.Sprintf("%d pinned versions are kept", sum.KeptPins))
	}
	if sum.Events == 0 && sum.Sessions == 0 {
		return flash("warn", "nothing to clear for "+scope.Describe())
	}
	sc := scope
	return func() tea.Msg {
		return confirmRequestMsg{pane: b.name, title: "delete " + scope.Describe() + "?", lines: lines, run: func() tea.Cmd { return doClear(b.src, sc) }}
	}
}

type confirmRequestMsg struct {
	pane  string
	title string
	lines []string
	run   func() tea.Cmd
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

func toolLabel(ev store.Event) string {
	switch ev.Kind {
	case store.KindExternal:
		return ui.Warn.Render(ev.Label())
	case store.KindRestore, store.KindUndo, store.KindBackground:
		return ui.Accent.Render(ev.Label())
	}
	return ev.Label()
}

func shortID(id string) string {
	if len(id) > 10 {
		return id[:10]
	}
	return id
}

func humanCount(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1e3)
	}
	return fmt.Sprint(n)
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GiB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}
