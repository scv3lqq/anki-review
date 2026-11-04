package owner

import (
	"context"
	"errors"
	"testing"

	"github.com/rs/zerolog"

	"github.com/scv3lqq/anki-review/internal/domain"
)

type metadataVault struct {
	*fakeVault
	existsErr error
	opens     int
}

func (v *metadataVault) Exists() (bool, error) { return v.exists, v.existsErr }
func (v *metadataVault) Open(user, password string) (domain.Secrets, error) {
	v.opens++
	return v.fakeVault.Open(user, password)
}

func TestRefreshVaultDiscoversFileWithoutDecryptingOrSealing(t *testing.T) {
	vault := &metadataVault{fakeVault: &fakeVault{}}
	auth, err := New(vault, &fakeChecker{}, Options{Log: zerolog.Nop()})
	if err != nil {
		t.Fatal(err)
	}
	secrets := domain.Secrets{ClaudeToken: testClaude, AnkiKey: testKey}
	if err := vault.Seal(testUser, testPass, secrets); err != nil {
		t.Fatal(err)
	}
	if err := auth.RefreshVault(); err != nil {
		t.Fatal(err)
	}
	if status := auth.Status(""); status.State != StateLogin || status.Unlocked {
		t.Fatalf("refresh changed unlock state: %+v", status)
	}
	if vault.opens != 0 || vault.seals != 1 || auth.Secrets() != (domain.Secrets{}) {
		t.Fatal("metadata refresh decrypted, resealed, or loaded credentials")
	}
	if _, err := auth.Setup(context.Background(), testClient, SetupInput{User: testUser, Password: "different password", ClaudeToken: testClaude}); !errors.Is(err, ErrSetupDone) {
		t.Fatalf("refresh did not guard Setup: %v", err)
	}
	if _, err := auth.Login(context.Background(), testClient, testUser, testPass); err != nil {
		t.Fatal(err)
	}
}

func TestRefreshVaultMonotonicAndPreservesUnlockedState(t *testing.T) {
	vault := &metadataVault{fakeVault: &fakeVault{}}
	auth, err := New(vault, &fakeChecker{}, Options{Log: zerolog.Nop()})
	if err != nil {
		t.Fatal(err)
	}
	token, err := auth.Setup(context.Background(), testClient, SetupInput{User: testUser, Password: testPass, ClaudeToken: testClaude, AnkiKey: testKey})
	if err != nil {
		t.Fatal(err)
	}
	before, secrets := auth.Status(token), auth.Secrets()
	vault.exists = false
	if err := auth.RefreshVault(); err != nil {
		t.Fatal(err)
	}
	if auth.Status(token) != before || auth.Secrets() != secrets {
		t.Fatal("missing file downgraded existing unlocked state")
	}
	metadataErr := errors.New("metadata unavailable")
	vault.existsErr = metadataErr
	if err := auth.RefreshVault(); !errors.Is(err, metadataErr) {
		t.Fatalf("metadata error was not wrapped: %v", err)
	}
	if auth.Status(token) != before || auth.Secrets() != secrets || !auth.Authorized(token) {
		t.Fatal("metadata error cleared unlocked state")
	}
}
