package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/elythi0n/pawprints/internal/setup"
	"github.com/elythi0n/pawprints/internal/ui"
)

// setupModel is the first-run checklist: what Pawprints can plug into on
// this machine, ticked where something is missing or out of date.
type setupModel struct {
	items   []setup.Item
	checked []bool
	results []string
	failed  []bool
	cursor  int
	phase   setupPhase
	applyAt int
	spin    spinner.Model
	// standalone means `pawprints setup`: quit when done instead of
	// continuing into the TUI.
	standalone bool
	firstRun   bool
}

type setupPhase int

const (
	setupScanning setupPhase = iota
	setupChoosing
	setupApplying
	setupDone
)

type setupScannedMsg struct{ items []setup.Item }

type setupAppliedMsg struct {
	index  int
	result string
	err    error
}

// setupClosedMsg tells the root the checklist is finished.
type setupClosedMsg struct{ quit bool }

// openSetupMsg asks the root to show the checklist.
type openSetupMsg struct{}

func newSetupModel(standalone, firstRun bool) *setupModel {
	sp := spinner.New()
	sp.Spinner = spinner.MiniDot
	sp.Style = ui.Cursor
	return &setupModel{phase: setupScanning, spin: sp, standalone: standalone, firstRun: firstRun}
}

func (m *setupModel) Init() tea.Cmd {
	return tea.Batch(m.spin.Tick, func() tea.Msg { return setupScannedMsg{items: setup.Scan()} })
}

func (m *setupModel) scanned(items []setup.Item) {
	m.items = items
	m.checked = make([]bool, len(items))
	m.results = make([]string, len(items))
	m.failed = make([]bool, len(items))
	for i, it := range items {
		m.checked[i] = it.Default
	}
	m.phase = setupChoosing
	m.cursor = 0
	for i, it := range items {
		if it.Actionable() {
			m.cursor = i
			break
		}
	}
}

func (m *setupModel) anyChecked() bool {
	for i := range m.items {
		if m.checked[i] {
			return true
		}
	}
	return false
}

// next starts the next ticked item, or finishes.
func (m *setupModel) next(from int) tea.Cmd {
	for i := from; i < len(m.items); i++ {
		if m.checked[i] {
			m.applyAt = i
			it := m.items[i]
			return func() tea.Msg {
				res, err := setup.Apply(it)
				return setupAppliedMsg{index: i, result: res, err: err}
			}
		}
	}
	m.phase = setupDone
	results := map[string]string{}
	for i, it := range m.items {
		if m.checked[i] {
			if m.failed[i] {
				results[it.ID] = "failed: " + m.results[i]
			} else {
				results[it.ID] = "done"
			}
		}
	}
	_ = setup.SaveMarker(false, results)
	return nil
}

func (m *setupModel) close(quit bool) tea.Cmd {
	return func() tea.Msg { return setupClosedMsg{quit: quit} }
}

func (m *setupModel) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case spinner.TickMsg:
		if m.phase == setupScanning || m.phase == setupApplying {
			var cmd tea.Cmd
			m.spin, cmd = m.spin.Update(msg)
			return cmd
		}
		return nil
	case setupScannedMsg:
		m.scanned(msg.items)
		return nil
	case setupAppliedMsg:
		if msg.err != nil {
			m.results[msg.index], m.failed[msg.index] = msg.err.Error(), true
		} else {
			m.results[msg.index] = msg.result
		}
		return m.next(msg.index + 1)
	case tea.KeyMsg:
		return m.key(msg)
	}
	return nil
}

func (m *setupModel) key(k tea.KeyMsg) tea.Cmd {
	switch m.phase {
	case setupScanning, setupApplying:
		if k.String() == "ctrl+c" {
			return tea.Quit
		}
		return nil
	case setupDone:
		switch k.String() {
		case "r":
			m.phase = setupScanning
			return m.Init()
		default:
			return m.close(m.standalone)
		}
	}
	switch k.String() {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.items)-1 {
			m.cursor++
		}
	case " ", "x":
		if m.items[m.cursor].Actionable() {
			m.checked[m.cursor] = !m.checked[m.cursor]
		}
	case "a":
		all := true
		for i, it := range m.items {
			if it.Actionable() && !m.checked[i] {
				all = false
			}
		}
		for i, it := range m.items {
			m.checked[i] = it.Actionable() && !all
		}
	case "enter":
		if !m.anyChecked() {
			_ = setup.SaveMarker(false, nil)
			return m.close(m.standalone)
		}
		m.phase = setupApplying
		return tea.Batch(m.spin.Tick, m.next(0))
	case "s":
		// Skip for good: the checklist does not open by itself again.
		_ = setup.SaveMarker(true, nil)
		return m.close(m.standalone)
	case "esc", "q":
		// Later: nothing is recorded, so it opens again next time.
		return m.close(m.standalone)
	case "ctrl+c":
		return tea.Quit
	}
	return nil
}

func (m *setupModel) View(width int) string {
	var sb strings.Builder
	title := "Set up pawprints"
	if !m.firstRun {
		title = "Integrations"
	}
	sb.WriteString(ui.Strong.Render(title) + "\n")
	if m.firstRun && m.phase != setupDone {
		sb.WriteString(ui.Dim.Render("Pick what to connect. Everything can be changed later with `pawprints setup` or from Settings.") + "\n")
	}
	sb.WriteString("\n")
	if m.phase == setupScanning {
		sb.WriteString(m.spin.View() + " " + ui.Soft.Render("looking for Claude Code, editors, and your shell…") + "\n")
		return sb.String()
	}
	titleW, detailW := 27, 16
	for i, it := range m.items {
		box := "[ ]"
		switch {
		case m.phase != setupChoosing && m.checked[i] && m.results[i] == "" && i == m.applyAt:
			box = " " + m.spin.View() + " "
		case m.phase != setupChoosing && m.checked[i] && m.failed[i]:
			box = ui.Err.Render("[✗]")
		case m.phase != setupChoosing && m.checked[i] && m.results[i] != "":
			box = ui.Ok.Render("[✓]")
		case m.checked[i]:
			box = ui.Cursor.Render("[x]")
		case !it.Actionable():
			box = ui.Dim.Render(" · ")
		}
		titleStyle := ui.Strong
		if !it.Actionable() {
			titleStyle = ui.Soft
		}
		state := stateLabel(it)
		line := fmt.Sprintf("%s %s %s %s", box, titleStyle.Render(ui.PadRight(it.Title, titleW)), ui.Dim.Render(ui.PadRight(ui.Truncate(it.Detail, detailW), detailW)), state)
		if i == m.cursor && m.phase == setupChoosing {
			line = ui.Cursor.Render("▍") + line
		} else {
			line = " " + line
		}
		sb.WriteString(ui.Truncate(line, width) + "\n")
		note := it.Note
		if m.results[i] != "" {
			note = m.results[i]
		}
		if note != "" && (i == m.cursor || m.results[i] != "") {
			style := ui.Dim
			if m.failed[i] {
				style = ui.Err
			}
			sb.WriteString("     " + style.Render(ui.Truncate(note, width-6)) + "\n")
		}
	}
	sb.WriteString("\n")
	switch m.phase {
	case setupChoosing:
		sb.WriteString(ui.Keys("space", "toggle", "a", "all", "enter", "apply", "s", "skip for good", "esc", "later"))
	case setupApplying:
		sb.WriteString(ui.Soft.Render("setting up…"))
	case setupDone:
		failed := 0
		for i := range m.items {
			if m.checked[i] && m.failed[i] {
				failed++
			}
		}
		if failed > 0 {
			sb.WriteString(ui.Warn.Render(fmt.Sprintf("%d item(s) failed; the reason is under each. ", failed)))
		} else {
			sb.WriteString(ui.Ok.Render("All set. "))
		}
		next := "continue"
		if m.standalone {
			next = "close"
		}
		sb.WriteString(ui.Keys("enter", next, "r", "scan again"))
	}
	return lipgloss.NewStyle().Render(sb.String())
}

func stateLabel(it setup.Item) string {
	switch it.State {
	case setup.Missing:
		return ui.Warn.Render("not set up")
	case setup.Outdated:
		return ui.Accent.Render("update available")
	case setup.Current:
		return ui.Ok.Render("✓ set up")
	case setup.Provided:
		return ui.Ok.Render("✓ via plugin")
	}
	return ui.Dim.Render("not found")
}
