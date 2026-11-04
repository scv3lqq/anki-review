package accounts

import (
	"crypto/sha256"
	"sync"
	"time"

	"github.com/scv3lqq/anki-review/internal/usecase/owner"
)

const maxThrottleClients = 1024

type failedLogin struct {
	count int
	last  time.Time
}
type loginThrottle struct {
	mu      sync.Mutex
	now     func() time.Time
	clients map[[32]byte]failedLogin
}

func newLoginThrottle(now func() time.Time) *loginThrottle {
	return &loginThrottle{now: now, clients: make(map[[32]byte]failedLogin)}
}
func (t *loginThrottle) wait(client string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	entry := t.clients[sha256.Sum256([]byte(client))]
	if entry.count < 5 {
		return nil
	}
	delay := min(time.Second<<min(entry.count-5, 10), 15*time.Minute)
	remaining := entry.last.Add(delay).Sub(t.now())
	if remaining > 0 {
		return &owner.ThrottledError{Wait: (remaining + time.Second - 1).Truncate(time.Second)}
	}
	return nil
}
func (t *loginThrottle) fail(client string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	key := sha256.Sum256([]byte(client))
	entry, exists := t.clients[key]
	if !exists && len(t.clients) >= maxThrottleClients {
		var oldest [32]byte
		var at time.Time
		for k, value := range t.clients {
			if at.IsZero() || value.last.Before(at) {
				oldest, at = k, value.last
			}
		}
		delete(t.clients, oldest)
	}
	entry.count = min(entry.count+1, 16)
	entry.last = t.now()
	t.clients[key] = entry
}
func (t *loginThrottle) reset(client string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.clients, sha256.Sum256([]byte(client)))
}
