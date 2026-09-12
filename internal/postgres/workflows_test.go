package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"ride-home-router/internal/database"
	"ride-home-router/internal/postgres"
	"ride-home-router/internal/postgres/postgrestest"
	"testing"
	"time"
)

func TestWorkflowCapacityPreservesExistingDrafts(t *testing.T) {
	db := postgrestest.Open(t)
	repo := db.Workflows()
	for i := range 256 {
		if err := repo.Create(t.Context(), "draft", fmt.Sprintf("%032x", i), []byte(`{}`), time.Hour); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.Create(t.Context(), "draft", fmt.Sprintf("%032x", 256), []byte(`{}`), time.Hour); !errors.Is(err, database.ErrWorkflowCapacity) {
		t.Fatalf("capacity error: %v", err)
	}
	if _, err := repo.Load(t.Context(), "draft", fmt.Sprintf("%032x", 0), time.Hour); err != nil {
		t.Fatalf("old draft was evicted: %v", err)
	}
}

func TestSharedNominatimGateHonorsOtherInstance(t *testing.T) {
	url := postgrestest.DatabaseURL(t)
	a, err := postgres.New(t.Context(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()
	b, err := postgres.New(t.Context(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = b.Close() }()
	if err = a.NominatimGate().Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if err = b.NominatimGate().Wait(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second instance skipped shared throttle: %v", err)
	}
}
