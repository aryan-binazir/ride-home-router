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
	schemaVersion     = 1
)

// Store is a SQLite-based data store implementing database.DataStore
type Store struct {
	db       *sql.DB
	dbPath   string
	mu       sync.RWMutex

	participantRepo         database.ParticipantRepository
	driverRepo              database.DriverRepository
	settingsRepo            database.SettingsRepository
	activityLocationRepo    database.ActivityLocationRepository
	organizationVehicleRepo database.OrganizationVehicleRepository
	eventRepo               database.EventRepository
	distanceCacheRepo       database.DistanceCacheRepository
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

	return store, nil
}

// GetDBPath returns the current database file path
func (s *Store) GetDBPath() string {
	return s.dbPath
}

func (s *Store) initSchema() error {
	// Check current schema version
	var version int
	err := s.db.QueryRow("SELECT version FROM schema_version LIMIT 1").Scan(&version)
	if err != nil {
		// Table doesn't exist, create everything
		if err := s.createSchema(); err != nil {
			return err
		}
		return nil
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
	INSERT INTO schema_version (version) VALUES (1);

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

	-- Settings (single row table)
	CREATE TABLE IF NOT EXISTS settings (
		id INTEGER PRIMARY KEY CHECK (id = 1),
		selected_activity_location_id INTEGER,
		use_miles INTEGER NOT NULL DEFAULT 0,
		FOREIGN KEY (selected_activity_location_id) REFERENCES activity_locations(id) ON DELETE SET NULL
	);
	INSERT OR IGNORE INTO settings (id, use_miles) VALUES (1, 0);

	-- Events
	CREATE TABLE IF NOT EXISTS events (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		event_date DATETIME NOT NULL,
		notes TEXT,
		mode TEXT NOT NULL DEFAULT 'dropoff',
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	-- Event assignments
	CREATE TABLE IF NOT EXISTS event_assignments (
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
		org_vehicle_name TEXT,
		FOREIGN KEY (event_id) REFERENCES events(id) ON DELETE CASCADE
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
	CREATE INDEX IF NOT EXISTS idx_events_date ON events(event_date DESC);
	CREATE INDEX IF NOT EXISTS idx_event_assignments_event ON event_assignments(event_id);
	`

	_, err := s.db.Exec(schema)
	if err != nil {
		return fmt.Errorf("failed to create schema: %w", err)
	}

	log.Printf("SQLite schema initialized (version %d)", schemaVersion)
	return nil
}

func (s *Store) runMigrations(fromVersion int) error {
	// Add migrations here as schema evolves
	// Example:
	// if fromVersion < 2 {
	//     _, err := s.db.Exec("ALTER TABLE participants ADD COLUMN phone TEXT")
	//     if err != nil { return err }
	// }

	// Update version
	_, err := s.db.Exec("UPDATE schema_version SET version = ?", schemaVersion)
	return err
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
func (s *Store) Participants() database.ParticipantRepository              { return s.participantRepo }
func (s *Store) Drivers() database.DriverRepository                        { return s.driverRepo }
func (s *Store) Settings() database.SettingsRepository                     { return s.settingsRepo }
func (s *Store) ActivityLocations() database.ActivityLocationRepository    { return s.activityLocationRepo }
func (s *Store) OrganizationVehicles() database.OrganizationVehicleRepository { return s.organizationVehicleRepo }
func (s *Store) Events() database.EventRepository                          { return s.eventRepo }
func (s *Store) DistanceCache() database.DistanceCacheRepository           { return s.distanceCacheRepo }
