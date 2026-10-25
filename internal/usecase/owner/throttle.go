package owner

import (
	"sync"
	"time"
)

const (
	// the map is bounded: an attacker rotating client addresses can't grow it past this
	maxEntries = 10_000
	// an entry whose wait ended this long ago has nothing left to count
	keepFor = time.Hour
	// the owner's settings checks share one counter: a session is already proof of the owner
	settingsKey = "owner"
)

type entry struct {
	failures int
	last     time.Time
}

// throttle counts wrong passwords per key: a client address for login and setup, one shared key for
// the settings checks. It has its own lock, so a throttled client never waits behind an Argon2id run.
type throttle struct {
	mu      sync.Mutex
	now     func() time.Time
	entries map[string]*entry
}

func newThrottle(now func() time.Time) *throttle {
	return &throttle{now: now, entries: map[string]*entry{}}
}

// wait returns how long key must still wait; zero means it may try.
func (t *throttle) wait(key string) time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	e, ok := t.entries[key]
	if !ok {
		return 0
	}
	wait := backoff(e.failures)
	if wait == 0 {
		return 0
	}
	remaining := e.last.Add(wait).Sub(t.now())
	if remaining <= 0 {
		return 0
	}
	return remaining
}

// fail counts a wrong password or a failed check for key and returns the count.
func (t *throttle) fail(key string) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	e, ok := t.entries[key]
	if !ok {
		if len(t.entries) >= maxEntries {
			t.pruneLocked()
		}
		if len(t.entries) >= maxEntries {
			t.evictOldestLocked()
		}
		e = &entry{}
		t.entries[key] = e
	}
	e.failures++
	e.last = t.now()
	return e.failures
}

// reset forgets key after a success.
func (t *throttle) reset(key string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.entries, key)
}

// pruneLocked drops the entries whose window ended more than keepFor ago.
func (t *throttle) pruneLocked() {
	now := t.now()
	for k, e := range t.entries {
		if now.Sub(e.last.Add(backoff(e.failures))) > keepFor {
			delete(t.entries, k)
		}
	}
}

func (t *throttle) evictOldestLocked() {
	var oldest string
	var oldestAt time.Time
	first := true
	for k, e := range t.entries {
		if first || e.last.Before(oldestAt) {
			oldest, oldestAt, first = k, e.last, false
		}
	}
	delete(t.entries, oldest)
}
