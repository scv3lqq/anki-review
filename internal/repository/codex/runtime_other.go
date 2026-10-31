//go:build !linux

package codex

import (
	"errors"
	"os"
)

func runtimeHome() (string, error) {
	// On macOS the owner controls filesystem encryption; credentials are removed after each call.
	root, err := os.MkdirTemp("", "anki-codex-")
	if err != nil {
		return "", errors.New("codex: cannot create private temporary credential directory")
	}
	return root, nil
}
