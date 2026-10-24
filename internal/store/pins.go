package store

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Pin marks a version that must never be pruned.
type Pin struct {
	Event   string `json:"event"`
	Hash    string `json:"hash"`
	Path    string `json:"path"`
	Message string `json:"message,omitempty"`
	At      string `json:"at"`
}

func (p *Project) pinsPath() string { return filepath.Join(p.Dir, "pins.jsonl") }

// Pins lists pins, newest first.
func (p *Project) Pins() ([]Pin, error) {
	f, err := os.Open(p.pinsPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Pin
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var pin Pin
		if json.Unmarshal(sc.Bytes(), &pin) == nil && pin.Event != "" {
			out = append(out, pin)
		}
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, sc.Err()
}

// AddPin pins an event's after content (or before when the event deleted it).
func (p *Project) AddPin(ev Event, message string) (Pin, error) {
	hash := ev.AfterHash()
	if hash == "" {
		hash = ev.BeforeHash()
	}
	if hash == "" {
		return Pin{}, fmt.Errorf("event %s has no content to pin", ev.ID)
	}
	pins, err := p.Pins()
	if err != nil {
		return Pin{}, err
	}
	for _, existing := range pins {
		if existing.Event == ev.ID {
			return existing, fmt.Errorf("event %s is already pinned", ev.ID)
		}
	}
	pin := Pin{Event: ev.ID, Hash: hash, Path: ev.Path, Message: message, At: Now(time.Now())}
	return pin, p.appendPin(pin)
}

func (p *Project) appendPin(pin Pin) error {
	data, err := json.Marshal(pin)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(p.pinsPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(data, '\n'))
	return err
}

// RemovePin deletes a pin by event id or prefix.
func (p *Project) RemovePin(prefix string) (Pin, error) {
	pins, err := p.Pins()
	if err != nil {
		return Pin{}, err
	}
	prefix = strings.ToUpper(prefix)
	var keep []Pin
	var removed *Pin
	for _, pin := range pins {
		if removed == nil && strings.HasPrefix(pin.Event, prefix) {
			r := pin
			removed = &r
			continue
		}
		keep = append(keep, pin)
	}
	if removed == nil {
		return Pin{}, fmt.Errorf("no pin matches %q", prefix)
	}
	return *removed, p.writePins(keep)
}

// IsPinned reports whether an event id is pinned.
func (p *Project) IsPinned(eventID string) bool {
	pins, _ := p.Pins()
	for _, pin := range pins {
		if pin.Event == eventID {
			return true
		}
	}
	return false
}

func (p *Project) writePins(pins []Pin) error {
	var b strings.Builder
	for i := len(pins) - 1; i >= 0; i-- {
		data, err := json.Marshal(pins[i])
		if err != nil {
			return err
		}
		b.Write(data)
		b.WriteByte('\n')
	}
	if b.Len() == 0 {
		err := os.Remove(p.pinsPath())
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	return writeAtomic(p.pinsPath(), []byte(b.String()))
}
