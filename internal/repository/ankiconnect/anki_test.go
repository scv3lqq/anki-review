package ankiconnect

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/scv3lqq/anki-review/internal/domain"
)

// fakeAnki answers AnkiConnect actions with the given function and records every request.
type fakeAnki struct {
	mu       sync.Mutex
	requests []request
	handle   func(action string, params json.RawMessage) (any, string)
}

func newFake(t *testing.T, handle func(action string, params json.RawMessage) (any, string)) (*Client, *fakeAnki) {
	t.Helper()
	f := &fakeAnki{handle: handle}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			request
			Params json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		f.mu.Lock()
		f.requests = append(f.requests, req.request)
		f.mu.Unlock()
		result, errMsg := f.handle(req.Action, req.Params)
		resp := map[string]any{"result": result, "error": nil}
		if errMsg != "" {
			resp["error"] = errMsg
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(srv.Close)
	c := New(srv.URL, "secret")
	c.lockWait = time.Millisecond
	return c, f
}

func TestCallErrors(t *testing.T) {
	tests := []struct {
		name    string
		errors  []string
		wantErr error
		calls   int
	}{
		{"ok", []string{""}, nil, 1},
		{"collection locked, then ok", []string{lockedMarker, lockedMarker, ""}, nil, 3},
		{"locked for too long", repeat(lockedMarker, lockRetries), domain.ErrAnki, lockRetries},
		{"queue busy", []string{"card 1 not at top of queue"}, domain.ErrQueueBusy, 1},
		{"other error", []string{"collection is not available"}, domain.ErrAnki, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n := 0
			c, f := newFake(t, func(string, json.RawMessage) (any, string) {
				msg := tt.errors[min(n, len(tt.errors)-1)]
				n++
				return nil, msg
			})
			err := c.Sync(context.Background())
			if !errors.Is(err, tt.wantErr) || (tt.wantErr == nil) != (err == nil) {
				t.Fatalf("Sync() error = %v, want %v", err, tt.wantErr)
			}
			if len(f.requests) != tt.calls {
				t.Errorf("calls = %d, want %d", len(f.requests), tt.calls)
			}
			if f.requests[0].Key != "secret" || f.requests[0].Version != apiVersion {
				t.Errorf("request = %+v, want key and version %d", f.requests[0], apiVersion)
			}
		})
	}
}

func TestCallUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	if err := New(srv.URL, "").Sync(context.Background()); !errors.Is(err, domain.ErrAnki) {
		t.Fatalf("Sync() error = %v, want ErrAnki", err)
	}
}

func repeat(s string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = s
	}
	return out
}

// collection is a tiny Anki: cards by id, with the search queries the queue uses.
type collection struct {
	cards    map[int64]map[string]any
	learn    []int64
	review   []int64
	fresh    []int64
	newLimit int
}

func (col collection) handle(action string, params json.RawMessage) (any, string) {
	var p struct {
		Query  string   `json:"query"`
		Cards  []int64  `json:"cards"`
		Decks  []string `json:"decks"`
		Fields []string `json:"fields"`
	}
	_ = json.Unmarshal(params, &p)
	switch action {
	case "findCards":
		switch {
		case strings.HasPrefix(p.Query, "is:learn"):
			return col.learn, ""
		case strings.HasPrefix(p.Query, "is:review"):
			return col.review, ""
		default:
			return col.fresh, ""
		}
	case "getDeckStats":
		return map[string]any{"1": map[string]any{"name": p.Decks[0], "new_count": col.newLimit}}, ""
	case "cardsInfo":
		out := make([]map[string]any, 0, len(p.Cards))
		for _, id := range p.Cards {
			card, ok := col.cards[id]
			if !ok {
				out = append(out, map[string]any{})
				continue
			}
			if p.Fields != nil {
				card = map[string]any{"cardId": id, "nextReviews": card["nextReviews"]}
			}
			out = append(out, card)
		}
		return out, ""
	}
	return nil, "unsupported action " + action
}

func card(id int64, typ int, due int64, question, answer string) map[string]any {
	return map[string]any{
		"cardId": id, "modelName": "Basic", "deckName": "Tech::Go", "type": typ, "due": due, "reps": 2, "mod": 1000 + id,
		"question": question, "answer": question + "<hr id=answer>" + answer,
		"fields":      map[string]any{"Front": map[string]any{"value": question}},
		"nextReviews": []string{"\u2068<1m\u2069", "\u20686m\u2069", "\u20681d\u2069", "\u20684d\u2069"},
	}
}

func TestDueCards(t *testing.T) {
	col := collection{
		cards: map[int64]map[string]any{
			1: card(1, 1, 0, "learning", "a1"),
			2: card(2, 2, 0, "review", "a2"),
			3: card(3, 0, 30, "new late", "a3"),
			4: card(4, 0, 10, "new early", "a4"),
			5: card(5, 0, 20, "new middle", "a5"),
		},
		learn:    []int64{1},
		review:   []int64{2, 1},
		fresh:    []int64{3, 4, 5},
		newLimit: 2,
	}
	col.cards[2]["fields"] = map[string]any{"Source": map[string]any{"value": "<a href='x'>Go Channels</a>"}}
	c, _ := newFake(t, col.handle)

	tests := []struct {
		name  string
		limit int
		want  []string
	}{
		{"learning, reviews, then new by due within the daily limit", 10, []string{"learning", "review", "new early", "new middle"}},
		{"limit cuts the tail", 3, []string{"learning", "review", "new early"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			items, err := c.DueCards(context.Background(), "Tech", tt.limit)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for i, it := range items {
				if it.N != i+1 {
					t.Errorf("item %d has N %d", i, it.N)
				}
				got = append(got, it.Question)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("questions = %v, want %v", got, tt.want)
			}
		})
	}

	items, err := c.DueCards(context.Background(), "Tech", 2)
	if err != nil {
		t.Fatal(err)
	}
	review := items[1]
	if review.Topic != "Go Channels" || review.Back != "a2" || review.IsNew || review.Reps != 2 || review.Mod != 1002 {
		t.Errorf("review item = %+v", review)
	}
	if items[0].Topic != "Tech::Go" {
		t.Errorf("topic without Source = %q, want the deck", items[0].Topic)
	}
	wantIntervals := map[domain.Grade]string{domain.Again: "<1m", domain.Hard: "6m", domain.Good: "1d", domain.Easy: "4d"}
	if !reflect.DeepEqual(review.Intervals, wantIntervals) {
		t.Errorf("intervals = %v, want %v", review.Intervals, wantIntervals)
	}
}

func TestDecks(t *testing.T) {
	c, f := newFake(t, func(action string, _ json.RawMessage) (any, string) {
		switch action {
		case "deckNames":
			return []string{"Default", "Tech::Go", "English", "Tech", "Empty"}, ""
		case "getDeckStats":
			return map[string]any{
				"1": map[string]any{"name": "Tech::Go", "new_count": 1, "learn_count": 0, "review_count": 2},
				"2": map[string]any{"name": "English", "new_count": 0, "learn_count": 1, "review_count": 0},
				"3": map[string]any{"name": "Tech", "new_count": 1, "learn_count": 0, "review_count": 4},
				"4": map[string]any{"name": "Empty"},
			}, ""
		}
		return nil, "unsupported"
	})
	decks, err := c.Decks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []domain.Deck{
		{Name: "English", Learn: 1, Due: 1},
		{Name: "Tech", Review: 4, New: 1, Due: 5},
		{Name: "Tech::Go", Review: 2, New: 1, Due: 3},
	}
	if !reflect.DeepEqual(decks, want) {
		t.Errorf("Decks() = %+v, want %+v", decks, want)
	}
	if strings.Contains(string(mustJSON(t, f.requests)), `"Default"`) {
		t.Error("Default deck was sent to getDeckStats")
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestCardStatesSkipsDeleted(t *testing.T) {
	c, _ := newFake(t, collection{cards: map[int64]map[string]any{7: card(7, 2, 0, "q", "a")}}.handle)
	states, err := c.CardStates(context.Background(), []int64{7, 8})
	if err != nil {
		t.Fatal(err)
	}
	want := map[int64]domain.CardState{7: {Reps: 2, Mod: 1007}}
	if !reflect.DeepEqual(states, want) {
		t.Errorf("CardStates() = %v, want %v", states, want)
	}
}

func TestAnswer(t *testing.T) {
	var got json.RawMessage
	c, _ := newFake(t, func(_ string, params json.RawMessage) (any, string) {
		got = params
		return []bool{true, false}, ""
	})
	ok, err := c.Answer(context.Background(), []domain.CardAnswer{{CardID: 1, Ease: 3}, {CardID: 2, Ease: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ok, []bool{true, false}) {
		t.Errorf("Answer() = %v", ok)
	}
	if want := `{"answers":[{"cardId":1,"ease":3},{"cardId":2,"ease":1}]}`; string(got) != want {
		t.Errorf("params = %s, want %s", got, want)
	}
}
