package grading

import (
	"errors"
	"fmt"

	"strings"

	"github.com/scv3lqq/anki-review/internal/domain"
)

// Attempts bounds retries of malformed structured output.
const Attempts = 2
const maxField = 2000

// ErrIncomplete indicates missing, duplicate or invalid requested grades.
var ErrIncomplete = errors.New("incomplete grades")

// Output is the provider-independent structured grading response.
type Output struct {
	Results []struct {
		N       int    `json:"n"`
		Grade   string `json:"grade"`
		Mistake string `json:"mistake"`
		Note    string `json:"note"`
		Topic   string `json:"topic"`
	} `json:"results"`
}

func clip(s string) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > maxField {
		s = string(r[:maxField])
	}
	return s
}

// Validate requires a valid grade for every item and ignores numbers it didn't ask about.
func Validate(items []domain.Item, out *Output) ([]domain.Result, error) {
	type entry = struct {
		grade                domain.Grade
		mistake, note, topic string
	}
	requested := make(map[int]bool, len(items))
	for _, item := range items {
		requested[item.N] = true
	}
	seen := make(map[int]bool, len(items))
	byN := make(map[int]entry, len(out.Results))
	for _, r := range out.Results {
		if !requested[r.N] {
			continue
		}
		if seen[r.N] {
			return nil, fmt.Errorf("%w: duplicate requested card", ErrIncomplete)
		}
		seen[r.N] = true
		g, ok := domain.ParseGrade(r.Grade)
		if !ok {
			continue
		}
		byN[r.N] = entry{g, r.Mistake, r.Note, r.Topic}
	}
	results := make([]domain.Result, 0, len(items))
	var missing []string
	for _, it := range items {
		e, ok := byN[it.N]
		if !ok {
			missing = append(missing, fmt.Sprint(it.N))
			continue
		}
		results = append(results, domain.Result{N: it.N, Grade: e.grade, Mistake: clip(e.mistake), Note: clip(e.note), Topic: clip(e.topic)})
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("%w: no valid grade for cards %s", ErrIncomplete, strings.Join(missing, ", "))
	}
	return results, nil
}
