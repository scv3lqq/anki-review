package owner

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/scv3lqq/anki-review/internal/domain"
	vaultrepo "github.com/scv3lqq/anki-review/internal/repository/vault"
)

type persistentHarness struct {
	t       *testing.T
	dir     string
	key     string
	clock   clock
	serial  int
	unlocks int
	checker fakeChecker
	u       *UseCase
	v       *persistentVaultProbe
}

// The probe injects write failures while retaining the real authenticated disk format.
type persistentVaultProbe struct {
	*vaultrepo.Vault
	saveErr     error
	postSaveErr error
	recovers    int
}

func (v *persistentVaultProbe) SaveSessions(sessions []domain.AuthSession) error {
	if v.saveErr != nil {
		return v.saveErr
	}
	if err := v.Vault.SaveSessions(sessions); err != nil {
		return err
	}
	return v.postSaveErr
}

func (v *persistentVaultProbe) Recover() (domain.Secrets, error) {
	v.recovers++
	return v.Vault.Recover()
}

func newPersistentHarness(t *testing.T) *persistentHarness {
	t.Helper()
	h := &persistentHarness{t: t, dir: t.TempDir(), key: filepath.Join(t.TempDir(), "session.key"),
		clock: clock{now: time.Date(2026, 11, 8, 10, 11, 12, 0, time.UTC)}}
	if err := os.WriteFile(h.key, bytes.Repeat([]byte{0x43}, 32), 0o600); err != nil {
		t.Fatal(err)
	}
	h.restart()
	t.Cleanup(func() { h.u.Close() })
	return h
}

func (h *persistentHarness) restart() {
	h.t.Helper()
	if h.u != nil {
		h.u.Close()
	}
	v, err := vaultrepo.New(h.dir, vaultrepo.WithCost(1, 8, 1), vaultrepo.WithSessionKey(h.key, "persistent-account"))
	if err != nil {
		h.t.Fatal(err)
	}
	h.v = &persistentVaultProbe{Vault: v}
	h.unlocks = 0
	h.u, err = New(h.v, &h.checker, Options{Log: zerolog.Nop(), Now: h.clock.Now,
		NewToken: func() string { h.serial++; return fmt.Sprintf("synthetic-persistent-token-%d", h.serial) },
		OnUnlock: func(domain.Secrets) error { h.unlocks++; return nil },
	})
	if err != nil {
		h.t.Fatal(err)
	}
}

func (h *persistentHarness) setup() string {
	h.t.Helper()
	token, err := h.u.Setup(context.Background(), testClient, SetupInput{User: testUser, Password: testPass,
		ClaudeToken: testClaude, AnkiKey: testKey, GraderProvider: "codex", CodexAuth: "synthetic-codex-original"})
	if err != nil {
		h.t.Fatal(err)
	}
	return token
}

func (h *persistentHarness) login() string {
	h.t.Helper()
	token, err := h.u.Login(context.Background(), testClient, testUser, testPass)
	if err != nil {
		h.t.Fatal(err)
	}
	return token
}

func persistentHash(token string) string {
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:])
}

func TestPersistentRestartOriginalExpiryAndLazyRecovery(t *testing.T) {
	for _, via := range []string{"status", "authorized"} {
		t.Run(via, func(t *testing.T) {
			h := newPersistentHarness(t)
			token := h.setup()
			secrets := h.u.Secrets()
			expiry := h.clock.now.Add(SessionTTL)
			h.clock.now = h.clock.now.Add(5 * 24 * time.Hour)
			h.restart()
			if h.unlocks != 0 || h.v.recovers != 0 || h.u.Secrets() != (domain.Secrets{}) {
				t.Fatal("construction recovered credentials")
			}
			if got, ok := h.u.SessionExpiry(token); !ok || !got.Equal(expiry) {
				t.Fatalf("restored expiry = %v, %v; want %v", got, ok, expiry)
			}
			for _, bad := range []string{"", "unknown-token", persistentHash(token)} {
				if h.u.Authorized(bad) || h.u.Status(bad).State == StateReady {
					t.Fatal("invalid bearer authorized")
				}
			}
			if h.unlocks != 0 || h.v.recovers != 0 {
				t.Fatal("anonymous request recovered credentials")
			}
			if via == "status" {
				if st := h.u.Status(token); st.State != StateReady || st.User != testUser || !st.Unlocked || st.GraderProvider != "codex" || !st.GraderConnected {
					t.Fatalf("restored status = %+v", st)
				}
			} else if !h.u.Authorized(token) {
				t.Fatal("persisted bearer rejected")
			}
			if h.unlocks != 1 || h.v.recovers != 1 || h.u.Secrets() != secrets {
				t.Fatal("valid bearer did not recover once")
			}
			if !h.u.Authorized(token) || h.u.Status(token).State != StateReady || h.unlocks != 1 || h.v.recovers != 1 {
				t.Fatal("authorization repeated recovery")
			}
			h.clock.now = expiry.Add(-time.Nanosecond)
			if !h.u.Authorized(token) {
				t.Fatal("session expired early")
			}
			h.clock.now = expiry
			if h.u.Authorized(token) || h.u.Status(token).State == StateReady {
				t.Fatal("session authorized at exact expiry")
			}
			if _, ok := h.u.SessionExpiry(token); ok {
				t.Fatal("expired session retained a live expiry")
			}
		})
	}
}

func TestPersistentExpiredSessionNeverRecovers(t *testing.T) {
	h := newPersistentHarness(t)
	token := h.setup()
	h.clock.now = h.clock.now.Add(SessionTTL)
	h.restart()
	if h.u.Authorized(token) || h.u.Status(token).State == StateReady {
		t.Fatal("expired bearer survived restart")
	}
	if h.v.recovers != 0 || h.unlocks != 0 || h.u.Secrets() != (domain.Secrets{}) {
		t.Fatal("expired bearer recovered credentials")
	}
}

func TestPersistentLogoutAndHashRevocationAreDurable(t *testing.T) {
	for _, via := range []string{"logout", "hash"} {
		t.Run(via, func(t *testing.T) {
			h := newPersistentHarness(t)
			revoked := h.setup()
			keep := h.login()
			var err error
			if via == "logout" {
				err = h.u.Logout(revoked)
			} else {
				err = h.u.RevokeHash(persistentHash(revoked))
			}
			if err != nil {
				t.Fatal(err)
			}
			if h.u.Authorized(revoked) || !h.u.Authorized(keep) {
				t.Fatal("revocation affected wrong session")
			}
			h.restart()
			if h.u.Authorized(revoked) {
				t.Fatal("revoked bearer resurrected after restart")
			}
			if h.v.recovers != 0 {
				t.Fatal("revoked bearer recovered credentials")
			}
			if !h.u.Authorized(keep) {
				t.Fatal("revocation dropped surviving bearer")
			}
		})
	}
}

func TestPersistentPasswordRotationKeepsOnlyInitiatorAndExpiry(t *testing.T) {
	h := newPersistentHarness(t)
	keep := h.setup()
	expiry := h.clock.now.Add(SessionTTL)
	h.clock.now = h.clock.now.Add(time.Hour)
	other := h.login()
	nextPassword := "synthetic rotated password"
	if err := h.u.Update(context.Background(), keep, UpdateInput{Password: testPass, NewPassword: &nextPassword}); err != nil {
		t.Fatal(err)
	}
	if !h.u.Authorized(keep) || h.u.Authorized(other) {
		t.Fatal("rotation did not retain only initiator")
	}
	h.restart()
	if h.u.Authorized(other) || !h.u.Authorized(keep) {
		t.Fatal("rotation sessions not durable")
	}
	if got, ok := h.u.SessionExpiry(keep); !ok || !got.Equal(expiry) {
		t.Fatalf("rotation renewed expiry: %v, %v", got, ok)
	}
	if _, err := h.u.Login(context.Background(), testClient, testUser, testPass); !errors.Is(err, domain.ErrCredentials) {
		t.Fatalf("old password accepted: %v", err)
	}
	if _, err := h.u.Login(context.Background(), testClient, testUser, nextPassword); err != nil {
		t.Fatalf("new password rejected: %v", err)
	}
}

func TestPersistentSaveFailuresDoNotPublishOrRevoke(t *testing.T) {
	for _, operation := range []string{"login", "logout", "hash"} {
		t.Run(operation, func(t *testing.T) {
			h := newPersistentHarness(t)
			token := h.setup()
			failure := errors.New("synthetic session write failure")
			h.v.saveErr = failure
			var err error
			switch operation {
			case "login":
				var created string
				created, err = h.u.Login(context.Background(), testClient, testUser, testPass)
				if created != "" {
					t.Fatal("failed durable login returned bearer")
				}
				if h.u.Authorized(fmt.Sprintf("synthetic-persistent-token-%d", h.serial)) {
					t.Fatal("failed durable login published bearer")
				}
			case "logout":
				err = h.u.Logout(token)
			case "hash":
				err = h.u.RevokeHash(persistentHash(token))
			}
			if !errors.Is(err, failure) {
				t.Fatalf("persistence failure not returned: %v", err)
			}
			if !h.u.Authorized(token) {
				t.Fatal("failed write revoked preexisting bearer")
			}
			h.restart()
			if !h.u.Authorized(token) {
				t.Fatal("failed write lost durable preexisting bearer")
			}
		})
	}
}

func TestPersistentSettingsRechecksAuthorizationAfterExternalCheck(t *testing.T) {
	for _, reason := range []string{"logout", "expiry"} {
		t.Run(reason, func(t *testing.T) {
			h := newPersistentHarness(t)
			token := h.setup()
			before := h.u.Secrets()
			h.checker.hook = func() {
				if reason == "logout" {
					if err := h.u.Logout(token); err != nil {
						t.Fatal(err)
					}
				} else {
					h.clock.now = h.clock.now.Add(SessionTTL)
				}
			}
			if err := h.u.Update(context.Background(), token, UpdateInput{Password: testPass, AnkiKey: strPtr("synthetic-replacement-key")}); !errors.Is(err, domain.ErrCredentials) {
				t.Fatalf("revoked settings call = %v, want credentials", err)
			}
			if h.u.Secrets() != before {
				t.Fatal("revoked settings call changed live credentials")
			}
			if saved, err := h.v.Open(testUser, testPass); err != nil || saved != before {
				t.Fatalf("revoked settings call changed disk credentials: %v", err)
			}
		})
	}
}

func TestPersistentSessionBoundAndHashedStorage(t *testing.T) {
	h := newPersistentHarness(t)
	first := h.setup()
	tokens := []string{first}
	for range 64 {
		h.clock.now = h.clock.now.Add(time.Second)
		tokens = append(tokens, h.login())
	}
	if h.u.Authorized(first) {
		t.Fatal("oldest expiry survived 64-session limit")
	}
	if len(h.u.sessions) != 64 {
		t.Fatalf("live sessions = %d, want 64", len(h.u.sessions))
	}
	for _, token := range tokens[1:] {
		if _, ok := h.u.sessions[token]; ok {
			t.Fatal("owner retains raw bearer as map key")
		}
		if _, ok := h.u.sessions[persistentHash(token)]; !ok {
			t.Fatal("owner did not index session by digest")
		}
	}
	h.restart()
	if h.u.Authorized(first) {
		t.Fatal("eviction not durable")
	}
	for _, token := range tokens[1:] {
		if !h.u.Authorized(token) {
			t.Fatal("session bound dropped newer bearer")
		}
	}
	raw, err := os.ReadFile(filepath.Join(h.dir, "vault.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range tokens {
		if bytes.Contains(raw, []byte(token)) {
			t.Fatal("raw bearer written to disk")
		}
	}
}

func TestPersistentCodexRefreshSurvivesRecovery(t *testing.T) {
	h := newPersistentHarness(t)
	token := h.setup()
	h.restart()
	if !h.u.Authorized(token) {
		t.Fatal("persisted session rejected")
	}
	if err := h.u.SaveCodexAuth("synthetic-codex-original", "synthetic-codex-refreshed"); err != nil {
		t.Fatal(err)
	}
	h.restart()
	if !h.u.Authorized(token) {
		t.Fatal("refresh dropped persistent session")
	}
	if got := h.u.Secrets(); got.CodexAuth != "synthetic-codex-refreshed" || got.GraderProvider != "codex" || got.ClaudeToken != testClaude || got.AnkiKey != testKey {
		t.Fatal("recovery lost refreshed or inactive credentials")
	}
}

func TestPersistentPasswordLoginMigratesLegacyVault(t *testing.T) {
	h := newPersistentHarness(t)
	h.u.Close()
	legacy, err := vaultrepo.New(h.dir, vaultrepo.WithCost(1, 8, 1))
	if err != nil {
		t.Fatal(err)
	}
	secrets := domain.Secrets{ClaudeToken: testClaude, AnkiKey: testKey}
	if err := legacy.Seal(testUser, testPass, secrets); err != nil {
		t.Fatal(err)
	}
	legacy.Close()
	h.restart()
	if h.u.Authorized("old-cookie-from-previous-process") || h.v.recovers != 0 {
		t.Fatal("legacy cookie recovered without password")
	}
	token := h.login()
	expiry := h.clock.now.Add(SessionTTL)
	h.restart()
	if !h.u.Authorized(token) || h.u.Secrets() != secrets {
		t.Fatal("successful password login did not migrate recovery")
	}
	if got, ok := h.u.SessionExpiry(token); !ok || !got.Equal(expiry) {
		t.Fatalf("migrated session expiry = %v, %v", got, ok)
	}
}

func TestPersistentRecoveryUnlockFailureDoesNotAuthorize(t *testing.T) {
	h := newPersistentHarness(t)
	token := h.setup()
	h.restart()
	h.u.opts.OnUnlock = func(domain.Secrets) error { return errors.New("synthetic passive load failure") }
	if h.u.Authorized(token) || h.u.Status(token).State == StateReady {
		t.Fatal("failed recovery unlock authorized bearer")
	}
	if h.u.Secrets() != (domain.Secrets{}) {
		t.Fatal("failed recovery unlock retained credentials")
	}
}

func TestPersistentRevocationReconcilesAmbiguousWrite(t *testing.T) {
	h := newPersistentHarness(t)
	revoked := h.setup()
	keep := h.login()
	failure := errors.New("synthetic post-rename sync failure")
	h.v.postSaveErr = failure
	if err := h.u.Logout(revoked); !errors.Is(err, failure) {
		t.Fatalf("ambiguous durable write = %v", err)
	}
	if h.u.Authorized(revoked) {
		t.Fatal("ambiguous write resurrected revoked in-memory session")
	}
	if !h.u.Authorized(keep) {
		t.Fatal("reconciliation dropped authenticated surviving session")
	}
	h.restart()
	if h.u.Authorized(revoked) || !h.u.Authorized(keep) {
		t.Fatal("restart disagrees with reconciled durable revocation")
	}
}

func TestPersistentPassiveHashRevocationDoesNotRecoverCredentials(t *testing.T) {
	h := newPersistentHarness(t)
	token := h.setup()
	h.restart()
	if err := h.u.RevokeHash(persistentHash(token)); err != nil {
		t.Fatal(err)
	}
	if h.v.recovers != 0 || h.unlocks != 0 || h.u.Secrets() != (domain.Secrets{}) {
		t.Fatal("passive hash revocation recovered credentials")
	}
	h.restart()
	if h.u.Authorized(token) || h.v.recovers != 0 {
		t.Fatal("passively revoked session recovered after restart")
	}
}

func TestPersistentLoginAtCapacityAlwaysKeepsNewSession(t *testing.T) {
	for _, mode := range []string{"equal-clock-smallest-new-hash", "clock-rollback"} {
		t.Run(mode, func(t *testing.T) {
			h := newPersistentHarness(t)
			existing := []string{h.setup()}
			for range 63 {
				existing = append(existing, h.login())
			}
			evicted := existing[0]
			for _, token := range existing[1:] {
				if persistentHash(token) < persistentHash(evicted) {
					evicted = token
				}
			}
			if mode == "clock-rollback" {
				h.clock.now = h.clock.now.Add(-time.Second)
			} else {
				candidate := ""
				for i := range 100_000 {
					token := fmt.Sprintf("synthetic-capacity-candidate-%d", i)
					if persistentHash(token) < persistentHash(evicted) {
						candidate = token
						break
					}
				}
				if candidate == "" {
					t.Fatal("could not find deterministic lower digest")
				}
				h.u.opts.NewToken = func() string { return candidate }
			}
			expiry := h.clock.now.Add(SessionTTL)
			token := h.login()
			assertSessions := func() {
				t.Helper()
				if !h.u.Authorized(token) {
					t.Error("successful login returned a session already evicted")
				}
				if len(h.u.sessions) != 64 {
					t.Errorf("live sessions = %d, want 64", len(h.u.sessions))
				}
				if got, ok := h.u.SessionExpiry(token); !ok || !got.Equal(expiry) {
					t.Errorf("new session expiry = %v, %v; want %v", got, ok, expiry)
				}
				for _, old := range existing {
					if got, want := h.u.Authorized(old), old != evicted; got != want {
						t.Error("capacity eviction did not remove exactly the oldest existing session")
					}
				}
			}
			assertSessions()
			h.restart()
			assertSessions()
		})
	}
}
