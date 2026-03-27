package handlers

import (
	"sync"
	"testing"
	"time"

	"ride-home-router/internal/models"
)

func TestDeepCopy_Independence(t *testing.T) {
	original := []models.CalculatedRoute{
		{
			Driver: &models.Driver{
				ID:              1,
				Name:            "OriginalDriver",
				VehicleCapacity: 4,
			},
			Stops: []models.RouteStop{
				{
					Order: 0,
					Participant: &models.Participant{
						ID:   1,
						Name: "Alice",
						Lat:  1.0,
						Lng:  2.0,
					},
					DistanceFromPrevMeters:   1000,
					CumulativeDistanceMeters: 1000,
				},
				{
					Order: 1,
					Participant: &models.Participant{
						ID:   2,
						Name: "Bob",
						Lat:  3.0,
						Lng:  4.0,
					},
					DistanceFromPrevMeters:   500,
					CumulativeDistanceMeters: 1500,
				},
			},
			TotalDropoffDistanceMeters: 1500,
			TotalDistanceMeters:        2000,
		},
	}

	copied := deepCopyRoutes(original)

	// Modify the copy
	copied[0].Driver.Name = "ModifiedDriver"
	copied[0].Stops[0].Participant.Name = "ModifiedAlice"
	copied[0].TotalDropoffDistanceMeters = 9999

	// Verify original is unchanged
	if original[0].Driver.Name != "OriginalDriver" {
		t.Errorf("original driver name changed: got %s", original[0].Driver.Name)
	}
	if original[0].Stops[0].Participant.Name != "Alice" {
		t.Errorf("original participant name changed: got %s", original[0].Stops[0].Participant.Name)
	}
	if original[0].TotalDropoffDistanceMeters != 1500 {
		t.Errorf("original distance changed: got %f", original[0].TotalDropoffDistanceMeters)
	}

	// Verify copy has the modifications
	if copied[0].Driver.Name != "ModifiedDriver" {
		t.Errorf("copy driver name not modified: got %s", copied[0].Driver.Name)
	}
	if copied[0].Stops[0].Participant.Name != "ModifiedAlice" {
		t.Errorf("copy participant name not modified: got %s", copied[0].Stops[0].Participant.Name)
	}
}

func TestDeepCopy_NilPointerHandling(t *testing.T) {
	// Test with nil driver
	routes := []models.CalculatedRoute{
		{
			Driver: nil,
			Stops:  []models.RouteStop{},
		},
	}

	copied := deepCopyRoutes(routes)

	if copied[0].Driver != nil {
		t.Error("expected nil driver in copy")
	}

	// Test with nil participant in stop
	routes2 := []models.CalculatedRoute{
		{
			Driver: &models.Driver{ID: 1, Name: "Driver"},
			Stops: []models.RouteStop{
				{
					Order:       0,
					Participant: nil,
				},
			},
		},
	}

	copied2 := deepCopyRoutes(routes2)

	if copied2[0].Stops[0].Participant != nil {
		t.Error("expected nil participant in copied stop")
	}
}

func TestSessionStore_ConcurrentAccess(t *testing.T) {
	store := NewRouteSessionStore()
	t.Cleanup(store.Close)

	routes := []models.CalculatedRoute{
		{
			Driver: &models.Driver{ID: 1, Name: "Driver1", VehicleCapacity: 4},
			Stops:  []models.RouteStop{},
		},
	}
	drivers := []models.Driver{
		{ID: 1, Name: "Driver1", VehicleCapacity: 4},
	}
	activityLoc := &models.ActivityLocation{ID: 1, Name: "HQ", Lat: 0, Lng: 0}

	// Create initial session
	session := store.Create(routes, drivers, activityLoc, false, "18:30", "dropoff", nil)
	sessionID := session.ID

	var wg sync.WaitGroup
	numGoroutines := 100

	// Concurrent reads
	for range numGoroutines {
		wg.Go(func() {
			s := store.Get(sessionID)
			if s == nil {
				t.Error("expected session to exist")
			}
		})
	}

	// Concurrent updates
	for range numGoroutines {
		wg.Go(func() {
			store.Update(sessionID, func(s *RouteSession) {
				// Just access the session
				_ = len(s.CurrentRoutes)
			})
		})
	}

	wg.Wait()

	// Verify session still exists and is valid
	finalSession := store.Get(sessionID)
	if finalSession == nil {
		t.Fatal("session should still exist after concurrent access")
	}
	if finalSession.ID != sessionID {
		t.Errorf("session ID mismatch: expected %s, got %s", sessionID, finalSession.ID)
	}
}

func TestSessionStore_CreateAndGet(t *testing.T) {
	store := NewRouteSessionStore()
	t.Cleanup(store.Close)

	routes := []models.CalculatedRoute{
		{
			Driver: &models.Driver{ID: 1, Name: "Driver1", VehicleCapacity: 4},
			Stops: []models.RouteStop{
				{Order: 0, Participant: &models.Participant{ID: 1, Name: "Alice"}},
			},
			TotalDistanceMeters: 1000,
		},
	}
	drivers := []models.Driver{
		{ID: 1, Name: "Driver1", VehicleCapacity: 4},
	}
	activityLoc := &models.ActivityLocation{ID: 1, Name: "HQ"}

	session := store.Create(routes, drivers, activityLoc, true, "18:30", "dropoff", nil)

	if session.ID == "" {
		t.Error("session should have an ID")
	}
	if len(session.ID) != 32 { // 16 bytes * 2 (hex encoding)
		t.Errorf("session ID should be 32 hex chars, got %d", len(session.ID))
	}
	if !session.UseMiles {
		t.Error("UseMiles should be true")
	}
	if session.RouteTime != "18:30" {
		t.Errorf("RouteTime = %q, want %q", session.RouteTime, "18:30")
	}
	if len(session.OriginalRoutes) != 1 {
		t.Errorf("expected 1 original route, got %d", len(session.OriginalRoutes))
	}
	if len(session.CurrentRoutes) != 1 {
		t.Errorf("expected 1 current route, got %d", len(session.CurrentRoutes))
	}

	// Verify Get returns the session
	retrieved := store.Get(session.ID)
	if retrieved == nil {
		t.Fatal("should be able to get session by ID")
	}
	if retrieved.ID != session.ID {
		t.Errorf("session IDs don't match")
	}

	// Verify Get returns nil for unknown ID
	unknown := store.Get("nonexistent")
	if unknown != nil {
		t.Error("should return nil for unknown session ID")
	}
}

func TestSessionStore_Delete(t *testing.T) {
	store := NewRouteSessionStore()
	t.Cleanup(store.Close)

	routes := []models.CalculatedRoute{}
	drivers := []models.Driver{}
	activityLoc := &models.ActivityLocation{ID: 1}

	session := store.Create(routes, drivers, activityLoc, false, "18:30", "dropoff", nil)
	sessionID := session.ID

	// Verify session exists
	if store.Get(sessionID) == nil {
		t.Fatal("session should exist before delete")
	}

	// Delete
	store.Delete(sessionID)

	// Verify session no longer exists
	if store.Get(sessionID) != nil {
		t.Error("session should not exist after delete")
	}
}

func TestSessionStore_GetExpiresIdleSession(t *testing.T) {
	store := newRouteSessionStore(50*time.Millisecond, time.Hour)
	t.Cleanup(store.Close)

	session := store.Create(nil, nil, &models.ActivityLocation{ID: 1}, false, "18:30", "dropoff", nil)
	session.LastAccessedAt = time.Now().Add(-time.Second)

	if got := store.Get(session.ID); got != nil {
		t.Fatal("expected expired session to be removed on get")
	}
}

func TestSessionStore_GetTouchesSession(t *testing.T) {
	store := newRouteSessionStore(50*time.Millisecond, time.Hour)
	t.Cleanup(store.Close)

	session := store.Create(nil, nil, &models.ActivityLocation{ID: 1}, false, "08:15", "pickup", nil)

	time.Sleep(30 * time.Millisecond)
	if got := store.Get(session.ID); got == nil {
		t.Fatal("expected session to still exist after first access")
	}

	time.Sleep(30 * time.Millisecond)
	if got := store.Get(session.ID); got == nil {
		t.Fatal("expected session access to extend TTL")
	}
}

func TestSessionStore_DeleteExpiredRemovesStaleSessions(t *testing.T) {
	store := newRouteSessionStore(time.Hour, time.Hour)
	t.Cleanup(store.Close)

	session := store.Create(nil, nil, &models.ActivityLocation{ID: 1}, false, "18:30", "dropoff", nil)
	session.LastAccessedAt = time.Now().Add(-2 * time.Hour)

	store.deleteExpired(time.Now())

	if got := store.Get(session.ID); got != nil {
		t.Fatal("expected stale session to be removed during cleanup")
	}
}
