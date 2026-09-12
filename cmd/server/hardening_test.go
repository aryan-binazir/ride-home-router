package main

import (
	"strings"
	"testing"
)

func TestStartupRejectsDatabaseURLWithoutSecrets(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://u@h/db?password=SECRET&sslmode=bogus")
	err := run(nil)
	if err == nil || strings.Contains(err.Error(), "SECRET") || strings.Contains(err.Error(), "postgres://") {
		t.Fatalf("unsafe startup error: %v", err)
	}
}
