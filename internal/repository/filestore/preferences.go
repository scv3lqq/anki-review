package filestore

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"unicode"
	"unicode/utf8"
)

// NormalizeKnowledgeDecks validates and copies bounded account-owned preferences.
func NormalizeKnowledgeDecks(decks []string) ([]string, error) {
	if len(decks) > 64 {
		return nil, errors.New("choose at most 64 knowledge decks")
	}
	result := make([]string, 0, len(decks))
	seen := make(map[string]bool, len(decks))
	for _, deck := range decks {
		if deck == "" || len(deck) > 256 || !utf8.ValidString(deck) {
			return nil, errors.New("each knowledge deck must contain 1 to 256 bytes")
		}
		for _, r := range deck {
			if unicode.IsControl(r) {
				return nil, errors.New("knowledge decks cannot contain control characters")
			}
		}
		if !seen[deck] {
			seen[deck] = true
			result = append(result, deck)
		}
	}
	return result, nil
}

// LoadPreferences reads only this account's preferences; absence copies the defaults.
func LoadPreferences(dir string, defaults []string) ([]string, error) {
	path := filepath.Join(dir, "preferences.json")
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return NormalizeKnowledgeDecks(defaults)
	}
	if err != nil {
		return nil, fmt.Errorf("preferences: stat: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > 65536 {
		return nil, errors.New("preferences: invalid private file")
	}
	raw, err := os.ReadFile(path) // #nosec G304 -- fixed basename below the administrator-selected account directory.
	if err != nil {
		return nil, fmt.Errorf("preferences: read: %w", err)
	}
	var value struct {
		Decks *[]string `json:"knowledge_decks"`
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, fmt.Errorf("preferences: decode: %w", err)
	}
	if value.Decks == nil {
		return nil, errors.New("preferences: missing knowledge decks")
	}
	return NormalizeKnowledgeDecks(*value.Decks)
}

// SavePreferences atomically replaces and fsyncs private preferences before publishing success.
func SavePreferences(dir string, decks []string) error {
	decks, err := NormalizeKnowledgeDecks(decks)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(struct {
		Decks []string `json:"knowledge_decks"`
	}{decks})
	if err != nil {
		return fmt.Errorf("preferences: encode: %w", err)
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("preferences: directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".preferences-")
	if err != nil {
		return fmt.Errorf("preferences: create: %w", err)
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	if _, err := tmp.Write(raw); err != nil {
		return fmt.Errorf("preferences: write: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("preferences: sync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("preferences: close: %w", err)
	}
	if err := os.Rename(tmp.Name(), filepath.Join(dir, "preferences.json")); err != nil {
		return fmt.Errorf("preferences: replace: %w", err)
	}
	folder, err := os.Open(dir) // #nosec G304 -- account directory is configured internally, never a request field.
	if err != nil {
		return fmt.Errorf("preferences: open directory: %w", err)
	}
	defer folder.Close()
	if err := folder.Sync(); err != nil {
		return fmt.Errorf("preferences: sync directory: %w", err)
	}
	return nil
}
