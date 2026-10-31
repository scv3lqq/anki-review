// Package vault seals the owner's tokens in a file: Argon2id turns the password into a key,
// XChaCha20-Poly1305 seals the tokens. The file holds the login in clear, nothing else readable.
package vault

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/chacha20poly1305"

	"github.com/scv3lqq/anki-review/internal/domain"
)

const (
	fileName = "vault.json"
	version  = 1
	dirMode  = 0o700
	saltSize = 16
	kdfName  = "argon2id"
	// the AAD binds the sealed tokens to this file format and to the login
	aadPrefix = "anki-review/vault/v1\x00"
	keySize   = chacha20poly1305.KeySize
)

// Bounds of the KDF cost read from a file: outside them the file is damaged or forged.
const (
	minTime      = 1
	maxTime      = 10
	minMemoryKiB = 8
	maxMemoryKiB = 1 << 20
	minThreads   = 1
	maxThreads   = 16
)

// ErrDamaged means the file is not a vault this version can open. Deleting it starts setup over.
var ErrDamaged = errors.New("vault damaged")

// ErrLocked means no successful Seal or Open has supplied a current encryption key.
var ErrLocked = errors.New("vault locked")

// cost is the Argon2id cost of a seal.
type cost struct {
	time      uint32
	memoryKiB uint32
	threads   uint8
}

// Option changes the cost new seals use; the defaults are the production cost.
type Option func(*Vault)

// WithCost sets the Argon2id cost. Tests lower it to stay fast; Open takes the cost from the file.
func WithCost(time, memoryKiB uint32, threads uint8) Option {
	return func(v *Vault) { v.cost = cost{time: time, memoryKiB: memoryKiB, threads: threads} }
}

// Vault is the sealed file in a directory only the owner can read.
type Vault struct {
	path   string
	cost   cost
	mu     sync.Mutex
	handle *unlockHandle
}

// New creates the directory if needed. The defaults are Argon2id t=3, m=64 MiB, p=4.
func New(dir string, opts ...Option) (*Vault, error) {
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return nil, fmt.Errorf("vault: %w", err)
	}
	v := &Vault{path: filepath.Join(dir, fileName), cost: cost{time: 3, memoryKiB: 64 << 10, threads: 4}}
	for _, opt := range opts {
		opt(v)
	}
	if err := checkCost(v.cost.time, v.cost.memoryKiB, v.cost.threads); err != nil {
		return nil, fmt.Errorf("vault: %w", err)
	}
	return v, nil
}

// kdfParams is the KDF section of the file.
type kdfParams struct {
	Name      string `json:"name"`
	Time      uint32 `json:"time"`
	MemoryKiB uint32 `json:"memory_kib"`
	Threads   uint8  `json:"threads"`
	Salt      string `json:"salt"`
}

type file struct {
	Version int       `json:"version"`
	User    string    `json:"user"`
	KDF     kdfParams `json:"kdf"`
	Nonce   string    `json:"nonce"`
	Sealed  string    `json:"sealed"`
}

// Exists reports whether a vault file is there, whatever its content.
func (v *Vault) Exists() (bool, error) {
	_, err := os.Stat(v.path)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	default:
		return false, fmt.Errorf("vault: stat: %w", err)
	}
}

// unlockHandle retains only the derived key and public envelope metadata, never the password.
type unlockHandle struct {
	key  []byte
	user string
	kdf  kdfParams
}

// Seal replaces the file with s sealed under a key from password. Every seal draws a new salt and nonce.
func (v *Vault) Seal(user, password string, s domain.Secrets) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	salt := make([]byte, saltSize)
	if _, err := rand.Read(salt); err != nil {
		return fmt.Errorf("vault: salt: %w", err)
	}
	key := deriveKey(password, salt, v.cost)
	defer clear(key)
	handle := unlockHandle{key: key, user: user, kdf: kdfParams{
		Name: kdfName, Time: v.cost.time, MemoryKiB: v.cost.memoryKiB, Threads: v.cost.threads,
		Salt: base64.StdEncoding.EncodeToString(salt),
	}}
	if err := v.seal(s, handle); err != nil {
		return err
	}
	v.installHandle(handle)
	return nil
}

// Reseal updates credentials using the unlocked key and a fresh nonce, preserving the KDF metadata.
func (v *Vault) Reseal(s domain.Secrets) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.handle == nil {
		return ErrLocked
	}
	return v.seal(s, *v.handle)
}

func (v *Vault) seal(s domain.Secrets, handle unlockHandle) error {
	plain, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("vault: encode: %w", err)
	}
	defer clear(plain)
	aead, err := chacha20poly1305.NewX(handle.key)
	if err != nil {
		return fmt.Errorf("vault: cipher: %w", err)
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return fmt.Errorf("vault: nonce: %w", err)
	}
	sealed := aead.Seal(nil, nonce, plain, aad(handle.user))
	raw, err := json.Marshal(file{
		Version: version, User: handle.user, KDF: handle.kdf,
		Nonce:  base64.StdEncoding.EncodeToString(nonce),
		Sealed: base64.StdEncoding.EncodeToString(sealed),
	})
	if err != nil {
		return fmt.Errorf("vault: encode file: %w", err)
	}
	return v.write(raw)
}

// Open returns the sealed credentials. A wrong login or password gives domain.ErrCredentials;
// the user check and AEAD check both run so timing does not distinguish these failures.
func (v *Vault) Open(user, password string) (domain.Secrets, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	raw, err := os.ReadFile(v.path)
	if err != nil {
		return domain.Secrets{}, fmt.Errorf("vault: open: %w", err)
	}
	f, salt, nonce, sealed, err := parse(raw)
	if err != nil {
		return domain.Secrets{}, fmt.Errorf("vault: %s: %w", v.path, err)
	}
	c := cost{time: f.KDF.Time, memoryKiB: f.KDF.MemoryKiB, threads: f.KDF.Threads}
	key := deriveKey(password, salt, c)
	defer clear(key)
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		return domain.Secrets{}, fmt.Errorf("vault: cipher: %w", err)
	}
	userOK := subtle.ConstantTimeCompare([]byte(user), []byte(f.User)) == 1
	plain, err := aead.Open(nil, nonce, sealed, aad(f.User))
	defer clear(plain)
	if !userOK || err != nil {
		return domain.Secrets{}, domain.ErrCredentials
	}
	var s domain.Secrets
	if err := json.Unmarshal(plain, &s); err != nil {
		return domain.Secrets{}, damaged(v.path, fmt.Sprintf("decode secrets: %v", err))
	}
	v.installHandle(unlockHandle{key: key, user: f.User, kdf: f.KDF})
	return s, nil
}

// Close clears the retained encryption key. A successful Seal or Open unlocks the vault again.
func (v *Vault) Close() {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.clearHandle()
}

// installHandle runs only after authentication or persistence has succeeded, while mu is held.
func (v *Vault) installHandle(handle unlockHandle) {
	v.clearHandle()
	handle.key = append([]byte(nil), handle.key...)
	v.handle = &handle
}

func (v *Vault) clearHandle() {
	if v.handle != nil {
		clear(v.handle.key)
		v.handle = nil
	}
}

// parse checks the file's shape and KDF bounds before any password is tried.
func parse(raw []byte) (file, []byte, []byte, []byte, error) {
	var f file
	if err := json.Unmarshal(raw, &f); err != nil {
		return file{}, nil, nil, nil, damaged("", fmt.Sprintf("decode: %v", err))
	}
	if f.Version != version {
		return file{}, nil, nil, nil, damaged("", fmt.Sprintf("version %d, want %d", f.Version, version))
	}
	if f.User == "" || f.KDF.Name != kdfName {
		return file{}, nil, nil, nil, damaged("", "no user or unknown KDF")
	}
	if err := checkCost(f.KDF.Time, f.KDF.MemoryKiB, f.KDF.Threads); err != nil {
		return file{}, nil, nil, nil, damaged("", err.Error())
	}
	salt, err := base64.StdEncoding.DecodeString(f.KDF.Salt)
	if err != nil || len(salt) != saltSize {
		return file{}, nil, nil, nil, damaged("", "bad salt")
	}
	nonce, err := base64.StdEncoding.DecodeString(f.Nonce)
	if err != nil || len(nonce) != chacha20poly1305.NonceSizeX {
		return file{}, nil, nil, nil, damaged("", "bad nonce")
	}
	sealed, err := base64.StdEncoding.DecodeString(f.Sealed)
	if err != nil || len(sealed) < chacha20poly1305.Overhead {
		return file{}, nil, nil, nil, damaged("", "bad sealed data")
	}
	return f, salt, nonce, sealed, nil
}

// damaged names the file (when known) and says how to recover.
func damaged(path, reason string) error {
	if path == "" {
		return fmt.Errorf("%w: %s; delete the vault file to set up again", ErrDamaged, reason)
	}
	return fmt.Errorf("%w: %s: %s; delete it to set up again", ErrDamaged, path, reason)
}

func checkCost(time, memoryKiB uint32, threads uint8) error {
	switch {
	case time < minTime || time > maxTime:
		return fmt.Errorf("argon2 time %d outside %d to %d", time, minTime, maxTime)
	case memoryKiB < minMemoryKiB || memoryKiB > maxMemoryKiB:
		return fmt.Errorf("argon2 memory %d KiB outside %d to %d", memoryKiB, minMemoryKiB, maxMemoryKiB)
	case threads < minThreads || threads > maxThreads:
		return fmt.Errorf("argon2 threads %d outside %d to %d", threads, minThreads, maxThreads)
	}
	return nil
}

func deriveKey(password string, salt []byte, c cost) []byte {
	return argon2.IDKey([]byte(password), salt, c.time, c.memoryKiB, c.threads, keySize)
}

func aad(user string) []byte {
	return []byte(aadPrefix + user)
}

// write replaces the file atomically: a crash leaves the old vault or the new one, never half of one.
func (v *Vault) write(raw []byte) error {
	// CreateTemp makes the file 0600
	tmp, err := os.CreateTemp(filepath.Dir(v.path), fileName+".*.tmp")
	if err != nil {
		return fmt.Errorf("vault: save: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("vault: write: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("vault: sync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("vault: close: %w", err)
	}
	if err := os.Rename(tmp.Name(), v.path); err != nil {
		return fmt.Errorf("vault: rename: %w", err)
	}
	// the rename itself lives in the directory entry: sync it, or a crash can forget the new vault
	dir, err := os.Open(filepath.Dir(v.path))
	if err != nil {
		return fmt.Errorf("vault: open dir: %w", err)
	}
	if err := dir.Sync(); err != nil {
		_ = dir.Close()
		return fmt.Errorf("vault: sync dir: %w", err)
	}
	if err := dir.Close(); err != nil {
		return fmt.Errorf("vault: close dir: %w", err)
	}
	return nil
}
