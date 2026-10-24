package store

import (
	"os"
	"path/filepath"
	"syscall"
)

// Lock serializes read-modify-write of the small state files between
// concurrent hook processes. Claude Code runs PostToolUse hooks in parallel
// when it makes parallel tool calls, so two hooks can update the same session
// record at once.
type Lock struct{ f *os.File }

// Acquire blocks until the project lock is held.
func (p *Project) Acquire() (*Lock, error) {
	f, err := os.OpenFile(filepath.Join(p.Dir, ".lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return &Lock{f: f}, nil
}

// lockFile blocks until the lock on path is held, shared or exclusive.
func lockFile(path string, how int) (*Lock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), how); err != nil {
		f.Close()
		return nil, err
	}
	return &Lock{f: f}, nil
}

// eventsLock guards the event files. Hooks append under the shared lock, so
// they never wait for each other; a rewrite takes it exclusive, so no event
// appended meanwhile is lost when the rewritten file replaces the old one.
func (p *Project) eventsLock(exclusive bool) (*Lock, error) {
	how := syscall.LOCK_SH
	if exclusive {
		how = syscall.LOCK_EX
	}
	return lockFile(filepath.Join(p.Dir, ".events-lock"), how)
}

// homeLock serializes read-modify-write of the small files shared by every
// project in the data directory, such as accounts.json.
func homeLock() (*Lock, error) {
	return lockFile(filepath.Join(Home(), "state", ".lock"), syscall.LOCK_EX)
}

// Release unlocks.
func (l *Lock) Release() {
	if l == nil || l.f == nil {
		return
	}
	_ = syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	l.f.Close()
	l.f = nil
}
