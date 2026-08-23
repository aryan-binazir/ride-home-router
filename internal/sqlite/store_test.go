package sqlite

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

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

	if len(connections) != 3 {
		t.Fatalf("opened %d connections, want 3", len(connections))
	}
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
