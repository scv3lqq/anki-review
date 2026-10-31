// Package fakecodex provides an isolated executable fixture for Codex integration tests.
package fakecodex

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

// Supported fake response modes.
const (
	Good            = "good"
	IncompleteOnce  = "incomplete-once"
	Malformed       = "malformed"
	Duplicate       = "duplicate"
	AuthFailure     = "auth-failure"
	Usage           = "usage"
	Oversize        = "oversize"
	OversizeStderr  = "oversize-stderr"
	VersionMismatch = "version-mismatch"
	RefreshFailure  = "refresh-failure"
	RefreshSlow     = "refresh-slow"
	RefreshGood     = "refresh-good"
	GoodSlow        = "good-slow"
)

// Main handles fixture subprocesses before the normal test runner starts.
func Main() {
	if mode := os.Getenv("FAKE_CODEX"); mode != "" {
		os.Exit(run(mode))
	}
}

// Use returns a wrapper that supplies only explicit fixture state after the adapter sanitizes its environment.
func Use(t *testing.T, mode string) (string, func() int) {
	t.Helper()
	dir := t.TempDir()
	calls := filepath.Join(dir, "calls")
	bin := filepath.Join(dir, "codex")
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
	script := "#!/bin/sh\nFAKE_CODEX=" + quote(mode) + " FAKE_CODEX_CALLS=" + quote(calls) + " GORACE=atexit_sleep_ms=0 exec " + quote(os.Args[0]) + " \"$@\"\n"
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return bin, func() int { raw, _ := os.ReadFile(calls); return strings.Count(string(raw), "call\n") }
}

func run(mode string) int {
	if slices.Equal(os.Args[1:], []string{"--version"}) {
		if mode == VersionMismatch {
			fmt.Println("codex-cli 0.1.0")
		} else {
			fmt.Println("codex-cli 0.166.0")
		}
		return 0
	}
	if err := safeInvocation(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 3
	}
	prompt, _ := io.ReadAll(os.Stdin)
	if err := recordCall(); err != nil {
		return 3
	}
	if mode == RefreshFailure || mode == RefreshSlow || mode == RefreshGood {
		path := filepath.Join(os.Getenv("CODEX_HOME"), "auth.json")
		raw, err := os.ReadFile(path)
		if err != nil {
			return 3
		}
		if err := os.WriteFile(path, []byte(strings.ReplaceAll(string(raw), "access-private", "access-rotated")), 0o600); err != nil {
			return 3
		}
	}
	switch mode {
	case AuthFailure, RefreshFailure:
		fmt.Fprintln(os.Stderr, "401 unauthorized access-private private-card-sentinel")
		return 1
	case Usage:
		fmt.Fprintln(os.Stderr, "usage limit exceeded private-card-sentinel")
		return 1
	case RefreshSlow:
		time.Sleep(time.Minute)
	case GoodSlow:
		time.Sleep(300 * time.Millisecond)
	case Oversize:
		fmt.Print(strings.Repeat("x", 3*1024*1024))
		return 0
	case OversizeStderr:
		fmt.Fprint(os.Stderr, strings.Repeat("x", 3*1024*1024))
		return 0
	case Malformed:
		return emit("not json private-card-sentinel")
	}
	if strings.Contains(string(prompt), "Reply with JSON") {
		return emit(`{"ok":true}`)
	}
	var results []map[string]any
	for _, match := range regexp.MustCompile(`(?m)^### (\d+)`).FindAllStringSubmatch(string(prompt), -1) {
		var n int
		_, _ = fmt.Sscan(match[1], &n)
		results = append(results, map[string]any{"n": n, "grade": "Good", "mistake": "", "ref": "reference", "note": "note", "topic": "topic"})
	}
	if mode == IncompleteOnce {
		raw, _ := os.ReadFile(os.Getenv("FAKE_CODEX_CALLS"))
		if strings.Count(string(raw), "call\n") == 1 {
			results = nil
		}
	}
	if mode == Duplicate && len(results) > 0 {
		results = append(results, results[0])
	}
	raw, _ := json.Marshal(map[string]any{"results": results})
	return emit(string(raw))
}

func safeInvocation() error {
	args := os.Args[1:]
	for _, flag := range []string{"exec", "--strict-config", "--ignore-user-config", "--ignore-rules", "--ephemeral", "--skip-git-repo-check", "--json", "read-only"} {
		if !slices.Contains(args, flag) {
			return fmt.Errorf("missing safety flag %s", flag)
		}
	}
	for _, assignment := range []string{`cli_auth_credentials_store="file"`, `project_doc_max_bytes=0`, `history.persistence="none"`, `features.shell_tool=false`, `features.unified_exec=false`, `features.apps=false`, `features.plugins=false`, `features.hooks=false`, `tools.update_plan.enabled=false`} {
		if !slices.Contains(args, assignment) {
			return fmt.Errorf("missing safety override %s", assignment)
		}
	}
	for _, key := range []string{"OPENAI_API_KEY", "OPENAI_BASE_URL", "CODEX_API_KEY", "ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN", "HTTP_PROXY", "HTTPS_PROXY", "OTEL_EXPORTER_OTLP_ENDPOINT"} {
		if os.Getenv(key) != "" {
			return fmt.Errorf("inherited forbidden variable %s", key)
		}
	}
	home := os.Getenv("CODEX_HOME")
	if home == "" || home != os.Getenv("HOME") {
		return fmt.Errorf("unisolated home")
	}
	for _, name := range []string{home, filepath.Join(home, "auth.json")} {
		info, err := os.Stat(name)
		if err != nil {
			return err
		}
		if info.Mode().Perm()&0o077 != 0 {
			return fmt.Errorf("unsafe credential permissions")
		}
	}
	schemaIndex := slices.Index(args, "--output-schema")
	if schemaIndex < 0 || schemaIndex+1 >= len(args) {
		return fmt.Errorf("missing schema")
	}
	schema, err := os.ReadFile(args[schemaIndex+1])
	if err != nil || !json.Valid(schema) {
		return fmt.Errorf("invalid schema")
	}
	return nil
}

func recordCall() error {
	homeFile, err := os.OpenFile(os.Getenv("FAKE_CODEX_CALLS")+".homes", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, err = homeFile.WriteString(os.Getenv("CODEX_HOME") + "\n")
	_ = homeFile.Close()
	if err != nil {
		return err
	}
	file, err := os.OpenFile(os.Getenv("FAKE_CODEX_CALLS"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, err = file.WriteString("call\n")
	closeErr := file.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func emit(message string) int {
	event := map[string]any{"type": "item.completed", "item": map[string]string{"type": "agent_message", "text": message}}
	if err := json.NewEncoder(os.Stdout).Encode(event); err != nil {
		return 3
	}
	return 0
}
