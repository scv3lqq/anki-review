package provisioner

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func randomHex(size int) (string, error) {
	data := make([]byte, size)
	if _, err := rand.Read(data); err != nil {
		return "", fmt.Errorf("generate runtime secret: %w", err)
	}
	return hex.EncodeToString(data), nil
}

func validName(name string) bool {
	if !strings.HasPrefix(name, "ar-") || len(name) != 35 {
		return false
	}
	_, err := hex.DecodeString(name[3:])
	return err == nil && strings.ToLower(name) == name
}

func privateDir(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return fmt.Errorf("create private directory: %w", err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("stat private directory: %w", err)
	}
	if !info.IsDir() || info.Mode().Perm() != 0700 {
		return errors.New("provisioner: private directory must have mode 0700 and cannot be a symlink")
	}
	return nil
}

func readPrivateJSON(path string, value any) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("stat private metadata: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > 16384 {
		return errors.New("provisioner: invalid private metadata")
	}
	file, err := os.Open(path) // #nosec G304 -- fixed metadata filename below the private daemon root.
	if err != nil {
		return fmt.Errorf("open private metadata: %w", err)
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 16385))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return fmt.Errorf("decode private metadata: %w", err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("provisioner: trailing metadata")
	}
	return nil
}

func writePrivateJSON(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode private metadata: %w", err)
	}
	return atomicPrivateFile(path, data, os.Geteuid(), os.Getegid())
}

func atomicPrivateFile(path string, data []byte, uid, gid int) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".write-")
	if err != nil {
		return fmt.Errorf("create private temporary file: %w", err)
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if err := file.Chown(uid, gid); err != nil {
		return fmt.Errorf("set private file ownership: %w", err)
	}
	if _, err := file.Write(data); err != nil {
		return fmt.Errorf("write private file: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync private file: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close private file: %w", err)
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return fmt.Errorf("publish private file: %w", err)
	}
	return syncDirectory(filepath.Dir(path))
}

func syncDirectory(path string) error {
	file, err := os.Open(path) // #nosec G304 -- administrator root and generated reservation directories only.
	if err != nil {
		return fmt.Errorf("open directory for sync: %w", err)
	}
	defer file.Close()
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync directory: %w", err)
	}
	return nil
}
