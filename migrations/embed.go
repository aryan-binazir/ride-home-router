// Package migrations owns and applies the embedded Postgres schema.
package migrations

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/golang-migrate/migrate/v4"
	migratepgx "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

const (
	connectTimeout              = 10 * time.Second
	migrationLockTimeout        = 10 * time.Second
	sessionLockTimeout          = 9 * time.Second
	statementTimeout            = 5 * time.Minute
	disabledDownMigrationMarker = "ride-home-router: down migration disabled"
)

//go:embed *.sql
var files embed.FS

var latestVersion = sync.OnceValues(findLatestVersion)

// LatestVersion returns the newest embedded migration version.
func LatestVersion() (uint, error) {
	return latestVersion()
}

func findLatestVersion() (uint, error) {
	driver, err := iofs.New(files, ".")
	if err != nil {
		return 0, fmt.Errorf("open embedded migrations: %w", err)
	}
	version, err := driver.First()
	if err != nil {
		return 0, fmt.Errorf("find first embedded migration: %w", err)
	}
	for {
		next, nextErr := driver.Next(version)
		if errors.Is(nextErr, fs.ErrNotExist) {
			return version, nil
		}
		if nextErr != nil {
			return 0, fmt.Errorf("find embedded migration after %d: %w", version, nextErr)
		}
		version = next
	}
}

// Run applies pending migrations to the URL's search path.
func Run(ctx context.Context, databaseURL string) error {
	return withMigrator(ctx, databaseURL, func(migrator *migrate.Migrate, _ source.Driver) error {
		if err := migrator.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
			return migrationOperationError("up", err)
		}
		return nil
	})
}

// Version returns the current migration version and dirty state. An
// unmigrated database reports version zero and dirty false.
func Version(ctx context.Context, databaseURL string) (version uint, dirty bool, err error) {
	database, err := openDatabase(ctx, databaseURL)
	if err != nil {
		return 0, false, err
	}
	defer func() { err = errors.Join(err, database.Close()) }()
	return VersionFromDB(ctx, database)
}

// VersionFromDB returns the migration state using an existing connection pool.
func VersionFromDB(ctx context.Context, database *sql.DB) (version uint, dirty bool, err error) {
	var currentSchema sql.NullString
	var exists bool
	if err := database.QueryRowContext(ctx, `
		SELECT current_schema(), EXISTS (
			SELECT 1
			FROM pg_catalog.pg_class AS class
			JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid = class.relnamespace
			WHERE namespace.nspname = current_schema()
			  AND class.relname = 'schema_migrations'
			  AND class.relkind IN ('r', 'p')
		)
	`).Scan(&currentSchema, &exists); err != nil {
		return 0, false, fmt.Errorf("inspect migration table: %w", err)
	}
	if !currentSchema.Valid {
		return 0, false, errors.New("inspect migration table: search_path has no current schema")
	}
	if !exists {
		return 0, false, nil
	}

	var storedVersion int64
	if err := database.QueryRowContext(ctx, "SELECT version, dirty FROM schema_migrations LIMIT 1").Scan(&storedVersion, &dirty); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("inspect migration version: %w", err)
	}
	if storedVersion < 0 {
		return 0, false, fmt.Errorf("inspect migration version: invalid negative version %d", storedVersion)
	}
	return uint(storedVersion), dirty, nil
}

// Down rolls back exactly one migration.
func Down(ctx context.Context, databaseURL string) error {
	return withMigrator(ctx, databaseURL, func(migrator *migrate.Migrate, sourceDriver source.Driver) error {
		if err := preflightDown(migrator, sourceDriver); err != nil {
			return err
		}
		if err := migrator.Steps(-1); err != nil &&
			!errors.Is(err, migrate.ErrNoChange) &&
			!errors.Is(err, migrate.ErrNilVersion) {
			return migrationOperationError("down", err)
		}
		return nil
	})
}

func migrationOperationError(action string, err error) error {
	if dirty, ok := errors.AsType[migrate.ErrDirty](err); ok {
		return fmt.Errorf(
			"migrate %s: migration state is dirty at version %d; repair or restore the database before retrying: %w",
			action,
			dirty.Version,
			err,
		)
	}
	return fmt.Errorf("migrate %s: %w", action, err)
}

func preflightDown(migrator *migrate.Migrate, sourceDriver source.Driver) error {
	version, dirty, err := migrator.Version()
	if errors.Is(err, migrate.ErrNilVersion) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect migration version before down: %w", err)
	}
	if dirty {
		return fmt.Errorf("refuse down: migration state is dirty at version %d; repair or restore the database before retrying", version)
	}

	migration, identifier, err := sourceDriver.ReadDown(version)
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("refuse down at version %d: missing down migration", version)
	}
	if err != nil {
		return fmt.Errorf("inspect down migration for version %d: %w", version, err)
	}
	body, readErr := io.ReadAll(migration)
	closeErr := migration.Close()
	if readErr != nil {
		return errors.Join(fmt.Errorf("read down migration %s: %w", identifier, readErr), closeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close down migration %s: %w", identifier, closeErr)
	}
	if strings.Contains(string(body), disabledDownMigrationMarker) {
		return fmt.Errorf("refuse down at version %d: down migration disabled by %s", version, identifier)
	}
	if !hasExecutableSQL(string(body)) {
		return fmt.Errorf("refuse down at version %d: down migration %s contains no executable SQL", version, identifier)
	}
	return nil
}

func hasExecutableSQL(body string) bool {
	for offset := 0; offset < len(body); {
		rest := body[offset:]
		if strings.HasPrefix(rest, "--") {
			if newline := strings.IndexAny(rest, "\r\n"); newline >= 0 {
				offset += newline + 1
				continue
			}
			return false
		}
		if strings.HasPrefix(rest, "/*") {
			offset += 2
			depth := 1
			for offset < len(body) && depth > 0 {
				switch {
				case strings.HasPrefix(body[offset:], "/*"):
					depth++
					offset += 2
				case strings.HasPrefix(body[offset:], "*/"):
					depth--
					offset += 2
				default:
					_, size := utf8.DecodeRuneInString(body[offset:])
					offset += size
				}
			}
			continue
		}
		r, size := utf8.DecodeRuneInString(rest)
		if r == '\ufeff' || r == ';' || unicode.IsSpace(r) {
			offset += size
			continue
		}
		return true
	}
	return false
}

func withMigrator(ctx context.Context, databaseURL string, operation func(*migrate.Migrate, source.Driver) error) (err error) {
	db, err := openDatabase(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, db.Close()) }()

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
	migrator.LockTimeout = migrationLockTimeout
	defer func() {
		sourceErr, databaseErr := migrator.Close()
		err = errors.Join(err, sourceErr, databaseErr)
	}()

	return operation(migrator, sourceDriver)
}

func openDatabase(ctx context.Context, databaseURL string) (*sql.DB, error) {
	config, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database URL for migrations: %w", err)
	}
	if config.ConnectTimeout == 0 || config.ConnectTimeout > connectTimeout {
		config.ConnectTimeout = connectTimeout
	}
	config.RuntimeParams["lock_timeout"] = strconv.FormatInt(sessionLockTimeout.Milliseconds(), 10)
	db := stdlib.OpenDB(*config)
	db.SetMaxOpenConns(1)

	pingCtx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		return nil, errors.Join(fmt.Errorf("connect to Postgres for migrations: %w", err), db.Close())
	}
	return db, nil
}
