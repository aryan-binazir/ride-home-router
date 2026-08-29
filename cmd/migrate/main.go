package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"ride-home-router/migrations"
	"strings"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	action := "up"
	if len(args) > 0 {
		action = args[0]
		args = args[1:]
	}

	if action == "up" {
		if len(args) != 0 {
			_, _ = fmt.Fprintln(stderr, "migration: usage: migrate [up|version|down --confirm]")
			return 1
		}
		databaseURL, ok := loadDatabaseURL(stderr)
		if !ok {
			return 1
		}
		if err := migrations.Run(context.Background(), databaseURL); err != nil {
			_, _ = fmt.Fprintf(stderr, "migration: %v\n", err)
			return 1
		}
		if err := printVersion(databaseURL, stdout); err != nil {
			_, _ = fmt.Fprintf(stderr, "migration: %v\n", err)
			return 1
		}
		return 0
	}
	if action == "version" {
		if len(args) != 0 {
			_, _ = fmt.Fprintln(stderr, "migration: usage: migrate version")
			return 1
		}
		databaseURL, ok := loadDatabaseURL(stderr)
		if !ok {
			return 1
		}
		if err := printVersion(databaseURL, stdout); err != nil {
			_, _ = fmt.Fprintf(stderr, "migration: %v\n", err)
			return 1
		}
		return 0
	}
	if action != "down" {
		_, _ = fmt.Fprintf(stderr, "migration: unsupported action %q\n", action)
		return 1
	}

	flags := flag.NewFlagSet("migrate down", flag.ContinueOnError)
	flags.SetOutput(stderr)
	confirmed := flags.Bool("confirm", false, "confirm one destructive migration rollback")
	if err := flags.Parse(args); err != nil {
		return 1
	}
	if flags.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "migration: usage: migrate down --confirm")
		return 1
	}
	if !*confirmed {
		_, _ = fmt.Fprintln(stderr, "migration: down requires --confirm")
		return 1
	}
	databaseURL, ok := loadDatabaseURL(stderr)
	if !ok {
		return 1
	}
	if err := migrations.Down(context.Background(), databaseURL); err != nil {
		_, _ = fmt.Fprintf(stderr, "migration: %v\n", err)
		return 1
	}
	return 0
}

func loadDatabaseURL(stderr io.Writer) (string, bool) {
	databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if databaseURL == "" {
		_, _ = fmt.Fprintln(stderr, "migration: DATABASE_URL is required")
		return "", false
	}
	return databaseURL, true
}

func printVersion(databaseURL string, output io.Writer) error {
	version, dirty, err := migrations.Version(context.Background(), databaseURL)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(output, "migration version %d dirty=%t\n", version, dirty)
	return err
}
