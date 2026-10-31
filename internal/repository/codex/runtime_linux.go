//go:build linux

package codex

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func runtimeHome() (string, error) {
	for _, base := range []string{os.Getenv("TMPDIR"), "/dev/shm"} {
		if base == "" {
			continue
		}
		var stat unix.Statfs_t
		if unix.Statfs(base, &stat) != nil || stat.Type != unix.TMPFS_MAGIC {
			continue
		}
		if root, err := os.MkdirTemp(base, "anki-codex-"); err == nil {
			return root, nil
		}
	}
	return "", errors.New("codex: memory-backed temporary directory required for credentials")
}
