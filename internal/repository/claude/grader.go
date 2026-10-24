// Package claude grades answers with the Claude Code CLI in print mode.
package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/scv3lqq/anki-review/internal/domain"
)

const (
	attempts  = 2
	maxField  = 2000
	tailBytes = 300
	waitDelay = 10 * time.Second
)

var errIncomplete = errors.New("incomplete grades from claude")

// Grader runs `claude -p` once per pass.
type Grader struct {
	Bin     string
	Model   string
	Timeout time.Duration
	// Dir is the working directory of claude: an empty data dir, so no project files are around.
	Dir string
}

type cliResult struct {
	IsError          bool            `json:"is_error"`
	Subtype          string          `json:"subtype"`
	Result           string          `json:"result"`
	StructuredOutput json.RawMessage `json:"structured_output"`
}

type gradesOutput struct {
	Results []struct {
		N       int    `json:"n"`
		Grade   string `json:"grade"`
		Mistake string `json:"mistake"`
		Ref     string `json:"ref"`
		Note    string `json:"note"`
		Topic   string `json:"topic"`
	} `json:"results"`
}

// Grade returns one result per item. Only a malformed answer from Claude is retried: a failed
// process (auth, usage limits, timeout) would fail the same way again.
func (g *Grader) Grade(ctx context.Context, kind domain.Kind, items []domain.Item, answers map[int]string) ([]domain.Result, error) {
	if len(items) == 0 {
		return []domain.Result{}, nil
	}
	prompt, err := Prompt(kind, items, answers)
	if err != nil {
		return nil, err
	}
	for attempt := 1; ; attempt++ {
		out, err := g.run(ctx, prompt)
		if err != nil {
			return nil, err
		}
		results, err := checkResults(items, out)
		if err == nil || !errors.Is(err, errIncomplete) || attempt == attempts {
			return results, err
		}
	}
}

// args keep Claude away from everything but the prompt: no tools, no MCP servers, no CLAUDE.md, no
// hooks (--safe-mode), so nothing written on a card can make it act.
func (g *Grader) args() []string {
	args := []string{"-p", "--safe-mode", "--tools", "", "--no-session-persistence", "--output-format", "json", "--json-schema", schema}
	if g.Model != "" {
		args = append(args, "--model", g.Model)
	}
	return args
}

func (g *Grader) run(parent context.Context, prompt string) (*gradesOutput, error) {
	ctx, cancel := context.WithTimeout(parent, g.Timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, g.Bin, g.args()...) //nolint:gosec // G204: the binary comes from the owner's config
	cmd.Dir = g.Dir
	// the prompt holds the answer keys: stdin, never argv, which any local user can read in `ps`
	cmd.Stdin = strings.NewReader(prompt)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = waitDelay

	runErr := cmd.Run()
	switch {
	case parent.Err() != nil:
		return nil, fmt.Errorf("claude: %w", parent.Err())
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return nil, fmt.Errorf("claude: no answer in %s", g.Timeout)
	}

	var res cliResult
	if err := json.Unmarshal(stdout.Bytes(), &res); err != nil {
		if runErr != nil {
			return nil, fmt.Errorf("claude: %w: %s", runErr, tail(stderr.String()+stdout.String()))
		}
		return nil, fmt.Errorf("claude: decode output: %w", err)
	}
	if res.IsError || len(res.StructuredOutput) == 0 {
		return nil, fmt.Errorf("claude: %s: %s", res.Subtype, tail(res.Result))
	}
	var out gradesOutput
	if err := json.Unmarshal(res.StructuredOutput, &out); err != nil {
		return nil, fmt.Errorf("%w: %w", errIncomplete, err)
	}
	return &out, nil
}

func tail(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > tailBytes {
		s = s[len(s)-tailBytes:]
	}
	return s
}

func clip(s string) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > maxField {
		s = string(r[:maxField])
	}
	return s
}

// checkResults wants a valid grade for every item and ignores numbers it didn't ask about.
func checkResults(items []domain.Item, out *gradesOutput) ([]domain.Result, error) {
	type entry = struct {
		grade                     domain.Grade
		mistake, ref, note, topic string
	}
	byN := make(map[int]entry, len(out.Results))
	for _, r := range out.Results {
		g, ok := domain.ParseGrade(r.Grade)
		if !ok {
			continue
		}
		byN[r.N] = entry{g, r.Mistake, r.Ref, r.Note, r.Topic}
	}
	results := make([]domain.Result, 0, len(items))
	var missing []string
	for _, it := range items {
		e, ok := byN[it.N]
		if !ok {
			missing = append(missing, fmt.Sprint(it.N))
			continue
		}
		results = append(results, domain.Result{N: it.N, Grade: e.grade, Mistake: clip(e.mistake), Ref: clip(e.ref), Note: clip(e.note), Topic: clip(e.topic)})
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("%w: no valid grade for cards %s", errIncomplete, strings.Join(missing, ", "))
	}
	return results, nil
}
