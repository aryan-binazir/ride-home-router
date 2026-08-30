// Package postgrestest provides schema-isolated, migrated Postgres stores for tests.
package postgrestest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"ride-home-router/internal/postgres"
	"ride-home-router/migrations"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
)

// EnvVar names the connection string tests use; unset skips database tests.
const EnvVar = "TEST_DATABASE_URL"

var (
	schemaSequence     atomic.Uint64
	schemaProcessNonce = newProcessNonce()
	unsafeSchemaChars  = regexp.MustCompile(`[^a-z0-9_]+`)
)

// Open returns a migrated store in a schema owned by this test.
func Open(t testing.TB) *postgres.Store {
	t.Helper()
	store, err := postgres.New(context.Background(), DatabaseURL(t))
	if err != nil {
		t.Fatalf("open test Postgres store: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("close test Postgres store: %v", err)
		}
	})
	return store
}

// DatabaseURL returns a migrated test schema that is dropped after the test.
func DatabaseURL(t testing.TB) string {
	t.Helper()
	databaseURL := UnmigratedDatabase(t)
	if err := migrations.Run(context.Background(), databaseURL); err != nil {
		t.Fatalf("migrate test schema: %v", err)
	}
	return databaseURL
}

// UnmigratedDatabase returns an empty test schema that is dropped after the test.
func UnmigratedDatabase(t testing.TB) string {
	t.Helper()
	base := strings.TrimSpace(os.Getenv(EnvVar))
	if base == "" {
		t.Skipf("%s is not set", EnvVar)
	}
	parsed, err := url.Parse(base)
	if err != nil {
		t.Fatalf("parse %s: %v", EnvVar, err)
	}
	schema := schemaName(t.Name(), schemaSequence.Add(1))
	quoted := pgx.Identifier{schema}.Sanitize()

	ctx := context.Background()
	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatalf("connect to test Postgres: %v", err)
	}
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		_ = admin.Close(ctx)
		t.Fatalf("create test schema: %v", err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(ctx, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			t.Errorf("drop test schema: %v", err)
		}
		if err := admin.Close(ctx); err != nil {
			t.Errorf("close test schema connection: %v", err)
		}
	})

	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func newProcessNonce() string {
	var value [4]byte
	if _, err := rand.Read(value[:]); err != nil {
		panic(fmt.Sprintf("postgrestest: read random nonce: %v", err))
	}
	return hex.EncodeToString(value[:])
}

func schemaName(testName string, sequence uint64) string {
	name := strings.Trim(unsafeSchemaChars.ReplaceAllString(strings.ToLower(testName), "_"), "_")
	suffix := fmt.Sprintf("_%s_%d", schemaProcessNonce, sequence)
	const maxIdentifierBytes = 63
	if limit := maxIdentifierBytes - len("t_") - len(suffix); len(name) > limit {
		name = name[:limit]
	}
	return "t_" + name + suffix
}
