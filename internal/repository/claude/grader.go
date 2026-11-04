// Package claude grades answers with the Claude Code CLI in print mode.
package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/scv3lqq/anki-review/internal/domain"
	"github.com/scv3lqq/anki-review/internal/repository/grading"
)

const (
	tailBytes             = 300
	maxOutputBytes        = 2 * 1024 * 1024
	waitDelay             = 10 * time.Second
	privateCleanupTimeout = 5 * time.Second
	// a check is one tiny call; it fails in seconds unless the network hangs
	checkTimeout = 90 * time.Second
	checkPrompt  = "Reply with the single word OK"
	oauthVar     = "CLAUDE_CODE_OAUTH_TOKEN"
	apiKeyPrefix = "sk-ant-api" //nolint:gosec // G101: a token prefix, not a credential
)

var errIncomplete = grading.ErrIncomplete

// Grader runs `claude -p` once per pass.
type Grader struct {
	// Isolated requires an explicit credential and gives every CLI call private directories and environment.
	Isolated    bool
	processOnce sync.Once
	processGate chan struct{}
	Bin         string
	Model       string
	Timeout     time.Duration
	// Dir is the working directory of claude: an empty data dir, so no project files are around.
	Dir string
	// Token returns the owner's Claude token, read on every call. Nil or "" means claude uses the
	// login of the account the server runs as, unless Isolated requires an explicit token.
	Token func() string
}

type cliResult struct {
	IsError          bool            `json:"is_error"`
	Subtype          string          `json:"subtype"`
	Result           string          `json:"result"`
	StructuredOutput json.RawMessage `json:"structured_output"`
}

type gradesOutput = grading.Output

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
		if err == nil || !errors.Is(err, errIncomplete) || attempt == grading.Attempts {
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
		if g.Isolated {
			return nil, errors.New("claude: no readable answer")
		}
		if p.exit != nil {
			return nil, fmt.Errorf("claude: %w: %s", p.exit, tail(p.text))
		}
		return nil, fmt.Errorf("claude: decode output: %w", err)
	}
	if res.IsError || len(res.StructuredOutput) == 0 {
		if g.Isolated {
			return nil, isolatedFailure(res.Result)
		}
		return nil, fmt.Errorf("claude: %s: %s", res.Subtype, tail(redact(res.Result, token)))
	}
	var out gradesOutput
	if err := json.Unmarshal(res.StructuredOutput, &out); err != nil {
		if g.Isolated {
			return nil, fmt.Errorf("claude: invalid grading answer: %w", errIncomplete)
		}
		return nil, fmt.Errorf("%w: %w", errIncomplete, err)
	}
	return &out, nil
}

// Check asks claude once with token, as a settings change does before it saves the token. An empty
// token checks the host login only in legacy mode. Isolated mode requires a token and exposes
// generic failure categories, because CLI diagnostics may contain private account information.
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
		if g.Isolated {
			return errors.New("claude: no readable answer")
		}
		if p.exit != nil {
			return fmt.Errorf("%w: %s", p.exit, tail(p.text))
		}
		return errors.New("no readable answer")
	}
	if res.IsError {
		if g.Isolated {
			return isolatedFailure(res.Result)
		}
		return errors.New(checkMessage(res.Result, token))
	}
	if p.exit != nil {
		return fmt.Errorf("%w: %s", p.exit, tail(p.text))
	}
	if strings.TrimRight(strings.TrimSpace(res.Result), ".!") != "OK" {
		if g.Isolated {
			return errors.New("claude: unexpected answer")
		}
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
func (g *Grader) exec(parent context.Context, timeout time.Duration, args []string, prompt, token string) (result proc, runError error) {
	if g.Isolated && strings.TrimSpace(token) == "" {
		return proc{}, errors.New("claude: explicit credential required")
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	if g.Isolated {
		g.processOnce.Do(func() { g.processGate = make(chan struct{}, 1) })
		select {
		case g.processGate <- struct{}{}:
			defer func() { <-g.processGate }()
		case <-ctx.Done():
			return proc{}, fmt.Errorf("claude: waiting for isolated CLI: %w", ctx.Err())
		}
	}

	cmd := exec.CommandContext(ctx, g.Bin, args...) //nolint:gosec // G204: the binary comes from the owner's config
	cmd.Dir = g.Dir
	cmd.Env = childEnv(os.Environ(), token)
	if g.Isolated {
		root, err := os.MkdirTemp("", "anki-review-claude-*")
		if err != nil {
			return proc{}, errors.New("claude: cannot create private environment")
		}
		defer func(root string) {
			if cleanupErr := removePrivateEnvironment(root); cleanupErr != nil {
				result = proc{}
				runError = errors.New("claude: could not remove private environment")
			}
		}(root)
		root, err = filepath.EvalSymlinks(root)
		if err != nil {
			return proc{}, errors.New("claude: cannot resolve private environment")
		}
		for _, name := range []string{"home", "work", "config", "tmp", "cache"} {
			if err := os.Mkdir(filepath.Join(root, name), 0o700); err != nil {
				return proc{}, errors.New("claude: cannot create private environment")
			}
		}
		cmd.Dir = filepath.Join(root, "work")
		cmd.Env = isolatedEnv(os.Environ(), root, token)
	}
	cmd.Stdin = strings.NewReader(prompt)
	var stdout, stderr boundedBuffer
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
	if stdout.overflow || stderr.overflow {
		return proc{}, errors.New("claude: output exceeds safe size limit")
	}
	if g.Isolated && runErr != nil {
		return proc{}, isolatedFailure(stderr.buffer.String() + stdout.buffer.String())
	}
	return proc{stdout: stdout.buffer.Bytes(), text: redact(stderr.buffer.String()+stdout.buffer.String(), token), exit: runErr}, nil
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

func checkResults(items []domain.Item, out *gradesOutput) ([]domain.Result, error) {
	return grading.Validate(items, out)
}

// boundedBuffer drains child output without letting a faulty CLI exhaust server memory.
type boundedBuffer struct {
	buffer   bytes.Buffer
	overflow bool
}

func (b *boundedBuffer) Write(raw []byte) (int, error) {
	size := len(raw)
	remaining := maxOutputBytes - b.buffer.Len()
	if len(raw) > remaining {
		b.overflow = true
		raw = raw[:remaining]
	}
	_, _ = b.buffer.Write(raw)
	return size, nil
}

func isolatedEnv(environ []string, root, token string) []string {
	env := []string{
		"HOME=" + filepath.Join(root, "home"),
		"CLAUDE_CONFIG_DIR=" + filepath.Join(root, "config"),
		"XDG_CONFIG_HOME=" + filepath.Join(root, "config"),
		"XDG_CACHE_HOME=" + filepath.Join(root, "cache"),
		"TMPDIR=" + filepath.Join(root, "tmp"),
	}
	for _, entry := range environ {
		key, _, _ := strings.Cut(entry, "=")
		switch key {
		case "PATH", "LANG", "LC_ALL", "LC_CTYPE", "SSL_CERT_FILE", "SSL_CERT_DIR", "SYSTEMROOT":
			env = append(env, entry)
		}
	}
	return childEnv(env, token)
}

// Raw CLI diagnostics can include account data and unrelated secrets, so isolated mode exposes only categories.
func isolatedFailure(diagnostics string) error {
	text := strings.ToLower(diagnostics)
	switch {
	case strings.Contains(text, "401") || strings.Contains(text, "unauthorized") || strings.Contains(text, "authentication") || strings.Contains(text, "invalid api key") || strings.Contains(text, "login"):
		return errors.New("claude: authentication failed; save an explicit Claude credential")
	case strings.Contains(text, "usage limit") || strings.Contains(text, "rate limit") || strings.Contains(text, "quota") || strings.Contains(text, "429"):
		return errors.New("claude: usage limit reached; try again later")
	default:
		return errors.New("claude: execution failed; check the installed CLI and connection")
	}
}

// Remove only this invocation's tree. WalkDir never descends through symlinks; restoring each
// directory before descent also handles CLI-created read-only configuration directories.
func removePrivateEnvironment(root string) error {
	ctx, cancel := context.WithTimeout(context.Background(), privateCleanupTimeout)
	defer cancel()
	private, err := os.OpenRoot(root)
	if err != nil {
		return fmt.Errorf("open private environment: %w", err)
	}
	defer private.Close()
	var dirs []string
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("walk private environment: %w", walkErr)
		}
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("clean private environment: %w", err)
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return fmt.Errorf("locate private entry: %w", err)
		}
		if entry.IsDir() {
			// os.Root also confines a directory replaced with a symlink during cleanup.
			if err := private.Chmod(relative, 0o700); err != nil {
				return fmt.Errorf("restore private directory: %w", err)
			}
			if relative != "." {
				dirs = append(dirs, relative)
			}
			return nil
		}
		if err := private.Remove(relative); err != nil {
			return fmt.Errorf("remove private file: %w", err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("clean private environment: %w", err)
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("clean private environment: %w", err)
		}
		if err := private.Remove(dirs[i]); err != nil {
			return fmt.Errorf("remove private directory: %w", err)
		}
	}
	if err := os.Remove(root); err != nil {
		return fmt.Errorf("remove private root: %w", err)
	}
	return nil
}
