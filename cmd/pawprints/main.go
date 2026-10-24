package main

import (
	"os"

	"github.com/elythi0n/pawprints/internal/cli"
	"github.com/elythi0n/pawprints/internal/tui"
)

func main() {
	// The TUI takes only --theme; anything else is a CLI command.
	if len(os.Args) > 1 && !isThemeFlagOnly(os.Args[1:]) {
		os.Exit(cli.Run(os.Args[1:]))
	}
	os.Exit(tui.Run())
}

func isThemeFlagOnly(args []string) bool {
	switch {
	case len(args) == 2 && args[0] == "--theme":
		return true
	case len(args) == 1 && len(args[0]) > 8 && args[0][:8] == "--theme=":
		return true
	}
	return false
}
