package ankiconnect

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/scv3lqq/anki-review/internal/domain"
)

const activityChunk = 100
const activityCardLimit = 200000
const activityEventLimit = 2000000

type activityReview struct {
	ID   int64 `json:"id"`
	Ease int   `json:"ease"`
}

// Activity reads extant cards and their history without invoking deck creation.
func (c *Client) Activity(ctx context.Context, now time.Time) ([]domain.ActivityDay, error) {
	var ids []int64
	if err := c.call(ctx, "findCards", map[string]any{"query": ""}, &ids); err != nil {
		return nil, fmt.Errorf("activity cards: %w", err)
	}
	if ids == nil {
		return nil, fmt.Errorf("activity cards: %w: missing result", domain.ErrAnki)
	}
	if len(ids) > activityCardLimit {
		return nil, fmt.Errorf("activity: %w: collection exceeds card limit", domain.ErrAnki)
	}
	events := make([]domain.ActivityEvent, 0)
	seen := make(map[int64]bool)
	seenCards := make(map[int64]bool, len(ids))
	first := domain.ActivityFirstDate(now)
	for start := 0; start < len(ids); start += activityChunk {
		chunk := ids[start:min(start+activityChunk, len(ids))]
		var decks map[string][]int64
		if err := c.call(ctx, "getDecks", map[string]any{"cards": chunk}, &decks); err != nil {
			return nil, fmt.Errorf("activity decks: %w", err)
		}
		if decks == nil {
			return nil, fmt.Errorf("activity decks: %w: missing result", domain.ErrAnki)
		}
		cardDeck := make(map[int64]string, len(chunk))
		for name, cards := range decks {
			for _, id := range cards {
				cardDeck[id] = name
			}
		}
		var reviews map[string][]activityReview
		if err := c.call(ctx, "getReviewsOfCards", map[string]any{"cards": chunk}, &reviews); err != nil {
			return nil, fmt.Errorf("activity reviews: %w", err)
		}
		if reviews == nil {
			return nil, fmt.Errorf("activity reviews: %w: missing result", domain.ErrAnki)
		}
		for _, id := range chunk {
			if seenCards[id] {
				continue
			}
			seenCards[id] = true
			deck, ok := cardDeck[id]
			if !ok {
				continue
			}
			created := time.UnixMilli(id)
			if created.In(now.Location()).Format("2006-01-02") >= first && !created.After(now) {
				events = append(events, domain.ActivityEvent{At: created, Deck: deck})
			}
			for _, row := range reviews[strconv.FormatInt(id, 10)] {
				at := time.UnixMilli(row.ID)
				if row.Ease < 1 || row.Ease > 4 || at.In(now.Location()).Format("2006-01-02") < first || at.After(now) || seen[row.ID] {
					continue
				}
				seen[row.ID] = true
				events = append(events, domain.ActivityEvent{At: at, Deck: deck, Review: true})
				if len(events) > activityEventLimit {
					return nil, fmt.Errorf("activity: %w: history exceeds event limit", domain.ErrAnki)
				}
			}
		}
	}
	return domain.ProjectActivity(now, events), nil
}
