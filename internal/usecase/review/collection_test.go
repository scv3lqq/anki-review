package review

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/scv3lqq/anki-review/internal/domain"
)

func TestWithCollectionRejectsActiveStagesAndWork(t *testing.T) {
	for _, stage := range []string{"idle", "input", "checking", "results", "confirmed", "written", "taking", "grading", "applying"} {
		t.Run(stage, func(t *testing.T) {
			e := newEnv(t, nil)
			s := e.svc
			s.mu.Lock()
			switch stage {
			case "input":
				s.pass = &domain.Pass{}
			case "checking":
				s.pass = &domain.Pass{Answers: []domain.Answer{}}
			case "results":
				s.pass = &domain.Pass{Results: []domain.Result{}}
			case "confirmed":
				s.pass = &domain.Pass{Confirmed: []domain.Confirmed{}}
			case "written":
				s.pass = &domain.Pass{Status: &domain.Status{Written: true}}
			case "taking":
				s.taking = true
			case "grading":
				s.grading = true
			case "applying":
				s.applying = true
			}
			before := s.pass
			s.mu.Unlock()
			called := false
			err := s.WithCollection(context.Background(), func() error { called = true; return nil })
			allowed := stage == "idle" || stage == "written"
			if allowed && err != nil {
				t.Fatal(err)
			}
			if !allowed {
				wantConflict(t, err, CodeBusy)
			}
			if called != allowed || s.pass != before {
				t.Fatal("collection mutation changed pass or ran while busy")
			}
		})
	}
}

func TestCollectionPreferencesTransactionalAndCopied(t *testing.T) {
	e := newEnv(t, nil)
	decks := []string{"English"}
	injected := errors.New("save failed")
	if err := e.svc.UpdateKnowledgeDecks(context.Background(), decks, func() error { return injected }); !errors.Is(err, injected) {
		t.Fatalf("save error = %v", err)
	}
	list, err := e.svc.Decks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if list[0].Kind != domain.KindLanguage {
		t.Fatal("failed save changed preference")
	}
	if err := e.svc.UpdateKnowledgeDecks(context.Background(), decks, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	decks[0] = "Tech"
	list, err = e.svc.Decks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if list[0].Kind != domain.KindKnowledge {
		t.Fatal("preference aliases caller slice")
	}
	e.start(t)
	if err := e.svc.SetKnowledgeDecks([]string{"Tech"}); err == nil {
		t.Fatal("preferences changed active pass")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := newEnv(t, nil).svc.WithCollection(ctx, func() error { t.Fatal("canceled mutation ran"); return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled mutation = %v", err)
	}
}

func TestCollectionMutationSerializesStartAndDeckPreferenceReads(t *testing.T) {
	e := newEnv(t, nil)
	entered, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- e.svc.WithCollection(context.Background(), func() error { close(entered); <-release; return nil })
	}()
	<-entered
	started := make(chan error, 1)
	go func() { started <- e.svc.Start(context.Background(), "Tech", 1) }()
	select {
	case err := <-started:
		t.Fatalf("start bypassed collection lock: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := <-started; err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Reset(e.svc.Cards().Session); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 3 {
		wg.Go(func() {
			for range 30 {
				if _, err := e.svc.Decks(context.Background()); err != nil {
					t.Error(err)
				}
			}
		})
	}
	for range 30 {
		if err := e.svc.SetKnowledgeDecks([]string{"Tech"}); err != nil {
			t.Fatal(err)
		}
	}
	wg.Wait()
}

type textOnlyGrader struct{ t *testing.T }

func (g textOnlyGrader) Grade(_ context.Context, _ domain.Kind, items []domain.Item, _ map[int]string) ([]domain.Result, error) {
	for _, it := range items {
		if it.QuestionHTML != "" || it.BackHTML != "" {
			g.t.Error("imported HTML reached grader")
		}
	}
	return []domain.Result{{N: 1, Grade: domain.Good}}, nil
}

func TestGraderReceivesOnlyPlainText(t *testing.T) {
	e := newEnv(t, nil)
	e.anki.due["Tech"] = []domain.Item{{N: 1, Question: "question", Back: "answer", QuestionHTML: "<b>question</b>", BackHTML: "<b>answer</b>"}}
	e.svc.grader = textOnlyGrader{t: t}
	session := e.start(t)
	if err := e.svc.SubmitAnswers(session, map[int]string{1: "answer"}); err != nil {
		t.Fatal(err)
	}
	e.settle()
	if got, err := e.svc.Results(); err != nil || got.Items[0].BackHTML == "" {
		t.Fatal("stored HTML removed from pass")
	}
}
