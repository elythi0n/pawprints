package theme

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuiltinsAreComplete(t *testing.T) {
	for _, id := range BuiltinNames() {
		p, err := Load(id, t.TempDir(), true)
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		if p.Name == "" {
			t.Fatalf("%s has no name", id)
		}
	}
	for _, want := range []string{"tokyo-night", "dracula", "eyecandy", "ratatorks", "andromeda", "catppuccin-latte", "terminal", "auto"} {
		found := false
		for _, id := range BuiltinNames() {
			found = found || id == want
		}
		if !found {
			t.Fatalf("missing built-in %s", want)
		}
	}
}

func TestAutoPicksByBackground(t *testing.T) {
	dark, _ := Load(Auto, t.TempDir(), true)
	light, _ := Load(Auto, t.TempDir(), false)
	if dark.Name != "Catppuccin Mocha" || light.Name != "Catppuccin Latte" {
		t.Fatalf("auto: %s / %s", dark.Name, light.Name)
	}
}

func TestKittyConf(t *testing.T) {
	dir := t.TempDir()
	conf := `# EyeCandy — a pink theme
background #2B1728
foreground #F5D6E8
selection_background #D4589E
color0 #3B2238
color1 #FF6B9D
color2 #A8D8A8
color3 #FFD4A8
color4 #B89FFF
color5 #FF85C2
color6 #F0A0C8
color7 #F5D6E8
color8 #6B3D68
`
	os.WriteFile(filepath.Join(dir, "My-Pink.conf"), []byte(conf), 0o644)
	p, err := Load("my-pink", dir, true)
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "EyeCandy" || p.Base != "#2B1728" || p.Red != "#FF6B9D" || p.Accent != "#FF85C2" || !p.Dark {
		t.Fatalf("parsed %+v", p)
	}
	if p.AddedBg == "" || p.AddedBg == p.Base {
		t.Fatal("tints must be derived")
	}
}

func TestJSONExtendsAndOverrides(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "night-pink.json"), []byte(`{"name":"Night Pink","extends":"tokyo-night","colors":{"accent":"#ff79c6","red":"f00"}}`), 0o644)
	os.WriteFile(filepath.Join(dir, "deeper.json"), []byte(`{"extends":"night-pink","colors":{"base":"#101010"}}`), 0o644)
	p, err := Load("night-pink", dir, true)
	if err != nil {
		t.Fatal(err)
	}
	tn, _ := Load("tokyo-night", t.TempDir(), true)
	if p.Accent != "#FF79C6" || p.Red != "#FF0000" || p.Blue != tn.Blue || p.Name != "Night Pink" {
		t.Fatalf("merge %+v", p)
	}
	d, err := Load("deeper", dir, true)
	if err != nil || d.Accent != "#FF79C6" || d.Base != "#101010" || d.AddedBg == p.AddedBg {
		t.Fatalf("chained extends %+v %v", d, err)
	}
	// A custom file can replace a built-in while extending it.
	os.WriteFile(filepath.Join(dir, "dracula.json"), []byte(`{"extends":"dracula","colors":{"accent":"#00ff00"}}`), 0o644)
	dr, err := Load("dracula", dir, true)
	if err != nil || dr.Accent != "#00FF00" || dr.Base != "#282A36" {
		t.Fatalf("override built-in %+v %v", dr, err)
	}
}

func TestBadThemesReportErrors(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "bad-role.json"), []byte(`{"colors":{"sparkle":"#fff"}}`), 0o644)
	os.WriteFile(filepath.Join(dir, "bad-color.json"), []byte(`{"colors":{"accent":"pink"}}`), 0o644)
	os.WriteFile(filepath.Join(dir, "loop-a.json"), []byte(`{"extends":"loop-b"}`), 0o644)
	os.WriteFile(filepath.Join(dir, "loop-b.json"), []byte(`{"extends":"loop-a"}`), 0o644)
	os.WriteFile(filepath.Join(dir, "broken.json"), []byte(`{`), 0o644)
	for _, id := range []string{"bad-role", "bad-color", "loop-a", "broken"} {
		if _, err := Load(id, dir, true); err == nil {
			t.Fatalf("%s should fail", id)
		}
	}
	errs := 0
	for _, info := range List(dir) {
		if info.Err != "" {
			errs++
		}
	}
	if errs != 5 {
		t.Fatalf("List should flag 5 broken themes, got %d", errs)
	}
}

func TestImportAndWriteFile(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	os.WriteFile(filepath.Join(src, "Ocean.conf"), []byte("background #001122\nforeground #ddeeff\ncolor2 #00ff00\ncolor1 #ff0000\n"), 0o644)
	os.WriteFile(filepath.Join(src, "notes.txt"), []byte("not a theme"), 0o644)
	os.WriteFile(filepath.Join(src, "Broken.conf"), []byte("color1 #ff0000\n"), 0o644)
	os.WriteFile(filepath.Join(src, "Dracula.conf"), []byte("background #282A36\nforeground #F8F8F2\n"), 0o644)
	ids, skipped, errs := ImportAll(src, dst)
	if len(ids) != 1 || ids[0] != "ocean" || len(errs) != 1 || len(skipped) != 1 || skipped[0] != "dracula" {
		t.Fatalf("import %v %v %v", ids, skipped, errs)
	}
	// Naming a built-in's file explicitly does replace it.
	if ids, _, errs := ImportAll(filepath.Join(src, "Dracula.conf"), dst); len(ids) != 1 || len(errs) != 0 {
		t.Fatalf("explicit import %v %v", ids, errs)
	}
	if _, err := os.Stat(filepath.Join(dst, "Broken.conf")); err == nil {
		t.Fatal("a theme that does not load must not be imported")
	}
	path, err := WriteFile(dst, "My Theme", "eyecandy")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	for _, role := range Roles {
		if !strings.Contains(string(data), `"`+role+`"`) {
			t.Fatalf("new theme should list role %s:\n%s", role, data)
		}
	}
	p, err := Load("my-theme", dst, true)
	if err != nil || p.Base != "#2B1728" {
		t.Fatalf("written theme loads as eyecandy: %+v %v", p, err)
	}
	if _, err := WriteFile(dst, "My Theme", "nord"); err == nil {
		t.Fatal("must not overwrite an existing theme")
	}
}

func TestID(t *testing.T) {
	cases := map[string]string{"Tokyo Night": "tokyo-night", "/x/Rose-Pine.conf": "rose-pine", "my_theme.json": "my-theme", "EyeCandy": "eyecandy"}
	for in, want := range cases {
		if got := ID(in); got != want {
			t.Errorf("%q: %q want %q", in, got, want)
		}
	}
}

// Every shipped theme must be readable: body text at WCAG AA, and hints,
// accents, and diff colors clearly visible.
func TestBuiltinContrast(t *testing.T) {
	for _, id := range BuiltinNames() {
		if id == Auto || id == TermName {
			continue
		}
		p, _ := Load(id, t.TempDir(), true)
		checks := []struct {
			role string
			min  float64
		}{{"text", 4.5}, {"subtext", 4.5}, {"muted", 3}, {"accent", 3}, {"green", 3}, {"red", 3}, {"yellow", 3}, {"blue", 3}}
		for _, c := range checks {
			if got := contrast(p.Get(c.role), p.Base); got < c.min {
				t.Errorf("%s: %s %s on %s is %.2f:1, want %.1f", id, c.role, p.Get(c.role), p.Base, got, c.min)
			}
		}
		if got := contrast(p.Text, p.AddedBg); got < 4.5 {
			t.Errorf("%s: text on added tint is %.2f:1", id, got)
		}
		if got := contrast(p.Text, p.RemovedBg); got < 4.5 {
			t.Errorf("%s: text on removed tint is %.2f:1", id, got)
		}
	}
}
