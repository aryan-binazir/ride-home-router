package migrations_test

import (
	"database/sql"
	"embed"
	"ride-home-router/internal/postgres/postgrestest"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	migratepgx "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

//go:embed *.sql
var migrationFiles embed.FS

func TestSMERouteFeedbackMigrationAppliesDownAndUp(t *testing.T) {
	databaseURL := postgrestest.DatabaseURL(t)
	config, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse database URL: %v", err)
	}
	db := stdlib.OpenDB(*config)
	db.SetMaxOpenConns(2)

	source, err := iofs.New(migrationFiles, ".")
	if err != nil {
		t.Fatalf("open embedded migrations: %v", err)
	}
	driver, err := migratepgx.WithInstance(db, &migratepgx.Config{MigrationsTable: "schema_migrations"})
	if err != nil {
		t.Fatalf("open migration database driver: %v", err)
	}
	migrator, err := migrate.NewWithInstance("iofs", source, "pgx5", driver)
	if err != nil {
		t.Fatalf("create migrator: %v", err)
	}
	t.Cleanup(func() {
		if sourceErr, databaseErr := migrator.Close(); sourceErr != nil || databaseErr != nil {
			t.Errorf("close migrator: source=%v database=%v", sourceErr, databaseErr)
		}
	})

	assertFeedbackSchema(t, db, true)
	if err := migrator.Steps(-1); err != nil {
		t.Fatalf("migrate down: %v", err)
	}
	assertFeedbackSchema(t, db, false)
	if err := migrator.Steps(1); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	assertFeedbackSchema(t, db, true)
}

func assertFeedbackSchema(t *testing.T, db *sql.DB, want bool) {
	t.Helper()
	var tableExists bool
	if err := db.QueryRowContext(t.Context(), `SELECT to_regclass(current_schema() || '.route_feedback') IS NOT NULL`).Scan(&tableExists); err != nil {
		t.Fatalf("query route_feedback table: %v", err)
	}
	var columnExists bool
	if err := db.QueryRowContext(t.Context(), `
		SELECT EXISTS (
			SELECT 1
			FROM information_schema.columns
			WHERE table_schema = current_schema()
			  AND table_name = 'settings'
			  AND column_name = 'sme_email'
		)`).Scan(&columnExists); err != nil {
		t.Fatalf("query settings.sme_email: %v", err)
	}
	if tableExists != want || columnExists != want {
		t.Fatalf("feedback schema exists = table:%v column:%v, want %v", tableExists, columnExists, want)
	}
}
