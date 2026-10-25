// Package config reads the settings from the environment.
package config

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog"
)

// Config is everything the server needs; see the README for each variable.
type Config struct {
	Port           int
	DataDir        string
	KnowledgeDecks []string
	AnkiURL        string
	AnkiSync       bool
	ClaudeBin      string
	ClaudeModel    string
	GradeTimeout   time.Duration
	LogLevel       zerolog.Level
}

// Addr is loopback only: a reverse proxy or an ssh tunnel reaches the page, never the network.
func (c Config) Addr() string {
	return fmt.Sprintf("127.0.0.1:%d", c.Port)
}

// Load reads the config through getenv, normally os.Getenv.
func Load(getenv func(string) string) (Config, error) {
	get := func(name, def string) string {
		if v := strings.TrimSpace(getenv(name)); v != "" {
			return v
		}
		return def
	}
	c := Config{
		DataDir:     get("DATA_DIR", "data"),
		AnkiURL:     get("ANKI_CONNECT_URL", "http://127.0.0.1:8765"),
		ClaudeBin:   get("CLAUDE_BIN", "claude"),
		ClaudeModel: get("CLAUDE_MODEL", ""),
	}
	for _, d := range strings.Split(get("KNOWLEDGE_DECKS", "Tech,Career"), ",") {
		if d = strings.TrimSpace(d); d != "" {
			c.KnowledgeDecks = append(c.KnowledgeDecks, d)
		}
	}

	var err error
	if c.Port, err = strconv.Atoi(get("PORT", "8767")); err != nil || c.Port < 1 || c.Port > 65535 {
		return Config{}, fmt.Errorf("config: PORT must be 1 to 65535, got %q", getenv("PORT"))
	}
	if c.AnkiSync, err = strconv.ParseBool(get("ANKI_SYNC", "true")); err != nil {
		return Config{}, fmt.Errorf("config: ANKI_SYNC: %w", err)
	}
	if c.GradeTimeout, err = time.ParseDuration(get("GRADE_TIMEOUT", "5m")); err != nil || c.GradeTimeout <= 0 {
		return Config{}, fmt.Errorf("config: GRADE_TIMEOUT must be a positive duration like 5m, got %q", getenv("GRADE_TIMEOUT"))
	}
	if c.LogLevel, err = zerolog.ParseLevel(get("LOG_LEVEL", "info")); err != nil {
		return Config{}, fmt.Errorf("config: LOG_LEVEL: %w", err)
	}
	if u, err := url.Parse(c.AnkiURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return Config{}, fmt.Errorf("config: ANKI_CONNECT_URL must be an http(s) URL, got %q", c.AnkiURL)
	}
	return c, nil
}
