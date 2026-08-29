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
	db, migrator := openMigrator(t)

	assertFeedbackSchema(t, db, true)
	if err := migrator.Migrate(20260826000000); err != nil {
		t.Fatalf("migrate down: %v", err)
	}
	assertFeedbackSchema(t, db, false)
	if err := migrator.Migrate(20260830000000); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	assertFeedbackSchema(t, db, true)
}

func TestSoftDeleteRosterMigrationRemovesArchivedRowsOnDown(t *testing.T) {
	db, migrator := openMigrator(t)
	ctx := t.Context()

	var liveParticipantID, archivedParticipantID int64
	if err := db.QueryRowContext(ctx, `INSERT INTO participants (name, address, lat, lng) VALUES ('Live Rider', '1 Main St', 40, -73) RETURNING id`).Scan(&liveParticipantID); err != nil {
		t.Fatalf("insert live participant: %v", err)
	}
	if err := db.QueryRowContext(ctx, `INSERT INTO participants (name, address, lat, lng, deleted_at) VALUES ('Archived Rider', '2 Main St', 40, -73, now()) RETURNING id`).Scan(&archivedParticipantID); err != nil {
		t.Fatalf("insert archived participant: %v", err)
	}
	var liveDriverID, archivedDriverID int64
	if err := db.QueryRowContext(ctx, `INSERT INTO drivers (name, address, lat, lng) VALUES ('Live Driver', '3 Main St', 40, -73) RETURNING id`).Scan(&liveDriverID); err != nil {
		t.Fatalf("insert live driver: %v", err)
	}
	if err := db.QueryRowContext(ctx, `INSERT INTO drivers (name, address, lat, lng, deleted_at) VALUES ('Archived Driver', '4 Main St', 40, -73, now()) RETURNING id`).Scan(&archivedDriverID); err != nil {
		t.Fatalf("insert archived driver: %v", err)
	}
	var archivedLocationID int64
	if _, err := db.ExecContext(ctx, `INSERT INTO activity_locations (name, address, lat, lng) VALUES ('Live Gym', '5 Main St', 40, -73)`); err != nil {
		t.Fatalf("insert live activity location: %v", err)
	}
	if err := db.QueryRowContext(ctx, `INSERT INTO activity_locations (name, address, lat, lng, deleted_at) VALUES ('Archived Gym', '6 Main St', 40, -73, now()) RETURNING id`).Scan(&archivedLocationID); err != nil {
		t.Fatalf("insert archived activity location: %v", err)
	}
	var labelID int64
	if err := db.QueryRowContext(ctx, `INSERT INTO labels (name) VALUES ('Retained') RETURNING id`).Scan(&labelID); err != nil {
		t.Fatalf("insert label: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO participant_labels (label_id, participant_id) VALUES ($1, $2), ($1, $3)`, labelID, liveParticipantID, archivedParticipantID); err != nil {
		t.Fatalf("insert participant labels: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO driver_labels (label_id, driver_id) VALUES ($1, $2), ($1, $3)`, labelID, liveDriverID, archivedDriverID); err != nil {
		t.Fatalf("insert driver labels: %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE settings SET selected_activity_location_id = $1 WHERE id = 1`, archivedLocationID); err != nil {
		t.Fatalf("select archived activity location: %v", err)
	}

	if err := migrator.Migrate(20260829000000); err != nil {
		t.Fatalf("migrate soft-delete roster down: %v", err)
	}
	for table, wantName := range map[string]string{
		"participants":       "Live Rider",
		"drivers":            "Live Driver",
		"activity_locations": "Live Gym",
	} {
		var count int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table+` WHERE name = $1`, wantName).Scan(&count); err != nil || count != 1 {
			t.Fatalf("live row in %s after down = %d, err=%v; want 1", table, count, err)
		}
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table).Scan(&count); err != nil || count != 1 {
			t.Fatalf("total rows in %s after down = %d, err=%v; want archived row removed", table, count, err)
		}
	}
	for _, table := range []string{"participant_labels", "driver_labels"} {
		var count int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table).Scan(&count); err != nil || count != 1 {
			t.Fatalf("rows in %s after down = %d, err=%v; want archived membership cascaded", table, count, err)
		}
	}
	var selectedLocationID sql.NullInt64
	if err := db.QueryRowContext(ctx, `SELECT selected_activity_location_id FROM settings WHERE id = 1`).Scan(&selectedLocationID); err != nil || selectedLocationID.Valid {
		t.Fatalf("selected location after down = %#v, err=%v; want NULL", selectedLocationID, err)
	}

	if err := migrator.Migrate(20260830000000); err != nil {
		t.Fatalf("migrate soft-delete roster up: %v", err)
	}
	assertSoftDeleteColumns(t, db, true)
}

func openMigrator(t *testing.T) (*sql.DB, *migrate.Migrate) {
	t.Helper()
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
	return db, migrator
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

func assertSoftDeleteColumns(t *testing.T, db *sql.DB, want bool) {
	t.Helper()
	for _, table := range []string{"participants", "drivers", "activity_locations"} {
		var exists bool
		if err := db.QueryRowContext(t.Context(), `
			SELECT EXISTS (
				SELECT 1 FROM information_schema.columns
				WHERE table_schema = current_schema() AND table_name = $1 AND column_name = 'deleted_at'
			)`, table).Scan(&exists); err != nil {
			t.Fatalf("query %s.deleted_at: %v", table, err)
		}
		if exists != want {
			t.Fatalf("%s.deleted_at exists = %v, want %v", table, exists, want)
		}
	}
}
