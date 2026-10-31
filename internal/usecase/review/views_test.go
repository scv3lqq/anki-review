package review

import (
	"testing"

	"github.com/scv3lqq/anki-review/internal/domain"
)

func TestResultsViewKeyIsCardBack(t *testing.T) {
	tests := []struct {
		name     string
		kind     domain.Kind
		back     string
		answer   string
		graded   bool
		wantBack string
	}{
		{"language, Cyrillic multi-line back", domain.KindLanguage, "запрещать\nПример: курить запрещено", "forbid", true, "запрещать\nПример: курить запрещено"},
		{"knowledge, inline code kept", domain.KindKnowledge, "Runs the call at `return`\nArguments are evaluated at once", "runs at return", true, "Runs the call at `return`\nArguments are evaluated at once"},
		{"cloze back as rendered", domain.KindLanguage, "Gap: arrange\nExtra line", "arrange", true, "Gap: arrange\nExtra line"},
		{"skipped card stays due, back withheld", domain.KindKnowledge, "secret", "", false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &domain.Pass{
				Session: "s1",
				Kind:    tt.kind,
				Items:   []domain.Item{{N: 1, Question: "q", Back: tt.back, Topic: "card topic"}},
				Answers: []domain.Answer{{N: 1, Text: tt.answer}},
				Results: []domain.Result{},
			}
			if tt.graded {
				p.Results = []domain.Result{{N: 1, Grade: domain.Hard, Mistake: "m", Note: "model note", Topic: "model topic"}}
			}
			got := resultsView(p).Items[0]
			if got.Back != tt.wantBack {
				t.Errorf("Back = %q, want %q", got.Back, tt.wantBack)
			}
			if got.Skipped == tt.graded {
				t.Errorf("Skipped = %v, want %v", got.Skipped, !tt.graded)
			}
			if tt.graded && got.Note != "model note" {
				t.Errorf("Note = %q, want the grader's note", got.Note)
			}
		})
	}
}
