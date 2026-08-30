package main

import (
	"bytes"
	"ride-home-router/internal/postgres/postgrestest"
	"ride-home-router/migrations"
	"strings"
	"testing"
)

func TestRunUpPrintsLatestCleanVersion(t *testing.T) {
	for name, args := range map[string][]string{
		"default":  nil,
		"explicit": {"up"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("DATABASE_URL", postgrestest.DatabaseURL(t))
			var stdout, stderr bytes.Buffer

			code := run(args, &stdout, &stderr)

			if code != 0 {
				t.Fatalf("run(%v) code = %d stderr = %q, want 0", args, code, stderr.String())
			}
			if stdout.String() != "migration version 20260830000000 dirty=false\n" {
				t.Fatalf("run(%v) stdout = %q", args, stdout.String())
			}
			if stderr.Len() != 0 {
				t.Fatalf("run(%v) stderr = %q, want empty", args, stderr.String())
			}
		})
	}
}

func TestRunVersionPrintsCurrentState(t *testing.T) {
	t.Setenv("DATABASE_URL", postgrestest.DatabaseURL(t))
	var stdout, stderr bytes.Buffer

	code := run([]string{"version"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("run(version) code = %d stderr = %q, want 0", code, stderr.String())
	}
	if stdout.String() != "migration version 20260830000000 dirty=false\n" {
		t.Fatalf("run(version) stdout = %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("run(version) stderr = %q, want empty", stderr.String())
	}
}

func TestRunRequiresConfirmationBeforeDown(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	var stdout, stderr bytes.Buffer

	code := run([]string{"down"}, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("run(down) code = %d, want 1", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("run(down) stdout = %q, want empty", stdout.String())
	}
	if !strings.Contains(stderr.String(), "down requires --confirm") {
		t.Fatalf("run(down) stderr = %q, want confirmation refusal", stderr.String())
	}
	if strings.Contains(stderr.String(), "DATABASE_URL") {
		t.Fatalf("run(down) loaded config before confirmation: %q", stderr.String())
	}
}

func TestRunConfirmedDownRollsBackOneMigration(t *testing.T) {
	databaseURL := postgrestest.DatabaseURL(t)
	t.Setenv("DATABASE_URL", databaseURL)
	var stdout, stderr bytes.Buffer

	code := run([]string{"down", "--confirm"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("run(down --confirm) code = %d stderr = %q, want 0", code, stderr.String())
	}
	if stdout.String() != "migration version 20260829000000 dirty=false\n" || stderr.Len() != 0 {
		t.Fatalf("run(down --confirm) output = stdout %q stderr %q", stdout.String(), stderr.String())
	}
	version, dirty, err := migrations.Version(t.Context(), databaseURL)
	if err != nil {
		t.Fatalf("Version() after down error = %v", err)
	}
	if version != 20260829000000 || dirty {
		t.Fatalf("Version() after down = (%d, %t), want (20260829000000, false)", version, dirty)
	}
}

func TestRunConfirmedDownWarnsAgainstRetryAfterFailure(t *testing.T) {
	databaseURL := postgrestest.DatabaseURL(t)
	for range 2 {
		if err := migrations.Down(t.Context(), databaseURL); err != nil {
			t.Fatalf("Down() to baseline error = %v", err)
		}
	}
	t.Setenv("DATABASE_URL", databaseURL)
	var stdout, stderr bytes.Buffer

	code := run([]string{"down", "--confirm"}, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("run(down --confirm) code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "outcome is uncertain") || !strings.Contains(stderr.String(), "inspect migration version before retrying") {
		t.Fatalf("run(down --confirm) stderr = %q, want retry warning", stderr.String())
	}
	version, dirty, err := migrations.Version(t.Context(), databaseURL)
	if err != nil {
		t.Fatalf("Version() after refused down error = %v", err)
	}
	if version != 20260826000000 || dirty {
		t.Fatalf("Version() after refused down = (%d, %t), want (20260826000000, false)", version, dirty)
	}
}
