// Package app wires the adapters, the review use case and the HTTP handler into a server.
package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/rs/zerolog"

	"github.com/scv3lqq/anki-review/internal/config"
	"github.com/scv3lqq/anki-review/internal/handler/httpapi"
	"github.com/scv3lqq/anki-review/internal/repository/ankiconnect"
	"github.com/scv3lqq/anki-review/internal/repository/claude"
	"github.com/scv3lqq/anki-review/internal/repository/filestore"
	"github.com/scv3lqq/anki-review/internal/usecase/review"
	"github.com/scv3lqq/anki-review/web"
)

const (
	readHeaderTimeout = 10 * time.Second
	readTimeout       = 30 * time.Second
	// a start syncs with AnkiWeb and reads every new card of the deck: allow it a while
	writeTimeout    = 3 * time.Minute
	idleTimeout     = 2 * time.Minute
	shutdownTimeout = 30 * time.Second
)

// App is the review server.
type App struct {
	cfg config.Config
	log zerolog.Logger
	svc *review.Service
	srv *http.Server
}

// New builds the server; nothing listens until Run or Serve.
func New(cfg config.Config, log zerolog.Logger) (*App, error) {
	store, err := filestore.New(cfg.DataDir)
	if err != nil {
		return nil, fmt.Errorf("app: %w", err)
	}
	grader := &claude.Grader{Bin: cfg.ClaudeBin, Model: cfg.ClaudeModel, Timeout: cfg.GradeTimeout, Dir: cfg.DataDir}
	svc, err := review.New(ankiconnect.New(cfg.AnkiURL, cfg.AnkiKey), grader, store, review.Options{
		KnowledgeDecks: cfg.KnowledgeDecks,
		Sync:           cfg.AnkiSync,
		Log:            log,
	})
	if err != nil {
		return nil, fmt.Errorf("app: %w", err)
	}
	h, err := httpapi.New(svc, web.FS, log)
	if err != nil {
		return nil, fmt.Errorf("app: %w", err)
	}
	srv := &http.Server{
		Handler:           h,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}
	return &App{cfg: cfg, log: log, svc: svc, srv: srv}, nil
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
	closeErr := a.svc.Close(stopCtx)
	if errors.Is(serveErr, http.ErrServerClosed) {
		serveErr = nil
	}
	if err := errors.Join(serveErr, shutdownErr, closeErr); err != nil {
		return fmt.Errorf("app: %w", err)
	}
	a.log.Info().Msg("stopped")
	return nil
}
