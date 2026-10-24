package review

import (
	"context"

	"github.com/scv3lqq/anki-review/internal/domain"
)

// Anki is the collection: due cards in, button presses out.
type Anki interface {
	Decks(ctx context.Context) ([]domain.Deck, error)
	DeckNames(ctx context.Context) ([]string, error)
	DueCards(ctx context.Context, deck string, limit int) ([]domain.Item, error)
	DueCount(ctx context.Context, deck string) (int, error)
	CardStates(ctx context.Context, ids []int64) (map[int64]domain.CardState, error)
	// Answer reports per answer whether Anki took it; domain.ErrQueueBusy means none was written.
	Answer(ctx context.Context, answers []domain.CardAnswer) ([]bool, error)
	Sync(ctx context.Context) error
}

// Grader grades the answered items of a pass, one result per item.
type Grader interface {
	Grade(ctx context.Context, kind domain.Kind, items []domain.Item, answers map[int]string) ([]domain.Result, error)
}

// Store persists the current pass; nil means no pass.
type Store interface {
	Load() (*domain.Pass, error)
	Save(p *domain.Pass) error
}
