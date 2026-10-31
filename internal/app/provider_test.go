package app

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/scv3lqq/anki-review/internal/domain"
	"github.com/scv3lqq/anki-review/internal/repository/codex"
	"github.com/scv3lqq/anki-review/internal/repository/vault"
	"github.com/scv3lqq/anki-review/internal/testutil/fakeanki"
	"github.com/scv3lqq/anki-review/internal/testutil/fakeclaude"
	"github.com/scv3lqq/anki-review/internal/testutil/fakecodex"
	"github.com/scv3lqq/anki-review/internal/usecase/owner"
)

const testCodexAuth = `{"tokens":{"access_token":"access-private","refresh_token":"refresh-private","id_token":"id-private","account_id":"account-private"}}`

func TestCodexRoutingAndConfirmation(t *testing.T) {
	anki := fakeanki.New(t, fakeanki.Card{ID: 1, Deck: "Tech", Question: "Question?", Answer: "Answer", Reps: 1}, fakeanki.Card{ID: 2, Deck: "Tech", Question: "Second?", Answer: "Second answer", Reps: 1})
	claudeBin, claudeCalls := fakeclaude.Use(t, fakeclaude.Good)
	fakeclaude.RequireToken(t, testToken)
	codexBin, codexCalls := fakecodex.Use(t, fakecodex.RefreshGood)
	dir := filepath.Join(t.TempDir(), "data")
	cfg := testConfig(anki, claudeBin, dir)
	cfg.CodexBin = codexBin
	cfg.GraderProvider = "codex"
	c, stop := serve(t, cfg)
	var session struct {
		State           string `json:"state"`
		GraderProvider  string `json:"grader_provider"`
		GraderConnected bool   `json:"grader_connected"`
		Unlocked        bool   `json:"unlocked"`
	}
	c.get("/api/session", &session)
	if session.GraderProvider != "codex" {
		t.Fatalf("initial provider = %+v", session)
	}
	c.post("/api/setup", map[string]any{"user": testUser, "password": testPassword, "grader_provider": "codex", "codex_auth": testCodexAuth, "claude_token": testToken})
	c.get("/api/session", &session)
	if session.GraderProvider != "codex" || !session.GraderConnected {
		t.Fatalf("saved provider = %+v", session)
	}
	c.post("/api/start", map[string]any{"deck": "Tech", "count": 1})
	var cards struct {
		Session string `json:"session"`
	}
	c.get("/api/cards", &cards)
	c.post("/api/answers", map[string]any{"session": cards.Session, "answers": []map[string]any{{"n": 1, "answer": "answer"}}})
	var results struct {
		Items []struct {
			Ref string `json:"ref"`
		} `json:"items"`
	}
	c.poll("/api/results", &results)
	if codexCalls() != 2 || claudeCalls() != 0 || len(results.Items) != 1 || results.Items[0].Ref != "reference" {
		t.Fatalf("wrong grading provider: codex %d Claude %d results %+v", codexCalls(), claudeCalls(), results)
	}
	if len(anki.Answered()) != 0 {
		t.Fatal("grading wrote to Anki before confirmation")
	}
	c.post("/api/confirm", map[string]any{"session": cards.Session})
	var status struct {
		Written bool `json:"written"`
	}
	c.poll("/api/status", &status)
	if !status.Written || len(anki.Answered()) != 1 {
		t.Fatal("confirmed grades not written")
	}
	c.post("/api/settings", map[string]any{"password": testPassword, "grader_provider": "claude"})
	c.get("/api/session", &session)
	if session.GraderProvider != "claude" || claudeCalls() != 0 {
		t.Fatal("settings did not switch provider")
	}
	c.post("/api/reset", map[string]any{"session": cards.Session})
	c.post("/api/start", map[string]any{"deck": "Tech", "count": 1})
	c.get("/api/cards", &cards)
	c.post("/api/answers", map[string]any{"session": cards.Session, "answers": []map[string]any{{"n": 1, "answer": "second"}}})
	c.poll("/api/results", &results)
	if claudeCalls() != 1 || codexCalls() != 2 || len(results.Items) != 1 || results.Items[0].Ref != "ref" {
		t.Fatal("settings did not change the actual grader")
	}
	if len(anki.Answered()) != 1 {
		t.Fatal("provider switch bypassed explicit confirmation")
	}
	stop()
	v, err := vault.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	secrets, err := v.Open(testUser, testPassword)
	if err != nil {
		t.Fatal(err)
	}
	v.Close()
	if secrets.ClaudeToken != testToken || !strings.Contains(secrets.CodexAuth, "access-rotated") {
		t.Fatal("vault did not preserve both credentials and refresh")
	}
	cfg.GraderProvider = "codex"
	c, _ = serve(t, cfg)
	c.get("/api/session", &session)
	if session.State != "login" || session.Unlocked {
		t.Fatal("restart did not lock")
	}
	c.post("/api/login", map[string]any{"user": testUser, "password": testPassword})
	c.get("/api/session", &session)
	if session.GraderProvider != "claude" {
		t.Fatal("environment overrode saved provider")
	}
}

func TestCheckerRefreshSurvivesPasswordChangeAndExecutionFailure(t *testing.T) {
	for _, mode := range []string{fakecodex.RefreshGood, fakecodex.RefreshFailure} {
		t.Run(mode, func(t *testing.T) {
			v, err := vault.New(t.TempDir(), vault.WithCost(1, 8, 1))
			if err != nil {
				t.Fatal(err)
			}
			if err := v.Seal(testUser, testPassword, domain.Secrets{CodexAuth: testCodexAuth, GraderProvider: "codex", ClaudeToken: testToken}); err != nil {
				t.Fatal(err)
			}
			bin, _ := fakecodex.Use(t, mode)
			var own *owner.UseCase
			checked := make(chan struct{})
			resume := make(chan struct{})
			g := &codex.Grader{Bin: bin, Auth: func() string { return own.Secrets().CodexAuth }, SaveAuth: func(previous, next string) error { close(checked); <-resume; return own.SaveCodexAuth(previous, next) }}
			c := checker{codex: g}
			own, err = owner.New(v, c, owner.Options{Log: zerolog.Nop()})
			if err != nil {
				t.Fatal(err)
			}
			token, err := own.Login(context.Background(), "test", testUser, testPassword)
			if err != nil {
				t.Fatal(err)
			}
			provider := "codex"
			result := make(chan error, 1)
			go func() {
				result <- own.Update(context.Background(), token, owner.UpdateInput{Password: testPassword, GraderProvider: &provider})
			}()
			select {
			case <-checked:
			case err := <-result:
				t.Fatalf("check ended before refresh: %v", err)
			case <-time.After(10 * time.Second):
				t.Fatal("refresh did not reach persistence")
			}
			password := "replacement password"
			if err := own.Update(context.Background(), token, owner.UpdateInput{Password: testPassword, NewPassword: &password}); err != nil {
				t.Fatal(err)
			}
			close(resume)
			err = <-result
			if mode == fakecodex.RefreshGood && !errors.Is(err, owner.ErrChanged) {
				t.Fatalf("stale settings = %v", err)
			}
			if mode == fakecodex.RefreshFailure && err == nil {
				t.Fatal("failed check accepted")
			}
			own.Close()
			secrets, err := v.Open(testUser, password)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(secrets.CodexAuth, "access-rotated") || secrets.ClaudeToken != testToken {
				t.Fatal("refresh lost on password race")
			}
		})
	}
}

func TestCodexInvalidImportSafeFieldError(t *testing.T) {
	anki := fakeanki.New(t)
	c, _ := serve(t, testConfig(anki, "unused", t.TempDir()))
	status, body := c.call(http.MethodPost, "/api/setup", map[string]any{"user": testUser, "password": testPassword, "grader_provider": "codex", "codex_auth": `{"OPENAI_API_KEY":"private-invalid"}`})
	if status != http.StatusBadRequest || !strings.Contains(string(body), `"field":"codex_auth"`) || strings.Contains(string(body), "private-invalid") {
		t.Fatalf("invalid auth response: %d %s", status, body)
	}
}

func TestInactiveCodexImportIsValidated(t *testing.T) {
	anki := fakeanki.New(t)
	bin, _ := fakeclaude.Use(t, fakeclaude.Good)
	c, _ := serve(t, testConfig(anki, bin, t.TempDir()))
	setUp(t, c)
	status, body := c.call(http.MethodPost, "/api/settings", map[string]any{"password": testPassword, "codex_auth": "private-invalid"})
	if status != http.StatusBadRequest || !strings.Contains(string(body), `"field":"codex_auth"`) || strings.Contains(string(body), "private-invalid") {
		t.Fatalf("invalid inactive import response: %d %s", status, body)
	}
}
