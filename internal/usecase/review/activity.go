package review

import (
	"context"
	"fmt"
	"time"

	"github.com/scv3lqq/anki-review/internal/domain"
)

// ActivitySource reads the collection for the authenticated account.
type ActivitySource interface {
	Activity(context.Context, time.Time) ([]domain.ActivityDay, error)
}

// ReadActivity retrieves a calendar without modifying the active pass.
func ReadActivity(ctx context.Context, source ActivitySource, now time.Time) ([]domain.ActivityDay, error) {
	days, err := source.Activity(ctx, now)
	if err != nil {
		return nil, fmt.Errorf("read activity: %w", err)
	}
	return days, nil
}
