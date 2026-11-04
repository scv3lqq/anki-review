// Package fakeanki is an in-memory AnkiConnect server for end-to-end tests. A card is due until it
// is answered; there are no learning steps.
package fakeanki

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// Card is a Basic card.
type Card struct {
	ID       int64
	Deck     string
	Question string
	Answer   string
	New      bool
	Reps     int
	Mod      int64
	CSS      string
	answered bool
}

// Answer is one answerCards entry Anki received.
type Answer struct {
	CardID int64 `json:"cardId"`
	Ease   int   `json:"ease"`
}

// Anki serves AnkiConnect on a local port.
type Anki struct {
	mu            sync.Mutex
	cards         []*Card
	answered      []Answer
	syncs         int
	key           string
	srv           *httptest.Server
	media         map[string][]byte
	importFailure bool
}

var (
	reDeck   = regexp.MustCompile(`"deck:((?:[^"\\]|\\.)*)"`)
	reEscape = regexp.MustCompile(`\\(.)`)
)

// New starts the server; it stops with the test.
func New(t *testing.T, cards ...Card) *Anki {
	t.Helper()
	a := &Anki{}
	for i := range cards {
		a.cards = append(a.cards, &cards[i])
	}
	a.srv = httptest.NewServer(http.HandlerFunc(a.serve))
	t.Cleanup(a.srv.Close)
	return a
}

// URL of the AnkiConnect endpoint.
func (a *Anki) URL() string { return a.srv.URL }

// RequireKey makes every request without this apiKey fail, as AnkiConnect does when apiKey is set.
func (a *Anki) RequireKey(key string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.key = key
}

// Answered lists the answers received so far.
func (a *Anki) Answered() []Answer {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.answered)
}

// Syncs counts sync calls.
func (a *Anki) Syncs() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.syncs
}

// SetMedia installs fixture bytes without accessing a real collection.
func (a *Anki) SetMedia(name string, raw []byte) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.media == nil {
		a.media = make(map[string][]byte)
	}
	a.media[name] = bytes.Clone(raw)
}

// FailImport injects native-import failures while retaining staged-file cleanup behavior.
func (a *Anki) FailImport(fail bool) { a.mu.Lock(); defer a.mu.Unlock(); a.importFailure = fail }

// MediaFiles snapshots fixture filenames for cleanup assertions.
func (a *Anki) MediaFiles() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	names := make([]string, 0, len(a.media))
	for name := range a.media {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// The apiKey AnkiConnect checks on every request.
const keyError = "valid api key must be provided"

type params struct {
	Filename string   `json:"filename"`
	Data     string   `json:"data"`
	Path     string   `json:"path"`
	Query    string   `json:"query"`
	Cards    []int64  `json:"cards"`
	Decks    []string `json:"decks"`
	Answers  []Answer `json:"answers"`
}

func (a *Anki) serve(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Action string `json:"action"`
		Key    string `json:"key"`
		Params params `json:"params"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	a.mu.Lock()
	var result any
	var errMsg string
	if a.key != "" && req.Key != a.key {
		errMsg = keyError
	} else {
		result, errMsg = a.handle(req.Action, req.Params)
	}
	a.mu.Unlock()
	var e any
	if errMsg != "" {
		e = errMsg
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"result": result, "error": e})
}

func (a *Anki) handle(action string, p params) (any, string) {
	switch action {
	case "storeMediaFile":
		raw, err := base64.StdEncoding.DecodeString(p.Data)
		if err != nil {
			return nil, "bad media data"
		}
		if a.media == nil {
			a.media = make(map[string][]byte)
		}
		a.media[p.Filename] = raw
		return p.Filename, ""
	case "deleteMediaFile":
		delete(a.media, p.Filename)
		return nil, ""
	case "retrieveMediaFile":
		raw, ok := a.media[p.Filename]
		if !ok {
			return false, ""
		}
		return base64.StdEncoding.EncodeToString(raw), ""
	case "getMediaDirPath":
		return "/fixture/collection.media", ""
	case "importPackage":
		return a.importPackage(p.Path)
	case "version":
		return 6, ""
	case "deckNames":
		var names []string
		for _, c := range a.cards {
			parts := strings.Split(c.Deck, "::")
			for i := range parts {
				if name := strings.Join(parts[:i+1], "::"); !slices.Contains(names, name) {
					names = append(names, name)
				}
			}
		}
		return append(names, "Default"), ""
	case "getDeckStats":
		stats := map[string]any{}
		for i, deck := range p.Decks {
			if deck == "Default" {
				return nil, "Default is hidden"
			}
			var fresh, review int
			for _, c := range a.due(deck) {
				if c.New {
					fresh++
				} else {
					review++
				}
			}
			stats[strconv.Itoa(i+1)] = map[string]any{"name": deck, "new_count": fresh, "learn_count": 0, "review_count": review}
		}
		return stats, ""
	case "findCards":
		return a.find(p.Query), ""
	case "cardsInfo":
		return a.info(p.Cards), ""
	case "answerCards":
		ok := make([]bool, len(p.Answers))
		for i, ans := range p.Answers {
			if c := a.card(ans.CardID); c != nil && !c.answered {
				c.answered, c.Reps, c.Mod = true, c.Reps+1, c.Mod+1
				a.answered = append(a.answered, ans)
				ok[i] = true
			}
		}
		return ok, ""
	case "sync":
		a.syncs++
		return nil, ""
	}
	return nil, "unsupported action " + action
}

func (a *Anki) due(deck string) []*Card {
	var out []*Card
	for _, c := range a.cards {
		if !c.answered && (c.Deck == deck || strings.HasPrefix(c.Deck, deck+"::")) {
			out = append(out, c)
		}
	}
	return out
}

func (a *Anki) find(query string) []int64 {
	m := reDeck.FindStringSubmatch(query)
	if m == nil {
		return []int64{}
	}
	deck := reEscape.ReplaceAllString(m[1], "$1")
	ids := []int64{}
	for _, c := range a.due(deck) {
		switch {
		case strings.HasPrefix(query, "is:learn"):
		case strings.HasPrefix(query, "is:review") && !c.New, strings.HasPrefix(query, "is:new") && c.New:
			ids = append(ids, c.ID)
		}
	}
	return ids
}

func (a *Anki) card(id int64) *Card {
	for _, c := range a.cards {
		if c.ID == id {
			return c
		}
	}
	return nil
}

func (a *Anki) info(ids []int64) []map[string]any {
	out := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		c := a.card(id)
		if c == nil {
			out = append(out, map[string]any{})
			continue
		}
		typ := 2
		if c.New {
			typ = 0
		}
		out = append(out, map[string]any{
			"css":    c.CSS,
			"cardId": c.ID, "deckName": c.Deck, "modelName": "Basic", "type": typ, "due": c.ID, "reps": c.Reps, "mod": c.Mod,
			"question": c.Question, "answer": c.Question + "<hr id=answer>" + c.Answer,
			"fields":      map[string]any{"Front": map[string]any{"value": c.Question}},
			"nextReviews": []string{"<1m", "6m", "1d", "4d"},
		})
	}
	return out
}

func (a *Anki) importPackage(packagePath string) (any, string) {
	if a.importFailure {
		return nil, "fixture import unavailable"
	}
	raw, ok := a.media[path.Base(packagePath)]
	if !ok {
		return false, ""
	}
	archive, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return nil, "bad fixture archive"
	}
	entries := make(map[string][]byte)
	for _, file := range archive.File {
		reader, err := file.Open()
		if err != nil {
			return nil, "bad fixture entry"
		}
		data, err := io.ReadAll(reader)
		_ = reader.Close()
		if err != nil {
			return nil, "bad fixture data"
		}
		entries[file.Name] = data
	}
	var mapping map[string]string
	if err := json.Unmarshal(entries["media"], &mapping); err != nil {
		return nil, "bad fixture media"
	}
	for number, name := range mapping {
		a.media[name] = entries[number]
	}
	if a.card(99) == nil {
		a.cards = append(a.cards, &Card{ID: 99, Deck: "Imported", Answer: "Imported answer"})
	}
	card := a.card(99)
	card.Question = `<b>Imported question</b><img src="photo.png">`
	card.CSS = ".card { color: navy; }"
	for _, name := range mapping {
		if name == "voice.wav" {
			card.Question += `<audio controls src="voice.wav"></audio>`
		}
	}
	return true, ""
}
