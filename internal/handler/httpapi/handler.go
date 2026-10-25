// Package httpapi serves the review page and its JSON API.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"path"
	"regexp"
	"strings"

	"github.com/rs/zerolog"

	"github.com/scv3lqq/anki-review/internal/domain"
	"github.com/scv3lqq/anki-review/internal/usecase/owner"
	"github.com/scv3lqq/anki-review/internal/usecase/review"
)

// Service is the review use case as the handlers need it.
type Service interface {
	Decks(ctx context.Context) ([]domain.Deck, error)
	Cards() review.CardsView
	Results() (review.ResultsView, error)
	Status() *domain.Status
	Start(ctx context.Context, deck string, count int) error
	More(ctx context.Context, session string, count int) error
	SubmitAnswers(session string, answers map[int]string) error
	Regrade(session string) error
	Confirm(session string, grades map[int]domain.Grade) error
	Reset(session string) error
}

// Owner is the owner use case as the handlers need it.
type Owner interface {
	Status(token string) owner.Status
	Authorized(token string) bool
	Setup(ctx context.Context, client string, in owner.SetupInput) (string, error)
	Login(ctx context.Context, client, user, password string) (string, error)
	Logout(token string)
	Update(ctx context.Context, token string, in owner.UpdateInput) error
}

// reviewRoute is a route that needs the review service, which exists only while the server is unlocked.
type reviewRoute func(w http.ResponseWriter, r *http.Request, svc Service)

const (
	maxBody         = 1 << 20
	maxClientKey    = 64
	codeGradeFailed = "grading_failed"
	codeLogin       = "login"
	codeCredentials = "credentials"
	codeThrottled   = "throttled"
	codeSetupDone   = "setup_done"
	codeSetup       = "setup"
	codeChanged     = "changed"
	codeUnlock      = "unlock_failed"
	// the page shows this when the password was right but the saved pass could not be loaded
	msgUnlock     = "Logged in, but the server could not load its saved pass: see its log"
	sessionCookie = "anki_review_session"
)

var (
	csp = strings.Join([]string{
		"default-src 'none'",
		"script-src 'self'",
		"style-src 'self' https://fonts.googleapis.com",
		"font-src https://fonts.gstatic.com",
		"connect-src 'self'",
		"base-uri 'none'",
		"form-action 'none'",
		"frame-ancestors 'none'",
	}, "; ")
	// DNS rebinding guard: a foreign page resolved to 127.0.0.1 must not read the answer keys.
	// A reverse proxy in front has to pass Host 127.0.0.1:<port>.
	reLocalHost = regexp.MustCompile(`^(127\.0\.0\.1|localhost)(:\d+)?$`)
	assetNames  = []string{"index.html", "app.js", "style.css"}
)

type asset struct {
	body        []byte
	contentType string
}

type handler struct {
	own    Owner
	review func() Service
	log    zerolog.Logger
	assets map[string]asset
}

// New returns the routes behind the host guard. static must hold index.html, app.js and style.css.
// review returns the review service while the server is unlocked and nil while it is locked.
func New(own Owner, review func() Service, static fs.FS, log zerolog.Logger) (http.Handler, error) {
	h := &handler{own: own, review: review, log: log, assets: make(map[string]asset, len(assetNames))}
	for _, name := range assetNames {
		body, err := fs.ReadFile(static, name)
		if err != nil {
			return nil, fmt.Errorf("httpapi: %w", err)
		}
		h.assets[name] = asset{body: body, contentType: mime.TypeByExtension(path.Ext(name))}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", h.serveAsset("index.html"))
	mux.HandleFunc("GET /app.js", h.serveAsset("app.js"))
	mux.HandleFunc("GET /style.css", h.serveAsset("style.css"))
	mux.HandleFunc("GET /api/session", h.session)
	mux.HandleFunc("POST /api/setup", h.setup)
	mux.HandleFunc("POST /api/login", h.login)
	mux.HandleFunc("POST /api/logout", h.authed(h.logout))
	mux.HandleFunc("POST /api/settings", h.authed(h.settings))
	mux.HandleFunc("GET /api/decks", h.reviewing(h.decks))
	mux.HandleFunc("GET /api/cards", h.reviewing(h.cards))
	mux.HandleFunc("GET /api/results", h.reviewing(h.results))
	mux.HandleFunc("GET /api/status", h.reviewing(h.status))
	mux.HandleFunc("POST /api/start", h.reviewing(h.start))
	mux.HandleFunc("POST /api/more", h.reviewing(h.more))
	mux.HandleFunc("POST /api/answers", h.reviewing(h.answers))
	mux.HandleFunc("POST /api/regrade", h.reviewing(h.regrade))
	mux.HandleFunc("POST /api/confirm", h.reviewing(h.confirm))
	mux.HandleFunc("POST /api/reset", h.reviewing(h.reset))
	return guard(mux), nil
}

func guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hdr := w.Header()
		hdr.Set("Content-Security-Policy", csp)
		hdr.Set("X-Content-Type-Options", "nosniff")
		hdr.Set("Referrer-Policy", "no-referrer")
		hdr.Set("Cache-Control", "no-store")
		if !reLocalHost.MatchString(r.Host) {
			writeJSON(w, http.StatusForbidden, errorBody{Error: "host"})
			return
		}
		// a JSON body forces a CORS preflight this server never answers, so other sites can't post here
		if r.Method == http.MethodPost && !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			writeJSON(w, http.StatusUnsupportedMediaType, errorBody{Error: "json only"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

type errorBody struct {
	Error string `json:"error"`
	Code  string `json:"code,omitempty"`
	Field string `json:"field,omitempty"`
}

var ok = map[string]bool{"ok": true}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (h *handler) fail(w http.ResponseWriter, err error) {
	var conflict *review.ConflictError
	var grading *review.GradingFailedError
	switch {
	case errors.As(err, &conflict):
		writeJSON(w, http.StatusConflict, errorBody{Error: conflict.Message, Code: conflict.Code})
	case errors.As(err, &grading):
		writeJSON(w, http.StatusConflict, errorBody{Error: grading.Reason, Code: codeGradeFailed})
	case errors.Is(err, review.ErrNotReady):
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, review.ErrUnknownDeck):
		writeJSON(w, http.StatusBadRequest, errorBody{Error: err.Error()})
	case errors.Is(err, domain.ErrAnki):
		writeJSON(w, http.StatusBadGateway, errorBody{Error: err.Error()})
	default:
		h.log.Error().Err(err).Msg("request failed")
		writeJSON(w, http.StatusInternalServerError, errorBody{Error: "internal error"})
	}
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody)).Decode(v); err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody{Error: "bad json"})
		return false
	}
	return true
}

func (h *handler) serveAsset(name string) http.HandlerFunc {
	a := h.assets[name]
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", a.contentType)
		_, _ = w.Write(a.body)
	}
}

func (h *handler) decks(w http.ResponseWriter, r *http.Request, svc Service) {
	decks, err := svc.Decks(r.Context())
	if err != nil {
		h.fail(w, err)
		return
	}
	if decks == nil {
		decks = []domain.Deck{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"decks": decks})
}

func (h *handler) cards(w http.ResponseWriter, _ *http.Request, svc Service) {
	writeJSON(w, http.StatusOK, svc.Cards())
}

func (h *handler) results(w http.ResponseWriter, _ *http.Request, svc Service) {
	res, err := svc.Results()
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (h *handler) status(w http.ResponseWriter, _ *http.Request, svc Service) {
	st := svc.Status()
	if st == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

type sessionRequest struct {
	Session string `json:"session"`
}

type countRequest struct {
	Session string `json:"session"`
	Deck    string `json:"deck"`
	Count   int    `json:"count"`
}

type answersRequest struct {
	Session string `json:"session"`
	Answers []struct {
		N      int    `json:"n"`
		Answer string `json:"answer"`
	} `json:"answers"`
}

type confirmRequest struct {
	Session string `json:"session"`
	Grades  []struct {
		N     int    `json:"n"`
		Grade string `json:"grade"`
	} `json:"grades"`
}

func (h *handler) reply(w http.ResponseWriter, err error) {
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ok)
}

func (h *handler) start(w http.ResponseWriter, r *http.Request, svc Service) {
	var req countRequest
	if decode(w, r, &req) {
		h.reply(w, svc.Start(r.Context(), req.Deck, req.Count))
	}
}

func (h *handler) more(w http.ResponseWriter, r *http.Request, svc Service) {
	var req countRequest
	if decode(w, r, &req) {
		h.reply(w, svc.More(r.Context(), req.Session, req.Count))
	}
}

func (h *handler) answers(w http.ResponseWriter, r *http.Request, svc Service) {
	var req answersRequest
	if !decode(w, r, &req) {
		return
	}
	byN := make(map[int]string, len(req.Answers))
	for _, a := range req.Answers {
		byN[a.N] = a.Answer
	}
	h.reply(w, svc.SubmitAnswers(req.Session, byN))
}

func (h *handler) regrade(w http.ResponseWriter, r *http.Request, svc Service) {
	var req sessionRequest
	if decode(w, r, &req) {
		h.reply(w, svc.Regrade(req.Session))
	}
}

func (h *handler) confirm(w http.ResponseWriter, r *http.Request, svc Service) {
	var req confirmRequest
	if !decode(w, r, &req) {
		return
	}
	// a grade the page got wrong is dropped, so the card keeps Claude's
	byN := make(map[int]domain.Grade, len(req.Grades))
	for _, g := range req.Grades {
		if grade, ok := domain.ParseGrade(g.Grade); ok {
			byN[g.N] = grade
		}
	}
	h.reply(w, svc.Confirm(req.Session, byN))
}

func (h *handler) reset(w http.ResponseWriter, r *http.Request, svc Service) {
	var req sessionRequest
	if decode(w, r, &req) {
		h.reply(w, svc.Reset(req.Session))
	}
}

// authed lets through requests with a live session; any other request gets the login prompt.
func (h *handler) authed(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !h.own.Authorized(sessionToken(r)) {
			writeJSON(w, http.StatusUnauthorized, errorBody{Error: "Log in first", Code: codeLogin})
			return
		}
		next(w, r)
	}
}

// reviewing is authed, plus the review service, which exists only while the server is unlocked.
func (h *handler) reviewing(next reviewRoute) http.HandlerFunc {
	return h.authed(func(w http.ResponseWriter, r *http.Request) {
		svc := h.review()
		if svc == nil {
			writeJSON(w, http.StatusUnauthorized, errorBody{Error: "Log in first", Code: codeLogin})
			return
		}
		next(w, r, svc)
	})
}

// clientOf names the caller for the login throttle. The port is loopback-only, so the last
// X-Forwarded-For hop is the one the local proxy appended: a client can't put its own value there.
// Long values are cut: the throttle keeps one entry per client.
func clientOf(r *http.Request) string {
	client := ""
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		last := xff
		if i := strings.LastIndex(xff, ","); i >= 0 {
			last = xff[i+1:]
		}
		client = strings.TrimSpace(last)
	}
	if client == "" {
		client = strings.TrimSpace(r.Header.Get("X-Real-IP"))
	}
	if client == "" {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = r.RemoteAddr
		}
		client = host
	}
	if len(client) > maxClientKey {
		client = client[:maxClientKey]
	}
	return client
}

func sessionToken(r *http.Request) string {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return ""
	}
	return c.Value
}

// setSessionCookie sets or expires the login cookie. Secure only behind an https proxy: the port is
// loopback-only, so only the local proxy can send X-Forwarded-Proto.
func setSessionCookie(w http.ResponseWriter, r *http.Request, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // G124: Secure is the https check above, not a missing flag
		Name:     sessionCookie,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https"),
		SameSite: http.SameSiteStrictMode,
	})
}

// ownerFail maps the owner use case's errors to the contract's codes.
func (h *handler) ownerFail(w http.ResponseWriter, err error) {
	var field *owner.FieldError
	var throttled *owner.ThrottledError
	switch {
	case errors.As(err, &field):
		writeJSON(w, http.StatusBadRequest, errorBody{Error: field.Message, Field: field.Field})
	case errors.As(err, &throttled):
		writeJSON(w, http.StatusTooManyRequests, errorBody{Error: throttled.Error(), Code: codeThrottled})
	case errors.Is(err, owner.ErrSetupDone):
		writeJSON(w, http.StatusConflict, errorBody{Error: "Setup is already done: log in instead", Code: codeSetupDone})
	case errors.Is(err, owner.ErrNoOwner):
		writeJSON(w, http.StatusConflict, errorBody{Error: "Nothing is set up yet: set up first", Code: codeSetup})
	case errors.Is(err, domain.ErrCredentials):
		writeJSON(w, http.StatusUnauthorized, errorBody{Error: "Wrong login or password", Code: codeCredentials})
	case errors.Is(err, owner.ErrChanged):
		writeJSON(w, http.StatusConflict, errorBody{Error: owner.ErrChanged.Error(), Code: codeChanged})
	case errors.Is(err, owner.ErrUnlock):
		// the owner logged the cause
		writeJSON(w, http.StatusInternalServerError, errorBody{Error: msgUnlock, Code: codeUnlock})
	default:
		h.log.Error().Err(err).Msg("owner request failed")
		writeJSON(w, http.StatusInternalServerError, errorBody{Error: "internal error"})
	}
}

func (h *handler) session(w http.ResponseWriter, r *http.Request) {
	st := h.own.Status(sessionToken(r))
	body := map[string]any{"state": st.State, "unlocked": st.Unlocked}
	if st.State == owner.StateReady {
		body["user"] = st.User
	}
	if st.Unlocked {
		if svc := h.review(); svc != nil {
			body["stage"] = svc.Cards().Stage
		}
	}
	writeJSON(w, http.StatusOK, body)
}

type setupRequest struct {
	User        string `json:"user"`
	Password    string `json:"password"`
	ClaudeToken string `json:"claude_token"`
	AnkiKey     string `json:"anki_key"`
}

func (h *handler) setup(w http.ResponseWriter, r *http.Request) {
	var req setupRequest
	if !decode(w, r, &req) {
		return
	}
	token, err := h.own.Setup(r.Context(), clientOf(r), owner.SetupInput{
		User: req.User, Password: req.Password, ClaudeToken: req.ClaudeToken, AnkiKey: req.AnkiKey,
	})
	h.signIn(w, r, token, err)
}

type loginRequest struct {
	User     string `json:"user"`
	Password string `json:"password"`
}

func (h *handler) login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if !decode(w, r, &req) {
		return
	}
	token, err := h.own.Login(r.Context(), clientOf(r), req.User, req.Password)
	h.signIn(w, r, token, err)
}

// signIn answers setup and login: the session cookie on success, the owner's error otherwise.
func (h *handler) signIn(w http.ResponseWriter, r *http.Request, token string, err error) {
	if err != nil {
		h.ownerFail(w, err)
		return
	}
	setSessionCookie(w, r, token, int(owner.SessionTTL.Seconds()))
	writeJSON(w, http.StatusOK, ok)
}

func (h *handler) logout(w http.ResponseWriter, r *http.Request) {
	h.own.Logout(sessionToken(r))
	setSessionCookie(w, r, "", -1)
	writeJSON(w, http.StatusOK, ok)
}

type settingsRequest struct {
	Password    string  `json:"password"`
	NewPassword *string `json:"new_password"`
	ClaudeToken *string `json:"claude_token"`
	AnkiKey     *string `json:"anki_key"`
}

func (h *handler) settings(w http.ResponseWriter, r *http.Request) {
	var req settingsRequest
	if !decode(w, r, &req) {
		return
	}
	err := h.own.Update(r.Context(), sessionToken(r), owner.UpdateInput{
		Password: req.Password, NewPassword: req.NewPassword, ClaudeToken: req.ClaudeToken, AnkiKey: req.AnkiKey,
	})
	if err != nil {
		h.ownerFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ok)
}
