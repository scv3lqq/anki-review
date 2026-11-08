// Package accounts routes authenticated sessions to isolated owner use cases.
package accounts

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/scv3lqq/anki-review/internal/domain"
	"github.com/scv3lqq/anki-review/internal/usecase/owner"
)

const (
	registryVersion    = 1
	ownerID            = "owner"
	maxLiveInvitations = 16
	invitationTTL      = 24 * time.Hour
	maxAccountSessions = 64
)

// Lifecycle errors are intentionally independent of HTTP status codes.
var (
	ErrInvitation  = errors.New("invitation is invalid or expired")
	ErrForbidden   = errors.New("owner authentication required")
	ErrCapacity    = errors.New("account or invitation limit reached")
	ErrUnavailable = errors.New("account service unavailable")
	ErrBusy        = errors.New("account operation in progress")
	ErrHasVault    = errors.New("account already has a vault")
)

// Record contains public account metadata only. A pending account reserves its slot.
type Record struct {
	ID             string `json:"id"`
	User           string `json:"user"`
	InvitationHash string `json:"invitation_hash,omitempty"`
	Pending        bool   `json:"pending"`
}

// Invitation stores a digest, never its bearer token.
type Invitation struct {
	Hash      string    `json:"hash"`
	ExpiresAt time.Time `json:"expires_at"`
	Used      bool      `json:"used"`
	AccountID string    `json:"account_id,omitempty"`
}

// State is the versioned, public account registry.
type State struct {
	Version     int          `json:"version"`
	Accounts    []Record     `json:"accounts"`
	Invitations []Invitation `json:"invitations"`
}

// Runtime keeps an account's authentication and application lifecycle together.
type Runtime struct {
	Auth    *owner.UseCase
	AnkiKey string
	Close   func(context.Context) error
}

// Factory provisions and opens only account-owned resources.
// Remove must refuse an existing vault with ErrHasVault before deleting resources.
type Factory interface {
	Open(context.Context, Record) (*Runtime, error)
	Remove(context.Context, string) error
}

// Store durably replaces the public account registry.
type Store interface {
	Load() (State, error)
	Save(State) error
}

// Options configure bounded account creation and an optional legacy owner login.
type Options struct {
	MaxAccounts int
	Now         func() time.Time
	RootUser    string
}

// InviteResult is the only return value that contains a plaintext invitation.
type InviteResult struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

type runtimeSlot struct {
	open    sync.Mutex
	runtime *Runtime // guarded by Manager.mu
}

type sessionAccount struct {
	id      string
	expires time.Time
}

type sessionEviction struct {
	hash string
	auth *owner.UseCase
}

// Manager shares bounded authentication work while keeping each account isolated.
type Manager struct {
	store        Store
	factory      Factory
	opts         Options
	registration sync.Mutex
	password     chan struct{}
	throttle     *loginThrottle
	mu           sync.Mutex
	state        State
	slots        map[string]*runtimeSlot
	sessions     map[string]sessionAccount
	closed       bool
}

// New reads public metadata without opening a guest runtime or unlocking a vault.
func New(store Store, factory Factory, root *Runtime, opts Options) (*Manager, error) {
	if store == nil || factory == nil || root == nil || root.Auth == nil {
		return nil, fmt.Errorf("accounts: missing dependency: %w", ErrUnavailable)
	}
	if opts.MaxAccounts == 0 {
		opts.MaxAccounts = 4
	}
	if opts.MaxAccounts < 2 || opts.MaxAccounts > 8 {
		return nil, fmt.Errorf("accounts: MaxAccounts must be between 2 and 8")
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	state, err := store.Load()
	if err != nil {
		return nil, fmt.Errorf("accounts: load registry: %w: %w", ErrUnavailable, err)
	}
	if state.Version == 0 && len(state.Accounts) == 0 && len(state.Invitations) == 0 {
		state.Version = registryVersion
	}
	if len(state.Accounts) == 0 {
		state.Accounts = []Record{{ID: ownerID, User: strings.TrimSpace(opts.RootUser)}}
	}
	if err := validateState(state, opts.MaxAccounts); err != nil {
		return nil, err
	}
	rootIndex := recordIndex(state, ownerID)
	if rootIndex < 0 {
		return nil, fmt.Errorf("accounts: registry has no owner")
	}
	if opts.RootUser != "" {
		rootUser, err := cleanUser(opts.RootUser)
		if err != nil {
			return nil, err
		}
		if state.Accounts[rootIndex].User != "" && state.Accounts[rootIndex].User != rootUser {
			return nil, fmt.Errorf("accounts: owner login differs from vault")
		}
		state.Accounts[rootIndex].User = rootUser
		if err := validateState(state, opts.MaxAccounts); err != nil {
			return nil, err
		}
	}
	m := &Manager{store: store, factory: factory, opts: opts, state: copyState(state), slots: make(map[string]*runtimeSlot), sessions: make(map[string]sessionAccount), password: make(chan struct{}, 1), throttle: newLoginThrottle(opts.Now)}
	for _, rec := range state.Accounts {
		m.slots[rec.ID] = &runtimeSlot{}
	}
	m.slots[ownerID].runtime = root
	return m, nil
}

// Resolve accepts only a live session, never a caller-supplied account identifier.
func (m *Manager) Resolve(token string) (Record, *Runtime, bool) {
	if token == "" || len(token) > 256 {
		return Record{}, nil, false
	}
	hash := invitationHash(token)
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return Record{}, nil, false
	}
	session, exists := m.sessions[hash]
	if exists && !m.opts.Now().Before(session.expires) {
		delete(m.sessions, hash)
		slot := m.slots[session.id]
		var auth *owner.UseCase
		if slot != nil && slot.runtime != nil {
			auth = slot.runtime.Auth
		}
		m.mu.Unlock()
		if auth != nil {
			_ = auth.RevokeHash(hash)
		}
		return Record{}, nil, false
	}
	m.mu.Unlock()
	id := session.id
	if !exists {
		resolver, ok := m.factory.(interface{ ResolveSession(string) (string, bool) })
		if !ok {
			return Record{}, nil, false
		}
		var matched bool
		id, matched = resolver.ResolveSession(token)
		if !matched {
			return Record{}, nil, false
		}
	}
	m.mu.Lock()
	i := recordIndex(m.state, id)
	if m.closed || i < 0 || m.state.Accounts[i].Pending {
		m.mu.Unlock()
		return Record{}, nil, false
	}
	rec := m.state.Accounts[i]
	m.mu.Unlock()
	rt, err := m.open(context.Background(), rec)
	if err == nil && rt.Auth.Authorized(token) {
		expires, live := rt.Auth.SessionExpiry(token)
		if live && m.opts.Now().Before(expires) {
			m.mu.Lock()
			if !m.closed {
				if !exists {
					m.sessions[hash] = sessionAccount{id: id, expires: expires}
				}
				m.mu.Unlock()
				return rec, rt, true
			}
			m.mu.Unlock()
		}
	}
	m.mu.Lock()
	delete(m.sessions, hash)
	m.mu.Unlock()
	return Record{}, nil, false
}

// Setup retains the legacy owner vault and reserves its unique login durably.
func (m *Manager) Setup(ctx context.Context, client string, in owner.SetupInput) (string, error) {
	m.registration.Lock()
	defer m.registration.Unlock()
	user, err := cleanUser(in.User)
	if err != nil {
		return "", err
	}
	m.mu.Lock()
	root := m.slots[ownerID].runtime
	m.mu.Unlock()
	if err := refreshRuntime(root); err != nil {
		return "", err
	}
	if root.Auth.Status("").State != owner.StateSetup {
		return "", owner.ErrSetupDone
	}
	if err := m.reserveOwner(user); err != nil {
		return "", err
	}
	in.User = user
	token, err := m.authenticate(ctx, client, func() (string, error) { return root.Auth.Setup(ctx, client, in) })
	if err != nil {
		return "", err
	}
	return m.complete(ownerID, root, token)
}

func (m *Manager) reserveOwner(user string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrUnavailable
	}
	next := copyState(m.state)
	for _, rec := range next.Accounts {
		if rec.ID != ownerID && rec.User == user {
			return duplicateUser()
		}
	}
	i := recordIndex(next, ownerID)
	next.Accounts[i].User, next.Accounts[i].Pending = user, true
	return m.saveLocked(next)
}

// Login authenticates only the account indexed by its trimmed, unique login.
func (m *Manager) Login(ctx context.Context, client, user, password string) (string, error) {
	if err := m.throttle.wait(client); err != nil {
		return "", err
	}
	user = strings.TrimSpace(user)
	m.mu.Lock()
	var found Record
	for _, rec := range m.state.Accounts {
		if rec.User != "" && rec.User == user {
			found = rec
			break
		}
	}
	closed := m.closed
	m.mu.Unlock()
	if closed {
		return "", ErrUnavailable
	}
	if found.ID == "" {
		m.throttle.fail(client)
		return "", domain.ErrCredentials
	}
	rt, err := m.open(ctx, found)
	if err != nil {
		return "", err
	}
	token, err := m.authenticate(ctx, client, func() (string, error) { return rt.Auth.Login(ctx, client, user, password) })
	if err != nil {
		return "", err
	}
	return m.complete(found.ID, rt, token)
}

// Invite creates a bounded, expiring invitation after owner authentication.
func (m *Manager) Invite(token string) (InviteResult, error) {
	rec, _, ok := m.Resolve(token)
	if !ok || rec.ID != ownerID {
		return InviteResult{}, ErrForbidden
	}
	m.registration.Lock()
	defer m.registration.Unlock()
	if err := m.cleanupExpired(); err != nil {
		return InviteResult{}, err
	}
	secret := randomHex()
	result := InviteResult{Token: secret, ExpiresAt: m.opts.Now().Add(invitationTTL)}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return InviteResult{}, ErrUnavailable
	}
	next := copyState(m.state)
	kept := make([]Invitation, 0, len(next.Invitations))
	live := 0
	for _, invite := range next.Invitations {
		if !invite.Used && m.opts.Now().Before(invite.ExpiresAt) {
			live++
			kept = append(kept, invite)
		} else if !invite.Used && invite.AccountID != "" {
			kept = append(kept, invite)
		}
	}
	if live >= maxLiveInvitations {
		return InviteResult{}, ErrCapacity
	}
	kept = append(kept, Invitation{Hash: invitationHash(secret), ExpiresAt: result.ExpiresAt})
	next.Invitations = kept
	if err := m.saveLocked(next); err != nil {
		return InviteResult{}, err
	}
	return result, nil
}

// Register reserves a stable guest before provisioning and publishes only durable success.
func (m *Manager) Register(ctx context.Context, client, invitation string, in owner.SetupInput) (string, error) {
	m.registration.Lock()
	defer m.registration.Unlock()
	if err := m.throttle.wait(client); err != nil {
		return "", err
	}
	user, err := cleanUser(in.User)
	if err != nil {
		return "", err
	}
	if err := selectedCredential(in); err != nil {
		return "", err
	}
	if err := m.cleanupExpired(); err != nil {
		return "", err
	}
	rec, err := m.reserveGuest(invitation, user)
	if err != nil {
		return "", err
	}
	rt, err := m.open(ctx, rec)
	if err != nil {
		return "", errors.Join(err, m.cleanup(rec))
	}
	if err := refreshRuntime(rt); err != nil {
		return "", err
	}
	in.User, in.AnkiKey = rec.User, rt.AnkiKey
	token, err := m.authenticate(ctx, client, func() (string, error) {
		// A crash can leave a complete vault behind a pending reservation. Only the
		// original password may recover it; Setup would overwrite that vault.
		if rt.Auth.Status("").State != owner.StateSetup {
			return rt.Auth.Login(ctx, client, user, in.Password)
		}
		return rt.Auth.Setup(ctx, client, in)
	})
	if err != nil {
		if rt.Auth.Status("").State == owner.StateSetup {
			err = errors.Join(err, m.cleanup(rec))
		}
		return "", err
	}
	return m.complete(rec.ID, rt, token)
}

func (m *Manager) reserveGuest(token, user string) (Record, error) {
	if len(token) != 64 {
		return Record{}, ErrInvitation
	}
	hash := invitationHash(token)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return Record{}, ErrUnavailable
	}
	next := copyState(m.state)
	i := -1
	for n, invite := range next.Invitations {
		if invite.Hash == hash {
			i = n
			break
		}
	}
	if i < 0 || next.Invitations[i].Used || !m.opts.Now().Before(next.Invitations[i].ExpiresAt) {
		return Record{}, ErrInvitation
	}
	invite := &next.Invitations[i]
	for _, rec := range next.Accounts {
		if rec.User == user {
			if rec.Pending && rec.InvitationHash == hash && rec.ID == invite.AccountID {
				return rec, nil
			}
			return Record{}, duplicateUser()
		}
	}
	if invite.AccountID != "" {
		return Record{}, ErrInvitation
	}
	if len(next.Accounts) >= m.opts.MaxAccounts {
		return Record{}, ErrCapacity
	}
	rec := Record{ID: randomHex()[:32], User: user, InvitationHash: hash, Pending: true}
	invite.AccountID = rec.ID
	next.Accounts = append(next.Accounts, rec)
	if err := m.saveLocked(next); err != nil {
		return Record{}, err
	}
	m.slots[rec.ID] = &runtimeSlot{}
	return rec, nil
}

func (m *Manager) open(ctx context.Context, rec Record) (*Runtime, error) {
	m.mu.Lock()
	slot := m.slots[rec.ID]
	closed := m.closed
	m.mu.Unlock()
	if closed || slot == nil {
		return nil, ErrUnavailable
	}
	slot.open.Lock()
	defer slot.open.Unlock()
	m.mu.Lock()
	// Cleanup may have removed this reservation while Open waited on its slot.
	// Revalidate ownership before returning or creating any account resources.
	if m.closed || m.slots[rec.ID] != slot || recordIndex(m.state, rec.ID) < 0 {
		m.mu.Unlock()
		return nil, ErrUnavailable
	}
	rt := slot.runtime
	m.mu.Unlock()
	if rt != nil {
		return rt, nil
	}
	rt, err := m.factory.Open(ctx, rec)
	if err != nil {
		return nil, fmt.Errorf("accounts: open runtime: %w: %w", ErrUnavailable, err)
	}
	if rt == nil || rt.Auth == nil {
		return nil, ErrUnavailable
	}
	m.mu.Lock()
	slot.runtime = rt
	m.mu.Unlock()
	return rt, nil
}

func (m *Manager) complete(id string, rt *Runtime, token string) (string, error) {
	expires, live := rt.Auth.SessionExpiry(token)
	if !live {
		return "", ErrUnavailable
	}
	evicted, err := m.commitSession(id, token, expires)
	for _, session := range evicted {
		if revokeErr := session.auth.RevokeHash(session.hash); revokeErr != nil {
			err = errors.Join(err, ErrUnavailable)
		}
	}
	if err != nil {
		_ = rt.Auth.Logout(token)
		return "", err
	}
	return token, nil
}

func (m *Manager) commitSession(id, token string, expires time.Time) ([]sessionEviction, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, ErrUnavailable
	}
	hash := invitationHash(token)
	next := copyState(m.state)
	i := recordIndex(next, id)
	if i < 0 {
		return nil, ErrUnavailable
	}
	if next.Accounts[i].Pending {
		next.Accounts[i].Pending = false
		for n := range next.Invitations {
			if next.Invitations[n].AccountID == id {
				next.Invitations[n].Used = true
			}
		}
		if err := m.saveLocked(next); err != nil {
			return nil, err
		}
	}
	if previous, ok := m.sessions[hash]; ok && previous.id != id {
		return nil, ErrUnavailable
	}
	now := m.opts.Now()
	var evicted []sessionEviction
	count, oldest := 0, ""
	var oldestExpiry time.Time
	for saved, session := range m.sessions {
		if !now.Before(session.expires) {
			if slot := m.slots[session.id]; slot != nil && slot.runtime != nil {
				evicted = append(evicted, sessionEviction{saved, slot.runtime.Auth})
			}
			delete(m.sessions, saved)
			continue
		}
		if session.id == id && saved != hash {
			count++
			if oldest == "" || session.expires.Before(oldestExpiry) || (session.expires.Equal(oldestExpiry) && saved < oldest) {
				oldest, oldestExpiry = saved, session.expires
			}
		}
	}
	if count >= maxAccountSessions {
		evicted = append(evicted, sessionEviction{oldest, m.slots[id].runtime.Auth})
		delete(m.sessions, oldest)
	}
	m.sessions[hash] = sessionAccount{id: id, expires: expires}
	return evicted, nil
}

func (m *Manager) cleanup(rec Record) error {
	// Failed request cancellation must not prevent bounded resource cleanup.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return m.removePending(ctx, rec)
}

// cleanupExpired runs under registration, never under the metadata mutex while
// contacting infrastructure. Sealed accounts survive even if setup never committed.
func (m *Manager) cleanupExpired() error {
	m.mu.Lock()
	var expired []Record
	for _, rec := range m.state.Accounts {
		if rec.ID == ownerID || !rec.Pending {
			continue
		}
		for _, invitation := range m.state.Invitations {
			if invitation.AccountID == rec.ID && !m.opts.Now().Before(invitation.ExpiresAt) {
				expired = append(expired, rec)
				break
			}
		}
	}
	m.mu.Unlock()
	for _, rec := range expired {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := m.removePending(ctx, rec)
		cancel()
		if errors.Is(err, ErrHasVault) {
			if err := m.publishRecovered(rec.ID); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) publishRecovered(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	next := copyState(m.state)
	i := recordIndex(next, id)
	if i < 0 {
		return ErrUnavailable
	}
	next.Accounts[i].Pending = false
	for n := range next.Invitations {
		if next.Invitations[n].AccountID == id {
			next.Invitations[n].Used = true
		}
	}
	return m.saveLocked(next)
}

func (m *Manager) removePending(ctx context.Context, rec Record) error {
	m.mu.Lock()
	slot := m.slots[rec.ID]
	m.mu.Unlock()
	if slot == nil {
		return ErrUnavailable
	}
	slot.open.Lock()
	defer slot.open.Unlock()
	m.mu.Lock()
	rt := slot.runtime
	m.mu.Unlock()
	if rt != nil {
		if err := refreshRuntime(rt); err != nil {
			return err
		}
		if rt.Auth.Status("").State != owner.StateSetup {
			return ErrHasVault
		}
		if rt.Close != nil {
			if err := rt.Close(ctx); err != nil {
				return fmt.Errorf("accounts: drain pending runtime: %w: %w", ErrUnavailable, err)
			}
		}
		rt.Auth.Close()
	}
	if err := m.factory.Remove(ctx, rec.ID); err != nil {
		if errors.Is(err, ErrHasVault) {
			if rt != nil {
				if err := refreshRuntime(rt); err != nil {
					return err
				}
			}
			return ErrHasVault
		}
		return fmt.Errorf("accounts: remove pending runtime: %w: %w", ErrUnavailable, err)
	}
	m.mu.Lock()
	// Resources are gone even if the registry save fails. Keep the reservation,
	// but force its next attempt to reopen rather than reuse a deleted vault.
	slot.runtime = nil
	m.mu.Unlock()
	return m.forgetRemoved(rec)
}

func refreshRuntime(rt *Runtime) error {
	if err := rt.Auth.RefreshVault(); err != nil {
		return fmt.Errorf("accounts: refresh vault metadata: %w: %w", ErrUnavailable, err)
	}
	return nil
}

func (m *Manager) forgetRemoved(rec Record) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	next := copyState(m.state)
	i := recordIndex(next, rec.ID)
	if i < 0 || !next.Accounts[i].Pending {
		return ErrUnavailable
	}
	next.Accounts = append(next.Accounts[:i], next.Accounts[i+1:]...)
	for n := range next.Invitations {
		if next.Invitations[n].AccountID == rec.ID {
			next.Invitations[n].AccountID = ""
		}
	}
	if err := m.saveLocked(next); err != nil {
		return err
	}
	delete(m.slots, rec.ID)
	return nil
}

// Close drains every initialized review service before clearing any vault handle.
// The application must drain requests before calling it.
func (m *Manager) Close(ctx context.Context) error {
	m.mu.Lock()
	m.closed = true
	clear(m.sessions)
	var runtimes []*Runtime
	for _, rec := range m.state.Accounts {
		if rt := m.slots[rec.ID].runtime; rt != nil {
			runtimes = append(runtimes, rt)
		}
	}
	m.mu.Unlock()
	var result error
	for _, rt := range runtimes {
		if rt.Close != nil {
			if err := rt.Close(ctx); err != nil {
				result = errors.Join(result, fmt.Errorf("accounts: close runtime: %w", err))
			}
		}
	}
	for _, rt := range runtimes {
		rt.Auth.Close()
	}
	return result
}

func (m *Manager) saveLocked(state State) error {
	if err := m.store.Save(state); err != nil {
		return fmt.Errorf("accounts: persist registry: %w: %w", ErrUnavailable, err)
	}
	m.state = state
	return nil
}

func (m *Manager) authenticate(ctx context.Context, client string, action func() (string, error)) (string, error) {
	if err := m.throttle.wait(client); err != nil {
		return "", err
	}
	select {
	case m.password <- struct{}{}:
	case <-ctx.Done():
		return "", fmt.Errorf("accounts: wait for password work: %w", ctx.Err())
	}
	defer func() { <-m.password }()
	if err := m.throttle.wait(client); err != nil {
		return "", err
	}
	token, err := action()
	if err != nil {
		m.throttle.fail(client)
	} else {
		m.throttle.reset(client)
	}
	return token, err
}

func selectedCredential(in owner.SetupInput) error {
	if in.GraderProvider == "codex" {
		if strings.TrimSpace(in.CodexAuth) == "" {
			return &owner.FieldError{Field: "codex_auth", Message: "Import auth.json from a dedicated Codex ChatGPT login"}
		}
	} else if strings.TrimSpace(in.ClaudeToken) == "" {
		return &owner.FieldError{Field: "claude_token", Message: "Enter your own Claude token"}
	}
	return nil
}

func cleanUser(user string) (string, error) {
	user = strings.TrimSpace(user)
	if !utf8.ValidString(user) || utf8.RuneCountInString(user) < 1 || utf8.RuneCountInString(user) > 64 || strings.ContainsFunc(user, unicode.IsControl) {
		return "", &owner.FieldError{Field: "user", Message: "Use 1 to 64 characters without control characters"}
	}
	return user, nil
}

func duplicateUser() error {
	return &owner.FieldError{Field: "user", Message: "This login is already in use"}
}
func invitationHash(token string) string {
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:])
}
func randomHex() string {
	var secret [32]byte
	_, _ = rand.Read(secret[:])
	return hex.EncodeToString(secret[:])
}
func copyState(state State) State {
	state.Accounts = append([]Record(nil), state.Accounts...)
	state.Invitations = append([]Invitation(nil), state.Invitations...)
	return state
}
func recordIndex(state State, id string) int {
	for i, rec := range state.Accounts {
		if rec.ID == id {
			return i
		}
	}
	return -1
}

func validateState(state State, maxAccounts int) error {
	if state.Version != registryVersion || len(state.Accounts) > maxAccounts || len(state.Invitations) > maxLiveInvitations+maxAccounts {
		return fmt.Errorf("accounts: invalid registry version or capacity")
	}
	ids, users := make(map[string]bool), make(map[string]bool)
	for _, rec := range state.Accounts {
		if (rec.ID != ownerID && !validHex(rec.ID, 32)) || ids[rec.ID] {
			return fmt.Errorf("accounts: invalid or duplicate account ID")
		}
		ids[rec.ID] = true
		if rec.ID != ownerID || rec.User != "" {
			user, err := cleanUser(rec.User)
			if err != nil || user != rec.User || users[user] {
				return fmt.Errorf("accounts: invalid or duplicate login")
			}
			users[user] = true
		}
		if rec.ID != ownerID && !validHex(rec.InvitationHash, 64) {
			return fmt.Errorf("accounts: invalid account invitation hash")
		}
	}
	hashes := make(map[string]bool)
	for _, invite := range state.Invitations {
		if !validHex(invite.Hash, 64) || hashes[invite.Hash] || invite.ExpiresAt.IsZero() || (invite.AccountID != "" && !ids[invite.AccountID]) {
			return fmt.Errorf("accounts: invalid invitation metadata")
		}
		hashes[invite.Hash] = true
		if invite.AccountID != "" {
			rec := state.Accounts[recordIndex(state, invite.AccountID)]
			if rec.InvitationHash != invite.Hash || (invite.Used && rec.Pending) {
				return fmt.Errorf("accounts: inconsistent invitation reservation")
			}
		}
	}
	return nil
}

func validHex(value string, size int) bool {
	if len(value) != size {
		return false
	}
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}
