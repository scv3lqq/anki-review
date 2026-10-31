package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/scv3lqq/anki-review/internal/config"
	"github.com/scv3lqq/anki-review/internal/testutil/fakeanki"
	"github.com/scv3lqq/anki-review/internal/testutil/fakeclaude"
	"github.com/scv3lqq/anki-review/internal/testutil/fakecodex"
)

func TestMain(m *testing.M) {
	fakeclaude.Main()
	fakecodex.Main()
	os.Exit(m.Run())
}

const (
	testUser     = "owner"
	testPassword = "a long password"
	testToken    = "sk-ant-oat01-owner"
	testKey      = "anki-key-1"
)

type client struct {
	t    *testing.T
	base string
	http *http.Client
}

// call sends a JSON request and returns the status and the body.
func (c client) call(method, path string, body any) (int, []byte) {
	c.t.Helper()
	var r io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			c.t.Fatal(err)
		}
		r = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, c.base+path, r)
	if err != nil {
		c.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		c.t.Fatal(err)
	}
	return resp.StatusCode, raw
}

func (c client) get(path string, out any) int {
	c.t.Helper()
	status, raw := c.call(http.MethodGet, path, nil)
	if out != nil && status == http.StatusOK {
		if err := json.Unmarshal(raw, out); err != nil {
			c.t.Fatal(err)
		}
	}
	return status
}

func (c client) post(path string, body any) {
	c.t.Helper()
	if status, raw := c.call(http.MethodPost, path, body); status != http.StatusOK {
		c.t.Fatalf("POST %s: %d %s", path, status, raw)
	}
}

// poll GETs path until it answers 200.
func (c client) poll(path string, out any) {
	c.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for c.get(path, out) != http.StatusOK {
		if time.Now().After(deadline) {
			c.t.Fatalf("GET %s: no 200 in time", path)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// serve starts a server on cfg and stops it when the test ends, or when the returned stop runs.
func serve(t *testing.T, cfg config.Config) (client, func()) {
	t.Helper()
	a, err := New(cfg, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	var lc net.ListenConfig
	ln, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- a.Serve(ctx, ln) }()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			if err := <-served; err != nil {
				t.Errorf("Serve() = %v", err)
			}
		})
	}
	t.Cleanup(stop)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return client{t: t, base: "http://" + ln.Addr().String(), http: &http.Client{Jar: jar}}, stop
}

func testConfig(anki *fakeanki.Anki, bin, dataDir string) config.Config {
	return config.Config{
		DataDir:        dataDir,
		KnowledgeDecks: []string{"Tech"},
		AnkiURL:        anki.URL(),
		AnkiSync:       true,
		ClaudeBin:      bin,
		GradeTimeout:   10 * time.Second,
	}
}

// setUp sets the owner up through the setup route, which checks both tokens, and checks the session.
func setUp(t *testing.T, c client) {
	t.Helper()
	if st := c.get("/api/session", nil); st != http.StatusOK {
		t.Fatalf("GET /api/session = %d", st)
	}
	var session struct {
		State    string `json:"state"`
		Unlocked bool   `json:"unlocked"`
	}
	c.get("/api/session", &session)
	if session.State != "setup" || session.Unlocked {
		t.Fatalf("before setup: %+v, want setup and locked", session)
	}
	if status, _ := c.call(http.MethodGet, "/api/cards", nil); status != http.StatusUnauthorized {
		t.Fatalf("GET /api/cards before setup = %d, want 401", status)
	}
	c.post("/api/setup", map[string]any{"user": testUser, "password": testPassword, "claude_token": testToken, "anki_key": testKey})
	c.get("/api/session", &session)
	if session.State != "ready" || !session.Unlocked {
		t.Fatalf("after setup: %+v, want ready and unlocked", session)
	}
}

// One pass through the real HTTP server, use case and adapters, with Anki and claude faked at the
// process boundary.
func TestReviewPass(t *testing.T) {
	anki := fakeanki.New(t,
		fakeanki.Card{ID: 1, Deck: "Tech::Go", Question: "What does <code>defer</code> do?", Answer: "Runs the call when the function returns", Reps: 2, Mod: 10},
		fakeanki.Card{ID: 2, Deck: "Tech::Go", Question: "Zero value of a map?", Answer: "nil", New: true},
		fakeanki.Card{ID: 3, Deck: "Tech::Go", Question: "Is a nil map writable?", Answer: "No, it panics", Reps: 1, Mod: 20},
		fakeanki.Card{ID: 4, Deck: "English", Question: "arrange (verb)", Answer: "to put in order"},
	)
	anki.RequireKey(testKey)
	bin, calls := fakeclaude.Use(t, fakeclaude.Good)
	fakeclaude.RequireToken(t, testToken)
	dataDir := filepath.Join(t.TempDir(), "data")
	c, _ := serve(t, testConfig(anki, bin, dataDir))
	setUp(t, c)

	var cards struct {
		Stage   string `json:"stage"`
		Session string `json:"session"`
		Kind    string `json:"kind"`
		Cards   []struct {
			N        int    `json:"n"`
			Question string `json:"question"`
		} `json:"cards"`
	}
	c.get("/api/cards", &cards)
	if cards.Stage != "idle" {
		t.Fatalf("stage before start = %q", cards.Stage)
	}

	var decks struct {
		Decks []struct {
			Name string `json:"name"`
			Kind string `json:"kind"`
			Due  int    `json:"due"`
		} `json:"decks"`
	}
	c.get("/api/decks", &decks)
	if len(decks.Decks) != 3 || decks.Decks[1].Name != "Tech" || decks.Decks[1].Due != 3 || decks.Decks[1].Kind != "knowledge" {
		t.Fatalf("decks = %+v", decks.Decks)
	}

	c.post("/api/start", map[string]any{"deck": "Tech", "count": 2})
	_, raw := c.call(http.MethodGet, "/api/cards", nil)
	if strings.Contains(string(raw), "Runs the call") || strings.Contains(string(raw), `"nil"`) {
		t.Fatalf("answer keys reached the page before grading: %s", raw)
	}
	if err := json.Unmarshal(raw, &cards); err != nil {
		t.Fatal(err)
	}
	if cards.Stage != "input" || cards.Kind != "knowledge" || len(cards.Cards) != 2 || cards.Cards[0].Question != "What does `defer` do?" {
		t.Fatalf("cards = %+v", cards)
	}

	c.post("/api/answers", map[string]any{"session": cards.Session, "answers": []map[string]any{{"n": 1, "answer": "runs at return"}, {"n": 2, "answer": ""}}})
	var results struct {
		Items []struct {
			N       int    `json:"n"`
			Grade   string `json:"grade"`
			Ref     string `json:"ref"`
			Skipped bool   `json:"skipped"`
		} `json:"items"`
	}
	c.poll("/api/results", &results)
	if len(results.Items) != 2 || results.Items[0].Grade != "Good" || results.Items[0].Ref != "ref" || !results.Items[1].Skipped {
		t.Fatalf("results = %+v", results.Items)
	}
	if got := calls(); got != 1 {
		t.Errorf("claude calls = %d, want 1", got)
	}

	c.post("/api/confirm", map[string]any{"session": cards.Session, "grades": []map[string]any{{"n": 1, "grade": "Hard"}}})
	var status struct {
		Written bool   `json:"written"`
		More    int    `json:"more"`
		Message string `json:"message"`
	}
	c.poll("/api/status", &status)
	if !status.Written || status.More != 2 || status.Message != "" {
		t.Fatalf("status = %+v", status)
	}
	if want := []fakeanki.Answer{{CardID: 1, Ease: 2}}; !reflect.DeepEqual(anki.Answered(), want) {
		t.Errorf("answered = %v, want %v", anki.Answered(), want)
	}
	if got := anki.Syncs(); got != 2 {
		t.Errorf("syncs = %d, want one before the pass and one after the write", got)
	}

	info, err := os.Stat(filepath.Join(dataDir, "state.json"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("state.json: %v, mode %v", err, info.Mode())
	}
}

// The tokens live in memory only: after a restart the server is locked until the owner logs in, and
// the pass saved before the restart comes back with the login.
func TestRestartLocksAndResumes(t *testing.T) {
	anki := fakeanki.New(t,
		fakeanki.Card{ID: 1, Deck: "Tech::Go", Question: "What does defer do?", Answer: "Runs at return", New: true},
		fakeanki.Card{ID: 2, Deck: "Tech::Go", Question: "Zero value of a map?", Answer: "nil", New: true},
	)
	anki.RequireKey(testKey)
	bin, _ := fakeclaude.Use(t, fakeclaude.Good)
	fakeclaude.RequireToken(t, testToken)
	cfg := testConfig(anki, bin, filepath.Join(t.TempDir(), "data"))

	c, stop := serve(t, cfg)
	setUp(t, c)
	c.post("/api/start", map[string]any{"deck": "Tech", "count": 2})
	var cards struct {
		Session string `json:"session"`
	}
	c.get("/api/cards", &cards)
	c.post("/api/answers", map[string]any{"session": cards.Session, "answers": []map[string]any{{"n": 1, "answer": "at return"}, {"n": 2, "answer": "nil"}}})
	var results struct {
		Items []struct {
			N int `json:"n"`
		} `json:"items"`
	}
	c.poll("/api/results", &results)
	stop()

	restarted, _ := serve(t, cfg)
	var session struct {
		State    string `json:"state"`
		Unlocked bool   `json:"unlocked"`
	}
	restarted.get("/api/session", &session)
	if session.State != "login" || session.Unlocked {
		t.Fatalf("after restart: %+v, want login and locked", session)
	}
	if status := restarted.get("/api/results", nil); status != http.StatusUnauthorized {
		t.Fatalf("GET /api/results while locked = %d, want 401", status)
	}
	if status, _ := restarted.call(http.MethodPost, "/api/login", map[string]any{"user": testUser, "password": "wrong password!!"}); status != http.StatusUnauthorized {
		t.Fatalf("wrong password = %d, want 401", status)
	}
	restarted.post("/api/login", map[string]any{"user": testUser, "password": testPassword})
	restarted.poll("/api/results", &results)
	if len(results.Items) != 2 {
		t.Fatalf("resumed results = %+v, want the two graded cards", results.Items)
	}
}
