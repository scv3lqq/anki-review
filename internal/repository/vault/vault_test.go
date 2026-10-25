package vault

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/scv3lqq/anki-review/internal/domain"
)

const (
	testUser     = "owner"
	testPassword = "correct horse battery"
)

var testSecrets = domain.Secrets{ClaudeToken: "sk-ant-oat01-test", AnkiKey: "anki-key"}

// fastVault seals with the cheapest cost the bounds allow, so tests stay quick under -race.
func fastVault(t *testing.T) *Vault {
	t.Helper()
	v, err := New(filepath.Join(t.TempDir(), "data"), WithCost(1, 8, 1))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func sealed(t *testing.T) *Vault {
	t.Helper()
	v := fastVault(t)
	if err := v.Seal(testUser, testPassword, testSecrets); err != nil {
		t.Fatal(err)
	}
	return v
}

func readFile(t *testing.T, v *Vault) file {
	t.Helper()
	raw, err := os.ReadFile(v.path)
	if err != nil {
		t.Fatal(err)
	}
	var f file
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	return f
}

func writeFile(t *testing.T, v *Vault, f file) {
	t.Helper()
	raw, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(v.path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

// flip changes the first byte of a base64 value and keeps its length.
func flip(s string) string {
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil || len(raw) == 0 {
		return s
	}
	raw[0] ^= 0x01
	return base64.StdEncoding.EncodeToString(raw)
}

func TestRoundTrip(t *testing.T) {
	v := sealed(t)
	exists, err := v.Exists()
	if err != nil || !exists {
		t.Fatalf("Exists() = %v, %v; want true", exists, err)
	}
	got, err := v.Open(testUser, testPassword)
	if err != nil {
		t.Fatalf("Open() = %v", err)
	}
	if got != testSecrets {
		t.Errorf("Open() = %+v, want %+v", got, testSecrets)
	}
}

func TestExistsBeforeSeal(t *testing.T) {
	exists, err := fastVault(t).Exists()
	if err != nil || exists {
		t.Errorf("Exists() = %v, %v; want false", exists, err)
	}
}

func TestOpenRejectsWrongCredentialsAndTampering(t *testing.T) {
	tests := []struct {
		name   string
		user   string
		pass   string
		mangle func(f *file)
	}{
		{name: "wrong password", user: testUser, pass: "wrong horse battery"},
		{name: "wrong user", user: "someone", pass: testPassword},
		{name: "empty password", user: testUser, pass: ""},
		{name: "tampered sealed", user: testUser, pass: testPassword, mangle: func(f *file) { f.Sealed = flip(f.Sealed) }},
		{name: "tampered nonce", user: testUser, pass: testPassword, mangle: func(f *file) { f.Nonce = flip(f.Nonce) }},
		{name: "tampered salt", user: testUser, pass: testPassword, mangle: func(f *file) { f.KDF.Salt = flip(f.KDF.Salt) }},
		{name: "tampered user", user: "owner2", pass: testPassword, mangle: func(f *file) { f.User = "owner2" }},
		{name: "tampered cost", user: testUser, pass: testPassword, mangle: func(f *file) { f.KDF.Time = 2 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := sealed(t)
			if tt.mangle != nil {
				f := readFile(t, v)
				tt.mangle(&f)
				writeFile(t, v, f)
			}
			_, err := v.Open(tt.user, tt.pass)
			if !errors.Is(err, domain.ErrCredentials) {
				t.Errorf("Open() = %v, want ErrCredentials", err)
			}
		})
	}
}

func TestOpenDamaged(t *testing.T) {
	tests := []struct {
		name   string
		mangle func(f *file)
	}{
		{name: "unknown version", mangle: func(f *file) { f.Version = 2 }},
		{name: "unknown KDF", mangle: func(f *file) { f.KDF.Name = "scrypt" }},
		{name: "time zero", mangle: func(f *file) { f.KDF.Time = 0 }},
		{name: "time too high", mangle: func(f *file) { f.KDF.Time = 11 }},
		{name: "memory too small", mangle: func(f *file) { f.KDF.MemoryKiB = 4 }},
		{name: "memory too large", mangle: func(f *file) { f.KDF.MemoryKiB = 2 << 20 }},
		{name: "threads zero", mangle: func(f *file) { f.KDF.Threads = 0 }},
		{name: "threads too many", mangle: func(f *file) { f.KDF.Threads = 17 }},
		{name: "short salt", mangle: func(f *file) { f.KDF.Salt = base64.StdEncoding.EncodeToString([]byte("short")) }},
		{name: "short nonce", mangle: func(f *file) { f.Nonce = base64.StdEncoding.EncodeToString([]byte("short")) }},
		{name: "bad base64", mangle: func(f *file) { f.Sealed = "not base64!" }},
		{name: "no user", mangle: func(f *file) { f.User = "" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := sealed(t)
			f := readFile(t, v)
			tt.mangle(&f)
			writeFile(t, v, f)
			_, err := v.Open(testUser, testPassword)
			if !errors.Is(err, ErrDamaged) {
				t.Fatalf("Open() = %v, want ErrDamaged", err)
			}
			if !strings.Contains(err.Error(), "vault.json") {
				t.Errorf("Open() = %v, want the file named", err)
			}
		})
	}
}

func TestOpenGarbageFile(t *testing.T) {
	v := fastVault(t)
	if err := os.MkdirAll(filepath.Dir(v.path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(v.path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Open(testUser, testPassword); !errors.Is(err, ErrDamaged) {
		t.Errorf("Open() = %v, want ErrDamaged", err)
	}
}

func TestOpenMissingFile(t *testing.T) {
	if _, err := fastVault(t).Open(testUser, testPassword); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Open() on no file = %v, want fs.ErrNotExist", err)
	}
}

func TestFileModes(t *testing.T) {
	v := sealed(t)
	info, err := os.Stat(v.path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("vault.json: %v, mode %v; want 0600", err, info.Mode().Perm())
	}
	dirInfo, err := os.Stat(filepath.Dir(v.path))
	if err != nil || dirInfo.Mode().Perm() != 0o700 {
		t.Errorf("data dir: %v, mode %v; want 0700", err, dirInfo.Mode().Perm())
	}
}

func TestSealReplacesAtomically(t *testing.T) {
	v := sealed(t)
	next := domain.Secrets{ClaudeToken: "sk-ant-api03-next", AnkiKey: ""}
	if err := v.Seal("owner2", "another horse battery", next); err != nil {
		t.Fatal(err)
	}
	got, err := v.Open("owner2", "another horse battery")
	if err != nil || !reflect.DeepEqual(got, next) {
		t.Fatalf("Open() after reseal = %+v, %v; want %+v", got, err, next)
	}
	entries, err := os.ReadDir(filepath.Dir(v.path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != fileName {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("data dir holds %v, want only %s", names, fileName)
	}
}

func TestSealDrawsFreshSaltAndNonce(t *testing.T) {
	v := sealed(t)
	first := readFile(t, v)
	if err := v.Seal(testUser, testPassword, testSecrets); err != nil {
		t.Fatal(err)
	}
	second := readFile(t, v)
	if first.KDF.Salt == second.KDF.Salt || first.Nonce == second.Nonce {
		t.Error("two seals share a salt or a nonce")
	}
}

func TestNewRejectsCost(t *testing.T) {
	tests := []struct {
		name   string
		time   uint32
		memory uint32
		thread uint8
	}{
		{name: "time zero", time: 0, memory: 8, thread: 1},
		{name: "memory too small", time: 1, memory: 4, thread: 1},
		{name: "threads zero", time: 1, memory: 8, thread: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := New(t.TempDir(), WithCost(tt.time, tt.memory, tt.thread)); err == nil {
				t.Error("New() accepted a cost outside the bounds")
			}
		})
	}
}
