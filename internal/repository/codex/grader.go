// Package codex grades answers with a pinned Codex CLI and isolated file credentials.
package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/scv3lqq/anki-review/internal/domain"
	"github.com/scv3lqq/anki-review/internal/repository/grading"
)

const (
	// SupportedVersion is the CLI version whose no-tool transport contract is verified.
	SupportedVersion = "0.166.0"
	maxAuthBytes     = 1024 * 1024
	maxOutputBytes   = 2 * 1024 * 1024
	defaultTimeout   = 90 * time.Second
	checkSchema      = `{"type":"object","properties":{"ok":{"type":"boolean"}},"required":["ok"],"additionalProperties":false}`
)

// Grader serializes CLI calls because OAuth refresh rotates the imported credentials.
type Grader struct {
	Bin   string
	Model string
	// Dir is retained for provider configuration compatibility. Every call uses a fresh empty working directory.
	Dir      string
	Timeout  time.Duration
	Auth     func() string
	SaveAuth func(previous, next string) error
	mu       sync.Mutex
}

// ValidateAuth accepts a bounded file-based ChatGPT login snapshot without revealing its contents.
func ValidateAuth(auth string) error {
	if len(auth) == 0 || len(auth) > maxAuthBytes {
		return errors.New("codex: import a dedicated ChatGPT auth.json from codex login --device-auth")
	}
	var snapshot struct {
		Tokens struct {
			Access  string `json:"access_token"`
			Refresh string `json:"refresh_token"`
			ID      string `json:"id_token"`
			Account string `json:"account_id"`
		} `json:"tokens"`
	}
	if json.Unmarshal([]byte(auth), &snapshot) != nil || snapshot.Tokens.Access == "" || snapshot.Tokens.Refresh == "" || snapshot.Tokens.ID == "" || snapshot.Tokens.Account == "" {
		return errors.New("codex: invalid ChatGPT auth.json; run codex login --device-auth again")
	}
	return nil
}

// Grade persists refreshed credentials before returning either results or an invocation failure.
func (g *Grader) Grade(ctx context.Context, kind domain.Kind, items []domain.Item, answers map[int]string) ([]domain.Result, error) {
	if len(items) == 0 {
		return []domain.Result{}, nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	prompt, err := grading.Prompt(kind, items, answers)
	if err != nil {
		return nil, err
	}
	for attempt := 1; attempt <= grading.Attempts; attempt++ {
		auth := ""
		if g.Auth != nil {
			auth = g.Auth()
		}
		message, next, runErr := g.invoke(ctx, auth, prompt, grading.Schema)
		if next != auth {
			if g.SaveAuth == nil || g.SaveAuth(auth, next) != nil {
				return nil, errors.New("codex: cannot persist refreshed credentials; unlock and reauthenticate")
			}
		}
		if runErr != nil && !errors.Is(runErr, grading.ErrIncomplete) {
			return nil, runErr
		}
		var out grading.Output
		var results []domain.Result
		if runErr == nil && json.Unmarshal([]byte(message), &out) == nil {
			results, err = grading.Validate(items, &out)
		} else {
			err = grading.ErrIncomplete
		}
		if err == nil {
			return results, nil
		}
	}
	return nil, errors.New("codex: invalid output after two attempts")
}

// Check persists refreshes of saved credentials before releasing the grading lock.
// Fresh candidates are returned for the caller to seal, including on invocation failure.
func (g *Grader) Check(ctx context.Context, auth string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	saved := g.Auth != nil && g.Auth() == auth
	message, next, err := g.invoke(ctx, auth, "Reply with JSON {\"ok\":true}.", checkSchema)
	if saved && next != auth {
		if g.SaveAuth == nil || g.SaveAuth(auth, next) != nil {
			return next, errors.New("codex: cannot persist refreshed credentials; unlock and reauthenticate")
		}
	}
	if err != nil {
		return next, err
	}
	var out struct {
		OK bool `json:"ok"`
	}
	if json.Unmarshal([]byte(message), &out) != nil || !out.OK {
		return next, errors.New("codex: invalid output from connection check")
	}
	return next, nil
}

func (g *Grader) invoke(ctx context.Context, auth, prompt, schema string) (message, next string, err error) {
	next = auth
	if err = ValidateAuth(auth); err != nil {
		return "", next, err
	}
	root, err := runtimeHome()
	if err != nil {
		return "", next, err
	}
	defer func() {
		if os.RemoveAll(root) != nil {
			message = ""
			err = errors.New("codex: cannot remove temporary credentials")
		}
	}()
	work := filepath.Join(root, "work")
	if os.Mkdir(work, 0o700) != nil {
		return "", next, errors.New("codex: cannot prepare isolated working directory")
	}
	version, err := g.execute(ctx, root, work, []string{"--version"}, "")
	if err != nil {
		return "", next, err
	}
	if strings.TrimSpace(version) != "codex-cli "+SupportedVersion {
		return "", next, fmt.Errorf("codex: unsupported CLI version; install %s", SupportedVersion)
	}
	authPath := filepath.Join(root, "auth.json")
	if os.WriteFile(authPath, []byte(auth), 0o600) != nil {
		return "", next, errors.New("codex: cannot prepare temporary credentials")
	}
	schemaPath := filepath.Join(root, "schema.json")
	if os.WriteFile(schemaPath, []byte(schema), 0o600) != nil {
		return "", next, errors.New("codex: cannot prepare output schema")
	}
	raw, runErr := g.execute(ctx, root, work, g.args(work, schemaPath), prompt)
	refreshed, refreshErr := readAuth(authPath)
	if refreshErr != nil {
		return "", next, refreshErr
	}
	next = refreshed
	if runErr != nil {
		return "", next, runErr
	}
	message, err = finalMessage(raw)
	return message, next, err
}

func readAuth(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxAuthBytes {
		return "", errors.New("codex: cannot read refreshed credentials; reauthenticate")
	}
	raw, err := os.ReadFile(path) //nolint:gosec // G304: path is the fixed auth file within our private runtime home.
	if err != nil {
		return "", errors.New("codex: cannot read refreshed credentials; reauthenticate")
	}
	if err := ValidateAuth(string(raw)); err != nil {
		return "", err
	}
	return string(raw), nil
}

func (g *Grader) args(work, schemaPath string) []string {
	args := []string{"exec", "--strict-config", "--ignore-user-config", "--ignore-rules", "--ephemeral", "--skip-git-repo-check", "--sandbox", "read-only", "-C", work, "--json", "--output-schema", schemaPath}
	if g.Model != "" {
		args = append(args, "-m", g.Model)
	}
	for _, setting := range []string{`cli_auth_credentials_store="file"`, `project_doc_max_bytes=0`, `history.persistence="none"`, `web_search="disabled"`, `analytics.enabled=false`, `skills.include_instructions=false`, `skills.bundled.enabled=false`, `tools.experimental_request_user_input.enabled=false`, `tools.update_plan.enabled=false`} {
		args = append(args, "-c", setting)
	}
	for _, feature := range []string{"goals", "default_mode_request_user_input", "send_message_to_user_async", "shell_tool", "unified_exec", "shell_snapshot", "apps", "plugins", "hooks", "multi_agent", "multi_agent_v2", "code_mode", "code_mode_host", "image_generation", "computer_use", "browser_use", "in_app_browser", "view_image", "sleep_tool", "skill_search", "tool_suggest"} {
		args = append(args, "-c", "features."+feature+"=false")
	}
	return append(args, "-")
}
