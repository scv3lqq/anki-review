package owner

import (
	"context"
	"testing"

	"github.com/scv3lqq/anki-review/internal/domain"
)

func TestStrictSetupRequiresSelectedClaudeCredential(t *testing.T) {
	h := newHarness(t)
	h.u.opts.RequireCredentials = true
	_, err := h.u.Setup(context.Background(), testClient, SetupInput{User: testUser, Password: testPass})
	if fieldOf(err) != "claude_token" || h.vault.exists || len(h.checker.claude) != 0 {
		t.Fatalf("strict empty setup = %v, seals=%d, grader calls=%d", err, h.vault.seals, len(h.checker.claude))
	}
	token := h.setup()
	if !h.u.Status(token).GraderConnected {
		t.Fatal("explicit Claude token not connected")
	}
}

func TestStrictUpdateRejectsEmptySelectedClaudeCredential(t *testing.T) {
	h := newHarness(t)
	token := h.setup()
	h.u.opts.RequireCredentials = true
	if err := h.u.Update(context.Background(), token, UpdateInput{Password: testPass, ClaudeToken: strPtr("")}); fieldOf(err) != "claude_token" {
		t.Fatalf("strict clear token = %v", err)
	}
	if h.u.Secrets().ClaudeToken != testClaude || h.vault.seals != 1 {
		t.Fatal("failed credential update changed saved secrets")
	}
}

func TestStrictLegacyUnlockDisconnectedUntilCredentialSaved(t *testing.T) {
	h := newHarness(t)
	h.vault.exists, h.vault.user, h.vault.password = true, testUser, testPass
	h.vault.secrets = domain.Secrets{AnkiKey: testKey}
	h.restart()
	h.u.opts.RequireCredentials = true
	token, err := h.login(testUser, testPass)
	if err != nil {
		t.Fatal(err)
	}
	if h.u.Status(token).GraderConnected {
		t.Fatal("legacy host login reported connected in strict mode")
	}
	if err := h.u.Update(context.Background(), token, UpdateInput{Password: testPass, AnkiKey: strPtr("new-key")}); fieldOf(err) != "claude_token" {
		t.Fatalf("strict update retained missing selected credential: %v", err)
	}
	if err := h.u.Update(context.Background(), token, UpdateInput{Password: testPass, ClaudeToken: strPtr(testClaude)}); err != nil {
		t.Fatal(err)
	}
	if !h.u.Status(token).GraderConnected {
		t.Fatal("saved explicit token not connected")
	}
}

func TestStrictCodexSelectionDoesNotRequireInactiveClaude(t *testing.T) {
	h := newHarness(t)
	h.u.opts.RequireCredentials = true
	token, err := h.u.Setup(context.Background(), testClient, SetupInput{User: testUser, Password: testPass, GraderProvider: "codex", CodexAuth: "dedicated snapshot"})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.u.Update(context.Background(), token, UpdateInput{Password: testPass, GraderProvider: strPtr("claude")}); fieldOf(err) != "claude_token" {
		t.Fatalf("switch to credentialless Claude = %v", err)
	}
	if h.u.Secrets().GraderProvider != "codex" {
		t.Fatal("failed switch changed provider")
	}
}
