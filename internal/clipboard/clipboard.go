// Package clipboard copies text for the TUI. OSC 52 goes first because it
// works over SSH and inside terminal multiplexers; local tools are the
// fallback.
package clipboard

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Copy writes text to the clipboard and reports which method succeeded.
func Copy(text string) (string, error) {
	if err := osc52(text); err == nil {
		return "osc52", nil
	}
	for _, c := range [][]string{
		{"wl-copy"},
		{"xclip", "-selection", "clipboard"},
		{"xsel", "--clipboard", "--input"},
		{"pbcopy"},
		{"clip.exe"},
	} {
		if _, err := exec.LookPath(c[0]); err != nil {
			continue
		}
		cmd := exec.Command(c[0], c[1:]...)
		cmd.Stdin = strings.NewReader(text)
		if err := cmd.Run(); err == nil {
			return c[0], nil
		}
	}
	return "", errors.New("no clipboard available: OSC 52 failed and no wl-copy, xclip, xsel, pbcopy, or clip.exe on PATH")
}

func osc52(text string) error {
	tty, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	defer tty.Close()
	enc := base64.StdEncoding.EncodeToString([]byte(text))
	seq := fmt.Sprintf("\x1b]52;c;%s\x07", enc)
	if term := os.Getenv("TERM"); strings.HasPrefix(term, "screen") || strings.HasPrefix(term, "tmux") || os.Getenv("TMUX") != "" {
		// Wrap for tmux-style multiplexers so the sequence reaches the outer terminal.
		seq = "\x1bPtmux;" + strings.ReplaceAll(seq, "\x1b", "\x1b\x1b") + "\x1b\\"
	}
	_, err = tty.WriteString(seq)
	return err
}
