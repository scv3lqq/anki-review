package vault

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/scv3lqq/anki-review/internal/domain"
)

func recoveryKey(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "session.key")
	if err := os.WriteFile(path, bytes.Repeat([]byte{0x37}, 32), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func recoveryVault(t *testing.T, dir, key, account string) *Vault {
	t.Helper()
	v, err := New(dir, WithCost(1, 8, 1), WithSessionKey(key, account))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(v.Close)
	return v
}

func recoverySessions() []domain.AuthSession {
	digest := sha256.Sum256([]byte("synthetic-browser-session"))
	return []domain.AuthSession{{Hash: hex.EncodeToString(digest[:]), User: testUser,
		Expires: time.Date(2027, 1, 7, 12, 34, 56, 0, time.UTC)}}
}

func TestRecoveryRoundTripAndRefreshedCredentials(t *testing.T) {
	dir, key := t.TempDir(), recoveryKey(t)
	v := recoveryVault(t, dir, key, "account-a")
	sessions := recoverySessions()
	secrets := domain.Secrets{ClaudeToken: "sk-ant-synthetic-private", AnkiKey: "synthetic-anki-private", GraderProvider: "codex", CodexAuth: "synthetic-codex-private"}
	if err := v.SealSessions(testUser, testPassword, secrets, sessions); err != nil {
		t.Fatal(err)
	}
	v.Close()
	restarted := recoveryVault(t, dir, key, "account-a")
	got, err := restarted.LoadSessions()
	if err != nil || !reflect.DeepEqual(got, sessions) {
		t.Fatalf("LoadSessions() = %+v, %v; want original sessions", got, err)
	}
	if err := restarted.Reseal(secrets); !errors.Is(err, ErrLocked) {
		t.Fatalf("loading metadata unlocked vault: Reseal() = %v", err)
	}
	if got, err := restarted.Recover(); err != nil || got != secrets {
		t.Fatalf("Recover() did not restore credentials: %v", err)
	}
	secrets.CodexAuth = "synthetic-refreshed-codex-private"
	if err := restarted.Reseal(secrets); err != nil {
		t.Fatal(err)
	}
	restarted.Close()
	third := recoveryVault(t, dir, key, "account-a")
	if got, err := third.Recover(); err != nil || got != secrets {
		t.Fatalf("refresh was lost after recovery: %v", err)
	}
	if got, err := third.LoadSessions(); err != nil || !reflect.DeepEqual(got, sessions) {
		t.Fatalf("refresh changed original session records: %+v, %v", got, err)
	}
	if got, err := third.Open(testUser, testPassword); err != nil || got != secrets {
		t.Fatalf("recovery refresh broke password access: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, fileName))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{testPassword, secrets.ClaudeToken, secrets.AnkiKey, secrets.CodexAuth, "synthetic-browser-session", sessions[0].Hash} {
		if bytes.Contains(raw, []byte(secret)) {
			t.Fatal("vault contains unencrypted private material")
		}
	}
}

func TestRecoveryRejectsUnsafeKey(t *testing.T) {
	for _, name := range []string{"missing", "short", "long", "world-readable", "group-writable", "executable", "symlink", "directory", "relative", "unsafe-parent", "inside-data"} {
		t.Run(name, func(t *testing.T) {
			dir, key := t.TempDir(), recoveryKey(t)
			switch name {
			case "missing":
				if err := os.Remove(key); err != nil {
					t.Fatal(err)
				}
			case "short", "long":
				n := 31
				if name == "long" {
					n = 33
				}
				if err := os.WriteFile(key, bytes.Repeat([]byte{1}, n), 0o600); err != nil {
					t.Fatal(err)
				}
			case "world-readable", "group-writable", "executable":
				mode := os.FileMode(0o604)
				if name == "group-writable" {
					mode = 0o660
				}
				if name == "executable" {
					mode = 0o700
				}
				if err := os.Chmod(key, mode); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				link := filepath.Join(t.TempDir(), "link")
				if err := os.Symlink(key, link); err != nil {
					t.Fatal(err)
				}
				key = link
			case "directory":
				key = t.TempDir()
			case "relative":
				key = "session.key"
			case "unsafe-parent":
				if err := os.Chmod(filepath.Dir(key), 0o777); err != nil {
					t.Fatal(err)
				}
			case "inside-data":
				key = filepath.Join(dir, "session.key")
				if err := os.WriteFile(key, bytes.Repeat([]byte{1}, 32), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			v, err := New(dir, WithCost(1, 8, 1), WithSessionKey(key, "account-a"))
			if err == nil {
				v.Close()
				t.Fatal("unsafe configured key accepted")
			}
		})
	}
}

func TestRecoveryAcceptsProtectedGroupReadableKey(t *testing.T) {
	key := recoveryKey(t)
	if err := os.Chmod(key, 0o640); err != nil {
		t.Fatal(err)
	}
	if !recoveryVault(t, t.TempDir(), key, "account-a").PersistenceEnabled() {
		t.Fatal("protected group-readable key disabled recovery")
	}
}

func TestRecoveryAuthenticatesEnvelopeBinding(t *testing.T) {
	for _, name := range []string{"wrong-key", "wrong-account", "changed-login", "changed-kdf", "changed-salt", "corrupt-envelope", "stale-envelope"} {
		t.Run(name, func(t *testing.T) {
			dir, key := t.TempDir(), recoveryKey(t)
			v := recoveryVault(t, dir, key, "account-a")
			if err := v.SealSessions(testUser, testPassword, testSecrets, recoverySessions()); err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(v.path)
			if err != nil {
				t.Fatal(err)
			}
			var envelope map[string]json.RawMessage
			if err := json.Unmarshal(raw, &envelope); err != nil {
				t.Fatal(err)
			}
			account := "account-a"
			switch name {
			case "wrong-key":
				key = filepath.Join(t.TempDir(), "other.key")
				if err := os.WriteFile(key, bytes.Repeat([]byte{0x71}, 32), 0o600); err != nil {
					t.Fatal(err)
				}
			case "wrong-account":
				account = "account-b"
			case "changed-login":
				envelope["user"] = json.RawMessage(`"other-owner"`)
			case "changed-kdf", "changed-salt":
				var kdf kdfParams
				if err := json.Unmarshal(envelope["kdf"], &kdf); err != nil {
					t.Fatal(err)
				}
				if name == "changed-kdf" {
					kdf.Time++
				} else {
					kdf.Salt = flip(kdf.Salt)
				}
				envelope["kdf"], err = json.Marshal(kdf)
				if err != nil {
					t.Fatal(err)
				}
			case "corrupt-envelope":
				envelope["recovery"] = json.RawMessage(`{"nonce":"invalid","sealed":"invalid"}`)
			case "stale-envelope":
				stale := append(json.RawMessage(nil), envelope["recovery"]...)
				if len(stale) == 0 {
					t.Fatal("missing recovery envelope")
				}
				if err := v.SealSessions(testUser, "new synthetic password", testSecrets, recoverySessions()); err != nil {
					t.Fatal(err)
				}
				raw, err := os.ReadFile(v.path)
				if err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(raw, &envelope); err != nil {
					t.Fatal(err)
				}
				envelope["recovery"] = stale
			}
			raw, err = json.Marshal(envelope)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(v.path, raw, 0o600); err != nil {
				t.Fatal(err)
			}
			v.Close()
			restarted := recoveryVault(t, dir, key, account)
			if _, err := restarted.LoadSessions(); err == nil {
				t.Error("unauthenticated session metadata accepted")
			}
			if _, err := restarted.Recover(); err == nil {
				t.Error("unauthenticated recovery accepted")
			}
			if err := restarted.Reseal(testSecrets); !errors.Is(err, ErrLocked) {
				t.Errorf("failed recovery installed key: %v", err)
			}
		})
	}
}

func TestRecoveryLegacyMigrationAndDisabledMode(t *testing.T) {
	dir, key := t.TempDir(), recoveryKey(t)
	legacy, err := New(dir, WithCost(1, 8, 1))
	if err != nil {
		t.Fatal(err)
	}
	if err := legacy.Seal(testUser, testPassword, testSecrets); err != nil {
		t.Fatal(err)
	}
	legacy.Close()
	v := recoveryVault(t, dir, key, "account-a")
	if sessions, err := v.LoadSessions(); err != nil || len(sessions) != 0 {
		t.Fatalf("legacy migration state = %+v, %v", sessions, err)
	}
	if _, err := v.Recover(); err == nil {
		t.Fatal("legacy vault recovered without password")
	}
	if _, err := v.Open(testUser, testPassword); err != nil {
		t.Fatal(err)
	}
	if err := v.SaveSessions(recoverySessions()); err != nil {
		t.Fatal(err)
	}
	v.Close()
	if got, err := recoveryVault(t, dir, key, "account-a").Recover(); err != nil || got != testSecrets {
		t.Fatalf("password migration did not enable recovery: %v", err)
	}
	if disabled, err := New(dir, WithCost(1, 8, 1)); err == nil {
		disabled.Close()
		t.Fatal("recovery-bearing vault started without key, permitting session resurrection")
	}
	if _, err := recoveryVault(t, dir, key, "account-a").Recover(); err != nil {
		t.Fatalf("refused key removal changed recovery: %v", err)
	}
}

func TestRecoveryDisabledLegacyModeHasNoPersistentSessions(t *testing.T) {
	v := sealed(t)
	t.Cleanup(v.Close)
	if v.PersistenceEnabled() {
		t.Fatal("unset key enabled persistence")
	}
	if sessions, err := v.LoadSessions(); err != nil || len(sessions) != 0 {
		t.Fatalf("disabled mode loaded sessions: %+v, %v", sessions, err)
	}
	if _, err := v.Recover(); !errors.Is(err, ErrLocked) {
		t.Fatalf("disabled recovery = %v, want locked", err)
	}
	if _, err := v.Open(testUser, testPassword); err != nil {
		t.Fatal(err)
	}
}

func TestRecoveryWrongKeyPreservesVaultAndGuidance(t *testing.T) {
	dir := t.TempDir()
	v := recoveryVault(t, dir, recoveryKey(t), "account-a")
	if err := v.SealSessions(testUser, testPassword, testSecrets, recoverySessions()); err != nil {
		t.Fatal(err)
	}
	v.Close()
	before, err := os.ReadFile(v.path)
	if err != nil {
		t.Fatal(err)
	}
	wrongKey := filepath.Join(t.TempDir(), "wrong-session.key")
	if err := os.WriteFile(wrongKey, bytes.Repeat([]byte{0x71}, 32), 0o600); err != nil {
		t.Fatal(err)
	}
	restarted := recoveryVault(t, dir, wrongKey, "account-a")
	for _, operation := range []struct {
		name string
		run  func() error
	}{
		{name: "load sessions", run: func() error { _, err := restarted.LoadSessions(); return err }},
		{name: "recover credentials", run: func() error { _, err := restarted.Recover(); return err }},
	} {
		t.Run(operation.name, func(t *testing.T) {
			err := operation.run()
			if !errors.Is(err, ErrDamaged) {
				t.Fatalf("wrong recovery key = %v, want damaged", err)
			}
			if strings.Contains(err.Error(), "delete") || strings.Contains(err.Error(), "remove") {
				t.Error("wrong-key diagnostic recommends destructive vault deletion")
			}
			if !strings.HasSuffix(err.Error(), "; preserve the vault and follow the documented recovery procedure") {
				t.Errorf("wrong-key diagnostic lacks preservation advice: %v", err)
			}
			after, err := os.ReadFile(v.path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(after, before) {
				t.Fatal("wrong recovery key changed the vault bytes")
			}
		})
	}
}

func TestRecoveryDamagedPreservationGuidance(t *testing.T) {
	for _, tc := range []struct {
		name string
		path string
		want string
	}{
		{name: "without path", want: "vault damaged: synthetic cause; preserve the vault and follow the documented recovery procedure"},
		{name: "with path", path: "synthetic/vault.json", want: "vault damaged: synthetic/vault.json: synthetic cause; preserve the vault and follow the documented recovery procedure"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := damaged(tc.path, "synthetic cause")
			if !errors.Is(err, ErrDamaged) {
				t.Fatal("preservation guidance lost ErrDamaged identity")
			}
			if err.Error() != tc.want {
				t.Errorf("damaged() = %q, want %q", err.Error(), tc.want)
			}
		})
	}
}
