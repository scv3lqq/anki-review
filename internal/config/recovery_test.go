package config

import "testing"

func TestRecoveryConfiguration(t *testing.T) {
	cfg, err := Load(func(key string) string {
		if key == "SESSION_KEY_FILE" {
			return "/private/session.key"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SessionKeyFile != "/private/session.key" {
		t.Fatal("session key path was ignored")
	}
}
