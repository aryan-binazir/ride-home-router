package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"ride-home-router/internal/models"
)

func TestStoreMigratesLegacyEventTablesAndPreservesVisibleHistory(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy-migration.db")
	createLegacyEventDB(t, dbPath)

	store, err := New(dbPath)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})

	assertSchemaVersion(t, store.db, 3)

	for _, tableName := range []string{
		"events_legacy",
		"event_assignments_legacy",
		"event_summaries_legacy",
		"events",
		"event_routes",
		"event_route_stops",
		"event_summaries",
	} {
		assertTableExists(t, store.db, tableName)
	}

	hasLegacyArchive, err := store.Events().HasLegacyArchive(context.Background())
	if err != nil {
		t.Fatalf("HasLegacyArchive() error = %v", err)
	}
	if !hasLegacyArchive {
		t.Fatal("expected HasLegacyArchive() to report archived legacy tables")
	}

	ctx := context.Background()
	events, total, err := store.Events().List(ctx, 20, 0)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if total != 1 {
		t.Fatalf("List() total = %d, want 1", total)
	}
	if len(events) != 1 {
		t.Fatalf("List() returned %d events, want 1", len(events))
	}
	if events[0].ID != 1 {
		t.Fatalf("List() event ID = %d, want 1", events[0].ID)
	}
	if events[0].Notes != "legacy event" {
		t.Fatalf("List() notes = %q, want %q", events[0].Notes, "legacy event")
	}

	event, routes, summary, err := store.Events().GetByID(ctx, 1)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if event == nil {
		t.Fatal("GetByID() returned nil event")
	}
	if summary == nil {
		t.Fatal("GetByID() returned nil summary")
	}
	if len(routes) != 1 {
		t.Fatalf("GetByID() returned %d routes, want 1", len(routes))
	}
	if summary.TotalDistanceMeters != 2100 {
		t.Fatalf("summary total_distance_meters = %.0f, want 2100", summary.TotalDistanceMeters)
	}
	if summary.OrgVehiclesUsed != 1 {
		t.Fatalf("summary org_vehicles_used = %d, want 1", summary.OrgVehiclesUsed)
	}

	route := routes[0]
	if route.SnapshotVersion != 1 {
		t.Fatalf("route SnapshotVersion = %d, want 1", route.SnapshotVersion)
	}
	if route.MetricsComplete {
		t.Fatal("expected migrated legacy route to mark MetricsComplete=false")
	}
	if route.TotalDistanceMeters != 2100 {
		t.Fatalf("route TotalDistanceMeters = %.0f, want 2100", route.TotalDistanceMeters)
	}
	if route.DistanceToDriverHomeMeters != 0 {
		t.Fatalf("route DistanceToDriverHomeMeters = %.0f, want 0 for legacy backfill", route.DistanceToDriverHomeMeters)
	}
	if len(route.Stops) != 2 {
		t.Fatalf("route stop count = %d, want 2", len(route.Stops))
	}
	if route.Stops[0].ParticipantName != "Legacy Rider One" || route.Stops[1].ParticipantName != "Legacy Rider Two" {
		t.Fatalf("unexpected migrated stop order: %#v", route.Stops)
	}
	if route.Stops[1].CumulativeDistanceMeters != 2100 {
		t.Fatalf("second stop cumulative distance = %.0f, want 2100", route.Stops[1].CumulativeDistanceMeters)
	}

	assertRowCount(t, store.db, "events_legacy", 1)
	assertRowCount(t, store.db, "event_assignments_legacy", 2)
	assertRowCount(t, store.db, "event_summaries_legacy", 1)
}

func TestStoreFreshSchemaCreatesV3EventTables(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "fresh-schema.db")

	store, err := New(dbPath)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})

	assertSchemaVersion(t, store.db, 3)

	for _, tableName := range []string{"events", "event_routes", "event_route_stops", "event_summaries"} {
		assertTableExists(t, store.db, tableName)
	}

	assertTableMissing(t, store.db, "events_legacy")
}

func TestEventRepositoryGetSummariesByEventIDs(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "event-list-summaries.db")

	store, err := New(dbPath)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})

	ctx := context.Background()
	createEvent := func(eventDate string, totalDistance float64) int64 {
		date, err := time.Parse("2006-01-02", eventDate)
		if err != nil {
			t.Fatalf("time.Parse() error = %v", err)
		}

		event := &models.Event{
			EventDate: date,
			Notes:     eventDate,
			Mode:      "dropoff",
		}
		routes := []models.EventRoute{
			{
				RouteOrder:                 0,
				DriverID:                   1,
				DriverName:                 "Driver",
				DriverAddress:              "1 Driver Way",
				EffectiveCapacity:          4,
				TotalDropoffDistanceMeters: totalDistance,
				TotalDistanceMeters:        totalDistance,
				Mode:                       "dropoff",
				Stops: []models.EventRouteStop{
					{
						Order:                  0,
						ParticipantID:          1,
						ParticipantName:        "Passenger",
						ParticipantAddress:     "1 Rider Road",
						DistanceFromPrevMeters: totalDistance,
					},
				},
			},
		}
		summary := &models.EventSummary{
			TotalParticipants:   1,
			TotalDrivers:        1,
			TotalDistanceMeters: totalDistance,
			Mode:                "dropoff",
		}

		created, err := store.Events().Create(ctx, event, routes, summary)
		if err != nil {
			t.Fatalf("Create() error = %v", err)
		}
		return created.ID
	}

	firstID := createEvent("2026-03-14", 1500)
	secondID := createEvent("2026-03-15", 2300)

	summaries, err := store.Events().GetSummariesByEventIDs(ctx, []int64{firstID, secondID, 999})
	if err != nil {
		t.Fatalf("GetSummariesByEventIDs() error = %v", err)
	}
	if len(summaries) != 2 {
		t.Fatalf("GetSummariesByEventIDs() returned %d summaries, want 2", len(summaries))
	}
	if summaries[firstID] == nil || summaries[firstID].TotalDistanceMeters != 1500 {
		t.Fatalf("first summary = %#v, want total distance 1500", summaries[firstID])
	}
	if summaries[secondID] == nil || summaries[secondID].TotalDistanceMeters != 2300 {
		t.Fatalf("second summary = %#v, want total distance 2300", summaries[secondID])
	}
	if summaries[999] != nil {
		t.Fatalf("unexpected summary for missing event: %#v", summaries[999])
	}
}

func TestEventRepositoryPersistsFullRouteSummaryDistance(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "event-summary.db")

	store, err := New(dbPath)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})

	ctx := context.Background()
	eventDate := time.Date(2026, time.March, 14, 0, 0, 0, 0, time.UTC)

	event := &models.Event{
		EventDate: eventDate,
		Notes:     "Persist full route totals",
		Mode:      "pickup",
	}

	fullRouteDistance := 2300.0
	dropoffDistance := 1700.0
	finalLegDistance := 600.0

	routes := []models.EventRoute{
		{
			RouteOrder:                 0,
			DriverID:                   42,
			DriverName:                 "Driver One",
			DriverAddress:              "1 Driver Way",
			EffectiveCapacity:          4,
			TotalDropoffDistanceMeters: dropoffDistance,
			DistanceToDriverHomeMeters: finalLegDistance,
			TotalDistanceMeters:        fullRouteDistance,
			BaselineDurationSecs:       900,
			RouteDurationSecs:          1200,
			DetourSecs:                 300,
			Mode:                       "pickup",
			Stops: []models.EventRouteStop{
				{
					Order:                    0,
					ParticipantID:            7,
					ParticipantName:          "Passenger One",
					ParticipantAddress:       "7 Rider Road",
					DistanceFromPrevMeters:   1000,
					CumulativeDistanceMeters: 1000,
					DurationFromPrevSecs:     600,
					CumulativeDurationSecs:   600,
				},
				{
					Order:                    1,
					ParticipantID:            8,
					ParticipantName:          "Passenger Two",
					ParticipantAddress:       "8 Rider Road",
					DistanceFromPrevMeters:   700,
					CumulativeDistanceMeters: dropoffDistance,
					DurationFromPrevSecs:     300,
					CumulativeDurationSecs:   900,
				},
			},
		},
	}

	summary := &models.EventSummary{
		TotalParticipants:   2,
		TotalDrivers:        1,
		TotalDistanceMeters: fullRouteDistance,
		OrgVehiclesUsed:     0,
		Mode:                "pickup",
	}

	created, err := store.Events().Create(ctx, event, routes, summary)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	var storedSummaryDistance float64
	if err := store.db.QueryRowContext(ctx, `
		SELECT total_distance_meters
		FROM event_summaries
		WHERE event_id = ?
	`, created.ID).Scan(&storedSummaryDistance); err != nil {
		t.Fatalf("query stored summary distance: %v", err)
	}
	if storedSummaryDistance != fullRouteDistance {
		t.Fatalf("stored summary total_distance_meters = %.0f, want %.0f", storedSummaryDistance, fullRouteDistance)
	}
	if storedSummaryDistance == dropoffDistance {
		t.Fatalf("stored summary total_distance_meters should not fall back to dropoff-only distance %.0f", dropoffDistance)
	}

	gotEvent, gotRoutes, gotSummary, err := store.Events().GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if gotEvent == nil {
		t.Fatal("GetByID() returned nil event")
	}
	if gotSummary == nil {
		t.Fatal("GetByID() returned nil summary")
	}
	if len(gotRoutes) != 1 {
		t.Fatalf("GetByID() returned %d routes, want 1", len(gotRoutes))
	}
	if gotSummary.TotalDistanceMeters != fullRouteDistance {
		t.Fatalf("GetByID() summary total distance = %.0f, want %.0f", gotSummary.TotalDistanceMeters, fullRouteDistance)
	}
	if gotRoutes[0].TotalDistanceMeters != fullRouteDistance {
		t.Fatalf("GetByID() route total distance = %.0f, want %.0f", gotRoutes[0].TotalDistanceMeters, fullRouteDistance)
	}
	if gotRoutes[0].TotalDropoffDistanceMeters != dropoffDistance {
		t.Fatalf("GetByID() route dropoff distance = %.0f, want %.0f", gotRoutes[0].TotalDropoffDistanceMeters, dropoffDistance)
	}
	if gotRoutes[0].SnapshotVersion != 2 {
		t.Fatalf("GetByID() route SnapshotVersion = %d, want 2", gotRoutes[0].SnapshotVersion)
	}
	if !gotRoutes[0].MetricsComplete {
		t.Fatal("expected new route snapshot to mark MetricsComplete=true")
	}
}

func createLegacyEventDB(t *testing.T, dbPath string) {
	t.Helper()

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer db.Close()

	legacySchema := `
		CREATE TABLE schema_version (
			version INTEGER PRIMARY KEY
		);
		INSERT INTO schema_version (version) VALUES (1);

		CREATE TABLE events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			event_date DATETIME NOT NULL,
			notes TEXT,
			mode TEXT NOT NULL DEFAULT 'dropoff',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);

		CREATE TABLE event_assignments (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			event_id INTEGER NOT NULL,
			driver_id INTEGER NOT NULL,
			driver_name TEXT NOT NULL,
			driver_address TEXT NOT NULL,
			route_order INTEGER NOT NULL,
			participant_id INTEGER NOT NULL,
			participant_name TEXT NOT NULL,
			participant_address TEXT NOT NULL,
			distance_from_prev_meters REAL NOT NULL DEFAULT 0,
			org_vehicle_id INTEGER,
			org_vehicle_name TEXT
		);

		CREATE TABLE event_summaries (
			event_id INTEGER PRIMARY KEY,
			total_participants INTEGER NOT NULL DEFAULT 0,
			total_drivers INTEGER NOT NULL DEFAULT 0,
			total_distance_meters REAL NOT NULL DEFAULT 0,
			org_vehicles_used INTEGER NOT NULL DEFAULT 0,
			mode TEXT NOT NULL DEFAULT 'dropoff'
		);

		INSERT INTO events (id, event_date, notes, mode, created_at)
		VALUES (1, '2026-03-13T00:00:00Z', 'legacy event', 'dropoff', '2026-03-13T00:00:00Z');

		INSERT INTO event_assignments (
			event_id, driver_id, driver_name, driver_address, route_order,
			participant_id, participant_name, participant_address, distance_from_prev_meters,
			org_vehicle_id, org_vehicle_name
		) VALUES
			(1, 10, 'Legacy Driver', '10 Driver Lane', 0, 11, 'Legacy Rider One', '11 Rider Lane', 1500, 5, 'Org Van'),
			(1, 10, 'Legacy Driver', '10 Driver Lane', 1, 12, 'Legacy Rider Two', '12 Rider Lane', 600, 5, 'Org Van');

		INSERT INTO event_summaries (
			event_id, total_participants, total_drivers, total_distance_meters, org_vehicles_used, mode
		) VALUES (1, 2, 1, 2100, 1, 'dropoff');
	`

	if _, err := db.Exec(legacySchema); err != nil {
		t.Fatalf("creating legacy schema: %v", err)
	}
}

func assertSchemaVersion(t *testing.T, db *sql.DB, want int) {
	t.Helper()

	var version int
	if err := db.QueryRow(`SELECT version FROM schema_version LIMIT 1`).Scan(&version); err != nil {
		t.Fatalf("query schema version: %v", err)
	}
	if version != want {
		t.Fatalf("schema version = %d, want %d", version, want)
	}
}

func assertTableExists(t *testing.T, db *sql.DB, tableName string) {
	t.Helper()

	exists, err := tableExists(db, tableName)
	if err != nil {
		t.Fatalf("tableExists(%q) error = %v", tableName, err)
	}
	if !exists {
		t.Fatalf("expected table %q to exist", tableName)
	}
}

func assertTableMissing(t *testing.T, db *sql.DB, tableName string) {
	t.Helper()

	exists, err := tableExists(db, tableName)
	if err != nil {
		t.Fatalf("tableExists(%q) error = %v", tableName, err)
	}
	if exists {
		t.Fatalf("expected table %q to be absent", tableName)
	}
}

func assertRowCount(t *testing.T, db *sql.DB, tableName string, want int) {
	t.Helper()

	var count int
	query := "SELECT COUNT(*) FROM " + tableName
	if err := db.QueryRow(query).Scan(&count); err != nil {
		t.Fatalf("count rows in %q: %v", tableName, err)
	}
	if count != want {
		t.Fatalf("row count for %q = %d, want %d", tableName, count, want)
	}
}
