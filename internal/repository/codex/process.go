package codex

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

	"github.com/scv3lqq/anki-review/internal/repository/grading"
)

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

func (g *Grader) execute(parent context.Context, home, work string, args []string, prompt string) (string, error) {
	timeout := g.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	bin := g.Bin
	if bin == "" {
		bin = "codex"
	}
	cmd := exec.CommandContext(ctx, bin, args...) //nolint:gosec // G204: the executable comes from trusted server configuration.
	cmd.Dir = work
	cmd.Env = childEnv(os.Environ(), home)
	cmd.Stdin = strings.NewReader(prompt)
	var stdout, stderr boundedBuffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = time.Second
	err := cmd.Run()
	switch {
	case parent.Err() != nil:
		return "", fmt.Errorf("codex: canceled: %w", parent.Err())
	case ctx.Err() != nil:
		return "", fmt.Errorf("codex: timeout: %w", ctx.Err())
	case stdout.overflow || stderr.overflow:
		return "", errors.New("codex: output exceeds safe size limit")
	case err != nil:
		return "", processError(stderr.buffer.String() + stdout.buffer.String())
	}
	return stdout.buffer.String(), nil
}

func childEnv(environ []string, home string) []string {
	env := []string{"HOME=" + home, "CODEX_HOME=" + home, "TMPDIR=" + home, "RUST_LOG=off"}
	for _, entry := range environ {
		key, _, _ := strings.Cut(entry, "=")
		switch key {
		case "PATH", "LANG", "LC_ALL", "LC_CTYPE", "SSL_CERT_FILE", "SSL_CERT_DIR", "SYSTEMROOT":
			env = append(env, entry)
		}
	}
	return env
}

func processError(diagnostics string) error {
	text := strings.ToLower(diagnostics)
	switch {
	case strings.Contains(text, "401") || strings.Contains(text, "unauthorized") || strings.Contains(text, "authentication") || strings.Contains(text, "refresh_token") || strings.Contains(text, "login"):
		return errors.New("codex: authentication failed; import a new dedicated auth.json")
	case strings.Contains(text, "usage limit") || strings.Contains(text, "rate limit") || strings.Contains(text, "quota") || strings.Contains(text, "429"):
		return errors.New("codex: usage limit reached; try again later")
	default:
		return errors.New("codex: execution failed; check the installed CLI and connection")
	}
}

func finalMessage(raw string) (string, error) {
	var final string
	for line := range strings.SplitSeq(raw, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var event struct {
			Type string `json:"type"`
			Item struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"item"`
		}
		if json.Unmarshal([]byte(line), &event) != nil {
			return "", fmt.Errorf("codex: invalid output: %w", grading.ErrIncomplete)
		}
		if event.Type == "error" || event.Type == "turn.failed" {
			return "", processError(line)
		}
		if event.Type == "item.completed" && event.Item.Type == "agent_message" {
			final = event.Item.Text
		}
	}
	if final == "" {
		return "", fmt.Errorf("codex: invalid output: %w", grading.ErrIncomplete)
	}
	return final, nil
}
