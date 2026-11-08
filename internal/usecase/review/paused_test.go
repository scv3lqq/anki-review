package review

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/scv3lqq/anki-review/internal/domain"
)

func pausedPass(stage domain.Stage) *domain.Pass {
	p := &domain.Pass{Session: "saved", Deck: "Tech", Kind: domain.KindKnowledge, CreatedAt: now, Items: items()}
	p.SetAnswers(map[int]string{1: "saved answer"})
	if stage == domain.StageConfirmed {
		p.Results = []domain.Result{{N: 1, Grade: domain.Hard, Note: "saved feedback"}}
		p.Confirm(map[int]domain.Grade{1: domain.Easy})
	}
	return p
}

func newPausedService(t *testing.T, pass *domain.Pass) (*Service, *fakeAnki, *fakeGrader) {
	t.Helper()
	anki, grader := newFakeAnki(items()...), &fakeGrader{}
	opts := Options{PauseOnLoad: true, Log: zerolog.Nop(), Now: func() time.Time { return now }}
	svc, err := New(anki, grader, &memStore{pass: pass}, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close(context.Background()) })
	return svc, anki, grader
}

func TestPausedLoadAndReadsHaveNoSideEffects(t *testing.T) {
	for _, stage := range []domain.Stage{domain.StageChecking, domain.StageConfirmed} {
		t.Run(string(stage), func(t *testing.T) {
			pass := pausedPass(stage)
			before, err := json.Marshal(pass)
			if err != nil {
				t.Fatal(err)
			}
			svc, anki, grader := newPausedService(t, pass)
			svc.wg.Wait()
			for range 3 {
				svc.Cards()
				_, _ = svc.Results()
				svc.Status()
			}
			svc.wg.Wait()
			if grader.calls != 0 || len(anki.answered) != 0 {
				t.Fatalf("passive load/reads: grading calls=%d, Anki writes=%d; want zero", grader.calls, len(anki.answered))
			}
			after, err := json.Marshal(pass)
			if err != nil {
				t.Fatal(err)
			}
			if string(before) != string(after) {
				t.Fatalf("passive load/reads changed saved pass: before=%s after=%s", before, after)
			}
		})
	}
}

func TestPausedViewsPreserveStageAndData(t *testing.T) {
	for _, stage := range []domain.Stage{domain.StageChecking, domain.StageConfirmed} {
		t.Run(string(stage), func(t *testing.T) {
			pass := pausedPass(stage)
			svc, _, _ := newPausedService(t, pass)
			cards := svc.Cards()
			if !cards.Paused || cards.Stage != stage || !reflect.DeepEqual(cards.Answers, pass.Answers) {
				t.Fatalf("paused cards=%+v; want stage=%s and saved answers", cards, stage)
			}
			results, err := svc.Results()
			if err != nil || !results.Paused || results.Session != pass.Session {
				t.Fatalf("paused results=%+v, err=%v", results, err)
			}
			if stage == domain.StageChecking {
				if results.Items == nil || len(results.Items) != 0 || results.Confirmed != nil || results.Status != nil {
					t.Fatalf("checking results must expose no answer keys: %+v", results)
				}
			} else if results.Confirmed[1] != domain.Easy || results.Items[0].Answer != "saved answer" || results.Items[0].Grade != domain.Hard {
				t.Fatalf("confirmed results lost saved data: %+v", results)
			}
		})
	}
}

type pausedBlockingAnki struct {
	*fakeAnki
	release chan struct{}
}

func (a *pausedBlockingAnki) Answer(ctx context.Context, answers []domain.CardAnswer) ([]bool, error) {
	select {
	case <-a.release:
		return a.fakeAnki.Answer(ctx, answers)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestResumePausedConcurrentStartsOnce(t *testing.T) {
	for _, stage := range []domain.Stage{domain.StageChecking, domain.StageConfirmed} {
		t.Run(string(stage), func(t *testing.T) {
			pass := pausedPass(stage)
			release := make(chan struct{})
			anki := &pausedBlockingAnki{fakeAnki: newFakeAnki(items()...), release: release}
			grader := &fakeGrader{release: release}
			svc, err := New(anki, grader, &memStore{pass: pass}, Options{PauseOnLoad: true, Log: zerolog.Nop(), Now: func() time.Time { return now }})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = svc.Close(context.Background()) })
			var callers sync.WaitGroup
			errs := make(chan error, 16)
			for range 16 {
				callers.Go(func() { errs <- svc.Resume("saved") })
			}
			callers.Wait()
			close(errs)
			for err := range errs {
				if err != nil {
					t.Errorf("concurrent Resume: %v", err)
				}
			}
			if svc.Cards().Paused {
				t.Error("running pass remains paused")
			}
			svc.mu.Lock()
			svc.opts.Now = func() time.Time { return now.Add(passTTL + time.Minute) }
			svc.mu.Unlock()
			if err := svc.Resume("saved"); err != nil {
				t.Errorf("Resume of active pass after TTL: %v", err)
			}
			close(release)
			svc.wg.Wait()
			if stage == domain.StageChecking {
				if grader.calls != 1 || len(anki.answered) != 0 || !reflect.DeepEqual(pass.AnswerMap(), map[int]string{1: "saved answer"}) {
					t.Fatalf("resumed checking: grader calls=%d, writes=%v, answers=%v", grader.calls, anki.answered, pass.Answers)
				}
			} else if grader.calls != 0 || !reflect.DeepEqual(anki.answered, [][]domain.CardAnswer{{{CardID: 10, Ease: 4}}}) {
				t.Fatalf("resumed confirmed: grader calls=%d, writes=%v", grader.calls, anki.answered)
			}
		})
	}
}

func TestResumePausedRejectsWrongAndExpiredSessions(t *testing.T) {
	for _, stage := range []domain.Stage{domain.StageChecking, domain.StageConfirmed} {
		for _, age := range []time.Duration{0, passTTL, passTTL + time.Nanosecond} {
			t.Run(fmt.Sprintf("%s/%s", stage, age), func(t *testing.T) {
				pass := pausedPass(stage)
				pass.CreatedAt = now.Add(-age)
				svc, anki, grader := newPausedService(t, pass)
				wantConflict(t, svc.Resume("incorrect"), CodeSession)
				if grader.calls != 0 || len(anki.answered) != 0 {
					t.Fatal("incorrect session started work")
				}
				err := svc.Resume("saved")
				if age > passTTL {
					wantConflict(t, err, CodeSession)
					if grader.calls != 0 || len(anki.answered) != 0 || svc.Cards().Stage != domain.StageIdle {
						t.Fatal("expired pass resumed")
					}
				} else {
					if err != nil {
						t.Fatal(err)
					}
					svc.wg.Wait()
					if grader.calls+len(anki.answered) != 1 {
						t.Fatalf("valid pass did not resume once: grader=%d writes=%d", grader.calls, len(anki.answered))
					}
				}
			})
		}
	}
}

func TestResumePausedRejectsOtherStagesAndErrors(t *testing.T) {
	for _, stage := range []domain.Stage{domain.StageIdle, domain.StageInput, domain.StageResults, domain.StageWritten, domain.StageChecking} {
		t.Run(string(stage), func(t *testing.T) {
			var pass *domain.Pass
			if stage != domain.StageIdle {
				pass = &domain.Pass{Session: "saved", CreatedAt: now, Items: items()}
				switch stage {
				case domain.StageResults:
					pass.Results = []domain.Result{{N: 1, Grade: domain.Good}}
				case domain.StageWritten:
					pass.Status = &domain.Status{Written: true}
				case domain.StageChecking:
					pass.SetAnswers(map[int]string{1: "saved answer"})
					pass.GradeError = "saved grading failure"
				}
			}
			svc, anki, grader := newPausedService(t, pass)
			if svc.Cards().Paused {
				t.Fatal("nonresumable pass marked paused")
			}
			if stage == domain.StageIdle {
				wantConflict(t, svc.Resume("saved"), CodeSession)
			} else {
				err := svc.Resume("saved")
				wantConflict(t, err, CodeSent)
				if err.Error() != "this batch has no interrupted operation" {
					t.Fatalf("Resume error=%v", err)
				}
			}
			if grader.calls != 0 || len(anki.answered) != 0 {
				t.Fatal("nonresumable pass started work")
			}
			if stage == domain.StageChecking {
				_, err := svc.Results()
				var failure *GradingFailedError
				if !errors.As(err, &failure) || failure.Reason != "saved grading failure" {
					t.Fatalf("saved error lost: %v", err)
				}
				if err := svc.Regrade("saved"); err != nil {
					t.Fatal(err)
				}
				svc.wg.Wait()
				if grader.calls != 1 {
					t.Fatalf("Regrade calls=%d", grader.calls)
				}
			}
		})
	}
}

func TestResumePausedCloseAndGradingFailure(t *testing.T) {
	t.Run("close blocks resume", func(t *testing.T) {
		for _, stage := range []domain.Stage{domain.StageChecking, domain.StageConfirmed} {
			svc, anki, grader := newPausedService(t, pausedPass(stage))
			if err := svc.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
			err := svc.Resume("saved")
			wantConflict(t, err, CodeBusy)
			if err.Error() != "review service is closed" {
				t.Fatalf("Resume error=%v", err)
			}
			if grader.calls != 0 || len(anki.answered) != 0 {
				t.Fatal("closed service resumed work")
			}
		}
	})
	t.Run("close cancels resumed grading", func(t *testing.T) {
		svc, _, grader := newPausedService(t, pausedPass(domain.StageChecking))
		grader.release = make(chan struct{})
		if err := svc.Resume("saved"); err != nil {
			t.Fatal(err)
		}
		if err := svc.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
		if svc.pass.Stage() != domain.StageChecking || svc.pass.GradeError != "" {
			t.Fatalf("close changed interrupted pass: %+v", svc.pass)
		}
	})
	t.Run("resumed grading failure stays retryable", func(t *testing.T) {
		svc, _, grader := newPausedService(t, pausedPass(domain.StageChecking))
		grader.err = errors.New("provider unavailable")
		if err := svc.Resume("saved"); err != nil {
			t.Fatal(err)
		}
		svc.wg.Wait()
		if cards := svc.Cards(); cards.Paused || cards.GradeError != "provider unavailable" {
			t.Fatalf("failed resume cards=%+v", cards)
		}
		wantConflict(t, svc.Resume("saved"), CodeSent)
		grader.err = nil
		if err := svc.Regrade("saved"); err != nil {
			t.Fatal(err)
		}
		svc.wg.Wait()
		if grader.calls != 2 {
			t.Fatalf("calls=%d, want 2", grader.calls)
		}
	})
}

func TestResumePausedCloseWaitsForWriting(t *testing.T) {
	pass := pausedPass(domain.StageConfirmed)
	release := make(chan struct{})
	anki := &pausedBlockingAnki{fakeAnki: newFakeAnki(items()...), release: release}
	svc, err := New(anki, &fakeGrader{}, &memStore{pass: pass}, Options{PauseOnLoad: true, Log: zerolog.Nop(), Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close(context.Background()) })
	if err := svc.Resume("saved"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := svc.Close(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("Close with pending write=%v, want context cancellation", err)
	}
	close(release)
	if err := svc.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if pass.Status == nil || !pass.Status.Written || len(anki.answered) != 1 {
		t.Fatalf("Close interrupted resumed write: status=%+v answers=%v", pass.Status, anki.answered)
	}
}
