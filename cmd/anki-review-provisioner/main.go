// anki-review-provisioner is the privileged Unix-only runtime daemon.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"time"

	"github.com/rs/zerolog"

	"github.com/scv3lqq/anki-review/internal/provisioner"
)

func main() {
	logger := zerolog.New(os.Stderr).With().Timestamp().Logger()
	if err := run(logger); err != nil {
		logger.Fatal().Err(err).Msg("provisioner stopped")
	}
}

func run(logger zerolog.Logger) error {
	var config provisioner.Config
	var socket, groupName string
	flag.StringVar(&config.Root, "root", "/var/lib/anki-review-provisioner", "Private root-owned reservation and filesystem directory")
	flag.StringVar(&socket, "socket", "/run/anki-review-provisioner/control.sock", "Group-protected Unix socket")
	flag.StringVar(&groupName, "group", "anki-review", "Public HTTP service socket group")
	flag.IntVar(&config.MaxGuests, "max-guests", 3, "Independent guest capacity (1..7)")
	flag.Int64Var(&config.StorageBytes, "storage-bytes", 2<<30, "Hard per-runtime ext4 filesystem size")
	flag.IntVar(&config.UID, "uid", 1000, "Container UID matching the pinned image user")
	flag.IntVar(&config.GID, "gid", 1000, "Container GID matching the pinned image user")
	flag.Parse()
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		return errors.New("provisioner requires root on Linux with ext4 loop mount support")
	}
	if flag.NArg() != 0 || !filepath.IsAbs(socket) || filepath.Clean(socket) != socket || len(socket) > 100 || filepath.Dir(socket) == config.Root || config.UID <= 0 || config.GID <= 0 {
		return errors.New("invalid daemon arguments")
	}
	group, err := user.LookupGroup(groupName)
	if err != nil {
		return fmt.Errorf("lookup socket group: %w", err)
	}
	groupID, err := strconv.Atoi(group.Gid)
	if err != nil {
		return fmt.Errorf("parse socket group: %w", err)
	}
	if groupID <= 0 {
		return errors.New("socket group cannot be the root group")
	}
	if err := prepareDirectory(filepath.Dir(socket), 0750, groupID); err != nil {
		return err
	}
	lock, err := acquireLock(filepath.Join(filepath.Dir(socket), "daemon.lock"))
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := prepareDirectory(config.Root, 0700, 0); err != nil {
		return err
	}
	runner := provisioner.ExecRunner{}
	service, err := provisioner.New(config, runner, &provisioner.Filesystem{Runner: runner})
	if err != nil {
		return err
	}
	if info, err := os.Lstat(socket); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return errors.New("refusing to replace non-socket path")
		}
		if err := os.Remove(socket); err != nil {
			return fmt.Errorf("remove stale socket: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat daemon socket: %w", err)
	}
	listenConfig := net.ListenConfig{}
	listener, err := listenConfig.Listen(context.Background(), "unix", socket)
	if err != nil {
		return fmt.Errorf("listen Unix socket: %w", err)
	}
	defer listener.Close()
	if err := os.Chown(socket, 0, groupID); err != nil {
		return fmt.Errorf("set socket owner: %w", err)
	}
	if err := os.Chmod(socket, 0660); err != nil {
		return fmt.Errorf("set socket permissions: %w", err)
	} // #nosec G302 -- the dedicated HTTP service group must connect to the Unix socket.
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	server := &http.Server{Handler: service, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 2 * time.Minute, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8192, BaseContext: func(net.Listener) context.Context { return ctx }}
	result := make(chan error, 1)
	go func() { result <- server.Serve(listener) }()
	reconcileCtx, stopReconcile := context.WithCancel(ctx)
	defer stopReconcile()
	reconcileDone := make(chan struct{})
	go func() { defer close(reconcileDone); reconcileLoop(reconcileCtx, service, config.MaxGuests, logger) }()
	select {
	case err := <-result:
		if !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve broker: %w", err)
		}
	case <-ctx.Done():
		shutdownCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown broker: %w", err)
		}
		select {
		case <-reconcileDone:
		case <-shutdownCtx.Done():
			return fmt.Errorf("stop runtime reconciliation: %w", shutdownCtx.Err())
		}
	}
	return nil
}

func reconcileLoop(ctx context.Context, service *provisioner.Service, maxGuests int, logger zerolog.Logger) {
	reconcile := func() {
		batchCtx, cancel := context.WithTimeout(ctx, time.Duration(maxGuests)*90*time.Second+5*time.Second)
		defer cancel()
		if err := service.Reconcile(batchCtx); err != nil && ctx.Err() == nil {
			logger.Warn().Err(err).Msg("guest runtime reconciliation failed")
		}
	}
	reconcile()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			reconcile()
		}
	}
}

func prepareDirectory(path string, mode os.FileMode, gid int) error {
	if err := os.MkdirAll(path, mode); err != nil {
		return fmt.Errorf("create daemon directory: %w", err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("stat daemon directory: %w", err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || !ok || stat.Uid != 0 || info.Mode().Perm()&0022 != 0 {
		return errors.New("daemon directory must be root-owned and cannot be writable by other users")
	}
	if err := os.Chown(path, 0, gid); err != nil {
		return fmt.Errorf("set daemon directory group: %w", err)
	}
	if err := os.Chmod(path, mode); err != nil {
		return fmt.Errorf("protect daemon directory: %w", err)
	}
	return nil
}

func acquireLock(path string) (*os.File, error) {
	fd, err := syscall.Open(path, syscall.O_CREAT|syscall.O_RDWR|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0600)
	if err != nil {
		return nil, fmt.Errorf("open daemon lock: %w", err)
	}
	file := os.NewFile(uintptr(fd), path)
	if err := syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("acquire exclusive daemon lock: %w", err)
	}
	return file, nil
}
