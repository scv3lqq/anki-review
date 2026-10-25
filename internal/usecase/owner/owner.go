// Package owner runs the one owner of the server: the setup, the login and the sessions that follow,
// the settings, and the tokens kept in memory while the server is unlocked.
package owner

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/rs/zerolog"

	"github.com/scv3lqq/anki-review/internal/domain"
)

// Page states, see Status.
const (
	StateSetup = "setup"
	StateLogin = "login"
	StateReady = "ready"
)

// SessionTTL is how long a login lasts; the cookie carries the same lifetime.
const SessionTTL = 30 * 24 * time.Hour

const (
	// the first wrong passwords are free; after that the wait doubles from one second up to maxWait
	freeFailures = 5
	maxWait      = 15 * time.Minute
	// past this shift the wait is at the cap anyway
	capShift = 10

	maxUserRunes     = 64
	minPassword      = 12
	maxPassword      = 1024
	maxClaudeBytes   = 4096
	maxAnkiKeyBytes  = 1024
	claudeTokenStart = "sk-ant-"
)

var (
	// ErrSetupDone means an owner exists already; log in instead.
	ErrSetupDone = errors.New("owner is already set up")
	// ErrNoOwner means nobody has set up the server yet.
	ErrNoOwner = errors.New("owner is not set up")
	// ErrChanged means another settings call sealed the vault while this one was checking its tokens.
	ErrChanged = errors.New("settings changed meanwhile, try again")
	// ErrUnlock means the password was right but the server could not start with the saved tokens.
	ErrUnlock = errors.New("could not load the saved pass")
)

// FieldError is a value the form got wrong: the page shows Message under Field.
type FieldError struct {
	Field   string
	Message string
}

func (e *FieldError) Error() string { return e.Message }

// ThrottledError refuses a password check until Wait has passed.
type ThrottledError struct {
	Wait time.Duration
}

func (e *ThrottledError) Error() string {
	return fmt.Sprintf("Too many wrong passwords: try again in %s", e.Wait)
}

// Vault seals the owner's tokens with the password; the vault package implements it.
type Vault interface {
	Exists() (bool, error)
	Seal(user, password string, s domain.Secrets) error
	Open(user, password string) (domain.Secrets, error)
}

// Checker proves a token works before it is sealed.
type Checker interface {
	CheckAnki(ctx context.Context, key string) error
	CheckClaude(ctx context.Context, token string) error
}

// Options tune a UseCase; zero values get defaults.
type Options struct {
	Log      zerolog.Logger
	Now      func() time.Time
	NewToken func() string
	// OnUnlock runs once, on the first unlock. Its error fails that login and leaves the server locked.
	OnUnlock func(domain.Secrets) error
}

// Status is what the page needs to know about one request.
type Status struct {
	State    string
	Unlocked bool
	// User is the login of the session; set only when State is StateReady.
	User string
}

// SetupInput is what the setup form sends.
type SetupInput struct {
	User        string
	Password    string
	ClaudeToken string
	AnkiKey     string
}

// UpdateInput is what the settings form sends. A nil field keeps the current value; a non-nil one
// replaces it, an empty string included.
type UpdateInput struct {
	Password    string
	NewPassword *string
	ClaudeToken *string
	AnkiKey     *string
}

type session struct {
	user    string
	expires time.Time
}

// UseCase is the owner. Sessions live in memory only, so a restart logs everyone out and locks the
// server until a login.
type UseCase struct {
	vault   Vault
	checker Checker
	opts    Options

	// setupMu runs one setup at a time
	setupMu sync.Mutex
	// pwMu serializes the Argon2id runs (64 MiB each) and the re-seal. It is never held across a
	// network check, so a slow check doesn't block a login.
	pwMu sync.Mutex
	// clients counts wrong passwords and failed setup checks per client address
	clients *throttle
	// settings counts wrong current passwords on the settings page; a login can't block it
	settings *throttle

	// mu guards the fields below. Lock order: setupMu or pwMu before mu.
	mu       sync.Mutex
	hasVault bool
	unlocked bool
	secrets  domain.Secrets
	sessions map[string]session
	// version counts the seals of the vault: a settings call re-seals only if it is unchanged
	version uint64
}

// New reads whether a vault exists; nothing is unlocked yet.
func New(vault Vault, checker Checker, opts Options) (*UseCase, error) {
	exists, err := vault.Exists()
	if err != nil {
		return nil, fmt.Errorf("owner: %w", err)
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.NewToken == nil {
		opts.NewToken = rand.Text
	}
	if opts.OnUnlock == nil {
		opts.OnUnlock = func(domain.Secrets) error { return nil }
	}
	return &UseCase{
		vault:    vault,
		checker:  checker,
		opts:     opts,
		clients:  newThrottle(opts.Now),
		settings: newThrottle(opts.Now),
		hasVault: exists,
		sessions: map[string]session{},
	}, nil
}

// Status tells the page where the owner stands for the session token.
func (u *UseCase) Status(token string) Status {
	u.mu.Lock()
	defer u.mu.Unlock()
	if !u.hasVault {
		return Status{State: StateSetup}
	}
	st := Status{State: StateLogin, Unlocked: u.unlocked}
	if s, ok := u.sessionLocked(token); ok {
		st.State, st.User = StateReady, s.user
	}
	return st
}

// Authorized reports whether token is a live session.
func (u *UseCase) Authorized(token string) bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	_, ok := u.sessionLocked(token)
	return ok
}

// Logout ends the session.
func (u *UseCase) Logout(token string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	delete(u.sessions, token)
}

// Secrets returns the current tokens. Adapters call it on every use, so a settings change takes
// effect without a restart.
func (u *UseCase) Secrets() domain.Secrets {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.secrets
}

// Setup checks both tokens, seals them under the password, and logs the owner in. client names the
// caller for the throttle: a failed check counts against it, like a wrong password.
func (u *UseCase) Setup(ctx context.Context, client string, in SetupInput) (string, error) {
	u.setupMu.Lock()
	defer u.setupMu.Unlock()
	u.mu.Lock()
	has := u.hasVault
	u.mu.Unlock()
	if has {
		return "", ErrSetupDone
	}
	if err := u.clientWait(client); err != nil {
		return "", err
	}
	user, err := cleanUser(in.User)
	if err != nil {
		return "", err
	}
	if err := checkPassword("password", in.Password); err != nil {
		return "", err
	}
	claude, err := cleanClaude(in.ClaudeToken)
	if err != nil {
		return "", err
	}
	anki, err := cleanAnkiKey(in.AnkiKey)
	if err != nil {
		return "", err
	}
	if err := u.checkAnki(ctx, anki); err != nil {
		u.countFailure(u.clients, client, "setup check failed")
		return "", err
	}
	if err := u.checkClaude(ctx, claude); err != nil {
		u.countFailure(u.clients, client, "setup check failed")
		return "", err
	}
	secrets := domain.Secrets{ClaudeToken: claude, AnkiKey: anki}
	u.pwMu.Lock()
	err = u.vault.Seal(user, in.Password, secrets)
	u.pwMu.Unlock()
	if err != nil {
		return "", fmt.Errorf("owner: setup: %w", err)
	}

	u.mu.Lock()
	defer u.mu.Unlock()
	u.hasVault = true
	if err := u.unlockLocked(secrets); err != nil {
		return "", err
	}
	token := u.newSessionLocked(user)
	u.opts.Log.Info().Msg("owner set up")
	return token, nil
}

// Login checks the password against the vault and opens a session. The first success unlocks the
// tokens. client names the caller for the throttle.
func (u *UseCase) Login(_ context.Context, client, user, password string) (string, error) {
	u.mu.Lock()
	has := u.hasVault
	u.mu.Unlock()
	if !has {
		return "", ErrNoOwner
	}
	if err := u.clientWait(client); err != nil {
		return "", err
	}

	u.pwMu.Lock()
	defer u.pwMu.Unlock()
	login := strings.TrimSpace(user)
	secrets, err := u.vault.Open(login, password)
	if err != nil {
		if errors.Is(err, domain.ErrCredentials) {
			u.countFailure(u.clients, client, "login failed")
			return "", domain.ErrCredentials
		}
		return "", fmt.Errorf("owner: login: %w", err)
	}
	u.clients.reset(client)

	u.mu.Lock()
	defer u.mu.Unlock()
	if err := u.unlockLocked(secrets); err != nil {
		return "", err
	}
	return u.newSessionLocked(login), nil
}

// Update replaces the fields given and seals the result again. The fields are checked before the
// password, so a malformed form costs no Argon2id run. The password is verified under pwMu, the
// checks of the new tokens run without it, and the re-seal fails with ErrChanged if the vault moved
// on in between. A changed password drops the other sessions.
func (u *UseCase) Update(ctx context.Context, token string, in UpdateInput) error {
	if in.Password == "" {
		return &FieldError{Field: "password", Message: "Enter your current password"}
	}
	newPassword := in.Password
	if in.NewPassword != nil {
		if err := checkPassword("new_password", *in.NewPassword); err != nil {
			return err
		}
		newPassword = *in.NewPassword
	}
	var claude, anki *string
	if in.ClaudeToken != nil {
		t, err := cleanClaude(*in.ClaudeToken)
		if err != nil {
			return err
		}
		claude = &t
	}
	if in.AnkiKey != nil {
		k, err := cleanAnkiKey(*in.AnkiKey)
		if err != nil {
			return err
		}
		anki = &k
	}

	s, next, version, err := u.verify(token, in.Password)
	if err != nil {
		return err
	}
	// only the tokens this call replaces are checked; an empty one is checked too (the local claude login)
	if anki != nil {
		if err := u.checkAnki(ctx, *anki); err != nil {
			return err
		}
		next.AnkiKey = *anki
	}
	if claude != nil {
		if err := u.checkClaude(ctx, *claude); err != nil {
			return err
		}
		next.ClaudeToken = *claude
	}
	return u.reseal(token, s.user, newPassword, next, version, in.NewPassword != nil)
}

// verify checks the current password for the session, under pwMu, and returns the session, the
// tokens in the vault and the vault's version.
func (u *UseCase) verify(token, password string) (session, domain.Secrets, uint64, error) {
	if err := u.throttledErr(u.settings.wait(settingsKey)); err != nil {
		return session{}, domain.Secrets{}, 0, err
	}
	u.pwMu.Lock()
	defer u.pwMu.Unlock()
	u.mu.Lock()
	s, ok := u.sessionLocked(token)
	version := u.version
	u.mu.Unlock()
	if !ok {
		return session{}, domain.Secrets{}, 0, domain.ErrCredentials
	}
	current, err := u.vault.Open(s.user, password)
	if err != nil {
		if errors.Is(err, domain.ErrCredentials) {
			u.countFailure(u.settings, settingsKey, "password refused")
			return session{}, domain.Secrets{}, 0, domain.ErrCredentials
		}
		return session{}, domain.Secrets{}, 0, fmt.Errorf("owner: update: %w", err)
	}
	u.settings.reset(settingsKey)
	return s, current, version, nil
}

// reseal seals next under password and makes it the live secrets, unless the vault changed since
// verify: then another settings call won, and this one must not overwrite it.
func (u *UseCase) reseal(token, user, password string, next domain.Secrets, version uint64, passwordChanged bool) error {
	u.pwMu.Lock()
	defer u.pwMu.Unlock()
	u.mu.Lock()
	changed := u.version != version
	u.mu.Unlock()
	if changed {
		return ErrChanged
	}
	if err := u.vault.Seal(user, password, next); err != nil {
		return fmt.Errorf("owner: update: %w", err)
	}

	u.mu.Lock()
	defer u.mu.Unlock()
	u.secrets = next
	u.version++
	if passwordChanged {
		u.dropSessionsLocked(token)
	}
	u.opts.Log.Info().Msg("settings saved")
	return nil
}

// checkAnki proves the AnkiConnect key. An empty key is checked too: it means no apiKey.
func (u *UseCase) checkAnki(ctx context.Context, key string) error {
	if err := u.checker.CheckAnki(ctx, key); err != nil {
		return ankiFailure(err)
	}
	return nil
}

// checkClaude proves the Claude token. An empty token is checked too: it means the server's own login.
func (u *UseCase) checkClaude(ctx context.Context, token string) error {
	if err := u.checker.CheckClaude(ctx, token); err != nil {
		if token == "" {
			return &FieldError{Field: "claude_token", Message: fmt.Sprintf("Claude did not answer with the server's own login, paste a token: %v", err)}
		}
		return &FieldError{Field: "claude_token", Message: fmt.Sprintf("Claude did not answer with this token: %v", err)}
	}
	return nil
}

func ankiFailure(err error) error {
	switch {
	case errors.Is(err, domain.ErrAnkiKey):
		return &FieldError{Field: "anki_key", Message: "AnkiConnect refused this key: copy apiKey from the add-on config, or leave the field empty if apiKey is null"}
	case errors.Is(err, domain.ErrAnki):
		return &FieldError{Field: "anki_key", Message: "Anki did not answer at ANKI_CONNECT_URL: start Anki with the AnkiConnect add-on"}
	default:
		return &FieldError{Field: "anki_key", Message: fmt.Sprintf("AnkiConnect check failed: %v", err)}
	}
}

// unlockLocked makes the tokens available; OnUnlock runs on the first unlock only. Resumed grading
// reads Secrets() under u.mu from a goroutine that OnUnlock starts, so the tokens are set before the
// lock is released, and OnUnlock runs with them in place. A failed unlock clears them again.
func (u *UseCase) unlockLocked(s domain.Secrets) error {
	if u.unlocked {
		u.secrets = s
		return nil
	}
	u.secrets = s
	if err := u.opts.OnUnlock(s); err != nil {
		u.secrets = domain.Secrets{}
		u.opts.Log.Error().Err(err).Msg("unlock failed")
		return fmt.Errorf("owner: unlock: %w: %w", ErrUnlock, err)
	}
	u.unlocked = true
	u.opts.Log.Info().Msg("unlocked")
	return nil
}

func (u *UseCase) newSessionLocked(user string) string {
	token := u.opts.NewToken()
	now := u.opts.Now()
	for t, s := range u.sessions {
		if !now.Before(s.expires) {
			delete(u.sessions, t)
		}
	}
	u.sessions[token] = session{user: user, expires: now.Add(SessionTTL)}
	return token
}

// sessionLocked returns the live session for token; an expired one is dropped.
func (u *UseCase) sessionLocked(token string) (session, bool) {
	if token == "" {
		return session{}, false
	}
	s, ok := u.sessions[token]
	if !ok {
		return session{}, false
	}
	if !u.opts.Now().Before(s.expires) {
		delete(u.sessions, token)
		return session{}, false
	}
	return s, true
}

// dropSessionsLocked keeps only the session token, which made the change.
func (u *UseCase) dropSessionsLocked(keep string) {
	for t := range u.sessions {
		if t != keep {
			delete(u.sessions, t)
		}
	}
}

// clientWait refuses a client that is still in its wait after wrong passwords or failed checks.
func (u *UseCase) clientWait(client string) error {
	return u.throttledErr(u.clients.wait(client))
}

func (u *UseCase) throttledErr(wait time.Duration) error {
	if wait == 0 {
		return nil
	}
	// round up: "try again in 0s" would be wrong
	secs := (wait + time.Second - 1).Truncate(time.Second)
	u.opts.Log.Warn().Msg("password check throttled")
	return &ThrottledError{Wait: secs}
}

// countFailure counts a wrong password or a failed check against key.
func (u *UseCase) countFailure(t *throttle, key, msg string) {
	n := t.fail(key)
	u.opts.Log.Warn().Int("failures", n).Msg(msg)
}

// backoff is the wait after failures wrong passwords in a row.
func backoff(failures int) time.Duration {
	if failures < freeFailures {
		return 0
	}
	shift := failures - freeFailures
	if shift >= capShift {
		return maxWait
	}
	return min(time.Second<<shift, maxWait)
}

func cleanUser(raw string) (string, error) {
	user := strings.TrimSpace(raw)
	if n := utf8.RuneCountInString(user); n < 1 || n > maxUserRunes || strings.IndexFunc(user, unicode.IsControl) >= 0 {
		return "", &FieldError{Field: "user", Message: "Login must be 1 to 64 characters with no control characters"}
	}
	return user, nil
}

// checkPassword takes the password as typed: no trimming.
func checkPassword(field, password string) error {
	if n := utf8.RuneCountInString(password); n < minPassword || n > maxPassword {
		return &FieldError{Field: field, Message: "Password must be 12 to 1024 characters"}
	}
	return nil
}

// cleanClaude: only an exactly empty token means none, and it makes claude use the login of the
// account the server runs as. Whitespace alone is a typo, not none.
func cleanClaude(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	token := strings.TrimSpace(raw)
	switch {
	case token == "":
		return "", &FieldError{Field: "claude_token", Message: "Claude token is only spaces: leave the field empty to use the server's own login"}
	case len(token) > maxClaudeBytes:
		return "", &FieldError{Field: "claude_token", Message: "Claude token is too long"}
	case strings.IndexFunc(token, unicode.IsSpace) >= 0:
		return "", &FieldError{Field: "claude_token", Message: "Claude token must not contain spaces"}
	case !strings.HasPrefix(token, claudeTokenStart):
		return "", &FieldError{Field: "claude_token", Message: "Claude token must start with sk-ant-"}
	}
	return token, nil
}

// cleanAnkiKey: only an exactly empty key means AnkiConnect without apiKey. Whitespace alone is a typo.
func cleanAnkiKey(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	key := strings.TrimSpace(raw)
	switch {
	case key == "":
		return "", &FieldError{Field: "anki_key", Message: "AnkiConnect key is only spaces: leave the field empty if apiKey is null"}
	case len(key) > maxAnkiKeyBytes:
		return "", &FieldError{Field: "anki_key", Message: "AnkiConnect key is too long"}
	case strings.IndexFunc(key, unicode.IsSpace) >= 0:
		return "", &FieldError{Field: "anki_key", Message: "AnkiConnect key must not contain spaces"}
	}
	return key, nil
}
