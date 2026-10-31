package domain

import (
	"errors"
	"fmt"
)

// Secrets are the credentials the owner enters on the setup page. They are sealed on disk and
// live in memory only while the server is unlocked.
type Secrets struct {
	ClaudeToken    string `json:"claude_token"`
	AnkiKey        string `json:"anki_key"`
	GraderProvider string `json:"grader_provider,omitempty"`
	CodexAuth      string `json:"codex_auth,omitempty"`
}

// ErrCredentials is a wrong login or password; the vault and the owner use case both return it.
var ErrCredentials = errors.New("wrong login or password")

// ErrAnkiKey is AnkiConnect refusing the key. It is an ErrAnki too, so every AnkiConnect failure
// still maps the same way where the key doesn't matter.
var ErrAnkiKey = fmt.Errorf("%w: key refused", ErrAnki)
