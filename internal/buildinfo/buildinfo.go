// Package buildinfo holds the values stamped into the binary at link time.
package buildinfo

import "fmt"

var (
	Version   = "dev"
	Commit    = "unknown"
	BuildTime = "unknown"
)

// String returns a one-line description like "pawprints v0.1.0 (abc1234, 2026-10-22T10:00Z)".
func String() string {
	return fmt.Sprintf("pawprints %s (%s, %s)", Version, Commit, BuildTime)
}
