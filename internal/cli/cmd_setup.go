package cli

import (
	"fmt"
	"os"

	"github.com/mattn/go-isatty"

	"github.com/elythi0n/pawprints/internal/setup"
	"github.com/elythi0n/pawprints/internal/tui"
	"github.com/elythi0n/pawprints/internal/ui"
)

// cmdSetup shows the integrations checklist. With --yes, or without a
// terminal, it applies the defaults: everything found that is missing or out
// of date. With --json and no --yes it only reports what it found.
func (c *cli) cmdSetup(args []string) int {
	fs := newFlags("setup")
	yes := fs.Bool("yes", false, "")
	if err := parseArgs(fs, args); err != nil {
		return c.fail(err.Error(), 2)
	}
	interactive := isatty.IsTerminal(os.Stdin.Fd()) && isatty.IsTerminal(os.Stdout.Fd())
	if !*yes && !c.json && interactive {
		return tui.RunSetup()
	}
	items := setup.Scan()
	if c.json && !*yes {
		for _, it := range items {
			c.emit(it)
		}
		return 0
	}
	code := 0
	results := map[string]string{}
	for _, it := range items {
		if !it.Default {
			if !c.json {
				fmt.Fprintf(c.out, "  %s %-27s %s\n", ui.Dim.Render("·"), it.Title, ui.Dim.Render(it.State+": "+it.Note))
			}
			continue
		}
		res, err := setup.Apply(it)
		if err != nil {
			code = 1
			results[it.ID] = "failed: " + err.Error()
			if c.json {
				c.emit(map[string]any{"id": it.ID, "ok": false, "error": err.Error()})
			} else {
				fmt.Fprintf(c.out, "  %s %-27s %s\n", ui.Err.Render("✗"), it.Title, err.Error())
			}
			continue
		}
		results[it.ID] = "done"
		if c.json {
			c.emit(map[string]any{"id": it.ID, "ok": true, "result": res})
		} else {
			fmt.Fprintf(c.out, "  %s %-27s %s\n", ui.Ok.Render("✓"), it.Title, res)
		}
	}
	_ = setup.SaveMarker(false, results)
	return code
}
