package provisioner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

type mountInfo struct {
	Target string `json:"target"`
	Source string `json:"source"`
	Type   string `json:"fstype"`
	UUID   string `json:"uuid"`
}

type loopInfo struct {
	Name    string `json:"name"`
	Backing string `json:"back-file"`
}

// CheckUnmounted protects recursive cleanup even after an interrupted tombstone removal.
func (s *Filesystem) CheckUnmounted(ctx context.Context, path string) error {
	if err := s.supported(); err != nil {
		return err
	}
	data, err := s.run(ctx, "findmnt", "--json", "--list", "--output", "TARGET,SOURCE,FSTYPE,UUID")
	if err != nil {
		return err
	}
	var table struct {
		Filesystems []mountInfo `json:"filesystems"`
	}
	if err := json.Unmarshal(data, &table); err != nil {
		return fmt.Errorf("decode cleanup mount table: %w", err)
	}
	for _, mount := range table.Filesystems {
		if mount.Target == path || strings.HasPrefix(mount.Target, path+string(filepath.Separator)) {
			return errors.New("provisioner: recursive cleanup would cross a live mount")
		}
	}
	devices, err := s.loops(ctx)
	if err != nil {
		return err
	}
	for _, device := range devices {
		if strings.HasPrefix(device.Backing, path+string(filepath.Separator)) {
			return errors.New("provisioner: recursive cleanup would delete an attached backing image")
		}
	}
	return nil
}

func (s *Filesystem) mount(ctx context.Context, path string) (*mountInfo, error) {
	data, err := s.run(ctx, "findmnt", "--json", "--list", "--output", "TARGET,SOURCE,FSTYPE,UUID")
	if err != nil {
		return nil, err
	}
	var table struct {
		Filesystems []mountInfo `json:"filesystems"`
	}
	if err := json.Unmarshal(data, &table); err != nil {
		return nil, fmt.Errorf("decode kernel mount table: %w", err)
	}
	var result *mountInfo
	for _, mount := range table.Filesystems {
		if mount.Target == path {
			if result != nil {
				return nil, errors.New("provisioner: ambiguous mount identity")
			}
			copyMount := mount
			result = &copyMount
		}
	}
	return result, nil
}

func (s *Filesystem) loops(ctx context.Context) ([]loopInfo, error) {
	data, err := s.run(ctx, "losetup", "--json", "--output", "NAME,BACK-FILE")
	if err != nil {
		return nil, err
	}
	var devices struct {
		Loops []loopInfo `json:"loopdevices"`
	}
	if err := json.Unmarshal(data, &devices); err != nil {
		return nil, fmt.Errorf("decode kernel loop table: %w", err)
	}
	return devices.Loops, nil
}

func (s *Filesystem) verifyMount(ctx context.Context, root string, identity filesystemIdentity) error {
	mount, err := s.mount(ctx, filepath.Join(root, "mount"))
	if err != nil {
		return err
	}
	if mount == nil || mount.Type != "ext4" || mount.UUID != identity.UUID || !strings.HasPrefix(mount.Source, "/dev/loop") {
		return errors.New("provisioner: foreign or missing filesystem mount")
	}
	devices, err := s.loops(ctx)
	if err != nil {
		return err
	}
	for _, device := range devices {
		if device.Name == mount.Source && device.Backing == filepath.Join(root, "storage.img") {
			return nil
		}
	}
	return errors.New("provisioner: mount loop backing mismatch")
}
