package tui

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/elythi0n/pawprints/internal/hook"
	"github.com/elythi0n/pawprints/internal/panes"
	"github.com/elythi0n/pawprints/internal/store"
	"github.com/elythi0n/pawprints/internal/view"
)

// procStart reads this process's start time the way Claude Code records it.
func procStart(t *testing.T) string {
	data, err := os.ReadFile("/proc/self/stat")
	if err != nil {
		t.Skip("needs /proc")
	}
	s := string(data)
	return strings.Fields(s[strings.LastIndex(s, ")")+1:])[19]
}

func TestLiveShowsEveryFileOfTheRunningPrompt(t *testing.T) {
	lipgloss.SetColorProfile(termenv.Ascii)
	t.Setenv("PAWPRINTS_HOME", t.TempDir())
	claude := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", claude)
	ws := filepath.Join(t.TempDir(), "ws")
	for _, d := range []string{"capture/billing", "go/services/billing"} {
		os.MkdirAll(filepath.Join(ws, d), 0o755)
	}
	os.MkdirAll(filepath.Join(claude, "sessions"), 0o755)
	reg, _ := json.Marshal(map[string]any{"pid": os.Getpid(), "procStart": procStart(t), "sessionId": "S", "cwd": ws, "name": "ws-a1", "status": "busy"})
	os.WriteFile(filepath.Join(claude, "sessions", strconv.Itoa(os.Getpid())+".json"), reg, 0o644)

	send := func(event string, fields map[string]any) {
		fields["session_id"] = "S"
		data, _ := json.Marshal(fields)
		var stderr bytes.Buffer
		hook.Run(event, bytes.NewReader(data), &stderr)
		if stderr.Len() > 0 {
			t.Fatal(stderr.String())
		}
	}
	edit := func(cwd, rel, id string) {
		file := filepath.Join(ws, rel)
		in := map[string]any{"cwd": cwd, "tool_name": "Edit", "tool_use_id": id, "tool_input": map[string]any{"file_path": file}}
		send("pre", in)
		os.WriteFile(file, []byte(id+"\n"), 0o644)
		send("post", map[string]any{"cwd": cwd, "tool_name": "Edit", "tool_use_id": id, "tool_input": map[string]any{"file_path": file}})
	}

	send("prompt", map[string]any{"cwd": ws, "prompt": "first prompt"})
	edit(ws, "README.md", "a")
	send("stop", map[string]any{"cwd": ws})

	send("prompt", map[string]any{"cwd": ws, "prompt": "wire the billing env"})
	edit(filepath.Join(ws, "capture"), "capture/billing/prepare-app.sh", "b")
	edit(filepath.Join(ws, "capture/billing"), "capture/billing/billing.env", "c")
	edit(filepath.Join(ws, "go/services/billing"), "go/services/billing/main.go", "d")
	// No stop yet: the prompt is still running.

	src := view.NewSource(ws)
	src.Reload()
	roots := panes.Live(src)
	if len(roots) != 1 || roots[0].Session == nil {
		t.Fatalf("expected the live session, got %d roots", len(roots))
	}
	now := roots[0].Children[0]
	if !strings.Contains(now.Label, "wire the billing env") || len(now.Children) != 3 {
		t.Fatalf("current prompt should hold 3 files, got %q with %d", now.Label, len(now.Children))
	}
	// The tree layout shows the same files inside their folders.
	store.SetConfigValue("file_layout", "tree")
	src.Reload()
	tree := panes.Live(src)[0].Children[0].Children
	if len(tree) != 2 || tree[0].Label != "capture/billing/" || tree[1].Label != "go/services/billing/" {
		var got []string
		for _, n := range tree {
			got = append(got, n.Label)
		}
		t.Fatalf("tree layout %v", got)
	}
	store.SetConfigValue("file_layout", "list")
	src.Reload()

	r := newRoot(src)
	drive(t, r, tea.WindowSizeMsg{Width: 140, Height: 40})
	drive(t, r, key("1"))
	frame := ansi.Strip(r.View())
	for _, want := range []string{"wire the billing env", "prepare-app.sh", "billing.env", "main.go", "earlier prompts"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("Live is missing %q:\n%s", want, frame)
		}
	}
}
