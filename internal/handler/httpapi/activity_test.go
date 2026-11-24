package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/scv3lqq/anki-review/internal/domain"
)

type activityStub struct {
	collectionStub
	calls int
}

func (c *activityStub) Activity(_ context.Context, _ time.Time) ([]domain.ActivityDay, error) {
	c.calls++
	return []domain.ActivityDay{{Date: "2026-01-01"}}, nil
}
func TestActivityAuthenticationAndAccountFence(t *testing.T) {
	c := &activityStub{}
	h, err := NewWithOptions(readyOwner(), func() Service { return &stub{} }, static, zerolog.Nop(), Options{AccountID: "b", Collection: c})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		cookie, account string
		want            int
	}{{"", "", 401}, {sessionValue, "a", 409}, {sessionValue, "b", 200}} {
		r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/activity?timezone=UTC", nil)
		r.Host = "localhost"
		if tc.cookie != "" {
			r.AddCookie(&http.Cookie{Name: sessionCookie, Value: tc.cookie})
		}
		r.Header.Set("X-Anki-Account", tc.account)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatalf("got %d want %d", w.Code, tc.want)
		}
	}
	if c.calls != 1 {
		t.Fatalf("calls %d", c.calls)
	}
}

func TestActivityTimezoneValidation(t *testing.T) {
	c := &activityStub{}
	h, err := NewWithOptions(readyOwner(), func() Service { return &stub{} }, static, zerolog.Nop(), Options{AccountID: "owner", Collection: c})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		query string
		want  int
	}{{"", 200}, {"?timezone=America%2FNew_York", 200}, {"?timezone=Local", 400}, {"?timezone=bad%2Fzone", 400}} {
		r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/activity"+tc.query, nil)
		r.Host = "localhost"
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: sessionValue})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatalf("%s: got %d want %d", tc.query, w.Code, tc.want)
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("missing no-store")
		}
	}
}
