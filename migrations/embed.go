// Package migrations owns and applies the embedded Postgres schema.
package migrations

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"time"

	"github.com/golang-migrate/migrate/v4"
	migratepgx "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

const (
	connectTimeout   = 10 * time.Second
	statementTimeout = 5 * time.Minute
)

//go:embed *.sql
var files embed.FS

// Run applies every pending migration to the database at databaseURL. The
// search_path of the URL decides which schema is migrated, which is how tests
// isolate themselves.
func Run(ctx context.Context, databaseURL string) (err error) {
	config, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		return fmt.Errorf("parse database URL for migrations: %w", err)
	}
	if config.ConnectTimeout == 0 || config.ConnectTimeout > connectTimeout {
		config.ConnectTimeout = connectTimeout
	}
	db := stdlib.OpenDB(*config)
	db.SetMaxOpenConns(1)
	defer func() { err = errors.Join(err, db.Close()) }()

	pingCtx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		return fmt.Errorf("connect to Postgres for migrations: %w", err)
	}

	sourceDriver, err := iofs.New(files, ".")
	if err != nil {
		return fmt.Errorf("open embedded migrations: %w", err)
	}
	databaseDriver, err := migratepgx.WithInstance(db, &migratepgx.Config{
		MigrationsTable:  "schema_migrations",
		StatementTimeout: statementTimeout,
	})
	if err != nil {
		return errors.Join(fmt.Errorf("initialize migration database: %w", err), sourceDriver.Close())
	}
	migrator, err := migrate.NewWithInstance("iofs", sourceDriver, "pgx5", databaseDriver)
	if err != nil {
		return errors.Join(fmt.Errorf("create migrator: %w", err), sourceDriver.Close(), databaseDriver.Close())
	}
	defer func() {
		sourceErr, databaseErr := migrator.Close()
		err = errors.Join(err, sourceErr, databaseErr)
	}()

	if err := migrator.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migrate up: %w", err)
	}
	return nil
}
