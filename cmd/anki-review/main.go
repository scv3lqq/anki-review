// Command anki-review serves a page for reviewing due Anki cards: typed answers, graded by Claude Code,
// written back to Anki after the owner confirms the grades.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rs/zerolog"

	"github.com/scv3lqq/anki-review/internal/app"
	"github.com/scv3lqq/anki-review/internal/config"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "anki-review:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}
	log := newLogger(cfg.LogLevel)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	a, err := app.New(cfg, log)
	if err != nil {
		return err
	}
	return a.Run(ctx)
}

// newLogger writes JSON lines (journald, log shippers) unless stderr is a terminal.
func newLogger(level zerolog.Level) zerolog.Logger {
	var w io.Writer = os.Stderr
	if fi, err := os.Stderr.Stat(); err == nil && fi.Mode()&os.ModeCharDevice != 0 {
		w = zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.TimeOnly}
	}
	return zerolog.New(w).Level(level).With().Timestamp().Logger()
}
