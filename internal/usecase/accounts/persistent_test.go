package accounts

import (
	"context"
	"testing"
	"time"
)

type resolvingFactory struct {
	*fakeFactory
	token, id string
	lookups   int
}

func (f *resolvingFactory) ResolveSession(token string) (string, bool) {
	f.lookups++
	return f.id, token == f.token
}

func TestPersistentResolveFallbackAndOriginalExpiry(t *testing.T) {
	h := newHarness(t, 4)
	token := h.register(t, "guest")
	rec, rt, ok := h.m.Resolve(token)
	if !ok {
		t.Fatal("missing guest")
	}
	// A fresh index uses only the factory's authenticated metadata; the owner remains authority.
	factory := &resolvingFactory{fakeFactory: h.factory, token: token, id: rec.ID}
	h.now = h.now.Add(time.Hour)
	fresh, err := New(h.store, factory, h.root, Options{Now: func() time.Time { return h.now }})
	if err != nil {
		t.Fatal(err)
	}
	fresh.slots[rec.ID].runtime = rt
	if _, _, ok := fresh.Resolve("unknown"); ok || len(factory.opens) != 1 {
		t.Fatal("unknown token opened runtime")
	}
	if got, _, ok := fresh.Resolve(token); !ok || got.ID != rec.ID {
		t.Fatal("persisted session was not resolved")
	}
	wantExpiry := time.Date(2026, time.January, 31, 0, 0, 0, 0, time.UTC)
	if got := fresh.sessions[invitationHash(token)].expires; !got.Equal(wantExpiry) {
		t.Fatalf("restored cache expiry=%s, want original expiry=%s", got, wantExpiry)
	}
	if _, raw := fresh.sessions[token]; raw {
		t.Fatal("manager retained bearer token")
	}
	h.now = h.now.Add(30*24*time.Hour - time.Hour)
	if _, _, ok := fresh.Resolve(token); ok {
		t.Fatal("fallback extended original expiry")
	}
}

func TestPersistentResolveRejectsPendingAndClosed(t *testing.T) {
	h := newHarness(t, 4)
	token := h.register(t, "guest")
	rec, _, _ := h.m.Resolve(token)
	factory := &resolvingFactory{fakeFactory: h.factory, token: token, id: rec.ID}
	h.store.state.Accounts[1].Pending = true
	h.store.state.Invitations[0].Used = false
	fresh, err := New(h.store, factory, h.root, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, ok := fresh.Resolve(token); ok || len(factory.opens) != 1 {
		t.Fatal("pending account restored")
	}
	if err := fresh.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	before := factory.lookups
	if _, _, ok := fresh.Resolve(token); ok || factory.lookups != before {
		t.Fatal("closed manager looked up token")
	}
}

func TestPersistentRestoredIndexEvictsOnlyOldestExpiry(t *testing.T) {
	h := newHarness(t, 4)
	tokens := []string{h.register(t, "guest")}
	rec, rt, ok := h.m.Resolve(tokens[0])
	if !ok {
		t.Fatal("guest missing")
	}
	for range 63 {
		h.now = h.now.Add(time.Second)
		token, err := h.m.Login(context.Background(), "guest-client", "guest", password)
		if err != nil {
			t.Fatal(err)
		}
		tokens = append(tokens, token)
	}
	f := &resolvingFactory{fakeFactory: h.factory, id: rec.ID}
	fresh, err := New(h.store, f, h.root, Options{Now: func() time.Time { return h.now }})
	if err != nil {
		t.Fatal(err)
	}
	fresh.slots[rec.ID].runtime = rt
	// Browser activity after restart can refill the cache in any order.
	for i := len(tokens) - 1; i >= 0; i-- {
		f.token = tokens[i]
		if _, _, ok := fresh.Resolve(tokens[i]); !ok {
			t.Fatal("restore failed")
		}
	}
	h.now = h.now.Add(time.Second)
	if _, err := fresh.Login(context.Background(), "guest-client", "guest", password); err != nil {
		t.Fatal(err)
	}
	if rt.Auth.Authorized(tokens[0]) {
		t.Fatal("oldest session retained")
	}
	if !rt.Auth.Authorized(tokens[len(tokens)-1]) {
		t.Fatal("newest restored session was also revoked; only oldest expiry should be evicted")
	}
}
