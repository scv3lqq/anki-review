package provisioner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"
)

// ExecRunner executes only the fixed Linux deployment tools with bounded output and time.
type ExecRunner struct{}

type boundedBuffer struct{ bytes.Buffer }

func (b *boundedBuffer) Write(data []byte) (int, error) {
	if b.Len()+len(data) > 1<<20 {
		return 0, errors.New("command output limit reached")
	}
	return b.Buffer.Write(data) //nolint:wrapcheck // bytes.Buffer.Write cannot return an error.
}

// Run never includes subprocess output or arguments in an error; secret values stay private.
func (ExecRunner) Run(ctx context.Context, program string, args ...string) ([]byte, error) {
	paths := map[string]string{"docker": "/usr/bin/docker", "findmnt": "/usr/bin/findmnt", "mount": "/usr/bin/mount", "umount": "/usr/bin/umount", "losetup": "/usr/sbin/losetup", "blkid": "/usr/sbin/blkid", "mkfs.ext4": "/usr/sbin/mkfs.ext4"}
	path, ok := paths[program]
	if !ok {
		return nil, errors.New("unsupported provisioner command")
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, path, args...) // #nosec G204 -- executable is selected from the fixed allowlist, with no shell or client arguments.
	command.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C", "HOME=/root", "DOCKER_HOST=unix:///var/run/docker.sock"}
	var stdout, stderr boundedBuffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	command.WaitDelay = 2 * time.Second
	if err := command.Run(); err != nil {
		return nil, fmt.Errorf("execute %s: %w", program, err)
	}
	return stdout.Bytes(), nil
}
