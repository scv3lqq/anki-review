// Package provisioner owns privileged, bounded guest Anki containers.
package provisioner

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/scv3lqq/anki-review/internal/repository/runtimebroker"
)

const (
	// Image is the only image the broker can create.
	Image            = "anki-desktop:26.09.3-ac26.10.14.0-audio1"
	managedLabel     = "io.anki-review.managed"
	accountLabel     = "io.anki-review.account"
	reservationLabel = "io.anki-review.reservation"
	managerValue     = "provisioner-v1"
)

// ErrCapacity means the independent privileged guest slot limit has been reached.
var ErrCapacity = errors.New("runtime guest capacity reached")

// Endpoint is returned only after authenticated AnkiConnect API v6 readiness.
type Endpoint = runtimebroker.Endpoint

// Config contains administrator settings, never fields from a client request.
type Config struct {
	Root         string
	MaxGuests    int
	StorageBytes int64
	UID          int
	GID          int
	ReadyTimeout time.Duration
}

// Runner executes fixed administrator-selected programs, without a shell.
type Runner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}

// Storage verifies a fixed-size private filesystem before it is exposed to Docker.
type Storage interface {
	Mounted(context.Context, string) (bool, error)
	Ensure(context.Context, string, int64, int, int) error
	Remove(context.Context, string) error
	CheckUnmounted(context.Context, string) error
}

type reservation struct {
	Version        int    `json:"version"`
	ID             string `json:"id"`
	Name           string `json:"name"`
	Port           int    `json:"port"`
	RebindRequired bool   `json:"rebind_required,omitempty"`
}

// Service serializes reservations and mutations independently of the public service.
type Service struct {
	mu           sync.Mutex
	config       Config
	runner       Runner
	storage      Storage
	reservations map[string]reservation
	ready        func(context.Context, Endpoint) error
}

// New loads durable reservations and fails closed on unknown or corrupt entries.
func New(config Config, runner Runner, storage Storage) (*Service, error) {
	if !filepath.IsAbs(config.Root) || filepath.Clean(config.Root) != config.Root || strings.ContainsAny(config.Root, ",\n\r:") || runner == nil || storage == nil {
		return nil, errors.New("provisioner: invalid private root or dependencies")
	}
	if config.MaxGuests == 0 {
		config.MaxGuests = 3
	}
	if config.StorageBytes == 0 {
		config.StorageBytes = 2 << 30
	}
	if config.ReadyTimeout == 0 {
		config.ReadyTimeout = 90 * time.Second
	}
	if config.MaxGuests < 1 || config.MaxGuests > 7 || config.StorageBytes < 64<<20 || config.StorageBytes > 64<<30 || config.UID < 0 || config.GID < 0 || config.ReadyTimeout <= 0 || config.ReadyTimeout > 90*time.Second {
		return nil, errors.New("provisioner: invalid guest limits")
	}
	if err := privateDir(config.Root); err != nil {
		return nil, err
	}
	service := &Service{config: config, runner: runner, storage: storage, reservations: make(map[string]reservation), ready: waitReady}
	entries, err := os.ReadDir(config.Root)
	if err != nil {
		return nil, fmt.Errorf("read reservation root: %w", err)
	}
	ports := map[int]bool{}
	names := map[string]bool{}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".reserve-") || (strings.HasPrefix(entry.Name(), ".deleted-") && runtimebroker.ValidID(strings.TrimPrefix(entry.Name(), ".deleted-"))) {
			if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
				return nil, errors.New("provisioner: unsafe abandoned reservation")
			}
			if err := storage.CheckUnmounted(context.Background(), filepath.Join(config.Root, entry.Name())); err != nil {
				return nil, fmt.Errorf("retain mounted abandoned reservation: %w", err)
			}
			if err := os.RemoveAll(filepath.Join(config.Root, entry.Name())); err != nil {
				return nil, fmt.Errorf("remove abandoned reservation: %w", err)
			}
			if err := syncDirectory(config.Root); err != nil {
				return nil, err
			}
			continue
		}
		if !runtimebroker.ValidID(entry.Name()) || !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return nil, errors.New("provisioner: unexpected private root entry")
		}
		directory := filepath.Join(config.Root, entry.Name())
		if err := privateDir(directory); err != nil {
			return nil, err
		}
		var record reservation
		if err := readPrivateJSON(filepath.Join(directory, "reservation.json"), &record); err != nil {
			return nil, err
		}
		if record.Version != 1 || record.ID != entry.Name() || !validName(record.Name) || record.Port < 19000 || record.Port >= 19000+7 || ports[record.Port] || names[record.Name] {
			return nil, errors.New("provisioner: invalid reservation")
		}
		ports[record.Port] = true
		names[record.Name] = true
		service.reservations[record.ID] = record
	}
	if len(service.reservations) > config.MaxGuests {
		return nil, ErrCapacity
	}
	return service, nil
}

// Ensure reserves capacity durably before creating any external resource.
func (s *Service) Ensure(ctx context.Context, id string) (Endpoint, error) {
	return s.ensure(ctx, id, false)
}

func (s *Service) ensure(ctx context.Context, id string, reservedOnly bool) (Endpoint, error) {
	if !runtimebroker.ValidID(id) {
		return Endpoint{}, errors.New("provisioner: invalid runtime ID")
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	if err := s.lock(ctx); err != nil {
		return Endpoint{}, err
	}
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return Endpoint{}, fmt.Errorf("ensure runtime: %w", err)
	}
	record, ok := s.reservations[id]
	if !ok {
		if reservedOnly {
			return Endpoint{}, nil
		}
		if len(s.reservations) >= s.config.MaxGuests {
			return Endpoint{}, ErrCapacity
		}
		name, err := randomHex(16)
		if err != nil {
			return Endpoint{}, err
		}
		port := 19000
		for _, other := range s.reservations {
			if other.Port == port {
				port++
				for {
					used := false
					for _, occupied := range s.reservations {
						if occupied.Port == port {
							used = true
							break
						}
					}
					if !used {
						break
					}
					port++
				}
			}
		}
		record = reservation{Version: 1, ID: id, Name: "ar-" + name, Port: port}
		if err := s.publishReservation(record); err != nil {
			// Rename may have succeeded before directory fsync failed. Keep that slot reserved.
			if _, statErr := os.Lstat(s.directory(record)); statErr == nil {
				s.reservations[id] = record
			}
			return Endpoint{}, err
		}
		s.reservations[id] = record
	}
	wasMounted, err := s.storage.Mounted(ctx, s.directory(record))
	if err != nil {
		return Endpoint{}, fmt.Errorf("inspect bounded storage: %w", err)
	}
	if !wasMounted && !record.RebindRequired {
		record.RebindRequired = true
		if err := writePrivateJSON(filepath.Join(s.directory(record), "reservation.json"), record); err != nil {
			return Endpoint{}, err
		}
		s.reservations[id] = record
	}
	if err := s.storage.Ensure(ctx, s.directory(record), s.config.StorageBytes, s.config.UID, s.config.GID); err != nil {
		return Endpoint{}, fmt.Errorf("ensure bounded storage: %w", err)
	}
	key, err := s.secret(record, "ankiconnect_key")
	if err != nil {
		return Endpoint{}, err
	}
	if _, err := s.secret(record, "vnc_password"); err != nil {
		return Endpoint{}, err
	}
	if err := s.ensureResources(ctx, record, record.RebindRequired); err != nil {
		return Endpoint{}, err
	}
	if record.RebindRequired {
		record.RebindRequired = false
		if err := writePrivateJSON(filepath.Join(s.directory(record), "reservation.json"), record); err != nil {
			return Endpoint{}, err
		}
		s.reservations[id] = record
	}
	endpoint := Endpoint{URL: "http://127.0.0.1:" + strconv.Itoa(record.Port), Key: key}
	readyCtx, cancel := context.WithTimeout(ctx, s.config.ReadyTimeout)
	defer cancel()
	if err := s.ready(readyCtx, endpoint); err != nil {
		return Endpoint{}, fmt.Errorf("runtime not ready: %w", err)
	}
	return endpoint, nil
}

// Reconcile recovers the bounded durable guest set without allocating new reservations.
// Each attempt uses Ensure's mount and ownership gates and a finite per-runtime deadline.
func (s *Service) Reconcile(ctx context.Context) error {
	if err := s.lock(ctx); err != nil {
		return err
	}
	ids := make([]string, 0, len(s.reservations))
	for id := range s.reservations {
		ids = append(ids, id)
	}
	s.mu.Unlock()
	sort.Strings(ids)
	failed, manual := 0, 0
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("reconcile runtimes: %w", err)
		}
		if _, err := s.ensure(ctx, id, true); err != nil {
			failed++
			if errors.Is(err, ErrManualRecovery) {
				manual++
			}
		}
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("reconcile runtimes: %w", err)
		}
	}
	if failed > 0 {
		return fmt.Errorf("reconcile managed runtimes: %d unavailable, %d require manual recovery", failed, manual)
	}
	return nil
}

func (s *Service) lock(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("wait for runtime mutation: %w", err)
		}
		if s.mu.TryLock() {
			return nil
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("wait for runtime mutation: %w", ctx.Err())
		case <-timer.C:
		}
	}
}

func (s *Service) directory(record reservation) string {
	return filepath.Join(s.config.Root, record.ID)
}

// Remove validates every resource before deleting any of them, never owner resources.
func (s *Service) Remove(ctx context.Context, id string) error {
	if !runtimebroker.ValidID(id) {
		return errors.New("provisioner: invalid runtime ID")
	}
	if err := s.lock(ctx); err != nil {
		return err
	}
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("remove runtime: %w", err)
	}
	record, ok := s.reservations[id]
	if !ok {
		return nil
	}
	tombstone := filepath.Join(s.config.Root, ".deleted-"+id)
	if _, err := os.Lstat(tombstone); err == nil { // #nosec G703 -- id has already passed the exact lowercase hexadecimal guest ID check.
		if err := s.clearTombstone(tombstone); err != nil {
			return err
		}
		delete(s.reservations, id)
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat runtime tombstone: %w", err)
	}
	kinds := []string{"container", "volume", "network"}
	existing := make(map[string]bool)
	for _, kind := range kinds {
		exists, err := s.owned(ctx, kind, record)
		if err != nil {
			return err
		}
		existing[kind] = exists
	}
	for _, kind := range kinds {
		if !existing[kind] {
			continue
		}
		args := []string{kind, "rm"}
		if kind == "container" {
			args = append(args, "--force")
		}
		args = append(args, record.Name)
		if _, err := s.docker(ctx, args...); err != nil {
			return err
		}
	}
	if err := s.storage.Remove(ctx, s.directory(record)); err != nil {
		return fmt.Errorf("remove bounded storage: %w", err)
	}
	if err := s.storage.CheckUnmounted(ctx, s.directory(record)); err != nil {
		return fmt.Errorf("retain mounted runtime directory: %w", err)
	}
	if err := os.Rename(s.directory(record), tombstone); err != nil { // #nosec G703 -- both paths contain only the previously validated opaque guest ID.
		return fmt.Errorf("publish removed reservation: %w", err)
	}
	if err := syncDirectory(s.config.Root); err != nil {
		return err
	}
	if err := s.clearTombstone(tombstone); err != nil {
		return err
	}
	delete(s.reservations, id)
	return nil
}

func (s *Service) secret(record reservation, name string) (string, error) {
	path := filepath.Join(s.directory(record), name)
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() != 64 {
			return "", errors.New("provisioner: invalid secret file")
		}
		data, err := os.ReadFile(path) // #nosec G304 -- fixed secret basename under a private daemon reservation.
		if err != nil {
			return "", fmt.Errorf("read runtime secret: %w", err)
		}
		value := string(data)
		if _, err := hex.DecodeString(value); err != nil {
			return "", errors.New("provisioner: invalid secret encoding")
		}
		return value, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("stat runtime secret: %w", err)
	}
	value, err := randomHex(32)
	if err != nil {
		return "", err
	}
	if err := atomicPrivateFile(path, []byte(value), s.config.UID, s.config.GID); err != nil {
		return "", err
	}
	return value, nil
}

// ServeHTTP exposes only two body-free operations and suppresses private diagnostics.
func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	const prefix = "/v1/runtimes/"
	if !strings.HasPrefix(r.URL.Path, prefix) || !runtimebroker.ValidID(strings.TrimPrefix(r.URL.Path, prefix)) || r.URL.RawQuery != "" {
		http.Error(w, "invalid runtime ID", http.StatusBadRequest)
		return
	}
	if r.Method != http.MethodPost && r.Method != http.MethodDelete {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, 1))
	if err != nil || len(data) != 0 {
		http.Error(w, "request body is not allowed", http.StatusBadRequest)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, prefix)
	if r.Method == http.MethodDelete {
		if err := s.Remove(r.Context(), id); err != nil {
			http.Error(w, "runtime unavailable", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	endpoint, err := s.Ensure(r.Context(), id)
	if err != nil {
		status := http.StatusServiceUnavailable
		if errors.Is(err, ErrCapacity) {
			status = http.StatusConflict
		}
		http.Error(w, "runtime unavailable", status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(endpoint)
}
