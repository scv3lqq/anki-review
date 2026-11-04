package app

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/scv3lqq/anki-review/internal/config"
	"github.com/scv3lqq/anki-review/internal/domain"
	"github.com/scv3lqq/anki-review/internal/repository/filestore"
	"github.com/scv3lqq/anki-review/internal/repository/runtimebroker"
	"github.com/scv3lqq/anki-review/internal/repository/vault"
	"github.com/scv3lqq/anki-review/internal/testutil/fakeanki"
	"github.com/scv3lqq/anki-review/internal/usecase/accounts"
	"github.com/scv3lqq/anki-review/internal/usecase/review"
)

type brokerFixture struct {
	mu               sync.Mutex
	endpoints        []*fakeanki.Anki
	ids              map[string]int
	fail             bool
	removeFail       bool
	ensures, removes int
}

func fakeBroker(t *testing.T, endpoints ...*fakeanki.Anki) (string, *brokerFixture) {
	t.Helper()
	directory, err := os.MkdirTemp("/tmp", "anki-broker-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	socket := filepath.Join(directory, "control.sock")
	lc := net.ListenConfig{}
	listener, err := lc.Listen(context.Background(), "unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	b := &brokerFixture{endpoints: endpoints, ids: map[string]int{}}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b.mu.Lock()
		defer b.mu.Unlock()
		id := strings.TrimPrefix(r.URL.Path, "/v1/runtimes/")
		if r.Method == http.MethodDelete {
			b.removes++
			if b.removeFail {
				http.Error(w, "private cleanup detail", http.StatusServiceUnavailable)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		b.ensures++
		if b.fail {
			http.Error(w, "private broker failure", http.StatusServiceUnavailable)
			return
		}
		index, exists := b.ids[id]
		if !exists {
			index = len(b.ids)
			if index >= len(b.endpoints) {
				http.Error(w, "capacity", http.StatusConflict)
				return
			}
			b.ids[id] = index
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"url": b.endpoints[index].URL(), "key": strings.Repeat(fmt.Sprint(index+1), 64)})
	})}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	return socket, b
}

func isolatedClaudeFixture(t *testing.T) (string, string) {
	t.Helper()
	directory := t.TempDir()
	audit := filepath.Join(directory, "tokens")
	bin := filepath.Join(directory, "claude")
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }
	script := "#!/bin/sh\nprintf '%s\\n' \"${CLAUDE_CODE_OAUTH_TOKEN:-${ANTHROPIC_API_KEY:-}}\" >>" + quote(audit) + "\nFAKE_CLAUDE=good GORACE=atexit_sleep_ms=0 exec " + quote(os.Args[0]) + " \"$@\"\n"
	if err := os.WriteFile(bin, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return bin, audit
}

func refreshingCodexFixture(t *testing.T) (string, func() int) {
	t.Helper()
	directory := t.TempDir()
	calls := filepath.Join(directory, "calls")
	bin := filepath.Join(directory, "codex")
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }
	// Credential checks preserve the initial fixture session; only grading rotates it.
	script := "#!/bin/sh\nprompt=$(cat)\ncase \"$prompt\" in *'Reply with JSON'*) mode=good ;; *) mode=refresh-good ;; esac\nprintf '%s' \"$prompt\" | FAKE_CODEX=\"$mode\" FAKE_CODEX_CALLS=" + quote(calls) + " GORACE=atexit_sleep_ms=0 " + quote(os.Args[0]) + " \"$@\"\n"
	if err := os.WriteFile(bin, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return bin, func() int { raw, _ := os.ReadFile(calls); return strings.Count(string(raw), "call\n") }
}

func separateClient(t *testing.T, base string) client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return client{t: t, base: base, http: &http.Client{Jar: jar}}
}

func inviteGuest(t *testing.T, root client, guest client, user, provider, credential string) string {
	t.Helper()
	var invitation struct {
		Token string `json:"token"`
	}
	status, body := root.call(http.MethodPost, "/api/invitations", map[string]any{})
	if status != 200 {
		t.Fatalf("invite %d %s", status, body)
	}
	if err := json.Unmarshal(body, &invitation); err != nil {
		t.Fatal(err)
	}
	input := map[string]any{"invitation": invitation.Token, "user": user, "password": testPassword, "grader_provider": provider}
	if provider == "codex" {
		input["codex_auth"] = credential
	} else {
		input["claude_token"] = credential
	}
	guest.post("/api/register", input)
	var session map[string]any
	guest.get("/api/session", &session)
	id, _ := session["account_id"].(string)
	if len(id) != 32 {
		t.Fatalf("guest session: %v", session)
	}
	return id
}

type multiFixture struct {
	cfg             config.Config
	root, a, b      client
	ankiA, ankiB    *fakeanki.Anki
	broker          *brokerFixture
	audit, idA, idB string
	stop            func()
	codexCalls      func() int
}

func multiAccounts(t *testing.T) *multiFixture {
	t.Helper()
	ownerAnki := fakeanki.New(t, fakeanki.Card{ID: 1, Deck: "Owner", Question: "Owner question", Answer: "Owner back"})
	ownerAnki.RequireKey(testKey)
	a := fakeanki.New(t, fakeanki.Card{ID: 1, Deck: "Tech", Question: `<b>A question</b><img src="photo.png"><script>attack()</script><audio src="voice.mp3"></audio>`, Answer: "A private back", CSS: ".card { color: red; }"})
	b := fakeanki.New(t, fakeanki.Card{ID: 1, Deck: "Tech", Question: "B question", Answer: "B private back"})
	a.RequireKey(strings.Repeat("1", 64))
	b.RequireKey(strings.Repeat("2", 64))
	a.SetMedia("photo.png", []byte("A image"))
	b.SetMedia("photo.png", []byte("B image"))
	socket, broker := fakeBroker(t, a, b)
	bin, audit := isolatedClaudeFixture(t)
	codexBin, codexCalls := refreshingCodexFixture(t)
	cfg := testConfig(ownerAnki, bin, t.TempDir())
	cfg.AccountSocket = socket
	cfg.MaxAccounts = 4
	cfg.CodexBin = codexBin
	root, stop := serve(t, cfg)
	setUp(t, root)
	f := &multiFixture{cfg: cfg, root: root, a: separateClient(t, root.base), b: separateClient(t, root.base), ankiA: a, ankiB: b, broker: broker, audit: audit, stop: stop}
	f.codexCalls = codexCalls
	f.idA = inviteGuest(t, root, f.a, "guest-a", "claude", "sk-ant-oat01-guest-a")
	f.idB = inviteGuest(t, root, f.b, "guest-b", "codex", testCodexAuth)
	return f
}

func TestMultiAccountIsolationAndCrossTab(t *testing.T) {
	f := multiAccounts(t)
	base, err := url.Parse(f.a.base)
	if err != nil {
		t.Fatal(err)
	}
	cookiesA, cookiesB := f.a.http.Jar.Cookies(base), f.b.http.Jar.Cookies(base)
	if len(cookiesA) != 1 || len(cookiesB) != 1 || cookiesA[0].Value == cookiesB[0].Value {
		t.Fatal("account cookies reused")
	}
	ownerBefore, err := os.ReadFile(filepath.Join(f.cfg.DataDir, "vault.json"))
	if err != nil {
		t.Fatal(err)
	}
	aBefore, err := os.ReadFile(filepath.Join(f.cfg.DataDir, "accounts", f.idA, "vault.json"))
	if err != nil {
		t.Fatal(err)
	}
	if status, _ := separateClient(t, f.a.base).call(http.MethodGet, "/api/media/photo.png", nil); status != 401 {
		t.Fatal("anonymous media exposed")
	}
	for _, c := range []client{f.a, f.b} {
		c.post("/api/start", map[string]any{"deck": "Tech", "count": 1})
	}
	var cardsA, cardsB review.CardsView
	f.a.get("/api/cards", &cardsA)
	f.b.get("/api/cards", &cardsB)
	if cardsA.Session == cardsB.Session || !strings.Contains(cardsA.Cards[0].Question, "A question") || cardsB.Cards[0].Question != "B question" {
		t.Fatal("collections crossed")
	}
	if status, _ := f.b.call(http.MethodPost, "/api/answers", map[string]any{"session": cardsA.Session}); status != 409 {
		t.Fatal("foreign pass accepted")
	}
	if status, _ := f.b.call(http.MethodGet, "/api/render?session="+cardsA.Session+"&n=1&side=question", nil); status != 409 {
		t.Fatal("foreign render accepted")
	}
	if status, _ := f.a.call(http.MethodGet, "/api/render?session="+cardsA.Session+"&n=1&side=back", nil); status != 409 {
		t.Fatal("ungraded back exposed")
	}
	if status, body := f.a.call(http.MethodGet, "/api/render?session="+cardsA.Session+"&n=1&side=question", nil); status != 200 || !strings.Contains(string(body), "color: red") || strings.Contains(string(body), "<script>") {
		t.Fatalf("unsafe render %d %s", status, body)
	}
	for _, test := range []struct {
		c    client
		want string
	}{{f.a, "A image"}, {f.b, "B image"}} {
		status, body := test.c.call(http.MethodGet, "/api/media/photo.png", nil)
		if status != 200 || string(body) != test.want {
			t.Fatalf("media crossed: %d %s", status, body)
		}
	}
	if status, _ := f.a.call(http.MethodPost, "/api/invitations", map[string]any{}); status != 403 {
		t.Fatal("guest created invitation")
	}
	if status, _ := f.a.call(http.MethodPost, "/api/settings", map[string]any{"password": testPassword, "anki_key": "wrong"}); status != 400 {
		t.Fatal("guest infrastructure changed")
	}
	var group sync.WaitGroup
	for _, entry := range []struct {
		c client
		s string
	}{{f.a, cardsA.Session}, {f.b, cardsB.Session}} {
		group.Go(func() {
			entry.c.post("/api/answers", map[string]any{"session": entry.s, "answers": []map[string]any{{"n": 1, "answer": "own answer"}}})
		})
	}
	group.Wait()
	for _, entry := range []struct {
		c client
		s string
	}{{f.a, cardsA.Session}, {f.b, cardsB.Session}} {
		entry.c.poll("/api/results", nil)
		status, raw := entry.c.call(http.MethodGet, "/api/render?session="+entry.s+"&n=1&side=back", nil)
		if status != 200 || !strings.Contains(string(raw), "private back") {
			t.Fatal("graded back unavailable")
		}
		entry.c.post("/api/confirm", map[string]any{"session": entry.s})
		entry.c.poll("/api/status", nil)
	}
	if !reflect.DeepEqual(f.ankiA.Answered(), []fakeanki.Answer{{CardID: 1, Ease: 3}}) || !reflect.DeepEqual(f.ankiB.Answered(), []fakeanki.Answer{{CardID: 1, Ease: 3}}) || f.ankiA.Syncs() != 0 || f.ankiB.Syncs() != 0 {
		t.Fatal("confirmation or sync crossed accounts")
	}
	passPath := filepath.Join(f.cfg.DataDir, "accounts", f.idA, "state.json")
	passBefore, err := os.ReadFile(passPath)
	if err != nil {
		t.Fatal(err)
	}
	f.ankiA.FailImport(true)
	if status := uploadPackage(t, f.a, importFixture(t)); status != 503 {
		t.Fatal("native import failure was hidden")
	}
	passAfter, err := os.ReadFile(passPath)
	if err != nil || !bytes.Equal(passBefore, passAfter) {
		t.Fatal("failed native import changed the written saved pass")
	}
	for _, name := range f.ankiA.MediaFiles() {
		if strings.HasPrefix(name, "_anki-review-import-") {
			t.Fatal("failed native import retained staging")
		}
	}
	rootTokens, err := os.ReadFile(f.audit)
	if err != nil || !strings.Contains(string(rootTokens), testToken) || !strings.Contains(string(rootTokens), "sk-ant-oat01-guest-a") {
		t.Fatal("isolated credentials were not used")
	}
	for _, token := range strings.Fields(string(rootTokens)) {
		if token != testToken && token != "sk-ant-oat01-guest-a" {
			t.Fatal("unexpected credential reached Claude")
		}
	}
	if f.codexCalls() != 2 {
		t.Fatalf("Codex calls=%d, want guest credential check plus its grade", f.codexCalls())
	}
	for _, route := range []string{"/api/preferences", "/api/settings", "/api/logout"} {
		body := `{"password":"a long password","knowledge_decks":["changed"],"claude_token":"changed"}`
		r, err := http.NewRequestWithContext(context.Background(), http.MethodPost, f.b.base+route, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Anki-Account", f.idA)
		response, err := f.b.http.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if response.StatusCode != http.StatusConflict || !strings.Contains(string(raw), "account_changed") {
			t.Fatalf("cross-tab %s %d %s", route, response.StatusCode, raw)
		}
	}
	var session map[string]any
	f.b.get("/api/session", &session)
	if session["account_id"] != f.idB {
		t.Fatal("expectation header routed or logged out wrong account")
	}
	f.stop()
	ownerAfter, err := os.ReadFile(filepath.Join(f.cfg.DataDir, "vault.json"))
	if err != nil || !bytes.Equal(ownerBefore, ownerAfter) {
		t.Fatal("guest refresh changed owner vault")
	}
	aAfter, err := os.ReadFile(filepath.Join(f.cfg.DataDir, "accounts", f.idA, "vault.json"))
	if err != nil || !bytes.Equal(aBefore, aAfter) {
		t.Fatal("Codex refresh changed the other guest vault")
	}
	v, err := vault.New(filepath.Join(f.cfg.DataDir, "accounts", f.idB))
	if err != nil {
		t.Fatal(err)
	}
	secrets, err := v.Open("guest-b", testPassword)
	v.Close()
	if err != nil || !strings.Contains(secrets.CodexAuth, "access-rotated") || secrets.ClaudeToken != "" {
		t.Fatal("guest refresh crossed vaults")
	}
}

func TestMultiAccountRestartPrivacyAndOwnerAvailability(t *testing.T) {
	f := multiAccounts(t)
	f.a.post("/api/start", map[string]any{"deck": "Tech", "count": 1})
	f.b.post("/api/start", map[string]any{"deck": "Tech", "count": 1})
	f.stop()
	for _, entry := range []struct {
		id        string
		confirmed bool
	}{{f.idA, false}, {f.idB, true}} {
		store, err := filestore.New(filepath.Join(f.cfg.DataDir, "accounts", entry.id))
		if err != nil {
			t.Fatal(err)
		}
		pass, err := store.Load()
		if err != nil {
			t.Fatal(err)
		}
		pass.Answers = []domain.Answer{{N: 1, Text: "saved answer"}}
		if entry.confirmed {
			pass.Results = []domain.Result{{N: 1, Grade: domain.Good}}
			pass.Confirmed = []domain.Confirmed{{N: 1, Grade: domain.Good}}
		}
		if err := store.Save(pass); err != nil {
			t.Fatal(err)
		}
	}
	auditBefore, err := os.ReadFile(f.audit)
	if err != nil {
		t.Fatal(err)
	}
	codexBefore := f.codexCalls()
	f.broker.mu.Lock()
	ensuresBefore := f.broker.ensures
	f.broker.mu.Unlock()
	ownerPath := filepath.Join(f.cfg.DataDir, "vault.json")
	before, err := os.ReadFile(ownerPath)
	if err != nil {
		t.Fatal(err)
	}
	restarted, _ := serve(t, f.cfg)
	f.broker.mu.Lock()
	ensuresAfter := f.broker.ensures
	f.broker.mu.Unlock()
	if ensuresBefore != ensuresAfter {
		t.Fatal("startup eagerly opened guest runtimes")
	}
	time.Sleep(100 * time.Millisecond)
	auditAfter, err := os.ReadFile(f.audit)
	if err != nil || !bytes.Equal(auditBefore, auditAfter) || f.codexCalls() != codexBefore || len(f.ankiA.Answered()) != 0 || len(f.ankiB.Answered()) != 0 {
		t.Fatal("restart performed grading or writes while locked")
	}
	after, err := os.ReadFile(ownerPath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("owner vault changed at startup")
	}
	status, raw := restarted.call(http.MethodGet, "/api/session", nil)
	if status != 200 || strings.Contains(string(raw), "stage") || strings.Contains(string(raw), "grader") || strings.Contains(string(raw), "account_id") {
		t.Fatalf("anonymous privacy %d %s", status, raw)
	}
	a := separateClient(t, restarted.base)
	a.post("/api/login", map[string]string{"user": "guest-a", "password": testPassword})
	a.poll("/api/results", nil)
	var cards review.CardsView
	a.get("/api/cards", &cards)
	if cards.Stage != "results" || !strings.Contains(cards.Cards[0].Question, "A question") {
		t.Fatal("own pass did not resume")
	}
	auditAfter, err = os.ReadFile(f.audit)
	if err != nil || len(auditAfter) <= len(auditBefore) || f.codexCalls() != codexBefore || len(f.ankiB.Answered()) != 0 {
		t.Fatal("A unlock resumed the wrong account background work")
	}
	if status, _ := restarted.call(http.MethodGet, "/api/cards", nil); status != 401 {
		t.Fatal("another account unlocked anonymously")
	}
	b := separateClient(t, restarted.base)
	b.post("/api/login", map[string]string{"user": "guest-b", "password": testPassword})
	b.poll("/api/status", nil)
	if len(f.ankiB.Answered()) != 1 || len(f.ankiA.Answered()) != 0 {
		t.Fatal("confirmed B pass did not resume only on B login")
	}
	f.broker.mu.Lock()
	f.broker.fail = true
	f.broker.mu.Unlock()
	restarted.post("/api/login", map[string]string{"user": testUser, "password": testPassword})
	if status := restarted.get("/api/decks", nil); status != 200 {
		t.Fatal("broker outage blocked owner")
	}
}

func importFixture(t *testing.T) []byte {
	t.Helper()
	var body bytes.Buffer
	writer := zip.NewWriter(&body)
	for name, data := range map[string]string{"collection.anki2": "SQLite format 3\x00fixture", "media": `{"0":"photo.png"}`, "0": "imported image"} {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(data)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return body.Bytes()
}

func uploadPackage(t *testing.T, c client, body []byte) int {
	t.Helper()
	r, err := http.NewRequestWithContext(context.Background(), http.MethodPost, c.base+"/api/import", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Content-Type", "application/octet-stream")
	r.Header.Set("X-Anki-Import", "1")
	response, err := c.http.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	return response.StatusCode
}

func TestMultiAccountImportAndPreferences(t *testing.T) {
	f := multiAccounts(t)
	f.a.post("/api/preferences", map[string]any{"knowledge_decks": []string{"Tech"}})
	var prefs map[string]any
	f.b.get("/api/preferences", &prefs)
	if decks, ok := prefs["knowledge_decks"].([]any); !ok || len(decks) != 0 {
		t.Fatal("preferences crossed accounts")
	}
	f.ankiA.FailImport(true)
	if status := uploadPackage(t, f.a, importFixture(t)); status != 503 {
		t.Fatalf("failed import status=%d", status)
	}
	for _, name := range f.ankiA.MediaFiles() {
		if strings.HasPrefix(name, "_anki-review-import-") {
			t.Fatal("failed import leaked staging")
		}
	}
	f.ankiA.FailImport(false)
	if status := uploadPackage(t, f.a, importFixture(t)); status != 200 {
		t.Fatalf("import status %d", status)
	}
	if status, body := f.a.call(http.MethodGet, "/api/media/photo.png", nil); status != 200 || string(body) != "imported image" {
		t.Fatal("imported media missing")
	}
	f.a.post("/api/start", map[string]any{"deck": "Tech", "count": 1})
	var cards review.CardsView
	f.a.get("/api/cards", &cards)
	if status := uploadPackage(t, f.a, importFixture(t)); status != 409 {
		t.Fatal("active import allowed")
	}
	if status := uploadPackage(t, f.a, []byte("invalid archive")); status != 409 {
		t.Fatal("active pass did not take precedence over import validation")
	}
	var after review.CardsView
	f.a.get("/api/cards", &after)
	if after.Session != cards.Session || after.Stage != cards.Stage {
		t.Fatal("import changed active pass")
	}
	if status, _ := f.a.call(http.MethodPost, "/api/preferences", map[string]any{"knowledge_decks": []string{}}); status != 409 {
		t.Fatal("active preferences changed")
	}
	if status, _ := f.b.call(http.MethodPost, "/api/preferences", map[string]any{}); status != 400 {
		t.Fatal("missing preferences accepted")
	}
}

func TestMultiAccountRegistrationRetryPrivacyAndCapacity(t *testing.T) {
	ownerAnki := fakeanki.New(t)
	ownerAnki.RequireKey(testKey)
	guestAnki := fakeanki.New(t)
	guestAnki.RequireKey("wrong-private-infrastructure-key")
	socket, broker := fakeBroker(t, guestAnki)
	bin, _ := isolatedClaudeFixture(t)
	cfg := testConfig(ownerAnki, bin, t.TempDir())
	cfg.AccountSocket = socket
	cfg.MaxAccounts = 2
	root, _ := serve(t, cfg)
	setUp(t, root)
	guest := separateClient(t, root.base)
	var invitation struct {
		Token string `json:"token"`
	}
	status, body := root.call(http.MethodPost, "/api/invitations", map[string]any{})
	if status != 200 {
		t.Fatal("invite failed")
	}
	if err := json.Unmarshal(body, &invitation); err != nil {
		t.Fatal(err)
	}
	request := map[string]any{"invitation": invitation.Token, "user": "retry-guest", "password": testPassword, "claude_token": "sk-ant-oat01-retry-fixture"}
	broker.mu.Lock()
	broker.removeFail = true
	broker.mu.Unlock()
	status, body = guest.call(http.MethodPost, "/api/register", request)
	if status != 503 || !strings.Contains(string(body), "Your collection is temporarily unavailable. Try again.") || strings.Contains(string(body), "anki_key") || strings.Contains(string(body), "private") || strings.Contains(string(body), "127.0.0.1") {
		t.Fatalf("managed error privacy: %d %s", status, body)
	}
	broker.mu.Lock()
	reserved := len(broker.ids)
	broker.removeFail = false
	broker.mu.Unlock()
	guestAnki.RequireKey(strings.Repeat("1", 64))
	guest.post("/api/register", request)
	broker.mu.Lock()
	after := len(broker.ids)
	broker.mu.Unlock()
	if reserved != 1 || after != 1 {
		t.Fatal("retry allocated another runtime")
	}
	if status, _ := separateClient(t, root.base).call(http.MethodPost, "/api/register", request); status != 400 {
		t.Fatal("invitation reused")
	}
	status, body = root.call(http.MethodPost, "/api/invitations", map[string]any{})
	if status != 200 {
		t.Fatal("second invite failed")
	}
	if err := json.Unmarshal(body, &invitation); err != nil {
		t.Fatal(err)
	}
	request["invitation"] = invitation.Token
	request["user"] = "over-capacity"
	if status, _ := separateClient(t, root.base).call(http.MethodPost, "/api/register", request); status != 409 {
		t.Fatal("HTTP account cap bypassed")
	}
}

func TestMultiAccountFactoryRemovePreservesActualVault(t *testing.T) {
	socket, broker := fakeBroker(t, fakeanki.New(t))
	client, err := runtimebroker.New(socket)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{DataDir: t.TempDir()}
	id := strings.Repeat("a", 32)
	v, err := vault.New(filepath.Join(cfg.DataDir, "accounts", id), vault.WithCost(1, 8, 1))
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Seal("guest", testPassword, domain.Secrets{ClaudeToken: "fixture"}); err != nil {
		t.Fatal(err)
	}
	v.Close()
	factory := &accountFactory{cfg: cfg, broker: client, runtimes: map[string]*App{}}
	if err := factory.Remove(context.Background(), id); !errors.Is(err, accounts.ErrHasVault) {
		t.Fatalf("existing vault removal: %v", err)
	}
	broker.mu.Lock()
	calls := broker.removes
	broker.mu.Unlock()
	if calls != 0 {
		t.Fatal("broker delete happened before checking vault")
	}
}

func TestMultiAccountLegacyMigrationKeepsOwnerFilesAndEndpoint(t *testing.T) {
	anki := fakeanki.New(t, fakeanki.Card{ID: 1, Deck: "Tech", Question: "Legacy question", Answer: "Legacy back"})
	anki.RequireKey(testKey)
	bin, _ := isolatedClaudeFixture(t)
	cfg := testConfig(anki, bin, t.TempDir())
	legacy, stop := serve(t, cfg)
	setUp(t, legacy)
	legacy.post("/api/start", map[string]any{"deck": "Tech", "count": 1})
	stop()
	before := map[string][]byte{}
	for _, name := range []string{"vault.json", "state.json"} {
		raw, err := os.ReadFile(filepath.Join(cfg.DataDir, name))
		if err != nil {
			t.Fatal(err)
		}
		before[name] = raw
	}
	socket, broker := fakeBroker(t)
	broker.mu.Lock()
	broker.fail = true
	broker.mu.Unlock()
	cfg.AccountSocket = socket
	cfg.MaxAccounts = 4
	multi, _ := serve(t, cfg)
	for name, raw := range before {
		after, err := os.ReadFile(filepath.Join(cfg.DataDir, name))
		if err != nil || !bytes.Equal(raw, after) {
			t.Fatalf("legacy %s changed before unlock", name)
		}
	}
	multi.post("/api/login", map[string]string{"user": testUser, "password": testPassword})
	var cards review.CardsView
	multi.get("/api/cards", &cards)
	if cards.Stage != "input" || cards.Cards[0].Question != "Legacy question" {
		t.Fatal("legacy owner endpoint or pass changed")
	}
	broker.mu.Lock()
	ensures := broker.ensures
	broker.mu.Unlock()
	if ensures != 0 {
		t.Fatal("owner touched broker")
	}
}
