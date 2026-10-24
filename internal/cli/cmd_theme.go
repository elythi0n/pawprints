package cli

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/elythi0n/pawprints/internal/diff"
	"github.com/elythi0n/pawprints/internal/store"
	"github.com/elythi0n/pawprints/internal/theme"
	"github.com/elythi0n/pawprints/internal/ui"
)

func (c *cli) cmdTheme(args []string) int {
	sub := "list"
	if len(args) > 0 {
		sub, args = args[0], args[1:]
	}
	switch sub {
	case "list", "ls":
		return c.themeList()
	case "show":
		if len(args) < 1 {
			return c.fail("usage: pawprints theme show <id>", 2)
		}
		return c.themeShow(args[0])
	case "set", "use":
		if len(args) < 1 {
			return c.fail("usage: pawprints theme set <id>", 2)
		}
		return c.themeSet(args[0])
	case "new":
		return c.themeNew(args)
	case "import":
		if len(args) < 1 {
			return c.fail("usage: pawprints theme import <file.json|file.conf>...", 2)
		}
		return c.themeImport(args)
	case "path", "dir":
		if c.json {
			c.emit(map[string]string{"dir": store.ThemeDir()})
			return 0
		}
		fmt.Fprintln(c.out, store.ThemeDir())
		return 0
	}
	return c.fail("usage: pawprints "+usages["theme"], 2)
}

func savedTheme() string {
	cfg, _ := store.LoadConfig()
	if cfg.Theme == "" {
		return theme.Auto
	}
	return theme.ID(cfg.Theme)
}

func (c *cli) themeList() int {
	list := theme.List(store.ThemeDir())
	current := savedTheme()
	if c.json {
		for _, t := range list {
			c.emit(map[string]any{"id": t.ID, "name": t.Name, "source": t.Source, "dark": t.Dark, "current": t.ID == current, "error": t.Err})
		}
		return 0
	}
	for _, t := range list {
		mark := "  "
		if t.ID == current {
			mark = ui.Ok.Render("● ")
		}
		swatch := ""
		if p, err := theme.Load(t.ID, store.ThemeDir(), true); err == nil && t.ID != theme.Auto {
			swatch = ui.Swatches(p) + " "
		}
		source := ""
		if t.Source != "builtin" {
			source = ui.Dim.Render(" " + t.Source)
		}
		line := fmt.Sprintf("%s%-18s %s%s%s", mark, t.ID, swatch, t.Name, source)
		if t.Err != "" {
			line += "\n    " + ui.Err.Render(t.Err)
		}
		fmt.Fprintln(c.out, line)
	}
	fmt.Fprintf(c.out, "\n%s %s\n", ui.Dim.Render("custom themes:"), store.ThemeDir())
	return 0
}

func (c *cli) themeShow(id string) int {
	p, err := theme.Load(id, store.ThemeDir(), true)
	if err != nil {
		return c.fail(err.Error(), 1)
	}
	if c.json {
		c.emit(p)
		return 0
	}
	ui.Apply(p)
	fmt.Fprintln(c.out, ui.Title.Render(" "+p.Name+" ")+" "+ui.Swatches(p))
	for _, role := range theme.Roles {
		v := p.Get(role)
		block := "  "
		if v != "" {
			block = ui.Swatch.Background(lipglossColor(v)).Render("  ")
		}
		fmt.Fprintf(c.out, "  %s %-10s %s\n", block, role, ui.Dim.Render(v))
	}
	before := []byte("func greet() string {\n\treturn \"hi\"\n}\n")
	after := []byte("func greet(name string) string {\n\treturn \"hi \" + name\n}\n")
	fmt.Fprintln(c.out)
	fmt.Fprint(c.out, diff.Render(diff.Unified("before", "after", before, after, 3), true))
	return 0
}

func (c *cli) themeSet(id string) int {
	id = theme.ID(id)
	if id != theme.Auto {
		if _, err := theme.Load(id, store.ThemeDir(), true); err != nil {
			return c.fail(err.Error(), 1)
		}
	}
	if err := store.SetConfigValue("theme", id); err != nil {
		return c.failErr(err)
	}
	if c.json {
		c.emit(map[string]string{"theme": id})
		return 0
	}
	_ = ui.UseTheme(id)
	fmt.Fprintf(c.out, "%s %s\n", ui.Ok.Render("theme set to"), id)
	return 0
}

// themeNew writes a JSON theme holding every role of its base, ready to edit.
func (c *cli) themeNew(args []string) int {
	fs := newFlags("theme new")
	from := fs.String("from", "", "")
	if err := parseArgs(fs, args); err != nil {
		return c.fail(err.Error(), 2)
	}
	if fs.NArg() < 1 {
		return c.fail("usage: pawprints theme new <name> [--from <id>]", 2)
	}
	name := fs.Arg(0)
	base := *from
	if base == "" {
		base = savedTheme()
	}
	path, err := theme.WriteFile(store.ThemeDir(), name, base)
	if err != nil {
		return c.fail(err.Error(), 1)
	}
	if c.json {
		c.emit(map[string]string{"id": theme.ID(name), "path": path})
		return 0
	}
	fmt.Fprintf(c.out, "%s %s\n", ui.Ok.Render("created"), path)
	fmt.Fprintf(c.out, "%s\n", ui.Dim.Render("edit the colors, then `pawprints theme set "+theme.ID(name)+"`; the TUI picks up changes when you reopen Settings"))
	return 0
}

func (c *cli) themeImport(sources []string) int {
	code := 0
	for _, src := range sources {
		ids, skipped, errs := theme.ImportAll(src, store.ThemeDir())
		if len(skipped) > 0 && !c.json {
			fmt.Fprintf(c.out, "%s %s %s\n", ui.Dim.Render("skipped"), strings.Join(skipped, ", "), ui.Dim.Render("(already built in; import the file by name to replace one)"))
		}
		for _, err := range errs {
			code = c.fail(err.Error(), 1)
		}
		for _, id := range ids {
			if c.json {
				c.emit(map[string]string{"id": id, "from": src})
				continue
			}
			fmt.Fprintf(c.out, "%s %s %s\n", ui.Ok.Render("imported"), id, ui.Dim.Render("from "+src))
		}
	}
	return code
}

func lipglossColor(v string) lipgloss.Color { return lipgloss.Color(v) }
