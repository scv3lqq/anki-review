package claude

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/scv3lqq/anki-review/internal/domain"
)

func isolatedScript(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestIsolatedRequiresExplicitTokenBeforeExec(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "executed")
	g := &Grader{Isolated: true, Timeout: time.Second, Bin: isolatedScript(t, "touch '"+marker+"'")}
	if err := g.Check(context.Background(), ""); err == nil || !strings.Contains(err.Error(), "credential") {
		t.Fatalf("missing explicit credential check = %v", err)
	}
	if _, err := g.Grade(context.Background(), domain.KindKnowledge, []domain.Item{{N: 1}}, map[int]string{1: "answer"}); err == nil || !strings.Contains(err.Error(), "credential") {
		t.Fatalf("missing explicit grading credential = %v", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("CLI executed without explicit credentials: %v", err)
	}
}

func TestIsolatedProcessDirectoriesEnvironmentAndCleanup(t *testing.T) {
	for _, key := range []string{"ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL", "NODE_OPTIONS", "CLAUDE_CONFIG_DIR", "XDG_CONFIG_HOME", "AWS_SECRET_ACCESS_KEY"} {
		t.Setenv(key, "host-secret")
	}
	bin := isolatedScript(t, `printf '%s\n' "$HOME" "$PWD" "$CLAUDE_CONFIG_DIR" "$TMPDIR" "$XDG_CONFIG_HOME" "$XDG_CACHE_HOME" "$CLAUDE_CODE_OAUTH_TOKEN" "$ANTHROPIC_API_KEY" "$ANTHROPIC_AUTH_TOKEN" "$ANTHROPIC_BASE_URL" "$NODE_OPTIONS" "$AWS_SECRET_ACCESS_KEY"`)
	g := &Grader{Isolated: true, Bin: bin, Dir: t.TempDir()}
	var previous string
	for range 2 {
		p, err := g.exec(context.Background(), 10*time.Second, nil, "prompt", "sk-ant-oat01-explicit")
		if err != nil || p.exit != nil {
			t.Fatalf("isolated exec = %v, %v", err, p.exit)
		}
		lines := strings.Split(strings.TrimSuffix(string(p.stdout), "\n"), "\n")
		if len(lines) != 12 {
			t.Fatalf("environment report has %d lines", len(lines))
		}
		if lines[0] == previous || lines[0] == os.Getenv("HOME") || lines[1] == g.Dir {
			t.Fatal("invocation reused host or previous directories")
		}
		previous = lines[0]
		for _, path := range lines[:6] {
			if !filepath.IsAbs(path) || !strings.HasPrefix(path, filepath.Dir(lines[0])+string(os.PathSeparator)) {
				t.Errorf("directory outside private root: %q", path)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Errorf("directory retained after invocation: %v", err)
			}
		}
		if lines[6] != "sk-ant-oat01-explicit" || strings.Join(lines[7:], "") != "" {
			t.Fatal("isolated process inherited credentials or execution configuration")
		}
	}
	g.Bin = isolatedScript(t, `printf '%s\n' "$ANTHROPIC_API_KEY" "$CLAUDE_CODE_OAUTH_TOKEN"`)
	p, err := g.exec(context.Background(), 10*time.Second, nil, "", "sk-ant-api03-explicit")
	if err != nil || string(p.stdout) != "sk-ant-api03-explicit\n\n" {
		t.Fatalf("explicit API credential selection failed: %v", err)
	}
}

func TestIsolatedFailuresAreBoundedAndSanitized(t *testing.T) {
	const private = "PRIVATE_HOST_DIAGNOSTICS"
	for _, body := range []string{
		"printf '" + private + "' >&2; exit 1",
		`printf '{"is_error":true,"subtype":"` + private + `","result":"` + private + `"}'`,
		`printf '{"result":"` + private + `"}'`,
		`printf '` + private + `'`,
		"head -c 2200000 /dev/zero",
		"head -c 2200000 /dev/zero >&2; printf '{\"result\":\"OK\"}'",
	} {
		g := &Grader{Isolated: true, Bin: isolatedScript(t, body), Timeout: 10 * time.Second, Token: func() string { return "sk-ant-oat01-explicit" }}
		if err := g.Check(context.Background(), g.token()); err == nil || strings.Contains(err.Error(), private) || len(err.Error()) > 300 {
			t.Fatalf("unsafe check diagnostic: %v", err)
		}
		if _, err := g.run(context.Background(), "prompt"); err == nil || strings.Contains(err.Error(), private) || len(err.Error()) > 300 {
			t.Fatalf("unsafe grade diagnostic: %v", err)
		}
	}
}

func TestIsolatedCleanupOnProcessFailure(t *testing.T) {
	capture := filepath.Join(t.TempDir(), "home")
	g := &Grader{Isolated: true, Bin: isolatedScript(t, "printf '%s' \"$HOME\" > '"+capture+"'; exit 1")}
	_, _ = g.exec(context.Background(), 10*time.Second, nil, "", "sk-ant-oat01-explicit")
	raw, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(string(raw)); !os.IsNotExist(err) {
		t.Fatalf("private HOME retained after failure: %v", err)
	}
}

func TestIsolatedSerializesInvocationsWithCancelableWait(t *testing.T) {
	root := t.TempDir()
	started, release := filepath.Join(root, "started"), filepath.Join(root, "release")
	bin := isolatedScript(t, "printf x >> '"+started+"'\nwhile [ ! -f '"+release+"' ]; do sleep 0.01; done\nprintf '{\"result\":\"OK\"}'")
	g := &Grader{Isolated: true, Bin: bin}
	first := make(chan error, 1)
	go func() { first <- g.Check(context.Background(), "sk-ant-oat01-explicit") }()
	defer func() {
		if err := os.WriteFile(release, nil, 0o600); err != nil {
			t.Error(err)
		}
		if err := <-first; err != nil {
			t.Error(err)
		}
	}()
	deadline := time.After(3 * time.Second)
	for {
		if _, err := os.Stat(started); err == nil {
			break
		}
		select {
		case <-deadline:
			t.Fatal("first isolated CLI did not start")
		case <-time.After(10 * time.Millisecond):
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := g.Check(ctx, "sk-ant-oat01-explicit"); err == nil {
		t.Fatal("canceled waiter executed CLI")
	}
	raw, err := os.ReadFile(started)
	if err != nil || string(raw) != "x" {
		t.Fatalf("concurrent CLI launched while prior call active: %q, %v", raw, err)
	}
}

func TestIsolatedCleanupRestoresTaskDirectoryPermissions(t *testing.T) {
	capture := filepath.Join(t.TempDir(), "config")
	g := &Grader{Isolated: true, Timeout: 10 * time.Second, Bin: isolatedScript(t, "printf '%s' \"$CLAUDE_CONFIG_DIR\" > '"+capture+"'\nprintf fixture > \"$CLAUDE_CONFIG_DIR/auth.json\"\nchmod 000 \"$CLAUDE_CONFIG_DIR\"\nprintf '{\"result\":\"OK\"}'")}
	if err := g.Check(context.Background(), "sk-ant-oat01-explicit"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	config := string(raw)
	t.Cleanup(func() { _ = os.Chmod(config, 0o700); _ = os.RemoveAll(filepath.Dir(config)) })
	if _, err := os.Stat(config); !os.IsNotExist(err) {
		t.Fatalf("private credential directory survived successful Check: %v", err)
	}
}

func TestIsolatedCleanupFailureDiscardsCheckAndGrade(t *testing.T) {
	for _, grade := range []bool{false, true} {
		t.Run(map[bool]string{false: "check", true: "grade"}[grade], func(t *testing.T) {
			outer := t.TempDir()
			t.Setenv("TMPDIR", outer)
			t.Cleanup(func() { _ = os.Chmod(outer, 0o700) })
			g := &Grader{Isolated: true, Timeout: 10 * time.Second, Token: func() string { return "sk-ant-oat01-explicit" }, Bin: isolatedScript(t, "printf fixture > \"$CLAUDE_CONFIG_DIR/auth.json\"\nchmod 000 \"$TMPDIR/../..\"\nprintf '{\"result\":\"OK\",\"structured_output\":{\"results\":[{\"n\":1,\"grade\":\"Good\"}]}}'")}
			var err error
			if grade {
				var results []domain.Result
				results, err = g.Grade(context.Background(), domain.KindKnowledge, []domain.Item{{N: 1}}, map[int]string{1: "answer"})
				if len(results) != 0 {
					t.Error("grading results accepted after failed cleanup")
				}
			} else {
				err = g.Check(context.Background(), g.token())
			}
			if err == nil || !strings.Contains(err.Error(), "private environment") || strings.Contains(err.Error(), outer) {
				t.Fatalf("cleanup failure not safely reported: %v", err)
			}
		})
	}
}

func TestIsolatedCleanupDoesNotFollowSymlinks(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.Mkdir(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(outside, "keep")
	if err := os.WriteFile(file, []byte("untouched"), 0o400); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(outside, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(outside, 0o700) })
	g := &Grader{Isolated: true, Bin: isolatedScript(t, "ln -s '"+outside+"' \"$HOME/external\"\nprintf '{\"result\":\"OK\"}'")}
	if err := g.Check(context.Background(), "sk-ant-oat01-explicit"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(file)
	if err != nil || string(raw) != "untouched" {
		t.Fatalf("cleanup followed symlink: %v", err)
	}
	info, err := os.Stat(outside)
	if err != nil || info.Mode().Perm() != 0o500 {
		t.Fatal("cleanup chmoded external directory")
	}
}
