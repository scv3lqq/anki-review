package codex

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/scv3lqq/anki-review/internal/domain"
	"github.com/scv3lqq/anki-review/internal/testutil/fakecodex"
)

const testAuth = `{"auth_mode":"chatgpt","tokens":{"access_token":"access-private","refresh_token":"refresh-private","id_token":"id-private","account_id":"account-private"}}`

func TestMain(m *testing.M) { fakecodex.Main(); os.Exit(m.Run()) }

func testGrader(t *testing.T, mode string) (*Grader, func() int) {
	t.Helper()
	bin, calls := fakecodex.Use(t, mode)
	return &Grader{Bin: bin, Dir: t.TempDir(), Timeout: 15 * time.Second, Auth: func() string { return testAuth }}, calls
}

func TestGradeContract(t *testing.T) {
	tests := []struct {
		mode, want string
		calls      int
	}{
		{fakecodex.Good, "", 1}, {fakecodex.IncompleteOnce, "", 2}, {fakecodex.Malformed, "invalid output", 2},
		{fakecodex.Duplicate, "invalid output", 2}, {fakecodex.AuthFailure, "authentication", 1}, {fakecodex.Usage, "usage limit", 1},
		{fakecodex.Oversize, "output exceeds", 1}, {fakecodex.OversizeStderr, "output exceeds", 1}, {fakecodex.VersionMismatch, "unsupported", 0},
	}
	for _, tt := range tests {
		t.Run(tt.mode, func(t *testing.T) {
			g, calls := testGrader(t, tt.mode)
			got, err := g.Grade(context.Background(), domain.KindKnowledge, []domain.Item{{N: 1, Question: "private-card-sentinel"}}, map[int]string{1: "answer"})
			if tt.want == "" {
				if err != nil || len(got) != 1 {
					t.Fatalf("got %v, %v", got, err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v; want %s", err, tt.want)
			}
			if err != nil && (strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "sentinel")) {
				t.Fatalf("leaking error: %v", err)
			}
			if calls() != tt.calls {
				t.Fatalf("calls = %d; want %d", calls(), tt.calls)
			}
		})
	}
}

func TestRefreshOnFailureAndPersistence(t *testing.T) {
	g, _ := testGrader(t, fakecodex.RefreshFailure)
	refreshed, err := g.Check(context.Background(), testAuth)
	if err == nil || refreshed == testAuth || !strings.Contains(refreshed, "rotated") {
		t.Fatalf("Check refresh=%s err=%v", refreshed, err)
	}
	saved := false
	g.SaveAuth = func(previous, next string) error {
		saved = previous == testAuth && strings.Contains(next, "rotated")
		return errors.New("private-storage-error")
	}
	_, err = g.Grade(context.Background(), domain.KindKnowledge, []domain.Item{{N: 1}}, nil)
	if !saved || err == nil || !strings.Contains(err.Error(), "persist refreshed credentials") || strings.Contains(err.Error(), "private") {
		t.Fatalf("saved=%v error=%v", saved, err)
	}
}

func TestTimeoutAndCancellationRefresh(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(map[bool]string{false: "timeout", true: "cancel"}[cancelled], func(t *testing.T) {
			g, calls := testGrader(t, fakecodex.RefreshSlow)
			g.Auth = nil // Connection candidates are returned to the caller without persistence.
			g.Timeout = time.Second
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if cancelled {
				go func() {
					for calls() == 0 && ctx.Err() == nil {
						time.Sleep(10 * time.Millisecond)
					}
					cancel()
				}()
			}
			refreshed, err := g.Check(ctx, testAuth)
			if err == nil || !strings.Contains(refreshed, "rotated") {
				t.Fatalf("refresh=%s error=%v", refreshed, err)
			}
			if cancelled && !errors.Is(err, context.Canceled) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestEmptyGradeDoesNotRun(t *testing.T) {
	g := &Grader{Bin: "/missing"}
	got, err := g.Grade(context.Background(), domain.KindKnowledge, nil, nil)
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("got %v %v", got, err)
	}
}

func TestSuccessfulRefreshAndCleanup(t *testing.T) {
	g, _ := testGrader(t, fakecodex.RefreshGood)
	current := testAuth
	g.Auth = func() string { return current }
	g.SaveAuth = func(previous, next string) error {
		if previous != current {
			t.Error("stale previous snapshot")
		}
		current = next
		return nil
	}
	got, err := g.Grade(context.Background(), domain.KindKnowledge, []domain.Item{{N: 1}}, nil)
	if err != nil || len(got) != 1 || current == testAuth {
		t.Fatalf("results=%v error=%v refreshed=%v", got, err, current != testAuth)
	}
	homes, err := os.ReadFile(filepath.Join(filepath.Dir(g.Bin), "calls.homes"))
	if err != nil {
		t.Fatal(err)
	}
	for home := range strings.SplitSeq(strings.TrimSpace(string(homes)), "\n") {
		if _, err := os.Stat(home); !os.IsNotExist(err) {
			t.Errorf("credential home remains after call: %v", err)
		}
	}
}

func TestCheckAndGradeAreSerialized(t *testing.T) {
	g, calls := testGrader(t, fakecodex.GoodSlow)
	checked := make(chan error, 1)
	go func() { _, err := g.Check(context.Background(), testAuth); checked <- err }()
	deadline := time.Now().Add(15 * time.Second)
	for calls() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if calls() == 0 {
		t.Fatal("check did not start")
	}
	graded := make(chan error, 1)
	go func() {
		_, err := g.Grade(context.Background(), domain.KindKnowledge, []domain.Item{{N: 1}}, nil)
		graded <- err
	}()
	time.Sleep(100 * time.Millisecond)
	if calls() != 1 {
		t.Fatal("Check and Grade executed concurrently")
	}
	if err := <-checked; err != nil {
		t.Fatal(err)
	}
	if err := <-graded; err != nil {
		t.Fatal(err)
	}
}

func TestInheritedCredentialsAndEndpointsAreRemoved(t *testing.T) {
	for _, key := range []string{"OPENAI_API_KEY", "OPENAI_BASE_URL", "CODEX_API_KEY", "ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN", "HTTP_PROXY", "HTTPS_PROXY", "OTEL_EXPORTER_OTLP_ENDPOINT"} {
		t.Setenv(key, "private-inherited")
	}
	g, _ := testGrader(t, fakecodex.Good)
	if _, err := g.Check(context.Background(), testAuth); err != nil {
		t.Fatal(err)
	}
}

func TestAuthValidation(t *testing.T) {
	for _, auth := range []string{"", `{"OPENAI_API_KEY":"private"}`, `{"tokens":{"access_token":"private"}}`, strings.Repeat("x", maxAuthBytes+1)} {
		if err := ValidateAuth(auth); err == nil || strings.Contains(err.Error(), "private") {
			t.Fatalf("unsafe validation error: %v", err)
		}
	}
}

func TestCheckPersistsSavedRefreshBeforeGrade(t *testing.T) {
	g, calls := testGrader(t, fakecodex.RefreshGood)
	current := testAuth
	authReads := make(chan string, 4)
	g.Auth = func() string { authReads <- current; return current }
	saving := make(chan struct{})
	release := make(chan struct{})
	g.SaveAuth = func(previous, next string) error {
		if previous != current {
			t.Error("refresh persisted with stale previous credentials")
		}
		close(saving)
		<-release
		current = next
		return nil
	}
	checked := make(chan error, 1)
	go func() { _, err := g.Check(context.Background(), testAuth); checked <- err }()
	select {
	case <-saving:
	case err := <-checked:
		t.Fatalf("Check returned before persisting refreshed saved credentials: %v", err)
	case <-time.After(15 * time.Second):
		t.Fatal("Check never persisted refresh")
	}
	<-authReads
	graded := make(chan error, 1)
	go func() {
		_, err := g.Grade(context.Background(), domain.KindKnowledge, []domain.Item{{N: 1}}, nil)
		graded <- err
	}()
	select {
	case <-authReads:
		t.Error("Grade read stale credentials while Check persistence was pending")
	case <-time.After(100 * time.Millisecond):
	}
	if calls() != 1 {
		t.Error("Grade invoked CLI while Check persistence was pending")
	}
	close(release)
	if err := <-checked; err != nil {
		t.Fatal(err)
	}
	if err := <-graded; err != nil {
		t.Fatal(err)
	}
	if read := <-authReads; !strings.Contains(read, "rotated") {
		t.Fatal("Grade reused credentials from before Check refresh")
	}
}

func TestCheckFreshCandidateDoesNotPersist(t *testing.T) {
	g, _ := testGrader(t, fakecodex.RefreshGood)
	g.Auth = func() string { return strings.ReplaceAll(testAuth, "access-private", "saved-other") }
	g.SaveAuth = func(string, string) error {
		t.Error("fresh candidate was persisted over saved credentials")
		return nil
	}
	next, err := g.Check(context.Background(), testAuth)
	if err != nil || !strings.Contains(next, "rotated") {
		t.Fatalf("updated=%v error=%v", next != testAuth, err)
	}
}

func TestCheckPersistenceFailurePreservesCandidate(t *testing.T) {
	g, _ := testGrader(t, fakecodex.RefreshGood)
	g.SaveAuth = func(string, string) error { return errors.New("private storage failure") }
	next, err := g.Check(context.Background(), testAuth)
	if !strings.Contains(next, "rotated") || err == nil || !strings.Contains(err.Error(), "persist refreshed") || strings.Contains(err.Error(), "private") {
		t.Fatalf("updated=%v error=%v", next != testAuth, err)
	}
}

func TestCheckPersistsRefreshOnExecutionFailure(t *testing.T) {
	g, _ := testGrader(t, fakecodex.RefreshFailure)
	saved := ""
	g.SaveAuth = func(previous, next string) error {
		if previous != testAuth {
			t.Error("stale snapshot")
		}
		saved = next
		return nil
	}
	next, err := g.Check(context.Background(), testAuth)
	if saved != next || !strings.Contains(next, "rotated") || err == nil || !strings.Contains(err.Error(), "authentication failed") {
		t.Fatalf("saved=%v updated=%v error=%v", saved == next, next != testAuth, err)
	}
}
