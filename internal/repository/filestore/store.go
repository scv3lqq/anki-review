// Package filestore keeps the current pass in a JSON file, so a restart resumes it.
package filestore

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/scv3lqq/anki-review/internal/domain"
)

const (
	fileName = "state.json"
	version  = 1
	dirMode  = 0o700
)

type state struct {
	Version int          `json:"version"`
	Pass    *domain.Pass `json:"pass"`
}

// Store is a single state file in a directory only the owner can read: it holds answer keys.
type Store struct {
	path string
}

// New creates the directory if needed.
func New(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return nil, fmt.Errorf("filestore: %w", err)
	}
	return &Store{path: filepath.Join(dir, fileName)}, nil
}

// Load returns the saved pass, nil when there is none.
func (s *Store) Load() (*domain.Pass, error) {
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("filestore: load: %w", err)
	}
	var st state
	if err := json.Unmarshal(raw, &st); err != nil {
		return nil, fmt.Errorf("filestore: decode %s: %w", s.path, err)
	}
	if st.Version != version {
		return nil, fmt.Errorf("filestore: %s has version %d, want %d", s.path, st.Version, version)
	}
	return st.Pass, nil
}

// Save replaces the file atomically: a crash never leaves half a pass behind.
func (s *Store) Save(p *domain.Pass) error {
	raw, err := json.Marshal(state{Version: version, Pass: p})
	if err != nil {
		return fmt.Errorf("filestore: encode: %w", err)
	}
	// CreateTemp makes the file 0600
	tmp, err := os.CreateTemp(filepath.Dir(s.path), fileName+".*.tmp")
	if err != nil {
		return fmt.Errorf("filestore: save: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("filestore: write: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("filestore: sync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("filestore: close: %w", err)
	}
	if err := os.Rename(tmp.Name(), s.path); err != nil {
		return fmt.Errorf("filestore: rename: %w", err)
	}
	return nil
}
