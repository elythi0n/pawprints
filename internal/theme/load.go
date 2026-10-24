package theme

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Info describes one available theme.
type Info struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Source string `json:"source"` // "builtin", or the file path of a custom theme
	Dark   bool   `json:"dark"`
	Err    string `json:"error,omitempty"`
}

// File is the JSON format of a custom theme. Every role is optional when
// Extends names a theme to start from.
//
//	{
//	  "name": "Midnight Rose",
//	  "extends": "tokyo-night",
//	  "colors": { "accent": "#FF79C6", "added_bg": "#1F3A2E" }
//	}
type File struct {
	Name    string            `json:"name"`
	Extends string            `json:"extends"`
	Dark    *bool             `json:"dark,omitempty"`
	Colors  map[string]string `json:"colors"`
}

// ID turns a display name or file name into the id used to select a theme.
func ID(name string) string {
	name = strings.TrimSuffix(strings.TrimSuffix(filepath.Base(name), ".json"), ".conf")
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case !dash && b.Len() > 0:
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.TrimSuffix(b.String(), "-")
}

// List returns built-in themes followed by custom ones found in dir.
// Custom themes with the same id as a built-in replace it.
func List(dir string) []Info {
	custom := map[string]Info{}
	for _, path := range customFiles(dir) {
		id := ID(path)
		p, err := loadFile(path, dir, 0)
		info := Info{ID: id, Name: p.Name, Source: path, Dark: p.Dark}
		if err != nil {
			info.Err = err.Error()
			if info.Name == "" {
				info.Name = id
			}
		}
		custom[id] = info
	}
	var out []Info
	for _, id := range BuiltinNames() {
		if c, ok := custom[id]; ok {
			out = append(out, c)
			delete(custom, id)
			continue
		}
		switch id {
		case Auto:
			out = append(out, Info{ID: Auto, Name: "Auto (dark or light from the terminal)", Source: "builtin", Dark: true})
		default:
			b := builtins[id]
			out = append(out, Info{ID: id, Name: b.Name, Source: "builtin", Dark: b.Dark})
		}
	}
	ids := make([]string, 0, len(custom))
	for id := range custom {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		out = append(out, custom[id])
	}
	return out
}

func customFiles(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || strings.HasPrefix(name, ".") {
			continue
		}
		if strings.HasSuffix(name, ".json") || strings.HasSuffix(name, ".conf") {
			out = append(out, filepath.Join(dir, name))
		}
	}
	sort.Strings(out)
	return out
}

// Load resolves a theme id: a custom file in dir wins over a built-in with
// the same id. "auto" picks by dark, the terminal's background.
func Load(id, dir string, dark bool) (Palette, error) {
	id = ID(id)
	if id == "" || id == Auto {
		if dark {
			id = Default
		} else {
			id = Light
		}
	}
	return load(id, dir, 0)
}

func load(id, dir string, depth int) (Palette, error) {
	if depth > 8 {
		return Palette{}, errors.New("themes extend each other in a loop")
	}
	for _, path := range customFiles(dir) {
		if ID(path) == id {
			return loadFile(path, dir, depth)
		}
	}
	if p, ok := builtins[id]; ok {
		return p, nil
	}
	return Palette{}, fmt.Errorf("no theme %q; `pawprints theme list` shows what is available", id)
}

func loadFile(path, dir string, depth int) (Palette, error) {
	if strings.HasSuffix(path, ".conf") {
		t, name, err := ParseKitty(path)
		if err != nil {
			return Palette{Name: ID(path)}, err
		}
		p := FromTerminal(name, t)
		return p, p.Validate()
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Palette{}, err
	}
	var f File
	if err := json.Unmarshal(data, &f); err != nil {
		return Palette{Name: ID(path)}, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	base := builtins[Default]
	if f.Extends != "" {
		parent := ID(f.Extends)
		if parent == ID(path) {
			// A file may extend the built-in it replaces.
			if b, ok := builtins[parent]; ok {
				base = b
			} else {
				return Palette{}, fmt.Errorf("%s extends itself", filepath.Base(path))
			}
		} else if base, err = load(parent, dir, depth+1); err != nil {
			return Palette{Name: f.Name}, err
		}
	}
	over := Palette{Name: f.Name}
	for role, value := range f.Colors {
		v := normalize(value)
		if v == "" && value != "" {
			return Palette{Name: f.Name}, fmt.Errorf("%s: %s=%q is not a color", filepath.Base(path), role, value)
		}
		if !over.Set(role, v) {
			return Palette{Name: f.Name}, fmt.Errorf("%s: unknown role %q (roles: %s)", filepath.Base(path), role, strings.Join(Roles, ", "))
		}
	}
	p := Merge(base, over)
	if p.Name == "" {
		p.Name = ID(path)
	}
	switch {
	case f.Dark != nil:
		p.Dark = *f.Dark
	case f.Colors["base"] != "":
		p.Dark = luminance(p.Base) < 0.5
	}
	// A new base without new tints would leave the old theme's tints behind.
	if f.Colors["base"] != "" || f.Colors["green"] != "" || f.Colors["red"] != "" {
		if f.Colors["added_bg"] == "" {
			p.AddedBg = mix(p.Base, p.Green, 0.18)
		}
		if f.Colors["removed_bg"] == "" {
			p.RemovedBg = mix(p.Base, p.Red, 0.18)
		}
	}
	return p, p.Validate()
}

// ParseKitty reads a kitty color file: "background #hex", "color0 #hex"...
// The first comment line, if any, names the theme.
func ParseKitty(path string) (Terminal, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return Terminal{}, "", err
	}
	defer f.Close()
	var t Terminal
	name := ""
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "#") {
			if name == "" {
				name = strings.TrimSpace(strings.SplitN(strings.TrimPrefix(line, "#"), "—", 2)[0])
				name = strings.TrimSpace(strings.SplitN(name, " - ", 2)[0])
			}
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		v := normalize(fields[1])
		switch key := fields[0]; {
		case key == "background":
			t.Background = v
		case key == "foreground":
			t.Foreground = v
		case key == "selection_background":
			t.Selection = v
		case strings.HasPrefix(key, "color"):
			if n, err := strconv.Atoi(strings.TrimPrefix(key, "color")); err == nil && n >= 0 && n < 16 {
				t.Colors[n] = v
			}
		}
	}
	if err := sc.Err(); err != nil {
		return t, name, err
	}
	if name == "" || strings.Contains(strings.ToLower(name), "http") {
		name = ID(path)
	}
	if t.Background == "" || t.Foreground == "" {
		return t, name, fmt.Errorf("%s: needs background and foreground", filepath.Base(path))
	}
	return t, name, nil
}

// Import copies a theme file into dir after checking it loads, and returns
// the id to select it by.
func Import(src, dir string) (string, error) {
	if !strings.HasSuffix(src, ".json") && !strings.HasSuffix(src, ".conf") {
		return "", errors.New("a theme file ends in .json (pawprints roles) or .conf (kitty colors)")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return "", err
	}
	dst := filepath.Join(dir, filepath.Base(src))
	// The extension decides how a theme file is read, so the check copy keeps it;
	// the leading dot keeps it out of the theme list meanwhile.
	tmp := filepath.Join(dir, ".import-"+filepath.Base(src))
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return "", err
	}
	id := ID(dst)
	if _, err := loadFile(tmp, dir, 0); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return id, os.Rename(tmp, dst)
}

// WriteFile creates <dir>/<id>.json holding every role of the base theme,
// ready to edit, and returns its path.
func WriteFile(dir, name, base string) (string, error) {
	p, err := Load(base, dir, true)
	if err != nil {
		return "", err
	}
	id := ID(name)
	if id == "" {
		return "", fmt.Errorf("theme name %q has no letters or digits", name)
	}
	path := filepath.Join(dir, id+".json")
	if _, err := os.Stat(path); err == nil {
		return "", fmt.Errorf("%s already exists", path)
	}
	colors := map[string]string{}
	for _, role := range Roles {
		colors[role] = p.Get(role)
	}
	baseID := ID(base)
	if baseID == Auto || baseID == "" {
		baseID = Default
	}
	if baseID == id {
		baseID = Default
	}
	data, err := json.MarshalIndent(File{Name: name, Extends: baseID, Colors: colors}, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return path, os.WriteFile(path, append(data, '\n'), 0o644)
}

// ImportAll imports one theme file, or every .json and .conf file in a
// directory, and returns the ids added and the ids skipped. A folder import
// skips themes that already ship, so importing a terminal's theme folder adds
// only what is new; naming a file explicitly replaces a built-in.
func ImportAll(src, dir string) (ids, skipped []string, errs []error) {
	info, err := os.Stat(src)
	if err != nil {
		return nil, nil, []error{err}
	}
	files := []string{src}
	if info.IsDir() {
		files = nil
		for _, f := range customFiles(src) {
			if _, builtin := builtins[ID(f)]; builtin {
				skipped = append(skipped, ID(f))
				continue
			}
			files = append(files, f)
		}
	}
	for _, f := range files {
		id, err := Import(f, dir)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", filepath.Base(f), err))
			continue
		}
		ids = append(ids, id)
	}
	return ids, skipped, errs
}
