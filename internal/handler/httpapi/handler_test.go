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

	"github.com/rs/zerolog"

	"github.com/scv3lqq/anki-review/internal/domain"
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

func newHandler(t *testing.T, svc Service) http.Handler {
	t.Helper()
	h, err := New(svc, static, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func do(h http.Handler, method, target, host, contentType, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(context.Background(), method, target, strings.NewReader(body))
	req.Host = host
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
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
