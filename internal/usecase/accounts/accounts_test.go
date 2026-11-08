package accounts

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/scv3lqq/anki-review/internal/domain"
	"github.com/scv3lqq/anki-review/internal/usecase/owner"
)

const password = "a private password"

type memoryStore struct {
	state    State
	fail     bool
	failNext int
}

func (s *memoryStore) Load() (State, error) { return copyState(s.state), nil }
func (s *memoryStore) Save(state State) error {
	if s.failNext > 0 {
		s.failNext--
		return errors.New("one-shot disk failure")
	}
	if s.fail {
		return errors.New("disk unavailable")
	}
	s.state = copyState(state)
	return nil
}

type fakeVault struct {
	mu                 sync.Mutex
	user, password     string
	secrets            domain.Secrets
	seals              int
	closed             bool
	openHook           func()
	sealErrAfterWrite  error
	existsErr          error
	existsErrAfterSeal error
}

func (v *fakeVault) Exists() (bool, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.user != "", v.existsErr
}
func (v *fakeVault) Seal(user, pass string, secrets domain.Secrets) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.user, v.password, v.secrets = user, pass, secrets
	v.seals++
	v.existsErr = v.existsErrAfterSeal
	return v.sealErrAfterWrite
}
func (v *fakeVault) Open(user, pass string) (domain.Secrets, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.openHook != nil {
		v.openHook()
	}
	if user != v.user || pass != v.password {
		return domain.Secrets{}, domain.ErrCredentials
	}
	return v.secrets, nil
}
func (v *fakeVault) Reseal(secrets domain.Secrets) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.secrets = secrets
	return nil
}
func (v *fakeVault) Close() { v.mu.Lock(); defer v.mu.Unlock(); v.closed = true }

type fakeChecker struct {
	fail bool
	hook func()
}

func (c *fakeChecker) CheckAnki(context.Context, string) error {
	if c.hook != nil {
		c.hook()
	}
	if c.fail {
		return domain.ErrAnki
	}
	return nil
}
func (*fakeChecker) CheckGrader(_ context.Context, _, credential string) (string, error) {
	return credential, nil
}

type fakeFactory struct {
	t                  *testing.T
	now                func() time.Time
	vaults             map[string]*fakeVault
	checker            *fakeChecker
	opens, removes     []string
	openErr, removeErr error
	unlockErr          error
	closeErr           error
	closeCalls         int
	removeHook         func()
	newVault           func() *fakeVault
}

func (f *fakeFactory) Open(_ context.Context, rec Record) (*Runtime, error) {
	f.opens = append(f.opens, rec.ID)
	if f.openErr != nil {
		return nil, f.openErr
	}
	v := f.vaults[rec.ID]
	if v == nil {
		v = &fakeVault{}
		if f.newVault != nil {
			v = f.newVault()
		}
		f.vaults[rec.ID] = v
	}
	auth, err := owner.New(v, f.checker, owner.Options{Log: zerolog.Nop(), Now: f.now, OnUnlock: func(domain.Secrets) error { return f.unlockErr }})
	if err != nil {
		f.t.Fatal(err)
	}
	return &Runtime{Auth: auth, AnkiKey: "managed-key", Close: func(context.Context) error { f.closeCalls++; return f.closeErr }}, nil
}
func (f *fakeFactory) Remove(_ context.Context, id string) error {
	f.removes = append(f.removes, id)
	if f.removeHook != nil {
		f.removeHook()
	}
	if v := f.vaults[id]; v != nil {
		// The factory's independent filesystem guard still sees a durable file
		// when the authentication adapter's metadata read fails transiently.
		v.mu.Lock()
		exists := v.user != ""
		v.mu.Unlock()
		if exists {
			return ErrHasVault
		}
	}
	if f.removeErr == nil {
		delete(f.vaults, id)
	}
	return f.removeErr
}

func TestPostWriteSealFailureNeverReplacesGuestPassword(t *testing.T) {
	h := newHarness(t, 4)
	h.factory.newVault = func() *fakeVault {
		return &fakeVault{sealErrAfterWrite: errors.New("directory fsync failed after replacement")}
	}
	invite := h.invite(t)
	if _, err := h.m.Register(context.Background(), "guest", invite.Token, input("guest")); err == nil {
		t.Fatal("seal durability failure accepted")
	}
	vault := h.factory.vaults[h.store.state.Accounts[1].ID]
	changed := input("guest")
	changed.Password = "a newly chosen password"
	if _, err := h.m.Register(context.Background(), "guest", invite.Token, changed); !errors.Is(err, domain.ErrCredentials) {
		t.Fatalf("retry did not authenticate original vault: %v", err)
	}
	if vault.seals != 1 || vault.password != password {
		t.Fatal("retry replaced a durably existing vault")
	}
	h.now = invite.ExpiresAt
	if _, err := h.m.Invite(h.token); err != nil {
		t.Fatal(err)
	}
	if _, err := h.m.Login(context.Background(), "guest", "guest", password); err != nil {
		t.Fatalf("original password could not recover without restart: %v", err)
	}
}

func TestPostWriteSealFailureMetadataErrorBlocksRecovery(t *testing.T) {
	h := newHarness(t, 4)
	metadataErr := errors.New("vault metadata unavailable")
	h.factory.newVault = func() *fakeVault {
		return &fakeVault{sealErrAfterWrite: errors.New("directory fsync failed"), existsErrAfterSeal: metadataErr}
	}
	invite := h.invite(t)
	if _, err := h.m.Register(context.Background(), "guest", invite.Token, input("guest")); !errors.Is(err, ErrUnavailable) || !errors.Is(err, metadataErr) {
		t.Fatalf("refresh failure not propagated: %v", err)
	}
	vault := h.factory.vaults[h.store.state.Accounts[1].ID]
	changed := input("guest")
	changed.Password = "a newly chosen password"
	if _, err := h.m.Register(context.Background(), "guest", invite.Token, changed); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("retry ignored unavailable vault metadata: %v", err)
	}
	h.now = invite.ExpiresAt
	if result, err := h.m.Invite(h.token); result.Token != "" || !errors.Is(err, ErrUnavailable) {
		t.Fatalf("expired recovery ignored unavailable vault metadata: %v", err)
	}
	if !h.store.state.Accounts[1].Pending || h.store.state.Invitations[0].Used || vault.seals != 1 || vault.password != password {
		t.Fatal("failed refresh published or resealed account")
	}
	vault.existsErr, vault.existsErrAfterSeal = nil, nil
	if _, err := h.m.Invite(h.token); err != nil {
		t.Fatal(err)
	}
	if _, err := h.m.Login(context.Background(), "guest", "guest", password); err != nil {
		t.Fatal(err)
	}
}

func TestPostWriteSealFailureNeverReplacesRootPassword(t *testing.T) {
	vault := &fakeVault{sealErrAfterWrite: errors.New("directory fsync failed after replacement")}
	factory := &fakeFactory{t: t, vaults: make(map[string]*fakeVault), checker: &fakeChecker{}}
	auth, err := owner.New(vault, factory.checker, owner.Options{Log: zerolog.Nop()})
	if err != nil {
		t.Fatal(err)
	}
	manager, err := New(&memoryStore{}, factory, &Runtime{Auth: auth}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Setup(context.Background(), "root", input("root")); err == nil {
		t.Fatal("seal durability failure accepted")
	}
	changed := input("root")
	changed.Password = "a newly chosen password"
	if _, err := manager.Setup(context.Background(), "root", changed); !errors.Is(err, owner.ErrSetupDone) {
		t.Fatalf("root setup ignored durable vault: %v", err)
	}
	if vault.seals != 1 || vault.password != password {
		t.Fatal("root setup replaced a durably existing vault")
	}
	if _, err := manager.Login(context.Background(), "root", "root", password); err != nil {
		t.Fatal(err)
	}
}

func TestExpiredPendingCleanupIsRetryableAndPreservesVault(t *testing.T) {
	for _, hasVault := range []bool{false, true} {
		t.Run(fmt.Sprint(hasVault), func(t *testing.T) {
			h := newHarness(t, 2)
			invite := h.invite(t)
			if hasVault {
				h.factory.unlockErr = errors.New("resume unavailable")
			} else {
				h.factory.openErr = errors.New("runtime unavailable")
				h.factory.removeErr = errors.New("cleanup unavailable")
			}
			if _, err := h.m.Register(context.Background(), "guest", invite.Token, input("guest")); err == nil {
				t.Fatal("failure expected")
			}
			id := h.store.state.Accounts[1].ID
			h.factory.openErr, h.factory.removeErr, h.factory.unlockErr = nil, nil, nil
			h.now = invite.ExpiresAt
			h.invite(t)
			if hasVault {
				if len(h.store.state.Accounts) != 2 || h.store.state.Accounts[1].Pending || h.factory.vaults[id].seals != 1 {
					t.Fatal("sealed pending account lost")
				}
				if _, err := h.m.Login(context.Background(), "guest", "guest", password); err != nil {
					t.Fatal(err)
				}
			} else if len(h.store.state.Accounts) != 1 {
				t.Fatal("expired empty pending reservation leaked capacity")
			}
		})
	}
}

func TestExpiredCleanupPersistenceFailureAbortsOperation(t *testing.T) {
	for _, hasVault := range []bool{false, true} {
		for _, operation := range []string{"invite", "register"} {
			t.Run(fmt.Sprintf("vault=%t/%s", hasVault, operation), func(t *testing.T) {
				h := newHarness(t, 4)
				expiring := h.invite(t)
				if hasVault {
					h.factory.unlockErr = errors.New("resume unavailable")
				} else {
					h.factory.openErr, h.factory.removeErr = errors.New("runtime unavailable"), errors.New("cleanup unavailable")
				}
				if _, err := h.m.Register(context.Background(), "guest", expiring.Token, input("guest")); err == nil {
					t.Fatal("pending registration expected")
				}
				h.factory.openErr, h.factory.removeErr, h.factory.unlockErr = nil, nil, nil
				h.now = h.now.Add(12 * time.Hour)
				fresh := h.invite(t)
				h.now = expiring.ExpiresAt
				before := copyState(h.store.state)
				h.store.failNext = 1
				run := func() (string, error) {
					if operation == "register" {
						return h.m.Register(context.Background(), "other", fresh.Token, input("other"))
					}
					result, err := h.m.Invite(h.token)
					return result.Token, err
				}
				if token, err := run(); token != "" || !errors.Is(err, ErrUnavailable) {
					t.Fatalf("cleanup persistence failure accepted: token=%q, err=%v", token, err)
				}
				if !reflect.DeepEqual(h.store.state, before) || !reflect.DeepEqual(h.m.state, before) {
					t.Fatal("failed cleanup changed durable reservation or published an invitation")
				}
				if token, err := run(); token == "" || err != nil {
					t.Fatalf("cleanup retry failed: token=%q, err=%v", token, err)
				}
				if hasVault && h.factory.vaults[before.Accounts[1].ID].seals != 1 {
					t.Fatal("cleanup retry resealed existing vault")
				}
			})
		}
	}
}

func TestExpiredCleanupCloseFailureKeepsRuntimeAndReservation(t *testing.T) {
	for _, operation := range []string{"invite", "register"} {
		t.Run(operation, func(t *testing.T) {
			h := newHarness(t, 4)
			expiring := h.invite(t)
			h.factory.checker.fail, h.factory.closeErr = true, errors.New("review still draining")
			if _, err := h.m.Register(context.Background(), "guest", expiring.Token, input("guest")); err == nil {
				t.Fatal("pending registration expected")
			}
			id := h.store.state.Accounts[1].ID
			runtime := h.m.slots[id].runtime
			h.now = h.now.Add(12 * time.Hour)
			fresh := h.invite(t)
			h.now = expiring.ExpiresAt
			before := copyState(h.store.state)
			var err error
			if operation == "register" {
				_, err = h.m.Register(context.Background(), "other", fresh.Token, input("other"))
			} else {
				_, err = h.m.Invite(h.token)
			}
			if !errors.Is(err, ErrUnavailable) {
				t.Fatalf("Close failure accepted: %v", err)
			}
			if !reflect.DeepEqual(h.store.state, before) || h.m.slots[id] == nil || h.m.slots[id].runtime != runtime {
				t.Fatal("close failure released tracked reservation/runtime")
			}
			if len(h.factory.removes) != 0 {
				t.Fatal("resources removed before runtime Close completed")
			}
			if h.factory.vaults[id].closed {
				t.Fatal("vault handle cleared while runtime Close was incomplete")
			}
			h.factory.closeErr, h.factory.checker.fail = nil, false
			if _, err := h.m.Invite(h.token); err != nil {
				t.Fatal(err)
			}
			if len(h.store.state.Accounts) != 1 || h.m.slots[id] != nil || len(h.factory.removes) != 1 {
				t.Fatal("successful retry did not release reservation")
			}
		})
	}
}

func TestExpiredCleanupCannotReopenRemovedSlot(t *testing.T) {
	h := newHarness(t, 4)
	expiring := h.invite(t)
	h.factory.checker.fail, h.factory.closeErr = true, errors.New("review still draining")
	if _, err := h.m.Register(context.Background(), "guest", expiring.Token, input("guest")); err == nil {
		t.Fatal("pending registration expected")
	}
	h.factory.closeErr = nil
	h.now = expiring.ExpiresAt
	removing, release := make(chan struct{}), make(chan struct{})
	h.factory.removeHook = func() { close(removing); <-release }
	inviteDone, loginDone := make(chan error, 1), make(chan error, 1)
	go func() { _, err := h.m.Invite(h.token); inviteDone <- err }()
	<-removing
	go func() { _, err := h.m.Login(context.Background(), "guest", "guest", password); loginDone <- err }()
	// Remove holds the slot lock but no metadata lock. Wait for Login's actual
	// mutex wait, so cleanup deterministically wins after Login captured the slot.
	blocked := false
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && !blocked {
		stack := make([]byte, 64*1024)
		n := runtime.Stack(stack, true)
		for _, goroutine := range strings.Split(string(stack[:n]), "\n\n") {
			if strings.Contains(goroutine, "accounts.(*Manager).open(") && strings.Contains(goroutine, "sync.(*Mutex).lockSlow(") {
				blocked = true
				break
			}
		}
		runtime.Gosched()
	}
	close(release)
	if err := <-inviteDone; err != nil {
		t.Fatal(err)
	}
	loginErr := <-loginDone
	if !blocked {
		t.Fatal("login never reached the blocked slot lock")
	}
	if !errors.Is(loginErr, ErrUnavailable) {
		t.Fatalf("stale open was not refused: %v", loginErr)
	}
	if len(h.factory.opens) != 1 || len(h.factory.vaults) != 0 {
		t.Fatal("login created an untracked runtime after cleanup")
	}
}

type harness struct {
	m       *Manager
	store   *memoryStore
	factory *fakeFactory
	root    *Runtime
	now     time.Time
	token   string
}

func newHarness(t *testing.T, capacity int) *harness {
	t.Helper()
	f := &fakeFactory{t: t, vaults: make(map[string]*fakeVault), checker: &fakeChecker{}}
	h := &harness{store: &memoryStore{}, factory: f, now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	f.now = func() time.Time { return h.now }
	v := &fakeVault{}
	auth, err := owner.New(v, f.checker, owner.Options{Log: zerolog.Nop(), Now: f.now})
	if err != nil {
		t.Fatal(err)
	}
	h.root = &Runtime{Auth: auth}
	h.m, err = New(h.store, f, h.root, Options{MaxAccounts: capacity, Now: func() time.Time { return h.now }})
	if err != nil {
		t.Fatal(err)
	}
	h.token, err = h.m.Setup(context.Background(), "owner-client", input("root"))
	if err != nil {
		t.Fatal(err)
	}
	return h
}
func input(user string) owner.SetupInput {
	return owner.SetupInput{User: user, Password: password, ClaudeToken: "sk-ant-test", AnkiKey: "untrusted-input"}
}
func (h *harness) invite(t *testing.T) InviteResult {
	t.Helper()
	in, err := h.m.Invite(h.token)
	if err != nil {
		t.Fatal(err)
	}
	return in
}
func (h *harness) register(t *testing.T, user string) string {
	t.Helper()
	token, err := h.m.Register(context.Background(), user, h.invite(t).Token, input(user))
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func TestInvitationsAuthorizationExpiryAndSingleUse(t *testing.T) {
	h := newHarness(t, 4)
	if _, err := h.m.Invite("invalid"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("anonymous invite: %v", err)
	}
	invite := h.invite(t)
	raw, err := hex.DecodeString(invite.Token)
	if err != nil || len(raw) != 32 {
		t.Fatalf("invitation lacks 256 random bits")
	}
	hash := sha256.Sum256([]byte(invite.Token))
	if h.store.state.Invitations[0].Hash != hex.EncodeToString(hash[:]) {
		t.Fatal("registry did not store token hash")
	}
	if !invite.ExpiresAt.Equal(h.now.Add(24 * time.Hour)) {
		t.Fatal("unexpected expiration")
	}
	guest, err := h.m.Register(context.Background(), "guest", invite.Token, input("guest"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.m.Invite(guest); !errors.Is(err, ErrForbidden) {
		t.Fatalf("guest invite: %v", err)
	}
	if _, err := h.m.Register(context.Background(), "other", invite.Token, input("other")); !errors.Is(err, ErrInvitation) {
		t.Fatalf("reused invite: %v", err)
	}
	expired := h.invite(t)
	h.now = expired.ExpiresAt
	if _, err := h.m.Register(context.Background(), "late", expired.Token, input("late")); !errors.Is(err, ErrInvitation) {
		t.Fatalf("expired invite: %v", err)
	}
	if _, _, ok := h.m.Resolve("owner"); ok {
		t.Fatal("caller-supplied account ID authorized")
	}
}

func TestCapacityAndDuplicateUser(t *testing.T) {
	h := newHarness(t, 2)
	for _, user := range []string{" root ", "", "bad\nname", strings.Repeat("界", 65)} {
		_, err := h.m.Register(context.Background(), "duplicate", h.invite(t).Token, input(user))
		var field *owner.FieldError
		if !errors.As(err, &field) || field.Field != "user" {
			t.Fatalf("username %q: %v", user, err)
		}
	}
	h.register(t, " guest ")
	if _, err := h.m.Register(context.Background(), "extra", h.invite(t).Token, input("extra")); !errors.Is(err, ErrCapacity) {
		t.Fatalf("capacity: %v", err)
	}
	if len(h.factory.opens) != 1 {
		t.Fatal("allocated beyond capacity")
	}
}

func TestLiveInvitationLimitAndPruning(t *testing.T) {
	h := newHarness(t, 4)
	for range 16 {
		h.invite(t)
	}
	if _, err := h.m.Invite(h.token); !errors.Is(err, ErrCapacity) {
		t.Fatalf("invitation cap: %v", err)
	}
	h.now = h.now.Add(24 * time.Hour)
	h.invite(t)
	if len(h.store.state.Invitations) != 1 {
		t.Fatal("expired invitations not pruned")
	}
}

func TestRegistrationOwnCredentialsAndInfrastructure(t *testing.T) {
	h := newHarness(t, 4)
	in := input("guest")
	in.ClaudeToken = " "
	_, err := h.m.Register(context.Background(), "guest", h.invite(t).Token, in)
	var field *owner.FieldError
	if !errors.As(err, &field) || field.Field != "claude_token" || len(h.factory.opens) != 0 {
		t.Fatalf("missing selected credential: %v", err)
	}
	token := h.register(t, "guest")
	rec, rt, ok := h.m.Resolve(token)
	if !ok || rec.ID == "owner" || len(rec.ID) != 32 || rec.User != "guest" || rec.Pending {
		t.Fatalf("resolved guest %+v", rec)
	}
	if rt.Auth.Secrets().AnkiKey != "managed-key" {
		t.Fatal("caller infrastructure key reached vault")
	}
	root, rootRT, ok := h.m.Resolve(h.token)
	if !ok || root.ID != "owner" || rootRT != h.root {
		t.Fatal("guest replaced root runtime")
	}
	if _, _, ok := h.m.Resolve(rec.ID); ok {
		t.Fatal("account ID is a session")
	}
}

func TestFailedProvisionCleanupAndRetry(t *testing.T) {
	for _, cleanupFails := range []bool{false, true} {
		t.Run(fmt.Sprint(cleanupFails), func(t *testing.T) {
			h := newHarness(t, 2)
			invite := h.invite(t)
			h.factory.openErr = errors.New("not ready")
			if cleanupFails {
				h.factory.removeErr = errors.New("cleanup unavailable")
			}
			if _, err := h.m.Register(context.Background(), "guest", invite.Token, input("guest")); !errors.Is(err, ErrUnavailable) {
				t.Fatalf("provisioning: %v", err)
			}
			id := h.factory.opens[0]
			if len(h.factory.removes) != 1 || h.factory.removes[0] != id {
				t.Fatal("wrong cleanup target")
			}
			if cleanupFails && (len(h.store.state.Accounts) != 2 || !h.store.state.Accounts[1].Pending) {
				t.Fatal("lost pending reservation")
			}
			h.factory.openErr, h.factory.removeErr = nil, nil
			if _, err := h.m.Register(context.Background(), "guest", invite.Token, input("guest")); err != nil {
				t.Fatal(err)
			}
			if cleanupFails && h.factory.opens[1] != id {
				t.Fatal("retry allocated a different runtime")
			}
		})
	}
}

func TestPendingVaultRestartNeverReseals(t *testing.T) {
	h := newHarness(t, 4)
	invite := h.invite(t)
	h.factory.unlockErr = errors.New("resume unavailable")
	if _, err := h.m.Register(context.Background(), "guest", invite.Token, input("guest")); err == nil {
		t.Fatal("unlock failure accepted")
	}
	rec := h.store.state.Accounts[1]
	v := h.factory.vaults[rec.ID]
	if v == nil || v.seals != 1 || len(h.factory.removes) != 0 || !rec.Pending {
		t.Fatal("pending vault destroyed")
	}
	h.factory.unlockErr = nil
	var err error
	h.m, err = New(h.store, h.factory, h.root, Options{RootUser: "root", Now: func() time.Time { return h.now }})
	if err != nil {
		t.Fatal(err)
	}
	changed := input("guest")
	changed.Password = "a different password"
	if _, err := h.m.Register(context.Background(), "guest", invite.Token, changed); !errors.Is(err, domain.ErrCredentials) {
		t.Fatalf("changed password replaced pending vault: %v", err)
	}
	token, err := h.m.Login(context.Background(), "guest", " guest ", password)
	if err != nil {
		t.Fatal(err)
	}
	if v.seals != 1 || h.store.state.Accounts[1].Pending || !h.store.state.Invitations[0].Used {
		t.Fatal("recovery did not commit existing vault")
	}
	if _, _, ok := h.m.Resolve(token); !ok {
		t.Fatal("recovery session unavailable")
	}
}

func TestPersistenceFailsClosedAndCanRecover(t *testing.T) {
	h := newHarness(t, 4)
	invite := h.invite(t)
	h.factory.checker.hook = func() { h.store.fail = true }
	if token, err := h.m.Register(context.Background(), "guest", invite.Token, input("guest")); token != "" || !errors.Is(err, ErrUnavailable) {
		t.Fatalf("durable commit failure: token %q, %v", token, err)
	}
	if !h.store.state.Accounts[1].Pending {
		t.Fatal("reservation not durable before vault")
	}
	h.store.fail = false
	h.factory.checker.hook = nil
	token, err := h.m.Register(context.Background(), "guest", invite.Token, input("guest"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, ok := h.m.Resolve(token); !ok {
		t.Fatal("retry failed")
	}
	if h.factory.vaults[h.store.state.Accounts[1].ID].seals != 1 {
		t.Fatal("retry resealed vault")
	}
}

func TestGlobalThrottleAcrossUsers(t *testing.T) {
	h := newHarness(t, 4)
	h.register(t, "guest")
	for i := range 5 {
		_, err := h.m.Login(context.Background(), "attacker", fmt.Sprintf("unknown%d", i), "wrong")
		if !errors.Is(err, domain.ErrCredentials) {
			t.Fatal(err)
		}
	}
	_, err := h.m.Login(context.Background(), "attacker", "root", password)
	var throttled *owner.ThrottledError
	if !errors.As(err, &throttled) {
		t.Fatalf("cross-account throttle: %v", err)
	}
	if _, err := h.m.Login(context.Background(), "different", "guest", password); err != nil {
		t.Fatal(err)
	}
}

func TestResolveNotBlockedByRegistrationNetwork(t *testing.T) {
	h := newHarness(t, 4)
	invite := h.invite(t)
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	h.factory.checker.hook = func() { close(entered); <-release }
	go func() {
		_, err := h.m.Register(context.Background(), "guest", invite.Token, input("guest"))
		done <- err
	}()
	<-entered
	resolved := make(chan bool, 1)
	go func() { _, _, ok := h.m.Resolve(h.token); resolved <- ok }()
	select {
	case ok := <-resolved:
		if !ok {
			t.Error("root session unavailable")
		}
	case <-time.After(time.Second):
		t.Error("metadata mutex held during network check")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestResolveNotBlockedByAnotherAccountUnlock(t *testing.T) {
	h := newHarness(t, 4)
	guest := h.register(t, "guest")
	entered, release, finished := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	v := &fakeVault{user: "root", password: password, secrets: domain.Secrets{ClaudeToken: "sk-ant-test"}}
	lockedRoot, err := owner.New(v, h.factory.checker, owner.Options{Log: zerolog.Nop(), OnUnlock: func(domain.Secrets) error { close(entered); <-release; return nil }})
	if err != nil {
		t.Fatal(err)
	}
	h.root.Auth = lockedRoot
	go func() { _, loginErr := h.m.Login(context.Background(), "root", "root", password); finished <- loginErr }()
	<-entered
	resolved := make(chan bool, 1)
	go func() { _, _, ok := h.m.Resolve(guest); resolved <- ok }()
	select {
	case ok := <-resolved:
		if !ok {
			t.Error("guest session unavailable")
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("guest lookup waited for another account auth lock")
	}
	close(release)
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
}

func TestPasswordWorkSharedAcrossAccounts(t *testing.T) {
	h := newHarness(t, 4)
	h.register(t, "guest")
	id := h.store.state.Accounts[1].ID
	entered, release := make(chan struct{}), make(chan struct{})
	h.factory.vaults[id].openHook = func() { close(entered); <-release }
	guestDone, rootDone := make(chan error, 1), make(chan error, 1)
	go func() { _, err := h.m.Login(context.Background(), "guest", "guest", password); guestDone <- err }()
	<-entered
	go func() { _, err := h.m.Login(context.Background(), "root", "root", password); rootDone <- err }()
	rootFinished := false
	select {
	case err := <-rootDone:
		t.Errorf("password work overlapped accounts: %v", err)
		rootFinished = true
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	if err := <-guestDone; err != nil {
		t.Fatal(err)
	}
	if !rootFinished {
		if err := <-rootDone; err != nil {
			t.Fatal(err)
		}
	}
}

func TestBoundedSessionsExpiryAndPasswordInvalidation(t *testing.T) {
	h := newHarness(t, 4)
	guest := h.register(t, "guest")
	first := h.token
	var latest, previous string
	for range 70 {
		h.now = h.now.Add(time.Second)
		previous = latest
		var err error
		latest, err = h.m.Login(context.Background(), "root", "root", password)
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(h.m.sessions) != 65 {
		t.Fatalf("session index grew beyond 64 per account: %d", len(h.m.sessions))
	}
	if h.root.Auth.Authorized(first) {
		t.Fatal("evicted session remains valid in auth runtime")
	}
	if _, _, ok := h.m.Resolve(guest); !ok {
		t.Fatal("root session eviction touched guest")
	}
	newPassword := "changed private password"
	if err := h.root.Auth.Update(context.Background(), latest, owner.UpdateInput{Password: password, NewPassword: &newPassword}); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := h.m.Resolve(previous); ok {
		t.Fatal("password-invalidated session resolved")
	}
	if _, exists := h.m.sessions[invitationHash(previous)]; exists {
		t.Fatal("invalid session index entry retained")
	}
	h.now = h.now.Add(owner.SessionTTL)
	if _, _, ok := h.m.Resolve(latest); ok {
		t.Fatal("expired session resolved")
	}
	if h.root.Auth.Authorized(latest) {
		t.Fatal("expired indexed session retained in auth runtime")
	}
	if _, _, ok := h.m.Resolve(strings.Repeat("unknown", 100)); ok {
		t.Fatal("unknown token resolved")
	}
}

func TestReservationFailureDoesNotProvision(t *testing.T) {
	h := newHarness(t, 4)
	invite := h.invite(t)
	h.store.fail = true
	if token, err := h.m.Register(context.Background(), "guest", invite.Token, input("guest")); token != "" || !errors.Is(err, ErrUnavailable) {
		t.Fatalf("reservation failure: %q, %v", token, err)
	}
	if len(h.factory.opens) != 0 {
		t.Fatal("provisioned before durable reservation")
	}
}

func TestRestartIsLazyAndInvalidatesEveryOldSession(t *testing.T) {
	h := newHarness(t, 4)
	guest := h.register(t, "guest")
	if err := h.m.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	var err error
	h.m, err = New(h.store, h.factory, h.root, Options{RootUser: "root", Now: func() time.Time { return h.now }})
	if err != nil {
		t.Fatal(err)
	}
	if len(h.factory.opens) != 1 {
		t.Fatal("startup provisioned guest infrastructure")
	}
	for _, old := range []string{h.token, guest} {
		if _, _, ok := h.m.Resolve(old); ok {
			t.Fatal("session survived restart")
		}
	}
	token, err := h.m.Login(context.Background(), "guest", "guest", password)
	if err != nil {
		t.Fatal(err)
	}
	if len(h.factory.opens) != 2 {
		t.Fatal("guest was not lazily reopened")
	}
	if h.root.Auth.Status("").Unlocked {
		t.Fatal("guest login unlocked root")
	}
	if rec, _, ok := h.m.Resolve(token); !ok || rec.ID == "owner" {
		t.Fatal("guest login selected root")
	}
}

func TestThrottleMetadataIsBounded(t *testing.T) {
	h := newHarness(t, 4)
	for i := range 1100 {
		if _, err := h.m.Login(context.Background(), fmt.Sprintf("client-%d", i), "unknown", "wrong"); !errors.Is(err, domain.ErrCredentials) {
			t.Fatal(err)
		}
	}
	if len(h.m.throttle.clients) > 1024 {
		t.Fatal("unbounded failed-client metadata")
	}
}

func TestRegistryRejectsInvalidCapacityIdentityAndVersion(t *testing.T) {
	h := newHarness(t, 4)
	for _, capacity := range []int{1, 9} {
		if _, err := New(h.store, h.factory, h.root, Options{MaxAccounts: capacity}); err == nil {
			t.Fatal("invalid capacity accepted")
		}
	}
	for _, state := range []State{
		{Version: 2},
		{Version: 1, Accounts: []Record{{ID: "owner", User: "root"}, {ID: "../bad", User: "guest"}}},
		{Version: 1, Accounts: []Record{{ID: "owner", User: "root"}, {ID: "owner", User: "other"}}},
	} {
		if _, err := New(&memoryStore{state: state}, h.factory, h.root, Options{}); err == nil {
			t.Fatalf("invalid registry accepted: %+v", state)
		}
	}
}

func TestCloseAllServicesBeforeVaults(t *testing.T) {
	h := newHarness(t, 4)
	token := h.register(t, "guest")
	_, guest, _ := h.m.Resolve(token)
	var order []string
	h.root.Close = func(context.Context) error { order = append(order, "root"); return nil }
	guest.Close = func(context.Context) error {
		if !h.root.Auth.Authorized(h.token) {
			t.Error("cleared root before services drained")
		}
		order = append(order, "guest")
		return errors.New("close failed")
	}
	if err := h.m.Close(context.Background()); err == nil {
		t.Fatal("lost close error")
	}
	if !reflect.DeepEqual(order, []string{"root", "guest"}) {
		t.Fatalf("close order: %v", order)
	}
	if h.root.Auth.Authorized(h.token) || guest.Auth.Authorized(token) {
		t.Fatal("session survived close")
	}
}
