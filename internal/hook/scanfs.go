package hook

import (
	"bytes"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/elythi0n/pawprints/internal/store"
)

// The file walk, git-ignore filter, and git baseline that command capture
// (capture.go) is built on.

const (
	scanBudget   = 2 * time.Second
	scanMaxFiles = 200_000
)

// skipDirs are never walked: they are large and never hold source edits.
var skipDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true, ".venv": true, "venv": true,
	"__pycache__": true, ".cache": true, ".next": true, ".turbo": true, "target": true,
	".idea": true, ".gradle": true, ".terraform": true,
}

// modifiedSince walks the project for regular files changed after start.
// It stops at a time and file budget so a huge tree cannot stall anything.
func modifiedSince(root string, start time.Time, ig *store.Ignore) []string {
	deadline := time.Now().Add(scanBudget)
	seen := 0
	var out []string
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		seen++
		if seen > scanMaxFiles || time.Now().After(deadline) {
			return fs.SkipAll
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if path != root && (skipDirs[d.Name()] || ig.Match(rel+"/")) {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || ig.Match(rel) {
			return nil
		}
		info, err := d.Info()
		if err == nil && info.ModTime().After(start) {
			out = append(out, path)
		}
		return nil
	})
	return out
}

// gitTop finds the git work tree holding path, if any, without leaving root.
func gitTop(root, path string) string {
	dir := filepath.Dir(path)
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		if dir == root || dir == filepath.Dir(dir) || !store.Within(root, dir) {
			return ""
		}
		dir = filepath.Dir(dir)
	}
}

// dropGitIgnored removes files their git repository ignores, such as build
// output, so a `go build` does not record a binary.
func dropGitIgnored(root string, files []string) []string {
	byTop := map[string][]string{}
	var out []string
	for _, f := range files {
		if top := gitTop(root, f); top != "" {
			byTop[top] = append(byTop[top], f)
		} else {
			out = append(out, f)
		}
	}
	for top, list := range byTop {
		var in bytes.Buffer
		for _, f := range list {
			rel, _ := filepath.Rel(top, f)
			in.WriteString(rel + "\n")
		}
		cmd := exec.Command("git", "-C", top, "check-ignore", "--stdin")
		cmd.Stdin = &in
		res, _ := cmd.Output() // exit 1 means nothing ignored
		ignored := map[string]bool{}
		for _, line := range strings.Split(string(res), "\n") {
			if line != "" {
				ignored[filepath.Join(top, line)] = true
			}
		}
		for _, f := range list {
			if !ignored[f] {
				out = append(out, f)
			}
		}
	}
	return out
}

// gitBaseline returns the content git holds for a file (index, else HEAD),
// the best "before" for a file Pawprints has not seen yet.
func gitBaseline(root, abs string) ([]byte, bool) {
	top := gitTop(root, abs)
	if top == "" {
		return nil, false
	}
	rel, _ := filepath.Rel(top, abs)
	rel = filepath.ToSlash(rel)
	for _, spec := range []string{":" + rel, "HEAD:" + rel} {
		out, err := exec.Command("git", "-C", top, "cat-file", "blob", spec).Output()
		if err == nil {
			return out, true
		}
	}
	return nil, false
}
