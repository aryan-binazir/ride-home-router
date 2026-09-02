package migrations_test

import (
	"context"
	"database/sql"
	"embed"
	"io/fs"
	"net/url"
	"regexp"
	"ride-home-router/internal/postgres/postgrestest"
	"ride-home-router/migrations"
	"strings"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	migratedatabase "github.com/golang-migrate/migrate/v4/database"
	migratepgx "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

//go:embed *.sql
var migrationFiles embed.FS

func TestLatestVersionMatchesNewestEmbeddedMigration(t *testing.T) {
	version, err := migrations.LatestVersion()
	if err != nil {
		t.Fatalf("LatestVersion() error = %v", err)
	}
	// Keep this literal independent of LatestVersion so every new migration
	// requires an explicit readiness expectation update.
	if version != 20260830000000 {
		t.Fatalf("LatestVersion() = %d, want 20260830000000", version)
	}
}

func TestEmbeddedMigrationsArePairedAndParseable(t *testing.T) {
	entries, err := fs.ReadDir(migrationFiles, ".")
	if err != nil {
		t.Fatalf("read embedded migrations: %v", err)
	}
	filename := regexp.MustCompile(`^([0-9]+_[a-z0-9_]+)\.(up|down)\.sql$`)
	directions := make(map[string]map[string]bool)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		matches := filename.FindStringSubmatch(entry.Name())
		if matches == nil {
			t.Errorf("migration filename %q is not parseable", entry.Name())
			continue
		}
		if directions[matches[1]] == nil {
			directions[matches[1]] = make(map[string]bool)
		}
		directions[matches[1]][matches[2]] = true
	}
	if len(directions) == 0 {
		t.Fatal("no embedded migrations found")
	}
	for migration, found := range directions {
		if !found["up"] || !found["down"] {
			t.Errorf("migration %s directions = %v, want paired up and down", migration, found)
		}
	}
}

func TestDisabledDownMigrationsContainNoExecutableSQL(t *testing.T) {
	entries, err := fs.ReadDir(migrationFiles, ".")
	if err != nil {
		t.Fatalf("read embedded migrations: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".down.sql") {
			continue
		}
		body, err := fs.ReadFile(migrationFiles, entry.Name())
		if err != nil {
			t.Fatalf("read %s: %v", entry.Name(), err)
		}
		if !strings.Contains(string(body), "ride-home-router: down migration disabled") {
			continue
		}
		for line := range strings.SplitSeq(string(body), "\n") {
			line = strings.TrimSpace(line)
			if line != "" && !strings.HasPrefix(line, "--") {
				t.Errorf("disabled down migration %s contains executable SQL: %q", entry.Name(), line)
			}
		}
	}
}

func TestVersionReportsLatestCleanMigration(t *testing.T) {
	databaseURL := postgrestest.DatabaseURL(t)

	version, dirty, err := migrations.Version(t.Context(), databaseURL)
	if err != nil {
		t.Fatalf("Version() error = %v", err)
	}
	if version != 20260830000000 || dirty {
		t.Fatalf("Version() = (%d, %t), want (20260830000000, false)", version, dirty)
	}
}

func TestVersionDoesNotCreateMigrationState(t *testing.T) {
	databaseURL := postgrestest.UnmigratedDatabase(t)

	version, dirty, err := migrations.Version(t.Context(), databaseURL)
	if err != nil {
		t.Fatalf("Version() error = %v", err)
	}
	if version != 0 || dirty {
		t.Fatalf("Version() = (%d, %t), want (0, false)", version, dirty)
	}

	connection, err := pgx.Connect(t.Context(), databaseURL)
	if err != nil {
		t.Fatalf("connect after Version: %v", err)
	}
	defer func() {
		if err := connection.Close(context.Background()); err != nil {
			t.Errorf("close Version inspection connection: %v", err)
		}
	}()
	var migrationTable bool
	if err := connection.QueryRow(t.Context(), "SELECT to_regclass(current_schema() || '.schema_migrations') IS NOT NULL").Scan(&migrationTable); err != nil {
		t.Fatalf("inspect schema after Version: %v", err)
	}
	if migrationTable {
		t.Fatal("Version() created schema_migrations, want read-only inspection")
	}
}

func TestVersionRejectsMissingCurrentSchema(t *testing.T) {
	databaseURL := postgrestest.UnmigratedDatabase(t)
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatalf("parse database URL: %v", err)
	}
	query := parsed.Query()
	query.Set("search_path", "schema_that_does_not_exist")
	parsed.RawQuery = query.Encode()

	_, _, err = migrations.Version(t.Context(), parsed.String())
	if err == nil || !strings.Contains(err.Error(), "current schema") {
		t.Fatalf("Version() error = %v, want missing current schema", err)
	}
}

func TestVersionSupportsQuotedSchemaNames(t *testing.T) {
	databaseURL := postgrestest.UnmigratedDatabase(t)
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatalf("parse database URL: %v", err)
	}
	originalSchema := parsed.Query().Get("search_path")
	quotedSchema := "MixedCase_" + strings.TrimPrefix(originalSchema, "t_")
	if len(quotedSchema) > 63 {
		quotedSchema = quotedSchema[:63]
	}

	connection, err := pgx.Connect(t.Context(), databaseURL)
	if err != nil {
		t.Fatalf("connect schema rename fixture: %v", err)
	}
	t.Cleanup(func() {
		if err := connection.Close(context.Background()); err != nil {
			t.Errorf("close schema rename fixture: %v", err)
		}
	})
	if _, err := connection.Exec(t.Context(), "ALTER SCHEMA "+pgx.Identifier{originalSchema}.Sanitize()+" RENAME TO "+pgx.Identifier{quotedSchema}.Sanitize()); err != nil {
		t.Fatalf("rename test schema: %v", err)
	}
	t.Cleanup(func() {
		if _, err := connection.Exec(context.Background(), "ALTER SCHEMA "+pgx.Identifier{quotedSchema}.Sanitize()+" RENAME TO "+pgx.Identifier{originalSchema}.Sanitize()); err != nil {
			t.Errorf("restore test schema name: %v", err)
		}
	})
	query := parsed.Query()
	query.Set("search_path", pgx.Identifier{quotedSchema}.Sanitize())
	parsed.RawQuery = query.Encode()

	if err := migrations.Run(t.Context(), parsed.String()); err != nil {
		t.Fatalf("Run() in quoted schema error = %v", err)
	}
	version, dirty, err := migrations.Version(t.Context(), parsed.String())
	if err != nil {
		t.Fatalf("Version() in quoted schema error = %v", err)
	}
	if version != 20260830000000 || dirty {
		t.Fatalf("Version() in quoted schema = (%d, %t), want (20260830000000, false)", version, dirty)
	}
}

func TestDownRollsBackExactlyOneMigration(t *testing.T) {
	databaseURL := postgrestest.DatabaseURL(t)

	if err := migrations.Down(t.Context(), databaseURL); err != nil {
		t.Fatalf("Down() error = %v", err)
	}
	version, dirty, err := migrations.Version(t.Context(), databaseURL)
	if err != nil {
		t.Fatalf("Version() after Down error = %v", err)
	}
	if version != 20260829000000 || dirty {
		t.Fatalf("Version() after Down = (%d, %t), want (20260829000000, false)", version, dirty)
	}

	if err := migrations.Run(t.Context(), databaseURL); err != nil {
		t.Fatalf("Run() after Down error = %v", err)
	}
	version, dirty, err = migrations.Version(t.Context(), databaseURL)
	if err != nil {
		t.Fatalf("Version() after Run error = %v", err)
	}
	if version != 20260830000000 || dirty {
		t.Fatalf("Version() after Run = (%d, %t), want (20260830000000, false)", version, dirty)
	}
}

func TestDownRefusesDisabledMigrationWithoutChangingVersion(t *testing.T) {
	databaseURL := postgrestest.DatabaseURL(t)
	for range 2 {
		if err := migrations.Down(t.Context(), databaseURL); err != nil {
			t.Fatalf("Down() to baseline error = %v", err)
		}
	}

	err := migrations.Down(t.Context(), databaseURL)
	if err == nil || !strings.Contains(err.Error(), "down migration disabled") {
		t.Fatalf("Down() baseline error = %v, want disabled migration refusal", err)
	}
	version, dirty, versionErr := migrations.Version(t.Context(), databaseURL)
	if versionErr != nil {
		t.Fatalf("Version() after refused Down error = %v", versionErr)
	}
	if version != 20260826000000 || dirty {
		t.Fatalf("Version() after refused Down = (%d, %t), want (20260826000000, false)", version, dirty)
	}
}

func TestDownRefusesMissingMigrationWithoutChangingVersion(t *testing.T) {
	databaseURL := postgrestest.DatabaseURL(t)
	connection, err := pgx.Connect(t.Context(), databaseURL)
	if err != nil {
		t.Fatalf("connect missing-down fixture: %v", err)
	}
	defer func() {
		if err := connection.Close(context.Background()); err != nil {
			t.Errorf("close missing-down fixture: %v", err)
		}
	}()
	const missingVersion = 99999999999999
	if _, err := connection.Exec(t.Context(), "UPDATE schema_migrations SET version = $1", missingVersion); err != nil {
		t.Fatalf("set missing migration version: %v", err)
	}

	err = migrations.Down(t.Context(), databaseURL)
	if err == nil || !strings.Contains(err.Error(), "missing down migration") {
		t.Fatalf("Down() error = %v, want missing down migration refusal", err)
	}
	version, dirty, versionErr := migrations.Version(t.Context(), databaseURL)
	if versionErr != nil {
		t.Fatalf("Version() after refused Down error = %v", versionErr)
	}
	if version != missingVersion || dirty {
		t.Fatalf("Version() after refused Down = (%d, %t), want (%d, false)", version, dirty, missingVersion)
	}
}

func TestRunBoundsConcurrentMigrationWait(t *testing.T) {
	databaseURL := postgrestest.DatabaseURL(t)
	connection, err := pgx.Connect(t.Context(), databaseURL)
	if err != nil {
		t.Fatalf("connect lock holder: %v", err)
	}
	t.Cleanup(func() {
		if err := connection.Close(context.Background()); err != nil {
			t.Errorf("close lock holder: %v", err)
		}
	})

	var databaseName, schemaName string
	if err := connection.QueryRow(t.Context(), "SELECT current_database(), current_schema()").Scan(&databaseName, &schemaName); err != nil {
		t.Fatalf("resolve migration lock scope: %v", err)
	}
	lockID, err := migratedatabase.GenerateAdvisoryLockId(databaseName, schemaName, "schema_migrations")
	if err != nil {
		t.Fatalf("generate migration lock id: %v", err)
	}
	if _, err := connection.Exec(t.Context(), "SELECT pg_advisory_lock($1)", lockID); err != nil {
		t.Fatalf("hold migration lock: %v", err)
	}
	locked := true
	unlock := func() {
		if !locked {
			return
		}
		locked = false
		if _, err := connection.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", lockID); err != nil {
			t.Errorf("release migration lock: %v", err)
		}
	}
	t.Cleanup(unlock)

	result := make(chan error, 1)
	go func() {
		result <- migrations.Run(context.Background(), databaseURL)
	}()

	select {
	case err := <-result:
		if err == nil {
			t.Fatal("Run() with held migration lock returned nil, want bounded lock error")
		}
		if !strings.Contains(strings.ToLower(err.Error()), "lock timeout") {
			t.Fatalf("Run() with held migration lock error = %v, want lock timeout", err)
		}
	case <-time.After(15 * time.Second):
		unlock()
		if err := <-result; err != nil {
			t.Fatalf("Run() exceeded bounded lock wait before returning %v", err)
		}
		t.Fatal("Run() waited for the held migration lock instead of failing within 15 seconds")
	}
}

func TestRunRefusesDirtyStateWithRecoveryGuidance(t *testing.T) {
	databaseURL := postgrestest.DatabaseURL(t)
	connection, err := pgx.Connect(t.Context(), databaseURL)
	if err != nil {
		t.Fatalf("connect dirty-state fixture: %v", err)
	}
	defer func() {
		if err := connection.Close(context.Background()); err != nil {
			t.Errorf("close dirty-state fixture: %v", err)
		}
	}()
	if _, err := connection.Exec(t.Context(), "UPDATE schema_migrations SET dirty = true"); err != nil {
		t.Fatalf("mark migration state dirty: %v", err)
	}

	err = migrations.Run(t.Context(), databaseURL)
	for _, want := range []string{"dirty at version 20260830000000", "repair or restore"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("Run() dirty-state error = %v, want containing %q", err, want)
		}
	}
}

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
