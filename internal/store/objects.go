package store

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// objectsDir is the bare git repository used purely as a content store.
func (p *Project) objectsDir() string { return filepath.Join(p.Dir, "objects") }

func (p *Project) initObjects() error {
	dir := p.objectsDir()
	if _, err := os.Stat(filepath.Join(dir, "HEAD")); err == nil {
		return nil
	}
	out, err := exec.Command("git", "init", "--bare", "--quiet", dir).CombinedOutput()
	if err != nil {
		return fmt.Errorf("git init: %s", strings.TrimSpace(string(out)))
	}
	// Loose objects pile up fast; let git pack them when it would anyway.
	_ = exec.Command("git", "--git-dir", dir, "config", "gc.auto", "256").Run()
	return nil
}

func (p *Project) git(args ...string) *exec.Cmd {
	full := append([]string{"--git-dir", p.objectsDir()}, args...)
	cmd := exec.Command("git", full...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	return cmd
}

// PutBytes stores content and returns its blob hash.
func (p *Project) PutBytes(content []byte) (string, error) {
	cmd := p.git("hash-object", "-w", "--stdin")
	cmd.Stdin = bytes.NewReader(content)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git hash-object: %s", strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(string(out)), nil
}

// PutFile stores a file and returns its hash and size. A missing file returns
// an empty hash and no error, which callers record as "did not exist".
func (p *Project) PutFile(path string) (hash string, size int64, err error) {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", 0, nil
	}
	if err != nil {
		return "", 0, err
	}
	cmd := p.git("hash-object", "-w", "--", path)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", 0, fmt.Errorf("git hash-object: %s", strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(string(out)), info.Size(), nil
}

// HashFile computes the blob hash without storing anything.
func (p *Project) HashFile(path string) (string, error) {
	out, err := p.git("hash-object", "--", path).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// Get returns the content of a blob.
func (p *Project) Get(hash string) ([]byte, error) {
	if hash == "" {
		return nil, nil
	}
	cmd := p.git("cat-file", "blob", hash)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("blob %s: %s", short(hash), strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

// Has reports whether a blob exists.
func (p *Project) Has(hash string) bool {
	return p.git("cat-file", "-e", hash).Run() == nil
}

// GC keeps the given hashes reachable through a temporary ref and prunes
// everything else, then repacks.
func (p *Project) GC(keep []string) error {
	refPath := filepath.Join(p.objectsDir(), "refs", "keep")
	if err := os.MkdirAll(filepath.Dir(refPath), 0o755); err != nil {
		return err
	}
	if len(keep) == 0 {
		_ = os.Remove(refPath)
	} else {
		tree, err := p.keepTree(keep)
		if err != nil {
			return err
		}
		if err := os.WriteFile(refPath, []byte(tree+"\n"), 0o644); err != nil {
			return err
		}
	}
	out, err := p.git("gc", "--prune=now", "--quiet").CombinedOutput()
	if err != nil {
		return fmt.Errorf("git gc: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// keepTree writes a tree object listing every blob so one ref keeps them all alive.
func (p *Project) keepTree(hashes []string) (string, error) {
	var in bytes.Buffer
	for i, h := range hashes {
		fmt.Fprintf(&in, "100644 blob %s\tb%d\n", h, i)
	}
	cmd := p.git("mktree")
	cmd.Stdin = &in
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git mktree: %s", strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(string(out)), nil
}

// IsBinary applies git's heuristic: a NUL byte in the first 8000 bytes.
func IsBinary(content []byte) bool {
	n := len(content)
	if n > 8000 {
		n = 8000
	}
	return bytes.IndexByte(content[:n], 0) >= 0
}

func short(hash string) string {
	if len(hash) > 7 {
		return hash[:7]
	}
	return hash
}
