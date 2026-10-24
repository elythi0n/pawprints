// Package store owns everything under ~/.pawprints: projects, the git object
// store, the event log, session records, pins, and the small state files the
// hook keeps. Nothing else in the binary touches that directory.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// FormatVersion is the on-disk layout version written to the "format" file.
const FormatVersion = 1

// EventVersion is the schema version written to every event line.
const EventVersion = 1

// Home returns the data directory, honoring PAWPRINTS_HOME.
func Home() string {
	if h := os.Getenv("PAWPRINTS_HOME"); h != "" {
		return h
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".pawprints"
	}
	return filepath.Join(home, ".pawprints")
}

// EnsureHome creates the data directory and the format file, and refuses to
// run against a newer format than this binary understands.
func EnsureHome() (string, error) {
	home := Home()
	if err := os.MkdirAll(filepath.Join(home, "projects"), 0o755); err != nil {
		return "", err
	}
	formatPath := filepath.Join(home, "format")
	data, err := os.ReadFile(formatPath)
	switch {
	case errors.Is(err, os.ErrNotExist):
		if err := os.WriteFile(formatPath, []byte(strconv.Itoa(FormatVersion)+"\n"), 0o644); err != nil {
			return "", err
		}
	case err != nil:
		return "", err
	default:
		v, convErr := strconv.Atoi(trimSpace(string(data)))
		if convErr != nil {
			return "", fmt.Errorf("unreadable format file %s: %q", formatPath, string(data))
		}
		if v > FormatVersion {
			return "", fmt.Errorf("store format %d is newer than this binary supports (%d); upgrade pawprints", v, FormatVersion)
		}
	}
	return home, nil
}

func trimSpace(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == ' ' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}

// Price is the per-million-token price of one model. Pawprints ships the
// API prices; config.json can override a model's.
type Price struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cache_read"`
	CacheWrite float64 `json:"cache_write"`
}

// Config is ~/.pawprints/config.json. Every key is optional.
type Config struct {
	RetentionDays   int      `json:"retention_days"`
	MaxProjectBytes int64    `json:"max_project_bytes"`
	MaxFileBytes    int64    `json:"max_file_bytes"`
	Ignore          []string `json:"ignore"`
	RecordPrompts   *bool    `json:"record_prompts,omitempty"`
	Log             bool     `json:"log"`
	Theme           string   `json:"theme,omitempty"`
	// FileVersions is how files under a prompt are listed: "combined" (one
	// row per file, one diff across all its edits) or "each" (every version).
	FileVersions string `json:"file_versions,omitempty"`
	// FileLayout is how files under a prompt are shown: "list" or "tree".
	FileLayout string `json:"file_layout,omitempty"`
	// WatchDirs are folders outside any project that command scans also
	// check, such as ~/.config; "~/" is expanded.
	WatchDirs []string         `json:"watch_dirs,omitempty"`
	Prices    map[string]Price `json:"prices,omitempty"`
	// SpikeFactor is how many times the normal pace a moment must reach to
	// count as a spike in metrics.
	SpikeFactor float64 `json:"spike_factor,omitempty"`
	// ClaudeHomes are extra Claude Code config folders to read sessions and
	// transcripts from, beside ~/.claude and the ones hooks report.
	ClaudeHomes []string `json:"claude_homes,omitempty"`
}

// DefaultConfig returns the values used when config.json is missing a key.
func DefaultConfig() Config {
	t := true
	return Config{
		RetentionDays:   90,
		MaxProjectBytes: 1 << 30,
		MaxFileBytes:    10 << 20,
		Ignore:          []string{".git/", "node_modules/", "vendor/", "*.lock"},
		RecordPrompts:   &t,
		SpikeFactor:     3,
	}
}

// PromptsEnabled reports whether prompt text may be stored.
func (c Config) PromptsEnabled() bool {
	return c.RecordPrompts == nil || *c.RecordPrompts
}

// LoadConfig reads config.json over the defaults. A missing file is not an error.
func LoadConfig() (Config, error) {
	cfg := DefaultConfig()
	data, err := os.ReadFile(filepath.Join(Home(), "config.json"))
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	var file Config
	if err := json.Unmarshal(data, &file); err != nil {
		return cfg, fmt.Errorf("config.json: %w", err)
	}
	if file.RetentionDays > 0 {
		cfg.RetentionDays = file.RetentionDays
	}
	if file.MaxProjectBytes > 0 {
		cfg.MaxProjectBytes = file.MaxProjectBytes
	}
	if file.MaxFileBytes > 0 {
		cfg.MaxFileBytes = file.MaxFileBytes
	}
	if file.Ignore != nil {
		cfg.Ignore = file.Ignore
	}
	if file.RecordPrompts != nil {
		cfg.RecordPrompts = file.RecordPrompts
	}
	cfg.Log = file.Log
	cfg.Theme = file.Theme
	cfg.FileVersions = file.FileVersions
	cfg.FileLayout = file.FileLayout
	cfg.WatchDirs = file.WatchDirs
	cfg.Prices = file.Prices
	if file.SpikeFactor > 0 {
		cfg.SpikeFactor = file.SpikeFactor
	}
	cfg.ClaudeHomes = file.ClaudeHomes
	return cfg, nil
}

// SaveConfig writes config.json.
func SaveConfig(cfg Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(Home(), "config.json"), append(data, '\n'), 0o644)
}

// ThemeDir is where custom themes live.
func ThemeDir() string { return filepath.Join(Home(), "themes") }

// SetConfigValue changes one key of config.json and leaves every other key,
// including ones this version does not know, exactly as it was.
func SetConfigValue(key string, value any) error {
	if _, err := EnsureHome(); err != nil {
		return err
	}
	path := filepath.Join(Home(), "config.json")
	raw := map[string]any{}
	if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
		if err := json.Unmarshal(data, &raw); err != nil {
			return fmt.Errorf("config.json: %w", err)
		}
	}
	if value == nil || value == "" {
		delete(raw, key)
	} else {
		raw[key] = value
	}
	data, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(path, append(data, '\n'))
}

// CombineVersions reports whether files under a prompt show one combined change.
func (c Config) CombineVersions() bool { return c.FileVersions != "each" }

// TreeLayout reports whether files under a prompt are shown in their folders.
func (c Config) TreeLayout() bool { return c.FileLayout == "tree" }
