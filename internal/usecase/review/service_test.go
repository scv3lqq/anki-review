package review

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/scv3lqq/anki-review/internal/domain"
)

// ---------- fakes ----------

type fakeAnki struct {
	mu        sync.Mutex
	decks     []string
	due       map[string][]domain.Item
	states    map[int64]domain.CardState
	answered  [][]domain.CardAnswer
	syncs     int
	answerErr error
	dueCount  int
}

func newFakeAnki(items ...domain.Item) *fakeAnki {
	f := &fakeAnki{decks: []string{"Tech", "Tech::Go", "English"}, due: map[string][]domain.Item{"Tech": items}, states: map[int64]domain.CardState{}}
	for _, it := range items {
		f.states[it.CardID] = domain.CardState{Reps: it.Reps, Mod: it.Mod}
	}
	return f
}

func (f *fakeAnki) Decks(context.Context) ([]domain.Deck, error) {
	return []domain.Deck{{Name: "English", Due: 1}, {Name: "Tech", Due: 2}}, nil
}

func (f *fakeAnki) DeckNames(context.Context) ([]string, error) { return f.decks, nil }

func (f *fakeAnki) DueCards(_ context.Context, deck string, limit int) ([]domain.Item, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	items := f.due[deck]
	return items[:min(limit, len(items))], nil
}

func (f *fakeAnki) DueCount(context.Context, string) (int, error) { return f.dueCount, nil }

func (f *fakeAnki) CardStates(context.Context, []int64) (map[int64]domain.CardState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.states, nil
}

func (f *fakeAnki) Answer(_ context.Context, answers []domain.CardAnswer) ([]bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.answerErr != nil {
		err := f.answerErr
		f.answerErr = nil
		return nil, err
	}
	f.answered = append(f.answered, answers)
	ok := make([]bool, len(answers))
	for i := range ok {
		ok[i] = true
	}
	return ok, nil
}

func (f *fakeAnki) Sync(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.syncs++
	return nil
}

// fakeGrader grades every answer Good, fails while err is set, or blocks until release is closed.
type fakeGrader struct {
	mu      sync.Mutex
	err     error
	release chan struct{}
	calls   int
}

func (g *fakeGrader) Grade(ctx context.Context, _ domain.Kind, items []domain.Item, _ map[int]string) ([]domain.Result, error) {
	g.mu.Lock()
	g.calls++
	err, release := g.err, g.release
	g.mu.Unlock()
	if release != nil {
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if err != nil {
		return nil, err
	}
	results := make([]domain.Result, len(items))
	for i, it := range items {
		results[i] = domain.Result{N: it.N, Grade: domain.Good, Ref: "key", Topic: "claude topic"}
	}
	return results, nil
}

type memStore struct {
	mu   sync.Mutex
	pass *domain.Pass
}

func (m *memStore) Load() (*domain.Pass, error) { return m.pass, nil }

func (m *memStore) Save(p *domain.Pass) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pass = p
	return nil
}

// ---------- helpers ----------

var now = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

func items() []domain.Item {
	return []domain.Item{
		{N: 1, CardID: 10, Reps: 2, Mod: 100, Deck: "Tech::Go", Topic: "Go Channels", Question: "q1", Back: "k1", Intervals: map[domain.Grade]string{domain.Good: "1d"}},
		{N: 2, CardID: 20, Reps: 0, Mod: 200, Deck: "Tech::Go", Topic: "Go Maps", Question: "q2", Back: "k2", IsNew: true},
		{N: 3, CardID: 30, Reps: 5, Mod: 300, Deck: "Tech::Go", Topic: "Go Defer", Question: "q3", Back: "k3"},
	}
}

type env struct {
	svc    *Service
	anki   *fakeAnki
	grader *fakeGrader
	store  *memStore
	clock  *time.Time
}

func newEnv(t *testing.T, store *memStore) *env {
	t.Helper()
	if store == nil {
		store = &memStore{}
	}
	e := &env{anki: newFakeAnki(items()...), grader: &fakeGrader{}, store: store, clock: new(time.Time)}
	*e.clock = now
	n := 0
	svc, err := New(e.anki, e.grader, e.store, Options{
		KnowledgeDecks: []string{"Tech"},
		Sync:           true,
		Log:            zerolog.Nop(),
		Now:            func() time.Time { return *e.clock },
		NewSession: func() string {
			n++
			return fmt.Sprintf("s%d", n)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	e.svc = svc
	t.Cleanup(func() { _ = svc.Close(context.Background()) })
	return e
}

// settle waits for grading and writing started so far.
func (e *env) settle() { e.svc.wg.Wait() }

func (e *env) start(t *testing.T) string {
	t.Helper()
	if err := e.svc.Start(context.Background(), "Tech", 10); err != nil {
		t.Fatal(err)
	}
	return e.svc.Cards().Session
}

func (e *env) graded(t *testing.T) string {
	t.Helper()
	session := e.start(t)
	if err := e.svc.SubmitAnswers(session, map[int]string{1: "a1", 2: "?", 3: " - "}); err != nil {
		t.Fatal(err)
	}
	e.settle()
	return session
}

func wantConflict(t *testing.T, err error, code string) {
	t.Helper()
	var c *ConflictError
	if !errors.As(err, &c) || c.Code != code {
		t.Fatalf("error = %v, want conflict %q", err, code)
	}
}

// ---------- tests ----------

func TestFullPass(t *testing.T) {
	e := newEnv(t, nil)
	session := e.start(t)

	cards := e.svc.Cards()
	if cards.Stage != domain.StageInput || cards.Due != 3 || cards.Fresh != 1 || cards.Kind != domain.KindKnowledge || cards.Deck != "Tech" {
		t.Fatalf("Cards() = %+v", cards)
	}
	if e.anki.syncs != 1 {
		t.Errorf("syncs before pass = %d, want 1", e.anki.syncs)
	}

	if err := e.svc.SubmitAnswers(session, map[int]string{1: "a1", 2: "?", 3: " - "}); err != nil {
		t.Fatal(err)
	}
	e.settle()
	res, err := e.svc.Results()
	if err != nil {
		t.Fatal(err)
	}
	if got := len(res.Items); got != 3 || !res.Items[2].Skipped || res.Items[1].Answer != "?" {
		t.Fatalf("Results().Items = %+v", res.Items)
	}
	first := res.Items[0]
	if first.Grade != domain.Good || first.Ref != "key" || first.Topic != "Go Channels" || first.Intervals[domain.Good] != "1d" {
		t.Errorf("graded item = %+v; knowledge topic must come from the card", first)
	}

	e.anki.dueCount = 4
	if err := e.svc.Confirm(session, map[int]domain.Grade{2: domain.Again}); err != nil {
		t.Fatal(err)
	}
	e.settle()
	want := [][]domain.CardAnswer{{{CardID: 10, Ease: 3}, {CardID: 20, Ease: 1}}}
	if !reflect.DeepEqual(e.anki.answered, want) {
		t.Errorf("answered = %v, want %v", e.anki.answered, want)
	}
	if st := e.svc.Status(); st == nil || !st.Written || st.More != 4 || st.Message != "" {
		t.Errorf("Status() = %+v", st)
	}
	if e.svc.Cards().Stage != domain.StageWritten || e.anki.syncs != 2 {
		t.Errorf("stage = %s, syncs = %d", e.svc.Cards().Stage, e.anki.syncs)
	}
	if e.store.pass == nil || e.store.pass.Stage() != domain.StageWritten {
		t.Error("written pass was not saved")
	}
}

func TestStartErrors(t *testing.T) {
	tests := []struct {
		name    string
		deck    string
		prepare func(e *env)
		check   func(t *testing.T, err error)
	}{
		{"unknown deck", "Nope", nil, func(t *testing.T, err error) {
			if !errors.Is(err, ErrUnknownDeck) {
				t.Fatalf("error = %v, want ErrUnknownDeck", err)
			}
		}},
		{"nothing due", "English", nil, func(t *testing.T, err error) { wantConflict(t, err, CodeEmpty) }},
		{"pass in progress", "Tech", func(e *env) { _ = e.svc.Start(context.Background(), "Tech", 1) }, func(t *testing.T, err error) {
			wantConflict(t, err, CodeBusy)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t, nil)
			if tt.prepare != nil {
				tt.prepare(e)
			}
			tt.check(t, e.svc.Start(context.Background(), tt.deck, 10))
		})
	}
}

func TestWrongSession(t *testing.T) {
	e := newEnv(t, nil)
	e.start(t)
	calls := map[string]func() error{
		"answers": func() error { return e.svc.SubmitAnswers("old", nil) },
		"regrade": func() error { return e.svc.Regrade("old") },
		"confirm": func() error { return e.svc.Confirm("old", nil) },
		"more":    func() error { return e.svc.More(context.Background(), "old", 1) },
		"reset":   func() error { return e.svc.Reset("old") },
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) { wantConflict(t, call(), CodeSession) })
	}
}

func TestStageOrder(t *testing.T) {
	e := newEnv(t, nil)
	session := e.start(t)
	wantConflict(t, e.svc.Confirm(session, nil), CodeSent)
	wantConflict(t, e.svc.More(context.Background(), session, 1), CodeEmpty)
	if _, err := e.svc.Results(); !errors.Is(err, ErrNotReady) {
		t.Errorf("Results() before answers: %v, want ErrNotReady", err)
	}
	if err := e.svc.SubmitAnswers(session, map[int]string{1: "a"}); err != nil {
		t.Fatal(err)
	}
	wantConflict(t, e.svc.SubmitAnswers(session, map[int]string{1: "a"}), CodeSent)
}

func TestGradingFailureAndRegrade(t *testing.T) {
	e := newEnv(t, nil)
	e.grader.err = errors.New("claude: usage limit reached")
	session := e.graded(t)

	var failed *GradingFailedError
	if _, err := e.svc.Results(); !errors.As(err, &failed) || !strings.Contains(failed.Reason, "usage limit") {
		t.Fatalf("Results() error = %v, want GradingFailedError", err)
	}
	if got := e.svc.Cards().GradeError; got == "" {
		t.Error("Cards().GradeError is empty after a failure")
	}

	e.grader.mu.Lock()
	e.grader.err = nil
	e.grader.mu.Unlock()
	if err := e.svc.Regrade(session); err != nil {
		t.Fatal(err)
	}
	e.settle()
	if _, err := e.svc.Results(); err != nil {
		t.Fatalf("Results() after regrade: %v", err)
	}
	wantConflict(t, e.svc.Regrade(session), CodeSent)
}

func TestWriteRefusedThenRetried(t *testing.T) {
	e := newEnv(t, nil)
	session := e.graded(t)
	e.anki.answerErr = fmt.Errorf("answerCards: %w", domain.ErrQueueBusy)

	if err := e.svc.Confirm(session, nil); err != nil {
		t.Fatal(err)
	}
	e.settle()
	st := e.svc.Status()
	if st == nil || st.Written || !st.Retry || st.Message != msgQueueBusy {
		t.Fatalf("Status() = %+v, want a retryable refusal", st)
	}
	if got := e.svc.Cards().Stage; got != domain.StageResults {
		t.Fatalf("stage after refusal = %s, want results", got)
	}

	if err := e.svc.Confirm(session, nil); err != nil {
		t.Fatal(err)
	}
	e.settle()
	if st := e.svc.Status(); st == nil || !st.Written {
		t.Errorf("Status() after retry = %+v", st)
	}
}

func TestWriteSkipsCardsReviewedElsewhere(t *testing.T) {
	e := newEnv(t, nil)
	session := e.graded(t)
	e.anki.states[10] = domain.CardState{Reps: 3, Mod: 150}

	if err := e.svc.Confirm(session, nil); err != nil {
		t.Fatal(err)
	}
	e.settle()
	if want := [][]domain.CardAnswer{{{CardID: 20, Ease: 3}}}; !reflect.DeepEqual(e.anki.answered, want) {
		t.Errorf("answered = %v, want %v", e.anki.answered, want)
	}
	if st := e.svc.Status(); !st.Written || !strings.Contains(st.Message, "skipped 1") {
		t.Errorf("Status() = %+v", st)
	}
}

func TestMore(t *testing.T) {
	e := newEnv(t, nil)
	session := e.graded(t)
	e.anki.dueCount = 1
	if err := e.svc.Confirm(session, nil); err != nil {
		t.Fatal(err)
	}
	e.settle()

	if err := e.svc.More(context.Background(), session, 2); err != nil {
		t.Fatal(err)
	}
	cards := e.svc.Cards()
	if cards.Session == session || cards.Stage != domain.StageInput || cards.Due != 2 {
		t.Fatalf("Cards() after More = %+v", cards)
	}
}

func TestMoreWhenNothingLeft(t *testing.T) {
	e := newEnv(t, nil)
	session := e.graded(t)
	e.anki.dueCount = 2
	if err := e.svc.Confirm(session, nil); err != nil {
		t.Fatal(err)
	}
	e.settle()
	e.anki.due["Tech"] = nil

	wantConflict(t, e.svc.More(context.Background(), session, 0), CodeEmpty)
	if st := e.svc.Status(); st.More != 0 {
		t.Errorf("Status().More = %d, want 0 once the deck is empty", st.More)
	}
}

func TestResetAndExpire(t *testing.T) {
	e := newEnv(t, nil)
	session := e.start(t)
	if err := e.svc.Reset(session); err != nil {
		t.Fatal(err)
	}
	if got := e.svc.Cards().Stage; got != domain.StageIdle {
		t.Fatalf("stage after reset = %s", got)
	}

	e.start(t)
	*e.clock = now.Add(passTTL + time.Minute)
	if got := e.svc.Cards().Stage; got != domain.StageIdle {
		t.Errorf("stage after TTL = %s, want idle", got)
	}
	if e.store.pass != nil {
		t.Error("expired pass is still saved")
	}
}

func TestResetWhileGrading(t *testing.T) {
	e := newEnv(t, nil)
	e.grader.release = make(chan struct{})
	session := e.start(t)
	if err := e.svc.SubmitAnswers(session, map[int]string{1: "a"}); err != nil {
		t.Fatal(err)
	}
	wantConflict(t, e.svc.Reset(session), CodeBusy)
	close(e.grader.release)
	e.settle()
}

func TestResume(t *testing.T) {
	tests := []struct {
		name  string
		pass  func() *domain.Pass
		check func(t *testing.T, e *env)
	}{
		{
			name: "answers without grades are graded again",
			pass: func() *domain.Pass {
				p := &domain.Pass{Session: "old", Deck: "Tech", Kind: domain.KindKnowledge, CreatedAt: now, Items: items()}
				p.SetAnswers(map[int]string{1: "a"})
				return p
			},
			check: func(t *testing.T, e *env) {
				if _, err := e.svc.Results(); err != nil || e.grader.calls != 1 {
					t.Errorf("Results() = %v after %d grader calls", err, e.grader.calls)
				}
			},
		},
		{
			name: "confirmed grades are written",
			pass: func() *domain.Pass {
				p := &domain.Pass{Session: "old", Deck: "Tech", Kind: domain.KindKnowledge, CreatedAt: now, Items: items()}
				p.SetAnswers(map[int]string{1: "a"})
				p.Results = []domain.Result{{N: 1, Grade: domain.Hard}}
				p.Confirm(nil)
				return p
			},
			check: func(t *testing.T, e *env) {
				if want := [][]domain.CardAnswer{{{CardID: 10, Ease: 2}}}; !reflect.DeepEqual(e.anki.answered, want) {
					t.Errorf("answered = %v, want %v", e.anki.answered, want)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t, &memStore{pass: tt.pass()})
			e.settle()
			tt.check(t, e)
		})
	}
}

// A shutdown in the middle of grading must not record a failure: the pass is graded after the restart.
func TestCloseDuringGrading(t *testing.T) {
	e := newEnv(t, nil)
	e.grader.release = make(chan struct{})
	session := e.start(t)
	if err := e.svc.SubmitAnswers(session, map[int]string{1: "a"}); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if p := e.store.pass; p.Stage() != domain.StageChecking || p.GradeError != "" {
		t.Errorf("saved pass: stage %s, grade error %q", p.Stage(), p.GradeError)
	}
}

func TestDecksGetKind(t *testing.T) {
	e := newEnv(t, nil)
	decks, err := e.svc.Decks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if decks[0].Kind != domain.KindLanguage || decks[1].Kind != domain.KindKnowledge {
		t.Errorf("Decks() = %+v", decks)
	}
}
