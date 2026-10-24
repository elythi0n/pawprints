package panes

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elythi0n/pawprints/internal/sessions"
	"github.com/elythi0n/pawprints/internal/store"
	"github.com/elythi0n/pawprints/internal/view"
)

func TestByWhoGroupsAgentsAndOutside(t *testing.T) {
	t.Setenv("PAWPRINTS_HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := filepath.Join(t.TempDir(), "ws")
	os.MkdirAll(root, 0o755)
	src := view.NewSource(root)
	src.Now = time.Now()
	at := time.Now().Add(-time.Minute)
	ev := func(i int, path, kind, agent, agentType, tool string) store.Event {
		ts := at.Add(time.Duration(i) * time.Second)
		return store.Event{ID: store.NewID(ts), TS: store.Now(ts), Session: "S", Turn: "T", Kind: kind, Tool: tool, Path: path, Agent: agent, AgentType: agentType}
	}
	evs := []store.Event{
		ev(5, "notes.txt", store.KindExternal, "", "", "external"),
		ev(4, "gen/api.go", store.KindEdit, "a1", "Explore", "Bash"),
		ev(3, "gen/types.go", store.KindEdit, "a1", "Explore", "Write"),
		ev(2, "main.go", store.KindEdit, "", "", "Edit"),
		ev(1, "out.txt", store.KindBackground, "", "", "Bash"),
	}
	se := sessions.Session{ID: "S", Agents: []store.Agent{{ID: "a1", Type: "Explore", Summary: "generated the API"}}}
	rows := byWho(src, se, evs, "p:")
	var got []string
	for _, r := range rows {
		got = append(got, r.Label)
	}
	want := []string{"main.go", "out.txt", "◇ Explore agent", "outside Claude"}
	if len(got) != len(want) {
		t.Fatalf("rows %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("rows %v want %v", got, want)
		}
	}
	agent := rows[2]
	if agent.Desc != "2 files · generated the API" || len(agent.Children) != 2 {
		t.Fatalf("agent group %q with %d rows", agent.Desc, len(agent.Children))
	}
	if rows[1].Event.Label() != "background" || agent.Children[0].Event.Label() != "Bash · Explore" {
		t.Fatalf("labels %q %q", rows[1].Event.Label(), agent.Children[0].Event.Label())
	}
	if (store.Event{Tool: "mcp__files__write_file"}).Label() != "files:write_file" {
		t.Fatal("MCP tools read as server:tool")
	}
}
