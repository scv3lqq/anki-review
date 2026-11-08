package httpapi

import (
	"errors"
	"net/http"
	"testing"

	"github.com/scv3lqq/anki-review/internal/usecase/review"
)

type resumeStub struct {
	stub
	calls int
}

func (s *resumeStub) Resume(session string) error { s.calls++; s.session = session; return s.err }

func TestResumeRoute(t *testing.T) {
	for _, tc := range []struct {
		name, token, body string
		err               error
		code, calls       int
	}{
		{"success", sessionValue, `{"session":"saved-pass"}`, nil, 200, 1},
		{"anonymous", "", `{"session":"saved-pass"}`, nil, 401, 0},
		{"bad JSON", sessionValue, `{`, nil, 400, 0},
		{"conflict", sessionValue, `{"session":"saved-pass"}`, &review.ConflictError{Code: "session", Message: "wrong pass"}, 409, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &resumeStub{stub: stub{err: tc.err}}
			got := send(newHandler(t, svc), tc.token, http.MethodPost, "/api/resume", "localhost", "application/json", tc.body)
			if got.Code != tc.code || svc.calls != tc.calls {
				t.Fatalf("code=%d calls=%d body=%s", got.Code, svc.calls, got.Body)
			}
			if tc.calls > 0 && svc.session != "saved-pass" {
				t.Fatal("pass identifier lost")
			}
		})
	}
}

func TestPersistentLogoutFailureKeepsCookie(t *testing.T) {
	own := readyOwner()
	own.err = errors.New("durable revocation failed")
	got := do(newHandlerWith(t, own, func() Service { return &stub{} }), http.MethodPost, "/api/logout", "localhost", "application/json", `{}`)
	if got.Code != 500 || len(got.Result().Cookies()) != 0 {
		t.Fatalf("failed revocation claimed success: %d %v", got.Code, got.Result().Cookies())
	}
}
