package postgres_test

import (
	"ride-home-router/internal/postgres/postgrestest"
	"strings"
	"testing"
)

func TestReadinessCheckRequiresLatestMigration(t *testing.T) {
	t.Run("latest migration", func(t *testing.T) {
		store := postgrestest.Open(t)
		if err := store.ReadinessCheck(t.Context()); err != nil {
			t.Fatalf("ReadinessCheck() error = %v", err)
		}
	})

	t.Run("unmigrated schema", func(t *testing.T) {
		store := openStore(t, postgrestest.UnmigratedDatabase(t))
		err := store.ReadinessCheck(t.Context())
		if err == nil || !strings.Contains(err.Error(), "schema migration version 0") {
			t.Fatalf("ReadinessCheck() error = %v, want migration version mismatch", err)
		}
	})
}

func TestReadinessCheckRejectsDirtyOrUnexpectedMigration(t *testing.T) {
	for _, tt := range []struct {
		name      string
		mutation  string
		wantError string
	}{
		{name: "dirty migration", mutation: "UPDATE schema_migrations SET dirty = true", wantError: "is dirty"},
		{name: "schema ahead of build", mutation: "UPDATE schema_migrations SET version = 20260831000000", wantError: "does not match expected version"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			databaseURL := postgrestest.DatabaseURL(t)
			store := openStore(t, databaseURL)
			execSQL(t, databaseURL, tt.mutation)

			err := store.ReadinessCheck(t.Context())
			if err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("ReadinessCheck() error = %v, want %q", err, tt.wantError)
			}
		})
	}
}
