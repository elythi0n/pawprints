package diff

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/elythi0n/pawprints/internal/ui"
)

// Render colors a unified diff for a terminal. With color off it returns the
// text unchanged.
func Render(unified string, color bool) string {
	if !color || unified == "" {
		return unified
	}
	add := lipgloss.NewStyle().Foreground(lipgloss.Color(ui.Green))
	del := lipgloss.NewStyle().Foreground(lipgloss.Color(ui.Red))
	hunk := lipgloss.NewStyle().Foreground(lipgloss.Color(ui.Blue))
	head := lipgloss.NewStyle().Foreground(lipgloss.Color(ui.Muted)).Bold(true)
	var sb strings.Builder
	for _, line := range strings.Split(strings.TrimSuffix(unified, "\n"), "\n") {
		switch {
		case strings.HasPrefix(line, "+++"), strings.HasPrefix(line, "---"), strings.HasPrefix(line, "diff --git"):
			sb.WriteString(head.Render(line))
		case strings.HasPrefix(line, "@@"):
			sb.WriteString(hunk.Render(line))
		case strings.HasPrefix(line, "+"):
			sb.WriteString(add.Render(line))
		case strings.HasPrefix(line, "-"):
			sb.WriteString(del.Render(line))
		default:
			sb.WriteString(line)
		}
		sb.WriteByte('\n')
	}
	return sb.String()
}

// External names the external differ to use, if any: PAWPRINTS_DIFFER, else
// delta, else difft. Empty means use the built-in renderer.
func External() string {
	if v := os.Getenv("PAWPRINTS_DIFFER"); v != "" {
		if v == "none" {
			return ""
		}
		if _, err := exec.LookPath(v); err == nil {
			return v
		}
	}
	for _, name := range []string{"delta", "difft"} {
		if _, err := exec.LookPath(name); err == nil {
			return name
		}
	}
	return ""
}

// RenderExternal runs the named differ. delta reads a unified diff on stdin;
// difft wants the two files. The returned bool is false when the tool failed
// and the caller should fall back to the built-in renderer.
func RenderExternal(name, path string, a, b []byte, unified string, width int) (string, bool) {
	switch name {
	case "delta":
		cmd := exec.Command("delta", "--paging=never", "--width", itoa(width))
		cmd.Stdin = strings.NewReader(unified)
		var out bytes.Buffer
		cmd.Stdout = &out
		if err := cmd.Run(); err != nil {
			return "", false
		}
		return out.String(), true
	case "difft":
		dir, err := os.MkdirTemp("", "pawprints-difft-")
		if err != nil {
			return "", false
		}
		defer func() { _ = os.RemoveAll(dir) }()
		base := filepath.Base(path)
		ap := filepath.Join(dir, "before", base)
		bp := filepath.Join(dir, "after", base)
		_ = os.MkdirAll(filepath.Dir(ap), 0o755)
		_ = os.MkdirAll(filepath.Dir(bp), 0o755)
		if os.WriteFile(ap, a, 0o644) != nil || os.WriteFile(bp, b, 0o644) != nil {
			return "", false
		}
		cmd := exec.Command("difft", "--color=always", "--width", itoa(width), "--display", "inline", ap, bp)
		var out bytes.Buffer
		cmd.Stdout = &out
		if err := cmd.Run(); err != nil {
			return "", false
		}
		return out.String(), true
	}
	return "", false
}

func itoa(n int) string {
	if n <= 0 {
		n = 120
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
