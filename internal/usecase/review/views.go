package review

import "github.com/scv3lqq/anki-review/internal/domain"

// CardView is a card as the page sees it before grading: no answer key.
type CardView struct {
	N        int    `json:"n"`
	Question string `json:"question"`
	IsNew    bool   `json:"isNew"`
	Deck     string `json:"deck"`
}

// CardsView is the current pass for the answer screen.
type CardsView struct {
	Stage      domain.Stage    `json:"stage"`
	Session    string          `json:"session,omitempty"`
	Kind       domain.Kind     `json:"kind,omitempty"`
	Deck       string          `json:"deck,omitempty"`
	Due        int             `json:"due"`
	Fresh      int             `json:"fresh"`
	Cards      []CardView      `json:"cards"`
	Answers    []domain.Answer `json:"answers"`
	GradeError string          `json:"gradeError,omitempty"`
}

// ResultItem is a card with its answer and, unless skipped, the grader's verdict and the card's back.
// A skipped card stays due, so its back is withheld.
type ResultItem struct {
	N         int                     `json:"n"`
	Question  string                  `json:"question"`
	IsNew     bool                    `json:"isNew"`
	Answer    string                  `json:"answer"`
	Skipped   bool                    `json:"skipped,omitempty"`
	Grade     domain.Grade            `json:"grade,omitempty"`
	Mistake   string                  `json:"mistake,omitempty"`
	Back      string                  `json:"back,omitempty"`
	Note      string                  `json:"note,omitempty"`
	Topic     string                  `json:"topic,omitempty"`
	Intervals map[domain.Grade]string `json:"intervals,omitempty"`
}

// ResultsView is the graded pass for the results screen.
type ResultsView struct {
	Session   string               `json:"session"`
	Confirmed map[int]domain.Grade `json:"confirmed"`
	Status    *domain.Status       `json:"status"`
	Items     []ResultItem         `json:"items"`
}

func cardsView(p *domain.Pass, grading bool) CardsView {
	if p == nil {
		return CardsView{Stage: domain.StageIdle, Cards: []CardView{}}
	}
	v := CardsView{Stage: p.Stage(), Session: p.Session, Kind: p.Kind, Deck: p.Deck, Due: len(p.Items), Answers: p.Answers}
	if !grading {
		v.GradeError = p.GradeError
	}
	v.Cards = make([]CardView, len(p.Items))
	for i, it := range p.Items {
		v.Cards[i] = CardView{N: it.N, Question: it.Question, IsNew: it.IsNew, Deck: it.Deck}
		if it.IsNew {
			v.Fresh++
		}
	}
	return v
}

func resultsView(p *domain.Pass) ResultsView {
	v := ResultsView{Session: p.Session, Status: p.Status, Items: make([]ResultItem, len(p.Items))}
	if p.Confirmed != nil {
		v.Confirmed = make(map[int]domain.Grade, len(p.Confirmed))
		for _, c := range p.Confirmed {
			v.Confirmed[c.N] = c.Grade
		}
	}
	answers := p.AnswerMap()
	results := make(map[int]domain.Result, len(p.Results))
	for _, r := range p.Results {
		results[r.N] = r
	}
	for i, it := range p.Items {
		item := ResultItem{N: it.N, Question: it.Question, IsNew: it.IsNew, Answer: answers[it.N]}
		r, graded := results[it.N]
		if !graded {
			item.Skipped = true
			v.Items[i] = item
			continue
		}
		topic := r.Topic
		// a knowledge card names its note in the Source field: more exact than Claude's guess
		if p.Kind == domain.KindKnowledge {
			topic = it.Topic
		}
		item.Grade, item.Mistake, item.Back, item.Note, item.Topic, item.Intervals = r.Grade, r.Mistake, it.Back, r.Note, topic, it.Intervals
		v.Items[i] = item
	}
	return v
}
