package store

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// LastKnown is what the store believes a file currently contains, keyed by
// stored path. The pre hook compares it with the disk to catch edits made
// outside the hooks.
type LastKnown struct {
	After string `json:"after"`
	Event string `json:"event"`
	TS    string `json:"ts"`
}

func (p *Project) lastPath() string { return filepath.Join(p.Dir, "state", "last.json") }

// LoadLast reads the map. Missing or corrupt files yield an empty map.
func (p *Project) LoadLast() map[string]LastKnown {
	out := map[string]LastKnown{}
	data, err := os.ReadFile(p.lastPath())
	if err != nil {
		return out
	}
	_ = json.Unmarshal(data, &out)
	return out
}

// SaveLast writes the map atomically.
func (p *Project) SaveLast(m map[string]LastKnown) error {
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return writeAtomic(p.lastPath(), data)
}

// SetLast updates one path under the lock.
func (p *Project) SetLast(path string, lk LastKnown) error {
	lock, err := p.Acquire()
	if err != nil {
		return err
	}
	defer lock.Release()
	m := p.LoadLast()
	if lk.After == "" {
		delete(m, path)
	} else {
		m[path] = lk
	}
	return p.SaveLast(m)
}
