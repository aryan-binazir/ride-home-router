package routesession

import (
	"context"
	"errors"
	"ride-home-router/internal/database"
	"ride-home-router/internal/postgres/postgrestest"
	"testing"
	"time"
)

func TestCoordinateDeadlineSurvivesSlidingAccess(t *testing.T) {
	for _, persistent := range []bool{false, true} {
		name := "memory"
		if persistent {
			name = "postgres"
		}
		t.Run(name, func(t *testing.T) {
			now := time.Now()
			store := newStore(nil, DefaultTTL, time.Hour, func() time.Time { return now })
			t.Cleanup(store.Close)
			if persistent {
				store = NewPersistentStore(nil, postgrestest.Open(t).Workflows())
				store.now = func() time.Time { return now }
			}
			deadline := now.Add(9 * time.Hour)
			created, err := store.CreateContext(t.Context(), CreateInput{CoordinatesFreshUntil: deadline})
			if err != nil {
				t.Fatal(err)
			}
			for range 2 {
				now = now.Add(4 * time.Hour)
				got, ok, err := store.Load(t.Context(), created.ID)
				if err != nil || !ok || !got.CoordinatesFreshUntil.Equal(deadline) {
					t.Fatalf("load: %+v %v %v", got, ok, err)
				}
				if _, err := store.ResetContext(t.Context(), created.ID); err != nil {
					t.Fatal(err)
				}
			}
			now = deadline
			operations := map[string]func() error{
				"move":  func() error { _, e := store.ApplyMoves(t.Context(), created.ID, nil, ApplyMovesOptions{}); return e },
				"swap":  func() error { _, e := store.SwapDrivers(t.Context(), created.ID, 0, 1); return e },
				"reset": func() error { _, e := store.ResetContext(t.Context(), created.ID); return e },
				"add":   func() error { _, e := store.AddDriver(t.Context(), created.ID, 1); return e },
				"save": func() error {
					return store.CommitEvent(t.Context(), created.ID, func(context.Context, CommitSnapshot, database.WorkflowWrites) error {
						t.Error("expired save called persistence")
						return nil
					})
				},
			}
			for name, op := range operations {
				if err := op(); !errors.Is(err, ErrNotFound) {
					t.Errorf("%s: %v", name, err)
				}
			}
			fresh, err := store.CreateContext(t.Context(), CreateInput{CoordinatesFreshUntil: now.Add(time.Hour)})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.ResetContext(t.Context(), fresh.ID); err != nil {
				t.Fatalf("fresh reset: %v", err)
			}
		})
	}
}

func TestLegacyPersistentPlanRequiresRecalculation(t *testing.T) {
	db := postgrestest.Open(t)
	if err := db.Workflows().Create(t.Context(), "route", "legacy-without-deadline", []byte(`{"Original":[],"Current":[]}`), DefaultTTL); err != nil {
		t.Fatal(err)
	}
	store := NewPersistentStore(nil, db.Workflows())
	if _, ok, err := store.Load(t.Context(), "legacy-without-deadline"); err != nil || ok {
		t.Fatalf("legacy load: found=%v err=%v", ok, err)
	}
	if err := store.CommitEvent(t.Context(), "legacy-without-deadline", func(context.Context, CommitSnapshot, database.WorkflowWrites) error {
		t.Error("legacy plan saved")
		return nil
	}); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}
