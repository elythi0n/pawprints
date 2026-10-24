package install

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallUninstallRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	settings := filepath.Join(home, "settings.json")
	orig := `{"permissions":{"allow":["Bash(ls *)"]},"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"echo hi"}]}]}}`
	if err := os.WriteFile(settings, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := Install("/usr/local/bin/pawprints")
	if err != nil || !st.SettingsHooks {
		t.Fatalf("install: %+v %v", st, err)
	}
	data, _ := os.ReadFile(settings)
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatal(err)
	}
	hooks := root["hooks"].(map[string]any)
	if len(hooks["PreToolUse"].([]any)) != 3 {
		t.Fatalf("existing PreToolUse entry must be kept: %v", hooks["PreToolUse"])
	}
	for _, ev := range []string{"PostToolUse", "SessionStart", "SessionEnd", "UserPromptSubmit", "Stop"} {
		if _, ok := hooks[ev]; !ok {
			t.Fatalf("missing %s", ev)
		}
	}
	if _, ok := root["permissions"]; !ok {
		t.Fatal("other settings must survive")
	}
	// Installing twice is a no-op.
	if _, err := Install("/usr/local/bin/pawprints"); err != nil {
		t.Fatal(err)
	}
	st, err = Uninstall()
	if err != nil || st.SettingsHooks {
		t.Fatalf("uninstall: %+v %v", st, err)
	}
	data, _ = os.ReadFile(settings)
	root = map[string]any{}
	_ = json.Unmarshal(data, &root)
	hooks = root["hooks"].(map[string]any)
	if len(hooks) != 1 || len(hooks["PreToolUse"].([]any)) != 1 {
		t.Fatalf("uninstall must leave only the user's hook: %v", hooks)
	}
}

func TestInstallAddsMissingEntries(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	// An older install without the Bash hooks.
	old := `{"hooks":{"PreToolUse":[{"matcher":"Edit|Write","hooks":[{"type":"command","command":"\"/x/pawprints\" hook pre"}]}]}}`
	os.WriteFile(filepath.Join(home, "settings.json"), []byte(old), 0o600)
	if _, err := Install("/x/pawprints"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(home, "settings.json"))
	for _, sub := range []string{"hook bash-pre", "hook bash-post", "hook post", "hook prompt"} {
		if !strings.Contains(string(data), sub) {
			t.Fatalf("missing %s:\n%s", sub, data)
		}
	}
	if strings.Count(string(data), "hook pre\"") != 1 {
		t.Fatalf("existing entry must not be duplicated:\n%s", data)
	}
}

// Other tools' hooks with the same subcommand names are never treated as ours.
func TestIsOursIgnoresOtherTools(t *testing.T) {
	ours := []string{`"/home/x/bin/pawprints" hook post`, `pawprints hook stop`, `"pawprints" hook bash-post`, `/opt/pawprints-dev hook session-start`}
	others := []string{`/home/x/.local/bin/rekall hook stop`, `rekall hook session-start`, `rekall hook session-end`, `rekall hook post-tool-use`, `bash '/home/x/.claude/hooks/herdr-agent-state.sh' session`, `echo hook pre`}
	for _, c := range ours {
		if !isOurs(c) {
			t.Errorf("should be ours: %s", c)
		}
	}
	for _, c := range others {
		if isOurs(c) {
			t.Errorf("must not be ours: %s", c)
		}
	}
}

// Uninstall leaves another tool's hooks with the same subcommand names alone.
func TestUninstallKeepsOtherToolsHooks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	settings := filepath.Join(home, "settings.json")
	os.WriteFile(settings, []byte(`{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"/usr/bin/rekall hook stop"}]}]}}`), 0o600)
	if _, err := Install("/usr/local/bin/pawprints"); err != nil {
		t.Fatal(err)
	}
	if _, err := Uninstall(); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(settings)
	if !strings.Contains(string(data), "rekall hook stop") || strings.Contains(string(data), "pawprints") {
		t.Fatalf("after uninstall: %s", data)
	}
}

// An install from an older version gets its Bash-only matcher widened and
// the new hooks added, without duplicating anything.
func TestInstallUpgradesOlderEntries(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	settings := filepath.Join(home, "settings.json")
	old := `{"hooks":{
	  "PreToolUse":[{"matcher":"Edit|Write|MultiEdit|NotebookEdit","hooks":[{"type":"command","command":"\"pawprints\" hook pre"}]},
	                {"matcher":"Bash","hooks":[{"type":"command","command":"\"pawprints\" hook bash-pre"}]}],
	  "PostToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"\"pawprints\" hook bash-post","async":true}]}],
	  "Stop":[{"hooks":[{"type":"command","command":"/usr/bin/rekall hook stop"}]}]}}`
	os.WriteFile(settings, []byte(old), 0o600)
	if Complete() {
		t.Fatal("an older install is not complete")
	}
	if _, err := Install("pawprints"); err != nil {
		t.Fatal(err)
	}
	if !Complete() {
		t.Fatal("after install everything is registered")
	}
	data, _ := os.ReadFile(settings)
	s := string(data)
	if strings.Count(s, "hook bash-pre") != 1 || strings.Count(s, "hook bash-post") != 1 {
		t.Fatalf("entries duplicated:\n%s", s)
	}
	if strings.Count(s, CommandMatcher) != 2 || strings.Contains(s, `"matcher": "Bash"`) {
		t.Fatalf("matcher not upgraded:\n%s", s)
	}
	for _, sub := range []string{"hook agent-start", "hook agent-stop", "hook cwd", "rekall hook stop"} {
		if !strings.Contains(s, sub) {
			t.Fatalf("missing %s:\n%s", sub, s)
		}
	}
}
