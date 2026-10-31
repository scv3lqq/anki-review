package config

import (
	"reflect"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

func TestLoad(t *testing.T) {
	defaults := Config{
		Port:           8767,
		DataDir:        "data",
		KnowledgeDecks: []string{"Tech", "Career"},
		AnkiURL:        "http://127.0.0.1:8765",
		AnkiSync:       true,
		ClaudeBin:      "claude",
		GraderProvider: "claude",
		CodexBin:       "codex",
		GradeTimeout:   5 * time.Minute,
		LogLevel:       zerolog.InfoLevel,
	}
	tests := []struct {
		name    string
		env     map[string]string
		want    func(c *Config)
		wantErr bool
	}{
		{name: "defaults", env: nil, want: func(*Config) {}},
		{
			name: "everything set",
			env: map[string]string{
				"PORT": "9000", "DATA_DIR": "/var/lib/anki-review", "KNOWLEDGE_DECKS": " Tech , Go,, ",
				"ANKI_CONNECT_URL": "http://anki:8765", "ANKI_SYNC": "false",
				"CLAUDE_BIN": "/usr/local/bin/claude", "CLAUDE_MODEL": "sonnet", "GRADE_TIMEOUT": "90s", "LOG_LEVEL": "debug",
				"GRADER_PROVIDER": "codex", "CODEX_BIN": "/usr/local/bin/codex", "CODEX_MODEL": "gpt-5.4",
			},
			want: func(c *Config) {
				c.Port, c.DataDir, c.KnowledgeDecks = 9000, "/var/lib/anki-review", []string{"Tech", "Go"}
				c.AnkiURL, c.AnkiSync = "http://anki:8765", false
				c.GraderProvider, c.CodexBin, c.CodexModel = "codex", "/usr/local/bin/codex", "gpt-5.4"
				c.ClaudeBin, c.ClaudeModel, c.GradeTimeout, c.LogLevel = "/usr/local/bin/claude", "sonnet", 90*time.Second, zerolog.DebugLevel
			},
		},
		{name: "invalid provider", env: map[string]string{"GRADER_PROVIDER": "other"}, wantErr: true},
		{name: "provider is case sensitive", env: map[string]string{"GRADER_PROVIDER": "Codex"}, wantErr: true},
		{name: "port out of range", env: map[string]string{"PORT": "70000"}, wantErr: true},
		{name: "port not a number", env: map[string]string{"PORT": "http"}, wantErr: true},
		{name: "bad sync flag", env: map[string]string{"ANKI_SYNC": "maybe"}, wantErr: true},
		{name: "timeout without unit", env: map[string]string{"GRADE_TIMEOUT": "300"}, wantErr: true},
		{name: "negative timeout", env: map[string]string{"GRADE_TIMEOUT": "-1m"}, wantErr: true},
		{name: "anki url without scheme", env: map[string]string{"ANKI_CONNECT_URL": "127.0.0.1:8765"}, wantErr: true},
		{name: "bad log level", env: map[string]string{"LOG_LEVEL": "loud"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Load(func(k string) string { return tt.env[k] })
			if (err != nil) != tt.wantErr {
				t.Fatalf("Load() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			want := defaults
			want.KnowledgeDecks = append([]string(nil), defaults.KnowledgeDecks...)
			tt.want(&want)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("Load() = %+v, want %+v", got, want)
			}
		})
	}
}

func TestAddrIsLoopback(t *testing.T) {
	if got := (Config{Port: 8767}).Addr(); got != "127.0.0.1:8767" {
		t.Errorf("Addr() = %q", got)
	}
}
