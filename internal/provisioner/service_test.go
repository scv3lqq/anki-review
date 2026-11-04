package provisioner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const accountA = "0123456789abcdef0123456789abcdef"
const accountB = "abcdef0123456789abcdef0123456789"

type fakeDocker struct {
	mu          sync.Mutex
	resources   map[string]map[string]any
	calls       [][]string
	failRun     bool
	failStop    bool
	foreign     bool
	beforeStart func() error
}

func (d *fakeDocker) Run(_ context.Context, program string, args ...string) ([]byte, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if program != "docker" {
		return nil, fmt.Errorf("unexpected command %s", program)
	}
	d.calls = append(d.calls, append([]string{}, args...))
	if len(args) < 2 {
		return nil, errors.New("short command")
	}
	kind, operation := args[0], args[1]
	if operation == "ls" {
		var names []string
		for key, resource := range d.resources {
			if resource["Kind"] == kind {
				names = append(names, strings.TrimPrefix(key, kind+"/"))
			}
		}
		return []byte(strings.Join(names, "\n")), nil
	}
	if operation == "inspect" {
		resource, ok := d.resources[kind+"/"+args[2]]
		if !ok {
			return nil, errors.New("missing")
		}
		data, err := json.Marshal([]map[string]any{resource})
		return data, err
	}
	if operation == "rm" {
		delete(d.resources, kind+"/"+args[len(args)-1])
		return nil, nil
	}
	if kind == "container" && operation == "start" {
		if d.beforeStart != nil {
			if err := d.beforeStart(); err != nil {
				return nil, err
			}
		}
		return nil, nil
	}
	if kind == "container" && operation == "stop" && d.failStop {
		d.failStop = false
		return nil, errors.New("injected stop failure")
	}
	if kind == "container" && (operation == "update" || operation == "stop") {
		return nil, nil
	}
	if operation == "create" {
		if kind == "container" && d.failRun {
			d.failRun = false
			return nil, errors.New("injected container failure")
		}
		labels := map[string]string{}
		name := args[len(args)-1]
		for i := 2; i < len(args)-1; i++ {
			if args[i] == "--label" {
				pair := strings.SplitN(args[i+1], "=", 2)
				labels[pair[0]] = pair[1]
			}
			if args[i] == "--name" {
				name = args[i+1]
			}
		}
		if d.foreign {
			labels[managedLabel] = "other"
		}
		resource := map[string]any{"Kind": kind, "Labels": labels, "Config": map[string]any{"Labels": labels}}
		if kind == "volume" {
			options := map[string]string{}
			for i, arg := range args {
				if arg == "--opt" {
					pair := strings.SplitN(args[i+1], "=", 2)
					options[pair[0]] = pair[1]
				}
			}
			resource["Driver"] = "local"
			resource["Options"] = options
		}
		d.resources[kind+"/"+name] = resource
		return nil, nil
	}
	return nil, fmt.Errorf("unexpected args %v", args)
}

type fakeStorage struct {
	ensures, removes int
	failure          error
	mounted          bool
	beforeEnsure     func() error
}

func (s *fakeStorage) Ensure(_ context.Context, _ string, _ int64, _, _ int) error {
	s.ensures++
	if s.beforeEnsure != nil {
		return s.beforeEnsure()
	}
	return s.failure
}
func (s *fakeStorage) Remove(_ context.Context, _ string) error { s.removes++; return s.failure }

func (s *fakeStorage) CheckUnmounted(context.Context, string) error {
	if s.mounted {
		return errors.New("live mount")
	}
	return nil
}

func (s *fakeStorage) Mounted(context.Context, string) (bool, error) { return s.mounted, nil }

func newFixture(t *testing.T, maxGuests int) (*Service, *fakeDocker, *fakeStorage, Config) {
	t.Helper()
	config := Config{Root: filepath.Join(t.TempDir(), "private"), MaxGuests: maxGuests, StorageBytes: 2 << 30, UID: os.Getuid(), GID: os.Getgid(), ReadyTimeout: time.Millisecond}
	docker := &fakeDocker{resources: map[string]map[string]any{}}
	storage := &fakeStorage{}
	service, err := New(config, docker, storage)
	if err != nil {
		t.Fatal(err)
	}
	service.ready = func(context.Context, Endpoint) error { return nil }
	return service, docker, storage, config
}

func TestStrictIDsAndIndependentGuestCapacity(t *testing.T) {
	service, docker, _, _ := newFixture(t, 1)
	for _, id := range []string{"owner", "../owner", accountA + "/x", strings.ToUpper(accountA), ""} {
		if _, err := service.Ensure(context.Background(), id); err == nil {
			t.Errorf("accepted %q", id)
		}
		if err := service.Remove(context.Background(), id); err == nil {
			t.Errorf("removed %q", id)
		}
	}
	if len(docker.calls) != 0 {
		t.Fatal("invalid ID reached docker")
	}
	if _, err := service.Ensure(context.Background(), accountA); err != nil {
		t.Fatal(err)
	}
	count := len(docker.calls)
	if _, err := service.Ensure(context.Background(), accountB); !errors.Is(err, ErrCapacity) {
		t.Fatalf("capacity: %v", err)
	}
	if len(docker.calls) != count {
		t.Fatal("over-capacity request reached docker")
	}
}

func TestReservationSurvivesFailureAndRestart(t *testing.T) {
	service, docker, storage, config := newFixture(t, 1)
	docker.failRun = true
	if _, err := service.Ensure(context.Background(), accountA); err == nil {
		t.Fatal("expected failure")
	}
	files, err := os.ReadDir(config.Root)
	if err != nil || len(files) != 1 {
		t.Fatalf("reservation files: %v %v", files, err)
	}
	service, err = New(config, docker, storage)
	if err != nil {
		t.Fatal(err)
	}
	service.ready = func(context.Context, Endpoint) error { return nil }
	first, err := service.Ensure(context.Background(), accountA)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Ensure(context.Background(), accountA)
	if err != nil || first != second {
		t.Fatalf("idempotency: %+v %+v %v", first, second, err)
	}
	if storage.ensures < 3 {
		t.Fatal("storage identity must be rechecked on every ensure")
	}
	if len(docker.resources) != 3 {
		t.Fatalf("resources = %d", len(docker.resources))
	}
	if err := service.Remove(context.Background(), accountA); err != nil {
		t.Fatal(err)
	}
	if err := service.Remove(context.Background(), accountA); err != nil {
		t.Fatal(err)
	}
	if len(docker.resources) != 0 || storage.removes != 1 {
		t.Fatal("cleanup not idempotent")
	}
}

func TestResourceIsolationAndSecretFiles(t *testing.T) {
	service, docker, _, config := newFixture(t, 1)
	endpoint, err := service.Ensure(context.Background(), accountA)
	if err != nil {
		t.Fatal(err)
	}
	var create []string
	for _, call := range docker.calls {
		if len(call) > 1 && call[0] == "container" && call[1] == "create" {
			create = call
		}
	}
	joined := strings.Join(create, " ")
	for _, required := range []string{Image, "--memory 2g", "--memory-swap 2g", "--cpus 1", "--pids-limit 1024", "--read-only", "--cap-drop ALL", "--security-opt no-new-privileges:true", "127.0.0.1:19000:8765", fmt.Sprintf("--user %d:%d", config.UID, config.GID), "/run/secrets/ankiconnect_key", "/run/secrets/vnc_password", "--tmpfs /tmp:size=64m", "--tmpfs /home/anki:size=256m", "--shm-size 256m"} {
		if !strings.Contains(joined, required) {
			t.Errorf("missing %q: %s", required, joined)
		}
	}
	if strings.Contains(joined, endpoint.Key) || strings.Contains(joined, "5900") || strings.Contains(joined, "8766") {
		t.Fatal("secret or unnecessary port in arguments")
	}
	if !strings.HasPrefix(endpoint.URL, "http://127.0.0.1:19000") || len(endpoint.Key) != 64 {
		t.Fatal("bad endpoint")
	}
	for _, filename := range []string{"ankiconnect_key", "vnc_password"} {
		info, err := os.Stat(filepath.Join(config.Root, accountA, filename))
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("secret permission: %v %v", info, err)
		}
	}
	for _, resource := range docker.resources {
		resource["Labels"] = map[string]string{managedLabel: "foreign"}
		resource["Config"] = map[string]any{"Labels": map[string]string{managedLabel: "foreign"}}
	}
	before := len(docker.resources)
	if err := service.Remove(context.Background(), accountA); err == nil {
		t.Fatal("deleted foreign resources")
	}
	if len(docker.resources) != before {
		t.Fatal("partial deletion of foreign resources")
	}
}

func TestReadinessRequiresVersionSix(t *testing.T) {
	for _, body := range []string{`{"result":5,"error":null}`, `{"result":6,"error":"bad key"}`, `{"result":6,"error":null}`} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var req map[string]any
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Error(err)
			}
			if req["action"] != "version" || req["key"] != "test-key" {
				t.Error(req)
			}
			_, _ = w.Write([]byte(body))
		}))
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		err := waitReady(ctx, Endpoint{URL: server.URL, Key: "test-key"})
		cancel()
		server.Close()
		if (err == nil) != (body == `{"result":6,"error":null}`) {
			t.Errorf("body=%s err=%v", body, err)
		}
	}
}

func TestQuotaFailsClosedAndHTTPRejectsBody(t *testing.T) {
	service, docker, storage, _ := newFixture(t, 1)
	storage.failure = errors.New("unsupported quota")
	if _, err := service.Ensure(context.Background(), accountA); err == nil {
		t.Fatal("quota failure accepted")
	}
	if len(docker.calls) != 0 {
		t.Fatal("docker reached before quota")
	}
	recorder := httptest.NewRecorder()
	service.ServeHTTP(recorder, httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/runtimes/"+accountA, strings.NewReader(`{"image":"evil"}`)))
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("body status %d", recorder.Code)
	}
}

func TestReservationPublicationAndRemovalCrashRecovery(t *testing.T) {
	service, docker, storage, config := newFixture(t, 2)
	if _, err := service.Ensure(context.Background(), accountA); err != nil {
		t.Fatal(err)
	}
	staging := filepath.Join(config.Root, ".reserve-abandoned")
	if err := os.Mkdir(staging, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staging, "reservation.json"), []byte("partial"), 0600); err != nil {
		t.Fatal(err)
	}
	service, err := New(config, docker, storage)
	if err != nil {
		t.Fatal(err)
	}
	service.ready = func(context.Context, Endpoint) error { return nil }
	if _, err := service.Ensure(context.Background(), accountB); err != nil {
		t.Fatal(err)
	}
	if err := service.Remove(context.Background(), accountA); err != nil {
		t.Fatal(err)
	}
	// A tombstone is published only after resources are removed and storage is verified unmounted.
	tombstone := filepath.Join(config.Root, ".deleted-"+accountA)
	if err := os.Mkdir(tombstone, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tombstone, "reservation.json"), []byte("partial removal"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(config, docker, storage); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(tombstone); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("tombstone remains: %v", err)
	}
}

func TestDaemonConfigBounds(t *testing.T) {
	for _, capacity := range []int{-1, 8} {
		config := Config{Root: filepath.Join(t.TempDir(), "private"), MaxGuests: capacity}
		if _, err := New(config, &fakeDocker{}, &fakeStorage{}); err == nil {
			t.Fatalf("accepted capacity %d", capacity)
		}
	}
}

func TestStartupRetainsMountedAbandonedReservation(t *testing.T) {
	_, docker, storage, config := newFixture(t, 1)
	tombstone := filepath.Join(config.Root, ".deleted-"+accountA)
	if err := os.Mkdir(tombstone, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(tombstone, "collection.anki2")
	if err := os.WriteFile(path, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	storage.mounted = true
	if _, err := New(config, docker, storage); err == nil {
		t.Fatal("accepted mounted tombstone")
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "preserve" {
		t.Fatalf("mounted data removed: %q %v", data, err)
	}
}

func TestConcurrentEnsuresShareOneDurableSlot(t *testing.T) {
	service, docker, _, config := newFixture(t, 1)
	results := make(chan Endpoint, 8)
	errorsFound := make(chan error, 8)
	var group sync.WaitGroup
	for range 8 {
		group.Go(func() {
			endpoint, err := service.Ensure(context.Background(), accountA)
			if err != nil {
				errorsFound <- err
				return
			}
			results <- endpoint
		})
	}
	group.Wait()
	close(results)
	close(errorsFound)
	for err := range errorsFound {
		t.Error(err)
	}
	var first Endpoint
	for endpoint := range results {
		if first.URL == "" {
			first = endpoint
		}
		if first != endpoint {
			t.Error("concurrent Ensure returned a different runtime")
		}
	}
	if first.URL == "" || len(docker.resources) != 3 {
		t.Fatal("missing or duplicated runtime")
	}
	entries, err := os.ReadDir(config.Root)
	if err != nil || len(entries) != 1 {
		t.Fatalf("durable reservations: %d %v", len(entries), err)
	}
}

func TestExistingVolumeCannotBypassFilesystemQuota(t *testing.T) {
	service, docker, _, _ := newFixture(t, 1)
	if _, err := service.Ensure(context.Background(), accountA); err != nil {
		t.Fatal(err)
	}
	for key, resource := range docker.resources {
		if strings.HasPrefix(key, "volume/") {
			resource["Options"] = map[string]string{"type": "none", "o": "bind", "device": "/some/owner/collection"}
		}
	}
	if _, err := service.Ensure(context.Background(), accountA); err == nil {
		t.Fatal("labelled volume escaped bounded filesystem")
	}
}

func TestReconcileRepairsRebootBindBeforeStartingManagedGuest(t *testing.T) {
	service, docker, storage, config := newFixture(t, 1)
	storage.beforeEnsure = func() error { storage.mounted = true; return nil }
	if _, err := service.Ensure(context.Background(), accountA); err != nil {
		t.Fatal(err)
	}
	for _, call := range docker.calls {
		if len(call) > 1 && call[0] == "container" && call[1] == "create" && !strings.Contains(strings.Join(call, " "), "--restart no") {
			t.Fatalf("unsafe Docker restart policy: %v", call)
		}
	}
	owner := map[string]any{"Kind": "container", "Config": map[string]any{"Labels": map[string]string{managedLabel: "owner"}}}
	docker.resources["container/anki-desktop"] = owner
	service, err := New(config, docker, storage)
	if err != nil {
		t.Fatal(err)
	}
	service.ready = func(context.Context, Endpoint) error { return nil }
	storage.mounted = false
	var order []string
	storage.beforeEnsure = func() error { order = append(order, "mount-verified"); storage.mounted = true; return nil }
	docker.beforeStart = func() error {
		if !storage.mounted {
			return errors.New("start preceded quota mount")
		}
		order = append(order, "start")
		return nil
	}
	docker.calls = nil
	if err := service.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if strings.Join(order, ",") != "mount-verified,start" {
		t.Fatalf("reboot recovery order: %v", order)
	}
	operations := []string{}
	for _, call := range docker.calls {
		if call[0] == "container" && call[1] != "ls" && call[1] != "inspect" {
			operations = append(operations, call[1])
		}
	}
	if strings.Join(operations, ",") != "update,stop,rm,create,start" {
		t.Fatalf("stale bind recovery operations: %v", operations)
	}
	if _, ok := docker.resources["container/anki-desktop"]; !ok {
		t.Fatal("reconcile deleted owner")
	}
	docker.calls = nil
	if err := service.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, call := range docker.calls {
		if call[0] == "container" && (call[1] == "stop" || call[1] == "rm" || call[1] == "create") {
			t.Fatalf("healthy reconciliation recreated runtime: %v", call)
		}
	}
	storage.mounted = false
	storage.beforeEnsure = func() error { return errors.New("private mount detail or secret") }
	docker.calls = nil
	err = service.Reconcile(context.Background())
	if err == nil || strings.Contains(err.Error(), "private mount detail") {
		t.Fatalf("unsanitized reconciliation failure: %v", err)
	}
	for _, call := range docker.calls {
		if call[0] == "container" && call[1] == "start" {
			t.Fatal("started runtime after quota failure")
		}
	}
}

func TestReconcileHonorsCancellation(t *testing.T) {
	service, _, _, _ := newFixture(t, 1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := service.Reconcile(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation = %v", err)
	}
	if _, err := service.Ensure(context.Background(), accountA); err != nil {
		t.Fatal(err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	service.ready = func(ctx context.Context, _ Endpoint) error { cancel(); return ctx.Err() }
	if err := service.Reconcile(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation during runtime = %v", err)
	}
}

func TestReconcileKeepsDurableRebindAfterInterruptedRepair(t *testing.T) {
	service, docker, storage, config := newFixture(t, 1)
	storage.beforeEnsure = func() error { storage.mounted = true; return nil }
	if _, err := service.Ensure(context.Background(), accountA); err != nil {
		t.Fatal(err)
	}
	storage.mounted = false
	docker.failStop = true
	if err := service.Reconcile(context.Background()); err == nil {
		t.Fatal("repair stop failure was hidden")
	}
	var record reservation
	if err := readPrivateJSON(filepath.Join(config.Root, accountA, "reservation.json"), &record); err != nil || !record.RebindRequired {
		t.Fatalf("repair flag was lost: %+v %v", record, err)
	}
	service, err := New(config, docker, storage)
	if err != nil {
		t.Fatal(err)
	}
	service.ready = func(context.Context, Endpoint) error { return nil }
	docker.calls = nil
	if err := service.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	recreated := false
	for _, call := range docker.calls {
		if call[0] == "container" && call[1] == "create" {
			recreated = true
		}
	}
	if !recreated {
		t.Fatal("restart forgot the stale bind repair")
	}
}

func TestRuntimeMutationWaitHonorsDeadline(t *testing.T) {
	service, _, _, _ := newFixture(t, 1)
	service.mu.Lock()
	defer service.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := service.Ensure(ctx, accountA); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("bounded mutex wait: %v", err)
	}
}
