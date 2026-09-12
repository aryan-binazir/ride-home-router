package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestInvalidDatabaseURLDoesNotExposeSecrets(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://u@h/db?password=SECRET&sslmode=bogus")
	var out, errs bytes.Buffer
	if run([]string{"up"}, &out, &errs) == 0 || strings.Contains(errs.String(), "SECRET") || strings.Contains(errs.String(), "postgres://") {
		t.Fatalf("unsafe error: %s", errs.String())
	}
}
