// Package ankiconnect talks to Anki through the AnkiConnect add-on (API v6).
package ankiconnect

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/scv3lqq/anki-review/internal/domain"
)

const (
	apiVersion   = 6
	lockRetries  = 20
	lockWait     = time.Second
	httpTimeout  = 2 * time.Minute
	lockedMarker = "lifecycle operation is active"
	queueMarker  = "not at top of queue"
	// AnkiConnect's answer when apiKey is set and the request doesn't carry it
	keyMarker = "valid api key must be provided"
)

// Client is an AnkiConnect client. Grades go through `answerCards`, i.e. Anki's own scheduler and
// revlog, exactly like pressing a button in the reviewer.
type Client struct {
	url  string
	key  func() string
	http *http.Client
	// lockWait is a field so tests don't wait a second per retry
	lockWait time.Duration
}

// New returns a client for the AnkiConnect endpoint. key is read on every call: it is AnkiConnect's
// optional apiKey, which the owner may change on the settings page.
func New(url string, key func() string) *Client {
	if key == nil {
		key = func() string { return "" }
	}
	return &Client{url: url, key: key, http: &http.Client{Timeout: httpTimeout}, lockWait: lockWait}
}

// Version checks that AnkiConnect answers with the API version this client speaks, using the key.
// A refused key gives domain.ErrAnkiKey; no answer gives domain.ErrAnki.
func (c *Client) Version(ctx context.Context) error {
	var v int
	if err := c.call(ctx, "version", nil, &v); err != nil {
		return fmt.Errorf("ankiconnect version: %w", err)
	}
	if v != apiVersion {
		return fmt.Errorf("ankiconnect version: %w: API version %d, want %d", domain.ErrAnki, v, apiVersion)
	}
	return nil
}

type request struct {
	Action  string `json:"action"`
	Version int    `json:"version"`
	Params  any    `json:"params,omitempty"`
	Key     string `json:"key,omitempty"`
}

type response struct {
	Result json.RawMessage `json:"result"`
	Error  *string         `json:"error"`
}

func (c *Client) call(ctx context.Context, action string, params, out any) error {
	body, err := json.Marshal(request{Action: action, Version: apiVersion, Params: params, Key: c.key()})
	if err != nil {
		return fmt.Errorf("ankiconnect %s: encode: %w", action, err)
	}
	for attempt := 1; ; attempt++ {
		resp, err := c.post(ctx, body)
		if err != nil {
			return fmt.Errorf("ankiconnect %s: %w: Anki is not running or AnkiConnect is off: %w", action, domain.ErrAnki, err)
		}
		if resp.Error == nil {
			if out == nil {
				return nil
			}
			if err := json.Unmarshal(resp.Result, out); err != nil {
				return fmt.Errorf("ankiconnect %s: %w: decode result: %w", action, domain.ErrAnki, err)
			}
			return nil
		}
		msg := *resp.Error
		// a sync (ours, or Anki's auto sync) locks the collection for a few seconds
		if strings.Contains(msg, lockedMarker) && attempt < lockRetries {
			select {
			case <-ctx.Done():
				return fmt.Errorf("ankiconnect %s: %w", action, ctx.Err())
			case <-time.After(c.lockWait):
			}
			continue
		}
		if strings.Contains(msg, keyMarker) {
			return fmt.Errorf("ankiconnect %s: %w: %s", action, domain.ErrAnkiKey, msg)
		}
		if strings.Contains(msg, queueMarker) {
			return fmt.Errorf("ankiconnect %s: %w: %s", action, domain.ErrQueueBusy, msg)
		}
		return fmt.Errorf("ankiconnect %s: %w: %s", action, domain.ErrAnki, msg)
	}
}

func (c *Client) post(ctx context.Context, body []byte) (*response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("post: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("post: status %d", resp.StatusCode)
	}
	var r response
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return &r, nil
}
