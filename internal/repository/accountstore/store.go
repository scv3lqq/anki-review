// Package accountstore persists public account metadata in a private atomic registry.
package accountstore

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"github.com/scv3lqq/anki-review/internal/usecase/accounts"
)

const maxRegistryBytes = 64 * 1024

// Store owns accounts.json under the application's data directory.
type Store struct {
	path string
	mu   sync.Mutex
}

// New prepares a private directory, without changing any owner vault or saved pass.
func New(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("accountstore: create directory: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil { //nolint:gosec // A private directory requires its owner's execute bit.
		return nil, fmt.Errorf("accountstore: protect directory: %w", err)
	}
	return &Store{path: filepath.Join(dir, "accounts.json")}, nil
}

// Load rejects unsupported versions and malformed or oversized registries.
func (s *Store) Load() (accounts.State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	file, err := os.Open(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return accounts.State{Version: 1}, nil
	}
	if err != nil {
		return accounts.State{}, fmt.Errorf("accountstore: open registry: %w", err)
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, maxRegistryBytes+1))
	if err != nil {
		return accounts.State{}, fmt.Errorf("accountstore: read registry: %w", err)
	}
	if len(raw) > maxRegistryBytes {
		return accounts.State{}, fmt.Errorf("accountstore: registry exceeds limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var state accounts.State
	if err := decoder.Decode(&state); err != nil {
		return accounts.State{}, fmt.Errorf("accountstore: decode registry: %w", err)
	}
	if err := decoder.Decode(new(json.RawMessage)); !errors.Is(err, io.EOF) {
		return accounts.State{}, fmt.Errorf("accountstore: registry has trailing data")
	}
	if state.Version != 1 {
		return accounts.State{}, fmt.Errorf("accountstore: unsupported registry version")
	}
	return state, nil
}

// Save fsyncs both the replacement file and its directory before returning success.
func (s *Store) Save(state accounts.State) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if state.Version != 1 {
		return fmt.Errorf("accountstore: unsupported registry version")
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("accountstore: encode registry: %w", err)
	}
	if len(raw) > maxRegistryBytes {
		return fmt.Errorf("accountstore: registry exceeds limit")
	}
	dir := filepath.Dir(s.path)
	file, err := os.CreateTemp(dir, ".accounts-*.tmp")
	if err != nil {
		return fmt.Errorf("accountstore: create replacement: %w", err)
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(raw); err != nil {
		_ = file.Close()
		return fmt.Errorf("accountstore: write replacement: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("accountstore: sync replacement: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("accountstore: close replacement: %w", err)
	}
	if err := os.Rename(file.Name(), s.path); err != nil {
		return fmt.Errorf("accountstore: replace registry: %w", err)
	}
	directory, err := os.Open(dir) //nolint:gosec // The registry directory is administrator configuration, never a request field.
	if err != nil {
		return fmt.Errorf("accountstore: open directory: %w", err)
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil {
		return fmt.Errorf("accountstore: sync directory: %w", err)
	}
	return nil
}
