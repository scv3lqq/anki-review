package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestRealCLIHasNoTools probes only a local mock and never imports a user's credentials.
func TestRealCLIHasNoTools(t *testing.T) {
	bin, err := exec.LookPath("codex")
	if err != nil {
		t.Skip("codex missing: pinned real-CLI transport probe unavailable")
	}
	home, err := runtimeHome()
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(home)
	work := filepath.Join(home, "work")
	if err := os.Mkdir(work, 0o700); err != nil {
		t.Fatal(err)
	}
	g := &Grader{Bin: bin, Model: "gpt-5.2", Timeout: 25 * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	version, err := g.execute(ctx, home, work, []string{"--version"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(version) != "codex-cli "+SupportedVersion {
		t.Skipf("pinned transport probe requires %s; found another version", SupportedVersion)
	}
	requests := make(chan map[string]json.RawMessage, 8)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		raw, err := io.ReadAll(io.LimitReader(r.Body, 8*1024*1024))
		if err != nil {
			t.Error(err)
			return
		}
		var body map[string]json.RawMessage
		if json.Unmarshal(raw, &body) != nil {
			t.Error("mock transport received unreadable request")
			return
		}
		requests <- body
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_probe\",\"object\":\"response\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":0,\"output_tokens\":0,\"total_tokens\":0}}}\n\n")
	}))
	defer server.Close()
	schemaPath := filepath.Join(home, "schema.json")
	if err := os.WriteFile(schemaPath, []byte(checkSchema), 0o600); err != nil {
		t.Fatal(err)
	}
	args := g.args(work, schemaPath)
	args = args[:len(args)-1]
	// The custom provider exists only in this test. Production args cannot set an endpoint.
	overrides := []string{`model_provider="probe"`, `model_providers.probe.name="probe"`, `model_providers.probe.wire_api="responses"`, `model_providers.probe.stream_max_retries=0`, `model_providers.probe.request_max_retries=0`, `features.enable_request_compression=false`}
	base, _ := json.Marshal(server.URL + "/v1")
	overrides = append(overrides, "model_providers.probe.base_url="+string(base))
	for _, value := range overrides {
		args = append(args, "-c", value)
	}
	args = append(args, "-")
	const sentinel = "ANKI_PRIVATE_SENTINEL_012345"
	_, err = g.execute(ctx, home, work, args, sentinel+": Reply OK.")
	if err != nil {
		t.Fatalf("local mock CLI invocation: %v", err)
	}
	select {
	case body := <-requests:
		if strings.TrimSpace(string(body["tools"])) != "[]" {
			t.Fatalf("CLI exposed tools: %s", body["tools"])
		}
		raw, _ := json.Marshal(body)
		if !strings.Contains(string(raw), sentinel) {
			t.Fatal("mock transport did not receive sentinel")
		}
		if len(body["text"]) == 0 {
			t.Fatal("mock transport did not receive structured schema")
		}
	default:
		t.Fatal("real CLI did not call local mock")
	}
	if err := filepath.WalkDir(home, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type().IsRegular() {
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if strings.Contains(string(raw), sentinel) {
				t.Errorf("CLI persisted card sentinel in %s", entry.Name())
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
