package postgres_test

import (
	"context"
	"errors"
	"ride-home-router/internal/database"
	"ride-home-router/internal/postgres/postgrestest"
	"sync"
	"testing"
)

func TestGoogleUsageReservesUntilTheMonthlyCeiling(t *testing.T) {
	db := postgrestest.Open(t)
	ledger := db.GoogleUsage()
	if err := ledger.SetCeiling(t.Context(), database.UsageSKURoutes, 5); err != nil {
		t.Fatal(err)
	}
	for i := range 5 {
		if err := ledger.Reserve(t.Context(), database.UsageSKURoutes, 1); err != nil {
			t.Fatalf("reserve %d: %v", i, err)
		}
	}
	if err := ledger.Reserve(t.Context(), database.UsageSKURoutes, 1); !errors.Is(err, database.ErrUsageExhausted) {
		t.Fatalf("sixth reserve = %v, want ErrUsageExhausted", err)
	}
	// Other SKUs have their own allowance.
	if err := ledger.Reserve(t.Context(), database.UsageSKUGeocoding, 3); err != nil {
		t.Fatalf("geocoding reserve: %v", err)
	}
	used, err := ledger.Reserved(t.Context(), database.UsageSKURoutes)
	if err != nil || used != 5 {
		t.Fatalf("Reserved() = (%d, %v), want 5", used, err)
	}
	if err := ledger.Reserve(t.Context(), database.UsageSKURoutes, 0); err == nil {
		t.Fatal("zero-count reservation should be rejected")
	}
}

func TestGoogleUsageBatchReservationIsAllOrNothing(t *testing.T) {
	db := postgrestest.Open(t)
	ledger := db.GoogleUsage()
	if err := ledger.SetCeiling(t.Context(), database.UsageSKURoutes, 10); err != nil {
		t.Fatal(err)
	}
	if err := ledger.Reserve(t.Context(), database.UsageSKURoutes, 8); err != nil {
		t.Fatal(err)
	}
	if err := ledger.Reserve(t.Context(), database.UsageSKURoutes, 3); !errors.Is(err, database.ErrUsageExhausted) {
		t.Fatalf("batch over ceiling = %v, want ErrUsageExhausted", err)
	}
	if used, _ := ledger.Reserved(t.Context(), database.UsageSKURoutes); used != 8 {
		t.Fatalf("Reserved() = %d after a refused batch, want 8", used)
	}
}

func TestGoogleUsageConcurrentReplicasNeverExceedTheCeiling(t *testing.T) {
	url := postgrestest.DatabaseURL(t)
	a := postgrestest.OpenURL(t, url)
	b := postgrestest.OpenURL(t, url)
	if err := a.GoogleUsage().SetCeiling(t.Context(), database.UsageSKURoutes, 10); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	granted := 0
	for i := range 30 {
		ledger := a.GoogleUsage()
		if i%2 == 1 {
			ledger = b.GoogleUsage()
		}
		wg.Go(func() {
			if err := ledger.Reserve(context.Background(), database.UsageSKURoutes, 1); err == nil {
				mu.Lock()
				granted++
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	if granted != 10 {
		t.Fatalf("granted = %d, want exactly the ceiling of 10", granted)
	}
}

func TestGoogleUsageDefaultCeilingsAndSeeding(t *testing.T) {
	db := postgrestest.Open(t)
	ledger := db.GoogleUsage()
	// Without SetCeiling the built-in application ceiling applies.
	if err := ledger.Reserve(t.Context(), database.UsageSKURoutes, database.UsageDefaultCeiling); err != nil {
		t.Fatalf("reserving the whole default ceiling: %v", err)
	}
	if err := ledger.Reserve(t.Context(), database.UsageSKURoutes, 1); !errors.Is(err, database.ErrUsageExhausted) {
		t.Fatalf("one past the default ceiling = %v", err)
	}
	// Seeding records usage that happened before the ledger existed this month.
	if err := ledger.Seed(t.Context(), database.UsageSKUAutocomplete, 7000); err != nil {
		t.Fatal(err)
	}
	if err := ledger.Reserve(t.Context(), database.UsageSKUAutocomplete, 1001); !errors.Is(err, database.ErrUsageExhausted) {
		t.Fatalf("seeded month should leave only 1000: %v", err)
	}
	if err := ledger.Seed(t.Context(), database.UsageSKUAutocomplete, 10); err != nil {
		t.Fatal(err)
	}
	if used, _ := ledger.Reserved(t.Context(), database.UsageSKUAutocomplete); used != 7000 {
		t.Fatalf("Seed() must never lower usage: %d", used)
	}
}
