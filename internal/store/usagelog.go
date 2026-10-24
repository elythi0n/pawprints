package store

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// UsageBucket is one line of usage/<session>.jsonl: the tokens one model
// spent in one minute. A minute can appear on several lines when it spans
// two reads of the transcript; readers add them up.
type UsageBucket struct {
	Minute  string `json:"minute"`
	Model   string `json:"model"`
	Account string `json:"account,omitempty"`
	Agent   string `json:"agent,omitempty"`
	Usage
}

// At is the bucket's minute, or zero when it does not parse.
func (b UsageBucket) At() time.Time {
	t, _ := time.Parse(time.RFC3339, b.Minute)
	return t
}

func (p *Project) usageDir() string { return filepath.Join(p.Dir, "usage") }

func (p *Project) usagePath(session string) string {
	return filepath.Join(p.usageDir(), sanitize(session)+".jsonl")
}

// AppendUsage adds buckets to a session's usage log. The hook calls it while
// it holds the project lock, so it takes no lock of its own.
func (p *Project) AppendUsage(session string, buckets []UsageBucket) error {
	if len(buckets) == 0 {
		return nil
	}
	if err := os.MkdirAll(p.usageDir(), 0o755); err != nil {
		return err
	}
	var buf bytes.Buffer
	for _, b := range buckets {
		line, err := json.Marshal(b)
		if err != nil {
			return err
		}
		buf.Write(line)
		buf.WriteByte('\n')
	}
	f, err := os.OpenFile(p.usagePath(session), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	_, err = f.Write(buf.Bytes())
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// copyUsageFrom replaces a session's usage log with the one another project
// holds for it; a missing log there leaves this one as it is.
func (p *Project) copyUsageFrom(src *Project, session string) error {
	data, err := os.ReadFile(src.usagePath(session))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := os.MkdirAll(p.usageDir(), 0o755); err != nil {
		return err
	}
	return writeAtomic(p.usagePath(session), data)
}

// ResetUsage removes a session's usage log, for a rebuild from the transcript.
func (p *Project) ResetUsage(session string) error {
	err := os.Remove(p.usagePath(session))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// Usage reads the usage logs of every session, keeping buckets at or after
// since. A log last written before since is skipped without being opened.
func (p *Project) Usage(since time.Time) (map[string][]UsageBucket, error) {
	entries, err := os.ReadDir(p.usageDir())
	if errors.Is(err, os.ErrNotExist) {
		return map[string][]UsageBucket{}, nil
	}
	if err != nil {
		return nil, err
	}
	cut := since.Truncate(time.Minute)
	out := map[string][]UsageBucket{}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		if !since.IsZero() {
			if info, err := e.Info(); err == nil && info.ModTime().Before(cut) {
				continue
			}
		}
		buckets, err := readUsageFile(filepath.Join(p.usageDir(), name), cut)
		if err != nil {
			continue
		}
		if len(buckets) > 0 {
			out[strings.TrimSuffix(name, ".jsonl")] = buckets
		}
	}
	return out, nil
}

// SessionUsage reads one session's usage log.
func (p *Project) SessionUsage(session string) ([]UsageBucket, error) {
	buckets, err := readUsageFile(p.usagePath(session), time.Time{})
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return buckets, err
}

// HasUsageLog reports whether a session has a usage log yet.
func (p *Project) HasUsageLog(session string) bool {
	_, err := os.Stat(p.usagePath(session))
	return err == nil
}

func readUsageFile(path string, since time.Time) ([]UsageBucket, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []UsageBucket
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		var b UsageBucket
		if json.Unmarshal(sc.Bytes(), &b) != nil {
			continue
		}
		if !since.IsZero() && b.At().Before(since) {
			continue
		}
		out = append(out, b)
	}
	return out, sc.Err()
}
