package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"ride-home-router/internal/access/accesstest"
	"ride-home-router/internal/postgres/postgrestest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestVerifiedAdminEmailPersistenceAndFailure(t *testing.T) {
	databaseURL := postgrestest.DatabaseURL(t)
	f := accesstest.New(t)
	f.User("admin", []string{"ADMIN@example.test", "unrelated@example.test"}, []string{"second@example.test"})
	f.Session("admin_session", "admin", "active")
	token := f.Token("admin", "admin_session")
	s, err := New(t.Context(), Config{Addr: "127.0.0.1:0", DatabaseURL: databaseURL, Auth: f.Config("admin@example.test", "second@example.test")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	})
	// Use the same public HTTP handler as production, with signed cookies.
	request := func(method string, origin bool) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequestWithContext(t.Context(), method, "http://localhost:8080/api/v1/settings", strings.NewReader(`{"use_miles":true}`))
		r.AddCookie(&http.Cookie{Name: "__session", Value: token, Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode})
		if origin {
			r.Header.Set("Origin", "http://localhost:8080")
		}
		w := httptest.NewRecorder()
		s.httpServer.Handler.ServeHTTP(w, r)
		return w
	}
	w := request("GET", false)
	if w.Code != 200 {
		t.Fatalf("admin admitted: %d %s", w.Code, w.Body.String())
	}
	conn, err := pgx.Connect(t.Context(), databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	var emails string
	if err := conn.QueryRow(t.Context(), `SELECT string_agg(email,',' ORDER BY email) FROM verified_admin_emails`).Scan(&emails); err != nil {
		t.Fatal(err)
	}
	if emails != "admin@example.test" {
		t.Fatalf("recorded emails %q; want only verified configured admin", emails)
	}
	// Fresh identity lookups reject lost verification without deleting history.
	f.User("admin", []string{"unrelated@example.test"}, []string{"admin@example.test"})
	f.Session("unverified_session", "admin", "active")
	token = f.Token("admin", "unverified_session")
	w = request("GET", false)
	if w.Code != 403 {
		t.Fatalf("unverified former admin got %d", w.Code)
	}
	f.User("admin", []string{"second@example.test"}, nil)
	f.Session("second_session", "admin", "active")
	token = f.Token("admin", "second_session")
	w = request("PUT", false)
	if w.Code != 403 {
		t.Fatalf("CSRF denial got %d", w.Code)
	}
	var count int
	if err := conn.QueryRow(t.Context(), `SELECT count(*) FROM verified_admin_emails`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("denied request created admin email record: %d", count)
	}
	// Force a persistence failure after valid identity verification.
	if _, err := conn.Exec(t.Context(), `ALTER TABLE verified_admin_emails RENAME TO unavailable_admin_emails`); err != nil {
		t.Fatal(err)
	}
	f.Session("history_failure_session", "admin", "active")
	token = f.Token("admin", "history_failure_session")
	w = request("GET", false)
	if w.Code != 200 {
		t.Fatalf("failed history persistence blocked access: %d %s", w.Code, w.Body.String())
	}
}
