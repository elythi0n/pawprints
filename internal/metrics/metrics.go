// Package metrics builds `pawprints metrics`: tokens, edits, and limits over
// time, with the breakdowns by project, session, model, account, and file
// that the web view and the TUI chart. It reads the per-minute usage logs
// the hooks write, never the transcripts, except for the minutes a running
// session has not recorded yet, which a Tail supplies.
package metrics

import (
	"path/filepath"
	"sort"
	"time"

	"github.com/elythi0n/pawprints/internal/sessions"
	"github.com/elythi0n/pawprints/internal/store"
	"github.com/elythi0n/pawprints/internal/usage"
)

// Options narrow the report.
type Options struct {
	Projects []*store.Project
	Since    time.Time
	Until    time.Time // zero means now
	Bucket   time.Duration
	Account  string
	Session  string
	Now      time.Time
	// Tail, when set, adds what running sessions have written but not yet
	// recorded.
	Tail *usage.Tail
}

// Tokens is a usage total. Counted is what counts toward usage limits:
// input, cache writes, and output.
type Tokens struct {
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	CacheRead  int64 `json:"cache_read"`
	CacheWrite int64 `json:"cache_write"`
	Thinking   int64 `json:"thinking"`
	Counted    int64 `json:"counted"`
}

func (t *Tokens) add(u store.Usage) {
	t.Input += u.Input
	t.Output += u.Output
	t.CacheRead += u.CacheRead
	t.CacheWrite += u.CacheWrite
	t.Thinking += u.Thinking
	t.Counted += u.Counted()
}

// Point is one bucket of the timeline.
type Point struct {
	At                 string  `json:"at"`
	Counted            int64   `json:"counted"`
	CacheRead          int64   `json:"cache_read"`
	PerMinute          float64 `json:"per_minute"`
	CacheReadPerMinute float64 `json:"cache_read_per_minute"`
	APICost            float64 `json:"api_cost"`
	Edits              int     `json:"edits"`
	LinesAdded         int     `json:"lines_added"`
	LinesRemoved       int     `json:"lines_removed"`
	SessionsActive     int     `json:"sessions_active"`
	// Session is the session that spent the most in this bucket.
	Session string `json:"session,omitempty"`
}

// Spike is a bucket that spent much faster than the range's normal pace.
type Spike struct {
	At          string  `json:"at"`
	Counted     int64   `json:"counted"`
	PerMinute   float64 `json:"per_minute"`
	TimesNormal float64 `json:"times_normal"`
	Session     string  `json:"session"`
	SessionName string  `json:"session_name"`
	Project     string  `json:"project"`
	Turn        string  `json:"turn,omitempty"`
	Prompt      string  `json:"prompt,omitempty"`
}

// ProjectRow is one project's share of the range.
type ProjectRow struct {
	ID           string   `json:"id"`
	Root         string   `json:"root"`
	Name         string   `json:"name"`
	Tokens       Tokens   `json:"tokens"`
	APICost      float64  `json:"api_cost"`
	Edits        int      `json:"edits"`
	LinesAdded   int      `json:"lines_added"`
	LinesRemoved int      `json:"lines_removed"`
	Files        int      `json:"files"`
	Sessions     int      `json:"sessions"`
	Share        float64  `json:"share"`
	Accounts     []string `json:"accounts"`
	Series       []int64  `json:"series"`
}

// SessionRow is one session active in the range.
type SessionRow struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Project      string `json:"project"`
	ProjectID    string `json:"project_id"`
	Account      string `json:"account,omitempty"`
	Model        string `json:"model,omitempty"`
	Status       string `json:"status"`
	Alive        bool   `json:"alive"`
	StartedAt    string `json:"started_at,omitempty"`
	LastActivity string `json:"last_activity,omitempty"`
	LastPrompt   string `json:"last_prompt,omitempty"`
	Turns        int    `json:"turns"`
	Agents       int    `json:"agents"`
	// AgentNames names each subagent by its task, else its type.
	AgentNames    map[string]string `json:"agent_names,omitempty"`
	Tokens        Tokens            `json:"tokens"`
	APICost       float64           `json:"api_cost"`
	Edits         int               `json:"edits"`
	LinesAdded    int               `json:"lines_added"`
	LinesRemoved  int               `json:"lines_removed"`
	PeakPerMinute float64           `json:"peak_per_minute"`
	Series        []int64           `json:"series"`
}

// ModelRow is one model's share.
type ModelRow struct {
	Model   string  `json:"model"`
	Tokens  Tokens  `json:"tokens"`
	APICost float64 `json:"api_cost"`
	Priced  bool    `json:"priced"`
	Share   float64 `json:"share"`
}

// ToolRow counts Claude's edits by the tool that made them.
type ToolRow struct {
	Tool  string `json:"tool"`
	Edits int    `json:"edits"`
}

// FileRow is one of the most changed files.
type FileRow struct {
	Path         string `json:"path"`
	Project      string `json:"project"`
	ProjectID    string `json:"project_id"`
	Edits        int    `json:"edits"`
	LinesAdded   int    `json:"lines_added"`
	LinesRemoved int    `json:"lines_removed"`
	Sessions     int    `json:"sessions"`
	LastAt       string `json:"last_at"`
	LastEvent    string `json:"last_event"`
	// Activity is edits in equal slices of the range, oldest first.
	Activity []int `json:"activity"`
}

// AccountRow is one Claude account's share. Email is local data.
type AccountRow struct {
	ID       string  `json:"id"`
	Email    string  `json:"email,omitempty"`
	Plan     string  `json:"plan,omitempty"`
	Tokens   Tokens  `json:"tokens"`
	APICost  float64 `json:"api_cost"`
	Sessions int     `json:"sessions"`
	Share    float64 `json:"share"`
}

// LimitPoint is one usage-limit reading.
type LimitPoint struct {
	At      string  `json:"at"`
	Percent float64 `json:"percent"`
	Reset   bool    `json:"reset,omitempty"`
}

// LimitLine is one account's readings of one window. Limits belong to the
// account, so they are never added across accounts or narrowed by project.
type LimitLine struct {
	Account  string       `json:"account"`
	Email    string       `json:"email,omitempty"`
	Plan     string       `json:"plan,omitempty"`
	Kind     string       `json:"kind"`
	Latest   float64      `json:"latest"`
	ResetsAt string       `json:"resets_at,omitempty"`
	Points   []LimitPoint `json:"points"`
}

// Totals sums the range.
type Totals struct {
	Sessions      int     `json:"sessions"`
	LiveSessions  int     `json:"live_sessions"`
	Turns         int     `json:"turns"`
	Edits         int     `json:"edits"`
	Files         int     `json:"files"`
	LinesAdded    int     `json:"lines_added"`
	LinesRemoved  int     `json:"lines_removed"`
	Tokens        Tokens  `json:"tokens"`
	APICost       float64 `json:"api_cost"`
	CostComplete  bool    `json:"api_cost_complete"`
	PeakPerMinute float64 `json:"peak_per_minute"`
	PeakAt        string  `json:"peak_at,omitempty"`
	PeakSession   string  `json:"peak_session,omitempty"`
}

// Range is the window the report covers.
type Range struct {
	From          string `json:"from"`
	To            string `json:"to"`
	Bucket        string `json:"bucket"`
	BucketSeconds int    `json:"bucket_seconds"`
}

// Report is `pawprints metrics --json`.
type Report struct {
	V        int          `json:"v"`
	Range    Range        `json:"range"`
	Totals   Totals       `json:"totals"`
	Timeline []Point      `json:"timeline"`
	Spikes   []Spike      `json:"spikes"`
	Projects []ProjectRow `json:"projects"`
	Sessions []SessionRow `json:"sessions"`
	Models   []ModelRow   `json:"models"`
	Tools    []ToolRow    `json:"tools"`
	TopFiles []FileRow    `json:"top_files"`
	Accounts []AccountRow `json:"accounts"`
	Limits   []LimitLine  `json:"limits"`
	// MissingHistory counts sessions with token totals but no per-minute log
	// yet, recorded before metrics existed; `pawprints usage backfill` fills
	// them in.
	MissingHistory int `json:"missing_history"`
}

// sparkPoints is how many points a project's or session's series has.
const sparkPoints = 48

// maxTopFiles is how many of the most changed files the report lists.
const maxTopFiles = 150

// fileSlots is how many slices a file's activity strip has.
const fileSlots = 16

// minSpikePerMinute keeps quiet ranges from reporting noise as spikes.
const minSpikePerMinute = 1000

// AutoBucket picks a bucket so a range draws a few hundred points at most.
func AutoBucket(span time.Duration) time.Duration {
	switch {
	case span <= 2*time.Hour:
		return time.Minute
	case span <= 12*time.Hour:
		return 5 * time.Minute
	case span <= 36*time.Hour:
		return 15 * time.Minute
	case span <= 8*24*time.Hour:
		return time.Hour
	case span <= 45*24*time.Hour:
		return 6 * time.Hour
	}
	return 24 * time.Hour
}

// BucketName is how a bucket size is written in the report.
func BucketName(d time.Duration) string {
	switch {
	case d >= 24*time.Hour && d%(24*time.Hour) == 0:
		return itoa(int(d/(24*time.Hour))) + "d"
	case d >= time.Hour && d%time.Hour == 0:
		return itoa(int(d/time.Hour)) + "h"
	}
	return itoa(int(d/time.Minute)) + "m"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// grid maps times to buckets. Day buckets start at local midnight and step
// by calendar days, so they stay on midnight across daylight saving changes;
// smaller buckets start at whole multiples of their size.
type grid struct {
	starts []time.Time
	size   time.Duration
}

func (g grid) days() int { return int(g.size / (24 * time.Hour)) }

// next is the start of the bucket after the one starting at t.
func (g grid) next(t time.Time) time.Time {
	if d := g.days(); d > 0 {
		return t.AddDate(0, 0, d)
	}
	return t.Add(g.size)
}

func newGrid(since, until time.Time, size time.Duration) grid {
	g := grid{size: size}
	var start time.Time
	if g.days() > 0 {
		y, m, d := since.Local().Date()
		start = time.Date(y, m, d, 0, 0, 0, 0, time.Local)
	} else {
		start = since.Truncate(size)
	}
	for t := start; t.Before(until); t = g.next(t) {
		g.starts = append(g.starts, t)
	}
	if len(g.starts) == 0 {
		g.starts = []time.Time{start}
	}
	return g
}

func (g grid) index(t time.Time) int {
	if t.Before(g.starts[0]) {
		return -1
	}
	i := sort.Search(len(g.starts), func(i int) bool { return g.starts[i].After(t) }) - 1
	if i == len(g.starts)-1 && !t.Before(g.next(g.starts[i])) {
		return -1
	}
	return i
}

type sessionAgg struct {
	row      *SessionRow
	rec      store.SessionRecord
	byModel  map[string]int64
	series   []int64
	inRange  bool
	accounts map[string]bool
}

type projectAgg struct {
	row      *ProjectRow
	files    map[string]bool
	sessions map[string]bool
	accounts map[string]bool
	series   []int64
}

// Build computes the report.
func Build(opts Options) (Report, error) {
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	until := opts.Until
	if until.IsZero() || until.After(now) {
		until = now
	}
	since := opts.Since
	if since.IsZero() || !since.Before(until) {
		since = until.Add(-24 * time.Hour)
	}
	size := opts.Bucket
	if size <= 0 {
		size = AutoBucket(until.Sub(since))
	}
	cfg, _ := store.LoadConfig()
	g := newGrid(since, until, size)
	minutes := size.Minutes()
	rep := Report{
		V:        1,
		Range:    Range{From: store.Now(g.starts[0]), To: store.Now(until), Bucket: BucketName(size), BucketSeconds: int(size.Seconds())},
		Timeline: make([]Point, len(g.starts)),
		Spikes:   []Spike{},
		Tools:    []ToolRow{},
		TopFiles: []FileRow{},
		Limits:   []LimitLine{},
	}
	for i, t := range g.starts {
		rep.Timeline[i].At = store.Now(t)
	}
	from := g.starts[0]
	running := sessions.Running()
	pins := store.SessionRoots()
	known := store.Accounts()

	bucketSessions := make([]map[string]int64, len(g.starts))
	bucketActive := make([]map[string]bool, len(g.starts))
	for i := range g.starts {
		bucketSessions[i] = map[string]int64{}
		bucketActive[i] = map[string]bool{}
	}
	allSessions := map[string]*sessionAgg{}
	usageOwner := map[string]string{}
	tailed := map[string]bool{}
	var sessionOrder []string
	projects := map[string]*projectAgg{}
	models := map[string]*ModelRow{}
	accounts := map[string]*AccountRow{}
	accountSessions := map[string]map[string]bool{}
	tools := map[string]int{}
	type fileAgg struct {
		row      *FileRow
		sessions map[string]bool
		lastAt   time.Time
	}
	files := map[string]*fileAgg{}
	costComplete := true

	accountRow := func(id string) *AccountRow {
		a, ok := accounts[id]
		if !ok {
			k := known[id]
			a = &AccountRow{ID: id, Email: k.Email, Plan: k.Plan}
			accounts[id] = a
			accountSessions[id] = map[string]bool{}
		}
		return a
	}

	for _, p := range opts.Projects {
		recs, err := p.Sessions()
		if err != nil {
			return rep, err
		}
		byID := map[string]store.SessionRecord{}
		for _, r := range recs {
			byID[r.Session] = r
		}
		pa := &projectAgg{
			row:      &ProjectRow{ID: p.ID, Root: p.Root, Name: filepath.Base(p.Root), Accounts: []string{}},
			files:    map[string]bool{},
			sessions: map[string]bool{},
			accounts: map[string]bool{},
			series:   make([]int64, len(g.starts)),
		}
		// A session that worked in several projects is one row, kept under
		// the project it is pinned to.
		session := func(id string) *sessionAgg {
			owner := pins[id] == p.Root
			if s, ok := allSessions[id]; ok {
				if owner && s.row.ProjectID != p.ID {
					if rec, ok := byID[id]; ok {
						s.rec = rec
					}
					s.row.Project, s.row.ProjectID = p.Root, p.ID
				}
				return s
			}
			rec, ok := byID[id]
			if !ok {
				rec = store.SessionRecord{Session: id}
			}
			entry, live := running[id]
			var ep *sessions.RegistryEntry
			if live {
				ep = &entry
			}
			status := sessions.StatusEnded
			switch {
			case live:
				status = sessions.LiveStatus(entry)
			case rec.EndedAt == "" && now.Sub(rec.LastActivity()) < 24*time.Hour:
				status = sessions.StatusIdle
			}
			row := &SessionRow{
				ID: id, Name: sessions.RecordName(rec, ep), Project: p.Root, ProjectID: p.ID,
				Account: rec.Account, Status: status, Alive: live,
				StartedAt: rec.StartedAt, LastPrompt: rec.LastPrompt, Turns: len(rec.Turns), Agents: len(rec.Agents),
			}
			if t := rec.LastActivity(); !t.IsZero() {
				row.LastActivity = store.Now(t)
			}
			for _, a := range rec.Agents {
				if row.AgentNames == nil {
					row.AgentNames = map[string]string{}
				}
				row.AgentNames[a.ID] = a.Name()
			}
			s := &sessionAgg{row: row, rec: rec, byModel: map[string]int64{}, series: make([]int64, len(g.starts)), accounts: map[string]bool{}}
			allSessions[id] = s
			sessionOrder = append(sessionOrder, id)
			return s
		}

		logs, err := p.Usage(from)
		if err != nil {
			return rep, err
		}
		for _, r := range recs {
			if len(r.Usage) > 0 && !p.HasUsageLog(r.Session) {
				rep.MissingHistory++
			}
			if opts.Tail != nil {
				// Only the record a session's tokens belong to is followed;
				// another copy's offsets would restart the tail every call.
				if root, pinned := pins[r.Session]; pinned && root != p.Root {
					continue
				}
				if tailed[r.Session] {
					continue
				}
				if _, live := running[r.Session]; live {
					tailed[r.Session] = true
					logs[r.Session] = append(logs[r.Session], opts.Tail.Pending(r)...)
				}
			}
		}
		for id, buckets := range logs {
			if opts.Session != "" && id != opts.Session {
				continue
			}
			// Older versions could leave one session's record, and so its
			// usage, in two projects. Its tokens count once: in the project it
			// is pinned to, else the first that has them.
			if root, pinned := pins[id]; pinned && root != p.Root {
				if _, owned := store.FindProject(root); owned {
					continue
				}
			}
			if seen, ok := usageOwner[id]; ok && seen != p.ID {
				continue
			}
			usageOwner[id] = p.ID
			for _, b := range buckets {
				t := b.At()
				i := g.index(t)
				if i < 0 || !t.Before(until) {
					continue
				}
				acct := b.Account
				if acct == "" {
					if rec, ok := byID[id]; ok {
						acct = rec.AccountAt(t)
					}
				}
				if opts.Account != "" && acct != opts.Account {
					continue
				}
				counted := b.Counted()
				cost, priced := usage.APICost(b.Model, b.Usage, cfg.Prices)
				if !priced && b.Model != "<synthetic>" && counted > 0 {
					costComplete = false
				}
				pt := &rep.Timeline[i]
				pt.Counted += counted
				pt.CacheRead += b.CacheRead
				pt.APICost += cost
				bucketSessions[i][id] += counted
				bucketActive[i][id] = true

				s := session(id)
				s.inRange = true
				s.row.Tokens.add(b.Usage)
				s.row.APICost += cost
				s.byModel[b.Model] += counted
				s.series[i] += counted
				if acct != "" {
					s.accounts[acct] = true
				}

				pa.row.Tokens.add(b.Usage)
				pa.row.APICost += cost
				pa.series[i] += counted
				pa.sessions[id] = true
				if acct != "" {
					pa.accounts[acct] = true
				}

				m, ok := models[b.Model]
				if !ok {
					m = &ModelRow{Model: b.Model, Priced: priced}
					models[b.Model] = m
				}
				m.Tokens.add(b.Usage)
				m.APICost += cost

				if acct != "" {
					a := accountRow(acct)
					a.Tokens.add(b.Usage)
					a.APICost += cost
					accountSessions[acct][id] = true
				}
				rep.Totals.Tokens.add(b.Usage)
				rep.Totals.APICost += cost
			}
		}

		events, err := p.Events(store.Query{Since: from, Session: opts.Session})
		if err != nil {
			return rep, err
		}
		for _, ev := range events {
			if ev.Kind != store.KindEdit && ev.Kind != store.KindBackground {
				continue
			}
			t := ev.Time()
			i := g.index(t)
			if i < 0 || !t.Before(until) {
				continue
			}
			rec := byID[ev.Session]
			acct := rec.AccountAt(t)
			if opts.Account != "" && acct != opts.Account {
				continue
			}
			pt := &rep.Timeline[i]
			pt.Edits++
			pt.LinesAdded += ev.LinesAdded
			pt.LinesRemoved += ev.LinesRemoved
			bucketActive[i][ev.Session] = true

			s := session(ev.Session)
			s.inRange = true
			s.row.Edits++
			s.row.LinesAdded += ev.LinesAdded
			s.row.LinesRemoved += ev.LinesRemoved
			if acct != "" {
				s.accounts[acct] = true
			}

			pa.row.Edits++
			pa.row.LinesAdded += ev.LinesAdded
			pa.row.LinesRemoved += ev.LinesRemoved
			pa.files[ev.Path] = true
			pa.sessions[ev.Session] = true
			if acct != "" {
				pa.accounts[acct] = true
				accountRow(acct)
				accountSessions[acct][ev.Session] = true
			}
			tools[store.ToolName(ev.Tool)]++

			key := p.ID + ":" + ev.Path
			f, ok := files[key]
			if !ok {
				f = &fileAgg{row: &FileRow{Path: ev.Path, Project: p.Root, ProjectID: p.ID, Activity: make([]int, fileSlots)}, sessions: map[string]bool{}}
				files[key] = f
			}
			f.row.Edits++
			f.row.LinesAdded += ev.LinesAdded
			f.row.LinesRemoved += ev.LinesRemoved
			f.sessions[ev.Session] = true
			if t.After(f.lastAt) {
				f.lastAt = t
				f.row.LastAt = ev.TS
				f.row.LastEvent = ev.ID
			}
			slot := int(float64(fileSlots) * float64(t.Sub(from)) / float64(until.Sub(from)))
			if slot >= fileSlots {
				slot = fileSlots - 1
			}
			if slot >= 0 {
				f.row.Activity[slot]++
			}
			rep.Totals.Edits++
			rep.Totals.LinesAdded += ev.LinesAdded
			rep.Totals.LinesRemoved += ev.LinesRemoved
		}

		// Sessions running now belong in the report even before they spend.
		for id, re := range running {
			if _, mine := byID[id]; !mine {
				root, pinned := pins[id]
				if !pinned {
					root = store.ResolveRoot(re.CWD)
				}
				if root != p.Root {
					continue
				}
			}
			if opts.Session != "" && id != opts.Session {
				continue
			}
			rec := byID[id]
			if opts.Account != "" && rec.Account != opts.Account {
				continue
			}
			session(id).inRange = true
		}

		pa.row.Files = len(pa.files)
		pa.row.Sessions = len(pa.sessions)
		rep.Totals.Files += len(pa.files)
		for a := range pa.accounts {
			pa.row.Accounts = append(pa.row.Accounts, a)
		}
		sort.Strings(pa.row.Accounts)
		if pa.row.Tokens.Counted > 0 || pa.row.Edits > 0 || len(pa.sessions) > 0 {
			pa.row.Series = downsample(pa.series, sparkPoints)
			projects[p.ID] = pa
		}
	}

	// Timeline rates, the peak, and who spent most in each bucket.
	for i := range rep.Timeline {
		pt := &rep.Timeline[i]
		pt.PerMinute = float64(pt.Counted) / minutes
		pt.CacheReadPerMinute = float64(pt.CacheRead) / minutes
		pt.SessionsActive = len(bucketActive[i])
		best, bestKey := int64(0), ""
		for k, v := range bucketSessions[i] {
			if v > best || (v == best && k < bestKey) {
				best, bestKey = v, k
			}
		}
		if bestKey != "" {
			pt.Session = allSessions[bestKey].row.ID
		}
		if pt.PerMinute > rep.Totals.PeakPerMinute {
			rep.Totals.PeakPerMinute = pt.PerMinute
			rep.Totals.PeakAt = pt.At
			if bestKey != "" {
				rep.Totals.PeakSession = allSessions[bestKey].row.Name
			}
		}
	}
	rep.Totals.CostComplete = costComplete
	rep.Spikes = spikes(rep.Timeline, g, bucketSessions, allSessions, spikeFactor(cfg))

	for _, key := range sessionOrder {
		s := allSessions[key]
		if !s.inRange {
			continue
		}
		var bestModel string
		var best int64
		for m, v := range s.byModel {
			if v > best {
				best, bestModel = v, m
			}
		}
		s.row.Model = bestModel
		for _, v := range s.series {
			if pm := float64(v) / minutes; pm > s.row.PeakPerMinute {
				s.row.PeakPerMinute = pm
			}
		}
		if s.row.Account == "" {
			for a := range s.accounts {
				s.row.Account = a
			}
		}
		s.row.Series = downsample(s.series, sparkPoints)
		rep.Sessions = append(rep.Sessions, *s.row)
		rep.Totals.Turns += turnsIn(s.rec, from, until)
		if s.row.Alive {
			rep.Totals.LiveSessions++
		}
	}
	rep.Totals.Sessions = len(rep.Sessions)
	sort.SliceStable(rep.Sessions, func(i, j int) bool {
		a, b := rep.Sessions[i], rep.Sessions[j]
		if a.Alive != b.Alive {
			return a.Alive
		}
		return a.LastActivity > b.LastActivity
	})

	total := float64(rep.Totals.Tokens.Counted)
	share := func(n int64) float64 {
		if total == 0 {
			return 0
		}
		return float64(n) / total
	}
	for _, pa := range projects {
		pa.row.Share = share(pa.row.Tokens.Counted)
		rep.Projects = append(rep.Projects, *pa.row)
	}
	sort.Slice(rep.Projects, func(i, j int) bool {
		if rep.Projects[i].Tokens.Counted != rep.Projects[j].Tokens.Counted {
			return rep.Projects[i].Tokens.Counted > rep.Projects[j].Tokens.Counted
		}
		return rep.Projects[i].Edits > rep.Projects[j].Edits
	})
	for _, m := range models {
		m.Share = share(m.Tokens.Counted)
		rep.Models = append(rep.Models, *m)
	}
	sort.Slice(rep.Models, func(i, j int) bool { return rep.Models[i].Tokens.Counted > rep.Models[j].Tokens.Counted })
	for id, a := range accounts {
		a.Share = share(a.Tokens.Counted)
		a.Sessions = len(accountSessions[id])
		rep.Accounts = append(rep.Accounts, *a)
	}
	sort.Slice(rep.Accounts, func(i, j int) bool { return rep.Accounts[i].Tokens.Counted > rep.Accounts[j].Tokens.Counted })
	for tool, n := range tools {
		rep.Tools = append(rep.Tools, ToolRow{Tool: tool, Edits: n})
	}
	sort.Slice(rep.Tools, func(i, j int) bool { return rep.Tools[i].Edits > rep.Tools[j].Edits })
	for _, f := range files {
		f.row.Sessions = len(f.sessions)
		rep.TopFiles = append(rep.TopFiles, *f.row)
	}
	sort.Slice(rep.TopFiles, func(i, j int) bool {
		a, b := rep.TopFiles[i], rep.TopFiles[j]
		if a.Edits != b.Edits {
			return a.Edits > b.Edits
		}
		return a.LastAt > b.LastAt
	})
	if len(rep.TopFiles) > maxTopFiles {
		rep.TopFiles = rep.TopFiles[:maxTopFiles]
	}
	rep.Limits = limits(from, until, opts.Account, known)
	if rep.Projects == nil {
		rep.Projects = []ProjectRow{}
	}
	if rep.Sessions == nil {
		rep.Sessions = []SessionRow{}
	}
	if rep.Models == nil {
		rep.Models = []ModelRow{}
	}
	if rep.Accounts == nil {
		rep.Accounts = []AccountRow{}
	}
	return rep, nil
}

func spikeFactor(cfg store.Config) float64 {
	if cfg.SpikeFactor > 1 {
		return cfg.SpikeFactor
	}
	return 3
}

// spikes marks buckets that spent faster than factor times the median pace
// of the buckets that spent anything, naming the session and prompt behind it.
func spikes(points []Point, g grid, bySession []map[string]int64, all map[string]*sessionAgg, factor float64) []Spike {
	var active []float64
	for _, p := range points {
		if p.Counted > 0 {
			active = append(active, p.PerMinute)
		}
	}
	out := []Spike{}
	if len(active) < 4 {
		return out
	}
	sort.Float64s(active)
	median := active[len(active)/2]
	if median <= 0 {
		return out
	}
	for i, p := range points {
		if p.PerMinute < factor*median || p.PerMinute < minSpikePerMinute {
			continue
		}
		sp := Spike{At: p.At, Counted: p.Counted, PerMinute: p.PerMinute, TimesNormal: p.PerMinute / median}
		best, bestKey := int64(0), ""
		for k, v := range bySession[i] {
			if v > best || (v == best && k < bestKey) {
				best, bestKey = v, k
			}
		}
		if s, ok := all[bestKey]; ok {
			sp.Session, sp.SessionName, sp.Project = s.row.ID, s.row.Name, s.row.Project
			mid := g.starts[i].Add(g.size / 2)
			if id := s.rec.TurnAt(store.Now(mid)); id != "" {
				sp.Turn = id
				for _, t := range s.rec.Turns {
					if t.ID == id {
						sp.Prompt = t.Text
					}
				}
			}
		}
		out = append(out, sp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TimesNormal > out[j].TimesNormal })
	if len(out) > 10 {
		out = out[:10]
	}
	return out
}

func turnsIn(rec store.SessionRecord, from, until time.Time) int {
	n := 0
	for _, t := range rec.Turns {
		at, err := time.Parse(time.RFC3339Nano, t.At)
		if err == nil && !at.Before(from) && at.Before(until) {
			n++
		}
	}
	return n
}

// downsample averages a series into at most n points. Points fall into
// groups of unequal size, so averaging keeps steady use flat.
func downsample(series []int64, n int) []int64 {
	if len(series) <= n {
		out := make([]int64, len(series))
		copy(out, series)
		return out
	}
	sums := make([]int64, n)
	counts := make([]int64, n)
	for i, v := range series {
		sums[i*n/len(series)] += v
		counts[i*n/len(series)]++
	}
	for i := range sums {
		sums[i] /= counts[i]
	}
	return sums
}

// limits reads the usage-limit readings of the range, one line per account
// and window. A drop of more than a few points means the window reset.
func limits(from, until time.Time, account string, known map[string]store.Account) []LimitLine {
	readings, err := store.Limits(from)
	if err != nil {
		return []LimitLine{}
	}
	byKey := map[string]*LimitLine{}
	var order []string
	for _, r := range readings {
		if account != "" && r.Account != account {
			continue
		}
		if r.Time().After(until) {
			continue
		}
		key := r.Account + "\x00" + r.Kind
		line, ok := byKey[key]
		if !ok {
			k := known[r.Account]
			line = &LimitLine{Account: r.Account, Email: k.Email, Plan: k.Plan, Kind: r.Kind, Points: []LimitPoint{}}
			byKey[key] = line
			order = append(order, key)
		}
		at := r.Time()
		if at.Before(from) {
			at = from
		}
		pt := LimitPoint{At: store.Now(at), Percent: r.Percent}
		if n := len(line.Points); n > 0 {
			if line.Points[n-1].Percent-r.Percent > 5 {
				pt.Reset = true
			}
			if line.Points[n-1].Percent == r.Percent && !pt.Reset {
				line.Latest = r.Percent
				line.ResetsAt = r.ResetsAt
				continue
			}
		}
		line.Points = append(line.Points, pt)
		line.Latest = r.Percent
		line.ResetsAt = r.ResetsAt
	}
	out := []LimitLine{}
	for _, k := range order {
		out = append(out, *byKey[k])
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind == "five_hour"
		}
		return out[i].Latest > out[j].Latest
	})
	return out
}
