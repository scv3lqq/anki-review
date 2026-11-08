package vault

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/crypto/chacha20poly1305"

	"github.com/scv3lqq/anki-review/internal/domain"
)

const maxSessions = 64

type recoveryEnvelope struct {
	Nonce  string `json:"nonce"`
	Sealed string `json:"sealed"`
}

type recoveryPayload struct {
	Key      []byte               `json:"key"`
	Sessions []domain.AuthSession `json:"sessions"`
}

// WithSessionKey enables recovery bound to the account's stable registry identifier.
func WithSessionKey(path, accountID string) Option {
	return func(v *Vault) { v.sessionKeyPath, v.accountID = path, accountID }
}

// PersistenceEnabled reports whether construction loaded a configured server key.
func (v *Vault) PersistenceEnabled() bool { return len(v.sessionKey) == keySize }

func (v *Vault) configureRecovery(dir string) error {
	if v.sessionKeyPath == "" {
		raw, err := readRegular(v.path, maxMetadataBytes)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("vault: inspect recovery: %w", err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			return damaged("", "invalid vault metadata")
		}
		if recovery, ok := fields["recovery"]; ok && string(recovery) != "null" {
			return errors.New("vault: existing recovery requires the configured session key")
		}
		return nil
	}
	if v.accountID == "" {
		return errors.New("vault: recovery requires an account identifier")
	}
	key, err := loadSessionKey(v.sessionKeyPath, dir)
	if err != nil {
		return fmt.Errorf("vault: session key: %w", err)
	}
	v.sessionKey = key
	return nil
}

func loadSessionKey(path, dataDir string) ([]byte, error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("key path must be absolute")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect key: %w", err)
	}
	if !info.Mode().IsRegular() || (info.Mode().Perm() != 0o600 && info.Mode().Perm() != 0o640) || info.Mode()&(os.ModeSetuid|os.ModeSetgid) != 0 {
		return nil, errors.New("key must be a regular file with mode 0600 or 0640")
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return nil, fmt.Errorf("resolve key directory: %w", err)
	}
	data, err := filepath.EvalSymlinks(dataDir)
	if err != nil {
		return nil, fmt.Errorf("resolve data directory: %w", err)
	}
	data, err = filepath.Abs(data)
	if err != nil {
		return nil, fmt.Errorf("resolve data path: %w", err)
	}
	canonical := filepath.Join(parent, filepath.Base(path))
	if withinDirectory(data, canonical) {
		return nil, errors.New("key must be outside application data")
	}
	for current := parent; ; current = filepath.Dir(current) {
		entry, err := os.Stat(current)
		if err != nil {
			return nil, fmt.Errorf("inspect key directory: %w", err)
		}
		stat, ok := entry.Sys().(*syscall.Stat_t)
		if !ok || (stat.Uid != 0 && int64(stat.Uid) != int64(os.Geteuid())) {
			return nil, errors.New("key directory has an untrusted owner")
		}
		// Root-owned sticky temporary ancestors cannot replace entries in the protected parent.
		stickyAncestor := current != parent && stat.Uid == 0 && entry.Mode()&os.ModeSticky != 0
		if entry.Mode().Perm()&0o022 != 0 && !stickyAncestor {
			return nil, errors.New("key directory is writable by another user")
		}
		if _, err := os.Lstat(filepath.Join(current, ".git")); err == nil {
			return nil, errors.New("key must be outside a repository")
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("inspect repository boundary: %w", err)
		}
		if current == filepath.Dir(current) {
			break
		}
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || (stat.Uid != 0 && int64(stat.Uid) != int64(os.Geteuid())) {
		return nil, errors.New("key has an untrusted owner")
	}
	key, err := readRegular(path, keySize)
	if err != nil {
		return nil, fmt.Errorf("read key: %w", err)
	}
	if len(key) != keySize {
		clear(key)
		return nil, errors.New("key must contain exactly 32 bytes")
	}
	return key, nil
}

func withinDirectory(dir, path string) bool {
	relative, err := filepath.Rel(dir, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

// readRegular bounds memory and rejects symlinks or replacement between inspection and opening.
func readRegular(path string, limit int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect file: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, errors.New("invalid file type or size")
	}
	f, err := os.Open(path) // #nosec G304 -- Internal/configured paths are checked as bounded regular files before and after opening.
	if err != nil {
		return nil, fmt.Errorf("open file: %w", err)
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("inspect opened file: %w", err)
	}
	if !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		return nil, errors.New("file changed while opening")
	}
	raw, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}
	if int64(len(raw)) > limit {
		clear(raw)
		return nil, errors.New("file exceeds size limit")
	}
	return raw, nil
}

func recoveryAAD(f file, accountID string) ([]byte, error) {
	raw, err := json.Marshal(struct {
		Version int       `json:"version"`
		Account string    `json:"account"`
		User    string    `json:"user"`
		KDF     kdfParams `json:"kdf"`
	}{Version: f.Version, Account: accountID, User: f.User, KDF: f.KDF})
	if err != nil {
		return nil, fmt.Errorf("vault: encode recovery binding: %w", err)
	}
	return append([]byte("anki-review/recovery/v1\x00"), raw...), nil
}

func validateSessions(user string, sessions []domain.AuthSession) error {
	if len(sessions) > maxSessions {
		return damaged("", "too many sessions")
	}
	seen := make(map[string]bool, len(sessions))
	for _, session := range sessions {
		digest, err := hex.DecodeString(session.Hash)
		if err != nil || len(digest) != 32 || strings.ToLower(session.Hash) != session.Hash || session.User != user || session.Expires.IsZero() || seen[session.Hash] {
			return damaged("", "invalid session record")
		}
		seen[session.Hash] = true
	}
	return nil
}

func (v *Vault) wrapRecovery(f file, key []byte, sessions []domain.AuthSession) (*recoveryEnvelope, error) {
	plain, err := json.Marshal(recoveryPayload{Key: key, Sessions: sessions})
	if err != nil {
		return nil, fmt.Errorf("vault: encode recovery: %w", err)
	}
	defer clear(plain)
	aead, err := chacha20poly1305.NewX(v.sessionKey)
	if err != nil {
		return nil, fmt.Errorf("vault: recovery cipher: %w", err)
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("vault: recovery nonce: %w", err)
	}
	aad, err := recoveryAAD(f, v.accountID)
	if err != nil {
		return nil, err
	}
	return &recoveryEnvelope{Nonce: base64.StdEncoding.EncodeToString(nonce), Sealed: base64.StdEncoding.EncodeToString(aead.Seal(nil, nonce, plain, aad))}, nil
}

func (v *Vault) unwrapRecovery(f file) (*recoveryPayload, error) {
	nonce, err := base64.StdEncoding.DecodeString(f.Recovery.Nonce)
	if err != nil || len(nonce) != chacha20poly1305.NonceSizeX {
		return nil, damaged("", "invalid recovery envelope")
	}
	sealed, err := base64.StdEncoding.DecodeString(f.Recovery.Sealed)
	if err != nil || len(sealed) < chacha20poly1305.Overhead {
		return nil, damaged("", "invalid recovery envelope")
	}
	aead, err := chacha20poly1305.NewX(v.sessionKey)
	if err != nil {
		return nil, fmt.Errorf("vault: recovery cipher: %w", err)
	}
	aad, err := recoveryAAD(f, v.accountID)
	if err != nil {
		return nil, err
	}
	plain, err := aead.Open(nil, nonce, sealed, aad)
	if err != nil {
		return nil, damaged("", "recovery authentication failed")
	}
	defer clear(plain)
	var payload recoveryPayload
	if err := json.Unmarshal(plain, &payload); err != nil {
		clear(payload.Key)
		return nil, damaged("", "invalid recovery payload")
	}
	if len(payload.Key) != keySize {
		clear(payload.Key)
		return nil, damaged("", "invalid recovery key")
	}
	if err := validateSessions(f.User, payload.Sessions); err != nil {
		clear(payload.Key)
		return nil, err
	}
	return &payload, nil
}

func (v *Vault) loadRecovery() (file, *recoveryPayload, error) {
	raw, err := readRegular(v.path, maxMetadataBytes)
	if err != nil {
		return file{}, nil, fmt.Errorf("vault: read recovery: %w", err)
	}
	f, _, _, _, err := parse(raw)
	if err != nil {
		return file{}, nil, err
	}
	if f.Recovery == nil {
		return f, nil, nil
	}
	payload, err := v.unwrapRecovery(f)
	return f, payload, err
}

// LoadSessions authenticates persisted metadata without installing the password-derived key.
func (v *Vault) LoadSessions() ([]domain.AuthSession, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if !v.PersistenceEnabled() {
		return nil, nil
	}
	_, payload, err := v.loadRecovery()
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if payload == nil {
		return nil, nil
	}
	defer clear(payload.Key)
	return payload.Sessions, nil
}

// SaveSessions updates only the recovery envelope, preserving the current password ciphertext.
func (v *Vault) SaveSessions(sessions []domain.AuthSession) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if !v.PersistenceEnabled() {
		return ErrLocked
	}
	f, previous, err := v.loadRecovery()
	if err != nil {
		return err
	}
	if previous != nil {
		defer clear(previous.Key)
	}
	if err := validateSessions(f.User, sessions); err != nil {
		return err
	}
	var key []byte
	switch {
	case previous != nil:
		key = previous.Key
	case v.handle != nil && f.User == v.handle.user && f.KDF == v.handle.kdf:
		key = v.handle.key
	default:
		return ErrLocked
	}
	f.Recovery, err = v.wrapRecovery(f, key, sessions)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(f)
	if err != nil {
		return fmt.Errorf("vault: encode session update: %w", err)
	}
	return v.write(raw)
}

// Recover decrypts the current credentials, then installs their key for normal credential refresh.
func (v *Vault) Recover() (domain.Secrets, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if !v.PersistenceEnabled() {
		return domain.Secrets{}, ErrLocked
	}
	f, payload, err := v.loadRecovery()
	if err != nil {
		return domain.Secrets{}, err
	}
	if payload == nil {
		return domain.Secrets{}, ErrLocked
	}
	defer clear(payload.Key)
	nonce, err := base64.StdEncoding.DecodeString(f.Nonce)
	if err != nil {
		return domain.Secrets{}, damaged("", "invalid credentials nonce")
	}
	sealed, err := base64.StdEncoding.DecodeString(f.Sealed)
	if err != nil {
		return domain.Secrets{}, damaged("", "invalid credentials ciphertext")
	}
	aead, err := chacha20poly1305.NewX(payload.Key)
	if err != nil {
		return domain.Secrets{}, fmt.Errorf("vault: credentials cipher: %w", err)
	}
	plain, err := aead.Open(nil, nonce, sealed, aad(f.User))
	if err != nil {
		return domain.Secrets{}, damaged("", "credentials authentication failed")
	}
	defer clear(plain)
	var secrets domain.Secrets
	if err := json.Unmarshal(plain, &secrets); err != nil {
		return domain.Secrets{}, damaged("", "invalid credentials payload")
	}
	v.installHandle(unlockHandle{key: payload.Key, user: f.User, kdf: f.KDF})
	return secrets, nil
}
