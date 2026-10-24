// Package install adds and removes the Pawprints hook entries in
// ~/.claude/settings.json for people who do not use the plugin.
package install

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/elythi0n/pawprints/internal/sessions"
)

// isOurs recognizes a hook command Pawprints wrote. Two things must hold:
// the command ends in "hook <sub>" for a subcommand Pawprints registers, and
// the program it runs is Pawprints, by name or because it is this binary.
// Other tools use the same subcommand names (rekall has "hook stop"), so the
// ending alone would claim their hooks and uninstall would remove them.
func isOurs(command string) bool {
	command = strings.TrimSpace(command)
	suffix := false
	for _, ev := range events {
		if strings.HasSuffix(command, " hook "+ev.sub) {
			suffix = true
			break
		}
	}
	if !suffix {
		return false
	}
	prog := program(command)
	if strings.Contains(filepath.Base(prog), "pawprints") {
		return true
	}
	self, err := os.Executable()
	if err != nil {
		return false
	}
	if resolved, err := filepath.EvalSymlinks(self); err == nil {
		self = resolved
	}
	if resolved, err := filepath.EvalSymlinks(prog); err == nil {
		prog = resolved
	}
	return prog == self
}

// program is the executable a hook command runs: its first word, or the
// quoted path it starts with.
func program(command string) string {
	if strings.HasPrefix(command, `"`) {
		if end := strings.Index(command[1:], `"`); end >= 0 {
			return command[1 : end+1]
		}
	}
	if fields := strings.Fields(command); len(fields) > 0 {
		return fields[0]
	}
	return ""
}

// Status describes what is registered.
type Status struct {
	SettingsPath    string `json:"settings_path"`
	SettingsHooks   bool   `json:"settings_hooks"`
	PluginInstalled bool   `json:"plugin_installed"`
}

var editMatcher = "Edit|Write|MultiEdit|NotebookEdit"

// CommandMatcher picks the tools whose file changes are found by scanning:
// Bash and every MCP tool. It is a regular expression, so it is anchored to
// keep "Bash" from also matching tools like BashOutput.
const CommandMatcher = "^(Bash|mcp__.*)$"

// events maps hook event name to the pawprints subcommand and whether it is async.
var events = []struct {
	event, sub, matcher string
	async               bool
}{
	{"PreToolUse", "pre", editMatcher, false},
	{"PostToolUse", "post", editMatcher, false},
	{"PreToolUse", "bash-pre", CommandMatcher, false},
	{"PostToolUse", "bash-post", CommandMatcher, true},
	{"SessionStart", "session-start", "", true},
	{"SessionEnd", "session-end", "", true},
	{"UserPromptSubmit", "prompt", "", true},
	{"Stop", "stop", "", true},
	{"SubagentStart", "agent-start", "", true},
	{"SubagentStop", "agent-stop", "", true},
	{"CwdChanged", "cwd", "", true},
}

func settingsPath() string { return filepath.Join(sessions.ClaudeHome(), "settings.json") }

// Check reports the current registration state.
func Check() Status {
	st := Status{SettingsPath: settingsPath()}
	if root, err := loadSettings(st.SettingsPath); err == nil {
		st.SettingsHooks = hasOurs(root)
	}
	st.PluginInstalled = pluginInstalled()
	return st
}

func hasOurs(root map[string]any) bool {
	hooks, _ := root["hooks"].(map[string]any)
	for _, raw := range hooks {
		list, _ := raw.([]any)
		for _, item := range list {
			entry, _ := item.(map[string]any)
			inner, _ := entry["hooks"].([]any)
			for _, hi := range inner {
				hm, _ := hi.(map[string]any)
				if cmd, _ := hm["command"].(string); isOurs(cmd) {
					return true
				}
			}
		}
	}
	return false
}

func pluginInstalled() bool {
	data, err := os.ReadFile(filepath.Join(sessions.ClaudeHome(), "plugins", "installed_plugins.json"))
	if err != nil {
		return false
	}
	return strings.Contains(string(data), `"pawprints@`)
}

// Install writes the hook entries pointing at the given binary path.
func Install(binary string) (Status, error) {
	st := Check()
	if st.PluginInstalled {
		return st, errors.New("the pawprints Claude Code plugin is installed and already provides the hooks; nothing to do")
	}
	root, err := loadSettings(st.SettingsPath)
	if err != nil {
		return st, err
	}
	hooks, _ := root["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	added := 0
	for _, ev := range events {
		if entry := ourEntry(hooks[ev.event], ev.sub); entry != nil {
			// An older version registered this hook with another matcher:
			// update it in place rather than adding a second entry.
			if m, _ := entry["matcher"].(string); m != ev.matcher && ev.matcher != "" {
				entry["matcher"] = ev.matcher
				added++
			}
			continue
		}
		added++
		entry := map[string]any{
			"hooks": []any{hookCommand(binary, ev.sub, ev.async)},
		}
		if ev.matcher != "" {
			entry["matcher"] = ev.matcher
		}
		list, _ := hooks[ev.event].([]any)
		hooks[ev.event] = append(list, entry)
	}
	if added == 0 {
		st.SettingsHooks = true
		return st, nil
	}
	root["hooks"] = hooks
	if err := saveSettings(st.SettingsPath, root, true); err != nil {
		return st, err
	}
	st.SettingsHooks = true
	return st, nil
}

func hookCommand(binary, sub string, async bool) map[string]any {
	h := map[string]any{
		"type":    "command",
		"command": fmt.Sprintf("%q %s %s", binary, "hook", sub),
		"timeout": 10,
	}
	if async {
		h["async"] = true
	}
	return h
}

// Uninstall removes every entry whose command is one Pawprints wrote.
func Uninstall() (Status, error) {
	st := Check()
	if !st.SettingsHooks {
		return st, nil
	}
	root, err := loadSettings(st.SettingsPath)
	if err != nil {
		return st, err
	}
	hooks, _ := root["hooks"].(map[string]any)
	for event, raw := range hooks {
		list, _ := raw.([]any)
		var kept []any
		for _, item := range list {
			entry, _ := item.(map[string]any)
			inner, _ := entry["hooks"].([]any)
			var keptInner []any
			for _, hi := range inner {
				hm, _ := hi.(map[string]any)
				cmd, _ := hm["command"].(string)
				if !isOurs(cmd) {
					keptInner = append(keptInner, hi)
				}
			}
			if len(keptInner) > 0 {
				entry["hooks"] = keptInner
				kept = append(kept, entry)
			}
		}
		if len(kept) == 0 {
			delete(hooks, event)
		} else {
			hooks[event] = kept
		}
	}
	if len(hooks) == 0 {
		delete(root, "hooks")
	} else {
		root["hooks"] = hooks
	}
	if err := saveSettings(st.SettingsPath, root, false); err != nil {
		return st, err
	}
	st.SettingsHooks = false
	return st, nil
}

func loadSettings(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	root := map[string]any{}
	if len(strings.TrimSpace(string(data))) == 0 {
		return root, nil
	}
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return root, nil
}

// saveSettings writes the file atomically. With backup set it first keeps a
// copy of the previous content, so an install can always be undone by hand.
func saveSettings(path string, root map[string]any, backup bool) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if backup {
		if old, err := os.ReadFile(path); err == nil {
			_ = os.WriteFile(path+".pawprints-backup", old, 0o600)
		}
	}
	data, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ourEntry finds the matcher group whose hooks run `pawprints hook <sub>`.
func ourEntry(raw any, sub string) map[string]any {
	list, _ := raw.([]any)
	for _, item := range list {
		entry, _ := item.(map[string]any)
		inner, _ := entry["hooks"].([]any)
		for _, hi := range inner {
			hm, _ := hi.(map[string]any)
			cmd, _ := hm["command"].(string)
			if isOurs(cmd) && strings.HasSuffix(strings.TrimSpace(cmd), " hook "+sub) {
				return entry
			}
		}
	}
	return nil
}

// BinaryPath is the command to put in hook entries: "pawprints" when the
// running binary is the one on PATH, so the entry survives reinstalls to a
// new path, else its absolute path.
func BinaryPath() (string, error) {
	bin, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(bin); err == nil {
		bin = resolved
	}
	if onPath, err := exec.LookPath("pawprints"); err == nil {
		if same, _ := filepath.EvalSymlinks(onPath); same == bin {
			return "pawprints", nil
		}
	}
	return bin, nil
}

// Complete reports whether settings.json has every hook this version
// registers; older installs miss the ones added since.
func Complete() bool {
	root, err := loadSettings(settingsPath())
	if err != nil {
		return false
	}
	hooks, _ := root["hooks"].(map[string]any)
	for _, ev := range events {
		entry := ourEntry(hooks[ev.event], ev.sub)
		if entry == nil {
			return false
		}
		if m, _ := entry["matcher"].(string); ev.matcher != "" && m != ev.matcher {
			return false
		}
	}
	return true
}

// PluginActive reports whether the pawprints plugin is installed and not
// switched off in settings.json's enabledPlugins.
func PluginActive() bool {
	if !pluginInstalled() {
		return false
	}
	root, err := loadSettings(settingsPath())
	if err != nil {
		return true
	}
	enabled, _ := root["enabledPlugins"].(map[string]any)
	for id, v := range enabled {
		if strings.HasPrefix(id, "pawprints@") {
			if on, ok := v.(bool); ok && !on {
				return false
			}
		}
	}
	return true
}
