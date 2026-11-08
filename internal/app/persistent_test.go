package app

import (
	"bytes"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/rs/zerolog"

	"github.com/scv3lqq/anki-review/internal/domain"
	"github.com/scv3lqq/anki-review/internal/repository/filestore"
	"github.com/scv3lqq/anki-review/internal/testutil/fakeanki"
	"github.com/scv3lqq/anki-review/internal/usecase/review"
)

func recoveryKey(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "session.key")
	if err := os.WriteFile(path, bytes.Repeat([]byte{17}, 32), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRecoveryConfiguredMissingKeyRefusesStartup(t *testing.T) {
	cfg := testConfig(fakeanki.New(t), "unused", t.TempDir())
	cfg.SessionKeyFile = filepath.Join(t.TempDir(), "missing")
	if _, err := New(cfg, zerolog.Nop()); err == nil {
		t.Fatal("configured missing key silently disabled recovery")
	}
}

func TestPersistentHTTPCookiesRestoreOwnerAndGuestPassively(t *testing.T) {
	anki := fakeanki.New(t, fakeanki.Card{ID: 1, Deck: "Tech", Question: "Owner question", Answer: "Owner answer"})
	guestAnki := fakeanki.New(t, fakeanki.Card{ID: 1, Deck: "Tech", Question: "Guest question", Answer: "Guest answer"})
	guestAnki.RequireKey("1111111111111111111111111111111111111111111111111111111111111111")
	socket, broker := fakeBroker(t, guestAnki)
	bin, audit := isolatedClaudeFixture(t)
	codexBin, codexCalls := refreshingCodexFixture(t)
	cfg := testConfig(anki, bin, t.TempDir())
	cfg.AccountSocket = socket
	cfg.MaxAccounts = 4
	cfg.SessionKeyFile = recoveryKey(t)
	cfg.CodexBin = codexBin
	root, stop := serve(t, cfg)
	setUp(t, root)
	guest := separateClient(t, root.base)
	id := inviteGuest(t, root, guest, "guest", "codex", testCodexAuth)
	guest.post("/api/preferences", map[string]any{"knowledge_decks": []string{"Tech"}})
	for _, c := range []client{root, guest} {
		c.post("/api/start", map[string]any{"deck": "Tech", "count": 1})
	}
	stop()
	paths := []string{cfg.DataDir, filepath.Join(cfg.DataDir, "accounts", id)}
	before := make([][]byte, len(paths))
	for i, directory := range paths {
		store, err := filestore.New(directory)
		if err != nil {
			t.Fatal(err)
		}
		pass, err := store.Load()
		if err != nil {
			t.Fatal(err)
		}
		pass.Answers = []domain.Answer{{N: 1, Text: "saved answer"}}
		if i == 1 {
			pass.Results = []domain.Result{{N: 1, Grade: domain.Hard, Note: "saved feedback"}}
			pass.Confirmed = []domain.Confirmed{{N: 1, Grade: domain.Easy}}
		}
		if err := store.Save(pass); err != nil {
			t.Fatal(err)
		}
		before[i], err = os.ReadFile(filepath.Join(directory, "state.json"))
		if err != nil {
			t.Fatal(err)
		}
	}
	auditBefore, err := os.ReadFile(audit)
	if err != nil {
		t.Fatal(err)
	}
	callsBefore := codexCalls()
	legacy := cfg
	legacy.SessionKeyFile = ""
	if _, err := New(legacy, zerolog.Nop()); err == nil {
		t.Fatal("recovery-bearing vault accepted without session key")
	}
	restarted, stopRestart := serve(t, cfg)
	broker.mu.Lock()
	opensBefore := broker.ensures
	broker.mu.Unlock()
	anonymous := separateClient(t, restarted.base)
	baseURL, err := url.Parse(restarted.base)
	if err != nil {
		t.Fatal(err)
	}
	anonymous.http.Jar.SetCookies(baseURL, []*http.Cookie{{Name: "anki_review_session", Value: "unknown-cookie"}})
	for _, path := range []string{"/api/session", "/api/cards"} {
		anonymous.get(path, nil)
	}
	broker.mu.Lock()
	opensAfter := broker.ensures
	broker.mu.Unlock()
	if opensBefore != opensAfter {
		t.Fatal("unknown cookie opened guest runtime")
	}
	root.base, guest.base = restarted.base, restarted.base
	for i, entry := range []struct {
		c            client
		id, provider string
	}{{root, "owner", "claude"}, {guest, id, "codex"}} {
		var session map[string]any
		entry.c.get("/api/session", &session)
		if session["state"] != "ready" || session["account_id"] != entry.id || session["grader_provider"] != entry.provider {
			t.Fatalf("restored cookie identity=%v", session)
		}
		var cards review.CardsView
		if entry.c.get("/api/cards", &cards) != 200 || !cards.Paused || !reflect.DeepEqual(cards.Answers, []domain.Answer{{N: 1, Text: "saved answer"}}) {
			t.Fatalf("saved pass not paused: %+v", cards)
		}
		var results review.ResultsView
		entry.c.get("/api/results", &results)
		if !results.Paused || (i == 1 && (results.Confirmed[1] != domain.Easy || results.Items[0].Grade != domain.Hard)) {
			t.Fatalf("saved grades lost: %+v", results)
		}
		after, err := os.ReadFile(filepath.Join(paths[i], "state.json"))
		if err != nil || !bytes.Equal(before[i], after) {
			t.Fatal("passive HTTP reads changed saved pass")
		}
	}
	var prefs map[string]any
	guest.get("/api/preferences", &prefs)
	if !reflect.DeepEqual(prefs["knowledge_decks"], []any{"Tech"}) {
		t.Fatal("preferences not retained")
	}
	auditAfter, err := os.ReadFile(audit)
	if err != nil || !bytes.Equal(auditBefore, auditAfter) || codexCalls() != callsBefore || len(anki.Answered()) != 0 || len(guestAnki.Answered()) != 0 {
		t.Fatal("restoration triggered grading or writes")
	}
	oldCookies := guest.http.Jar.Cookies(baseURL)
	guest.post("/api/logout", map[string]any{})
	if status, _ := guest.call(http.MethodGet, "/api/cards", nil); status != 401 {
		t.Fatal("logged-out guest authorized")
	}
	if root.get("/api/cards", nil) != 200 {
		t.Fatal("guest logout revoked owner")
	}
	stopRestart()
	again, _ := serve(t, cfg)
	revoked := separateClient(t, again.base)
	againURL, err := url.Parse(again.base)
	if err != nil {
		t.Fatal(err)
	}
	revoked.http.Jar.SetCookies(againURL, oldCookies)
	if revoked.get("/api/cards", nil) != 401 {
		t.Fatal("logout was not durable across HTTP restart")
	}
}
