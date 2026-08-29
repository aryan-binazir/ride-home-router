package plandraft

import (
	"encoding/hex"
	"reflect"
	"ride-home-router/internal/models"
	"sync"
	"testing"
	"time"
)

type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) Advance(duration time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(duration)
}

func TestStoreGetDoesNotInsertOnMiss(t *testing.T) {
	clock := &testClock{now: time.Date(2026, time.August, 29, 10, 7, 30, 0, time.UTC)}
	store := newStore(time.Hour, time.Hour, clock.Now)
	t.Cleanup(store.Close)

	draft, ok := store.Get("client-controlled-id")
	if ok || !reflect.DeepEqual(draft, Draft{}) {
		t.Fatalf("Get() = (%#v, %t), want zero draft and false", draft, ok)
	}

	store.mu.Lock()
	draftCount := len(store.drafts)
	store.mu.Unlock()
	if draftCount != 0 {
		t.Fatalf("Get() inserted %d drafts on a miss, want 0", draftCount)
	}
}

func TestStoreGetExpiresWithoutResurrectingDraft(t *testing.T) {
	clock := &testClock{now: time.Date(2026, time.August, 29, 10, 0, 0, 0, time.UTC)}
	store := newStore(time.Hour, time.Hour, clock.Now)
	t.Cleanup(store.Close)
	store.Update("draft", func(d *Draft) { d.LocationID = 42 })

	clock.Advance(time.Hour + time.Nanosecond)
	if draft, ok := store.Get("draft"); ok || !reflect.DeepEqual(draft, Draft{}) {
		t.Fatalf("expired Get() = (%#v, %t), want zero draft and false", draft, ok)
	}
	if _, ok := store.Get("draft"); ok {
		t.Fatal("second Get() resurrected the expired draft")
	}

	store.mu.Lock()
	_, exists := store.drafts["draft"]
	store.mu.Unlock()
	if exists {
		t.Fatal("expired draft remains in the store")
	}
}

func TestStoreGetRefreshesSlidingTTL(t *testing.T) {
	clock := &testClock{now: time.Date(2026, time.August, 29, 10, 0, 0, 0, time.UTC)}
	store := newStore(time.Hour, time.Hour, clock.Now)
	t.Cleanup(store.Close)
	store.Update("draft", func(d *Draft) { d.LocationID = 42 })

	clock.Advance(45 * time.Minute)
	if _, ok := store.Get("draft"); !ok {
		t.Fatal("Get() expired a draft before its TTL")
	}
	clock.Advance(45 * time.Minute)
	if draft, ok := store.Get("draft"); !ok || draft.LocationID != 42 {
		t.Fatalf("Get() after refreshed TTL = (%#v, %t), want saved draft and true", draft, ok)
	}
	clock.Advance(time.Hour + time.Nanosecond)
	if _, ok := store.Get("draft"); ok {
		t.Fatal("Get() kept a draft beyond the refreshed TTL")
	}
}

func TestStoreUpdateReplacesExpiredDraftWithDefaults(t *testing.T) {
	clock := &testClock{now: time.Date(2026, time.August, 29, 10, 7, 30, 0, time.UTC)}
	store := newStore(time.Hour, time.Hour, clock.Now)
	t.Cleanup(store.Close)
	store.Update("draft", func(d *Draft) {
		d.LocationID = 42
		d.ParticipantIDs = []int64{1}
	})

	clock.Advance(time.Hour + time.Nanosecond)
	draft := store.Update("draft", func(d *Draft) { d.DriverIDs = []int64{2} })
	if draft.LocationID != 0 || len(draft.ParticipantIDs) != 0 {
		t.Fatalf("Update() retained expired state: %#v", draft)
	}
	if got, want := draft.RouteTime, "11:15"; got != want {
		t.Fatalf("RouteTime = %q, want %q", got, want)
	}
	if got, want := draft.Mode, string(models.RouteModeDropoff); got != want {
		t.Fatalf("Mode = %q, want %q", got, want)
	}
	if draft.DriverVehicleIDs == nil {
		t.Fatal("DriverVehicleIDs is nil")
	}
}

func TestDefaultDraftRoundsTimeLikeDesktop(t *testing.T) {
	tests := []struct {
		name string
		now  time.Time
		want string
	}{
		{name: "round up", now: time.Date(2026, time.August, 29, 10, 7, 59, 0, time.UTC), want: "10:15"},
		{name: "keep boundary", now: time.Date(2026, time.August, 29, 10, 15, 59, 0, time.UTC), want: "10:15"},
		{name: "next hour", now: time.Date(2026, time.August, 29, 10, 59, 59, 0, time.UTC), want: "11:00"},
		{name: "next day", now: time.Date(2026, time.August, 29, 23, 59, 59, 0, time.UTC), want: "00:00"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			draft := defaultDraft(test.now)
			if draft.RouteTime != test.want {
				t.Fatalf("RouteTime = %q, want %q", draft.RouteTime, test.want)
			}
			if draft.Mode != string(models.RouteModeDropoff) {
				t.Fatalf("Mode = %q, want %q", draft.Mode, models.RouteModeDropoff)
			}
			if draft.DriverVehicleIDs == nil {
				t.Fatal("DriverVehicleIDs is nil")
			}
		})
	}
}

func TestStoreCleanupRemovesExpiredDrafts(t *testing.T) {
	clock := &testClock{now: time.Date(2026, time.August, 29, 10, 0, 0, 0, time.UTC)}
	store := newStore(time.Hour, time.Millisecond, clock.Now)
	t.Cleanup(store.Close)
	store.Update("expired", func(d *Draft) { d.LocationID = 42 })
	clock.Advance(time.Hour + time.Nanosecond)

	deadline := time.Now().Add(time.Second)
	for {
		store.mu.Lock()
		_, exists := store.drafts["expired"]
		store.mu.Unlock()
		if !exists {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("cleanup did not remove expired draft")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestStoreCopiesDraftState(t *testing.T) {
	store := NewStore()
	t.Cleanup(store.Close)
	id := store.NewID()
	updated := store.Update(id, func(d *Draft) {
		d.ParticipantIDs = []int64{1, 2}
		d.DriverIDs = []int64{3, 4}
		d.DriverVehicleIDs[7] = 9
	})
	updated.ParticipantIDs[0] = 98
	updated.DriverIDs[0] = 97
	updated.DriverVehicleIDs[7] = 96

	draft, ok := store.Get(id)
	if !ok {
		t.Fatal("Get() did not find updated draft")
	}
	draft.ParticipantIDs[0] = 99
	draft.DriverIDs[0] = 88
	draft.DriverVehicleIDs[7] = 77

	got, ok := store.Get(id)
	if !ok {
		t.Fatal("second Get() did not find updated draft")
	}
	if got.ParticipantIDs[0] != 1 || got.DriverIDs[0] != 3 || got.DriverVehicleIDs[7] != 9 {
		t.Fatalf("store leaked mutable state: %#v", got)
	}
}

func TestStoreNewIDIsRandomHex(t *testing.T) {
	store := NewStore()
	t.Cleanup(store.Close)
	first := store.NewID()
	second := store.NewID()
	if len(first) != 32 {
		t.Fatalf("NewID() length = %d, want 32", len(first))
	}
	if _, err := hex.DecodeString(first); err != nil {
		t.Fatalf("NewID() = %q, want hex: %v", first, err)
	}
	if first == second {
		t.Fatalf("two NewID() calls returned %q", first)
	}
}

func TestStoreCloseIsIdempotent(t *testing.T) {
	store := NewStore()
	store.Close()
	store.Close()
}
