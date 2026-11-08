package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"github.com/scv3lqq/anki-review/internal/config"
	"github.com/scv3lqq/anki-review/internal/handler/httpapi"
	"github.com/scv3lqq/anki-review/internal/repository/runtimebroker"
	"github.com/scv3lqq/anki-review/internal/repository/vault"
	"github.com/scv3lqq/anki-review/internal/usecase/accounts"
	"github.com/scv3lqq/anki-review/internal/usecase/owner"
)

type runtimeBroker interface {
	Ensure(context.Context, string) (runtimebroker.Endpoint, error)
	Remove(context.Context, string) error
}
type accountFactory struct {
	mu       sync.Mutex
	cfg      config.Config
	log      zerolog.Logger
	registry accounts.Store
	broker   runtimeBroker
	runtimes map[string]*App
}

func (f *accountFactory) runtime(id string, a *App) *accounts.Runtime {
	return &accounts.Runtime{Auth: a.owner, AnkiKey: a.ankiKey, Close: func(ctx context.Context) error {
		if err := a.closeReview(ctx); err != nil {
			return err
		}
		f.mu.Lock()
		if f.runtimes[id] == a {
			delete(f.runtimes, id)
		}
		f.mu.Unlock()
		return nil
	}}
}

func (f *accountFactory) Open(ctx context.Context, record accounts.Record) (*accounts.Runtime, error) {
	if !runtimebroker.ValidID(record.ID) {
		return nil, accounts.ErrUnavailable
	}
	f.mu.Lock()
	cached := f.runtimes[record.ID]
	f.mu.Unlock()
	if cached != nil {
		return f.runtime(record.ID, cached), nil
	}
	endpoint, err := f.broker.Ensure(ctx, record.ID)
	if err != nil {
		return nil, fmt.Errorf("open managed account: %w", accounts.ErrUnavailable)
	}
	cfg := f.cfg
	cfg.DataDir = filepath.Join(cfg.DataDir, "accounts", record.ID)
	cfg.AnkiURL = endpoint.URL
	cfg.AnkiSync = false
	cfg.KnowledgeDecks = nil
	a, err := newAccount(cfg, f.log.With().Str("account_id", record.ID).Logger(), httpapi.Options{AccountID: record.ID, MultiAccount: true, Managed: true})
	if err != nil {
		return nil, fmt.Errorf("construct managed account: %w", accounts.ErrUnavailable)
	}
	a.ankiKey = endpoint.Key
	f.mu.Lock()
	f.runtimes[record.ID] = a
	f.mu.Unlock()
	return f.runtime(record.ID, a), nil
}

// ResolveSession inspects authenticated vault metadata before provisioning a runtime.
func (f *accountFactory) ResolveSession(token string) (string, bool) {
	if token == "" || len(token) > 256 || f.cfg.SessionKeyFile == "" || f.registry == nil {
		return "", false
	}
	state, err := f.registry.Load()
	if err != nil || len(state.Accounts) > 8 {
		return "", false
	}
	digest := sha256.Sum256([]byte(token))
	hash := hex.EncodeToString(digest[:])
	for _, record := range state.Accounts {
		if record.Pending || (record.ID != "owner" && !runtimebroker.ValidID(record.ID)) {
			continue
		}
		directory := f.cfg.DataDir
		if record.ID != "owner" {
			directory = filepath.Join(directory, "accounts", record.ID)
		}
		v, err := vault.New(directory, vault.WithSessionKey(f.cfg.SessionKeyFile, record.ID))
		if err != nil {
			continue
		}
		sessions, err := v.LoadSessions()
		v.Close()
		if err != nil {
			continue
		}
		for _, session := range sessions {
			if session.Hash == hash && time.Now().Before(session.Expires) {
				return record.ID, true
			}
		}
	}
	return "", false
}

func (f *accountFactory) Remove(ctx context.Context, id string) error {
	if !runtimebroker.ValidID(id) {
		return accounts.ErrForbidden
	}
	directory := filepath.Join(f.cfg.DataDir, "accounts", id)
	v, err := vault.New(directory, vault.WithSessionKey(f.cfg.SessionKeyFile, id))
	if err != nil {
		return fmt.Errorf("inspect pending account: %w", accounts.ErrUnavailable)
	}
	exists, err := v.Exists()
	if err != nil {
		return fmt.Errorf("inspect pending vault: %w", accounts.ErrUnavailable)
	}
	if exists {
		return accounts.ErrHasVault
	}
	if err := f.broker.Remove(ctx, id); err != nil {
		return fmt.Errorf("remove managed account: %w", accounts.ErrUnavailable)
	}
	if err := os.RemoveAll(directory); err != nil {
		return fmt.Errorf("remove pending account directory: %w", accounts.ErrUnavailable)
	}
	return nil
}

type accountDirectory struct {
	manager *accounts.Manager
	factory *accountFactory
	root    *App
}

func (d *accountDirectory) Resolve(token string) (http.Handler, bool) {
	record, runtime, ok := d.manager.Resolve(token)
	if !ok {
		return nil, false
	}
	d.factory.mu.Lock()
	account := d.factory.runtimes[record.ID]
	d.factory.mu.Unlock()
	if account == nil || account.owner != runtime.Auth {
		return nil, false
	}
	return account.handler, true
}
func (d *accountDirectory) PublicState() string {
	if d.root.owner.Status("").State == owner.StateSetup {
		return owner.StateSetup
	}
	return owner.StateLogin
}
func (d *accountDirectory) Setup(ctx context.Context, client string, in owner.SetupInput) (string, error) {
	return d.manager.Setup(ctx, client, in)
}
func (d *accountDirectory) Login(ctx context.Context, client, user, password string) (string, error) {
	return d.manager.Login(ctx, client, user, password)
}
func (d *accountDirectory) Invite(token string) (accounts.InviteResult, error) {
	return d.manager.Invite(token)
}
func (d *accountDirectory) Register(ctx context.Context, client, invitation string, in owner.SetupInput) (string, error) {
	return d.manager.Register(ctx, client, invitation, in)
}
