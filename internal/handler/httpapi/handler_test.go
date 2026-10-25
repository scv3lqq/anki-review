package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/rs/zerolog"

	"github.com/scv3lqq/anki-review/internal/domain"
	"github.com/scv3lqq/anki-review/internal/usecase/owner"
	"github.com/scv3lqq/anki-review/internal/usecase/review"
)

// stub records the arguments it got and fails with err.
type stub struct {
	err     error
	status  *domain.Status
	deck    string
	count   int
	session string
	answers map[int]string
	grades  map[int]domain.Grade
}

func (s *stub) Decks(context.Context) ([]domain.Deck, error) { return nil, s.err }
func (s *stub) Cards() review.CardsView                      { return review.CardsView{Stage: domain.StageIdle} }
func (s *stub) Results() (review.ResultsView, error)         { return review.ResultsView{Session: "s1"}, s.err }
func (s *stub) Status() *domain.Status                       { return s.status }

func (s *stub) Start(_ context.Context, deck string, count int) error {
	s.deck, s.count = deck, count
	return s.err
}

func (s *stub) More(_ context.Context, session string, count int) error {
	s.session, s.count = session, count
	return s.err
}

func (s *stub) SubmitAnswers(session string, answers map[int]string) error {
	s.session, s.answers = session, answers
	return s.err
}

func (s *stub) Regrade(session string) error {
	s.session = session
	return s.err
}

func (s *stub) Confirm(session string, grades map[int]domain.Grade) error {
	s.session, s.grades = session, grades
	return s.err
}

func (s *stub) Reset(session string) error {
	s.session = session
	return s.err
}

var static = fstest.MapFS{
	"index.html": {Data: []byte("<!doctype html>page")},
	"app.js":     {Data: []byte("boot()")},
	"style.css":  {Data: []byte("body{}")},
}

// sessionValue is the cookie of the one session fakeOwner accepts.
const sessionValue = "good-session"

// fakeOwner answers the owner use case with its fields and records what it was asked.
type fakeOwner struct {
	status  owner.Status
	err     error
	token   string
	setup   owner.SetupInput
	login   [2]string
	client  string
	updated owner.UpdateInput
	logout  string
}

func (f *fakeOwner) Status(string) owner.Status   { return f.status }
func (f *fakeOwner) Authorized(token string) bool { return token == sessionValue }

func (f *fakeOwner) Setup(_ context.Context, client string, in owner.SetupInput) (string, error) {
	f.setup, f.client = in, client
	return f.token, f.err
}

func (f *fakeOwner) Login(_ context.Context, client, user, password string) (string, error) {
	f.login, f.client = [2]string{user, password}, client
	return f.token, f.err
}

func (f *fakeOwner) Logout(token string) { f.logout = token }

func (f *fakeOwner) Update(_ context.Context, _ string, in owner.UpdateInput) error {
	f.updated = in
	return f.err
}

// readyOwner is logged in and unlocked, so the review routes are reachable.
func readyOwner() *fakeOwner {
	return &fakeOwner{status: owner.Status{State: owner.StateReady, Unlocked: true, User: "owner"}}
}

func newHandlerWith(t *testing.T, own Owner, review func() Service) http.Handler {
	t.Helper()
	h, err := New(own, review, static, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func newHandler(t *testing.T, svc Service) http.Handler {
	t.Helper()
	return newHandlerWith(t, readyOwner(), func() Service { return svc })
}

// do sends a request with the session cookie of a logged-in owner.
func do(h http.Handler, method, target, host, contentType, body string) *httptest.ResponseRecorder {
	return send(h, sessionValue, method, target, host, contentType, body)
}

// send sends a request with the given session cookie value; "" sends none.
func send(h http.Handler, cookie, method, target, host, contentType, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(context.Background(), method, target, strings.NewReader(body))
	req.Host = host
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestGuard(t *testing.T) {
	h := newHandler(t, &stub{})
	tests := []struct {
		name, method, target, host, contentType, body string
		want                                          int
	}{
		{"page on loopback", http.MethodGet, "/", "127.0.0.1:8767", "", "", http.StatusOK},
		{"localhost without port", http.MethodGet, "/", "localhost", "", "", http.StatusOK},
		{"rebinding host", http.MethodGet, "/api/cards", "evil.example:8767", "", "", http.StatusForbidden},
		{"proxy forgot to set Host", http.MethodGet, "/", "anki.example.com", "", "", http.StatusForbidden},
		{"form post", http.MethodPost, "/api/reset", "127.0.0.1:8767", "application/x-www-form-urlencoded", "session=s1", http.StatusUnsupportedMediaType},
		{"post without type", http.MethodPost, "/api/reset", "127.0.0.1:8767", "", `{"session":"s1"}`, http.StatusUnsupportedMediaType},
		{"bad json", http.MethodPost, "/api/reset", "127.0.0.1:8767", "application/json", `{"session":`, http.StatusBadRequest},
		{"unknown path", http.MethodGet, "/state.json", "127.0.0.1:8767", "", "", http.StatusNotFound},
		{"wrong method", http.MethodGet, "/api/reset", "127.0.0.1:8767", "", "", http.StatusMethodNotAllowed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(h, tt.method, tt.target, tt.host, tt.contentType, tt.body)
			if rec.Code != tt.want {
				t.Errorf("status = %d, want %d (%s)", rec.Code, tt.want, rec.Body)
			}
			if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "script-src 'self'") {
				t.Errorf("CSP = %q", csp)
			}
		})
	}
}

func TestAssets(t *testing.T) {
	h := newHandler(t, &stub{})
	tests := []struct{ path, wantType, wantBody string }{
		{"/", "text/html; charset=utf-8", "page"},
		{"/app.js", "text/javascript; charset=utf-8", "boot()"},
		{"/style.css", "text/css; charset=utf-8", "body{}"},
	}
	for _, tt := range tests {
		rec := do(h, http.MethodGet, tt.path, "127.0.0.1", "", "")
		if got := rec.Header().Get("Content-Type"); got != tt.wantType || !strings.Contains(rec.Body.String(), tt.wantBody) {
			t.Errorf("GET %s: type %q, body %q", tt.path, got, rec.Body)
		}
	}
}

func TestErrors(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		want     int
		wantCode string
	}{
		{"ok", nil, http.StatusOK, ""},
		{"conflict", &review.ConflictError{Code: review.CodeSession, Message: "another session"}, http.StatusConflict, review.CodeSession},
		{"grading failed", fmt.Errorf("wrapped: %w", &review.GradingFailedError{Reason: "limit"}), http.StatusConflict, codeGradeFailed},
		{"not ready", review.ErrNotReady, http.StatusNoContent, ""},
		{"unknown deck", review.ErrUnknownDeck, http.StatusBadRequest, ""},
		{"anki down", fmt.Errorf("review: %w", domain.ErrAnki), http.StatusBadGateway, ""},
		{"anything else", errors.New("disk full"), http.StatusInternalServerError, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(newHandler(t, &stub{err: tt.err}), http.MethodGet, "/api/results", "127.0.0.1", "", "")
			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d", rec.Code, tt.want)
			}
			if tt.wantCode == "" {
				return
			}
			var body errorBody
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Code != tt.wantCode {
				t.Errorf("body = %s, want code %q", rec.Body, tt.wantCode)
			}
		})
	}
	if rec := do(newHandler(t, &stub{err: errors.New("disk full")}), http.MethodGet, "/api/results", "127.0.0.1", "", ""); strings.Contains(rec.Body.String(), "disk") {
		t.Error("an internal error leaked to the page")
	}
}

func TestRequestsReachService(t *testing.T) {
	tests := []struct {
		name  string
		path  string
		body  string
		check func(t *testing.T, s *stub)
	}{
		{"start", "/api/start", `{"deck":"Tech::Go","count":10}`, func(t *testing.T, s *stub) {
			if s.deck != "Tech::Go" || s.count != 10 {
				t.Errorf("Start(%q, %d)", s.deck, s.count)
			}
		}},
		{"more", "/api/more", `{"session":"s1","count":5}`, func(t *testing.T, s *stub) {
			if s.session != "s1" || s.count != 5 {
				t.Errorf("More(%q, %d)", s.session, s.count)
			}
		}},
		{"answers", "/api/answers", `{"session":"s1","answers":[{"n":1,"answer":"a"},{"n":2,"answer":""}]}`, func(t *testing.T, s *stub) {
			if want := map[int]string{1: "a", 2: ""}; !reflect.DeepEqual(s.answers, want) {
				t.Errorf("answers = %v, want %v", s.answers, want)
			}
		}},
		{"confirm drops unknown grades", "/api/confirm", `{"session":"s1","grades":[{"n":1,"grade":"hard"},{"n":2,"grade":"Perfect"}]}`, func(t *testing.T, s *stub) {
			if want := map[int]domain.Grade{1: domain.Hard}; !reflect.DeepEqual(s.grades, want) {
				t.Errorf("grades = %v, want %v", s.grades, want)
			}
		}},
		{"regrade", "/api/regrade", `{"session":"s1"}`, func(t *testing.T, s *stub) {
			if s.session != "s1" {
				t.Errorf("Regrade(%q)", s.session)
			}
		}},
		{"reset", "/api/reset", `{"session":"s1"}`, func(t *testing.T, s *stub) {
			if s.session != "s1" {
				t.Errorf("Reset(%q)", s.session)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &stub{}
			rec := do(newHandler(t, s), http.MethodPost, tt.path, "127.0.0.1", "application/json", tt.body)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d: %s", rec.Code, rec.Body)
			}
			tt.check(t, s)
		})
	}
}

func TestStatus(t *testing.T) {
	if rec := do(newHandler(t, &stub{}), http.MethodGet, "/api/status", "127.0.0.1", "", ""); rec.Code != http.StatusNoContent {
		t.Errorf("status before a write = %d, want 204", rec.Code)
	}
	rec := do(newHandler(t, &stub{status: &domain.Status{Written: true, More: 3}}), http.MethodGet, "/api/status", "127.0.0.1", "", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"more":3`) {
		t.Errorf("status = %d %s", rec.Code, rec.Body)
	}
}

func TestDecksNeverNull(t *testing.T) {
	rec := do(newHandler(t, &stub{}), http.MethodGet, "/api/decks", "127.0.0.1", "", "")
	if got := strings.TrimSpace(rec.Body.String()); got != `{"decks":[]}` {
		t.Errorf("body = %s", got)
	}
}

func TestBodyLimit(t *testing.T) {
	body := `{"session":"` + strings.Repeat("x", maxBody) + `"}`
	if rec := do(newHandler(t, &stub{}), http.MethodPost, "/api/reset", "127.0.0.1", "application/json", body); rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for a body over %d bytes", rec.Code, maxBody)
	}
}

func TestReviewRoutesNeedSession(t *testing.T) {
	// logout and settings need a session only; the review routes need an unlocked server too
	routes := []struct {
		method, path string
		review       bool
	}{
		{http.MethodGet, "/api/decks", true}, {http.MethodGet, "/api/cards", true}, {http.MethodGet, "/api/results", true},
		{http.MethodGet, "/api/status", true}, {http.MethodPost, "/api/start", true}, {http.MethodPost, "/api/more", true},
		{http.MethodPost, "/api/answers", true}, {http.MethodPost, "/api/regrade", true}, {http.MethodPost, "/api/confirm", true},
		{http.MethodPost, "/api/reset", true}, {http.MethodPost, "/api/logout", false}, {http.MethodPost, "/api/settings", false},
	}
	tests := []struct {
		name   string
		cookie string
		review func() Service
		locked bool
	}{
		{name: "no cookie", cookie: "", review: func() Service { return &stub{} }},
		{name: "unknown cookie", cookie: "stale", review: func() Service { return &stub{} }},
		{name: "valid cookie while locked", cookie: sessionValue, review: func() Service { return nil }, locked: true},
	}
	for _, tt := range tests {
		h := newHandlerWith(t, readyOwner(), tt.review)
		for _, r := range routes {
			if tt.locked && !r.review {
				continue
			}
			t.Run(tt.name+" "+r.method+" "+r.path, func(t *testing.T) {
				rec := send(h, tt.cookie, r.method, r.path, "127.0.0.1", "application/json", "{}")
				var body errorBody
				if rec.Code != http.StatusUnauthorized || json.Unmarshal(rec.Body.Bytes(), &body) != nil || body.Code != codeLogin || body.Error != "Log in first" {
					t.Errorf("status %d body %s, want 401 login", rec.Code, rec.Body)
				}
			})
		}
	}
}

func TestSessionEndpoint(t *testing.T) {
	tests := []struct {
		name   string
		own    *fakeOwner
		review func() Service
		want   map[string]any
	}{
		{
			name:   "setup",
			own:    &fakeOwner{status: owner.Status{State: owner.StateSetup}},
			review: func() Service { return nil },
			want:   map[string]any{"state": "setup", "unlocked": false},
		},
		{
			name:   "login after a restart",
			own:    &fakeOwner{status: owner.Status{State: owner.StateLogin}},
			review: func() Service { return nil },
			want:   map[string]any{"state": "login", "unlocked": false},
		},
		{
			name:   "ready with the review stage",
			own:    readyOwner(),
			review: func() Service { return &stub{} },
			want:   map[string]any{"state": "ready", "unlocked": true, "user": "owner", "stage": "idle"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHandlerWith(t, tt.own, tt.review)
			rec := send(h, "", http.MethodGet, "/api/session", "127.0.0.1", "", "")
			var got map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || rec.Code != http.StatusOK {
				t.Fatalf("status %d body %s", rec.Code, rec.Body)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("body = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestOwnerStatusMapping(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
		body   string
		err    error
		status int
		code   string
		field  string
	}{
		{name: "setup", method: http.MethodPost, path: "/api/setup", body: `{"user":"o","password":"p"}`, status: http.StatusOK},
		{name: "setup done", method: http.MethodPost, path: "/api/setup", body: `{}`, err: owner.ErrSetupDone, status: http.StatusConflict, code: codeSetupDone},
		{name: "setup field", method: http.MethodPost, path: "/api/setup", body: `{}`, err: &owner.FieldError{Field: "anki_key", Message: "AnkiConnect refused this key"}, status: http.StatusBadRequest, field: "anki_key"},
		{name: "setup internal error hides the cause", method: http.MethodPost, path: "/api/setup", body: `{}`, err: errors.New("disk full"), status: http.StatusInternalServerError},
		{name: "login", method: http.MethodPost, path: "/api/login", body: `{"user":"o","password":"p"}`, status: http.StatusOK},
		{name: "login refused", method: http.MethodPost, path: "/api/login", body: `{}`, err: domain.ErrCredentials, status: http.StatusUnauthorized, code: codeCredentials},
		{name: "login throttled", method: http.MethodPost, path: "/api/login", body: `{}`, err: &owner.ThrottledError{Wait: 4 * time.Second}, status: http.StatusTooManyRequests, code: codeThrottled},
		{name: "login before setup", method: http.MethodPost, path: "/api/login", body: `{}`, err: owner.ErrNoOwner, status: http.StatusConflict, code: codeSetup},
		{name: "settings checked meanwhile", method: http.MethodPost, path: "/api/settings", body: `{"password":"p"}`, err: owner.ErrChanged, status: http.StatusConflict, code: codeChanged},
		{name: "unlock failed", method: http.MethodPost, path: "/api/login", body: `{}`, err: fmt.Errorf("owner: unlock: %w: resume broke", owner.ErrUnlock), status: http.StatusInternalServerError, code: codeUnlock},
		{name: "login bad json", method: http.MethodPost, path: "/api/login", body: `{`, status: http.StatusBadRequest},
		{name: "settings", method: http.MethodPost, path: "/api/settings", body: `{"password":"p"}`, status: http.StatusOK},
		{name: "settings wrong password", method: http.MethodPost, path: "/api/settings", body: `{"password":"p"}`, err: domain.ErrCredentials, status: http.StatusUnauthorized, code: codeCredentials},
		{name: "settings field", method: http.MethodPost, path: "/api/settings", body: `{"password":"p"}`, err: &owner.FieldError{Field: "new_password", Message: "too short"}, status: http.StatusBadRequest, field: "new_password"},
		{name: "logout", method: http.MethodPost, path: "/api/logout", body: `{}`, status: http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			own := readyOwner()
			own.err, own.token = tt.err, "new-session"
			rec := do(newHandlerWith(t, own, func() Service { return nil }), tt.method, tt.path, "127.0.0.1", "application/json", tt.body)
			if rec.Code != tt.status {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tt.status, rec.Body)
			}
			var body errorBody
			_ = json.Unmarshal(rec.Body.Bytes(), &body)
			if body.Code != tt.code || body.Field != tt.field {
				t.Errorf("body = %s, want code %q field %q", rec.Body, tt.code, tt.field)
			}
			if tt.name == "setup internal error hides the cause" && strings.Contains(rec.Body.String(), "disk") {
				t.Error("an internal error leaked to the page")
			}
		})
	}
}

func TestSettingsNullKeepsFields(t *testing.T) {
	own := readyOwner()
	body := `{"password":"current","new_password":null,"claude_token":"","anki_key":null}`
	if rec := do(newHandlerWith(t, own, func() Service { return nil }), http.MethodPost, "/api/settings", "127.0.0.1", "application/json", body); rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	if own.updated.Password != "current" || own.updated.NewPassword != nil || own.updated.AnkiKey != nil {
		t.Errorf("null fields must stay nil: %+v", own.updated)
	}
	if own.updated.ClaudeToken == nil || *own.updated.ClaudeToken != "" {
		t.Errorf("an empty string must replace the token: %+v", own.updated.ClaudeToken)
	}
}

func TestSetupForwardsFields(t *testing.T) {
	own := readyOwner()
	own.token = "new-session"
	body := `{"user":"owner","password":"a long password","claude_token":"sk-ant-oat01-x","anki_key":"k"}`
	do(newHandlerWith(t, own, func() Service { return nil }), http.MethodPost, "/api/setup", "127.0.0.1", "application/json", body)
	want := owner.SetupInput{User: "owner", Password: "a long password", ClaudeToken: "sk-ant-oat01-x", AnkiKey: "k"}
	if own.setup != want {
		t.Errorf("Setup() got %+v, want %+v", own.setup, want)
	}
}

func TestSessionCookie(t *testing.T) {
	own := readyOwner()
	own.token = "new-session"
	h := newHandlerWith(t, own, func() Service { return nil })
	cookieOf := func(rec *httptest.ResponseRecorder) *http.Cookie {
		t.Helper()
		for _, c := range rec.Result().Cookies() {
			if c.Name == sessionCookie {
				return c
			}
		}
		t.Fatalf("no %s cookie in %v", sessionCookie, rec.Header())
		return nil
	}
	login := func(proto string) *http.Cookie {
		t.Helper()
		req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/login", strings.NewReader(`{}`))
		req.Host = "127.0.0.1"
		req.Header.Set("Content-Type", "application/json")
		if proto != "" {
			req.Header.Set("X-Forwarded-Proto", proto)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return cookieOf(rec)
	}

	plain := login("")
	if plain.Value != "new-session" || !plain.HttpOnly || plain.SameSite != http.SameSiteStrictMode || plain.Path != "/" || plain.MaxAge != 30*24*60*60 {
		t.Errorf("cookie = %+v, want HttpOnly, SameSite=Strict, Path=/, 30 days", plain)
	}
	if plain.Secure {
		t.Error("Secure set on plain http, the local page would lose its login")
	}
	if !login("https").Secure {
		t.Error("Secure missing behind an https proxy")
	}
	rec := do(h, http.MethodPost, "/api/logout", "127.0.0.1", "application/json", `{}`)
	if out := cookieOf(rec); out.MaxAge >= 0 || out.Value != "" {
		t.Errorf("logout cookie = %+v, want expired", out)
	}
	if own.logout != sessionValue {
		t.Errorf("Logout() got %q, want the session cookie", own.logout)
	}
}

func TestClientOf(t *testing.T) {
	tests := []struct {
		name       string
		xff        string
		realIP     string
		remoteAddr string
		want       string
	}{
		{name: "last forwarded hop wins", xff: "spoofed, 198.51.100.9", remoteAddr: "127.0.0.1:5000", want: "198.51.100.9"},
		{name: "single forwarded hop", xff: "198.51.100.9", want: "198.51.100.9"},
		{name: "empty last hop falls through", xff: "198.51.100.9, ", realIP: "203.0.113.4", want: "203.0.113.4"},
		{name: "real ip", realIP: "203.0.113.4", remoteAddr: "127.0.0.1:5000", want: "203.0.113.4"},
		{name: "remote host without port", remoteAddr: "127.0.0.1", want: "127.0.0.1"},
		{name: "remote address", remoteAddr: "127.0.0.1:5000", want: "127.0.0.1"},
		{name: "long value is cut", xff: strings.Repeat("x", 500), want: strings.Repeat("x", maxClientKey)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/login", nil)
			if tt.xff != "" {
				r.Header.Set("X-Forwarded-For", tt.xff)
			}
			if tt.realIP != "" {
				r.Header.Set("X-Real-IP", tt.realIP)
			}
			r.RemoteAddr = tt.remoteAddr
			if got := clientOf(r); got != tt.want {
				t.Errorf("clientOf() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLoginPassesTheClient(t *testing.T) {
	own := readyOwner()
	own.token = "new-session"
	h := newHandlerWith(t, own, func() Service { return nil })
	r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/login", strings.NewReader(`{"user":"o","password":"p"}`))
	r.Host = "127.0.0.1"
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Forwarded-For", "198.51.100.9")
	h.ServeHTTP(httptest.NewRecorder(), r)
	if own.client != "198.51.100.9" {
		t.Errorf("Login() client = %q, want the forwarded address", own.client)
	}
}
