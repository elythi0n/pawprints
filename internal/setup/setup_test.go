package setup

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeVSIX builds a minimal .vsix carrying a version.
func fakeVSIX(t *testing.T, version string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("extension/package.json")
	w.Write([]byte(`{"name":"pawprints","version":"` + version + `"}`))
	zw.Close()
	return buf.Bytes()
}

// fakeEditor puts a script named cmd on PATH that reports listed as its
// installed extensions and logs install calls.
func fakeEditor(t *testing.T, bin, cmd, listed string) string {
	t.Helper()
	log := filepath.Join(t.TempDir(), cmd+".log")
	script := "#!/bin/sh\ncase \"$1\" in\n--list-extensions) printf '%s\\n' \"" + listed + "\" ;;\n--install-extension) echo \"$@\" > " + log + "; test -s \"$2\" ;;\nesac\n"
	if err := os.WriteFile(filepath.Join(bin, cmd), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return log
}

func env(t *testing.T) (bin string) {
	t.Helper()
	bin = t.TempDir()
	// Only the fakes: a real editor on this machine must never be reached.
	t.Setenv("PATH", bin)
	t.Setenv("PAWPRINTS_HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("SHELL", "/usr/bin/fish")
	UseVSIXForTest(fakeVSIX(t, "0.2.0"))
	t.Cleanup(func() { UseVSIXForTest(nil) })
	return bin
}

func byID(items []Item) map[string]Item {
	m := map[string]Item{}
	for _, it := range items {
		m[it.ID] = it
	}
	return m
}

func TestScanStates(t *testing.T) {
	bin := env(t)
	fakeEditor(t, bin, "code", "ms-python.python@1.0.0\nelythion.pawprints@0.1.0")
	fakeEditor(t, bin, "cursor", "")
	items := byID(Scan())
	if it := items["vscode"]; it.State != Outdated || !it.Default || !strings.Contains(it.Note, "0.1.0") || !strings.Contains(it.Note, "0.2.0") {
		t.Fatalf("vscode %+v", it)
	}
	if it := items["cursor"]; it.State != Missing || !it.Default {
		t.Fatalf("cursor %+v", it)
	}
	if it := items["vscodium"]; it.State != Unavailable || it.Default || it.Actionable() {
		t.Fatalf("vscodium %+v", it)
	}
	if it := items["hooks"]; it.State != Missing || !it.Default {
		t.Fatalf("hooks %+v", it)
	}
	if it := items["completions"]; it.State != Missing || it.Detail != "fish" {
		t.Fatalf("completions %+v", it)
	}
}

func TestApplyEverything(t *testing.T) {
	bin := env(t)
	codeLog := fakeEditor(t, bin, "code", "")
	for _, it := range Scan() {
		if !it.Default {
			continue
		}
		if _, err := Apply(it); err != nil {
			t.Fatalf("%s: %v", it.ID, err)
		}
	}
	logged, _ := os.ReadFile(codeLog)
	if !strings.Contains(string(logged), "--install-extension") || !strings.Contains(string(logged), "pawprints-0.2.0.vsix") || !strings.Contains(string(logged), "--force") {
		t.Fatalf("editor install call: %q", logged)
	}
	settings, _ := os.ReadFile(filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "settings.json"))
	if !strings.Contains(string(settings), "hook bash-post") {
		t.Fatalf("hooks not registered: %s", settings)
	}
	home := os.Getenv("HOME")
	if _, err := os.Stat(filepath.Join(home, ".config", "fish", "completions", "pawprints.fish")); err != nil {
		t.Fatal("fish completions not written")
	}
	// After applying, the hooks and completions are current. The fake editor
	// does not remember installs, so it is left out.
	items := byID(Scan())
	if items["hooks"].State != Current || items["completions"].State != Current {
		t.Fatalf("after apply: hooks %s, completions %s", items["hooks"].State, items["completions"].State)
	}
}

func TestHooksProvidedByPlugin(t *testing.T) {
	env(t)
	dir := filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "plugins")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "installed_plugins.json"), []byte(`{"plugins":{"pawprints@pawprints":[{}]}}`), 0o644)
	if it := byID(Scan())["hooks"]; it.State != Provided || it.Actionable() {
		t.Fatalf("hooks with the plugin %+v", it)
	}
	// A plugin switched off in enabledPlugins does not count.
	os.WriteFile(filepath.Join(os.Getenv("CLAUDE_CONFIG_DIR"), "settings.json"), []byte(`{"enabledPlugins":{"pawprints@pawprints":false}}`), 0o644)
	if it := byID(Scan())["hooks"]; it.State == Provided {
		t.Fatal("a disabled plugin provides nothing")
	}
}

func TestNoEmbeddedExtension(t *testing.T) {
	bin := env(t)
	UseVSIXForTest(nil)
	if _, _, ok := EmbeddedVSIX(); ok {
		t.Skip("this build embeds a real vsix")
	}
	fakeEditor(t, bin, "code", "")
	if it := byID(Scan())["vscode"]; it.Actionable() || !strings.Contains(it.Note, "carries no extension") {
		t.Fatalf("dev build without a vsix %+v", it)
	}
}

func TestMarker(t *testing.T) {
	env(t)
	items := []Item{{ID: "vscode", State: Outdated}}
	if !ShouldOffer(nil) {
		t.Fatal("first run offers setup")
	}
	if err := SaveMarker(true, nil); err != nil {
		t.Fatal(err)
	}
	if ShouldOffer(items) {
		t.Fatal("same version: no offer even with something outdated")
	}
	m, ok := LoadMarker()
	if !ok || !m.Skipped {
		t.Fatalf("marker %+v", m)
	}
}

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{{"0.1.0", "0.2.0", -1}, {"0.10.0", "0.9.9", 1}, {"v1.0.0", "1.0", 0}, {"1.0.0-dev", "1.0.0", 0}}
	for _, c := range cases {
		if got := compareVersions(c.a, c.b); got != c.want {
			t.Errorf("%s vs %s: %d want %d", c.a, c.b, got, c.want)
		}
	}
	if v := vsixVersion(fakeVSIX(t, "3.4.5")); v != "3.4.5" {
		t.Fatalf("vsix version %q", v)
	}
}
