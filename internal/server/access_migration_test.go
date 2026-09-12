package server

import (
	"context"
	"net/http"
	"reflect"
	"ride-home-router/internal/access/accesstest"
	"ride-home-router/internal/postgres/postgrestest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestAccessMigrationBetweenClerkInstances(t *testing.T) {
	databaseURL := postgrestest.DatabaseURL(t)
	const adminEmail = "admin@example.test"
	start := func(f *accesstest.Fixture, admin string) (*Server, string) {
		t.Helper()
		s, err := New(t.Context(), Config{Addr: "127.0.0.1:0", DatabaseURL: databaseURL, Auth: f.Config(admin)})
		if err != nil {
			t.Fatal(err)
		}
		addr, err := s.Start()
		if err != nil {
			_ = s.Shutdown(t.Context())
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := s.Shutdown(context.Background()); err != nil {
				t.Error(err)
			}
		})
		return s, "http://" + addr
	}
	identity := func(f *accesstest.Fixture, id, email string, verified bool) string {
		t.Helper()
		if verified {
			f.User(id, []string{email}, nil)
		} else {
			f.User(id, nil, []string{email})
		}
		f.Session("session_"+id, id, "active")
		return f.Token(id, "session_"+id)
	}
	old := accesstest.NewInstance(t, "old.clerk.accounts.dev")
	_, oldBase := start(old, adminEmail)
	conn, err := pgx.Connect(t.Context(), databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	// Historical admins are persistence evidence only; never seed admission through SQL.
	history := func() map[string]time.Time {
		t.Helper()
		rows, err := conn.Query(t.Context(), `SELECT email, first_verified_at FROM verified_admin_emails ORDER BY email`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		result := map[string]time.Time{}
		for rows.Next() {
			var email string
			var first time.Time
			if err := rows.Scan(&email, &first); err != nil {
				t.Fatal(err)
			}
			if first.IsZero() {
				t.Fatal("missing first verification timestamp")
			}
			result[email] = first
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return result
	}
	if got := history(); len(got) != 0 {
		t.Fatalf("admin recorded before authentication: %v", got)
	}
	h := accessHTTP{t, &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	request := func(base, method, path, token, body string, want int) string {
		t.Helper()
		status, data, headers := h.request(base, method, path, token, body, "", nil)
		if status != want {
			t.Fatalf("%s %s at %s: got %d want %d: %s", method, path, base, status, want, data)
		}
		if want == 401 || want == 403 {
			assertAccessDenied(t, status, data, headers, want, method)
		}
		return data
	}
	oldAdmin := identity(old, "old_admin", adminEmail, true)
	unverifiedAdmin := identity(old, "old_unverified_admin", adminEmail, false)
	request(oldBase, "GET", "/api/v1/access", unverifiedAdmin, "", 403)
	if got := history(); len(got) != 0 {
		t.Fatalf("unverified admin recorded: %v", got)
	}
	request(oldBase, "POST", "/api/v1/access", oldAdmin, `{"email":"member@gmail.com"}`, 200)
	recorded := history()
	if _, ok := recorded[adminEmail]; !ok || len(recorded) != 1 {
		t.Fatalf("verified old admin not recorded: %v", recorded)
	}
	oldMember := identity(old, "old_member", "member@gmail.com", true)
	request(oldBase, "POST", "/api/v1/labels", oldMember, `{"name":"migration persistence evidence"}`, 201)
	for _, hostname := range []string{"new-one.clerk.accounts.dev", "new-two.clerk.accounts.dev"} {
		f := accesstest.NewInstance(t, hostname)
		s, base := start(f, adminEmail)
		admin := identity(f, hostname+"_admin", adminEmail, true)
		member := identity(f, hostname+"_member", "member@gmail.com", true)
		unknown := identity(f, hostname+"_unknown", "unknown@gmail.com", true)
		unverified := identity(f, hostname+"_unverified", "member@gmail.com", false)
		before := accessDatabaseSnapshot(t, conn)
		request(base, "GET", "/api/v1/labels", oldMember, "", 401)
		request(oldBase, "GET", "/api/v1/labels", member, "", 401)
		request(base, "GET", "/api/v1/labels", unknown, "", 403)
		request(base, "GET", "/api/v1/labels", unverified, "", 403)
		request(base, "GET", "/api/v1/labels", "", "", 401)
		if !reflect.DeepEqual(before, accessDatabaseSnapshot(t, conn)) {
			t.Fatal("denied migration requests wrote database state")
		}
		if data := request(base, "GET", "/api/v1/labels", member, "", 200); !strings.Contains(data, "migration persistence evidence") {
			t.Fatalf("persisted app data missing: %s", data)
		}
		request(base, "GET", "/api/v1/access", admin, "", 200)
		if !reflect.DeepEqual(recorded, history()) {
			t.Fatal("same admin email duplicated or first verification changed across Clerk instances")
		}
		// Restart with the same Clerk instance but a different environment admin.
		if err := s.Shutdown(t.Context()); err != nil {
			t.Fatal(err)
		}
		_, restarted := start(f, "replacement@example.test")
		before = accessDatabaseSnapshot(t, conn)
		request(restarted, "GET", "/api/v1/labels", admin, "", 403)
		request(restarted, "GET", "/api/v1/access", admin, "", 403)
		request(restarted, "POST", "/api/v1/access", admin, `{"email":"unknown@gmail.com"}`, 403)
		if !reflect.DeepEqual(before, accessDatabaseSnapshot(t, conn)) {
			t.Fatal("historical admin denial wrote database state")
		}
		if got := history(); got[adminEmail] != recorded[adminEmail] {
			t.Fatal("former admin history disappeared")
		}
		replacement := identity(f, hostname+"_replacement", "replacement@example.test", true)
		request(restarted, "POST", "/api/v1/access", replacement, `{"email":"admin@example.test"}`, 200)
		request(restarted, "GET", "/api/v1/labels", admin, "", 200)
		request(restarted, "GET", "/api/v1/access", admin, "", 403)
		request(restarted, "POST", "/api/v1/access", admin, `{"email":"unknown@gmail.com"}`, 403)
		request(restarted, "DELETE", "/api/v1/access", replacement, `{"email":"admin@example.test"}`, 200)
		// Preserve history for the next instance's idempotency assertion.
		recorded = history()
	}
}
