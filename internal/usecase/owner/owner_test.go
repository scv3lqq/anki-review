package owner

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/scv3lqq/anki-review/internal/domain"
)

const (
	testUser   = "owner"
	testPass   = "current password"
	testClaude = "sk-ant-oat01-first"
	testKey    = "key-one"
	// testClient is the address the owner's requests come from
	testClient = "owner-laptop"
)

// fakeVault keeps one sealed owner in memory and checks the same credentials the real vault does.
type fakeVault struct {
	exists    bool
	user      string
	password  string
	secrets   domain.Secrets
	seals     int
	resealErr error
	closed    bool
}

func (v *fakeVault) Exists() (bool, error) { return v.exists, nil }

func (v *fakeVault) Seal(user, password string, s domain.Secrets) error {
	v.exists, v.user, v.password, v.secrets = true, user, password, s
	v.seals++
	return nil
}

func (v *fakeVault) Open(user, password string) (domain.Secrets, error) {
	if !v.exists {
		return domain.Secrets{}, errors.New("no vault")
	}
	if user != v.user || password != v.password {
		return domain.Secrets{}, domain.ErrCredentials
	}
	return v.secrets, nil
}

func (v *fakeVault) Reseal(s domain.Secrets) error {
	if v.resealErr != nil {
		return v.resealErr
	}
	v.secrets = s
	v.seals++
	return nil
}
func (v *fakeVault) Close() { v.closed = true }

// fakeChecker records the tokens it was asked to check and fails with the configured errors.
type fakeChecker struct {
	ankiErr   error
	claudeErr error
	anki      []string
	claude    []string
	// hook runs once, inside the next check: a check runs without the use case's locks, so a test can
	// make another call land in the middle of it
	hook func()
}

func (c *fakeChecker) CheckAnki(_ context.Context, key string) error {
	c.anki = append(c.anki, key)
	c.runHook()
	return c.ankiErr
}

func (c *fakeChecker) CheckClaude(_ context.Context, token string) error {
	c.claude = append(c.claude, token)
	c.runHook()
	return c.claudeErr
}

func (c *fakeChecker) CheckGrader(ctx context.Context, provider, credential string) (string, error) {
	if provider == "claude" {
		return credential, c.CheckClaude(ctx, credential)
	}
	c.runHook()
	return credential, nil
}

func (c *fakeChecker) runHook() {
	if h := c.hook; h != nil {
		c.hook = nil
		h()
	}
}

// clock is a fake time source the tests move by hand.
type clock struct{ now time.Time }

func (c *clock) Now() time.Time { return c.now }

type harness struct {
	t       *testing.T
	vault   *fakeVault
	checker *fakeChecker
	clock   *clock
	unlocks int
	// unlockErr fails OnUnlock while set
	unlockErr error
	u         *UseCase
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{t: t, vault: &fakeVault{}, checker: &fakeChecker{}, clock: &clock{now: time.Unix(1_000_000, 0)}}
	h.restart()
	return h
}

// restart builds a UseCase over the same vault, as a server restart does: locked, no sessions.
func (h *harness) restart() {
	h.t.Helper()
	n := 0
	h.unlocks = 0
	u, err := New(h.vault, h.checker, Options{
		Log:      zerolog.Nop(),
		Now:      h.clock.Now,
		NewToken: func() string { n++; return fmt.Sprintf("tok%d", n) },
		OnUnlock: func(domain.Secrets) error {
			if h.unlockErr != nil {
				return h.unlockErr
			}
			h.unlocks++
			return nil
		},
	})
	if err != nil {
		h.t.Fatal(err)
	}
	h.u = u
}

func (h *harness) setup() string {
	h.t.Helper()
	token, err := h.u.Setup(context.Background(), testClient, SetupInput{User: testUser, Password: testPass, ClaudeToken: testClaude, AnkiKey: testKey})
	if err != nil {
		h.t.Fatalf("Setup() = %v", err)
	}
	return token
}

func (h *harness) login(user, pass string) (string, error) {
	return h.u.Login(context.Background(), testClient, user, pass)
}

// outcome names the result of a call, for step tables.
func outcome(err error) string {
	var throttled *ThrottledError
	switch {
	case err == nil:
		return "ok"
	case errors.As(err, &throttled):
		return fmt.Sprintf("throttled %s", throttled.Wait)
	case errors.Is(err, domain.ErrCredentials):
		return "credentials"
	default:
		return "other: " + err.Error()
	}
}

func fieldOf(err error) string {
	var fe *FieldError
	if errors.As(err, &fe) {
		return fe.Field
	}
	return ""
}

func TestSetupValidation(t *testing.T) {
	long := strings.Repeat("a", 65)
	tests := []struct {
		name  string
		in    SetupInput
		field string
	}{
		{name: "empty login", in: SetupInput{User: "  ", Password: testPass}, field: "user"},
		{name: "control character in login", in: SetupInput{User: "a\tb", Password: testPass}, field: "user"},
		{name: "claude token only spaces", in: SetupInput{User: testUser, Password: testPass, ClaudeToken: "   "}, field: "claude_token"},
		{name: "anki key only spaces", in: SetupInput{User: testUser, Password: testPass, AnkiKey: "  "}, field: "anki_key"},
		{name: "login too long", in: SetupInput{User: long, Password: testPass}, field: "user"},
		{name: "short password", in: SetupInput{User: testUser, Password: "elevenchars"}, field: "password"},
		{name: "password too long", in: SetupInput{User: testUser, Password: strings.Repeat("p", 1025)}, field: "password"},
		{name: "claude token without prefix", in: SetupInput{User: testUser, Password: testPass, ClaudeToken: "sk-openai-x"}, field: "claude_token"},
		{name: "claude token with space", in: SetupInput{User: testUser, Password: testPass, ClaudeToken: "sk-ant-oat01-a b"}, field: "claude_token"},
		{name: "claude token too long", in: SetupInput{User: testUser, Password: testPass, ClaudeToken: "sk-ant-" + strings.Repeat("x", 4096)}, field: "claude_token"},
		{name: "anki key with space", in: SetupInput{User: testUser, Password: testPass, AnkiKey: "a b"}, field: "anki_key"},
		{name: "anki key too long", in: SetupInput{User: testUser, Password: testPass, AnkiKey: strings.Repeat("k", 1025)}, field: "anki_key"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			_, err := h.u.Setup(context.Background(), testClient, tt.in)
			if got := fieldOf(err); got != tt.field {
				t.Fatalf("Setup() field = %q (%v), want %q", got, err, tt.field)
			}
			if len(h.checker.anki)+len(h.checker.claude) != 0 || h.vault.seals != 0 {
				t.Errorf("checks ran or vault sealed for invalid input: anki %v claude %v", h.checker.anki, h.checker.claude)
			}
		})
	}
}

func TestSetupAcceptsEmptyTokens(t *testing.T) {
	h := newHarness(t)
	if _, err := h.u.Setup(context.Background(), testClient, SetupInput{User: " owner ", Password: "  spaced password  "}); err != nil {
		t.Fatalf("Setup() = %v", err)
	}
	if want := (domain.Secrets{}); h.vault.secrets != want {
		t.Errorf("sealed %+v, want empty tokens", h.vault.secrets)
	}
	if h.vault.user != testUser || h.vault.password != "  spaced password  " {
		t.Errorf("sealed under user %q and password %q: login is trimmed, password is not", h.vault.user, h.vault.password)
	}
	if !reflect.DeepEqual(h.checker.claude, []string{""}) {
		t.Errorf("claude checked with %q, want the empty token checked too", h.checker.claude)
	}
}

func TestSetupCheckOrder(t *testing.T) {
	tests := []struct {
		name       string
		ankiErr    error
		claudeErr  error
		wantField  string
		wantClaude int
	}{
		{name: "both pass", wantField: "", wantClaude: 1},
		{name: "key refused", ankiErr: domain.ErrAnkiKey, wantField: "anki_key"},
		{name: "anki not reachable", ankiErr: domain.ErrAnki, wantField: "anki_key"},
		{name: "claude refused", claudeErr: errors.New("claude: error_during_execution: Invalid API key"), wantField: "claude_token", wantClaude: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			h.checker.ankiErr, h.checker.claudeErr = tt.ankiErr, tt.claudeErr
			token, err := h.u.Setup(context.Background(), testClient, SetupInput{User: testUser, Password: testPass, ClaudeToken: testClaude, AnkiKey: testKey})
			if got := fieldOf(err); got != tt.wantField {
				t.Fatalf("Setup() field = %q (%v), want %q", got, err, tt.wantField)
			}
			if tt.wantField == "" && err != nil {
				t.Fatal(err)
			}
			if len(h.checker.anki) != 1 || len(h.checker.claude) != tt.wantClaude {
				t.Errorf("anki checks %v, claude checks %v; want 1 and %d", h.checker.anki, h.checker.claude, tt.wantClaude)
			}
			if tt.wantField != "" {
				if h.vault.seals != 0 || h.u.Status(token).State != StateSetup {
					t.Errorf("a failed check must seal nothing: seals %d", h.vault.seals)
				}
				return
			}
			st := h.u.Status(token)
			if st.State != StateReady || !st.Unlocked || st.User != testUser {
				t.Errorf("Status() = %+v, want ready and unlocked", st)
			}
			if h.unlocks != 1 || h.u.Secrets() != (domain.Secrets{ClaudeToken: testClaude, AnkiKey: testKey}) {
				t.Errorf("unlocks %d, secrets %+v", h.unlocks, h.u.Secrets())
			}
		})
	}
}

func TestSetupTwiceAndLoginBeforeSetup(t *testing.T) {
	h := newHarness(t)
	if _, err := h.login(testUser, testPass); !errors.Is(err, ErrNoOwner) {
		t.Fatalf("Login() before setup = %v, want ErrNoOwner", err)
	}
	h.setup()
	if _, err := h.u.Setup(context.Background(), testClient, SetupInput{User: testUser, Password: testPass, AnkiKey: testKey}); !errors.Is(err, ErrSetupDone) {
		t.Errorf("second Setup() = %v, want ErrSetupDone", err)
	}
}

func TestStatusStates(t *testing.T) {
	h := newHarness(t)
	if st := h.u.Status(""); st.State != StateSetup || st.Unlocked {
		t.Fatalf("fresh Status() = %+v, want setup and locked", st)
	}
	token := h.setup()
	h.restart()
	if st := h.u.Status(token); st.State != StateLogin || st.Unlocked {
		t.Errorf("Status() after restart = %+v, want login and locked", st)
	}
	if h.u.Authorized(token) {
		t.Error("a session survived a restart")
	}
	if _, err := h.login(testUser, testPass); err != nil {
		t.Fatal(err)
	}
	if st := h.u.Status(""); st.State != StateLogin || !st.Unlocked {
		t.Errorf("Status() without a session = %+v, want login and unlocked", st)
	}
}

func TestLoginUnlocksOnce(t *testing.T) {
	h := newHarness(t)
	h.setup()
	h.restart()
	for i := 1; i <= 2; i++ {
		token, err := h.login("  "+testUser+" ", testPass)
		if err != nil {
			t.Fatalf("login %d: %v", i, err)
		}
		if st := h.u.Status(token); st.State != StateReady || st.User != testUser {
			t.Errorf("login %d: Status() = %+v", i, st)
		}
	}
	if h.unlocks != 1 {
		t.Errorf("OnUnlock ran %d times, want once", h.unlocks)
	}
}

func TestOnUnlockErrorLeavesLocked(t *testing.T) {
	h := newHarness(t)
	h.setup()
	h.restart()
	h.unlockErr = errors.New("review service broke")
	if _, err := h.login(testUser, testPass); err == nil || !strings.Contains(err.Error(), "review service broke") {
		t.Fatalf("Login() = %v, want the OnUnlock error", err)
	}
	if st := h.u.Status(""); st.Unlocked {
		t.Errorf("Status() = %+v, want locked", st)
	}
	if !h.vault.closed {
		t.Error("failed first unlock retained the vault key")
	}
	h.unlockErr = nil
	if _, err := h.login(testUser, testPass); err != nil || h.unlocks != 1 {
		t.Errorf("Login() after the failure = %v, unlocks %d; want success on the first unlock", err, h.unlocks)
	}
}

func TestLoginCredentials(t *testing.T) {
	tests := []struct {
		name string
		user string
		pass string
		want string
	}{
		{name: "wrong password", user: testUser, pass: "wrong password!!", want: "credentials"},
		{name: "wrong login", user: "someone", pass: testPass, want: "credentials"},
		{name: "right password", user: testUser, pass: testPass, want: "ok"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			h.setup()
			h.restart()
			if got := outcome(func() error { _, err := h.login(tt.user, tt.pass); return err }()); got != tt.want {
				t.Errorf("Login() = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestBackoff(t *testing.T) {
	tests := []struct {
		failures int
		want     time.Duration
	}{
		{0, 0}, {4, 0}, {5, time.Second}, {6, 2 * time.Second}, {9, 16 * time.Second},
		{14, 512 * time.Second}, {15, maxWait}, {100, maxWait},
	}
	for _, tt := range tests {
		if got := backoff(tt.failures); got != tt.want {
			t.Errorf("backoff(%d) = %s, want %s", tt.failures, got, tt.want)
		}
	}
}

func TestThrottling(t *testing.T) {
	type step struct {
		wait time.Duration
		pass string
		want string
	}
	const wrong, right = "wrong password!!", testPass
	tests := []struct {
		name  string
		steps []step
	}{
		{
			name: "five free, then doubling waits",
			steps: []step{
				{0, wrong, "credentials"}, {0, wrong, "credentials"}, {0, wrong, "credentials"}, {0, wrong, "credentials"}, {0, wrong, "credentials"},
				{0, right, "throttled 1s"},
				{time.Second, wrong, "credentials"},
				{0, right, "throttled 2s"},
				{time.Second, right, "throttled 1s"},
				{time.Second, right, "ok"},
			},
		},
		{
			name: "a success resets the count",
			steps: []step{
				{0, wrong, "credentials"}, {0, wrong, "credentials"}, {0, wrong, "credentials"},
				{0, right, "ok"},
				{0, wrong, "credentials"}, {0, wrong, "credentials"}, {0, wrong, "credentials"}, {0, wrong, "credentials"}, {0, wrong, "credentials"},
				{0, right, "throttled 1s"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			h.setup()
			h.restart()
			for i, s := range tt.steps {
				h.clock.now = h.clock.now.Add(s.wait)
				if got := outcome(func() error { _, err := h.login(testUser, s.pass); return err }()); got != s.want {
					t.Fatalf("step %d: %s, want %s", i, got, s.want)
				}
			}
		})
	}
}

func TestSessionExpiry(t *testing.T) {
	h := newHarness(t)
	token := h.setup()
	h.clock.now = h.clock.now.Add(SessionTTL - time.Second)
	if !h.u.Authorized(token) {
		t.Fatal("session expired before its time")
	}
	h.clock.now = h.clock.now.Add(time.Second)
	if h.u.Authorized(token) {
		t.Error("session still valid at its expiry")
	}
	if st := h.u.Status(token); st.State != StateLogin {
		t.Errorf("Status() after expiry = %+v, want login", st)
	}
}

func TestLogout(t *testing.T) {
	h := newHarness(t)
	token := h.setup()
	h.u.Logout(token)
	if h.u.Authorized(token) || h.u.Authorized("") {
		t.Error("session survived logout")
	}
}

func strPtr(s string) *string { return &s }

func TestUpdate(t *testing.T) {
	tests := []struct {
		name       string
		in         func() UpdateInput
		ankiErr    error
		wantErr    string
		wantAnki   []string
		wantClaude []string
		wantSecret domain.Secrets
		wantNewPw  bool
	}{
		{
			name:       "nil fields keep the tokens",
			in:         func() UpdateInput { return UpdateInput{Password: testPass} },
			wantSecret: domain.Secrets{ClaudeToken: testClaude, AnkiKey: testKey},
		},
		{
			name:       "replace the AnkiConnect key",
			in:         func() UpdateInput { return UpdateInput{Password: testPass, AnkiKey: strPtr("key-two")} },
			wantAnki:   []string{"key-two"},
			wantSecret: domain.Secrets{ClaudeToken: testClaude, AnkiKey: "key-two"},
		},
		{
			name:       "an empty claude token clears it and is checked",
			in:         func() UpdateInput { return UpdateInput{Password: testPass, ClaudeToken: strPtr("")} },
			wantClaude: []string{""},
			wantSecret: domain.Secrets{ClaudeToken: "", AnkiKey: testKey},
		},
		{
			name:       "a refused key keeps the old one",
			in:         func() UpdateInput { return UpdateInput{Password: testPass, AnkiKey: strPtr("bad-key")} },
			ankiErr:    domain.ErrAnkiKey,
			wantErr:    "anki_key",
			wantAnki:   []string{"bad-key"},
			wantSecret: domain.Secrets{ClaudeToken: testClaude, AnkiKey: testKey},
		},
		{
			name:       "wrong current password",
			in:         func() UpdateInput { return UpdateInput{Password: "wrong password!!", AnkiKey: strPtr("key-two")} },
			wantErr:    "credentials",
			wantSecret: domain.Secrets{ClaudeToken: testClaude, AnkiKey: testKey},
		},
		{
			name:       "empty current password",
			in:         func() UpdateInput { return UpdateInput{} },
			wantErr:    "password",
			wantSecret: domain.Secrets{ClaudeToken: testClaude, AnkiKey: testKey},
		},
		{
			name:       "new password too short",
			in:         func() UpdateInput { return UpdateInput{Password: testPass, NewPassword: strPtr("short")} },
			wantErr:    "new_password",
			wantSecret: domain.Secrets{ClaudeToken: testClaude, AnkiKey: testKey},
		},
		{
			name:       "new password re-seals and drops the other sessions",
			in:         func() UpdateInput { return UpdateInput{Password: testPass, NewPassword: strPtr("brand new password")} },
			wantSecret: domain.Secrets{ClaudeToken: testClaude, AnkiKey: testKey},
			wantNewPw:  true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			h.setup()
			h.checker.ankiErr = tt.ankiErr
			h.restart()
			keep, err := h.login(testUser, testPass)
			if err != nil {
				t.Fatal(err)
			}
			other, err := h.login(testUser, testPass)
			if err != nil {
				t.Fatal(err)
			}
			h.checker.anki, h.checker.claude = nil, nil

			err = h.u.Update(context.Background(), keep, tt.in())
			switch {
			case tt.wantErr == "credentials":
				if !errors.Is(err, domain.ErrCredentials) {
					t.Fatalf("Update() = %v, want credentials", err)
				}
			case tt.wantErr != "":
				if got := fieldOf(err); got != tt.wantErr {
					t.Fatalf("Update() field = %q (%v), want %q", got, err, tt.wantErr)
				}
			case err != nil:
				t.Fatalf("Update() = %v", err)
			}
			if !reflect.DeepEqual(h.checker.anki, tt.wantAnki) || !reflect.DeepEqual(h.checker.claude, tt.wantClaude) {
				t.Errorf("checks: anki %q claude %q; want anki %q claude %q", h.checker.anki, h.checker.claude, tt.wantAnki, tt.wantClaude)
			}
			if got := h.u.Secrets(); got != tt.wantSecret {
				t.Errorf("Secrets() = %+v, want %+v", got, tt.wantSecret)
			}
			if h.vault.secrets != tt.wantSecret && tt.wantErr == "" {
				t.Errorf("sealed %+v, want %+v", h.vault.secrets, tt.wantSecret)
			}
			if tt.wantNewPw {
				if !h.u.Authorized(keep) || h.u.Authorized(other) {
					t.Error("changing the password must keep the session that made it and drop the others")
				}
				if _, err := h.login(testUser, testPass); !errors.Is(err, domain.ErrCredentials) {
					t.Errorf("old password still opens: %v", err)
				}
				if _, err := h.login(testUser, "brand new password"); err != nil {
					t.Errorf("new password refused: %v", err)
				}
			} else if !h.u.Authorized(keep) || !h.u.Authorized(other) {
				t.Error("a change that keeps the password must keep every session")
			}
		})
	}
}

func TestThrottleIsPerClient(t *testing.T) {
	tests := []struct {
		name   string
		client string
		want   string
	}{
		{name: "stranger still throttled", client: "stranger", want: "throttled 1s"},
		{name: "owner from another address", client: testClient, want: "ok"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			h.setup()
			h.restart()
			for range 5 {
				if _, err := h.u.Login(context.Background(), "stranger", testUser, "wrong password!!"); !errors.Is(err, domain.ErrCredentials) {
					t.Fatalf("wrong password = %v, want credentials", err)
				}
			}
			_, err := h.u.Login(context.Background(), tt.client, testUser, testPass)
			if got := outcome(err); got != tt.want {
				t.Errorf("Login() from %q = %s, want %s", tt.client, got, tt.want)
			}
		})
	}
}

func TestSetupChecksCountAgainstTheClient(t *testing.T) {
	tests := []struct {
		name     string
		ankiErr  error
		invalid  bool
		wantStep string
	}{
		{name: "refused key counts", ankiErr: domain.ErrAnkiKey, wantStep: "throttled 1s"},
		{name: "form errors do not count", invalid: true, wantStep: "ok"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			h.checker.ankiErr = tt.ankiErr
			ctx := context.Background()
			in := SetupInput{User: testUser, Password: testPass, ClaudeToken: testClaude, AnkiKey: testKey}
			if tt.invalid {
				for range 10 {
					_, err := h.u.Setup(ctx, "stranger", SetupInput{User: "", Password: testPass})
					if fieldOf(err) != "user" {
						t.Fatalf("invalid form = %v, want a user field error", err)
					}
				}
				if _, err := h.u.Setup(ctx, "stranger", in); err != nil {
					t.Fatalf("valid form after invalid ones = %v", err)
				}
				return
			}
			for range 5 {
				if _, err := h.u.Setup(ctx, "stranger", in); fieldOf(err) != "anki_key" {
					t.Fatalf("refused key = %v, want anki_key", err)
				}
			}
			checks := len(h.checker.anki)
			_, err := h.u.Setup(ctx, "stranger", in)
			if got := outcome(err); got != tt.wantStep {
				t.Fatalf("sixth attempt = %s, want %s", got, tt.wantStep)
			}
			if len(h.checker.anki) != checks {
				t.Error("a throttled client's setup ran a check")
			}
			h.checker.ankiErr = nil
			if _, err := h.u.Setup(ctx, testClient, in); err != nil {
				t.Errorf("owner's setup from another address = %v", err)
			}
		})
	}
}

func TestSettingsNotBlockedByLoginFailures(t *testing.T) {
	h := newHarness(t)
	h.setup()
	h.restart()
	token, err := h.login(testUser, testPass)
	if err != nil {
		t.Fatal(err)
	}
	for range 7 {
		_, _ = h.u.Login(context.Background(), "stranger", testUser, "wrong password!!")
	}
	if err := h.u.Update(context.Background(), token, UpdateInput{Password: testPass, AnkiKey: strPtr("key-two")}); err != nil {
		t.Fatalf("Update() after anonymous login failures = %v, want success", err)
	}
}

func TestSettingsWrongPasswordThrottles(t *testing.T) {
	h := newHarness(t)
	h.setup()
	h.restart()
	token, err := h.login(testUser, testPass)
	if err != nil {
		t.Fatal(err)
	}
	steps := []string{"credentials", "credentials", "credentials", "credentials", "credentials", "throttled 1s", "throttled 1s"}
	for i, want := range steps {
		got := outcome(h.u.Update(context.Background(), token, UpdateInput{Password: "wrong password!!"}))
		if got != want {
			t.Fatalf("attempt %d = %s, want %s", i+1, got, want)
		}
	}
	if err := h.u.Update(context.Background(), token, UpdateInput{Password: testPass}); err == nil {
		t.Error("the right password got through the settings throttle")
	}
}

func TestUpdateDetectsConcurrentSettings(t *testing.T) {
	h := newHarness(t)
	h.setup()
	h.restart()
	token, err := h.login(testUser, testPass)
	if err != nil {
		t.Fatal(err)
	}
	// another settings call lands while this one is checking its AnkiConnect key
	h.checker.hook = func() {
		if err := h.u.Update(context.Background(), token, UpdateInput{Password: testPass, ClaudeToken: strPtr("sk-ant-oat01-other")}); err != nil {
			t.Errorf("inner Update() = %v", err)
		}
	}
	err = h.u.Update(context.Background(), token, UpdateInput{Password: testPass, AnkiKey: strPtr("key-two")})
	if !errors.Is(err, ErrChanged) {
		t.Fatalf("Update() = %v, want ErrChanged", err)
	}
	want := domain.Secrets{ClaudeToken: "sk-ant-oat01-other", AnkiKey: testKey}
	if got := h.u.Secrets(); got != want || h.vault.secrets != want {
		t.Errorf("secrets %+v, sealed %+v; want the first call's result %+v and no overwrite", got, h.vault.secrets, want)
	}
}

func TestUnlockFailureClearsSecrets(t *testing.T) {
	h := newHarness(t)
	h.setup()
	h.restart()
	h.unlockErr = errors.New("resume failed")
	_, err := h.login(testUser, testPass)
	if !errors.Is(err, ErrUnlock) {
		t.Fatalf("Login() = %v, want ErrUnlock", err)
	}
	if got := h.u.Secrets(); got != (domain.Secrets{}) {
		t.Errorf("Secrets() after a failed unlock = %+v, want none", got)
	}
}

// Resumed grading starts in OnUnlock and reads the token from another goroutine: it must see the
// token the owner logged in with, not an empty one.
func TestResumedGradingSeesToken(t *testing.T) {
	v := &fakeVault{}
	checker := &fakeChecker{}
	first, err := New(v, checker, Options{Log: zerolog.Nop()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.Setup(context.Background(), testClient, SetupInput{User: testUser, Password: testPass, ClaudeToken: testClaude, AnkiKey: testKey}); err != nil {
		t.Fatal(err)
	}
	seen := make(chan domain.Secrets, 1)
	var restarted *UseCase
	restarted, err = New(v, checker, Options{Log: zerolog.Nop(), OnUnlock: func(domain.Secrets) error {
		go func() { seen <- restarted.Secrets() }()
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.Login(context.Background(), testClient, testUser, testPass); err != nil {
		t.Fatal(err)
	}
	if got := <-seen; got.ClaudeToken != testClaude || got.AnkiKey != testKey {
		t.Errorf("resumed grading saw %+v, want the saved tokens", got)
	}
}
