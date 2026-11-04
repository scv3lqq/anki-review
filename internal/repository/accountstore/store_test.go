package accountstore

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/scv3lqq/anki-review/internal/usecase/accounts"
)

func TestPrivateDurableRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	empty, err := s.Load()
	if err != nil || empty.Version != 1 {
		t.Fatalf("empty registry: %+v, %v", empty, err)
	}
	want := accounts.State{Version: 1, Accounts: []accounts.Record{{ID: "owner", User: "root"}}}
	if err := s.Save(want); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := reopened.Load()
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip %+v, %v", got, err)
	}
	for path, mode := range map[string]os.FileMode{dir: 0o700, filepath.Join(dir, "accounts.json"): 0o600} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != mode {
			t.Fatalf("private mode %s: %v, %v", path, info, err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("temporary file leaked: %v, %v", entries, err)
	}
}

func TestRejectMalformedOversizedAndUnknownVersion(t *testing.T) {
	for _, raw := range []string{"{", `{"version":2}`, `{"version":1,"password":"private"}`, `{"version":1} {}`, strings.Repeat(" ", 65537)} {
		t.Run(raw[:min(len(raw), 30)], func(t *testing.T) {
			dir := t.TempDir()
			s, err := New(dir)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "accounts.json"), []byte(raw), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Load(); err == nil {
				t.Fatal("invalid registry accepted")
			}
		})
	}
}

func TestFailedSavePreservesRegistry(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := accounts.State{Version: 1, Accounts: []accounts.Record{{ID: "owner", User: "root"}}}
	if err := s.Save(want); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(accounts.State{Version: 2}); err == nil {
		t.Fatal("invalid save accepted")
	}
	got, err := s.Load()
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("previous registry lost: %+v, %v", got, err)
	}
}
