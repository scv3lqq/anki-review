package review

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/scv3lqq/anki-review/internal/domain"
)

type activitySourceStub struct{ err error }

func (s activitySourceStub) Activity(context.Context, time.Time) ([]domain.ActivityDay, error) {
	return nil, s.err
}
func TestReadActivityPropagatesSourceFailure(t *testing.T) {
	want := errors.New("offline")
	_, err := ReadActivity(context.Background(), activitySourceStub{want}, time.Now())
	if !errors.Is(err, want) {
		t.Fatalf("error = %v", err)
	}
}
