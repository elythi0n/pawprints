package setup

import (
	"archive/zip"
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ExtensionID is the editor extension's id: publisher.name.
const ExtensionID = "elythion.pawprints"

// The release build packages the editor extension into assets/ before
// compiling, so the binary can install a version-matched extension offline.
//
//go:embed all:assets
var assets embed.FS

// testVSIX stands in for the embedded extension in tests.
var testVSIX []byte

// UseVSIXForTest makes EmbeddedVSIX return data; nil restores the embedded one.
func UseVSIXForTest(data []byte) { testVSIX = data }

// Editor is one VS Code family editor that takes a .vsix.
type Editor struct {
	ID   string
	Name string
	Cmd  string // command on PATH
	Path string // where it was found; empty when not on this machine
}

// knownEditors are checked in this order.
var knownEditors = []Editor{
	{ID: "vscode", Name: "VS Code", Cmd: "code"},
	{ID: "cursor", Name: "Cursor", Cmd: "cursor"},
	{ID: "vscodium", Name: "VSCodium", Cmd: "codium"},
	{ID: "vscode-insiders", Name: "VS Code Insiders", Cmd: "code-insiders"},
}

// Editors returns every known editor, with Path set for the ones found.
func Editors() []Editor {
	out := make([]Editor, len(knownEditors))
	for i, e := range knownEditors {
		if p, err := exec.LookPath(e.Cmd); err == nil {
			e.Path = p
		}
		out[i] = e
	}
	return out
}

// EmbeddedVSIX returns the extension this build carries and its version.
func EmbeddedVSIX() ([]byte, string, bool) {
	if testVSIX != nil {
		return testVSIX, vsixVersion(testVSIX), true
	}
	data, err := assets.ReadFile("assets/pawprints.vsix")
	if err != nil || len(data) == 0 {
		return nil, "", false
	}
	return data, vsixVersion(data), true
}

// vsixVersion reads extension/package.json inside the .vsix zip.
func vsixVersion(data []byte) string {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return ""
	}
	for _, f := range zr.File {
		if f.Name != "extension/package.json" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return ""
		}
		defer func() { _ = rc.Close() }()
		body, _ := io.ReadAll(io.LimitReader(rc, 1<<20))
		var pkg struct {
			Version string `json:"version"`
		}
		if json.Unmarshal(body, &pkg) == nil {
			return pkg.Version
		}
	}
	return ""
}

// installedVersion asks the editor which version of the extension it has.
// It returns "" when the extension is not installed.
func installedVersion(e Editor) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, e.Path, "--list-extensions", "--show-versions").Output()
	if err != nil {
		return "", fmt.Errorf("%s --list-extensions: %w", e.Cmd, err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if id, version, ok := strings.Cut(line, "@"); ok && strings.EqualFold(id, ExtensionID) {
			return version, nil
		}
	}
	return "", nil
}

func editorItem(e Editor) Item {
	it := Item{ID: e.ID, Title: e.Name + " extension", kind: "editor", editor: &e}
	if e.Path == "" {
		it.State, it.Detail, it.Note = Unavailable, e.Cmd+" not found", "not on this machine"
		return it
	}
	it.Detail = e.Cmd + " found"
	_, bundled, hasVSIX := EmbeddedVSIX()
	installed, err := installedVersion(e)
	switch {
	case err != nil:
		it.State, it.Note = Unavailable, err.Error()
	case installed == "" && !hasVSIX:
		it.State, it.Note = Unavailable, "not installed; this build carries no extension (use the release build or install.sh)"
	case installed == "":
		it.State, it.Note = Missing, "install v"+bundled
	case !hasVSIX:
		it.State, it.Note = Current, "v"+installed+" installed"
	case compareVersions(installed, bundled) < 0:
		it.State, it.Note = Outdated, "v"+installed+" installed → update to v"+bundled
	default:
		it.State, it.Note = Current, "v"+installed+" installed"
	}
	it.Default = it.Actionable()
	return it
}

func applyEditor(e Editor) (string, error) {
	data, version, ok := EmbeddedVSIX()
	if !ok {
		return "", errors.New("this build carries no editor extension")
	}
	dir, err := os.MkdirTemp("", "pawprints-vsix-")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	path := filepath.Join(dir, "pawprints-"+version+".vsix")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, e.Path, "--install-extension", path, "--force").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s --install-extension: %s", e.Cmd, lastLine(string(out), err))
	}
	return "installed v" + version + " in " + e.Name + "; reload its window to load it", nil
}

func lastLine(out string, err error) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if l := strings.TrimSpace(lines[len(lines)-1]); l != "" {
		return l
	}
	return err.Error()
}

// compareVersions compares dotted numbers: -1, 0, or 1. Anything that is
// not a number compares as 0, so odd versions never trigger an update.
func compareVersions(a, b string) int {
	pa, pb := strings.Split(strings.TrimPrefix(a, "v"), "."), strings.Split(strings.TrimPrefix(b, "v"), ".")
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		switch {
		case x < y:
			return -1
		case x > y:
			return 1
		}
	}
	return 0
}
