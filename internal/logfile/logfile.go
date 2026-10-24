// Package logfile is opt-in logging to a file under the data directory.
// Off by default so the hook never writes anything the user did not ask for.
package logfile

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var (
	mu      sync.Mutex
	enabled bool
	path    string
)

// SetEnabled turns logging on or off and resolves the file path.
func SetEnabled(on bool, home string) {
	mu.Lock()
	defer mu.Unlock()
	enabled = on
	if on {
		path = filepath.Join(home, "pawprints.log")
	}
}

// Enabled reports whether logging is on.
func Enabled() bool {
	mu.Lock()
	defer mu.Unlock()
	return enabled
}

// Path returns the log file path, or "" when disabled.
func Path() string {
	mu.Lock()
	defer mu.Unlock()
	if !enabled {
		return ""
	}
	return path
}

func write(level, format string, args ...any) {
	mu.Lock()
	defer mu.Unlock()
	if !enabled {
		return
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s %s %s\n", time.Now().Format(time.RFC3339), level, fmt.Sprintf(format, args...))
}

// Infof logs at info level.
func Infof(format string, args ...any) { write("info", format, args...) }

// Errorf logs at error level.
func Errorf(format string, args ...any) { write("error", format, args...) }
