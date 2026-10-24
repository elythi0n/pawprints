package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elythi0n/pawprints/internal/store"
	"github.com/elythi0n/pawprints/internal/usage"
)

type call struct{ args []string }

func newServer(t *testing.T, answer func([]string) ([]byte, int)) (*Server, *[]call, *store.Project) {
	t.Helper()
	t.Setenv("PAWPRINTS_HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := filepath.Join(t.TempDir(), "app")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	p, err := store.OpenProject(root)
	if err != nil {
		t.Fatal(err)
	}
	var calls []call
	s := &Server{Token: strings.Repeat("a", 48), Run: func(args []string) ([]byte, int) {
		calls = append(calls, call{args})
		return answer(args)
	}}
	s.tail = usage.NewTail()
	s.clients = map[chan []byte]bool{}
	s.port = "7878"
	return s, &calls, p
}

func do(t *testing.T, s *Server, method, path, host, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	}
	r.Host = host
	if token != "" {
		r.Header.Set("X-Pawprints-Token", token)
	}
	w := httptest.NewRecorder()
	s.routes().ServeHTTP(w, r)
	return w
}

func TestGuard(t *testing.T) {
	s, _, _ := newServer(t, func([]string) ([]byte, int) { return nil, 0 })
	tok := s.Token
	if w := do(t, s, "GET", "/api/info", "127.0.0.1:7878", "", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("no token: %d", w.Code)
	}
	if w := do(t, s, "GET", "/api/info", "127.0.0.1:7878", "wrong", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token: %d", w.Code)
	}
	// A page on another site that rebinds a name to 127.0.0.1 sends its own Host.
	if w := do(t, s, "GET", "/api/info", "evil.example:7878", tok, ""); w.Code != http.StatusForbidden {
		t.Fatalf("wrong host: %d", w.Code)
	}
	if w := do(t, s, "GET", "/api/info", "localhost:7878", tok, ""); w.Code != http.StatusOK {
		t.Fatalf("localhost: %d", w.Code)
	}
	if w := do(t, s, "GET", "/api/info?t="+tok, "127.0.0.1:7878", "", ""); w.Code != http.StatusOK {
		t.Fatalf("token in the query, as EventSource sends it: %d", w.Code)
	}
	r := httptest.NewRequest("POST", "/api/restore", strings.NewReader("event=x"))
	r.Host = "127.0.0.1:7878"
	r.Header.Set("X-Pawprints-Token", tok)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	s.routes().ServeHTTP(w, r)
	if w.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("a form post, which any site can send, is refused: %d", w.Code)
	}
	if w := do(t, s, "GET", "/", "127.0.0.1:7878", "", ""); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Pawprints") {
		t.Fatalf("the page itself loads: %d", w.Code)
	}
}

func TestActionsDryRunThenApply(t *testing.T) {
	s, calls, p := newServer(t, func(args []string) ([]byte, int) {
		return []byte(`{"kind":"restore","applied":false,"changes":[]}` + "\n"), 0
	})
	body := `{"project":"` + p.ID + `","event":"01ABC","before":true}`
	if w := do(t, s, "POST", "/api/restore", "127.0.0.1:7878", s.Token, body); w.Code != http.StatusOK {
		t.Fatalf("dry run: %d %s", w.Code, w.Body)
	}
	apply := `{"project":"` + p.ID + `","event":"01ABC","before":true,"apply":true,"mode":"three-way"}`
	if w := do(t, s, "POST", "/api/restore", "127.0.0.1:7878", s.Token, apply); w.Code != http.StatusOK {
		t.Fatalf("apply: %d", w.Code)
	}
	got := []string{strings.Join((*calls)[0].args, " "), strings.Join((*calls)[1].args, " ")}
	want := []string{
		"--json --project " + p.Root + " restore 01ABC --before --dry-run",
		"--json --project " + p.Root + " restore 01ABC --before --yes --three-way",
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("call %d:\n got %s\nwant %s", i, got[i], want[i])
		}
	}
	if w := do(t, s, "POST", "/api/restore", "127.0.0.1:7878", s.Token, `{"project":"`+p.ID+`","event":"--force"}`); w.Code != http.StatusBadRequest {
		t.Fatalf("a flag passed as an event is refused: %d", w.Code)
	}
}

func TestCommandErrorsKeepTheirMeaning(t *testing.T) {
	s, _, p := newServer(t, func(args []string) ([]byte, int) {
		return []byte(`{"error":"a.go changed since the last recorded edit","exit":3}` + "\n"), 3
	})
	w := do(t, s, "POST", "/api/undo", "127.0.0.1:7878", s.Token, `{"project":"`+p.ID+`","session":"s","turn":"T"}`)
	if w.Code != http.StatusConflict {
		t.Fatalf("exit 3 is a conflict the page offers to merge: %d", w.Code)
	}
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if !strings.Contains(body["error"].(string), "changed since") {
		t.Fatalf("the CLI's message reaches the page: %v", body)
	}
}

func TestListingsAreArrays(t *testing.T) {
	s, _, p := newServer(t, func(args []string) ([]byte, int) {
		return []byte("{\"id\":\"a\"}\n{\"id\":\"b\"}\n"), 0
	})
	w := do(t, s, "GET", "/api/log?project="+p.ID+"&session=s", "127.0.0.1:7878", s.Token, "")
	var rows []map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil || len(rows) != 2 {
		t.Fatalf("log as a list: %v %s", err, w.Body)
	}
	empty, _, p2 := newServer(t, func([]string) ([]byte, int) { return nil, 0 })
	w = do(t, empty, "GET", "/api/turns?project="+p2.ID+"&session=s", "127.0.0.1:7878", empty.Token, "")
	if strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatalf("no rows is an empty list: %s", w.Body)
	}
}

func TestMetricsEndpoint(t *testing.T) {
	s, _, _ := newServer(t, func([]string) ([]byte, int) { return nil, 0 })
	w := do(t, s, "GET", "/api/metrics?range=7d", "127.0.0.1:7878", s.Token, "")
	var rep struct {
		Range struct {
			Bucket string `json:"bucket"`
		} `json:"range"`
		Timeline []any `json:"timeline"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &rep); err != nil || rep.Range.Bucket != "1h" || len(rep.Timeline) < 168 {
		t.Fatalf("7 days per hour: %v %+v", err, rep.Range)
	}
	if w := do(t, s, "GET", "/api/metrics?project=nope", "127.0.0.1:7878", s.Token, ""); w.Code != http.StatusNotFound {
		t.Fatalf("unknown project: %d", w.Code)
	}
}

func TestTokenIsKept(t *testing.T) {
	t.Setenv("PAWPRINTS_HOME", t.TempDir())
	a, err := LoadToken()
	if err != nil || len(a) < 32 {
		t.Fatalf("token %q %v", a, err)
	}
	b, _ := LoadToken()
	if a != b {
		t.Fatal("the same token across starts, so bookmarks keep working")
	}
	info, _ := os.Stat(filepath.Join(store.Home(), "state", "ui-token"))
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("only the owner reads the token: %v", info.Mode())
	}
}

func TestMetricsPreviousPeriod(t *testing.T) {
	s, _, _ := newServer(t, func([]string) ([]byte, int) { return nil, 0 })
	w := do(t, s, "GET", "/api/metrics?range=24h&previous=1", "127.0.0.1:7878", s.Token, "")
	var rep struct {
		Range struct {
			From string `json:"from"`
			To   string `json:"to"`
		} `json:"range"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &rep); err != nil {
		t.Fatal(err)
	}
	from, _ := time.Parse(time.RFC3339Nano, rep.Range.From)
	to, _ := time.Parse(time.RFC3339Nano, rep.Range.To)
	if d := time.Since(to); d < 23*time.Hour || d > 25*time.Hour {
		t.Fatalf("the previous day ends a day ago: %v", d)
	}
	if d := to.Sub(from); d < 23*time.Hour || d > 25*time.Hour+15*time.Minute {
		t.Fatalf("and spans a day: %v", d)
	}
}
