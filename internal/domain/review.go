// Package domain holds the review pass and the rules that do not depend on Anki, Claude or HTTP.
package domain

import (
	"errors"
	"slices"
	"strings"
	"time"
)

// Grade is an Anki answer button.
type Grade string

// The four answer buttons, in Anki's order.
const (
	Again Grade = "Again"
	Hard  Grade = "Hard"
	Good  Grade = "Good"
	Easy  Grade = "Easy"
)

// Grades lists the answer buttons in Anki's order.
var Grades = []Grade{Again, Hard, Good, Easy}

// ParseGrade accepts a grade in any letter case.
func ParseGrade(s string) (Grade, bool) {
	for _, g := range Grades {
		if strings.EqualFold(string(g), strings.TrimSpace(s)) {
			return g, true
		}
	}
	return "", false
}

// Ease is the button number AnkiConnect expects: 1 for Again up to 4 for Easy.
func (g Grade) Ease() int {
	return slices.Index(Grades, g) + 1
}

// Kind decides how answers are graded.
type Kind string

const (
	// KindKnowledge cards are graded by meaning, feedback in Russian.
	KindKnowledge Kind = "knowledge"
	// KindLanguage cards test an exact word or collocation.
	KindLanguage Kind = "language"
)

// KindOf classifies a deck by its top-level name.
func KindOf(deck string, knowledgeDecks []string) Kind {
	top, _, _ := strings.Cut(deck, "::")
	if slices.Contains(knowledgeDecks, top) {
		return KindKnowledge
	}
	return KindLanguage
}

var (
	// ErrAnki wraps every failed AnkiConnect call: Anki closed, add-on off, or an error from Anki.
	ErrAnki = errors.New("anki")
	// ErrQueueBusy means Anki rejected answers because a deck's study screen holds its queue.
	ErrQueueBusy = errors.New("anki queue busy")
)

// Deck is one row of Anki's deck list with today's counts.
type Deck struct {
	Name   string `json:"name"`
	Kind   Kind   `json:"kind"`
	Learn  int    `json:"learn"`
	Review int    `json:"review"`
	New    int    `json:"fresh"`
	Due    int    `json:"due"`
}

// Item is a card in a pass. Back is the answer key: it never reaches the page before grading.
type Item struct {
	N      int    `json:"n"`
	CardID int64  `json:"cardId"`
	Mod    int64  `json:"mod"`
	Reps   int    `json:"reps"`
	Deck   string `json:"deck"`
	Topic  string `json:"topic"`
	IsNew  bool   `json:"isNew"`
	// Question and Back are plain text; inline code is kept in backticks.
	Question  string           `json:"question"`
	Back      string           `json:"back"`
	Intervals map[Grade]string `json:"intervals,omitempty"`
}

// CardState is what Anki says about a card right now.
type CardState struct {
	Reps int
	Mod  int64
}

// CardAnswer is one press of an answer button.
type CardAnswer struct {
	CardID int64
	Ease   int
}

// Answer is what the owner typed for card N; empty means skipped, "?" means "I don't remember".
type Answer struct {
	N    int    `json:"n"`
	Text string `json:"answer"`
}

// MaxAnswerLen caps one answer so a pasted wall of text can't blow up the prompt.
const MaxAnswerLen = 5000

// NormalizeAnswer trims an answer; "-" is the skip marker of the list format.
func NormalizeAnswer(s string) string {
	s = strings.TrimSpace(s)
	if s == "-" {
		return ""
	}
	if r := []rune(s); len(r) > MaxAnswerLen {
		s = string(r[:MaxAnswerLen])
	}
	return s
}

// Result is Claude's verdict on one answered card.
type Result struct {
	N       int    `json:"n"`
	Grade   Grade  `json:"grade"`
	Mistake string `json:"mistake"`
	Ref     string `json:"ref"`
	Note    string `json:"note"`
	Topic   string `json:"topic"`
}

// Confirmed is the grade the owner accepted or picked.
type Confirmed struct {
	N     int   `json:"n"`
	Grade Grade `json:"grade"`
}

// Status is the outcome of writing a pass to Anki.
type Status struct {
	Written bool `json:"written"`
	// Retry: nothing was written and the owner may press «Записать в Anki» again.
	Retry   bool   `json:"retry,omitempty"`
	More    int    `json:"more"`
	Message string `json:"message"`
}

// Stage is where a pass is; it is derived from the pass, never stored.
type Stage string

// Stages in the order a pass goes through them.
const (
	StageIdle      Stage = "idle"
	StageInput     Stage = "input"
	StageChecking  Stage = "checking"
	StageResults   Stage = "results"
	StageConfirmed Stage = "confirmed"
	StageWritten   Stage = "written"
)

// Pass is one batch of cards: answered, graded, confirmed, written to Anki.
type Pass struct {
	Session   string    `json:"session"`
	Deck      string    `json:"deck"`
	Kind      Kind      `json:"kind"`
	Limit     int       `json:"limit"`
	CreatedAt time.Time `json:"createdAt"`
	Items     []Item    `json:"items"`
	// Answers holds one entry per item once the page has sent them.
	Answers    []Answer `json:"answers"`
	GradeError string   `json:"gradeError,omitempty"`
	// Results is non-nil once grading is done, empty when every card was skipped.
	Results   []Result    `json:"results"`
	Confirmed []Confirmed `json:"confirmed"`
	Status    *Status     `json:"status"`
}

// Stage of the pass; a nil pass is idle.
func (p *Pass) Stage() Stage {
	switch {
	case p == nil:
		return StageIdle
	case p.Status != nil && p.Status.Written:
		return StageWritten
	case p.Confirmed != nil:
		return StageConfirmed
	case p.Results != nil:
		return StageResults
	case p.Answers != nil:
		return StageChecking
	default:
		return StageInput
	}
}

// AnswerMap maps card number to a non-empty answer.
func (p *Pass) AnswerMap() map[int]string {
	m := make(map[int]string, len(p.Answers))
	for _, a := range p.Answers {
		if a.Text != "" {
			m[a.N] = a.Text
		}
	}
	return m
}

// Answered lists the items that have an answer, in pass order.
func (p *Pass) Answered() []Item {
	answers := p.AnswerMap()
	var items []Item
	for _, it := range p.Items {
		if _, ok := answers[it.N]; ok {
			items = append(items, it)
		}
	}
	return items
}

// SetAnswers stores one normalized answer per item; cards the page left out count as skipped.
func (p *Pass) SetAnswers(byN map[int]string) {
	p.Answers = make([]Answer, len(p.Items))
	for i, it := range p.Items {
		p.Answers[i] = Answer{N: it.N, Text: NormalizeAnswer(byN[it.N])}
	}
}

// Confirm takes the owner's grades; a graded card the owner didn't touch keeps Claude's grade.
func (p *Pass) Confirm(byN map[int]Grade) {
	p.Confirmed = make([]Confirmed, len(p.Results))
	for i, r := range p.Results {
		g, ok := byN[r.N]
		if !ok {
			g = r.Grade
		}
		p.Confirmed[i] = Confirmed{N: r.N, Grade: g}
	}
	p.Status = nil
}

// PlanAnswers turns confirmed grades into button presses. A card whose reps or mod changed since
// the pass was taken was reviewed or edited elsewhere: answering it again would count it twice.
func PlanAnswers(items []Item, confirmed []Confirmed, now map[int64]CardState) (plan []CardAnswer, skipped int) {
	byN := make(map[int]Item, len(items))
	for _, it := range items {
		byN[it.N] = it
	}
	for _, c := range confirmed {
		it, ok := byN[c.N]
		state, known := now[it.CardID]
		if !ok || !known || state.Reps != it.Reps || state.Mod != it.Mod {
			skipped++
			continue
		}
		plan = append(plan, CardAnswer{CardID: it.CardID, Ease: c.Grade.Ease()})
	}
	return plan, skipped
}
