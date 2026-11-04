package runtimebroker

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testID = "0123456789abcdef0123456789abcdef"

func brokerFixture(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	directory, err := os.MkdirTemp("/tmp", "broker-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	socket := filepath.Join(directory, "broker.sock")
	listenConfig := net.ListenConfig{}
	listener, err := listenConfig.Listen(context.Background(), "unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: handler}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	client, err := New(socket)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestClientUnixContract(t *testing.T) {
	var methods []string
	client := brokerFixture(t, func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		if r.URL.Path != "/v1/runtimes/"+testID {
			t.Errorf("path = %s", r.URL.Path)
		}
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		_, _ = fmt.Fprintf(w, `{"url":"http://127.0.0.1:19000","key":"%s"}`, strings.Repeat("a", 64))
	})
	endpoint, err := client.Ensure(context.Background(), testID)
	if err != nil || endpoint.URL != "http://127.0.0.1:19000" || len(endpoint.Key) != 64 {
		t.Fatalf("endpoint/error = %+v/%v", endpoint, err)
	}
	if err := client.Remove(context.Background(), testID); err != nil {
		t.Fatal(err)
	}
	if strings.Join(methods, ",") != "POST,DELETE" {
		t.Fatal(methods)
	}
}

func TestClientRejectsIDsBeforeDial(t *testing.T) {
	client, err := New("/tmp/missing-runtime-broker.sock")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"owner", "../owner", testID + "/x", strings.ToUpper(testID), ""} {
		if _, err := client.Ensure(context.Background(), id); err == nil || !strings.Contains(err.Error(), "invalid runtime ID") {
			t.Errorf("Ensure(%q): %v", id, err)
		}
		if err := client.Remove(context.Background(), id); err == nil || !strings.Contains(err.Error(), "invalid runtime ID") {
			t.Errorf("Remove(%q): %v", id, err)
		}
	}
	for _, socket := range []string{"relative.sock", "http://localhost:1234", "/tmp/../broker.sock"} {
		if _, err := New(socket); err == nil {
			t.Errorf("accepted %q", socket)
		}
	}
}

func TestClientRejectsUnsafeOrOversizedResponses(t *testing.T) {
	for _, body := range []string{
		`{"url":"http://example.com:19000","key":"` + strings.Repeat("a", 64) + `"}`,
		`{"url":"http://127.0.0.1:8765","key":"` + strings.Repeat("a", 64) + `"}`,
		`{"url":"http://127.0.0.1:19000","key":"short"}`,
		strings.Repeat("x", 8193),
	} {
		t.Run(fmt.Sprint(len(body), body[:10]), func(t *testing.T) {
			client := brokerFixture(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) })
			if _, err := client.Ensure(context.Background(), testID); err == nil {
				t.Fatal("accepted unsafe response")
			}
		})
	}
}
