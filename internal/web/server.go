// Package web serves the metrics and history page on localhost for
// `pawprints ui`. The page is plain files embedded in the binary. Metrics come
// from the metrics package; every other answer is the same JSON the CLI's
// --json commands print, so the page can do nothing the CLI cannot.
package web

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/elythi0n/pawprints/internal/buildinfo"
	"github.com/elythi0n/pawprints/internal/metrics"
	"github.com/elythi0n/pawprints/internal/sessions"
	"github.com/elythi0n/pawprints/internal/store"
	"github.com/elythi0n/pawprints/internal/usage"
	"github.com/elythi0n/pawprints/internal/watch"
)

//go:embed static
var static embed.FS

// Runner runs one CLI command in JSON mode and returns what it printed and
// its exit code.
type Runner func(args []string) ([]byte, int)

// Server is one `pawprints ui` process.
type Server struct {
	Token string
	Run   Runner
	// Idle is how long the server waits with no page open before it exits;
	// zero keeps it running.
	Idle time.Duration

	tail     *usage.Tail
	mu       sync.Mutex
	clients  map[chan []byte]bool
	lastSeen time.Time
	port     string
}

// LoadToken returns the page's access token, kept in the data directory so
// a bookmark keeps working across restarts.
func LoadToken() (string, error) {
	if _, err := store.EnsureHome(); err != nil {
		return "", err
	}
	path := filepath.Join(store.Home(), "state", "ui-token")
	if data, err := os.ReadFile(path); err == nil {
		if tok := strings.TrimSpace(string(data)); len(tok) >= 32 {
			return tok, nil
		}
	}
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	tok := hex.EncodeToString(buf)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	return tok, os.WriteFile(path, []byte(tok+"\n"), 0o600)
}

// Running reports whether a pawprints ui with this token already answers at
// the address.
func Running(addr, token string) bool {
	client := http.Client{Timeout: 400 * time.Millisecond}
	req, err := http.NewRequest(http.MethodGet, "http://"+addr+"/api/info", nil)
	if err != nil {
		return false
	}
	req.Header.Set("X-Pawprints-Token", token)
	res, err := client.Do(req)
	if err != nil {
		return false
	}
	_ = res.Body.Close()
	return res.StatusCode == http.StatusOK
}

// Listen binds to 127.0.0.1 on the port, falling back to any free port when
// fallback is set and the port is taken.
func Listen(port int, fallback bool) (net.Listener, error) {
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil && fallback {
		ln, err = net.Listen("tcp", "127.0.0.1:0")
	}
	return ln, err
}

// Serve answers on the listener until the context ends or the page has been
// closed for longer than Idle.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	s.tail = usage.NewTail()
	s.clients = map[chan []byte]bool{}
	// The first page load gets a minute more than later reconnects.
	s.lastSeen = time.Now().Add(time.Minute)
	_, s.port, _ = net.SplitHostPort(ln.Addr().String())

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	srv := &http.Server{Handler: s.routes(), ReadHeaderTimeout: 10 * time.Second}
	go s.followChanges(ctx)
	go s.followUsage(ctx)
	if s.Idle > 0 {
		go s.exitWhenIdle(ctx, cancel)
	}
	go func() {
		<-ctx.Done()
		shut, done := context.WithTimeout(context.Background(), 2*time.Second)
		defer done()
		_ = srv.Shutdown(shut)
	}()
	err := srv.Serve(ln)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	files, _ := fs.Sub(static, "static")
	fileServer := http.FileServer(http.FS(files))
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		fileServer.ServeHTTP(w, r)
	}))
	api := func(path string, h func(http.ResponseWriter, *http.Request)) {
		mux.HandleFunc(path, s.guard(h))
	}
	api("/api/info", s.info)
	api("/api/metrics", s.metrics)
	api("/api/accounts", s.accounts)
	api("/api/turns", s.passthrough(func(q map[string]string) []string {
		return []string{"turns", "--session", q["session"]}
	}))
	api("/api/log", s.passthrough(func(q map[string]string) []string {
		args := []string{"log", "--all-events"}
		for _, k := range []string{"session", "turn", "path", "since", "kind"} {
			if q[k] != "" {
				args = append(args, "--"+k, q[k])
			}
		}
		return args
	}))
	api("/api/diff", s.passthrough(func(q map[string]string) []string {
		args := []string{"diff", q["event"]}
		if q["from"] != "" {
			args = append(args, "--from", q["from"])
		} else if q["against"] != "" {
			args = append(args, "--against", q["against"])
		}
		return args
	}))
	api("/api/show", s.passthrough(func(q map[string]string) []string {
		return []string{"show", q["event"]}
	}))
	api("/api/restore", s.action(func(b actionBody) []string {
		args := []string{"restore", b.Event}
		if b.Before {
			args = append(args, "--before")
		}
		return args
	}))
	api("/api/undo", s.action(func(b actionBody) []string {
		args := []string{"undo", "--session", b.Session}
		if b.Turn != "" {
			args = append(args, "--turn", b.Turn)
		}
		return args
	}))
	api("/api/pin", s.action(func(b actionBody) []string {
		return []string{"pin", b.Event, "-m", b.Message}
	}))
	api("/api/unpin", s.action(func(b actionBody) []string {
		return []string{"unpin", b.Event}
	}))
	api("/api/backfill", s.action(func(actionBody) []string {
		return []string{"usage", "backfill"}
	}))
	api("/api/events", s.events)
	return mux
}

// guard admits a request only with the token and a Host header naming this
// server. Without both, any other page open in the browser could call
// restore or undo on localhost.
func (s *Server) guard(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if host != "127.0.0.1:"+s.port && host != "localhost:"+s.port {
			http.Error(w, `{"error":"wrong host"}`, http.StatusForbidden)
			return
		}
		tok := r.Header.Get("X-Pawprints-Token")
		if tok == "" {
			tok = r.URL.Query().Get("t")
		}
		if subtle.ConstantTimeCompare([]byte(tok), []byte(s.Token)) != 1 {
			http.Error(w, `{"error":"missing or wrong token; open the link pawprints ui printed"}`, http.StatusUnauthorized)
			return
		}
		if r.Method == http.MethodPost && !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			http.Error(w, `{"error":"expected JSON"}`, http.StatusUnsupportedMediaType)
			return
		}
		s.touch()
		w.Header().Set("Cache-Control", "no-store")
		h(w, r)
	}
}

func (s *Server) touch() {
	s.mu.Lock()
	s.lastSeen = time.Now()
	s.mu.Unlock()
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func (s *Server) info(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"version": buildinfo.Version, "home": store.Home()})
}

// projectByID finds a project; an empty id means every project.
func projectByID(id string) ([]*store.Project, error) {
	all, err := store.ListProjects()
	if err != nil || id == "" {
		return all, err
	}
	for _, p := range all {
		if p.ID == id {
			return []*store.Project{p}, nil
		}
	}
	return nil, errors.New("no such project")
}

var ranges = map[string]time.Duration{
	"1h": time.Hour, "6h": 6 * time.Hour, "24h": 24 * time.Hour, "7d": 7 * 24 * time.Hour, "30d": 30 * 24 * time.Hour,
}

func (s *Server) metrics(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	span, ok := ranges[q.Get("range")]
	if !ok {
		span = 24 * time.Hour
	}
	projects, err := projectByID(q.Get("project"))
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	now := time.Now()
	opts := metrics.Options{Projects: projects, Since: now.Add(-span), Now: now, Account: q.Get("account"), Session: q.Get("session"), Tail: s.tail}
	// previous asks for the same length of time just before, for comparing.
	if q.Get("previous") == "1" {
		opts.Since, opts.Until, opts.Tail = now.Add(-2*span), now.Add(-span), nil
	}
	rep, err := metrics.Build(opts)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

func (s *Server) accounts(w http.ResponseWriter, _ *http.Request) {
	out := []store.Account{}
	for _, a := range store.Accounts() {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Email < out[j].Email })
	writeJSON(w, http.StatusOK, out)
}

// projectArgs turns a project id into the CLI's --project flag.
func projectArgs(id string) ([]string, error) {
	if id == "" {
		return nil, errors.New("project is required")
	}
	ps, err := projectByID(id)
	if err != nil {
		return nil, err
	}
	return []string{"--project", ps[0].Root}, nil
}

// run runs a CLI command and answers with its JSON: one object, a list for
// listings, or its error with the matching status.
func (s *Server) run(w http.ResponseWriter, project string, args []string) {
	pargs, err := projectArgs(project)
	if err != nil && project != "-" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	out, exit := s.Run(append(append([]string{"--json"}, pargs...), args...))
	var items []json.RawMessage
	for _, line := range bytes.Split(bytes.TrimSpace(out), []byte("\n")) {
		if len(bytes.TrimSpace(line)) > 0 {
			items = append(items, json.RawMessage(line))
		}
	}
	if exit != 0 {
		status := http.StatusBadRequest
		if exit == 3 {
			status = http.StatusConflict
		}
		if len(items) > 0 {
			writeJSON(w, status, items[0])
			return
		}
		writeJSON(w, status, map[string]any{"error": "command failed", "exit": exit})
		return
	}
	if len(args) > 0 && (args[0] == "log" || args[0] == "turns") {
		if items == nil {
			items = []json.RawMessage{}
		}
		writeJSON(w, http.StatusOK, items)
		return
	}
	if len(items) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{})
		return
	}
	writeJSON(w, http.StatusOK, items[0])
}

func (s *Server) passthrough(build func(map[string]string) []string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := map[string]string{}
		for k, v := range r.URL.Query() {
			if len(v) > 0 {
				q[k] = v[0]
			}
		}
		for _, v := range q {
			if strings.HasPrefix(v, "-") {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad argument"})
				return
			}
		}
		s.run(w, q["project"], build(q))
	}
}

type actionBody struct {
	Project string `json:"project"`
	Event   string `json:"event"`
	Session string `json:"session"`
	Turn    string `json:"turn"`
	Before  bool   `json:"before"`
	Message string `json:"message"`
	// Apply runs the change; without it the answer is the dry run's plan.
	Apply bool `json:"apply"`
	// Mode is "three-way" or "force" for a file changed since Claude's edit.
	Mode string `json:"mode"`
}

func (s *Server) action(build func(actionBody) []string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "use POST"})
			return
		}
		var b actionBody
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&b); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request body"})
			return
		}
		for _, v := range []string{b.Event, b.Session, b.Turn} {
			if strings.HasPrefix(v, "-") {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad argument"})
				return
			}
		}
		args := build(b)
		switch args[0] {
		case "restore", "undo":
			if b.Apply {
				args = append(args, "--yes")
			} else {
				args = append(args, "--dry-run")
			}
			switch b.Mode {
			case "three-way":
				args = append(args, "--three-way")
			case "force":
				args = append(args, "--force")
			}
		case "usage":
			s.run(w, "-", args)
			return
		}
		s.run(w, b.Project, args)
	}
}

// events streams changes to the page as server-sent events: "change" when an
// edit or a session changed, "usage" when a running session spent tokens.
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Connection", "keep-alive")
	ch := make(chan []byte, 16)
	s.mu.Lock()
	s.clients[ch] = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.clients, ch)
		s.lastSeen = time.Now()
		s.mu.Unlock()
	}()
	_, _ = w.Write([]byte("event: hello\ndata: {}\n\n"))
	flusher.Flush()
	beat := time.NewTicker(25 * time.Second)
	defer beat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case msg := <-ch:
			_, _ = w.Write(msg)
			flusher.Flush()
		case <-beat.C:
			_, _ = w.Write([]byte(": beat\n\n"))
			flusher.Flush()
		}
	}
}

func (s *Server) broadcast(event string, data any) {
	payload, _ := json.Marshal(data)
	msg := []byte("event: " + event + "\ndata: " + string(payload) + "\n\n")
	s.mu.Lock()
	defer s.mu.Unlock()
	for ch := range s.clients {
		select {
		case ch <- msg:
		default:
			// A slow page catches up on the next message.
		}
	}
}

func (s *Server) clientCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.clients)
}

func (s *Server) followChanges(ctx context.Context) {
	ch := make(chan watch.Change, 64)
	go func() { _ = watch.New(watch.Options{All: true}).Run(ctx, ch) }()
	for change := range ch {
		if change.Kind == watch.KindHeartbeat {
			continue
		}
		msg := map[string]string{"kind": change.Kind, "project": change.Project}
		if change.Session != nil {
			msg["session"] = change.Session.ID
		}
		if change.Event != nil {
			msg["session"] = change.Event.Session
		}
		s.broadcast("change", msg)
	}
}

// followUsage tells the page when a running session's transcript grew, so
// long prompts show up as they run. It costs a stat per running transcript.
func (s *Server) followUsage(ctx context.Context) {
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	size := func(p string) int64 {
		info, err := os.Stat(p)
		if err != nil {
			return 0
		}
		return info.Size()
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		if s.clientCount() == 0 {
			continue
		}
		pins := store.SessionRoots()
		for id, re := range sessions.Running() {
			root, ok := pins[id]
			if !ok {
				root = store.ResolveRoot(re.CWD)
			}
			p, found := store.FindProject(root)
			if !found {
				continue
			}
			rec, ok := p.LoadSession(id)
			if ok && s.tail.Grew(rec, size) {
				s.broadcast("usage", map[string]string{"session": id, "project": p.ID})
			}
		}
	}
}

func (s *Server) exitWhenIdle(ctx context.Context, stop context.CancelFunc) {
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		s.mu.Lock()
		idle := len(s.clients) == 0 && time.Since(s.lastSeen) > s.Idle
		s.mu.Unlock()
		if idle {
			stop()
			return
		}
	}
}
