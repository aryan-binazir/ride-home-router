package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"ride-home-router/internal/credentials"
	"ride-home-router/internal/database"
	"ride-home-router/migrations"
	"strings"
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

type Store struct {
	db               *sql.DB
	credentialCipher *credentials.Cipher
}

func New(ctx context.Context, databaseURL string) (*Store, error) {
	config, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		return nil, errors.New("DATABASE_URL is not a valid Postgres connection string")
	}
	if config.ConnectTimeout == 0 || config.ConnectTimeout > connectTimeout {
		config.ConnectTimeout = connectTimeout
	}
	for key, value := range map[string]string{"statement_timeout": "30s", "idle_in_transaction_session_timeout": "60s"} {
		if _, set := config.RuntimeParams[key]; !set && !strings.Contains(config.RuntimeParams["options"], key) {
			config.RuntimeParams[key] = value
		}
	}
	db := stdlib.OpenDB(*config)
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

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) HealthCheck(ctx context.Context) error { return s.db.PingContext(ctx) }

func (s *Store) ReadinessCheck(ctx context.Context) error {
	expected, err := migrations.LatestVersion()
	if err != nil {
		return fmt.Errorf("find expected schema migration version: %w", err)
	}
	applied, dirty, err := migrations.VersionFromDB(ctx, s.db)
	if err != nil {
		return fmt.Errorf("inspect schema migration version: %w", err)
	}
	if dirty {
		return fmt.Errorf("schema migration version %d is dirty", applied)
	}
	if applied != expected {
		return fmt.Errorf("schema migration version %d does not match expected version %d", applied, expected)
	}
	return nil
}

func (s *Store) Participants() database.ParticipantRepository {
	return &participantRepository{db: s.db}
}

func (s *Store) Drivers() database.DriverRepository { return &driverRepository{db: s.db} }

func (s *Store) Settings() database.SettingsRepository {
	return &settingsRepository{db: s.db, cipher: s.credentialCipher}
}

func (s *Store) ActivityLocations() database.ActivityLocationRepository {
	return &activityLocationRepository{db: s.db}
}

func (s *Store) OrganizationVehicles() database.OrganizationVehicleRepository {
	return &organizationVehicleRepository{db: s.db}
}

func (s *Store) Events() database.EventRepository { return &eventRepository{db: s.db} }

func (s *Store) RouteFeedback() database.RouteFeedbackRepository {
	return &routeFeedbackRepository{db: s.db}
}

func (s *Store) DistanceCache() database.DistanceCacheRepository {
	return &distanceCacheRepository{db: s.db}
}

func (s *Store) Labels() database.LabelRepository { return &labelRepository{db: s.db} }

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

func mapUniqueViolation(err error) error {
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == pgerrcode.UniqueViolation {
		return database.ErrDuplicate
	}
	return err
}
