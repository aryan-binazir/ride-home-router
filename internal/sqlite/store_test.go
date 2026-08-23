package sqlite

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewInitializesSingleSchemaVersionRow(t *testing.T) {
	store, err := New(filepath.Join(t.TempDir(), "fresh.db"))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	assertSchemaVersionState(t, store.db, schemaVersion, 1)
}

func TestNewRepairsMultipleSchemaVersionRows(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "poisoned.db")
	seedStore, err := New(dbPath)
	if err != nil {
		t.Fatalf("seed New() error = %v", err)
	}
	if _, err := seedStore.db.ExecContext(context.Background(), `
		DROP TABLE participant_labels;
		DROP TABLE driver_labels;
		DROP TABLE labels;
		INSERT INTO schema_version (version) VALUES (2)
	`); err != nil {
		t.Fatalf("seed poisoned pre-v4 schema: %v", err)
	}
	if err := seedStore.Close(); err != nil {
		t.Fatalf("seed Close() error = %v", err)
	}

	store, err := New(dbPath)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	assertSchemaVersionState(t, store.db, schemaVersion, 1)
	for _, tableName := range []string{"labels", "participant_labels", "driver_labels"} {
		assertTableExists(t, store.db, tableName)
	}
	for _, indexName := range []string{
		"idx_labels_name",
		"idx_participant_labels_participant",
		"idx_driver_labels_driver",
	} {
		assertSchemaObjectExists(t, store.db, "index", indexName)
	}
}

func TestNewReopensCurrentSchemaWithSingleVersionRow(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "current.db")
	store, err := New(dbPath)
	if err != nil {
		t.Fatalf("first New() error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}

	store, err = New(dbPath)
	if err != nil {
		t.Fatalf("second New() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	assertSchemaVersionState(t, store.db, schemaVersion, 1)
}

func TestInitSchemaReturnsUnexpectedVersionReadError(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "closed.db"))
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	err = (&Store{db: db}).initSchema()
	if err == nil {
		t.Fatal("initSchema() error = nil, want closed database error")
	}
	if !strings.Contains(err.Error(), "failed to read schema version") {
		t.Fatalf("initSchema() error = %q, want schema version read context", err)
	}
}

func assertSchemaVersionState(t *testing.T, db *sql.DB, wantVersion, wantRows int) {
	t.Helper()

	var version, rows int
	if err := db.QueryRowContext(context.Background(), `
		SELECT COALESCE(MAX(version), 0), COUNT(*) FROM schema_version
	`).Scan(&version, &rows); err != nil {
		t.Fatalf("query schema_version state: %v", err)
	}
	if version != wantVersion {
		t.Errorf("schema version = %d, want %d", version, wantVersion)
	}
	if rows != wantRows {
		t.Errorf("schema_version rows = %d, want %d", rows, wantRows)
	}
}

func assertSchemaObjectExists(t *testing.T, db *sql.DB, objectType, name string) {
	t.Helper()

	var count int
	if err := db.QueryRowContext(context.Background(), `
		SELECT COUNT(*) FROM sqlite_master WHERE type = ? AND name = ?
	`, objectType, name).Scan(&count); err != nil {
		t.Fatalf("query %s %q: %v", objectType, name, err)
	}
	if count != 1 {
		t.Fatalf("%s %q count = %d, want 1", objectType, name, count)
	}
}

func TestNewAppliesConnectionPragmasToEveryPooledConnection(t *testing.T) {
	store, err := New(filepath.Join(t.TempDir(), "database with spaces.db"))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})

	ctx := context.Background()
	connections := make([]*sql.Conn, 0, 3)
	for i := range 3 {
		conn, err := store.db.Conn(ctx)
		if err != nil {
			t.Fatalf("Conn() %d error = %v", i, err)
		}
		connections = append(connections, conn)

		var foreignKeys int
		if err := conn.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
			t.Fatalf("connection %d foreign_keys query error = %v", i, err)
		}
		if foreignKeys != 1 {
			t.Errorf("connection %d foreign_keys = %d, want 1", i, foreignKeys)
		}

		var busyTimeout int
		if err := conn.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&busyTimeout); err != nil {
			t.Fatalf("connection %d busy_timeout query error = %v", i, err)
		}
		if busyTimeout != sqliteBusyTimeoutMS {
			t.Errorf("connection %d busy_timeout = %d, want %d", i, busyTimeout, sqliteBusyTimeoutMS)
		}

		var journalMode string
		if err := conn.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journalMode); err != nil {
			t.Fatalf("connection %d journal_mode query error = %v", i, err)
		}
		if journalMode != "wal" {
			t.Errorf("connection %d journal_mode = %q, want %q", i, journalMode, "wal")
		}

		var synchronous int
		if err := conn.QueryRowContext(ctx, "PRAGMA synchronous").Scan(&synchronous); err != nil {
			t.Fatalf("connection %d synchronous query error = %v", i, err)
		}
		if synchronous != 1 { // NORMAL
			t.Errorf("connection %d synchronous = %d, want 1 (NORMAL)", i, synchronous)
		}

		var cacheSize int
		if err := conn.QueryRowContext(ctx, "PRAGMA cache_size").Scan(&cacheSize); err != nil {
			t.Fatalf("connection %d cache_size query error = %v", i, err)
		}
		if cacheSize != sqliteCacheSizeKB {
			t.Errorf("connection %d cache_size = %d, want %d", i, cacheSize, sqliteCacheSizeKB)
		}

		if _, err := conn.ExecContext(ctx, `
			INSERT INTO participant_labels (label_id, participant_id) VALUES (?, ?)
		`, 1000+i, 1000+i); err == nil {
			t.Errorf("connection %d allowed an insert that violates foreign keys", i)
		}
	}
	t.Cleanup(func() {
		for i, conn := range connections {
			if err := conn.Close(); err != nil {
				t.Errorf("connection %d Close() error = %v", i, err)
			}
		}
	})
}

func TestNewAcceptsRelativeDatabasePath(t *testing.T) {
	t.Chdir(t.TempDir())
	dbPath := filepath.Join("data directory", "relative database.db")

	store, err := New(dbPath)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})

	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("database file Stat() error = %v", err)
	}
}
