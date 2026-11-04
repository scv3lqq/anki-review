package provisioner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Filesystem supplies a hard capacity bound using a private Linux ext4 loop filesystem.
// The backing image, filesystem UUID and kernel loop association must agree on every reuse.
type Filesystem struct {
	Runner   Runner
	platform string
}

type filesystemIdentity struct {
	Version int    `json:"version"`
	UUID    string `json:"uuid"`
	Bytes   int64  `json:"bytes"`
}

// ErrManualRecovery retains possible collection data written outside a mounted quota.
var ErrManualRecovery = errors.New("provisioner: unmounted fallback contains data; manual recovery is required")

// Mounted reports only a mount whose backing image, UUID and loop association agree.
func (s *Filesystem) Mounted(ctx context.Context, root string) (bool, error) {
	if err := s.supported(); err != nil {
		return false, err
	}
	identity, mount, err := s.identity(ctx, root, 0)
	if err != nil {
		return false, err
	}
	if mount == nil {
		return false, nil
	}
	if err := s.verifyImage(ctx, root, identity); err != nil {
		return false, err
	}
	if err := s.verifyMount(ctx, root, identity); err != nil {
		return false, err
	}
	return true, nil
}

// Ensure never falls back to an unbounded directory or an ordinary Docker volume.
func (s *Filesystem) Ensure(ctx context.Context, root string, size int64, uid, gid int) error {
	if err := s.supported(); err != nil {
		return err
	}
	if err := privateDir(root); err != nil {
		return err
	}
	identity, mounted, err := s.identity(ctx, root, size)
	if err != nil {
		return err
	}
	if mounted == nil {
		if err := emptyFallback(filepath.Join(root, "mount")); err != nil {
			return err
		}
	}
	image := filepath.Join(root, "storage.img")
	if _, err := os.Lstat(image); errors.Is(err, os.ErrNotExist) {
		if mounted != nil {
			return errors.New("provisioner: mounted filesystem has no backing image")
		}
		if err := s.createImage(ctx, root, identity); err != nil {
			return err
		}
	} else if err != nil {
		return fmt.Errorf("stat backing image: %w", err)
	}
	if err := s.verifyImage(ctx, root, identity); err != nil {
		return err
	}
	path := filepath.Join(root, "mount")
	if mounted == nil {
		if err := privateDir(path); err != nil {
			return err
		}
		if _, err := s.run(ctx, "mount", "-t", "ext4", "-o", "loop,nodev,nosuid,noexec", image, path); err != nil {
			return err
		}
	}
	if err := s.verifyMount(ctx, root, identity); err != nil {
		return err
	}
	if err := os.Chmod(path, 0700); err != nil {
		return fmt.Errorf("protect mount root: %w", err)
	} // #nosec G302 -- this is a directory and requires owner search permission.
	data := filepath.Join(path, "data")
	if err := privateDir(data); err != nil {
		return err
	}
	if err := os.Chown(data, uid, gid); err != nil {
		return fmt.Errorf("set bounded storage owner: %w", err)
	}
	return nil
}

func emptyFallback(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect unmounted fallback: %w", err)
	}
	if !info.IsDir() {
		return errors.New("provisioner: invalid unmounted fallback directory")
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return fmt.Errorf("inspect unmounted fallback contents: %w", err)
	}
	if len(entries) != 0 {
		return ErrManualRecovery
	}
	return nil
}

// Remove refuses foreign mounts and detaches only a loop backed by this exact image.
func (s *Filesystem) Remove(ctx context.Context, root string) error {
	if err := s.supported(); err != nil {
		return err
	}
	identity, mounted, err := s.identity(ctx, root, 0)
	if err != nil {
		return err
	}
	if mounted == nil {
		if err := emptyFallback(filepath.Join(root, "mount")); err != nil {
			return err
		}
	}
	image := filepath.Join(root, "storage.img")
	if _, err := os.Lstat(image); errors.Is(err, os.ErrNotExist) {
		if mounted != nil {
			return errors.New("provisioner: refusing mount without backing image")
		}
		return nil
	} else if err != nil {
		return fmt.Errorf("stat removed backing image: %w", err)
	}
	if err := s.verifyImage(ctx, root, identity); err != nil {
		return err
	}
	if mounted != nil {
		if err := s.verifyMount(ctx, root, identity); err != nil {
			return err
		}
		if _, err := s.run(ctx, "umount", "--", filepath.Join(root, "mount")); err != nil {
			return err
		}
	}
	// mount -o loop normally autoclears the device; retain the image if any loop still owns it.
	loops, err := s.loops(ctx)
	if err != nil {
		return err
	}
	for _, loop := range loops {
		if loop.Backing == image {
			return errors.New("provisioner: backing image is still attached to a loop device")
		}
	}
	return nil
}

func (s *Filesystem) supported() error {
	platform := s.platform
	if platform == "" {
		platform = runtime.GOOS
	}
	if platform != "linux" || s.Runner == nil {
		return errors.New("provisioner: Linux ext4 loop filesystem quota is required")
	}
	return nil
}

func (s *Filesystem) run(ctx context.Context, program string, args ...string) ([]byte, error) {
	result, err := s.Runner.Run(ctx, program, args...)
	if err != nil {
		return nil, fmt.Errorf("filesystem %s: %w", program, err)
	}
	return result, nil
}

func (s *Filesystem) identity(ctx context.Context, root string, size int64) (filesystemIdentity, *mountInfo, error) {
	mounted, err := s.mount(ctx, filepath.Join(root, "mount"))
	if err != nil {
		return filesystemIdentity{}, nil, err
	}
	path := filepath.Join(root, "filesystem.json")
	var identity filesystemIdentity
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		if mounted != nil {
			return identity, nil, errors.New("provisioner: refusing mount without filesystem identity")
		}
		if size == 0 {
			return identity, nil, nil
		}
		random, err := randomHex(16)
		if err != nil {
			return identity, nil, err
		}
		uuid := random[:8] + "-" + random[8:12] + "-" + random[12:16] + "-" + random[16:20] + "-" + random[20:]
		identity = filesystemIdentity{Version: 1, UUID: uuid, Bytes: size}
		if err := writePrivateJSON(path, identity); err != nil {
			return identity, nil, err
		}
	} else if err != nil {
		return identity, nil, fmt.Errorf("stat filesystem identity: %w", err)
	} else if err := readPrivateJSON(path, &identity); err != nil {
		return identity, nil, err
	}
	if identity.Version != 1 || len(identity.UUID) != 36 || identity.Bytes < 64<<20 || identity.Bytes > 64<<30 || (size != 0 && identity.Bytes != size) {
		return identity, nil, errors.New("provisioner: invalid filesystem identity or changed quota")
	}
	return identity, mounted, nil
}

func (s *Filesystem) createImage(ctx context.Context, root string, identity filesystemIdentity) error {
	// A temporary image cannot have been exposed to Docker. Publish only after mkfs and fsync.
	entries, err := os.ReadDir(root)
	if err != nil {
		return fmt.Errorf("read image staging directory: %w", err)
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), ".storage-build-") {
			continue
		}
		if entry.Type()&os.ModeSymlink != 0 || entry.IsDir() {
			return errors.New("provisioner: unsafe image staging file")
		}
		if err := os.Remove(filepath.Join(root, entry.Name())); err != nil {
			return fmt.Errorf("remove abandoned image staging file: %w", err)
		}
	}
	file, err := os.CreateTemp(root, ".storage-build-")
	if err != nil {
		return fmt.Errorf("create fixed backing image: %w", err)
	}
	defer file.Close()
	defer os.Remove(file.Name())
	if err := file.Truncate(identity.Bytes); err != nil {
		return fmt.Errorf("size fixed backing image: %w", err)
	}
	if _, err := s.run(ctx, "mkfs.ext4", "-q", "-F", "-m", "0", "-U", identity.UUID, file.Name()); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync formatted image: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close formatted image: %w", err)
	}
	if err := os.Rename(file.Name(), filepath.Join(root, "storage.img")); err != nil {
		return fmt.Errorf("publish formatted image: %w", err)
	}
	return syncDirectory(root)
}

func (s *Filesystem) verifyImage(ctx context.Context, root string, identity filesystemIdentity) error {
	image := filepath.Join(root, "storage.img")
	info, err := os.Lstat(image)
	if err != nil {
		return fmt.Errorf("stat backing filesystem: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() != identity.Bytes {
		return errors.New("provisioner: backing image identity or size mismatch")
	}
	uuid, err := s.run(ctx, "blkid", "-p", "-s", "UUID", "-o", "value", image)
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(uuid)) != identity.UUID {
		return errors.New("provisioner: backing image filesystem UUID mismatch")
	}
	return nil
}
