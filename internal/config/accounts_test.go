package config

import "testing"

func TestAccountConfiguration(t *testing.T) {
	for _, tc := range []struct {
		socket, max string
		bad         bool
	}{
		{"", "", false}, {"/run/anki-review-provisioner/control.sock", "8", false}, {"relative", "4", true}, {"/tmp/../broker.sock", "4", true}, {"/tmp/broker.sock", "1", true}, {"/tmp/broker.sock", "9", true},
	} {
		cfg, err := Load(func(key string) string {
			switch key {
			case "ACCOUNT_SOCKET":
				return tc.socket
			case "MAX_ACCOUNTS":
				return tc.max
			}
			return ""
		})
		if (err != nil) != tc.bad {
			t.Fatalf("%+v: %v", tc, err)
		}
		if !tc.bad && (cfg.AccountSocket != tc.socket || cfg.MaxAccounts < 2) {
			t.Fatalf("missing account config: %+v", cfg)
		}
	}
}
