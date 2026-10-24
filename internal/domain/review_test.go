package domain

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestParseGrade(t *testing.T) {
	tests := []struct {
		in   string
		want Grade
		ok   bool
	}{
		{"Good", Good, true},
		{"again", Again, true},
		{" EASY ", Easy, true},
		{"hard", Hard, true},
		{"", "", false},
		{"Perfect", "", false},
	}
	for _, tt := range tests {
		got, ok := ParseGrade(tt.in)
		if got != tt.want || ok != tt.ok {
			t.Errorf("ParseGrade(%q) = %q, %v; want %q, %v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}

func TestGradeEase(t *testing.T) {
	tests := []struct {
		g    Grade
		want int
	}{{Again, 1}, {Hard, 2}, {Good, 3}, {Easy, 4}}
	for _, tt := range tests {
		if got := tt.g.Ease(); got != tt.want {
			t.Errorf("%s.Ease() = %d, want %d", tt.g, got, tt.want)
		}
	}
}

func TestKindOf(t *testing.T) {
	knowledge := []string{"Tech", "Career"}
	tests := []struct {
		deck string
		want Kind
	}{
		{"Tech", KindKnowledge},
		{"Tech::Go::Channels", KindKnowledge},
		{"Career", KindKnowledge},
		{"English", KindLanguage},
		{"Technology", KindLanguage},
		{"English::Tech", KindLanguage},
	}
	for _, tt := range tests {
		if got := KindOf(tt.deck, knowledge); got != tt.want {
			t.Errorf("KindOf(%q) = %s, want %s", tt.deck, got, tt.want)
		}
	}
}

func TestNormalizeAnswer(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{"trims", "  goroutine \n", "goroutine"},
		{"dash is skip", " - ", ""},
		{"dash inside stays", "a - b", "a - b"},
		{"question mark stays", "?", "?"},
		{"caps runes, not bytes", strings.Repeat("日", MaxAnswerLen+10), strings.Repeat("日", MaxAnswerLen)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeAnswer(tt.in); got != tt.want {
				t.Errorf("NormalizeAnswer() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPassStage(t *testing.T) {
	tests := []struct {
		name string
		pass *Pass
		want Stage
	}{
		{"nil pass", nil, StageIdle},
		{"fresh", &Pass{}, StageInput},
		{"answers sent", &Pass{Answers: []Answer{{N: 1}}}, StageChecking},
		{"grading failed stays checking", &Pass{Answers: []Answer{{N: 1}}, GradeError: "boom"}, StageChecking},
		{"graded, all skipped", &Pass{Answers: []Answer{{N: 1}}, Results: []Result{}}, StageResults},
		{"confirmed", &Pass{Results: []Result{}, Confirmed: []Confirmed{}}, StageConfirmed},
		{"write refused", &Pass{Results: []Result{}, Status: &Status{Retry: true}}, StageResults},
		{"written", &Pass{Results: []Result{}, Confirmed: []Confirmed{}, Status: &Status{Written: true}}, StageWritten},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.pass.Stage(); got != tt.want {
				t.Errorf("Stage() = %s, want %s", got, tt.want)
			}
		})
	}
}

// An empty slice and nil mean different stages, so they must survive the state file.
func TestPassStageSurvivesJSON(t *testing.T) {
	p := &Pass{Answers: []Answer{{N: 1}}, Results: []Result{}}
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var back Pass
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if got := back.Stage(); got != StageResults {
		t.Errorf("Stage() after round trip = %s, want %s", got, StageResults)
	}
}

func TestSetAnswersAndAnswered(t *testing.T) {
	p := &Pass{Items: []Item{{N: 1}, {N: 2}, {N: 3}}}
	p.SetAnswers(map[int]string{1: " defer ", 2: "-", 9: "stray"})

	wantAnswers := []Answer{{N: 1, Text: "defer"}, {N: 2, Text: ""}, {N: 3, Text: ""}}
	if !reflect.DeepEqual(p.Answers, wantAnswers) {
		t.Errorf("Answers = %v, want %v", p.Answers, wantAnswers)
	}
	if got := p.Answered(); len(got) != 1 || got[0].N != 1 {
		t.Errorf("Answered() = %v, want only card 1", got)
	}
}

func TestConfirm(t *testing.T) {
	p := &Pass{
		Results: []Result{{N: 1, Grade: Good}, {N: 2, Grade: Again}},
		Status:  &Status{Retry: true, Message: "busy"},
	}
	p.Confirm(map[int]Grade{2: Hard, 7: Easy})

	want := []Confirmed{{N: 1, Grade: Good}, {N: 2, Grade: Hard}}
	if !reflect.DeepEqual(p.Confirmed, want) {
		t.Errorf("Confirmed = %v, want %v", p.Confirmed, want)
	}
	if p.Status != nil {
		t.Errorf("Status = %v, want nil after a new confirm", p.Status)
	}
}

func TestPlanAnswers(t *testing.T) {
	items := []Item{
		{N: 1, CardID: 10, Reps: 3, Mod: 100},
		{N: 2, CardID: 20, Reps: 0, Mod: 200},
		{N: 3, CardID: 30, Reps: 5, Mod: 300},
	}
	tests := []struct {
		name        string
		confirmed   []Confirmed
		now         map[int64]CardState
		wantPlan    []CardAnswer
		wantSkipped int
	}{
		{
			name:      "unchanged cards are answered",
			confirmed: []Confirmed{{N: 1, Grade: Good}, {N: 2, Grade: Again}},
			now:       map[int64]CardState{10: {Reps: 3, Mod: 100}, 20: {Reps: 0, Mod: 200}},
			wantPlan:  []CardAnswer{{CardID: 10, Ease: 3}, {CardID: 20, Ease: 1}},
		},
		{
			name:        "reviewed elsewhere is skipped",
			confirmed:   []Confirmed{{N: 1, Grade: Good}},
			now:         map[int64]CardState{10: {Reps: 4, Mod: 150}},
			wantSkipped: 1,
		},
		{
			name:        "edited is skipped",
			confirmed:   []Confirmed{{N: 3, Grade: Easy}},
			now:         map[int64]CardState{30: {Reps: 5, Mod: 301}},
			wantSkipped: 1,
		},
		{
			name:        "deleted card is skipped",
			confirmed:   []Confirmed{{N: 2, Grade: Hard}},
			now:         map[int64]CardState{},
			wantSkipped: 1,
		},
		{
			name:        "unknown number is skipped",
			confirmed:   []Confirmed{{N: 9, Grade: Hard}},
			now:         map[int64]CardState{10: {Reps: 3, Mod: 100}},
			wantSkipped: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan, skipped := PlanAnswers(items, tt.confirmed, tt.now)
			if !reflect.DeepEqual(plan, tt.wantPlan) || skipped != tt.wantSkipped {
				t.Errorf("PlanAnswers() = %v, %d; want %v, %d", plan, skipped, tt.wantPlan, tt.wantSkipped)
			}
		})
	}
}
