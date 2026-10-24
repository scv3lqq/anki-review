package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/scv3lqq/anki-review/internal/config"
	"github.com/scv3lqq/anki-review/internal/testutil/fakeanki"
	"github.com/scv3lqq/anki-review/internal/testutil/fakeclaude"
)

func TestMain(m *testing.M) {
	fakeclaude.Main()
	os.Exit(m.Run())
}

type client struct {
	t    *testing.T
	base string
}

func (c client) get(path string, out any) int {
	c.t.Helper()
	resp, err := http.Get(c.base + path) //nolint:noctx // a test against a local server
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	if out != nil && resp.StatusCode == http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			c.t.Fatal(err)
		}
	}
	return resp.StatusCode
}

func (c client) post(path string, body any) {
	c.t.Helper()
	raw, _ := json.Marshal(body)
	resp, err := http.Post(c.base+path, "application/json", bytes.NewReader(raw)) //nolint:noctx // a test against a local server
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var e map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&e)
		c.t.Fatalf("POST %s: %d %v", path, resp.StatusCode, e)
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

// One pass through the real HTTP server, use case and adapters, with Anki and claude faked at the
// process boundary.
func TestReviewPass(t *testing.T) {
	anki := fakeanki.New(t,
		fakeanki.Card{ID: 1, Deck: "Tech::Go", Question: "What does <code>defer</code> do?", Answer: "Runs the call when the function returns", Reps: 2, Mod: 10},
		fakeanki.Card{ID: 2, Deck: "Tech::Go", Question: "Zero value of a map?", Answer: "nil", New: true},
		fakeanki.Card{ID: 3, Deck: "Tech::Go", Question: "Is a nil map writable?", Answer: "No, it panics", Reps: 1, Mod: 20},
		fakeanki.Card{ID: 4, Deck: "English", Question: "arrange (verb)", Answer: "организовать"},
	)
	bin, calls := fakeclaude.Use(t, fakeclaude.Good)
	dataDir := filepath.Join(t.TempDir(), "data")
	a, err := New(config.Config{
		DataDir:        dataDir,
		KnowledgeDecks: []string{"Tech"},
		AnkiURL:        anki.URL(),
		AnkiSync:       true,
		ClaudeBin:      bin,
		GradeTimeout:   10 * time.Second,
	}, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	var lc net.ListenConfig
	ln, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, stop := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- a.Serve(ctx, ln) }()
	t.Cleanup(func() {
		stop()
		if err := <-served; err != nil {
			t.Errorf("Serve() = %v", err)
		}
	})
	c := client{t: t, base: "http://" + ln.Addr().String()}

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
	resp, err := http.Get(c.base + "/api/cards") //nolint:noctx // a test against a local server
	if err != nil {
		t.Fatal(err)
	}
	raw := new(bytes.Buffer)
	_, _ = raw.ReadFrom(resp.Body)
	resp.Body.Close()
	if strings.Contains(raw.String(), "Runs the call") || strings.Contains(raw.String(), `"nil"`) {
		t.Fatalf("answer keys reached the page before grading: %s", raw)
	}
	if err := json.Unmarshal(raw.Bytes(), &cards); err != nil {
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
