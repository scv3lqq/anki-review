// Package app wires the adapters, the owner and review use cases and the HTTP handler into a server.
package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"github.com/scv3lqq/anki-review/internal/config"
	"github.com/scv3lqq/anki-review/internal/domain"
	"github.com/scv3lqq/anki-review/internal/handler/httpapi"
	"github.com/scv3lqq/anki-review/internal/repository/ankiconnect"
	"github.com/scv3lqq/anki-review/internal/repository/claude"
	"github.com/scv3lqq/anki-review/internal/repository/filestore"
	"github.com/scv3lqq/anki-review/internal/repository/vault"
	"github.com/scv3lqq/anki-review/internal/usecase/owner"
	"github.com/scv3lqq/anki-review/internal/usecase/review"
	"github.com/scv3lqq/anki-review/web"
)

const (
	readHeaderTimeout = 10 * time.Second
	readTimeout       = 30 * time.Second
	// a start syncs Anki with its sync server and reads every new card of the deck: allow it a while
	writeTimeout    = 3 * time.Minute
	idleTimeout     = 2 * time.Minute
	shutdownTimeout = 30 * time.Second
)

// App is the review server.
type App struct {
	cfg     config.Config
	log     zerolog.Logger
	store   *filestore.Store
	grader  *claude.Grader
	owner   *owner.UseCase
	srv     *http.Server
	mu      sync.Mutex
	service *review.Service // built on the first unlock, nil while locked
}

// New builds the server; nothing listens until Run or Serve. The server starts locked: the tokens
// come from the owner's vault once someone sets up or logs in on the page.
func New(cfg config.Config, log zerolog.Logger) (*App, error) {
	store, err := filestore.New(cfg.DataDir)
	if err != nil {
		return nil, fmt.Errorf("app: %w", err)
	}
	vlt, err := vault.New(cfg.DataDir)
	if err != nil {
		return nil, fmt.Errorf("app: %w", err)
	}
	a := &App{cfg: cfg, log: log, store: store}
	a.grader = &claude.Grader{
		Bin:     cfg.ClaudeBin,
		Model:   cfg.ClaudeModel,
		Timeout: cfg.GradeTimeout,
		Dir:     cfg.DataDir,
		// read per call: a token changed on the settings page applies to the next grading
		Token: func() string { return a.owner.Secrets().ClaudeToken },
	}
	own, err := owner.New(vlt, checker{ankiURL: cfg.AnkiURL, grader: a.grader}, owner.Options{Log: log, OnUnlock: a.unlock})
	if err != nil {
		return nil, fmt.Errorf("app: %w", err)
	}
	a.owner = own
	h, err := httpapi.New(own, a.reviewService, web.FS, log)
	if err != nil {
		return nil, fmt.Errorf("app: %w", err)
	}
	a.srv = &http.Server{
		Handler:           h,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}
	return a, nil
}

// unlock builds the review service. Its AnkiConnect key is read from the owner on every call, so a
// key changed on the settings page applies at once.
func (a *App) unlock(domain.Secrets) error {
	svc, err := review.New(ankiconnect.New(a.cfg.AnkiURL, func() string { return a.owner.Secrets().AnkiKey }), a.grader, a.store, review.Options{
		KnowledgeDecks: a.cfg.KnowledgeDecks,
		Sync:           a.cfg.AnkiSync,
		Log:            a.log,
	})
	if err != nil {
		return fmt.Errorf("app: review: %w", err)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.service = svc
	return nil
}

// reviewService is the review use case while the server is unlocked, nil before. It returns a nil
// interface, not a typed nil, so the handler can tell the two apart.
func (a *App) reviewService() httpapi.Service {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.service == nil {
		return nil
	}
	return a.service
}

// checker proves a token before the owner seals it, with the same AnkiConnect and claude the server uses.
type checker struct {
	ankiURL string
	grader  *claude.Grader
}

func (c checker) CheckAnki(ctx context.Context, key string) error {
	return ankiconnect.New(c.ankiURL, func() string { return key }).Version(ctx)
}

func (c checker) CheckClaude(ctx context.Context, token string) error {
	return c.grader.Check(ctx, token)
}

// Run listens on the configured loopback address until ctx is done.
func (a *App) Run(ctx context.Context) error {
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", a.cfg.Addr())
	if err != nil {
		return fmt.Errorf("app: listen: %w", err)
	}
	return a.Serve(ctx, ln)
}

// Serve serves on ln until ctx is done, then drains requests and waits for an Anki write in flight.
func (a *App) Serve(ctx context.Context, ln net.Listener) error {
	a.log.Info().Str("url", "http://"+ln.Addr().String()+"/").Msg("serving")
	served := make(chan error, 1)
	go func() { served <- a.srv.Serve(ln) }()

	var serveErr error
	select {
	case serveErr = <-served:
	case <-ctx.Done():
	}

	stopCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	shutdownErr := a.srv.Shutdown(stopCtx)
	var closeErr error
	a.mu.Lock()
	svc := a.service
	a.mu.Unlock()
	if svc != nil {
		closeErr = svc.Close(stopCtx)
	}
	if errors.Is(serveErr, http.ErrServerClosed) {
		serveErr = nil
	}
	if err := errors.Join(serveErr, shutdownErr, closeErr); err != nil {
		return fmt.Errorf("app: %w", err)
	}
	a.log.Info().Msg("stopped")
	return nil
}
