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
	dropLegacySecrets(log)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	a, err := app.New(cfg, log)
	if err != nil {
		return err
	}
	return a.Run(ctx)
}

// dropLegacySecrets: the tokens are entered on the setup page now. A value still in the environment is
// ignored, and removed from this process so that no child process inherits it.
func dropLegacySecrets(log zerolog.Logger) {
	for _, name := range []string{"ANKI_CONNECT_KEY", "CLAUDE_CODE_OAUTH_TOKEN"} {
		if _, ok := os.LookupEnv(name); !ok {
			continue
		}
		log.Warn().Str("variable", name).Msg("ignored: enter the value on the setup page and remove it from the environment")
		if err := os.Unsetenv(name); err != nil {
			log.Warn().Err(err).Str("variable", name).Msg("could not remove from the environment")
		}
	}
}

// newLogger writes JSON lines (journald, log shippers) unless stderr is a terminal.
func newLogger(level zerolog.Level) zerolog.Logger {
	var w io.Writer = os.Stderr
	if fi, err := os.Stderr.Stat(); err == nil && fi.Mode()&os.ModeCharDevice != 0 {
		w = zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.TimeOnly}
	}
	return zerolog.New(w).Level(level).With().Timestamp().Logger()
}
