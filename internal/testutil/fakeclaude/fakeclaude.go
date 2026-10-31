// Package fakeclaude lets a test binary stand in for the claude CLI: call Main first in TestMain, then
// Use in a test and pass the returned path as the claude binary.
package fakeclaude

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

// Modes of the fake.
const (
	Good           = "good"            // every card graded Good
	Incomplete     = "incomplete"      // the last card is left out, every time
	IncompleteOnce = "incomplete-once" // the last card is left out on the first call only
	Error          = "error"           // an error result, like a usage limit
	Crash          = "crash"           // exit 1 with a message on stderr
	Slow           = "slow"            // sleep longer than any test timeout
)

const (
	envMode     = "FAKE_CLAUDE"
	envCalls    = "FAKE_CLAUDE_CALLS"
	envRequired = "FAKE_CLAUDE_REQUIRE_TOKEN"
	// the prompt of a settings check, see claude.Grader.Check
	checkPrompt = "Reply with the single word OK"
)

var reCardHeader = regexp.MustCompile(`(?m)^### (\d+)`)

// Main turns the process into the fake when a test started it as claude.
func Main() {
	if mode := os.Getenv(envMode); mode != "" {
		os.Exit(run(mode))
	}
}

// Use makes claude calls from t run the fake in mode. It returns the binary to call and a counter
// of the calls made so far.
func Use(t *testing.T, mode string) (bin string, calls func() int) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "calls")
	t.Setenv(envMode, mode)
	t.Setenv(envCalls, path)
	// under -race every child would otherwise sleep a second before exiting
	t.Setenv("GORACE", "atexit_sleep_ms=0")
	return os.Args[0], func() int {
		raw, _ := os.ReadFile(path)
		return strings.Count(string(raw), "call\n")
	}
}

// RequireToken makes calls from t fail like claude with a wrong token, unless the child's env carries
// token: the way a test proves the owner's token reaches claude.
func RequireToken(t *testing.T, token string) {
	t.Helper()
	t.Setenv(envRequired, token)
}

func run(mode string) int {
	args := os.Args[1:]
	tools := slices.Index(args, "--tools")
	if !slices.Contains(args, "-p") || !slices.Contains(args, "--safe-mode") || tools < 0 || tools+1 >= len(args) || args[tools+1] != "" {
		fmt.Fprintln(os.Stderr, "fake claude: unsafe args", args)
		return 3
	}
	prompt, _ := io.ReadAll(os.Stdin)
	if want := os.Getenv(envRequired); want != "" && childToken() != want {
		return emit(map[string]any{"type": "result", "is_error": true, "subtype": "error_during_execution", "result": "Invalid API key. Please run /login"})
	}
	// a check is not a grading call: the counter only tracks grades
	if strings.Contains(string(prompt), checkPrompt) {
		if code, failed := failFast(mode); failed {
			return code
		}
		return emit(map[string]any{"type": "result", "is_error": false, "subtype": "success", "result": "OK"})
	}
	calls := countCall()
	if code, failed := failFast(mode); failed {
		return code
	}

	var results []map[string]any
	for _, m := range reCardHeader.FindAllStringSubmatch(string(prompt), -1) {
		var n int
		_, _ = fmt.Sscan(m[1], &n)
		results = append(results, map[string]any{"n": n, "grade": "Good", "mistake": "", "note": "note", "topic": "topic"})
	}

	switch mode {
	case Incomplete:
		results = results[:len(results)-1]
	case IncompleteOnce:
		if calls == 1 {
			results = results[:len(results)-1]
		}
	}
	return emit(map[string]any{"type": "result", "is_error": false, "result": "", "structured_output": map[string]any{"results": results}})
}

// failFast is the failure a mode gives every call, grading or check; false means the call goes on.
func failFast(mode string) (int, bool) {
	switch mode {
	case Crash:
		fmt.Fprintln(os.Stderr, "Invalid API key")
		return 1, true
	case Slow:
		time.Sleep(time.Minute)
	case Error:
		return emit(map[string]any{"type": "result", "is_error": true, "subtype": "error_during_execution", "result": "usage limit reached"}), true
	}
	return 0, false
}

// childToken is the token claude would authenticate with: the subscription token first, then the API key.
func childToken() string {
	if v := os.Getenv("CLAUDE_CODE_OAUTH_TOKEN"); v != "" {
		return v
	}
	return os.Getenv("ANTHROPIC_API_KEY")
}

// countCall records this call and returns how many there were.
func countCall() int {
	path := os.Getenv(envCalls)
	if path == "" {
		return 0
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return 0
	}
	_, _ = f.WriteString("call\n")
	_ = f.Close()
	raw, _ := os.ReadFile(path)
	return strings.Count(string(raw), "call\n")
}

func emit(v any) int {
	if err := json.NewEncoder(os.Stdout).Encode(v); err != nil {
		return 2
	}
	return 0
}
