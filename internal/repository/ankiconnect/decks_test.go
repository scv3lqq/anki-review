package ankiconnect

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/scv3lqq/anki-review/internal/domain"
)

func TestDecksDefaultPresence(t *testing.T) {
	for _, tc := range []struct {
		name       string
		names      []string
		defaultIDs []int64
		lookupErr  string
		wantErr    error
		wantStats  []string
		wantDecks  []domain.Deck
		wantCalls  []string
	}{
		{
			name: "populated Default keeps due counts and ordering", names: []string{"English", "Default"}, defaultIDs: []int64{123},
			wantStats: []string{"English", "Default"},
			wantDecks: []domain.Deck{{Name: "Default", Learn: 2, Review: 3, New: 4, Due: 9}, {Name: "English", New: 1, Due: 1}},
			wantCalls: []string{"deckNames", "findCards", "getDeckStats"},
		},
		{
			name: "empty Default excluded before stats", names: []string{"Default", "English"},
			wantStats: []string{"English"}, wantDecks: []domain.Deck{{Name: "English", New: 1, Due: 1}},
			wantCalls: []string{"deckNames", "findCards", "getDeckStats"},
		},
		{
			name: "Default-only empty skips stats", names: []string{"Default"},
			wantCalls: []string{"deckNames", "findCards"},
		},
		{
			name: "no Default skips presence lookup", names: []string{"English"},
			wantStats: []string{"English"}, wantDecks: []domain.Deck{{Name: "English", New: 1, Due: 1}},
			wantCalls: []string{"deckNames", "getDeckStats"},
		},
		{
			name: "presence failure skips stats", names: []string{"Default", "English"}, lookupErr: "collection is unavailable", wantErr: domain.ErrAnki,
			wantCalls: []string{"deckNames", "findCards"},
		},
		{
			name: "presence key failure preserves cause", names: []string{"Default"}, lookupErr: keyMarker, wantErr: domain.ErrAnkiKey,
			wantCalls: []string{"deckNames", "findCards"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var actions []string
			c, _ := newFake(t, func(action string, params json.RawMessage) (any, string) {
				actions = append(actions, action)
				switch action {
				case "deckNames":
					return tc.names, ""
				case "findCards":
					var query struct {
						Query string `json:"query"`
					}
					if err := json.Unmarshal(params, &query); err != nil {
						t.Error(err)
					}
					if query.Query != `"deck:Default"` {
						t.Errorf("Default presence query = %q", query.Query)
					}
					return tc.defaultIDs, tc.lookupErr
				case "getDeckStats":
					var requested struct {
						Decks []string `json:"decks"`
					}
					if err := json.Unmarshal(params, &requested); err != nil {
						t.Error(err)
					}
					if !reflect.DeepEqual(requested.Decks, tc.wantStats) {
						t.Errorf("stats decks = %v, want %v", requested.Decks, tc.wantStats)
					}
					stats := make(map[string]deckStats)
					for _, name := range requested.Decks {
						if name == "Default" {
							stats[name] = deckStats{Name: name, LearnCount: 2, ReviewCount: 3, NewCount: 4}
						} else {
							stats[name] = deckStats{Name: name, NewCount: 1}
						}
					}
					return stats, ""
				default:
					t.Errorf("unexpected or write action %q", action)
					return nil, "unexpected action"
				}
			})
			decks, err := c.Decks(context.Background())
			if !errors.Is(err, tc.wantErr) || (err == nil) != (tc.wantErr == nil) {
				t.Errorf("Decks error = %v, want %v", err, tc.wantErr)
			}
			if tc.wantErr != nil && (err == nil || !strings.HasPrefix(err.Error(), "ankiconnect default deck: ")) {
				t.Errorf("Default presence error lacks operation context: %v", err)
			}
			if !reflect.DeepEqual(decks, tc.wantDecks) {
				t.Errorf("Decks = %+v, want %+v", decks, tc.wantDecks)
			}
			if !reflect.DeepEqual(actions, tc.wantCalls) {
				t.Errorf("actions = %v, want %v", actions, tc.wantCalls)
			}
		})
	}
}
