package claude

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/scv3lqq/anki-review/internal/domain"
	"github.com/scv3lqq/anki-review/internal/testutil/fakeclaude"
)

func TestMain(m *testing.M) {
	fakeclaude.Main()
	os.Exit(m.Run())
}

func TestGrade(t *testing.T) {
	items := []domain.Item{{N: 1, Question: "q1", Back: "k1"}, {N: 3, Question: "q3", Back: "k3"}}
	answers := map[int]string{1: "a1", 3: "a3"}
	tests := []struct {
		name      string
		mode      string
		timeout   time.Duration
		wantErr   string
		wantCalls int
	}{
		{name: "good", mode: fakeclaude.Good, wantCalls: 1},
		{name: "malformed answer is retried once", mode: fakeclaude.IncompleteOnce, wantCalls: 2},
		{name: "malformed twice fails", mode: fakeclaude.Incomplete, wantErr: "no valid grade for cards 3", wantCalls: 2},
		{name: "error result is not retried", mode: fakeclaude.Error, wantErr: "usage limit reached", wantCalls: 1},
		{name: "crash shows stderr", mode: fakeclaude.Crash, wantErr: "Invalid API key", wantCalls: 1},
		{name: "timeout", mode: fakeclaude.Slow, timeout: 200 * time.Millisecond, wantErr: "no answer in 200ms", wantCalls: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bin, calls := fakeclaude.Use(t, tt.mode)
			timeout := tt.timeout
			if timeout == 0 {
				timeout = 10 * time.Second
			}
			g := &Grader{Bin: bin, Timeout: timeout, Dir: t.TempDir()}

			results, err := g.Grade(context.Background(), domain.KindKnowledge, items, answers)
			switch {
			case tt.wantErr != "":
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Grade() error = %v, want it to contain %q", err, tt.wantErr)
				}
			case err != nil:
				t.Fatalf("Grade() error = %v", err)
			case len(results) != 2 || results[0].N != 1 || results[1].N != 3 || results[1].Grade != domain.Good:
				t.Errorf("Grade() = %+v", results)
			}
			if got := calls(); got != tt.wantCalls {
				t.Errorf("claude calls = %d, want %d", got, tt.wantCalls)
			}
		})
	}
}

func TestGradeNothingAnswered(t *testing.T) {
	g := &Grader{Bin: "/nonexistent/claude"}
	results, err := g.Grade(context.Background(), domain.KindLanguage, nil, nil)
	if err != nil || results == nil || len(results) != 0 {
		t.Errorf("Grade() = %v, %v; want an empty non-nil slice and no claude call", results, err)
	}
}

func TestArgs(t *testing.T) {
	tests := []struct {
		model string
		want  []string
	}{
		{"", []string{"-p", "--safe-mode", "--tools", "", "--no-session-persistence", "--output-format", "json", "--json-schema", schema}},
		{"sonnet", []string{"-p", "--safe-mode", "--tools", "", "--no-session-persistence", "--output-format", "json", "--json-schema", schema, "--model", "sonnet"}},
	}
	for _, tt := range tests {
		if got := (&Grader{Model: tt.model}).args(); !slices.Equal(got, tt.want) {
			t.Errorf("args() = %q, want %q", got, tt.want)
		}
	}
}

func TestCheckResults(t *testing.T) {
	items := []domain.Item{{N: 1}, {N: 2}}
	type r = map[string]any
	tests := []struct {
		name    string
		results []r
		want    []domain.Result
		wantErr bool
	}{
		{
			name:    "all graded, extra number ignored, fields trimmed",
			results: []r{{"n": 2, "grade": "hard", "ref": " k2 "}, {"n": 1, "grade": "Good"}, {"n": 9, "grade": "Easy"}},
			want:    []domain.Result{{N: 1, Grade: domain.Good}, {N: 2, Grade: domain.Hard, Ref: "k2"}},
		},
		{name: "missing card", results: []r{{"n": 1, "grade": "Good"}}, wantErr: true},
		{name: "bad grade", results: []r{{"n": 1, "grade": "Good"}, {"n": 2, "grade": "Perfect"}}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, _ := json.Marshal(r{"results": tt.results})
			var out gradesOutput
			if err := json.Unmarshal(raw, &out); err != nil {
				t.Fatal(err)
			}
			got, err := checkResults(items, &out)
			if (err != nil) != tt.wantErr {
				t.Fatalf("checkResults() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && !reflect.DeepEqual(got, tt.want) {
				t.Errorf("checkResults() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestPrompt(t *testing.T) {
	items := []domain.Item{
		{N: 1, Topic: "Go Channels", Deck: "Tech::Go", Question: "What does `close` do?", Back: "Marks the channel closed", IsNew: true},
		{N: 4, Topic: "MVCC", Deck: "Tech::Postgres", Question: "xmin?", Back: "Creating transaction id"},
	}
	answers := map[int]string{1: "no more sends\n  receivers drain", 4: "?"}
	tests := []struct {
		kind domain.Kind
		want []string
	}{
		{domain.KindKnowledge, []string{
			"## Cards (2)",
			"### 1 (new)\nTopic: Go Channels\nQuestion: What does `close` do?\nAnswer key: Marks the channel closed\nMy answer: no more sends / receivers drain\n",
			"### 4\nTopic: MVCC",
			"My answer: ?",
		}},
		{domain.KindLanguage, []string{
			"## Cards (2)",
			"### 1 (new)\nDeck: Tech::Go\nQuestion: What does `close` do?\nAnswer key: Marks the channel closed\nMy answer: no more sends / receivers drain\n",
		}},
	}
	for _, tt := range tests {
		t.Run(string(tt.kind), func(t *testing.T) {
			got, err := Prompt(tt.kind, items, answers)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range tt.want {
				if !strings.Contains(got, want) {
					t.Errorf("prompt lacks %q:\n%s", want, got)
				}
			}
			if strings.ContainsRune(got, '\u2014') {
				t.Error("prompt contains an em dash")
			}
		})
	}
}

func TestSchemaGradesMatchDomain(t *testing.T) {
	var s struct {
		Properties struct {
			Results struct {
				Items struct {
					Properties struct {
						Grade struct {
							Enum []domain.Grade `json:"enum"`
						} `json:"grade"`
					} `json:"properties"`
				} `json:"items"`
			} `json:"results"`
		} `json:"properties"`
	}
	if err := json.Unmarshal([]byte(schema), &s); err != nil {
		t.Fatal(err)
	}
	if got := s.Properties.Results.Items.Properties.Grade.Enum; !slices.Equal(got, domain.Grades) {
		t.Errorf("schema grades = %v, want %v", got, domain.Grades)
	}
}

func TestChildEnv(t *testing.T) {
	environ := []string{"PATH=/bin", "CLAUDE_CODE_OAUTH_TOKEN=old", "HOME=/home/x"}
	tests := []struct {
		name  string
		token string
		want  []string
	}{
		{name: "no token", token: "", want: []string{"PATH=/bin", "HOME=/home/x"}},
		{name: "subscription token", token: "sk-ant-oat01-new", want: []string{"PATH=/bin", "HOME=/home/x", "CLAUDE_CODE_OAUTH_TOKEN=sk-ant-oat01-new"}},
		{name: "API key", token: "sk-ant-api03-new", want: []string{"PATH=/bin", "HOME=/home/x", "ANTHROPIC_API_KEY=sk-ant-api03-new"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := childEnv(environ, tt.token); !slices.Equal(got, tt.want) {
				t.Errorf("childEnv() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRedact(t *testing.T) {
	tests := []struct{ in, token, want string }{
		{"Invalid API key sk-ant-api03-x", "sk-ant-api03-x", "Invalid API key [token]"},
		{"no token here", "", "no token here"},
		{"nothing to hide", "sk-ant-oat01-x", "nothing to hide"},
	}
	for _, tt := range tests {
		if got := redact(tt.in, tt.token); got != tt.want {
			t.Errorf("redact(%q, %q) = %q, want %q", tt.in, tt.token, got, tt.want)
		}
	}
}

func TestCheck(t *testing.T) {
	const good = "sk-ant-oat01-good"
	tests := []struct {
		name    string
		mode    string
		token   string
		require string
		wantErr string
	}{
		{name: "answers OK with the token", mode: fakeclaude.Good, token: good, require: good},
		{name: "API key goes through ANTHROPIC_API_KEY", mode: fakeclaude.Good, token: "sk-ant-api03-k", require: "sk-ant-api03-k"},
		{name: "wrong token is refused", mode: fakeclaude.Good, token: "sk-ant-oat01-bad", require: good, wantErr: "Invalid API key"},
		{name: "no token is refused when one is required", mode: fakeclaude.Good, token: "", require: good, wantErr: "Invalid API key"},
		{name: "error result", mode: fakeclaude.Error, token: good, wantErr: "usage limit reached"},
		{name: "crash shows stderr", mode: fakeclaude.Crash, token: good, wantErr: "Invalid API key"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bin, calls := fakeclaude.Use(t, tt.mode)
			if tt.require != "" {
				fakeclaude.RequireToken(t, tt.require)
			}
			g := &Grader{Bin: bin, Timeout: 10 * time.Second, Dir: t.TempDir()}
			err := g.Check(context.Background(), tt.token)
			switch {
			case tt.wantErr != "":
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Check() = %v, want it to contain %q", err, tt.wantErr)
				}
			case err != nil:
				t.Fatalf("Check() = %v", err)
			}
			if tt.token != "" && err != nil && strings.Contains(err.Error(), tt.token) {
				t.Errorf("Check() error carries the token: %v", err)
			}
			if got := calls(); got != 0 {
				t.Errorf("claude grading calls = %d, want 0 for a check", got)
			}
		})
	}
}

func TestGradeUsesOwnerToken(t *testing.T) {
	const token = "sk-ant-oat01-owner"
	items := []domain.Item{{N: 1, Question: "q1", Back: "k1"}}
	tests := []struct {
		name    string
		token   func() string
		require string
		wantErr bool
	}{
		{name: "token from the owner", token: func() string { return token }, require: token},
		{name: "no token function uses the local login", token: nil, require: ""},
		{name: "token the server does not send", token: func() string { return "sk-ant-oat01-other" }, require: token, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bin, _ := fakeclaude.Use(t, fakeclaude.Good)
			if tt.require != "" {
				fakeclaude.RequireToken(t, tt.require)
			}
			g := &Grader{Bin: bin, Timeout: 10 * time.Second, Dir: t.TempDir(), Token: tt.token}
			_, err := g.Grade(context.Background(), domain.KindKnowledge, items, map[int]string{1: "a"})
			if (err != nil) != tt.wantErr {
				t.Fatalf("Grade() error = %v, want error %v", err, tt.wantErr)
			}
		})
	}
}

func TestCheckMessageIsClaudesOwn(t *testing.T) {
	tests := []struct {
		name    string
		mode    string
		token   string
		require string
		want    string
	}{
		{name: "error result", mode: fakeclaude.Error, token: "sk-ant-oat01-x", want: "usage limit reached"},
		{name: "refused token", mode: fakeclaude.Good, token: "sk-ant-oat01-bad", require: "sk-ant-oat01-good", want: "Invalid API key. Please run /login"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bin, _ := fakeclaude.Use(t, tt.mode)
			if tt.require != "" {
				fakeclaude.RequireToken(t, tt.require)
			}
			g := &Grader{Bin: bin, Timeout: 10 * time.Second, Dir: t.TempDir()}
			if err := g.Check(context.Background(), tt.token); err == nil || err.Error() != tt.want {
				t.Errorf("Check() = %v, want exactly %q", err, tt.want)
			}
		})
	}
}
