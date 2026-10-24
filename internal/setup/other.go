package setup

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/elythi0n/pawprints/internal/completion"
	"github.com/elythi0n/pawprints/internal/install"
)

func hooksItem() Item {
	it := Item{ID: "hooks", Title: "Claude Code hooks", Detail: "records Claude's edits", kind: "hooks"}
	st := install.Check()
	switch {
	case install.PluginActive():
		it.State, it.Note = Provided, "provided by the pawprints plugin"
	case st.SettingsHooks && install.Complete():
		it.State, it.Note = Current, "registered in "+shortHome(st.SettingsPath)
	case st.SettingsHooks:
		it.State, it.Note = Outdated, "add the hooks this version is missing"
	default:
		it.State, it.Note = Missing, "register in "+shortHome(st.SettingsPath)
	}
	it.Default = it.Actionable()
	return it
}

func applyHooks() (string, error) {
	bin, err := install.BinaryPath()
	if err != nil {
		return "", err
	}
	st, err := install.Install(bin)
	if err != nil {
		return "", err
	}
	return "hooks registered in " + shortHome(st.SettingsPath) + "; Claude sessions pick them up on their next tool call", nil
}

// completionTarget is where a shell loads user completions from without any
// change to the shell's startup file, where such a place exists.
func completionTarget(shell string) (path, script string, ok bool) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", false
	}
	data := os.Getenv("XDG_DATA_HOME")
	if data == "" {
		data = filepath.Join(home, ".local", "share")
	}
	switch shell {
	case "bash":
		return filepath.Join(data, "bash-completion", "completions", "pawprints"), completion.Bash, true
	case "zsh":
		return filepath.Join(data, "zsh", "site-functions", "_pawprints"), completion.Zsh, true
	case "fish":
		cfg := os.Getenv("XDG_CONFIG_HOME")
		if cfg == "" {
			cfg = filepath.Join(home, ".config")
		}
		return filepath.Join(cfg, "fish", "completions", "pawprints.fish"), completion.Fish, true
	}
	return "", "", false
}

func completionItem() Item {
	shell := filepath.Base(os.Getenv("SHELL"))
	it := Item{ID: "completions", Title: "Shell completions", Detail: shell, kind: "completion", shell: shell}
	path, script, ok := completionTarget(shell)
	if !ok {
		if shell == "" || shell == "." {
			it.Detail = "no $SHELL"
		}
		it.State, it.Note = Unavailable, "bash, zsh, and fish are supported"
		return it
	}
	existing, err := os.ReadFile(path)
	switch {
	case err != nil:
		it.State, it.Note = Missing, "write "+shortHome(path)
	case string(existing) != script:
		it.State, it.Note = Outdated, "update "+shortHome(path)
	default:
		it.State, it.Note = Current, "in "+shortHome(path)
	}
	it.Default = it.Actionable()
	return it
}

func applyCompletion(shell string) (string, error) {
	path, script, ok := completionTarget(shell)
	if !ok {
		return "", nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(script), 0o644); err != nil {
		return "", err
	}
	msg := "wrote " + shortHome(path)
	switch shell {
	case "zsh":
		msg += "; zsh loads it if that folder is in $fpath (add `fpath+=(~/.local/share/zsh/site-functions)` before compinit)"
	case "bash":
		msg += "; new bash shells load it through bash-completion"
	case "fish":
		msg += "; new fish shells load it"
	}
	return msg, nil
}

func shortHome(p string) string {
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(p, home) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}
