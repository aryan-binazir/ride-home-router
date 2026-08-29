// Package postgres implements database.DataStore on PostgreSQL.
package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"ride-home-router/internal/database"
	"time"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"
)

const (
	connectTimeout  = 10 * time.Second
	maxOpenConns    = 10
	connMaxLifetime = 30 * time.Minute
	connMaxIdleTime = 5 * time.Minute
)

// Store is a PostgreSQL-backed data store for a migrated schema.
type Store struct {
	db *sql.DB
}

// New opens a connection pool to databaseURL and verifies it is reachable.
func New(ctx context.Context, databaseURL string) (*Store, error) {
	config, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database URL: %w", err)
	}
	if config.ConnectTimeout == 0 || config.ConnectTimeout > connectTimeout {
		config.ConnectTimeout = connectTimeout
	}
	db := stdlib.OpenDB(*config)
	// Bound the pool for managed Postgres connection limits.
	db.SetMaxOpenConns(maxOpenConns)
	db.SetMaxIdleConns(maxOpenConns)
	db.SetConnMaxLifetime(connMaxLifetime)
	db.SetConnMaxIdleTime(connMaxIdleTime)

	pingCtx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("connect to Postgres: %w", err)
	}
	return &Store{db: db}, nil
}

// Close releases the connection pool.
func (s *Store) Close() error { return s.db.Close() }

// HealthCheck verifies the database connection.
func (s *Store) HealthCheck(ctx context.Context) error { return s.db.PingContext(ctx) }

func (s *Store) Participants() database.ParticipantRepository {
	return &participantRepository{db: s.db}
}

func (s *Store) Drivers() database.DriverRepository { return &driverRepository{db: s.db} }

func (s *Store) Settings() database.SettingsRepository { return &settingsRepository{db: s.db} }

func (s *Store) ActivityLocations() database.ActivityLocationRepository {
	return &activityLocationRepository{db: s.db}
}

func (s *Store) OrganizationVehicles() database.OrganizationVehicleRepository {
	return &organizationVehicleRepository{db: s.db}
}

func (s *Store) Events() database.EventRepository { return &eventRepository{db: s.db} }

func (s *Store) DistanceCache() database.DistanceCacheRepository {
	return &distanceCacheRepository{db: s.db}
}

func (s *Store) Labels() database.LabelRepository { return &labelRepository{db: s.db} }

// rowsAffectedOrNotFound maps a zero-row write to database.ErrNotFound.
func rowsAffectedOrNotFound(result sql.Result) error {
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rows == 0 {
		return database.ErrNotFound
	}
	return nil
}

// mapUniqueViolation turns a Postgres unique violation into database.ErrDuplicate.
func mapUniqueViolation(err error) error {
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == pgerrcode.UniqueViolation {
		return database.ErrDuplicate
	}
	return err
}
