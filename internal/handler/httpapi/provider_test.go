package httpapi

import (
	"net/http"
	"strings"
	"testing"

	"github.com/scv3lqq/anki-review/internal/usecase/owner"
)

func TestProviderCredentialsForwardOnly(t *testing.T) {
	own := readyOwner()
	own.token = sessionValue
	h := newHandlerWith(t, own, func() Service { return &stub{} })
	rec := send(h, "", http.MethodPost, "/api/setup", "127.0.0.1", "application/json", `{"user":"owner","password":"password","grader_provider":"codex","codex_auth":"private-snapshot"}`)
	if rec.Code != http.StatusOK || own.setup.GraderProvider != "codex" || own.setup.CodexAuth != "private-snapshot" || strings.Contains(rec.Body.String(), "private-snapshot") {
		t.Fatalf("setup provider not forwarded safely")
	}
	rec = send(h, sessionValue, http.MethodPost, "/api/settings", "127.0.0.1", "application/json", `{"password":"password","grader_provider":"codex","codex_auth":"new-private-snapshot"}`)
	if rec.Code != http.StatusOK || own.updated.GraderProvider == nil || *own.updated.GraderProvider != "codex" || own.updated.CodexAuth == nil || *own.updated.CodexAuth != "new-private-snapshot" || strings.Contains(rec.Body.String(), "private-snapshot") {
		t.Fatalf("settings provider not forwarded safely: %s", rec.Body)
	}
	own.status = owner.Status{State: owner.StateReady, Unlocked: true, GraderProvider: "codex", GraderConnected: true}
	rec = send(h, sessionValue, http.MethodGet, "/api/session", "127.0.0.1", "", "")
	if !strings.Contains(rec.Body.String(), `"grader_provider":"codex"`) || !strings.Contains(rec.Body.String(), `"grader_connected":true`) || strings.Contains(rec.Body.String(), "private-snapshot") {
		t.Fatalf("unsafe/missing metadata: %s", rec.Body)
	}
}
