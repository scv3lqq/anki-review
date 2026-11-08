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
	msgQueueBusy = "A deck's study screen is open in Anki: go back to the deck list there and press Save to Anki again"
)

// Options tune a Service; zero values get defaults.
type Options struct {
	// KnowledgeDecks are top-level decks graded by meaning; the rest are language decks.
	KnowledgeDecks []string
	// PauseOnLoad requires explicit Resume before interrupted grading or writes restart.
	PauseOnLoad bool
	// Sync with Anki's sync server before taking cards and after writing grades.
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
	paused   bool
	closed   bool

	// gradeCtx is canceled by Close: grading in flight stops and runs again after the restart
	gradeCtx    context.Context
	cancelGrade context.CancelFunc
	wg          sync.WaitGroup
}

// New loads the saved pass, resuming interrupted work unless PauseOnLoad is enabled.
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
	opts.KnowledgeDecks = slices.Clone(opts.KnowledgeDecks)
	s := &Service{anki: anki, grader: grader, store: store, opts: opts, pass: pass}
	s.gradeCtx, s.cancelGrade = context.WithCancel(context.Background())

	s.mu.Lock()
	defer s.mu.Unlock()
	switch pass.Stage() {
	case domain.StageChecking:
		if pass.GradeError == "" {
			if opts.PauseOnLoad {
				s.paused = true
			} else {
				s.startGradingLocked()
			}
		}
	case domain.StageConfirmed:
		if opts.PauseOnLoad {
			s.paused = true
		} else {
			s.startWritingLocked()
		}
	}
	return s, nil
}

// Close stops grading and waits for an Anki write in flight: a write is finished, never torn.
func (s *Service) Close(ctx context.Context) error {
	s.mu.Lock()
	s.closed = true
	s.cancelGrade()
	s.mu.Unlock()
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
	s.mu.Lock()
	knowledgeDecks := slices.Clone(s.opts.KnowledgeDecks)
	s.mu.Unlock()
	decks, err := s.anki.Decks(ctx)
	if err != nil {
		return nil, fmt.Errorf("review: decks: %w", err)
	}
	for i := range decks {
		decks[i].Kind = domain.KindOf(decks[i].Name, knowledgeDecks)
	}
	return decks, nil
}

// Cards returns the current pass without answer keys.
func (s *Service) Cards() CardsView {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireLocked()
	view := cardsView(s.pass, s.grading)
	view.Paused = s.paused
	return view
}

// Results returns graded results or a paused marker; ErrNotReady while grading, GradingFailedError after a failure.
func (s *Service) Results() (ResultsView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireLocked()
	switch s.pass.Stage() {
	case domain.StageResults, domain.StageConfirmed, domain.StageWritten:
		view := resultsView(s.pass)
		view.Paused = s.paused
		return view, nil
	case domain.StageChecking:
		if s.paused {
			return ResultsView{Session: s.pass.Session, Paused: true, Items: []ResultItem{}}, nil
		}
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
		return conflict(CodeBusy, "a batch is already in progress")
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
	if s.pass.Stage() != domain.StageWritten {
		s.mu.Unlock()
		return conflict(CodeEmpty, "this batch isn't saved yet or no cards are left")
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
		return conflict(CodeSent, "answers already sent")
	}
	s.pass.SetAnswers(answers)
	err := s.saveLocked()
	s.startGradingLocked()
	return err
}

// Resume explicitly restarts an interrupted pass without changing its answers or confirmed grades.
func (s *Service) Resume(session string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return conflict(CodeBusy, "review service is closed")
	}
	s.expireLocked()
	if err := s.checkSessionLocked(session); err != nil {
		return err
	}
	switch s.pass.Stage() {
	case domain.StageChecking:
		if s.grading {
			return nil
		}
		if s.pass.GradeError == "" {
			s.startGradingLocked()
			return nil
		}
	case domain.StageConfirmed:
		if !s.applying {
			s.startWritingLocked()
		}
		return nil
	}
	return conflict(CodeSent, "this batch has no interrupted operation")
}

// Regrade runs grading again after a failure.
func (s *Service) Regrade(session string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkSessionLocked(session); err != nil {
		return err
	}
	if s.pass.Stage() != domain.StageChecking || s.grading || s.pass.GradeError == "" {
		return conflict(CodeSent, "grading is already running")
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
		return conflict(CodeSent, "grades already sent")
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
		return conflict(CodeBusy, "wait: grading or saving is in progress")
	}
	s.opts.Log.Info().Str("session", s.pass.Session).Msg("pass dropped")
	s.pass = nil
	s.paused = false
	return s.saveLocked()
}

func (s *Service) checkSessionLocked(session string) error {
	if s.pass == nil || s.pass.Session != session {
		return conflict(CodeSession, "another session")
	}
	return nil
}

func (s *Service) expireLocked() {
	if s.pass == nil || s.grading || s.applying || s.taking || s.opts.Now().Sub(s.pass.CreatedAt) <= passTTL {
		return
	}
	s.opts.Log.Info().Str("session", s.pass.Session).Msg("pass expired")
	s.pass = nil
	s.paused = false
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
		return conflict(CodeSent, "cards are already being taken")
	}
	s.taking = true
	knowledgeDecks := slices.Clone(s.opts.KnowledgeDecks)
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
		return conflict(CodeSession, "another session")
	}
	if len(items) == 0 {
		if from != nil && from.Status != nil {
			from.Status.More = 0
			_ = s.saveLocked()
		}
		return conflict(CodeEmpty, "nothing to review in this deck")
	}
	s.pass = &domain.Pass{
		Session:   s.opts.NewSession(),
		Deck:      deck,
		Kind:      domain.KindOf(deck, knowledgeDecks),
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
	s.paused = false
	p := s.pass
	kind, items, answers := p.Kind, p.Answered(), p.AnswerMap()
	// Rendered card content is for the browser only; grader ports receive plain text.
	for i := range items {
		items[i].QuestionHTML, items[i].BackHTML = "", ""
	}
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
	s.paused = false
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
		notes = append(notes, fmt.Sprintf("skipped %d: already reviewed or changed", skipped))
	}
	if failed := len(plan) - countTrue(ok); failed > 0 {
		notes = append(notes, fmt.Sprintf("Anki rejected %d", failed))
	}
	if s.opts.Sync {
		if err := s.anki.Sync(ctx); err != nil {
			notes = append(notes, "sync failed: "+err.Error())
		}
	}
	more, err := s.anki.DueCount(ctx, deck)
	if err != nil {
		notes = append(notes, "couldn't count the cards left: "+err.Error())
	}
	return domain.Status{Written: true, More: more, Message: strings.Join(notes, "; ")}
}

func refused(err error) domain.Status {
	return domain.Status{Retry: true, Message: "Not saved to Anki: " + err.Error()}
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

// WithCollection serializes collection changes with review mutations and permits only idle/written
// passes. The callback runs under the service mutex and must not call other Service methods.
func (s *Service) WithCollection(ctx context.Context, change func() error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("review: collection: %w", err)
	}
	if s.taking || s.grading || s.applying {
		return conflict(CodeBusy, "wait: a review operation is in progress")
	}
	if stage := s.pass.Stage(); stage != domain.StageIdle && stage != domain.StageWritten {
		return conflict(CodeBusy, "a batch is already in progress")
	}
	if err := change(); err != nil {
		return fmt.Errorf("review: collection: %w", err)
	}
	return nil
}

// SetKnowledgeDecks changes the classification for subsequent passes, copying the caller's slice.
func (s *Service) SetKnowledgeDecks(decks []string) error {
	return s.UpdateKnowledgeDecks(context.Background(), decks, func() error { return nil })
}

// UpdateKnowledgeDecks persists preferences before publishing them, under the collection lock.
// The save callback must not call Service methods.
func (s *Service) UpdateKnowledgeDecks(ctx context.Context, decks []string, save func() error) error {
	return s.WithCollection(ctx, func() error {
		if err := save(); err != nil {
			return err
		}
		s.opts.KnowledgeDecks = slices.Clone(decks)
		return nil
	})
}
