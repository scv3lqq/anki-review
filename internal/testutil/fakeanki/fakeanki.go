// Package fakeanki is an in-memory AnkiConnect server for end-to-end tests. A card is due until it
// is answered; there are no learning steps.
package fakeanki

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	answered bool
}

// Answer is one answerCards entry Anki received.
type Answer struct {
	CardID int64 `json:"cardId"`
	Ease   int   `json:"ease"`
}

// Anki serves AnkiConnect on a local port.
type Anki struct {
	mu       sync.Mutex
	cards    []*Card
	answered []Answer
	syncs    int
	key      string
	srv      *httptest.Server
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

// The apiKey AnkiConnect checks on every request.
const keyError = "valid api key must be provided"

type params struct {
	Query   string   `json:"query"`
	Cards   []int64  `json:"cards"`
	Decks   []string `json:"decks"`
	Answers []Answer `json:"answers"`
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
			"cardId": c.ID, "deckName": c.Deck, "modelName": "Basic", "type": typ, "due": c.ID, "reps": c.Reps, "mod": c.Mod,
			"question": c.Question, "answer": c.Question + "<hr id=answer>" + c.Answer,
			"fields":      map[string]any{"Front": map[string]any{"value": c.Question}},
			"nextReviews": []string{"<1m", "6m", "1d", "4d"},
		})
	}
	return out
}
