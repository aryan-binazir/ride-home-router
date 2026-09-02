package plandraft

import (
	"encoding/hex"
	"fmt"
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

func TestStoreUpdateIncrementsRevisionAndRejectsStaleSessionSet(t *testing.T) {
	store := NewStore()
	t.Cleanup(store.Close)

	draft := store.Update("draft", func(d *Draft) { d.LocationID = 1 })
	if draft.Revision != 1 {
		t.Fatalf("first revision = %d, want 1", draft.Revision)
	}
	staleRevision := draft.Revision
	draft = store.Update("draft", func(d *Draft) { d.LocationID = 2 })
	if draft.Revision != 2 {
		t.Fatalf("second revision = %d, want 2", draft.Revision)
	}

	if displaced, ok := store.SetRouteSessionIDIfUnchanged("draft", staleRevision, "stale-session"); ok || displaced != "" {
		t.Fatalf("stale set = (%q, %t), want empty and false", displaced, ok)
	}
	got, ok := store.Get("draft")
	if !ok || got.RouteSessionID != "" || got.Revision != 2 {
		t.Fatalf("draft after stale set = (%#v, %t), want unchanged revision 2", got, ok)
	}

	if displaced, ok := store.SetRouteSessionIDIfUnchanged("draft", draft.Revision, "current-session"); !ok || displaced != "" {
		t.Fatalf("current set = (%q, %t), want empty and true", displaced, ok)
	}
	got, ok = store.Get("draft")
	if !ok || got.RouteSessionID != "current-session" || got.Revision != 3 {
		t.Fatalf("draft after current set = (%#v, %t), want session and revision 3", got, ok)
	}
}

func TestStoreSessionSetReturnsOnlyTheDisplacedSession(t *testing.T) {
	store := NewStore()
	t.Cleanup(store.Close)

	draft := store.Update("draft", func(d *Draft) { d.RouteSessionID = "old-session" })
	displaced, ok := store.SetRouteSessionIDIfUnchanged("draft", draft.Revision, "new-session")
	if !ok || displaced != "old-session" {
		t.Fatalf("session set = (%q, %t), want old-session and true", displaced, ok)
	}
	got, found := store.Get("draft")
	if !found || got.RouteSessionID != "new-session" || got.Revision != 2 {
		t.Fatalf("updated draft = (%#v, %t), want new-session at revision 2", got, found)
	}
}

func TestStoreSessionClearPreservesNewerSession(t *testing.T) {
	store := NewStore()
	t.Cleanup(store.Close)

	draft := store.Update("draft", func(d *Draft) { d.RouteSessionID = "saved-session" })
	if _, ok := store.SetRouteSessionIDIfUnchanged("draft", draft.Revision, "newer-session"); !ok {
		t.Fatal("failed to attach newer session")
	}
	if store.ClearRouteSessionIDIfCurrent("draft", "saved-session") {
		t.Fatal("clear accepted a displaced session ID")
	}
	got, ok := store.Get("draft")
	if !ok || got.RouteSessionID != "newer-session" || got.Revision != 2 {
		t.Fatalf("draft after stale clear = (%#v, %t), want newer-session at revision 2", got, ok)
	}
	if !store.ClearRouteSessionIDIfCurrent("draft", "newer-session") {
		t.Fatal("clear rejected the current session ID")
	}
	got, ok = store.Get("draft")
	if !ok || got.RouteSessionID != "" || got.Revision != 3 {
		t.Fatalf("draft after current clear = (%#v, %t), want empty session at revision 3", got, ok)
	}
}

func TestStoreConditionalSessionWritesDoNotCreateOrReviveDrafts(t *testing.T) {
	clock := &testClock{now: time.Date(2026, time.August, 29, 10, 0, 0, 0, time.UTC)}
	store := newStore(time.Hour, time.Hour, clock.Now)
	t.Cleanup(store.Close)

	if _, ok := store.SetRouteSessionIDIfUnchanged("missing", 0, "session"); ok {
		t.Fatal("conditional set created a missing draft")
	}
	if store.ClearRouteSessionIDIfCurrent("missing", "session") {
		t.Fatal("conditional clear created a missing draft")
	}
	store.mu.Lock()
	draftCount := len(store.drafts)
	store.mu.Unlock()
	if draftCount != 0 {
		t.Fatalf("conditional writes inserted %d missing drafts, want 0", draftCount)
	}

	setDraft := store.Update("expired-set", func(d *Draft) { d.RouteSessionID = "old-session" })
	store.Update("expired-clear", func(d *Draft) { d.RouteSessionID = "old-session" })
	clock.Advance(time.Hour + time.Nanosecond)
	if _, ok := store.SetRouteSessionIDIfUnchanged("expired-set", setDraft.Revision, "new-session"); ok {
		t.Fatal("conditional set revived an expired draft")
	}
	if store.ClearRouteSessionIDIfCurrent("expired-clear", "old-session") {
		t.Fatal("conditional clear revived an expired draft")
	}
	if _, ok := store.Get("expired-set"); ok {
		t.Fatal("expired set draft remains after conditional write")
	}
	if _, ok := store.Get("expired-clear"); ok {
		t.Fatal("expired clear draft remains after conditional write")
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

func TestStoreEvictsLeastRecentlyAccessedDraftAtCapacity(t *testing.T) {
	clock := &testClock{now: time.Unix(100, 0)}
	store := newStore(time.Hour, time.Hour, clock.Now)
	t.Cleanup(store.Close)
	ids := make([]string, MaxConcurrentDrafts)
	for index := range MaxConcurrentDrafts {
		ids[index] = fmt.Sprintf("draft-%d", index)
		store.Update(ids[index], func(d *Draft) { d.LocationID = int64(index + 1) })
		clock.Advance(time.Second)
	}
	if _, ok := store.Get(ids[0]); !ok {
		t.Fatal("failed to touch the oldest-created draft")
	}
	clock.Advance(time.Second)
	store.Update("new-draft", func(d *Draft) { d.LocationID = 999 })

	if _, ok := store.Get(ids[1]); ok {
		t.Fatal("least-recently-accessed draft survived bounded-store eviction")
	}
	if draft, ok := store.Get(ids[0]); !ok || draft.LocationID != 1 {
		t.Fatalf("recently touched draft = %#v, found=%v", draft, ok)
	}
	if draft, ok := store.Get("new-draft"); !ok || draft.LocationID != 999 {
		t.Fatalf("new draft = %#v, found=%v", draft, ok)
	}
}

func TestStoreCapsDraftSelections(t *testing.T) {
	store := NewStore()
	t.Cleanup(store.Close)
	ids := make([]int64, MaxSelectionSize+1)
	assignments := make(map[int64]int64, len(ids))
	for index := range ids {
		ids[index] = int64(index + 1)
		assignments[ids[index]] = int64(index + 1000)
	}
	draft := store.Update("bounded", func(d *Draft) {
		d.ParticipantIDs = ids
		d.DriverIDs = ids
		d.DriverVehicleIDs = assignments
	})
	if len(draft.ParticipantIDs) != MaxSelectionSize || len(draft.DriverIDs) != MaxSelectionSize || len(draft.DriverVehicleIDs) != MaxSelectionSize {
		t.Fatalf("bounded draft sizes = participants:%d drivers:%d assignments:%d", len(draft.ParticipantIDs), len(draft.DriverIDs), len(draft.DriverVehicleIDs))
	}
	if _, exists := draft.DriverVehicleIDs[ids[MaxSelectionSize]]; exists {
		t.Fatal("assignment for a capped driver survived")
	}
}
