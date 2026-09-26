package postgres_test

import (
	"context"
	"encoding/base64"
	"ride-home-router/internal/postgres"
	"ride-home-router/internal/postgres/postgrestest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestCredentialEncryptionStorageAndRecovery(t *testing.T) {
	url := postgrestest.DatabaseURL(t)
	db, err := postgres.New(t.Context(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	}()
	conn, err := pgx.Connect(t.Context(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := conn.Close(context.Background()); err != nil {
			t.Error(err)
		}
	}()
	if err = db.ConfigureCredentialEncryption(""); err == nil {
		t.Fatal("missing key accepted")
	}
	if err = db.Settings().SetGoogleMapsKey(t.Context(), "must-not-be-plaintext"); err == nil {
		t.Fatal("unconfigured write accepted")
	}
	const secret = "synthetic-google-secret"
	if err = db.ConfigureCredentialEncryption(postgrestest.EncryptionKey); err != nil {
		t.Fatal(err)
	}
	if err = db.Settings().SetGoogleMapsKey(t.Context(), secret); err != nil {
		t.Fatal(err)
	}
	got, err := db.Settings().GoogleMapsKey(t.Context())
	if err != nil || got != secret {
		t.Fatal("saved credential unreadable")
	}
	var ciphertext string
	if err = conn.QueryRow(t.Context(), `SELECT encrypted_api_key FROM google_maps_credentials`).Scan(&ciphertext); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(ciphertext, secret) || !strings.HasPrefix(ciphertext, "v1:") {
		t.Fatal("plaintext at rest")
	}
	if _, err = conn.Exec(t.Context(), `INSERT INTO google_maps_credentials(id,api_key) VALUES(1,'old writer')`); err == nil {
		t.Fatal("old writer accepted")
	}
	if _, err = conn.Exec(t.Context(), `UPDATE google_maps_credentials SET encrypted_api_key='plaintext'`); err == nil {
		t.Fatal("plaintext constraint missing")
	}
	wrong := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("b", 32)))
	replica, err := postgres.New(t.Context(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := replica.Close(); err != nil {
			t.Error(err)
		}
	}()
	if err = replica.ConfigureCredentialEncryption(wrong); err != nil {
		t.Fatal(err)
	}
	if _, err = replica.Settings().GoogleMapsKey(t.Context()); err == nil {
		t.Fatal("wrong key decrypted credential")
	}
	if err = replica.Settings().SetGoogleMapsKey(t.Context(), "replacement-secret"); err != nil {
		t.Fatal(err)
	}
	if got, err = replica.Settings().GoogleMapsKey(t.Context()); err != nil || got != "replacement-secret" {
		t.Fatal("replacement unreadable")
	}
	if _, err = db.Settings().GoogleMapsKey(t.Context()); err == nil {
		t.Fatal("old encryption key accepted replacement")
	}
	if err = replica.Settings().DeleteGoogleMapsKey(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got, err = replica.Settings().GoogleMapsKey(t.Context()); err != nil || got != "" {
		t.Fatal("delete did not remove credential")
	}
}
