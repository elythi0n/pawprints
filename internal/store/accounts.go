package store

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Account is one Claude account Pawprints has seen a session run under,
// kept in accounts.json by its Claude account id. Emails stay on this machine.
type Account struct {
	ID     string `json:"id"`
	Email  string `json:"email,omitempty"`
	Plan   string `json:"plan,omitempty"`
	Org    string `json:"org,omitempty"`
	SeenAt string `json:"seen_at,omitempty"`
}

func accountsPath() string { return filepath.Join(Home(), "accounts.json") }

// Accounts reads every known account.
func Accounts() map[string]Account {
	out := map[string]Account{}
	data, err := os.ReadFile(accountsPath())
	if err != nil {
		return out
	}
	_ = json.Unmarshal(data, &out)
	return out
}

// RememberAccount stores or refreshes an account. It writes only when
// something other than the time changed, or once a day, so the hook stays cheap.
func RememberAccount(a Account, now time.Time) error {
	if a.ID == "" {
		return nil
	}
	// Most hooks find the account unchanged and write nothing, so they check
	// before taking the lock every project shares.
	current := func(all map[string]Account) bool {
		old, ok := all[a.ID]
		seen, _ := time.Parse(time.RFC3339Nano, old.SeenAt)
		return ok && old.Email == a.Email && old.Plan == a.Plan && old.Org == a.Org && now.Sub(seen) < 24*time.Hour
	}
	if current(Accounts()) {
		return nil
	}
	if _, err := EnsureHome(); err != nil {
		return err
	}
	lock, err := homeLock()
	if err != nil {
		return err
	}
	defer lock.Release()
	all := Accounts()
	if current(all) {
		return nil
	}
	a.SeenAt = Now(now)
	all[a.ID] = a
	data, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(accountsPath(), append(data, '\n'))
}

// LimitReading is one line of limits.jsonl: how much of a usage-limit window
// an account had used, as the running Claude session reported it.
type LimitReading struct {
	At       string  `json:"at"`
	Session  string  `json:"session,omitempty"`
	Account  string  `json:"account,omitempty"`
	Kind     string  `json:"kind"`
	Percent  float64 `json:"percent"`
	ResetsAt string  `json:"resets_at,omitempty"`
}

// Time is the reading's time, or zero.
func (l LimitReading) Time() time.Time {
	t, _ := time.Parse(time.RFC3339Nano, l.At)
	return t
}

func limitsPath() string { return filepath.Join(Home(), "limits.jsonl") }

// AppendLimit records one reading.
func AppendLimit(r LimitReading) error {
	if _, err := EnsureHome(); err != nil {
		return err
	}
	line, err := json.Marshal(r)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(limitsPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	_, err = f.Write(append(line, '\n'))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// Limits reads the readings at or after since, oldest first, plus the last
// reading before since for each account and kind so a chart can start from it.
func Limits(since time.Time) ([]LimitReading, error) {
	f, err := os.Open(limitsPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	before := map[string]LimitReading{}
	var out []LimitReading
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var r LimitReading
		if json.Unmarshal(sc.Bytes(), &r) != nil || r.Kind == "" {
			continue
		}
		if !since.IsZero() && r.Time().Before(since) {
			before[r.Account+"\x00"+r.Kind] = r
			continue
		}
		out = append(out, r)
	}
	for _, r := range before {
		out = append(out, r)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Time().Before(out[j].Time()) })
	return out, sc.Err()
}

func claudeHomesPath() string { return filepath.Join(Home(), "state", "claude-homes.json") }

// SeenClaudeHomes lists the Claude Code config folders hooks have reported
// through CLAUDE_CONFIG_DIR, beside the default one.
func SeenClaudeHomes() []string {
	var out []string
	data, err := os.ReadFile(claudeHomesPath())
	if err != nil {
		return nil
	}
	_ = json.Unmarshal(data, &out)
	return out
}

// RememberClaudeHome adds a config folder to the seen list once.
func RememberClaudeHome(dir string) error {
	dir = strings.TrimRight(dir, "/")
	if dir == "" {
		return nil
	}
	for _, d := range SeenClaudeHomes() {
		if d == dir {
			return nil
		}
	}
	lock, err := homeLock()
	if err != nil {
		return err
	}
	defer lock.Release()
	// Read again under the lock: another hook may have added a folder.
	seen := SeenClaudeHomes()
	for _, d := range seen {
		if d == dir {
			return nil
		}
	}
	data, err := json.MarshalIndent(append(seen, dir), "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(claudeHomesPath(), append(data, '\n'))
}
