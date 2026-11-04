package vault

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"strings"
	"testing"
)

func TestUserReadsPublicEnvelopeWithoutUnlockOrWrite(t *testing.T) {
	v := sealed(t)
	v.Close()
	before, err := os.ReadFile(v.path)
	if err != nil {
		t.Fatal(err)
	}
	user, err := v.User()
	if err != nil || user != testUser {
		t.Fatalf("User() = %q, %v", user, err)
	}
	if v.handle != nil {
		t.Fatal("metadata read unlocked vault")
	}
	after, err := os.ReadFile(v.path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("metadata read replaced vault")
	}
}

func TestUserValidatesBoundedEnvelope(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*file)
	}{
		{"unknown version", func(f *file) { f.Version++ }},
		{"bad salt", func(f *file) { f.KDF.Salt = "not base64" }},
		{"bad ciphertext", func(f *file) { f.Sealed = "" }},
		{"empty username", func(f *file) { f.User = "" }},
		{"untrimmed username", func(f *file) { f.User = " owner " }},
		{"control username", func(f *file) { f.User = "own\ner" }},
		{"long username", func(f *file) { f.User = strings.Repeat("é", 65) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := sealed(t)
			f := readFile(t, v)
			tc.mutate(&f)
			writeFile(t, v, f)
			if _, err := v.User(); !errors.Is(err, ErrDamaged) {
				t.Fatalf("invalid metadata accepted: %v", err)
			}
		})
	}
	v := fastVault(t)
	if _, err := v.User(); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing metadata error = %v", err)
	}
	if err := os.WriteFile(v.path, bytes.Repeat([]byte(" "), 2<<20), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := v.User(); !errors.Is(err, ErrDamaged) {
		t.Fatalf("oversized envelope accepted: %v", err)
	}
}

func TestUserDoesNotAuthenticateCiphertextOrChangeHandle(t *testing.T) {
	v := sealed(t)
	handle := v.handle
	f := readFile(t, v)
	f.User = strings.Repeat("é", 64)
	f.Sealed = flip(f.Sealed)
	writeFile(t, v, f)
	user, err := v.User()
	if err != nil || user != f.User {
		t.Fatalf("public Unicode username = %q, %v", user, err)
	}
	if v.handle != handle {
		t.Fatal("metadata lookup changed encryption handle")
	}
}
