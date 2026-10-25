package owner

import (
	"fmt"
	"testing"
	"time"
)

// fakeNow is a clock the tests move by hand.
type fakeNow struct{ t time.Time }

func (f *fakeNow) now() time.Time { return f.t }

func TestThrottleWait(t *testing.T) {
	tests := []struct {
		name     string
		failures int
		elapsed  time.Duration
		want     time.Duration
	}{
		{name: "free failures", failures: 4, elapsed: 0, want: 0},
		{name: "fifth failure waits a second", failures: 5, elapsed: 0, want: time.Second},
		{name: "wait ended", failures: 5, elapsed: time.Second, want: 0},
		{name: "doubling", failures: 7, elapsed: time.Second, want: 3 * time.Second},
		{name: "capped", failures: 50, elapsed: 0, want: maxWait},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clk := &fakeNow{t: time.Unix(1_000, 0)}
			th := newThrottle(clk.now)
			for range tt.failures {
				th.fail("k")
			}
			clk.t = clk.t.Add(tt.elapsed)
			if got := th.wait("k"); got != tt.want {
				t.Errorf("wait() = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestThrottleKeysAreIndependent(t *testing.T) {
	clk := &fakeNow{t: time.Unix(1_000, 0)}
	th := newThrottle(clk.now)
	for range 5 {
		th.fail("a")
	}
	if th.wait("a") == 0 || th.wait("b") != 0 {
		t.Errorf("a waits %s, b waits %s; want only a to wait", th.wait("a"), th.wait("b"))
	}
	th.reset("a")
	if th.wait("a") != 0 {
		t.Error("reset did not forget the key")
	}
}

func TestThrottlePrunesFinishedEntries(t *testing.T) {
	clk := &fakeNow{t: time.Unix(1_000, 0)}
	th := newThrottle(clk.now)
	for i := range maxEntries {
		th.fail(fmt.Sprint("k", i))
	}
	clk.t = clk.t.Add(keepFor + time.Minute)
	th.fail("fresh")
	if len(th.entries) != 1 {
		t.Errorf("entries = %d after an hour, want the finished ones pruned to 1", len(th.entries))
	}
}

func TestThrottleEvictsOldestWhenFull(t *testing.T) {
	clk := &fakeNow{t: time.Unix(1_000, 0)}
	th := newThrottle(clk.now)
	for i := range maxEntries {
		th.fail(fmt.Sprint("k", i))
		// close enough together that none has finished its hour: the map must evict, not prune
		clk.t = clk.t.Add(time.Millisecond)
	}
	th.fail("new")
	if len(th.entries) != maxEntries {
		t.Errorf("entries = %d, want the bound %d", len(th.entries), maxEntries)
	}
	if _, ok := th.entries["k0"]; ok {
		t.Error("the oldest entry survived a full map")
	}
	if _, ok := th.entries["new"]; !ok {
		t.Error("the new entry was not stored")
	}
}
