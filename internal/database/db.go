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

// DB wraps the database connection and provides access to repositories
type DB struct {
	conn                  *sql.DB
	ParticipantRepository ParticipantRepository
	DriverRepository      DriverRepository
	SettingsRepository    SettingsRepository
	EventRepository       EventRepository
	DistanceCacheRepository DistanceCacheRepository
}

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
		conn:                    conn,
		ParticipantRepository:   &participantRepository{db: conn},
		DriverRepository:        &driverRepository{db: conn},
		SettingsRepository:      &settingsRepository{db: conn},
		EventRepository:         &eventRepository{db: conn},
		DistanceCacheRepository: &distanceCacheRepository{db: conn},
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
