package provisioner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type filesystemRunner struct {
	calls                []string
	mount, backing, uuid string
	foreign              bool
}

func (r *filesystemRunner) Run(_ context.Context, program string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, program+" "+strings.Join(args, " "))
	switch program {
	case "findmnt":
		mounts := []map[string]string{}
		if r.mount != "" {
			uuid := r.uuid
			if r.foreign {
				uuid = "foreign-uuid"
			}
			mounts = append(mounts, map[string]string{"target": r.mount, "source": "/dev/loop7", "fstype": "ext4", "uuid": uuid})
		}
		return json.Marshal(map[string]any{"filesystems": mounts})
	case "mkfs.ext4":
		for i, arg := range args {
			if arg == "-U" {
				r.uuid = args[i+1]
			}
		}
		return nil, nil
	case "blkid":
		return []byte(r.uuid + "\n"), nil
	case "mount":
		r.backing = args[len(args)-2]
		r.mount = args[len(args)-1]
		return nil, nil
	case "losetup":
		if r.backing == "" {
			return []byte(`{"loopdevices":[]}`), nil
		}
		return json.Marshal(map[string]any{"loopdevices": []map[string]string{{"name": "/dev/loop7", "back-file": r.backing}}})
	case "umount":
		r.mount = ""
		r.backing = ""
		return nil, nil
	default:
		return nil, fmt.Errorf("unexpected program %s", program)
	}
}

func TestFixedFilesystemQuotaAndForeignMountRefusal(t *testing.T) {
	runner := &filesystemRunner{}
	storage := &Filesystem{Runner: runner, platform: "linux"}
	root := filepath.Join(t.TempDir(), "guest")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	const size = 64 << 20
	if err := storage.Ensure(context.Background(), root, size, os.Getuid(), os.Getgid()); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(root, "storage.img"))
	if err != nil || info.Size() != size {
		t.Fatalf("backing size: %v %v", info, err)
	}
	if !strings.Contains(strings.Join(runner.calls, "\n"), "nodev,nosuid,noexec") {
		t.Fatal("missing mount restrictions")
	}
	before := len(runner.calls)
	if err := storage.Ensure(context.Background(), root, size, os.Getuid(), os.Getgid()); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(runner.calls[before:], "\n"), "mkfs") {
		t.Fatal("reformatted existing filesystem")
	}
	runner.foreign = true
	if err := storage.Ensure(context.Background(), root, size, os.Getuid(), os.Getgid()); err == nil {
		t.Fatal("foreign mount reused")
	}
	if err := storage.Remove(context.Background(), root); err == nil {
		t.Fatal("foreign mount removed")
	}
	if runner.mount == "" {
		t.Fatal("unmounted foreign filesystem")
	}
	runner.foreign = false
	runner.backing = "/some/owner/storage.img"
	if err := storage.Remove(context.Background(), root); err == nil {
		t.Fatal("foreign loop backing removed")
	}
	runner.backing = filepath.Join(root, "storage.img")
	if err := storage.CheckUnmounted(context.Background(), root); err == nil {
		t.Fatal("mounted root allowed recursive deletion")
	}
	if err := storage.Remove(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	if err := storage.CheckUnmounted(context.Background(), root); err != nil {
		t.Fatal(err)
	}
}

func TestFilesystemUnsupportedFailsClosed(t *testing.T) {
	storage := &Filesystem{Runner: &filesystemRunner{}, platform: "unsupported"}
	if err := storage.Ensure(context.Background(), t.TempDir(), 64<<20, 1, 1); err == nil {
		t.Fatal("unsupported quota accepted")
	}
}

func TestFilesystemRetainsUnboundedFallbackDataForManualRecovery(t *testing.T) {
	runner := &filesystemRunner{}
	storage := &Filesystem{Runner: runner, platform: "linux"}
	root := filepath.Join(t.TempDir(), "guest")
	path := filepath.Join(root, "mount", "data", "collection.anki2")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("fallback collection"), 0600); err != nil {
		t.Fatal(err)
	}
	err := storage.Ensure(context.Background(), root, 64<<20, os.Getuid(), os.Getgid())
	if err == nil || !strings.Contains(err.Error(), "manual recovery") {
		t.Fatalf("fallback recovery error: %v", err)
	}
	for _, call := range runner.calls {
		if strings.HasPrefix(call, "mount ") || strings.HasPrefix(call, "mkfs.ext4 ") {
			t.Fatalf("hid fallback data: %s", call)
		}
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "fallback collection" {
		t.Fatalf("fallback data changed: %q %v", data, err)
	}
	if err := storage.Remove(context.Background(), root); !errors.Is(err, ErrManualRecovery) {
		t.Fatalf("fallback deletion must require manual recovery: %v", err)
	}
}
