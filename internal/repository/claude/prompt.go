package claude

import (
	"github.com/scv3lqq/anki-review/internal/domain"
	"github.com/scv3lqq/anki-review/internal/repository/grading"
)

var schema = grading.Schema

// Prompt renders the shared rubric for compatibility with existing callers.
func Prompt(kind domain.Kind, items []domain.Item, answers map[int]string) (string, error) {
	return grading.Prompt(kind, items, answers)
}
