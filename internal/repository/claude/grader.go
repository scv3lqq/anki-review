// Package claude grades answers with the Claude Code CLI in print mode.
package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
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
	// a check is one tiny call; it fails in seconds unless the network hangs
	checkTimeout = 90 * time.Second
	checkPrompt  = "Reply with the single word OK"
	oauthVar     = "CLAUDE_CODE_OAUTH_TOKEN"
	apiKeyPrefix = "sk-ant-api" //nolint:gosec // G101: a token prefix, not a credential
)

var errIncomplete = errors.New("incomplete grades from claude")

// Grader runs `claude -p` once per pass.
type Grader struct {
	Bin     string
	Model   string
	Timeout time.Duration
	// Dir is the working directory of claude: an empty data dir, so no project files are around.
	Dir string
	// Token returns the owner's Claude token, read on every call. Nil or "" means claude uses the
	// login of the account the server runs as.
	Token func() string
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
	token := g.token()
	p, err := g.exec(parent, g.Timeout, g.args(), prompt, token)
	if err != nil {
		return nil, err
	}
	var res cliResult
	if err := json.Unmarshal(p.stdout, &res); err != nil {
		if p.exit != nil {
			return nil, fmt.Errorf("claude: %w: %s", p.exit, tail(p.text))
		}
		return nil, fmt.Errorf("claude: decode output: %w", err)
	}
	if res.IsError || len(res.StructuredOutput) == 0 {
		return nil, fmt.Errorf("claude: %s: %s", res.Subtype, tail(redact(res.Result, token)))
	}
	var out gradesOutput
	if err := json.Unmarshal(res.StructuredOutput, &out); err != nil {
		return nil, fmt.Errorf("%w: %w", errIncomplete, err)
	}
	return &out, nil
}

// Check asks claude once with token, as a settings change does before it saves the token. An empty
// token checks the login of the account the server runs as. The error text is shown to the owner
// as it is: claude's own message, without the token.
func (g *Grader) Check(ctx context.Context, token string) error {
	args := []string{"-p", "--safe-mode", "--tools", "", "--no-session-persistence", "--output-format", "json"}
	if g.Model != "" {
		args = append(args, "--model", g.Model)
	}
	p, err := g.exec(ctx, checkTimeout, args, checkPrompt, token)
	if err != nil {
		return err
	}
	var res cliResult
	if err := json.Unmarshal(p.stdout, &res); err != nil {
		if p.exit != nil {
			return fmt.Errorf("%w: %s", p.exit, tail(p.text))
		}
		return errors.New("no readable answer")
	}
	if res.IsError {
		return errors.New(checkMessage(res.Result, token))
	}
	if p.exit != nil {
		return fmt.Errorf("%w: %s", p.exit, tail(p.text))
	}
	if strings.TrimRight(strings.TrimSpace(res.Result), ".!") != "OK" {
		return errors.New("unexpected answer: " + checkMessage(res.Result, token))
	}
	return nil
}

// proc is one finished claude process.
type proc struct {
	stdout []byte
	// text is stderr then stdout, for messages
	text string
	// exit is claude's exit error, nil on exit 0
	exit error
}

// exec runs claude once. The prompt goes through stdin and the token only into the child's env: neither
// is in argv, which any local user can read in `ps`. A timeout or a canceled parent is an error; a
// non-zero exit is reported in proc.exit, because claude may still print an answer before it fails.
func (g *Grader) exec(parent context.Context, timeout time.Duration, args []string, prompt, token string) (proc, error) {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, g.Bin, args...) //nolint:gosec // G204: the binary comes from the owner's config
	cmd.Dir = g.Dir
	cmd.Env = childEnv(os.Environ(), token)
	cmd.Stdin = strings.NewReader(prompt)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = waitDelay

	runErr := cmd.Run()
	switch {
	case parent.Err() != nil:
		return proc{}, fmt.Errorf("claude: %w", parent.Err())
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return proc{}, fmt.Errorf("claude: no answer in %s", timeout)
	}
	return proc{stdout: stdout.Bytes(), text: redact(stderr.String()+stdout.String(), token), exit: runErr}, nil
}

func (g *Grader) token() string {
	if g.Token == nil {
		return ""
	}
	return g.Token()
}

// childEnv is the server's environment without the subscription token variable, plus the owner's
// token under the variable claude reads it from: an API key or a subscription (OAuth) token.
func childEnv(environ []string, token string) []string {
	env := make([]string, 0, len(environ)+1)
	for _, kv := range environ {
		if !strings.HasPrefix(kv, oauthVar+"=") {
			env = append(env, kv)
		}
	}
	switch {
	case token == "":
	case strings.HasPrefix(token, apiKeyPrefix):
		env = append(env, "ANTHROPIC_API_KEY="+token)
	default:
		env = append(env, oauthVar+"="+token)
	}
	return env
}

// checkMessage is claude's own text for a failed check, without the token; the subtype stays out of
// it because the page shows the message as it is.
func checkMessage(result, token string) string {
	msg := tail(redact(result, token))
	if msg == "" {
		return "no message from claude"
	}
	return msg
}

// redact keeps the token out of messages, which reach the page and the log.
func redact(s, token string) string {
	if token == "" {
		return s
	}
	return strings.ReplaceAll(s, token, "[token]")
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
