package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/rs/zerolog"

	"github.com/scv3lqq/anki-review/internal/usecase/accounts"
	"github.com/scv3lqq/anki-review/internal/usecase/owner"
	"github.com/scv3lqq/anki-review/internal/usecase/review"
)

// Directory resolves only authenticated cookies to their immutable account handler.
type Directory interface {
	Resolve(string) (http.Handler, bool)
	PublicState() string
	Setup(context.Context, string, owner.SetupInput) (string, error)
	Login(context.Context, string, string, string) (string, error)
	Invite(string) (accounts.InviteResult, error)
	Register(context.Context, string, string, owner.SetupInput) (string, error)
}

// NewMulti keeps global authentication separate from account-local API operations.
func NewMulti(directory Directory, root http.Handler) http.Handler {
	return guard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			switch r.URL.Path {
			case "/api/setup", "/api/register":
				var req struct {
					setupRequest
					Invitation string  `json:"invitation"`
					AnkiURL    *string `json:"anki_url"`
				}
				if !decode(w, r, &req) {
					return
				}
				in := owner.SetupInput{User: req.User, Password: req.Password, GraderProvider: req.GraderProvider, ClaudeToken: req.ClaudeToken, CodexAuth: req.CodexAuth, AnkiKey: req.AnkiKey}
				var token string
				var err error
				if r.URL.Path == "/api/register" {
					if req.AnkiKey != "" || req.AnkiURL != nil {
						writeJSON(w, 400, errorBody{Error: "Collection connection settings are managed by the server"})
						return
					}
					token, err = directory.Register(r.Context(), clientOf(r), req.Invitation, in)
				} else {
					token, err = directory.Setup(r.Context(), clientOf(r), in)
				}
				multiSignIn(w, r, token, err, r.URL.Path == "/api/register")
				return
			case "/api/login":
				var req loginRequest
				if !decode(w, r, &req) {
					return
				}
				token, err := directory.Login(r.Context(), clientOf(r), req.User, req.Password)
				multiSignIn(w, r, token, err, false)
				return
			case "/api/invitations":
				account, valid := directory.Resolve(sessionToken(r))
				if !valid {
					writeJSON(w, 401, errorBody{Error: "Log in first", Code: codeLogin})
					return
				}
				// The child validates cross-tab expectations before the manager checks the owner role.
				probe := r.Clone(r.Context())
				probe.Method = http.MethodGet
				target := *r.URL
				target.Path = "/api/session"
				target.RawQuery = ""
				probe.URL = &target
				capture := &identityResponse{header: make(http.Header)}
				account.ServeHTTP(capture, probe)
				w.Header().Set("X-Anki-Account", capture.header.Get("X-Anki-Account"))
				if capture.code != 200 {
					w.WriteHeader(capture.code)
					_, _ = w.Write(capture.body)
					return
				}
				invite, err := directory.Invite(sessionToken(r))
				if err != nil {
					multiFail(w, err, false)
					return
				}
				writeJSON(w, 200, invite)
				return
			}
		}
		if r.URL.Path == "/api/session" {
			if account, valid := directory.Resolve(sessionToken(r)); valid {
				account.ServeHTTP(w, r)
				return
			}
			writeJSON(w, 200, map[string]any{"state": directory.PublicState(), "multi_account": true, "unlocked": false})
			return
		}
		if len(r.URL.Path) >= 5 && r.URL.Path[:5] == "/api/" {
			if account, valid := directory.Resolve(sessionToken(r)); valid {
				account.ServeHTTP(w, r)
				return
			}
			writeJSON(w, 401, errorBody{Error: "Log in first", Code: codeLogin})
			return
		}
		root.ServeHTTP(w, r)
	}))
}

func multiSignIn(w http.ResponseWriter, r *http.Request, token string, err error, managed bool) {
	if err != nil {
		multiFail(w, err, managed)
		return
	}
	setSessionCookie(w, r, token, int(owner.SessionTTL.Seconds()))
	writeJSON(w, 200, ok)
}

func multiFail(w http.ResponseWriter, err error, managed bool) {
	var field *owner.FieldError
	switch {
	case errors.As(err, &field):
		if managed && field.Field == "anki_key" {
			writeJSON(w, 503, errorBody{Error: collectionUnavailable})
			return
		}
		writeJSON(w, 400, errorBody{Error: field.Message, Field: field.Field})
	case errors.Is(err, accounts.ErrForbidden):
		writeJSON(w, 403, errorBody{Error: "Owner authentication is required"})
	case errors.Is(err, accounts.ErrInvitation):
		writeJSON(w, 400, errorBody{Error: "Invitation is invalid or expired", Code: "invitation"})
	case errors.Is(err, accounts.ErrCapacity):
		writeJSON(w, 409, errorBody{Error: "Account limit reached", Code: "capacity"})
	case errors.Is(err, accounts.ErrBusy):
		writeJSON(w, 409, errorBody{Error: "Account operation in progress", Code: "busy"})
	case errors.Is(err, accounts.ErrUnavailable):
		writeJSON(w, 503, errorBody{Error: "Your account is temporarily unavailable. Try again."})
	default:
		(&handler{log: zerolog.Nop(), opts: Options{Managed: managed}}).ownerFail(w, err)
	}
}

type identityResponse struct {
	header http.Header
	code   int
	body   []byte
}

func (r *identityResponse) Header() http.Header  { return r.header }
func (r *identityResponse) WriteHeader(code int) { r.code = code }
func (r *identityResponse) Write(body []byte) (int, error) {
	if r.code == 0 {
		r.code = 200
	}
	r.body = append(r.body, body...)
	return len(body), nil
}

func collectionPublicError(w http.ResponseWriter, err error) bool {
	var conflict *review.ConflictError
	var field *owner.FieldError
	if errors.As(err, &conflict) {
		writeJSON(w, 409, errorBody{Error: conflict.Message, Code: conflict.Code})
		return true
	}
	if errors.As(err, &field) {
		writeJSON(w, 400, errorBody{Error: field.Message, Field: field.Field})
		return true
	}
	return false
}
