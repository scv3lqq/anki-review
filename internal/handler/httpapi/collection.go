package httpapi

import (
	"context"
	"html"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"

	"github.com/scv3lqq/anki-review/internal/repository/apkg"
	"github.com/scv3lqq/anki-review/internal/repository/cardrender"
)

const collectionUnavailable = "Your collection is temporarily unavailable. Try again."
const documentCSP = "sandbox allow-same-origin; default-src 'none'; script-src 'none'; connect-src 'none'; style-src 'unsafe-inline'; img-src 'self'; media-src 'self'; font-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'self'"

// Collection is this account's serialized collection and preferences adapter.
type Collection interface {
	Import(context.Context, []byte) error
	Media(context.Context, string) ([]byte, error)
	Preferences() ([]string, error)
	SavePreferences(context.Context, []string) error
}

// Options are immutable account metadata. Account expectation headers never select an account.
type Options struct {
	AccountID                      string
	MultiAccount, Managed, IsOwner bool
	Collection                     Collection
}

func (h *handler) expectedAccount(w http.ResponseWriter, r *http.Request) bool {
	if h.opts.AccountID != "" {
		w.Header().Set("X-Anki-Account", h.opts.AccountID)
	}
	if expected := r.Header.Get("X-Anki-Account"); expected != "" && expected != h.opts.AccountID {
		writeJSON(w, http.StatusConflict, errorBody{Error: "The signed-in account changed. Reload the page.", Code: "account_changed"})
		return false
	}
	return true
}

func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	switch r.Header.Get("Sec-Fetch-Site") {
	case "same-origin":
		return true
	case "cross-site", "same-site":
		return false
	}
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	host := r.Host
	if forwarded := r.Header.Get("X-Forwarded-Host"); forwarded != "" {
		host = forwarded
	}
	return u.Scheme == scheme && strings.EqualFold(u.Host, host)
}

func (h *handler) importPackage(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Content-Type") != "application/octet-stream" {
		writeJSON(w, 415, errorBody{Error: "application/octet-stream is required"})
		return
	}
	if r.Header.Get("X-Anki-Import") != "1" || !sameOrigin(r) {
		writeJSON(w, 403, errorBody{Error: "Import requires a same-origin request"})
		return
	}
	if h.opts.Collection == nil {
		writeJSON(w, 503, errorBody{Error: collectionUnavailable})
		return
	}
	select {
	case h.importLease <- struct{}{}:
		defer func() { <-h.importLease }()
	default:
		writeJSON(w, http.StatusConflict, errorBody{Error: "An import is already in progress", Code: "busy"})
		return
	}
	if r.ContentLength > apkg.MaxBytes {
		writeJSON(w, 413, errorBody{Error: "Package exceeds 8 MiB"})
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, apkg.MaxBytes))
	if err != nil {
		writeJSON(w, 413, errorBody{Error: "Package exceeds 8 MiB"})
		return
	}
	if err := h.opts.Collection.Import(r.Context(), raw); err != nil {
		h.collectionFail(w, err)
		return
	}
	writeJSON(w, 200, ok)
}

func (h *handler) preferences(w http.ResponseWriter, _ *http.Request) {
	if h.opts.Collection == nil {
		writeJSON(w, 503, errorBody{Error: collectionUnavailable})
		return
	}
	decks, err := h.opts.Collection.Preferences()
	if err != nil {
		h.collectionFail(w, err)
		return
	}
	if decks == nil {
		decks = []string{}
	}
	writeJSON(w, 200, map[string]any{"knowledge_decks": decks})
}

func (h *handler) savePreferences(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Decks *[]string `json:"knowledge_decks"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.Decks == nil {
		writeJSON(w, 400, errorBody{Error: "knowledge_decks is required", Field: "knowledge_decks"})
		return
	}
	if h.opts.Collection == nil {
		writeJSON(w, 503, errorBody{Error: collectionUnavailable})
		return
	}
	if err := h.opts.Collection.SavePreferences(r.Context(), *req.Decks); err != nil {
		h.collectionFail(w, err)
		return
	}
	writeJSON(w, 200, ok)
}

func (h *handler) collectionFail(w http.ResponseWriter, err error) {
	// Pass conflicts and validated field errors retain their public meaning; adapter errors do not.
	if collectionPublicError(w, err) {
		return
	}
	writeJSON(w, 503, errorBody{Error: collectionUnavailable})
}

func (h *handler) render(w http.ResponseWriter, r *http.Request, svc Service) {
	query := r.URL.Query()
	side := query.Get("side")
	if side != "question" && side != "back" {
		writeJSON(w, 400, errorBody{Error: "side must be question or back"})
		return
	}
	n, err := strconv.Atoi(query.Get("n"))
	if err != nil || n < 1 {
		writeJSON(w, 400, errorBody{Error: "Invalid card number"})
		return
	}
	cards := svc.Cards()
	if query.Get("session") == "" || cards.Session != query.Get("session") {
		writeJSON(w, 409, errorBody{Error: "This pass changed. Reload the page.", Code: "session"})
		return
	}
	raw := ""
	found := false
	if side == "question" {
		for _, card := range cards.Cards {
			if card.N == n {
				raw = card.QuestionHTML
				if raw == "" {
					raw = "<pre>" + html.EscapeString(card.Question) + "</pre>"
				}
				found = true
				break
			}
		}
	} else {
		results, err := svc.Results()
		if err != nil || results.Session != cards.Session {
			writeJSON(w, 409, errorBody{Error: "The answer is not available yet", Code: "busy"})
			return
		}
		for _, item := range results.Items {
			if item.N == n {
				if item.Skipped || item.Grade == "" {
					writeJSON(w, 409, errorBody{Error: "The answer is not available", Code: "busy"})
					return
				}
				raw = item.BackHTML
				if raw == "" {
					raw = "<pre>" + html.EscapeString(item.Back) + "</pre>"
				}
				found = true
				break
			}
		}
	}
	if !found {
		writeJSON(w, 404, errorBody{Error: "Card not found"})
		return
	}
	w.Header().Set("Content-Security-Policy", documentCSP)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, cardrender.Document(raw))
}

func (h *handler) media(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !cardrender.MediaName(name) {
		writeJSON(w, 400, errorBody{Error: "Invalid media filename"})
		return
	}
	if h.opts.Collection == nil {
		writeJSON(w, 503, errorBody{Error: collectionUnavailable})
		return
	}
	raw, err := h.opts.Collection.Media(r.Context(), name)
	if err != nil {
		writeJSON(w, 404, errorBody{Error: "Media not found"})
		return
	}
	w.Header().Set("Content-Security-Policy", documentCSP)
	w.Header().Set("Cache-Control", "private, no-store")
	typeName := mime.TypeByExtension(strings.ToLower(path.Ext(name)))
	safe := strings.HasPrefix(typeName, "image/") || strings.HasPrefix(typeName, "audio/") || strings.HasPrefix(typeName, "video/") || strings.HasPrefix(typeName, "font/") || strings.HasPrefix(typeName, "text/css") || strings.HasPrefix(typeName, "application/font") || strings.HasPrefix(typeName, "application/vnd.ms-fontobject")
	if !safe {
		typeName = "application/octet-stream"
		w.Header().Set("Content-Disposition", "attachment")
	}
	w.Header().Set("Content-Type", typeName)
	_, _ = w.Write(raw)
}
