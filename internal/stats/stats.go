// Package stats aggregates session records and events into the numbers
// behind `pawprints stats`: how much Claude is doing and what it may cost.
package stats

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/elythi0n/pawprints/internal/sessions"
	"github.com/elythi0n/pawprints/internal/store"
	"github.com/elythi0n/pawprints/internal/usage"
)

// FileCount is one row of the most-touched list.
type FileCount struct {
	Path  string `json:"path"`
	Edits int    `json:"edits"`
}

// Day is one row of the daily table.
type Day struct {
	Date     string `json:"date"`
	Edits    int    `json:"edits"`
	Sessions int    `json:"sessions"`
	Files    int    `json:"files"`
	Added    int    `json:"lines_added"`
	Removed  int    `json:"lines_removed"`
	// From Claude Code's own stats cache when available.
	ClaudeMessages  int `json:"claude_messages,omitempty"`
	ClaudeSessions  int `json:"claude_sessions,omitempty"`
	ClaudeToolCalls int `json:"claude_tool_calls,omitempty"`
}

// Report is the full stats output.
type Report struct {
	Projects     int                    `json:"projects"`
	Since        string                 `json:"since,omitempty"`
	Sessions     int                    `json:"sessions"`
	LiveSessions int                    `json:"live_sessions"`
	Turns        int                    `json:"turns"`
	Edits        int                    `json:"edits"`
	External     int                    `json:"external_edits"`
	Files        int                    `json:"files"`
	LinesAdded   int                    `json:"lines_added"`
	LinesRemoved int                    `json:"lines_removed"`
	Usage        map[string]store.Usage `json:"usage"`
	Total        store.Usage            `json:"usage_total"`
	Cost         *float64               `json:"cost_estimate,omitempty"`
	// CostComplete is false when some model in use has no price, so Cost
	// covers only the priced ones.
	CostComplete bool        `json:"cost_complete"`
	TopFiles     []FileCount `json:"top_files"`
	Days         []Day       `json:"days"`
	ClaudeCache  bool        `json:"claude_stats_cache"`
	Storage      Storage     `json:"storage"`
}

// Storage is what the history costs on disk, for judging retention.
type Storage struct {
	Bytes         int64 `json:"bytes"`
	BytesPerEdit  int64 `json:"bytes_per_edit"`
	EditsPerDay   int   `json:"edits_per_day"`
	RetentionDays int   `json:"retention_days"`
	// Projected is the size a project reaches at this pace over the
	// retention window, before git packs blobs into deltas.
	Projected int64 `json:"projected_bytes"`
	CapBytes  int64 `json:"cap_bytes"`
}

// Options narrows the report.
type Options struct {
	Projects []*store.Project
	Since    time.Time
	TopN     int
	Now      time.Time
}

// Build computes the report.
func Build(opts Options) (Report, error) {
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	cfg, _ := store.LoadConfig()
	rep := Report{Projects: len(opts.Projects), Usage: map[string]store.Usage{}}
	if !opts.Since.IsZero() {
		rep.Since = store.Now(opts.Since)
	}
	topN := opts.TopN
	if topN <= 0 {
		topN = 10
	}
	files := map[string]int{}
	allFiles := map[string]bool{}
	days := map[string]*Day{}
	daySessions := map[string]map[string]bool{}
	dayFiles := map[string]map[string]bool{}

	for _, p := range opts.Projects {
		recs, err := p.Sessions()
		if err != nil {
			return rep, err
		}
		for _, rec := range recs {
			if !opts.Since.IsZero() && rec.LastActivity().Before(opts.Since) {
				continue
			}
			rep.Sessions++
			rep.Turns += len(rec.Turns)
			for model, u := range rec.Usage {
				cur := rep.Usage[model]
				cur.Add(u)
				rep.Usage[model] = cur
			}
		}
		events, err := p.Events(store.Query{Since: opts.Since})
		if err != nil {
			return rep, err
		}
		for _, ev := range events {
			switch ev.Kind {
			case store.KindEdit, store.KindBackground:
				rep.Edits++
			case store.KindExternal:
				// Counted, but not as Claude's lines or files.
				rep.External++
				continue
			default:
				continue
			}
			key := p.ID + ":" + ev.Path
			files[key]++
			allFiles[key] = true
			rep.LinesAdded += ev.LinesAdded
			rep.LinesRemoved += ev.LinesRemoved
			date := ev.Time().Local().Format("2006-01-02")
			d, ok := days[date]
			if !ok {
				d = &Day{Date: date}
				days[date] = d
				daySessions[date] = map[string]bool{}
				dayFiles[date] = map[string]bool{}
			}
			d.Edits++
			d.Added += ev.LinesAdded
			d.Removed += ev.LinesRemoved
			daySessions[date][ev.Session] = true
			dayFiles[date][key] = true
		}
	}
	rep.Files = len(allFiles)
	for model, u := range rep.Usage {
		_ = model
		rep.Total.Add(u)
	}
	cost, complete := usage.Cost(rep.Usage, cfg.Prices)
	if cost > 0 {
		rep.Cost = &cost
	}
	rep.CostComplete = complete
	if live, err := sessions.List(sessions.Options{All: true, Live: true, Now: now}); err == nil {
		wanted := map[string]bool{}
		for _, p := range opts.Projects {
			wanted[p.ID] = true
		}
		for _, s := range live {
			if wanted[s.ProjectID] {
				rep.LiveSessions++
			}
		}
	}
	for key, n := range files {
		path := key
		if i := indexByte(key, ':'); i >= 0 {
			path = key[i+1:]
		}
		rep.TopFiles = append(rep.TopFiles, FileCount{Path: path, Edits: n})
	}
	sort.Slice(rep.TopFiles, func(i, j int) bool {
		if rep.TopFiles[i].Edits != rep.TopFiles[j].Edits {
			return rep.TopFiles[i].Edits > rep.TopFiles[j].Edits
		}
		return rep.TopFiles[i].Path < rep.TopFiles[j].Path
	})
	if len(rep.TopFiles) > topN {
		rep.TopFiles = rep.TopFiles[:topN]
	}
	for date, d := range days {
		d.Sessions = len(daySessions[date])
		d.Files = len(dayFiles[date])
		rep.Days = append(rep.Days, *d)
	}
	rep.ClaudeCache = mergeClaudeCache(&rep)
	rep.Storage = storage(opts.Projects, rep, cfg)
	sort.Slice(rep.Days, func(i, j int) bool { return rep.Days[i].Date > rep.Days[j].Date })
	return rep, nil
}

func indexByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

type claudeCache struct {
	DailyActivity []struct {
		Date         string `json:"date"`
		MessageCount int    `json:"messageCount"`
		SessionCount int    `json:"sessionCount"`
		ToolCalls    int    `json:"toolCallCount"`
	} `json:"dailyActivity"`
}

// mergeClaudeCache adds Claude Code's own daily counters to matching days.
// Those numbers cover every project on the machine, so they are a
// cross-check, not a per-project figure.
func mergeClaudeCache(rep *Report) bool {
	data, err := os.ReadFile(filepath.Join(sessions.ClaudeHome(), "stats-cache.json"))
	if err != nil {
		return false
	}
	var cc claudeCache
	if json.Unmarshal(data, &cc) != nil {
		return false
	}
	byDate := map[string]int{}
	for i, d := range rep.Days {
		byDate[d.Date] = i
	}
	for _, d := range cc.DailyActivity {
		i, ok := byDate[d.Date]
		if !ok {
			continue
		}
		rep.Days[i].ClaudeMessages = d.MessageCount
		rep.Days[i].ClaudeSessions = d.SessionCount
		rep.Days[i].ClaudeToolCalls = d.ToolCalls
	}
	return true
}

func storage(projects []*store.Project, rep Report, cfg store.Config) Storage {
	st := Storage{RetentionDays: cfg.RetentionDays, CapBytes: cfg.MaxProjectBytes}
	for _, p := range projects {
		st.Bytes += p.Size()
	}
	edits := rep.Edits + rep.External
	if edits > 0 {
		st.BytesPerEdit = st.Bytes / int64(edits)
	}
	if len(rep.Days) > 0 {
		st.EditsPerDay = edits / len(rep.Days)
	}
	st.Projected = st.BytesPerEdit * int64(st.EditsPerDay) * int64(cfg.RetentionDays)
	return st
}
