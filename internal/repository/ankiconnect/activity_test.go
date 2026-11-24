package ankiconnect

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

func TestActivityReadOnlyAndFiltersReviews(t *testing.T) {
	now := time.Date(2026, 3, 9, 12, 0, 0, 0, time.UTC)
	id := now.Add(-time.Hour).UnixMilli()
	c, _ := newFake(t, func(action string, _ json.RawMessage) (any, string) {
		switch action {
		case "findCards":
			return []int64{id}, ""
		case "getDecks":
			return map[string][]int64{"Deck": {id}}, ""
		case "getReviewsOfCards":
			return map[string]any{fmt.Sprint(id): []map[string]any{{"id": id + 1, "ease": 3}, {"id": id + 2, "ease": 0}, {"id": now.Add(time.Hour).UnixMilli(), "ease": 4}}}, ""
		default:
			t.Errorf("unexpected action %s", action)
			return nil, "unexpected"
		}
	})
	days, err := c.Activity(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	last := days[len(days)-1]
	if last.Reviews != 1 || last.New != 1 || len(last.Decks) != 1 || last.Decks[0].Name != "Deck" {
		t.Fatalf("last day: %+v", last)
	}
}

func TestActivityEmptyAndErrors(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name string
		fail bool
	}{{"empty", false}, {"unavailable", true}} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := newFake(t, func(action string, _ json.RawMessage) (any, string) {
				if tc.fail {
					return nil, "offline"
				}
				if action != "findCards" {
					t.Fatalf("unexpected %s", action)
				}
				return []int64{}, ""
			})
			days, err := c.Activity(context.Background(), now)
			if tc.fail {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil || len(days) != 365 {
				t.Fatalf("days=%d err=%v", len(days), err)
			}
		})
	}
}

func TestActivityMissingMappingAndDuplicateReviews(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	a := now.Add(-time.Hour).UnixMilli()
	b := a - 1
	c, _ := newFake(t, func(action string, _ json.RawMessage) (any, string) {
		switch action {
		case "findCards":
			return []int64{a, b}, ""
		case "getDecks":
			return map[string][]int64{"A": {a}}, ""
		case "getReviewsOfCards":
			return map[string]any{fmt.Sprint(a): []map[string]any{{"id": a + 1, "ease": 1}, {"id": a + 1, "ease": 1}}, fmt.Sprint(b): []map[string]any{{"id": b + 3, "ease": 2}}}, ""
		}
		return nil, "unexpected"
	})
	days, err := c.Activity(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	d := days[364]
	if d.Reviews != 1 || d.New != 1 {
		t.Fatalf("day=%+v", d)
	}
}

func TestActivityRejectsNullResponse(t *testing.T) {
	c, _ := newFake(t, func(string, json.RawMessage) (any, string) { return nil, "" })
	if _, err := c.Activity(context.Background(), time.Now()); err == nil {
		t.Fatal("null result must be unavailable")
	}
}

func TestActivitySantiagoFirstDate(t *testing.T) {
	loc, err := time.LoadLocation("America/Santiago")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2027, 9, 5, 12, 0, 0, 0, loc)
	first := time.Date(2026, 9, 6, 1, 0, 0, 0, loc)
	id := first.UnixMilli()
	previous := first.Add(-time.Millisecond).UnixMilli()
	c, _ := newFake(t, func(action string, _ json.RawMessage) (any, string) {
		switch action {
		case "findCards":
			return []int64{id, previous}, ""
		case "getDecks":
			return map[string][]int64{"Deck": {id, previous}}, ""
		case "getReviewsOfCards":
			return map[string]any{fmt.Sprint(id): []map[string]any{{"id": id + 1, "ease": 3}, {"id": previous, "ease": 3}, {"id": now.Add(time.Second).UnixMilli(), "ease": 3}}}, ""
		}
		return nil, "unexpected"
	})
	days, err := c.Activity(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if days[0].Date != "2026-09-06" || days[0].New != 1 || days[0].Reviews != 1 || days[364].Reviews != 0 {
		t.Fatalf("first=%+v last=%+v", days[0], days[364])
	}
}
