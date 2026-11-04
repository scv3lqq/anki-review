// Package runtimebroker provides the bounded Unix-only client for managed Anki runtimes.
package runtimebroker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"time"
)

const responseLimit = 8192

// Endpoint is a managed loopback AnkiConnect endpoint and its private API key.
type Endpoint struct {
	URL string `json:"url"`
	Key string `json:"key"`
}

// Client has no TCP transport and never follows redirects.
type Client struct{ http *http.Client }

// New binds a client to one administrator-configured absolute Unix socket.
func New(socket string) (*Client, error) {
	if !filepath.IsAbs(socket) || filepath.Clean(socket) != socket || len(socket) > 100 {
		return nil, errors.New("runtimebroker: invalid Unix socket path")
	}
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		connection, err := dialer.DialContext(ctx, "unix", socket)
		if err != nil {
			return nil, fmt.Errorf("dial runtime broker: %w", err)
		}
		return connection, nil
	}, ResponseHeaderTimeout: 2 * time.Minute, IdleConnTimeout: 30 * time.Second}
	return &Client{http: &http.Client{Transport: transport, Timeout: 2 * time.Minute, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

// ValidID accepts only opaque guest IDs; the legacy owner is never provisioned.
func ValidID(id string) bool { return regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(id) }

// Ensure creates or recovers the same bounded runtime for id.
func (c *Client) Ensure(ctx context.Context, id string) (Endpoint, error) {
	body, err := c.call(ctx, http.MethodPost, id)
	if err != nil {
		return Endpoint{}, err
	}
	var endpoint Endpoint
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&endpoint); err != nil {
		return Endpoint{}, fmt.Errorf("runtimebroker: decode endpoint: %w", err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return Endpoint{}, errors.New("runtimebroker: trailing response data")
	}
	u, err := url.Parse(endpoint.URL)
	if err != nil {
		return Endpoint{}, fmt.Errorf("runtimebroker: parse endpoint: %w", err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || port < 19000 || port > 65535 || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(endpoint.Key) {
		return Endpoint{}, errors.New("runtimebroker: invalid managed endpoint")
	}
	return endpoint, nil
}

// Remove deletes only the runtime associated with this valid guest ID.
func (c *Client) Remove(ctx context.Context, id string) error {
	_, err := c.call(ctx, http.MethodDelete, id)
	return err
}

func (c *Client) call(ctx context.Context, method, id string) ([]byte, error) {
	if !ValidID(id) {
		return nil, errors.New("runtimebroker: invalid runtime ID")
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://runtime-broker/v1/runtimes/"+id, nil)
	if err != nil {
		return nil, fmt.Errorf("runtimebroker: create request: %w", err)
	}
	response, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("runtimebroker: request: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, responseLimit+1))
	if err != nil {
		return nil, fmt.Errorf("runtimebroker: read response: %w", err)
	}
	if len(body) > responseLimit {
		return nil, errors.New("runtimebroker: response too large")
	}
	want := http.StatusOK
	if method == http.MethodDelete {
		want = http.StatusNoContent
	}
	if response.StatusCode != want {
		return nil, fmt.Errorf("runtimebroker: service status %d", response.StatusCode)
	}
	return body, nil
}
