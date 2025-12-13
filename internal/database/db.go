package database

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaSQL string

// DataStore is the interface for data persistence
type DataStore interface {
	Close() error
	HealthCheck(ctx context.Context) error
	Participants() ParticipantRepository
	Drivers() DriverRepository
	Settings() SettingsRepository
	ActivityLocations() ActivityLocationRepository
	Events() EventRepository
	DistanceCache() DistanceCacheRepository
}

// DB wraps the database connection and provides access to repositories
type DB struct {
	conn                       *sql.DB
	participantRepository      ParticipantRepository
	driverRepository           DriverRepository
	settingsRepository         SettingsRepository
	activityLocationRepository ActivityLocationRepository
	eventRepository            EventRepository
	distanceCacheRepository    DistanceCacheRepository
}

func (db *DB) Participants() ParticipantRepository         { return db.participantRepository }
func (db *DB) Drivers() DriverRepository                   { return db.driverRepository }
func (db *DB) Settings() SettingsRepository                { return db.settingsRepository }
func (db *DB) ActivityLocations() ActivityLocationRepository { return db.activityLocationRepository }
func (db *DB) Events() EventRepository                     { return db.eventRepository }
func (db *DB) DistanceCache() DistanceCacheRepository       { return db.distanceCacheRepository }

// New creates a new database connection and runs migrations
func New(dbPath string) (*DB, error) {
	conn, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	if err := conn.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	if err := runMigrations(conn); err != nil {
		return nil, fmt.Errorf("failed to run migrations: %w", err)
	}

	db := &DB{
		conn:                       conn,
		participantRepository:      &participantRepository{db: conn},
		driverRepository:           &driverRepository{db: conn},
		settingsRepository:         &settingsRepository{db: conn},
		activityLocationRepository: &activityLocationRepository{db: conn},
		eventRepository:            &eventRepository{db: conn},
		distanceCacheRepository:    &distanceCacheRepository{db: conn},
	}

	return db, nil
}

// Close closes the database connection
func (db *DB) Close() error {
	return db.conn.Close()
}

// HealthCheck verifies the database connection is alive
func (db *DB) HealthCheck(ctx context.Context) error {
	return db.conn.PingContext(ctx)
}

// runMigrations executes the schema SQL
func runMigrations(db *sql.DB) error {
	_, err := db.Exec(schemaSQL)
	if err != nil {
		return fmt.Errorf("failed to execute schema: %w", err)
	}
	return nil
}
