package tui

import (
	"archive/zip"
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/elythi0n/pawprints/internal/setup"
	"github.com/elythi0n/pawprints/internal/view"
)

func TestSetupChecklist(t *testing.T) {
	lipgloss.SetColorProfile(termenv.Ascii)
	bin := t.TempDir()
	// Only fakes and git on PATH: no real editor can be reached.
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("needs git")
	}
	os.Symlink(git, filepath.Join(bin, "git"))
	t.Setenv("PATH", bin)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("SHELL", "/bin/bash")
	src := seed(t)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("extension/package.json")
	w.Write([]byte(`{"version":"0.3.0"}`))
	zw.Close()
	setup.UseVSIXForTest(buf.Bytes())
	t.Cleanup(func() { setup.UseVSIXForTest(nil) })
	log := filepath.Join(t.TempDir(), "cursor.log")
	os.WriteFile(filepath.Join(bin, "cursor"), []byte("#!/bin/sh\ncase \"$1\" in --list-extensions) ;; --install-extension) echo \"$@\" > "+log+" ;; esac\n"), 0o755)

	r := newRoot(src)
	r.offerSetup()
	if r.setup == nil || !r.setup.firstRun {
		t.Fatal("first run should open the checklist")
	}
	drive(t, r, tea.WindowSizeMsg{Width: 110, Height: 34})
	if !strings.Contains(ansi.Strip(r.View()), "looking for Claude Code") {
		t.Fatal("the checklist shows a scanning line first")
	}
	drive(t, r, setupScannedMsg{items: setup.Scan()})
	frame := ansi.Strip(r.View())
	for _, want := range []string{"Set up pawprints", "Claude Code hooks", "Cursor extension", "VS Code extension", "not found", "Shell completions", "space toggle"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("checklist missing %q:\n%s", want, frame)
		}
	}
	checkFrame(t, r, 110, 34, "setup")
	ids := map[string]int{}
	for i, it := range r.setup.items {
		ids[it.ID] = i
	}
	if !r.setup.checked[ids["cursor"]] || r.setup.checked[ids["vscode"]] {
		t.Fatal("found editors start ticked, missing ones cannot be")
	}
	// Untick the completions, then apply.
	for r.setup.items[r.setup.cursor].ID != "completions" {
		drive(t, r, key("down"))
	}
	drive(t, r, key(" "))
	drive(t, r, key("enter"))
	if r.setup.phase != setupDone {
		t.Fatalf("expected done, phase %d", r.setup.phase)
	}
	logged, _ := os.ReadFile(log)
	if !strings.Contains(string(logged), "--install-extension") {
		t.Fatal("cursor should have been asked to install the extension")
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("HOME"), ".local", "share", "bash-completion", "completions", "pawprints")); err == nil {
		t.Fatal("unticked completions must not be written")
	}
	if !strings.Contains(ansi.Strip(r.View()), "All set") {
		t.Fatalf("done screen:\n%s", ansi.Strip(r.View()))
	}
	drive(t, r, key("enter"))
	if r.setup != nil {
		t.Fatal("enter closes the checklist")
	}
	if m, ok := setup.LoadMarker(); !ok || m.Results["cursor"] != "done" {
		t.Fatalf("marker %+v", m)
	}
	// Second run: no checklist.
	r2 := newRoot(view.NewSource(src.Root))
	r2.offerSetup()
	if r2.setup != nil {
		t.Fatal("the checklist opens by itself only once")
	}
	// Settings opens it on demand.
	drive(t, r, key("7"))
	drive(t, r, key("S"))
	if r.setup == nil || r.setup.firstRun {
		t.Fatal("S in Settings opens the checklist")
	}
	drive(t, r, setupScannedMsg{items: setup.Scan()})
	drive(t, r, key("esc"))
	if r.setup != nil {
		t.Fatal("esc closes it")
	}
}
