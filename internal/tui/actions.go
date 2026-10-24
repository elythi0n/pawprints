package tui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/elythi0n/pawprints/internal/clipboard"
	"github.com/elythi0n/pawprints/internal/export"
	"github.com/elythi0n/pawprints/internal/restore"
	"github.com/elythi0n/pawprints/internal/store"
	"github.com/elythi0n/pawprints/internal/ui"
	"github.com/elythi0n/pawprints/internal/view"
)

// Everything here runs as a tea.Cmd so git and clipboard work never block
// the event loop. Each returns a status flash and a reload request.

func done(kind, text string) tea.Cmd {
	return tea.Batch(func() tea.Msg { return ui.StatusMsg{Text: text, Kind: kind} }, func() tea.Msg { return RequestReloadMsg{} })
}

func flash(kind, text string) tea.Cmd {
	return func() tea.Msg { return ui.StatusMsg{Text: text, Kind: kind} }
}

func copyContent(src *view.Source, ev store.Event) tea.Cmd {
	return func() tea.Msg {
		content, err := src.Project.Get(ev.AfterHash())
		if err != nil {
			return ui.StatusMsg{Text: err.Error(), Kind: "err"}
		}
		if ev.AfterHash() == "" {
			content, _ = src.Project.Get(ev.BeforeHash())
		}
		how, err := clipboard.Copy(string(content))
		if err != nil {
			return ui.StatusMsg{Text: err.Error(), Kind: "err"}
		}
		return ui.StatusMsg{Text: fmt.Sprintf("copied %s (%d bytes) via %s", ev.Path, len(content), how), Kind: "ok"}
	}
}

func copyReference(ev store.Event) tea.Cmd {
	return func() tea.Msg {
		ref := "pawprints:" + ev.ID + " " + ev.Path
		if _, err := clipboard.Copy(ref); err != nil {
			return ui.StatusMsg{Text: err.Error(), Kind: "err"}
		}
		return ui.StatusMsg{Text: "copied " + ref, Kind: "ok"}
	}
}

func copySnippet(src *view.Source, ev store.Event) tea.Cmd {
	return func() tea.Msg {
		content, err := src.Project.Get(ev.AfterHash())
		if err != nil {
			return ui.StatusMsg{Text: err.Error(), Kind: "err"}
		}
		lang := strings.TrimPrefix(filepath.Ext(ev.Path), ".")
		snippet := fmt.Sprintf("%s @ %s (pawprints:%s)\n```%s\n%s```\n", ev.Path, ev.Time().Local().Format("2006-01-02 15:04"), ev.ID, lang, string(content))
		if _, err := clipboard.Copy(snippet); err != nil {
			return ui.StatusMsg{Text: err.Error(), Kind: "err"}
		}
		return ui.StatusMsg{Text: "copied markdown snippet of " + ev.Path, Kind: "ok"}
	}
}

func doRestore(src *view.Source, ev store.Event, which restore.Version, opts restore.Options) tea.Cmd {
	return func() tea.Msg {
		res, err := restore.Restore(src.Project, ev, which, opts)
		if err != nil {
			return actionDoneMsg{kind: "err", text: err.Error(), ev: &ev}
		}
		return actionDoneMsg{kind: "ok", text: describe("restored", res)}
	}
}

func doUndo(src *view.Source, scope restore.UndoScope, opts restore.Options) tea.Cmd {
	return func() tea.Msg {
		res, err := restore.Undo(src.Project, scope, opts)
		if err != nil {
			return actionDoneMsg{kind: "err", text: err.Error()}
		}
		return actionDoneMsg{kind: "ok", text: describe("undid", res)}
	}
}

func describe(verb string, res restore.Result) string {
	n := 0
	conflicts := 0
	for _, ch := range res.Changes {
		if ch.From != ch.To {
			n++
		}
		conflicts += ch.Conflicts
	}
	s := fmt.Sprintf("%s %d file(s)", verb, n)
	if conflicts > 0 {
		s += fmt.Sprintf(", %d with conflict markers", conflicts)
	}
	return s
}

func togglePin(src *view.Source, ev store.Event) tea.Cmd {
	return func() tea.Msg {
		if src.Pinned(ev.ID) {
			if _, err := src.Project.RemovePin(ev.ID); err != nil {
				return actionDoneMsg{kind: "err", text: err.Error()}
			}
			return actionDoneMsg{kind: "ok", text: "unpinned " + ev.Path}
		}
		if _, err := src.Project.AddPin(ev, ""); err != nil {
			return actionDoneMsg{kind: "err", text: err.Error()}
		}
		return actionDoneMsg{kind: "ok", text: "pinned " + ev.Path}
	}
}

func pinWithMessage(src *view.Source, ev store.Event, msg string) tea.Cmd {
	return func() tea.Msg {
		if _, err := src.Project.AddPin(ev, msg); err != nil {
			return actionDoneMsg{kind: "err", text: err.Error()}
		}
		return actionDoneMsg{kind: "ok", text: "pinned " + ev.Path}
	}
}

func doClear(src *view.Source, scope store.ClearScope) tea.Cmd {
	return func() tea.Msg {
		if scope.All {
			sum, err := store.ClearAll()
			if err != nil {
				return actionDoneMsg{kind: "err", text: err.Error()}
			}
			return actionDoneMsg{kind: "ok", text: fmt.Sprintf("cleared %d projects, %d events", sum.Projects, sum.Events)}
		}
		sum, err := src.Project.Clear(scope, time.Now())
		if err != nil {
			return actionDoneMsg{kind: "err", text: err.Error()}
		}
		return actionDoneMsg{kind: "ok", text: fmt.Sprintf("cleared %s: %d events, %d sessions", sum.Scope, sum.Events, sum.Sessions)}
	}
}

func exportPatch(src *view.Source, q store.Query, name string) tea.Cmd {
	return func() tea.Msg {
		events, err := src.Project.Events(q)
		if err != nil {
			return ui.StatusMsg{Text: err.Error(), Kind: "err"}
		}
		patch, err := export.Patch(src.Project, export.Collect(events))
		if err != nil {
			return ui.StatusMsg{Text: err.Error(), Kind: "err"}
		}
		if patch == "" {
			return ui.StatusMsg{Text: "no net changes to export", Kind: "warn"}
		}
		path := filepath.Join(os.TempDir(), "pawprints-"+name+".patch")
		if err := os.WriteFile(path, []byte(patch), 0o644); err != nil {
			return ui.StatusMsg{Text: err.Error(), Kind: "err"}
		}
		if _, err := clipboard.Copy(path); err == nil {
			return ui.StatusMsg{Text: "patch written to " + path + " (path copied)", Kind: "ok"}
		}
		return ui.StatusMsg{Text: "patch written to " + path, Kind: "ok"}
	}
}

// openInEditor writes the before and after versions to temp files and opens
// them in $EDITOR, suspending the TUI meanwhile.
func openInEditor(src *view.Source, ev store.Event) tea.Cmd {
	editor := editorCommand()
	if editor == "" {
		return flash("err", "set $EDITOR to open versions")
	}
	dir, err := os.MkdirTemp("", "pawprints-open-")
	if err != nil {
		return flash("err", err.Error())
	}
	base := filepath.Base(ev.Path)
	before, _ := src.Project.Get(ev.BeforeHash())
	after, _ := src.Project.Get(ev.AfterHash())
	bp := filepath.Join(dir, "before."+base)
	ap := filepath.Join(dir, "after."+base)
	_ = os.WriteFile(bp, before, 0o644)
	_ = os.WriteFile(ap, after, 0o644)
	parts := strings.Fields(editor)
	parts = append(parts, bp, ap)
	cmd := exec.Command(parts[0], parts[1:]...)
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		_ = os.RemoveAll(dir)
		if err != nil {
			return ui.StatusMsg{Text: "editor: " + err.Error(), Kind: "err"}
		}
		return ui.StatusMsg{Text: "closed " + editor, Kind: "ok"}
	})
}

// handOff opens the live file in VS Code or Cursor when one is on PATH,
// without leaving the TUI; otherwise falls back to $EDITOR.
func handOff(src *view.Source, ev store.Event) tea.Cmd {
	abs := src.Project.Abs(ev.Path)
	for _, name := range []string{"cursor", "code"} {
		if _, err := exec.LookPath(name); err == nil {
			return func() tea.Msg {
				if err := exec.Command(name, "-g", abs).Start(); err != nil {
					return ui.StatusMsg{Text: err.Error(), Kind: "err"}
				}
				return ui.StatusMsg{Text: "opened in " + name, Kind: "ok"}
			}
		}
	}
	editor := editorCommand()
	if editor == "" {
		return flash("err", "no code, cursor, or $EDITOR found")
	}
	parts := append(strings.Fields(editor), abs)
	return tea.ExecProcess(exec.Command(parts[0], parts[1:]...), func(err error) tea.Msg {
		if err != nil {
			return ui.StatusMsg{Text: err.Error(), Kind: "err"}
		}
		return nil
	})
}

func openTranscript(path string) tea.Cmd {
	if path == "" {
		return flash("warn", "no transcript path recorded for this session")
	}
	if _, err := os.Stat(path); err != nil {
		return flash("warn", "transcript not found: "+path)
	}
	pager := os.Getenv("PAGER")
	if pager == "" {
		pager = "less -R"
	}
	parts := append(strings.Fields(pager), path)
	return tea.ExecProcess(exec.Command(parts[0], parts[1:]...), func(error) tea.Msg { return nil })
}

func editorCommand() string {
	if e := os.Getenv("VISUAL"); e != "" {
		return e
	}
	return os.Getenv("EDITOR")
}
