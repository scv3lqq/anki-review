// Package review runs a review pass: due cards, typed answers, grades from Claude, the owner's
// confirmation, button presses in Anki.
package review

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"github.com/scv3lqq/anki-review/internal/domain"
)

const (
	// an unfinished pass is dropped on the next visit: its cards may have been reviewed elsewhere since
	passTTL      = 12 * time.Hour
	defaultBatch = 25
	maxBatch     = 500
	writeTimeout = 5 * time.Minute
	msgQueueBusy = "Anki держит очередь открытой колоды: выйди в список колод и нажми «Записать в Anki» ещё раз"
)

// Options tune a Service; zero values get defaults.
type Options struct {
	// KnowledgeDecks are top-level decks graded by meaning; the rest are language decks.
	KnowledgeDecks []string
	// Sync with AnkiWeb before taking cards and after writing grades.
	Sync       bool
	Log        zerolog.Logger
	Now        func() time.Time
	NewSession func() string
}

// Service holds the one current pass. Slow calls (Anki, Claude) run without the lock.
type Service struct {
	anki   Anki
	grader Grader
	store  Store
	opts   Options

	mu       sync.Mutex
	pass     *domain.Pass
	grading  bool
	applying bool
	taking   bool

	// gradeCtx is canceled by Close: grading in flight stops and runs again after the restart
	gradeCtx    context.Context
	cancelGrade context.CancelFunc
	wg          sync.WaitGroup
}

// New loads the saved pass and resumes a stage that was running when the process stopped.
func New(anki Anki, grader Grader, store Store, opts Options) (*Service, error) {
	pass, err := store.Load()
	if err != nil {
		return nil, fmt.Errorf("review: %w", err)
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.NewSession == nil {
		opts.NewSession = func() string { return "a" + rand.Text()[:12] }
	}
	s := &Service{anki: anki, grader: grader, store: store, opts: opts, pass: pass}
	s.gradeCtx, s.cancelGrade = context.WithCancel(context.Background())

	s.mu.Lock()
	defer s.mu.Unlock()
	switch pass.Stage() {
	case domain.StageChecking:
		if pass.GradeError == "" {
			s.startGradingLocked()
		}
	case domain.StageConfirmed:
		s.startWritingLocked()
	}
	return s, nil
}

// Close stops grading and waits for an Anki write in flight: a write is finished, never torn.
func (s *Service) Close(ctx context.Context) error {
	s.cancelGrade()
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("review: close: %w", ctx.Err())
	}
}

// Decks lists decks with something due.
func (s *Service) Decks(ctx context.Context) ([]domain.Deck, error) {
	decks, err := s.anki.Decks(ctx)
	if err != nil {
		return nil, fmt.Errorf("review: decks: %w", err)
	}
	for i := range decks {
		decks[i].Kind = domain.KindOf(decks[i].Name, s.opts.KnowledgeDecks)
	}
	return decks, nil
}

// Cards returns the current pass without answer keys.
func (s *Service) Cards() CardsView {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireLocked()
	return cardsView(s.pass, s.grading)
}

// Results returns the graded pass; ErrNotReady while grading, GradingFailedError after a failure.
func (s *Service) Results() (ResultsView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireLocked()
	switch s.pass.Stage() {
	case domain.StageResults, domain.StageConfirmed, domain.StageWritten:
		return resultsView(s.pass), nil
	case domain.StageChecking:
		if !s.grading && s.pass.GradeError != "" {
			return ResultsView{}, &GradingFailedError{Reason: s.pass.GradeError}
		}
	}
	return ResultsView{}, ErrNotReady
}

// Status is the outcome of the last write, nil before one.
func (s *Service) Status() *domain.Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pass == nil || s.pass.Status == nil {
		return nil
	}
	st := *s.pass.Status
	return &st
}

// Start takes a pass from a deck; a written pass may be replaced.
func (s *Service) Start(ctx context.Context, deck string, count int) error {
	s.mu.Lock()
	s.expireLocked()
	if st := s.pass.Stage(); st != domain.StageIdle && st != domain.StageWritten {
		s.mu.Unlock()
		return conflict(CodeBusy, "пачка уже идёт")
	}
	from := s.pass
	s.mu.Unlock()
	return s.take(ctx, from, deck, count)
}

// More takes the next pass from the same deck after a written one; count <= 0 repeats its size.
func (s *Service) More(ctx context.Context, session string, count int) error {
	s.mu.Lock()
	if err := s.checkSessionLocked(session); err != nil {
		s.mu.Unlock()
		return err
	}
	if s.pass.Stage() != domain.StageWritten || s.pass.Status.More <= 0 {
		s.mu.Unlock()
		return conflict(CodeEmpty, "эта пачка ещё не записана или карточек не осталось")
	}
	from, deck := s.pass, s.pass.Deck
	if count <= 0 {
		count = s.pass.Limit
	}
	s.mu.Unlock()
	return s.take(ctx, from, deck, count)
}

// SubmitAnswers stores the answers (card number to text) and starts grading.
func (s *Service) SubmitAnswers(session string, answers map[int]string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkSessionLocked(session); err != nil {
		return err
	}
	if s.pass.Stage() != domain.StageInput {
		return conflict(CodeSent, "ответы уже отправлены")
	}
	s.pass.SetAnswers(answers)
	err := s.saveLocked()
	s.startGradingLocked()
	return err
}

// Regrade runs grading again after a failure.
func (s *Service) Regrade(session string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkSessionLocked(session); err != nil {
		return err
	}
	if s.pass.Stage() != domain.StageChecking || s.grading || s.pass.GradeError == "" {
		return conflict(CodeSent, "проверка уже идёт")
	}
	s.pass.GradeError = ""
	err := s.saveLocked()
	s.startGradingLocked()
	return err
}

// Confirm takes the owner's grades (card number to grade; missing ones keep Claude's) and writes them.
func (s *Service) Confirm(session string, grades map[int]domain.Grade) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkSessionLocked(session); err != nil {
		return err
	}
	if s.pass.Stage() != domain.StageResults || s.applying {
		return conflict(CodeSent, "оценки уже отправлены")
	}
	s.pass.Confirm(grades)
	err := s.saveLocked()
	s.startWritingLocked()
	return err
}

// Reset drops the pass and goes back to the deck list.
func (s *Service) Reset(session string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkSessionLocked(session); err != nil {
		return err
	}
	if s.grading || s.applying || s.taking {
		return conflict(CodeBusy, "подожди: идёт проверка или запись")
	}
	s.opts.Log.Info().Str("session", s.pass.Session).Msg("pass dropped")
	s.pass = nil
	return s.saveLocked()
}

func (s *Service) checkSessionLocked(session string) error {
	if s.pass == nil || s.pass.Session != session {
		return conflict(CodeSession, "другая сессия")
	}
	return nil
}

func (s *Service) expireLocked() {
	if s.pass == nil || s.grading || s.applying || s.taking || s.opts.Now().Sub(s.pass.CreatedAt) <= passTTL {
		return
	}
	s.opts.Log.Info().Str("session", s.pass.Session).Msg("pass expired")
	s.pass = nil
	_ = s.saveLocked()
}

func (s *Service) saveLocked() error {
	if err := s.store.Save(s.pass); err != nil {
		s.opts.Log.Error().Err(err).Msg("save state")
		return fmt.Errorf("review: %w", err)
	}
	return nil
}

// take replaces `from` (the pass the caller saw) with a new pass from the deck.
func (s *Service) take(ctx context.Context, from *domain.Pass, deck string, count int) error {
	s.mu.Lock()
	if s.taking {
		s.mu.Unlock()
		return conflict(CodeSent, "пачка уже берётся")
	}
	s.taking = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.taking = false
		s.mu.Unlock()
	}()

	limit := count
	if limit <= 0 {
		limit = defaultBatch
	}
	limit = min(limit, maxBatch)
	items, err := s.dueItems(ctx, deck, limit)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pass != from {
		return conflict(CodeSession, "другая сессия")
	}
	if len(items) == 0 {
		if from != nil && from.Status != nil {
			from.Status.More = 0
			_ = s.saveLocked()
		}
		return conflict(CodeEmpty, "в колоде нечего повторять")
	}
	s.pass = &domain.Pass{
		Session:   s.opts.NewSession(),
		Deck:      deck,
		Kind:      domain.KindOf(deck, s.opts.KnowledgeDecks),
		Limit:     limit,
		CreatedAt: s.opts.Now(),
		Items:     items,
	}
	s.opts.Log.Info().Str("session", s.pass.Session).Int("cards", len(items)).Msg("pass taken")
	return s.saveLocked()
}

func (s *Service) dueItems(ctx context.Context, deck string, limit int) ([]domain.Item, error) {
	names, err := s.anki.DeckNames(ctx)
	if err != nil {
		return nil, fmt.Errorf("review: deck names: %w", err)
	}
	// the name goes into an Anki search: only names Anki itself reports are accepted
	if !slices.Contains(names, deck) {
		return nil, ErrUnknownDeck
	}
	if s.opts.Sync {
		// the cards may have been reviewed on another device since the last sync
		if err := s.anki.Sync(ctx); err != nil {
			s.opts.Log.Warn().Err(err).Msg("sync before pass")
		}
	}
	items, err := s.anki.DueCards(ctx, deck, limit)
	if err != nil {
		return nil, fmt.Errorf("review: due cards: %w", err)
	}
	return items, nil
}

func (s *Service) startGradingLocked() {
	p := s.pass
	kind, items, answers := p.Kind, p.Answered(), p.AnswerMap()
	s.grading = true
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		results, err := s.grader.Grade(s.gradeCtx, kind, items, answers)

		s.mu.Lock()
		defer s.mu.Unlock()
		s.grading = false
		if s.gradeCtx.Err() != nil {
			return
		}
		log := s.opts.Log.With().Str("session", p.Session).Logger()
		if err != nil {
			p.GradeError = err.Error()
			log.Warn().Err(err).Msg("grading failed")
		} else {
			p.Results = results
			log.Info().Int("graded", len(results)).Msg("graded")
		}
		if s.pass == p {
			_ = s.saveLocked()
		}
	}()
}

func (s *Service) startWritingLocked() {
	p := s.pass
	items, confirmed, deck := p.Items, slices.Clone(p.Confirmed), p.Deck
	s.applying = true
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
		defer cancel()
		status := s.write(ctx, items, confirmed, deck)

		s.mu.Lock()
		defer s.mu.Unlock()
		s.applying = false
		p.Status = &status
		// a refused write goes back to the results screen, where it can be pressed again
		if !status.Written {
			p.Confirmed = nil
		}
		s.opts.Log.Info().Str("session", p.Session).Bool("written", status.Written).Int("more", status.More).Str("message", status.Message).Msg("write")
		if s.pass == p {
			_ = s.saveLocked()
		}
	}()
}

func (s *Service) write(ctx context.Context, items []domain.Item, confirmed []domain.Confirmed, deck string) domain.Status {
	ids := make([]int64, len(items))
	for i, it := range items {
		ids[i] = it.CardID
	}
	now, err := s.anki.CardStates(ctx, ids)
	if err != nil {
		return refused(err)
	}
	plan, skipped := domain.PlanAnswers(items, confirmed, now)
	ok, err := s.anki.Answer(ctx, plan)
	if errors.Is(err, domain.ErrQueueBusy) {
		return domain.Status{Retry: true, Message: msgQueueBusy}
	}
	if err != nil {
		return refused(err)
	}

	var notes []string
	if skipped > 0 {
		notes = append(notes, fmt.Sprintf("пропущено %d: их уже повторили или изменили", skipped))
	}
	if failed := len(plan) - countTrue(ok); failed > 0 {
		notes = append(notes, fmt.Sprintf("Anki не принял %d", failed))
	}
	if s.opts.Sync {
		if err := s.anki.Sync(ctx); err != nil {
			notes = append(notes, "синхронизация не прошла: "+err.Error())
		}
	}
	more, err := s.anki.DueCount(ctx, deck)
	if err != nil {
		notes = append(notes, "не узнал, сколько карточек осталось: "+err.Error())
	}
	return domain.Status{Written: true, More: more, Message: strings.Join(notes, "; ")}
}

func refused(err error) domain.Status {
	return domain.Status{Retry: true, Message: "Не записано в Anki: " + err.Error()}
}

func countTrue(ok []bool) int {
	n := 0
	for _, v := range ok {
		if v {
			n++
		}
	}
	return n
}
