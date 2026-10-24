package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// The scan watermark is when the project was last scanned for changes made
// outside Claude's edit tools. Each scan covers the time since the previous
// one, so a change made between two commands is never missed.

type scanMark struct {
	LastNano int64 `json:"last_nano"`
}

func (p *Project) scanMarkPath() string { return filepath.Join(p.Dir, "state", "scan.json") }

// LastScan returns when the project was last scanned, or zero.
func (p *Project) LastScan() time.Time {
	data, err := os.ReadFile(p.scanMarkPath())
	if err != nil {
		return time.Time{}
	}
	var m scanMark
	if json.Unmarshal(data, &m) != nil || m.LastNano == 0 {
		return time.Time{}
	}
	return time.Unix(0, m.LastNano)
}

// MarkScanned moves the watermark forward; it never moves it back, so a
// slow scan that finishes late cannot undo a newer one.
func (p *Project) MarkScanned(at time.Time) error {
	if prev := p.LastScan(); !prev.IsZero() && !at.After(prev) {
		return nil
	}
	data, _ := json.Marshal(scanMark{LastNano: at.UnixNano()})
	return writeAtomic(p.scanMarkPath(), data)
}
