package httpapi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/scv3lqq/anki-review/internal/domain"
	"github.com/scv3lqq/anki-review/internal/usecase/review"
)

type collectionStub struct {
	imported bool
	saved    bool
	decks    []string
}

func (c *collectionStub) Import(context.Context, []byte) error { c.imported = true; return nil }
func (*collectionStub) Media(context.Context, string) ([]byte, error) {
	return []byte("<html><script>attack()</script></html>"), nil
}
func (c *collectionStub) Preferences() ([]string, error) { return c.decks, nil }
func (c *collectionStub) SavePreferences(_ context.Context, d []string) error {
	c.saved = true
	c.decks = d
	return nil
}

func TestAccountExpectationPrecedesMutations(t *testing.T) {
	own := readyOwner()
	collection := &collectionStub{}
	svc := &stub{deck: "unchanged", session: "unchanged"}
	h, err := NewWithOptions(own, func() Service { return svc }, static, zerolog.Nop(), Options{AccountID: "account-b", MultiAccount: true, Managed: true, Collection: collection})
	if err != nil {
		t.Fatal(err)
	}
	for _, route := range []string{"/api/logout", "/api/settings", "/api/preferences", "/api/import", "/api/session", "/api/start", "/api/confirm", "/api/reset"} {
		method, content := http.MethodPost, "application/json"
		if route == "/api/session" {
			method = http.MethodGet
		}
		if route == "/api/import" {
			content = "application/octet-stream"
		}
		req := httptest.NewRequestWithContext(context.Background(), method, route, strings.NewReader(`{"password":"old password","knowledge_decks":["wrong"]}`))
		req.Host = "localhost"
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: sessionValue})
		req.Header.Set("Content-Type", content)
		req.Header.Set("X-Anki-Import", "1")
		req.Header.Set("X-Anki-Account", "account-a")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != 409 || !strings.Contains(w.Body.String(), `"code":"account_changed"`) {
			t.Fatalf("%s: %d %s", route, w.Code, w.Body)
		}
		if w.Header().Get("X-Anki-Account") != "account-b" {
			t.Fatal("missing actual account identity")
		}
	}
	if own.logout != "" || own.updated.Password != "" || collection.saved || collection.imported || svc.deck != "unchanged" || svc.session != "unchanged" {
		t.Fatal("stale account mutation reached current account")
	}
}

func TestImportSizeAndAuthentication(t *testing.T) {
	collection := &collectionStub{}
	h, err := NewWithOptions(readyOwner(), func() Service { return &stub{} }, static, zerolog.Nop(), Options{AccountID: "owner", Collection: collection})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		cookie string
		size   int64
		stream bool
		want   int
	}{{"", 1, false, 401}, {sessionValue, 8<<20 + 1, false, 413}, {sessionValue, 8<<20 + 1, true, 413}} {
		body := "x"
		if tc.stream {
			body = strings.Repeat("x", int(tc.size))
		}
		r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/import", strings.NewReader(body))
		r.Host = "localhost"
		r.Header.Set("Content-Type", "application/octet-stream")
		r.Header.Set("X-Anki-Import", "1")
		if tc.cookie != "" {
			r.AddCookie(&http.Cookie{Name: sessionCookie, Value: tc.cookie})
		}
		r.ContentLength = tc.size
		if tc.stream {
			r.ContentLength = -1
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatalf("size/auth %+v: %d", tc, w.Code)
		}
	}
	if collection.imported {
		t.Fatal("unauthorized or oversized import reached collection")
	}
}

type renderedFixture struct {
	stub
	cards   review.CardsView
	results review.ResultsView
}

func (f *renderedFixture) Cards() review.CardsView              { return f.cards }
func (f *renderedFixture) Results() (review.ResultsView, error) { return f.results, nil }

func TestRenderedDocumentWithholdsAnswersAndHasSandbox(t *testing.T) {
	svc := &renderedFixture{cards: review.CardsView{Session: "own", Cards: []review.CardView{{N: 1, Question: "<b>literal</b>", QuestionHTML: `<style>.card {color:red}</style><b>question</b><script>alert(1)</script><img src="https://evil.example/pixel">`}}}, results: review.ResultsView{Session: "own", Items: []review.ResultItem{{N: 1, Skipped: true}}}}
	h, err := NewWithOptions(readyOwner(), func() Service { return svc }, static, zerolog.Nop(), Options{AccountID: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		query string
		want  int
	}{{"session=foreign&n=1&side=question", 409}, {"session=own&n=1&side=back", 409}, {"session=own&n=2&side=question", 404}, {"session=own&n=1&side=invalid", 400}, {"session=own&n=1&side=question", 200}} {
		w := do(h, http.MethodGet, "/api/render?"+tc.query, "localhost", "", "")
		if w.Code != tc.want {
			t.Fatalf("render %s: %d", tc.query, w.Code)
		}
		if w.Code == 200 && (!strings.Contains(w.Header().Get("Content-Security-Policy"), "sandbox allow-same-origin") || !strings.Contains(w.Body.String(), "<b>question</b>") || strings.Contains(w.Body.String(), "evil.example") || strings.Contains(w.Body.String(), "<script")) {
			t.Fatal("unsafe rendered document")
		}
	}
	svc.results.Items = []review.ResultItem{{N: 1, Grade: domain.Good, Back: "<private back>"}}
	w := do(h, http.MethodGet, "/api/render?session=own&n=1&side=back", "localhost", "", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "&lt;private back&gt;") {
		t.Fatal("legacy plain-text back was not escaped")
	}
}

func TestManagedInfrastructureUpdatesAreRejected(t *testing.T) {
	own := readyOwner()
	h, err := NewWithOptions(own, func() Service { return &stub{} }, static, zerolog.Nop(), Options{AccountID: "guest", Managed: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{`{"password":"old password","anki_key":""}`, `{"password":"old password","anki_url":"http://evil.example"}`, `{"password":"old password","anki_connect_url":"http://evil.example"}`} {
		w := do(h, http.MethodPost, "/api/settings", "localhost", "application/json", body)
		if w.Code != 400 || own.updated.Password != "" {
			t.Fatalf("managed update reached owner: %d %s", w.Code, w.Body)
		}
	}
}

type blockedImport struct {
	collectionStub
	entered chan struct{}
	release chan struct{}
}

func (c *blockedImport) Import(ctx context.Context, _ []byte) error {
	c.entered <- struct{}{}
	select {
	case <-c.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type readSentinel struct {
	reader *strings.Reader
	read   bool
}

func (r *readSentinel) Read(data []byte) (int, error) { r.read = true; return r.reader.Read(data) }

func TestImportLeaseBoundsBodiesPerAccount(t *testing.T) {
	blocked := &blockedImport{entered: make(chan struct{}, 2), release: make(chan struct{})}
	h, err := NewWithOptions(readyOwner(), func() Service { return &stub{} }, static, zerolog.Nop(), Options{AccountID: "a", Collection: blocked})
	if err != nil {
		t.Fatal(err)
	}
	request := func(body io.Reader) *http.Request {
		r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/import", body)
		r.Host = "localhost"
		r.Header.Set("Content-Type", "application/octet-stream")
		r.Header.Set("X-Anki-Import", "1")
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: sessionValue})
		return r
	}
	first := httptest.NewRecorder()
	firstDone := make(chan struct{})
	go func() { h.ServeHTTP(first, request(strings.NewReader("first"))); close(firstDone) }()
	<-blocked.entered
	other := &collectionStub{}
	otherHandler, err := NewWithOptions(readyOwner(), func() Service { return &stub{} }, static, zerolog.Nop(), Options{AccountID: "b", Collection: other})
	if err != nil {
		t.Fatal(err)
	}
	otherResponse := httptest.NewRecorder()
	otherHandler.ServeHTTP(otherResponse, request(strings.NewReader("other")))
	if otherResponse.Code != 200 || !other.imported {
		t.Fatal("one account blocked another account import")
	}
	body := &readSentinel{reader: strings.NewReader("second")}
	second := httptest.NewRecorder()
	secondDone := make(chan struct{})
	go func() { h.ServeHTTP(second, request(body)); close(secondDone) }()
	select {
	case <-secondDone:
	case <-time.After(time.Second):
		close(blocked.release)
		<-firstDone
		<-secondDone
		t.Fatal("concurrent upload queued after reading its body")
	}
	close(blocked.release)
	<-firstDone
	if second.Code != 409 || body.read || !strings.Contains(second.Body.String(), `"code":"busy"`) {
		t.Fatalf("unbounded concurrent upload: code=%d read=%v", second.Code, body.read)
	}
}

func TestImportOriginAndMediaNavigationGuards(t *testing.T) {
	c := &collectionStub{}
	h, err := NewWithOptions(readyOwner(), func() Service { return &stub{} }, static, zerolog.Nop(), Options{AccountID: "owner", Collection: c})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		origin, site, header string
		status               int
	}{
		{"", "", "", 403}, {"https://evil.example", "cross-site", "1", 403}, {"https://public.example", "same-origin", "1", 200}, {"http://localhost", "", "1", 200}, {"https://evil.example", "", "1", 403},
	} {
		r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/import", strings.NewReader("fixture"))
		r.Host = "localhost"
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: sessionValue})
		r.Header.Set("Content-Type", "application/octet-stream")
		r.Header.Set("X-Anki-Import", test.header)
		r.Header.Set("Origin", test.origin)
		r.Header.Set("Sec-Fetch-Site", test.site)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != test.status {
			t.Fatalf("origin %+v: %d %s", test, w.Code, w.Body)
		}
	}
	w := do(h, http.MethodGet, "/api/media/upload.html", "localhost", "", "")
	if w.Code != 200 || !strings.Contains(w.Header().Get("Content-Type"), "application/octet-stream") || !strings.Contains(w.Header().Get("Content-Disposition"), "attachment") {
		t.Fatalf("unsafe media navigation: %d %v", w.Code, w.Header())
	}
}
