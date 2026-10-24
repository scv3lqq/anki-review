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
)

// Client is an AnkiConnect client. Grades go through `answerCards`, i.e. Anki's own scheduler and
// revlog, exactly like pressing a button in the reviewer.
type Client struct {
	url      string
	key      string
	http     *http.Client
	lockWait time.Duration
}

// New returns a client for the AnkiConnect endpoint; key is AnkiConnect's optional apiKey.
func New(url, key string) *Client {
	return &Client{url: url, key: key, http: &http.Client{Timeout: httpTimeout}, lockWait: lockWait}
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
	body, err := json.Marshal(request{Action: action, Version: apiVersion, Params: params, Key: c.key})
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
