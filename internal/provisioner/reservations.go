package provisioner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func (s *Service) publishReservation(record reservation) error {
	directory, err := os.MkdirTemp(s.config.Root, ".reserve-")
	if err != nil {
		return fmt.Errorf("stage runtime reservation: %w", err)
	}
	defer os.RemoveAll(directory)
	if err := writePrivateJSON(filepath.Join(directory, "reservation.json"), record); err != nil {
		return err
	}
	if err := os.Rename(directory, s.directory(record)); err != nil {
		return fmt.Errorf("publish runtime reservation: %w", err)
	}
	return syncDirectory(s.config.Root)
}

func (s *Service) clearTombstone(path string) error {
	info, err := os.Lstat(path) // #nosec G703 -- path is built only from a validated guest ID under the private daemon root.
	if err != nil {
		return fmt.Errorf("stat removed reservation: %w", err)
	}
	if !info.IsDir() || info.Mode().Perm() != 0700 {
		return errors.New("provisioner: unsafe removed reservation directory")
	}
	if err := s.storage.CheckUnmounted(context.Background(), path); err != nil {
		return fmt.Errorf("retain mounted runtime tombstone: %w", err)
	}
	if err := os.RemoveAll(path); err != nil { // #nosec G703 -- generated tombstone path is private, validated, and proven to contain no mounts.
		return fmt.Errorf("remove reservation directory: %w", err)
	}
	return syncDirectory(s.config.Root)
}
