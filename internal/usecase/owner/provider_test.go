package owner

import (
	"context"
	"errors"
	"testing"

	"github.com/scv3lqq/anki-review/internal/domain"
)

func TestCodexSetupAndSwitchPreservesClaude(t *testing.T) {
	h := newHarness(t)
	token := h.setup()
	provider, auth := "codex", "opaque login snapshot"
	if err := h.u.Update(context.Background(), token, UpdateInput{Password: testPass, GraderProvider: &provider, CodexAuth: &auth}); err != nil {
		t.Fatal(err)
	}
	if got := h.u.Secrets(); got.GraderProvider != "codex" || got.CodexAuth != auth || got.ClaudeToken != testClaude {
		t.Fatalf("provider switch lost credentials")
	}
	st := h.u.Status(token)
	if st.GraderProvider != "codex" || !st.GraderConnected {
		t.Fatalf("status = %+v", st)
	}
	provider = "claude"
	if err := h.u.Update(context.Background(), token, UpdateInput{Password: testPass, GraderProvider: &provider}); err != nil {
		t.Fatal(err)
	}
	if got := h.u.Secrets(); got.CodexAuth != auth || got.ClaudeToken != testClaude {
		t.Fatal("switch erased inactive credentials")
	}
}

func TestSaveCodexAuthUsesCurrentPasswordAndRejectsStaleSnapshot(t *testing.T) {
	h := newHarness(t)
	token := h.setup()
	auth, provider := "snapshot A", "codex"
	if err := h.u.Update(context.Background(), token, UpdateInput{Password: testPass, GraderProvider: &provider, CodexAuth: &auth}); err != nil {
		t.Fatal(err)
	}
	nextPassword := "another long password"
	if err := h.u.Update(context.Background(), token, UpdateInput{Password: testPass, NewPassword: &nextPassword}); err != nil {
		t.Fatal(err)
	}
	if err := h.u.SaveCodexAuth(auth, "snapshot B"); err != nil {
		t.Fatal(err)
	}
	if err := h.u.SaveCodexAuth(auth, "stale snapshot"); !errors.Is(err, ErrChanged) {
		t.Fatalf("stale refresh = %v", err)
	}
	h.restart()
	if _, err := h.login(testUser, nextPassword); err != nil {
		t.Fatal(err)
	}
	if got := h.u.Secrets(); got.CodexAuth != "snapshot B" || got.ClaudeToken != testClaude || got.AnkiKey != testKey {
		t.Fatal("refresh lost latest credentials")
	}
}

func TestRefreshInvalidatesPendingSettingsAndFailsClosed(t *testing.T) {
	h := newHarness(t)
	token := h.setup()
	if err := h.u.SaveCodexAuth("", "A"); err != nil {
		t.Fatal(err)
	}
	h.checker.hook = func() {
		if err := h.u.SaveCodexAuth("A", "B"); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.u.Update(context.Background(), token, UpdateInput{Password: testPass, AnkiKey: strPtr("new-key")}); !errors.Is(err, ErrChanged) {
		t.Fatalf("stale settings = %v", err)
	}
	h.vault.resealErr = errors.New("disk unavailable")
	if err := h.u.SaveCodexAuth("B", "C"); err == nil {
		t.Fatal("refresh accepted failed persistence")
	}
	if h.u.Secrets().CodexAuth != "B" {
		t.Fatal("failed write changed live credentials")
	}
	h.u.Close()
	if err := h.u.SaveCodexAuth("B", "C"); !errors.Is(err, domain.ErrCredentials) {
		t.Fatalf("locked refresh = %v", err)
	}
}

func TestProviderValidationAndInitialDefault(t *testing.T) {
	h := newHarness(t)
	h.u.opts.DefaultProvider = "codex"
	if st := h.u.Status(""); st.GraderProvider != "codex" {
		t.Fatalf("initial provider = %q", st.GraderProvider)
	}
	_, err := h.u.Setup(context.Background(), testClient, SetupInput{User: testUser, Password: testPass, GraderProvider: "other"})
	if fieldOf(err) != "grader_provider" {
		t.Fatalf("invalid provider = %v", err)
	}
	_, err = h.u.Setup(context.Background(), testClient, SetupInput{User: testUser, Password: testPass})
	if fieldOf(err) != "codex_auth" {
		t.Fatalf("empty Codex auth = %v", err)
	}
}
