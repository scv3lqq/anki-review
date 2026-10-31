package grading

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/scv3lqq/anki-review/internal/domain"
)

func TestValidateContract(t *testing.T) {
	tests := []struct {
		name, raw string
		bad       bool
	}{
		{"variants and unknown extras", `{"results":[{"n":1,"grade":" good "},{"n":2,"grade":"HARD"},{"n":9,"grade":"invalid"}]}`, false},
		{"missing", `{"results":[{"n":1,"grade":"Good"}]}`, true},
		{"invalid", `{"results":[{"n":1,"grade":"Good"},{"n":2,"grade":"Perfect"}]}`, true},
		{"duplicate", `{"results":[{"n":1,"grade":"Good"},{"n":1,"grade":"Hard"},{"n":2,"grade":"Good"}]}`, true},
		{"invalid duplicate", `{"results":[{"n":1,"grade":"invalid"},{"n":1,"grade":"Good"},{"n":2,"grade":"Good"}]}`, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out Output
			if err := json.Unmarshal([]byte(tt.raw), &out); err != nil {
				t.Fatal(err)
			}
			got, err := Validate([]domain.Item{{N: 1}, {N: 2}}, &out)
			if tt.bad {
				if !errors.Is(err, ErrIncomplete) {
					t.Fatalf("error = %v", err)
				}
				return
			}
			if err != nil || len(got) != 2 || got[1].Grade != domain.Hard {
				t.Fatalf("got %v, %v", got, err)
			}
		})
	}
}

func TestFeedbackClipsRunes(t *testing.T) {
	var out Output
	raw, _ := json.Marshal(map[string]any{"results": []map[string]any{{"n": 1, "grade": "Good", "note": " " + strings.Repeat("界", 2001) + " "}}})
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	got, err := Validate([]domain.Item{{N: 1}}, &out)
	if err != nil || len([]rune(got[0].Note)) != 2000 {
		t.Fatalf("result=%v error=%v", got, err)
	}
}
