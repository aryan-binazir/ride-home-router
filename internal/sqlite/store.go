package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"

	"ride-home-router/internal/database"

	_ "modernc.org/sqlite"
)

const (
	DefaultDBFileName = "data.db"
	schemaVersion     = 4
)

// Store is a SQLite-based data store implementing database.DataStore
type Store struct {
	db     *sql.DB
	dbPath string
	mu     sync.RWMutex

	participantRepo         database.ParticipantRepository
	driverRepo              database.DriverRepository
	settingsRepo            database.SettingsRepository
	activityLocationRepo    database.ActivityLocationRepository
	organizationVehicleRepo database.OrganizationVehicleRepository
	eventRepo               database.EventRepository
	distanceCacheRepo       database.DistanceCacheRepository
	groupRepo               database.GroupRepository
}

// New creates a new SQLite store at the specified path
func New(dbPath string) (*Store, error) {
	// Ensure directory exists
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create database directory: %w", err)
	}

	log.Printf("Opening SQLite database at: %s", dbPath)

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Enable foreign keys and WAL mode for better performance
	pragmas := []string{
		"PRAGMA foreign_keys = ON",
		"PRAGMA journal_mode = WAL",
		"PRAGMA synchronous = NORMAL",
		"PRAGMA cache_size = -64000", // 64MB cache
		"PRAGMA busy_timeout = 5000",
	}

	for _, pragma := range pragmas {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("failed to set pragma %s: %w", pragma, err)
		}
	}

	store := &Store{
		db:     db,
		dbPath: dbPath,
	}

	// Initialize schema
	if err := store.initSchema(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to initialize schema: %w", err)
	}

	// Initialize repositories
	store.participantRepo = &participantRepository{store: store}
	store.driverRepo = &driverRepository{store: store}
	store.settingsRepo = &settingsRepository{store: store}
	store.activityLocationRepo = &activityLocationRepository{store: store}
	store.organizationVehicleRepo = &organizationVehicleRepository{store: store}
	store.eventRepo = &eventRepository{store: store}
	store.distanceCacheRepo = &distanceCacheRepository{store: store}
	store.groupRepo = &groupRepository{store: store}

	return store, nil
}

// GetDBPath returns the current database file path
func (s *Store) GetDBPath() string {
	return s.dbPath
}

func (s *Store) initSchema() error {
	var version int
	err := s.db.QueryRow("SELECT version FROM schema_version LIMIT 1").Scan(&version)
	if err != nil {
		return s.createSchema()
	}

	// Run migrations if needed
	if version < schemaVersion {
		if err := s.runMigrations(version); err != nil {
			return err
		}
	}

	return nil
}

func (s *Store) createSchema() error {
	schema := `
	-- Schema version tracking
	CREATE TABLE IF NOT EXISTS schema_version (
		version INTEGER PRIMARY KEY
	);
	INSERT INTO schema_version (version) VALUES (4);

	-- Activity locations
	CREATE TABLE IF NOT EXISTS activity_locations (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		address TEXT NOT NULL,
		lat REAL NOT NULL,
		lng REAL NOT NULL
	);

	-- Participants
	CREATE TABLE IF NOT EXISTS participants (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		address TEXT NOT NULL,
		lat REAL NOT NULL,
		lng REAL NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	-- Drivers
	CREATE TABLE IF NOT EXISTS drivers (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		address TEXT NOT NULL,
		lat REAL NOT NULL,
		lng REAL NOT NULL,
		vehicle_capacity INTEGER NOT NULL DEFAULT 4,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	-- Organization vehicles
	CREATE TABLE IF NOT EXISTS organization_vehicles (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		capacity INTEGER NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	-- Groups
	CREATE TABLE IF NOT EXISTS groups (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL UNIQUE,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS participant_groups (
		group_id INTEGER NOT NULL,
		participant_id INTEGER NOT NULL,
		PRIMARY KEY (group_id, participant_id),
		FOREIGN KEY (group_id) REFERENCES groups(id) ON DELETE CASCADE,
		FOREIGN KEY (participant_id) REFERENCES participants(id) ON DELETE CASCADE
	);

	CREATE TABLE IF NOT EXISTS driver_groups (
		group_id INTEGER NOT NULL,
		driver_id INTEGER NOT NULL,
		PRIMARY KEY (group_id, driver_id),
		FOREIGN KEY (group_id) REFERENCES groups(id) ON DELETE CASCADE,
		FOREIGN KEY (driver_id) REFERENCES drivers(id) ON DELETE CASCADE
	);

	-- Settings (single row table)
	CREATE TABLE IF NOT EXISTS settings (
		id INTEGER PRIMARY KEY CHECK (id = 1),
		selected_activity_location_id INTEGER,
		use_miles INTEGER NOT NULL DEFAULT 1,
		FOREIGN KEY (selected_activity_location_id) REFERENCES activity_locations(id) ON DELETE SET NULL
	);
	INSERT OR IGNORE INTO settings (id, use_miles) VALUES (1, 1);

	-- Events
	CREATE TABLE IF NOT EXISTS events (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		event_date DATETIME NOT NULL,
		notes TEXT,
		mode TEXT NOT NULL DEFAULT 'dropoff',
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	-- Event route snapshots
	CREATE TABLE IF NOT EXISTS event_routes (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		event_id INTEGER NOT NULL,
		route_order INTEGER NOT NULL,
		driver_id INTEGER NOT NULL,
		driver_name TEXT NOT NULL,
		driver_address TEXT NOT NULL,
		effective_capacity INTEGER NOT NULL DEFAULT 0,
		org_vehicle_id INTEGER,
		org_vehicle_name TEXT,
		total_dropoff_distance_meters REAL NOT NULL DEFAULT 0,
		distance_to_driver_home_meters REAL NOT NULL DEFAULT 0,
		total_distance_meters REAL NOT NULL DEFAULT 0,
		baseline_duration_secs REAL NOT NULL DEFAULT 0,
		route_duration_secs REAL NOT NULL DEFAULT 0,
		detour_secs REAL NOT NULL DEFAULT 0,
		mode TEXT NOT NULL DEFAULT 'dropoff',
		snapshot_version INTEGER NOT NULL DEFAULT 2,
		metrics_complete INTEGER NOT NULL DEFAULT 1,
		FOREIGN KEY (event_id) REFERENCES events(id) ON DELETE CASCADE
	);

	-- Event route stop snapshots
	CREATE TABLE IF NOT EXISTS event_route_stops (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		event_route_id INTEGER NOT NULL,
		route_order INTEGER NOT NULL,
		participant_id INTEGER NOT NULL,
		participant_name TEXT NOT NULL,
		participant_address TEXT NOT NULL,
		distance_from_prev_meters REAL NOT NULL DEFAULT 0,
		cumulative_distance_meters REAL NOT NULL DEFAULT 0,
		duration_from_prev_secs REAL NOT NULL DEFAULT 0,
		cumulative_duration_secs REAL NOT NULL DEFAULT 0,
		FOREIGN KEY (event_route_id) REFERENCES event_routes(id) ON DELETE CASCADE
	);

	-- Event summaries
	CREATE TABLE IF NOT EXISTS event_summaries (
		event_id INTEGER PRIMARY KEY,
		total_participants INTEGER NOT NULL DEFAULT 0,
		total_drivers INTEGER NOT NULL DEFAULT 0,
		total_distance_meters REAL NOT NULL DEFAULT 0,
		org_vehicles_used INTEGER NOT NULL DEFAULT 0,
		mode TEXT NOT NULL DEFAULT 'dropoff',
		FOREIGN KEY (event_id) REFERENCES events(id) ON DELETE CASCADE
	);

	-- Distance cache
	CREATE TABLE IF NOT EXISTS distance_cache (
		origin_lat REAL NOT NULL,
		origin_lng REAL NOT NULL,
		dest_lat REAL NOT NULL,
		dest_lng REAL NOT NULL,
		distance_meters REAL NOT NULL,
		duration_secs REAL NOT NULL,
		PRIMARY KEY (origin_lat, origin_lng, dest_lat, dest_lng)
	);

	-- Indexes for common queries
	CREATE INDEX IF NOT EXISTS idx_participants_name ON participants(name);
	CREATE INDEX IF NOT EXISTS idx_drivers_name ON drivers(name);
	CREATE INDEX IF NOT EXISTS idx_groups_name ON groups(name);
	CREATE INDEX IF NOT EXISTS idx_participant_groups_participant ON participant_groups(participant_id);
	CREATE INDEX IF NOT EXISTS idx_driver_groups_driver ON driver_groups(driver_id);
	CREATE INDEX IF NOT EXISTS idx_events_date ON events(event_date DESC);
	CREATE INDEX IF NOT EXISTS idx_event_routes_event ON event_routes(event_id);
	CREATE INDEX IF NOT EXISTS idx_event_route_stops_route ON event_route_stops(event_route_id);
	`

	_, err := s.db.Exec(schema)
	if err != nil {
		return fmt.Errorf("failed to create schema: %w", err)
	}

	log.Printf("SQLite schema initialized (version %d)", schemaVersion)
	return nil
}

func (s *Store) runMigrations(fromVersion int) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin migration transaction: %w", err)
	}
	defer tx.Rollback()

	if fromVersion < 2 {
		legacyTables := []string{"event_assignments", "event_summaries", "events"}
		for _, table := range legacyTables {
			exists, err := tableExists(tx, table)
			if err != nil {
				return err
			}
			if !exists {
				continue
			}

			legacyName := table + "_legacy"
			legacyExists, err := tableExists(tx, legacyName)
			if err != nil {
				return err
			}
			if legacyExists {
				continue
			}

			if _, err := tx.Exec(fmt.Sprintf("ALTER TABLE %s RENAME TO %s", table, legacyName)); err != nil {
				return fmt.Errorf("failed to archive %s: %w", table, err)
			}
		}

		if _, err := tx.Exec(`
			CREATE TABLE IF NOT EXISTS events (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				event_date DATETIME NOT NULL,
				notes TEXT,
				mode TEXT NOT NULL DEFAULT 'dropoff',
				created_at DATETIME DEFAULT CURRENT_TIMESTAMP
			);

			CREATE TABLE IF NOT EXISTS event_routes (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				event_id INTEGER NOT NULL,
				route_order INTEGER NOT NULL,
				driver_id INTEGER NOT NULL,
				driver_name TEXT NOT NULL,
				driver_address TEXT NOT NULL,
				effective_capacity INTEGER NOT NULL DEFAULT 0,
				org_vehicle_id INTEGER,
				org_vehicle_name TEXT,
				total_dropoff_distance_meters REAL NOT NULL DEFAULT 0,
				distance_to_driver_home_meters REAL NOT NULL DEFAULT 0,
				total_distance_meters REAL NOT NULL DEFAULT 0,
				baseline_duration_secs REAL NOT NULL DEFAULT 0,
				route_duration_secs REAL NOT NULL DEFAULT 0,
				detour_secs REAL NOT NULL DEFAULT 0,
				mode TEXT NOT NULL DEFAULT 'dropoff',
				snapshot_version INTEGER NOT NULL DEFAULT 2,
				metrics_complete INTEGER NOT NULL DEFAULT 1,
				FOREIGN KEY (event_id) REFERENCES events(id) ON DELETE CASCADE
			);

			CREATE TABLE IF NOT EXISTS event_route_stops (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				event_route_id INTEGER NOT NULL,
				route_order INTEGER NOT NULL,
				participant_id INTEGER NOT NULL,
				participant_name TEXT NOT NULL,
				participant_address TEXT NOT NULL,
				distance_from_prev_meters REAL NOT NULL DEFAULT 0,
				cumulative_distance_meters REAL NOT NULL DEFAULT 0,
				duration_from_prev_secs REAL NOT NULL DEFAULT 0,
				cumulative_duration_secs REAL NOT NULL DEFAULT 0,
				FOREIGN KEY (event_route_id) REFERENCES event_routes(id) ON DELETE CASCADE
			);

			CREATE TABLE IF NOT EXISTS event_summaries (
				event_id INTEGER PRIMARY KEY,
				total_participants INTEGER NOT NULL DEFAULT 0,
				total_drivers INTEGER NOT NULL DEFAULT 0,
				total_distance_meters REAL NOT NULL DEFAULT 0,
				org_vehicles_used INTEGER NOT NULL DEFAULT 0,
				mode TEXT NOT NULL DEFAULT 'dropoff',
				FOREIGN KEY (event_id) REFERENCES events(id) ON DELETE CASCADE
			);

			CREATE INDEX IF NOT EXISTS idx_events_date ON events(event_date DESC);
			CREATE INDEX IF NOT EXISTS idx_event_routes_event ON event_routes(event_id);
			CREATE INDEX IF NOT EXISTS idx_event_route_stops_route ON event_route_stops(event_route_id);
		`); err != nil {
			return fmt.Errorf("failed to create v2 event tables: %w", err)
		}
	}

	if fromVersion < 3 {
		if err := ensureEventRouteColumn(tx, "snapshot_version", "INTEGER NOT NULL DEFAULT 2"); err != nil {
			return err
		}
		if err := ensureEventRouteColumn(tx, "metrics_complete", "INTEGER NOT NULL DEFAULT 1"); err != nil {
			return err
		}
		if err := backfillLegacyEventHistory(tx); err != nil {
			return err
		}
	}

	if fromVersion < 4 {
		if _, err := tx.Exec(`
			CREATE TABLE IF NOT EXISTS groups (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				name TEXT NOT NULL UNIQUE,
				created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
				updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
			);

			CREATE TABLE IF NOT EXISTS participant_groups (
				group_id INTEGER NOT NULL,
				participant_id INTEGER NOT NULL,
				PRIMARY KEY (group_id, participant_id),
				FOREIGN KEY (group_id) REFERENCES groups(id) ON DELETE CASCADE,
				FOREIGN KEY (participant_id) REFERENCES participants(id) ON DELETE CASCADE
			);

			CREATE TABLE IF NOT EXISTS driver_groups (
				group_id INTEGER NOT NULL,
				driver_id INTEGER NOT NULL,
				PRIMARY KEY (group_id, driver_id),
				FOREIGN KEY (group_id) REFERENCES groups(id) ON DELETE CASCADE,
				FOREIGN KEY (driver_id) REFERENCES drivers(id) ON DELETE CASCADE
			);

			CREATE INDEX IF NOT EXISTS idx_groups_name ON groups(name);
			CREATE INDEX IF NOT EXISTS idx_participant_groups_participant ON participant_groups(participant_id);
			CREATE INDEX IF NOT EXISTS idx_driver_groups_driver ON driver_groups(driver_id);
		`); err != nil {
			return fmt.Errorf("failed to create v4 group tables: %w", err)
		}
	}

	if _, err := tx.Exec("UPDATE schema_version SET version = ?", schemaVersion); err != nil {
		return fmt.Errorf("failed to update schema version: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit migrations: %w", err)
	}

	return nil
}

func ensureEventRouteColumn(tx *sql.Tx, name, definition string) error {
	exists, err := columnExists(tx, "event_routes", name)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	if _, err := tx.Exec(fmt.Sprintf("ALTER TABLE event_routes ADD COLUMN %s %s", name, definition)); err != nil {
		return fmt.Errorf("failed to add event_routes.%s: %w", name, err)
	}
	return nil
}

func tableExists(queryer interface {
	QueryRow(query string, args ...interface{}) *sql.Row
}, tableName string) (bool, error) {
	var exists int
	err := queryer.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = ?)`, tableName).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("failed checking table %s: %w", tableName, err)
	}
	return exists == 1, nil
}

func columnExists(queryer interface {
	Query(query string, args ...interface{}) (*sql.Rows, error)
}, tableName, columnName string) (bool, error) {
	rows, err := queryer.Query(fmt.Sprintf("PRAGMA table_info(%s)", tableName))
	if err != nil {
		return false, fmt.Errorf("failed checking columns for %s: %w", tableName, err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			cid        int
			name       string
			columnType string
			notNull    int
			defaultVal sql.NullString
			pk         int
		)
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultVal, &pk); err != nil {
			return false, fmt.Errorf("failed scanning column info for %s: %w", tableName, err)
		}
		if name == columnName {
			return true, nil
		}
	}

	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("failed iterating columns for %s: %w", tableName, err)
	}

	return false, nil
}

func backfillLegacyEventHistory(tx *sql.Tx) error {
	hasLegacyEvents, err := tableExists(tx, "events_legacy")
	if err != nil {
		return err
	}
	hasLegacyAssignments, err := tableExists(tx, "event_assignments_legacy")
	if err != nil {
		return err
	}
	if !hasLegacyEvents || !hasLegacyAssignments {
		return nil
	}

	nextEventID, err := nextAvailableEventID(tx)
	if err != nil {
		return err
	}

	rows, err := tx.Query(`
		SELECT id, event_date, notes, mode, created_at
		FROM events_legacy
		ORDER BY id
	`)
	if err != nil {
		return fmt.Errorf("failed to query legacy events: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			eventID   int64
			eventDate string
			notes     sql.NullString
			mode      string
			createdAt string
		)
		if err := rows.Scan(&eventID, &eventDate, &notes, &mode, &createdAt); err != nil {
			return fmt.Errorf("failed to scan legacy event: %w", err)
		}

		backfilled, err := eventHasLegacyBackfill(tx, eventID)
		if err != nil {
			return err
		}
		if backfilled {
			continue
		}

		exists, err := eventExists(tx, eventID)
		if err != nil {
			return err
		}
		if exists {
			if err := moveCurrentEvent(tx, eventID, nextEventID); err != nil {
				return err
			}
			nextEventID++
		}

		if _, err := tx.Exec(`
			INSERT INTO events (id, event_date, notes, mode, created_at)
			VALUES (?, ?, ?, ?, ?)
		`, eventID, eventDate, notes, mode, createdAt); err != nil {
			return fmt.Errorf("failed to insert migrated event %d: %w", eventID, err)
		}

		if err := backfillLegacyRoutes(tx, eventID, mode); err != nil {
			return err
		}
		if err := backfillLegacySummary(tx, eventID, mode); err != nil {
			return err
		}
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("failed iterating legacy events: %w", err)
	}

	return nil
}

func nextAvailableEventID(tx *sql.Tx) (int64, error) {
	var maxID sql.NullInt64
	if err := tx.QueryRow(`SELECT MAX(id) FROM events`).Scan(&maxID); err != nil {
		return 0, fmt.Errorf("failed to query next event id: %w", err)
	}
	if !maxID.Valid {
		return 1, nil
	}
	return maxID.Int64 + 1, nil
}

func eventExists(tx *sql.Tx, eventID int64) (bool, error) {
	var exists int
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM events WHERE id = ?)`, eventID).Scan(&exists); err != nil {
		return false, fmt.Errorf("failed to check event existence for %d: %w", eventID, err)
	}
	return exists == 1, nil
}

func eventHasLegacyBackfill(tx *sql.Tx, eventID int64) (bool, error) {
	var count int
	if err := tx.QueryRow(`
		SELECT COUNT(*)
		FROM event_routes
		WHERE event_id = ? AND snapshot_version = 1
	`, eventID).Scan(&count); err != nil {
		return false, fmt.Errorf("failed to check migrated legacy routes for %d: %w", eventID, err)
	}
	return count > 0, nil
}

func moveCurrentEvent(tx *sql.Tx, oldID, newID int64) error {
	if _, err := tx.Exec(`
		INSERT INTO events (id, event_date, notes, mode, created_at)
		SELECT ?, event_date, notes, mode, created_at
		FROM events
		WHERE id = ?
	`, newID, oldID); err != nil {
		return fmt.Errorf("failed to duplicate event %d to %d: %w", oldID, newID, err)
	}

	if _, err := tx.Exec(`UPDATE event_routes SET event_id = ? WHERE event_id = ?`, newID, oldID); err != nil {
		return fmt.Errorf("failed to move event routes from %d to %d: %w", oldID, newID, err)
	}
	if _, err := tx.Exec(`UPDATE event_summaries SET event_id = ? WHERE event_id = ?`, newID, oldID); err != nil {
		return fmt.Errorf("failed to move event summary from %d to %d: %w", oldID, newID, err)
	}
	if _, err := tx.Exec(`DELETE FROM events WHERE id = ?`, oldID); err != nil {
		return fmt.Errorf("failed to delete remapped event %d: %w", oldID, err)
	}

	return nil
}

func backfillLegacyRoutes(tx *sql.Tx, eventID int64, mode string) error {
	rows, err := tx.Query(`
		SELECT id, driver_id, driver_name, driver_address, route_order,
		       participant_id, participant_name, participant_address,
		       distance_from_prev_meters, org_vehicle_id, org_vehicle_name
		FROM event_assignments_legacy
		WHERE event_id = ?
		ORDER BY driver_id, route_order, id
	`, eventID)
	if err != nil {
		return fmt.Errorf("failed to query legacy assignments for event %d: %w", eventID, err)
	}
	defer rows.Close()

	type legacyAssignment struct {
		driverID           int64
		driverName         string
		driverAddress      string
		routeOrder         int
		participantID      int64
		participantName    string
		participantAddress string
		distanceFromPrev   float64
		orgVehicleID       sql.NullInt64
		orgVehicleName     sql.NullString
		cumulativeDistance float64
	}
	type legacyRoute struct {
		driverID       int64
		driverName     string
		driverAddress  string
		orgVehicleID   sql.NullInt64
		orgVehicleName sql.NullString
		assignments    []legacyAssignment
		totalDistance  float64
	}

	var (
		routes  []legacyRoute
		current *legacyRoute
	)

	for rows.Next() {
		var assignment legacyAssignment
		if err := rows.Scan(
			new(int64), &assignment.driverID, &assignment.driverName, &assignment.driverAddress, &assignment.routeOrder,
			&assignment.participantID, &assignment.participantName, &assignment.participantAddress,
			&assignment.distanceFromPrev, &assignment.orgVehicleID, &assignment.orgVehicleName,
		); err != nil {
			return fmt.Errorf("failed to scan legacy assignment for event %d: %w", eventID, err)
		}

		if current == nil ||
			current.driverID != assignment.driverID ||
			current.orgVehicleID.Valid != assignment.orgVehicleID.Valid ||
			current.orgVehicleID.Int64 != assignment.orgVehicleID.Int64 ||
			current.orgVehicleName.Valid != assignment.orgVehicleName.Valid ||
			current.orgVehicleName.String != assignment.orgVehicleName.String {
			routes = append(routes, legacyRoute{
				driverID:       assignment.driverID,
				driverName:     assignment.driverName,
				driverAddress:  assignment.driverAddress,
				orgVehicleID:   assignment.orgVehicleID,
				orgVehicleName: assignment.orgVehicleName,
			})
			current = &routes[len(routes)-1]
		}

		current.totalDistance += assignment.distanceFromPrev
		assignment.cumulativeDistance = current.totalDistance
		current.assignments = append(current.assignments, assignment)
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("failed iterating legacy assignments for event %d: %w", eventID, err)
	}

	for routeIndex, route := range routes {
		var orgVehicleID *int64
		var orgVehicleName *string
		if route.orgVehicleID.Valid {
			orgVehicleID = &route.orgVehicleID.Int64
		}
		if route.orgVehicleName.Valid {
			orgVehicleName = &route.orgVehicleName.String
		}

		result, err := tx.Exec(`
			INSERT INTO event_routes (
				event_id, route_order, driver_id, driver_name, driver_address,
				effective_capacity, org_vehicle_id, org_vehicle_name,
				total_dropoff_distance_meters, distance_to_driver_home_meters,
				total_distance_meters, baseline_duration_secs, route_duration_secs,
				detour_secs, mode, snapshot_version, metrics_complete
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, eventID, routeIndex, route.driverID, route.driverName, route.driverAddress,
			0, orgVehicleID, orgVehicleName,
			route.totalDistance, 0, route.totalDistance, 0, 0, 0, mode, 1, 0,
		)
		if err != nil {
			return fmt.Errorf("failed to insert migrated route for event %d: %w", eventID, err)
		}

		eventRouteID, err := result.LastInsertId()
		if err != nil {
			return fmt.Errorf("failed to get migrated route id for event %d: %w", eventID, err)
		}

		for stopIndex, assignment := range route.assignments {
			if _, err := tx.Exec(`
				INSERT INTO event_route_stops (
					event_route_id, route_order, participant_id, participant_name,
					participant_address, distance_from_prev_meters, cumulative_distance_meters,
					duration_from_prev_secs, cumulative_duration_secs
				) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
			`, eventRouteID, stopIndex, assignment.participantID, assignment.participantName,
				assignment.participantAddress, assignment.distanceFromPrev, assignment.cumulativeDistance, 0, 0,
			); err != nil {
				return fmt.Errorf("failed to insert migrated stop for event %d: %w", eventID, err)
			}
		}
	}

	return nil
}

func backfillLegacySummary(tx *sql.Tx, eventID int64, mode string) error {
	var (
		totalParticipants int
		totalDrivers      int
		totalDistance     float64
		orgVehiclesUsed   int
		summaryMode       string
	)

	hasLegacySummaries, err := tableExists(tx, "event_summaries_legacy")
	if err != nil {
		return err
	}

	if hasLegacySummaries {
		err = tx.QueryRow(`
			SELECT total_participants, total_drivers, total_distance_meters, org_vehicles_used, mode
			FROM event_summaries_legacy
			WHERE event_id = ?
		`, eventID).Scan(&totalParticipants, &totalDrivers, &totalDistance, &orgVehiclesUsed, &summaryMode)
		if err != nil && err != sql.ErrNoRows {
			return fmt.Errorf("failed to query legacy summary for event %d: %w", eventID, err)
		}
	} else {
		err = sql.ErrNoRows
	}

	if err == sql.ErrNoRows {
		if err := tx.QueryRow(`
			SELECT COUNT(*), COUNT(DISTINCT driver_id), COALESCE(SUM(distance_from_prev_meters), 0),
			       COUNT(DISTINCT CASE WHEN org_vehicle_id IS NOT NULL AND org_vehicle_id != 0 THEN org_vehicle_id END)
			FROM event_assignments_legacy
			WHERE event_id = ?
		`, eventID).Scan(&totalParticipants, &totalDrivers, &totalDistance, &orgVehiclesUsed); err != nil {
			return fmt.Errorf("failed to synthesize legacy summary for event %d: %w", eventID, err)
		}
		summaryMode = mode
	}

	if _, err := tx.Exec(`
		INSERT INTO event_summaries (
			event_id, total_participants, total_drivers, total_distance_meters,
			org_vehicles_used, mode
		) VALUES (?, ?, ?, ?, ?, ?)
	`, eventID, totalParticipants, totalDrivers, totalDistance, orgVehiclesUsed, summaryMode); err != nil {
		return fmt.Errorf("failed to insert migrated summary for event %d: %w", eventID, err)
	}

	return nil
}

// Close closes the database connection
func (s *Store) Close() error {
	if s.db != nil {
		// Checkpoint WAL before closing
		s.db.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
		return s.db.Close()
	}
	return nil
}

// HealthCheck verifies the database connection
func (s *Store) HealthCheck(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

// Repository accessors
func (s *Store) Participants() database.ParticipantRepository { return s.participantRepo }
func (s *Store) Drivers() database.DriverRepository           { return s.driverRepo }
func (s *Store) Settings() database.SettingsRepository        { return s.settingsRepo }
func (s *Store) ActivityLocations() database.ActivityLocationRepository {
	return s.activityLocationRepo
}
func (s *Store) OrganizationVehicles() database.OrganizationVehicleRepository {
	return s.organizationVehicleRepo
}
func (s *Store) Events() database.EventRepository                { return s.eventRepo }
func (s *Store) DistanceCache() database.DistanceCacheRepository { return s.distanceCacheRepo }
func (s *Store) Groups() database.GroupRepository                { return s.groupRepo }
