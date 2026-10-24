// Package cli is the non-TUI entry point. Handlers parse flags, call the
// feature packages, and render. Every listing supports --json, and in JSON
// mode errors land on stdout too so a caller parses one stream.
package cli

import (
	"bufio"
	_ "embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-isatty"
	"github.com/muesli/termenv"

	"github.com/elythi0n/pawprints/internal/buildinfo"
	"github.com/elythi0n/pawprints/internal/hook"
	"github.com/elythi0n/pawprints/internal/logfile"
	"github.com/elythi0n/pawprints/internal/restore"
	"github.com/elythi0n/pawprints/internal/store"
	"github.com/elythi0n/pawprints/internal/ui"
)

//go:embed AGENTS.md
var agentGuide string

type cli struct {
	json    bool
	color   bool
	project string
	theme   string
	out     io.Writer
	err     io.Writer
	in      io.Reader
}

// Run dispatches argv (without the program name) and returns an exit code.
func Run(args []string) int {
	return RunWith(args, os.Stdin, os.Stdout, os.Stderr)
}

// RunWith is Run with explicit streams, for tests.
func RunWith(args []string, in io.Reader, out, errOut io.Writer) int {
	c := &cli{out: out, err: errOut, in: in}
	args = c.globalFlags(args)
	c.color = !c.json && useColor()
	if !c.color {
		lipgloss.SetColorProfile(termenv.Ascii)
	} else if err := ui.UseTheme(ui.ThemeChoice(c.theme)); err != nil {
		fmt.Fprintln(c.err, ui.Warn.Render("theme:")+" "+err.Error())
	}
	cfg, _ := store.LoadConfig()
	if cfg.Log || isTruthy(os.Getenv("PAWPRINTS_LOG")) {
		logfile.SetEnabled(true, store.Home())
	}
	if len(args) == 0 {
		c.printHelp()
		return 0
	}
	cmd, rest := args[0], args[1:]
	for _, a := range rest {
		if a == "-h" || a == "--help" {
			if u, ok := usages[cmd]; ok {
				fmt.Fprintln(c.out, "usage: pawprints "+u)
				return 0
			}
		}
	}
	switch cmd {
	case "-h", "--help", "help":
		c.printHelp()
		return 0
	case "version", "--version", "-v":
		if c.json {
			c.emit(map[string]string{"version": buildinfo.Version, "commit": buildinfo.Commit, "build_time": buildinfo.BuildTime})
			return 0
		}
		fmt.Fprintln(c.out, buildinfo.String())
		return 0
	case "agent-guide":
		fmt.Fprint(c.out, agentGuide)
		return 0
	case "hook":
		if len(rest) == 0 {
			return c.fail("usage: pawprints hook <pre|post|session-start|session-end|prompt|stop>", 2)
		}
		return hook.Run(rest[0], c.in, c.err)
	case "log":
		return c.cmdLog(rest)
	case "show":
		return c.cmdShow(rest)
	case "diff":
		return c.cmdDiff(rest)
	case "cat":
		return c.cmdCat(rest)
	case "sessions":
		return c.cmdSessions(rest)
	case "turns":
		return c.cmdTurns(rest)
	case "pins":
		return c.cmdPins(rest)
	case "info":
		return c.cmdInfo(rest)
	case "restore":
		return c.cmdRestore(rest)
	case "undo":
		return c.cmdUndo(rest)
	case "pin":
		return c.cmdPin(rest)
	case "unpin":
		return c.cmdUnpin(rest)
	case "export":
		return c.cmdExport(rest)
	case "prune":
		return c.cmdPrune(rest)
	case "clear":
		return c.cmdClear(rest)
	case "install":
		return c.cmdInstall(rest)
	case "uninstall":
		return c.cmdUninstall(rest)
	case "maintain":
		return c.cmdMaintain(rest)
	case "watch":
		return c.cmdWatch(rest)
	case "stats":
		return c.cmdStats(rest)
	case "metrics":
		return c.cmdMetrics(rest)
	case "limits":
		return c.cmdLimits(rest)
	case "usage":
		return c.cmdUsage(rest)
	case "ui":
		return c.cmdUI(rest)
	case "doctor":
		return c.cmdDoctor(rest)
	case "completion":
		return c.cmdCompletion(rest)
	case "hunks":
		return c.cmdHunks(rest)
	case "regroup":
		return c.cmdRegroup(rest)
	case "theme", "themes":
		return c.cmdTheme(rest)
	case "setup":
		return c.cmdSetup(rest)
	}
	return c.fail(fmt.Sprintf("unknown command %q; run `pawprints help`", cmd), 2)
}

// globalFlags strips --json, --log, --no-color, and --project wherever they
// appear so subcommands never re-parse them.
func (c *cli) globalFlags(args []string) []string {
	out := make([]string, 0, len(args))
	noColor := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--json":
			c.json = true
		case a == "--log":
			logfile.SetEnabled(true, store.Home())
		case a == "--no-color":
			noColor = true
		case a == "--project" && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-"):
			c.project = args[i+1]
			i++
		case strings.HasPrefix(a, "--project="):
			c.project = strings.TrimPrefix(a, "--project=")
		case a == "--theme" && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-"):
			c.theme = args[i+1]
			i++
		case strings.HasPrefix(a, "--theme="):
			c.theme = strings.TrimPrefix(a, "--theme=")
		default:
			out = append(out, a)
		}
	}
	if noColor {
		c.color = false
		lipgloss.SetColorProfile(termenv.Ascii)
	}
	return out
}

func useColor() bool {
	if _, off := os.LookupEnv("NO_COLOR"); off {
		return false
	}
	if os.Getenv("PAWPRINTS_COLOR") == "always" {
		return true
	}
	return isatty.IsTerminal(os.Stdout.Fd()) || isatty.IsCygwinTerminal(os.Stdout.Fd())
}

func isTruthy(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// emit writes one JSON value and a newline.
func (c *cli) emit(v any) {
	enc := json.NewEncoder(c.out)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

// fail reports an error and returns the exit code to use.
func (c *cli) fail(msg string, exit int) int {
	if c.json {
		c.emit(map[string]any{"error": msg, "exit": exit})
	} else {
		fmt.Fprintln(c.err, ui.Err.Render("error:")+" "+msg)
	}
	return exit
}

// failErr maps package errors to exit codes: 3 means a file changed since
// Claude's last edit and the caller must choose --three-way or --force.
func (c *cli) failErr(err error) int {
	var modified *restore.ErrModified
	if errors.As(err, &modified) {
		return c.fail(err.Error(), 3)
	}
	return c.fail(err.Error(), 1)
}

// root resolves the project root from --project or the working directory.
func (c *cli) root() string {
	dir := c.project
	if dir == "" {
		dir, _ = os.Getwd()
	}
	abs, err := filepath.Abs(dir)
	if err == nil {
		dir = abs
	}
	return store.ResolveRoot(dir)
}

// openProject returns the project, creating its slice when create is set.
// Read commands pass false and get a friendly "no history" instead of
// creating directories as a side effect.
func (c *cli) openProject(create bool) (*store.Project, error) {
	root := c.root()
	if p, ok := store.FindProjectFor(root); ok {
		return p, nil
	}
	if create {
		return store.OpenProject(root)
	}
	p, ok := store.FindProject(root)
	if !ok {
		return nil, fmt.Errorf("no history for %s yet; the hook records the first edit Claude makes here", root)
	}
	return p, nil
}

// confirm asks the person to type yes. Non-interactive stdin means no.
func (c *cli) confirm(prompt string) bool {
	if c.json {
		return false
	}
	tty, err := os.Open("/dev/tty")
	if err != nil {
		return false
	}
	defer tty.Close()
	fmt.Fprint(c.out, ui.Warn.Render(prompt)+" "+ui.Dim.Render("type yes to continue:")+" ")
	line, _ := bufio.NewReader(tty).ReadString('\n')
	return strings.TrimSpace(line) == "yes"
}

func newFlags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

// parseArgs parses flags that may appear after positional arguments, which
// the standard flag package refuses. Flags are moved in front, carrying the
// value of any flag that takes one.
func parseArgs(fs *flag.FlagSet, args []string) error {
	var flags, positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(a, "-") || a == "-" {
			positional = append(positional, a)
			continue
		}
		flags = append(flags, a)
		name := strings.TrimLeft(a, "-")
		if strings.Contains(name, "=") {
			continue
		}
		f := fs.Lookup(name)
		if f == nil {
			continue
		}
		if b, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && b.IsBoolFlag() {
			continue
		}
		if i+1 < len(args) {
			flags = append(flags, args[i+1])
			i++
		}
	}
	return fs.Parse(append(flags, positional...))
}

// parseSpan reads durations like 30min, 2h, 3d, 2w, 3m (months), 1y, plus
// anything time.ParseDuration accepts.
func parseSpan(s string) (time.Duration, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return 0, errors.New("empty duration")
	}
	units := []struct {
		suffix string
		d      time.Duration
	}{
		{"min", time.Minute},
		{"mo", 30 * 24 * time.Hour},
		{"y", 365 * 24 * time.Hour},
		{"w", 7 * 24 * time.Hour},
		{"d", 24 * time.Hour},
		{"h", time.Hour},
		{"m", 30 * 24 * time.Hour},
	}
	for _, u := range units {
		if strings.HasSuffix(s, u.suffix) {
			n, err := strconv.ParseFloat(strings.TrimSuffix(s, u.suffix), 64)
			if err == nil {
				return time.Duration(n * float64(u.d)), nil
			}
		}
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("cannot read duration %q (use 30min, 2h, 3d, 2w, 3m, 1y)", s)
	}
	return d, nil
}

// sinceFlag turns a --since value into a time: a span back from now, or an
// absolute date.
func sinceFlag(s string, now time.Time) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	if d, err := parseSpan(s); err == nil {
		return now.Add(-d), nil
	}
	for _, layout := range []string{"2006-01-02", "2006-01-02T15:04", time.RFC3339} {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("cannot read --since %q", s)
}

// ago formats a time relative to now for human output.
func ago(t time.Time, now time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
	return t.Format("2006-01-02")
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

func shortID(id string) string {
	if len(id) > 10 {
		return id[:10]
	}
	return id
}

// usages are the one-line synopses printed for `<command> --help`.
var usages = map[string]string{
	"log":        "log [--limit N] [--session ID|current] [--turn ID] [--path P] [--since T] [--kind K] [--all-events]",
	"show":       "show <event-id>",
	"diff":       "diff <event-id> [--against previous|current|<event-id>] [--from <event-id>]",
	"metrics":    "metrics [--since T] [--until T] [--bucket 1m|5m|15m|1h|6h|1d] [--account ID|email] [--session ID] [--all]",
	"limits":     "limits record --session ID --kind five_hour|seven_day --percent N [--resets-at TIME]",
	"usage":      "usage backfill [--here]   (rebuild token history from the transcripts still on disk)",
	"ui":         "ui [--port N] [--range 1h|24h|7d|30d] [--no-open] [--keep]   (graphs and history in the browser)",
	"hunks":      "hunks <event-id> [--against previous|current]",
	"cat":        "cat <hash|event-id> [--before]",
	"sessions":   "sessions [--live] [--all] [--since T]",
	"turns":      "turns [--session ID|current]",
	"pins":       "pins",
	"info":       "info",
	"restore":    "restore <event-id> [--before] [--three-way|--force] [--dry-run] [--yes]",
	"undo":       "undo [--turn [ID]] [--session ID] [--three-way|--force] [--dry-run] [--yes]",
	"pin":        "pin <event-id> [-m message]",
	"unpin":      "unpin <event-id>",
	"export":     "export --session ID | --turn ID | --since T",
	"prune":      "prune",
	"clear":      "clear --older-than T | --session ID | --project | --all [--pins] [--dry-run] [--yes]",
	"install":    "install",
	"uninstall":  "uninstall",
	"maintain":   "maintain",
	"watch":      "watch [--all]",
	"stats":      "stats [--since T] [--all] [--daily] [--top N]",
	"doctor":     "doctor [--fix]",
	"completion": "completion bash|zsh|fish",
	"hook":       "hook pre|post|session-start|session-end|prompt|stop   (reads Claude Code hook JSON on stdin)",
	"regroup":    "regroup [--dry-run]   (repair history older versions grouped wrongly: projects, outside-Claude changes, subagent prompts)",
	"theme":      "theme list | show <id> | set <id> | new <name> [--from <id>] | import <file-or-dir>... | path",
	"setup":      "setup [--yes] [--json]   (hooks, editor extensions, shell completions)",
}

func (c *cli) printHelp() {
	title := ui.Title.Render(" pawprints ")
	fmt.Fprintf(c.out, "%s %s\n\n", title, ui.Dim.Render("version history of every file Claude touches"))
	sections := []struct {
		name string
		rows [][2]string
	}{
		{"browse", [][2]string{
			{"pawprints", "open the terminal UI"},
			{"log [--limit N] [--session ID|current] [--turn ID] [--path P] [--since T] [--kind K]", "list edits, newest first"},
			{"show <event>", "one event with its diff"},
			{"diff <event> [--against previous|current|<event>]", "diff a version against something"},
			{"cat <hash|event> [--before]", "print stored content"},
			{"sessions [--live] [--all] [--since T]", "sessions with status, prompt, counts"},
			{"turns [--session ID|current]", "prompts of a session with their edits"},
			{"pins", "pinned versions"},
			{"stats [--since T] [--all] [--daily] [--top N]", "edits, lines, tokens, API cost"},
			{"metrics [--since T] [--bucket B] [--account A] [--all]", "tokens, spikes, and edits over time"},
			{"ui [--no-open]", "open graphs and history in the browser"},
			{"watch [--all]", "follow events and session changes"},
		}},
		{"change", [][2]string{
			{"restore <event> [--before] [--three-way|--force] [--dry-run] [--yes]", "write a version back"},
			{"undo [--turn [ID]] [--session ID] [--three-way|--force] [--dry-run] [--yes]", "revert the last edit, a turn, or a session"},
			{"pin <event> [-m message] / unpin <event>", "keep a version forever"},
			{"export --session ID | --turn ID | --since T", "net change as a unified patch"},
		}},
		{"maintain", [][2]string{
			{"clear --older-than 3m | --session ID | --project | --all [--dry-run] [--yes] [--pins]", "delete history by scope"},
			{"prune", "apply retention and reclaim space"},
			{"regroup [--dry-run]", "repair history older versions grouped wrongly"},
			{"usage backfill [--here]", "rebuild token history from the transcripts on disk"},
			{"limits record --session ID --kind K --percent N", "store a usage-limit reading (Whiskers does this)"},
			{"setup [--yes]", "connect Claude Code hooks, VS Code and Cursor, and shell completions"},
			{"install / uninstall", "register the hooks in ~/.claude/settings.json"},
			{"doctor [--fix]", "check git, hooks, store, session sources"},
			{"info", "store location, project id, hook status"},
			{"theme list | show | set | new | import | path", "colors for the TUI and CLI; custom themes are JSON or kitty .conf files"},
			{"completion bash|zsh|fish", "shell completions"},
			{"agent-guide", "how an agent should drive this CLI"},
			{"version", "build information"},
		}},
	}
	for _, s := range sections {
		fmt.Fprintln(c.out, ui.PaneTitle.Render(s.name))
		for _, r := range s.rows {
			fmt.Fprintf(c.out, "  %s\n      %s\n", ui.Strong.Render(r[0]), ui.Dim.Render(r[1]))
		}
		fmt.Fprintln(c.out)
	}
	fmt.Fprintln(c.out, ui.PaneTitle.Render("global flags"))
	fmt.Fprintf(c.out, "  %s  %s\n", ui.Strong.Render("--json"), ui.Dim.Render("machine output, one object per line for listings"))
	fmt.Fprintf(c.out, "  %s  %s\n", ui.Strong.Render("--project <path>"), ui.Dim.Render("act on another project (default: the current directory's git root)"))
	fmt.Fprintf(c.out, "  %s  %s\n", ui.Strong.Render("--no-color"), ui.Dim.Render("plain output; NO_COLOR is honored too"))
	fmt.Fprintf(c.out, "  %s  %s\n", ui.Strong.Render("--theme <id>"), ui.Dim.Render("use a theme for this run; PAWPRINTS_THEME works too"))
	fmt.Fprintf(c.out, "  %s  %s\n", ui.Strong.Render("--log"), ui.Dim.Render("write a log file under the data directory"))
	fmt.Fprintf(c.out, "\n%s %s\n", ui.Dim.Render("durations:"), ui.Dim.Render("30min 2h 3d 2w 3m 1y"))
}
