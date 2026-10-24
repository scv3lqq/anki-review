// Package httpapi serves the review page and its JSON API.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"regexp"
	"strings"

	"github.com/rs/zerolog"

	"github.com/scv3lqq/anki-review/internal/domain"
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

const (
	maxBody         = 1 << 20
	codeGradeFailed = "grading_failed"
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
	svc    Service
	log    zerolog.Logger
	assets map[string]asset
}

// New returns the routes behind the host guard. static must hold index.html, app.js and style.css.
func New(svc Service, static fs.FS, log zerolog.Logger) (http.Handler, error) {
	h := &handler{svc: svc, log: log, assets: make(map[string]asset, len(assetNames))}
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
	mux.HandleFunc("GET /api/decks", h.decks)
	mux.HandleFunc("GET /api/cards", h.cards)
	mux.HandleFunc("GET /api/results", h.results)
	mux.HandleFunc("GET /api/status", h.status)
	mux.HandleFunc("POST /api/start", h.start)
	mux.HandleFunc("POST /api/more", h.more)
	mux.HandleFunc("POST /api/answers", h.answers)
	mux.HandleFunc("POST /api/regrade", h.regrade)
	mux.HandleFunc("POST /api/confirm", h.confirm)
	mux.HandleFunc("POST /api/reset", h.reset)
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

func (h *handler) decks(w http.ResponseWriter, r *http.Request) {
	decks, err := h.svc.Decks(r.Context())
	if err != nil {
		h.fail(w, err)
		return
	}
	if decks == nil {
		decks = []domain.Deck{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"decks": decks})
}

func (h *handler) cards(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, h.svc.Cards())
}

func (h *handler) results(w http.ResponseWriter, _ *http.Request) {
	res, err := h.svc.Results()
	if err != nil {
		h.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (h *handler) status(w http.ResponseWriter, _ *http.Request) {
	st := h.svc.Status()
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

func (h *handler) start(w http.ResponseWriter, r *http.Request) {
	var req countRequest
	if decode(w, r, &req) {
		h.reply(w, h.svc.Start(r.Context(), req.Deck, req.Count))
	}
}

func (h *handler) more(w http.ResponseWriter, r *http.Request) {
	var req countRequest
	if decode(w, r, &req) {
		h.reply(w, h.svc.More(r.Context(), req.Session, req.Count))
	}
}

func (h *handler) answers(w http.ResponseWriter, r *http.Request) {
	var req answersRequest
	if !decode(w, r, &req) {
		return
	}
	byN := make(map[int]string, len(req.Answers))
	for _, a := range req.Answers {
		byN[a.N] = a.Answer
	}
	h.reply(w, h.svc.SubmitAnswers(req.Session, byN))
}

func (h *handler) regrade(w http.ResponseWriter, r *http.Request) {
	var req sessionRequest
	if decode(w, r, &req) {
		h.reply(w, h.svc.Regrade(req.Session))
	}
}

func (h *handler) confirm(w http.ResponseWriter, r *http.Request) {
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
	h.reply(w, h.svc.Confirm(req.Session, byN))
}

func (h *handler) reset(w http.ResponseWriter, r *http.Request) {
	var req sessionRequest
	if decode(w, r, &req) {
		h.reply(w, h.svc.Reset(req.Session))
	}
}
