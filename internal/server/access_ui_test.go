package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"ride-home-router/internal/access/accesstest"
	"ride-home-router/internal/postgres/postgrestest"
	"strings"
	"testing"
)

func TestAccessSettingsCookieManagement(t *testing.T) {
	f := accesstest.New(t)
	admin := f.Admin()
	f.User("member", []string{"member@example.test"}, nil)
	f.Session("member_session", "member", "active")
	member := f.Token("member", "member_session")
	s, err := New(t.Context(), Config{Addr: "127.0.0.1:0", DatabaseURL: postgrestest.DatabaseURL(t), Auth: f.Config()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	})
	allowlist, err := newRequestAllowlist("127.0.0.1:8080", nil)
	if err != nil {
		t.Fatal(err)
	}
	handler := requestSecurityMiddleware(allowlist, s.httpServer.Handler)
	request := func(method, path, token, body string, hx bool) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequestWithContext(t.Context(), method, "http://localhost:8080"+path, strings.NewReader(body))
		r.AddCookie(&http.Cookie{Name: "__session", Value: token, Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode})
		r.Header.Set("Origin", "http://localhost:8080")
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if hx {
			r.Header.Set("HX-Request", "true")
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	w := request("GET", "/settings", admin, "", false)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `hx-get="/api/v1/access"`) {
		t.Fatalf("admin settings %d %s", w.Code, w.Body.String())
	}
	w = request("POST", "/api/v1/access", admin, "email=MEMBER%40example.test", true)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "member@example.test") || !strings.Contains(w.Body.String(), `hx-delete="/api/v1/access"`) {
		t.Fatalf("approve HTMX %d %s", w.Code, w.Body.String())
	}
	w = request("GET", "/settings", member, "", false)
	if w.Code != 200 || strings.Contains(w.Body.String(), `hx-get="/api/v1/access"`) {
		t.Fatalf("member settings %d %s", w.Code, w.Body.String())
	}
	w = request("GET", "/api/v1/access", member, "", true)
	if w.Code != 403 || strings.Contains(w.Body.String(), "member@example.test") {
		t.Fatalf("member access list %d %s", w.Code, w.Body.String())
	}
	// HTMX sends DELETE form parameters in the query string by default.
	w = request("DELETE", "/api/v1/access?"+url.Values{"email": {"member@example.test"}}.Encode(), admin, "", true)
	if w.Code != 200 || strings.Contains(w.Body.String(), "member@example.test") {
		t.Fatalf("remove HTMX %d %s", w.Code, w.Body.String())
	}
	w = request("GET", "/settings", member, "", false)
	if w.Code != 403 {
		t.Fatalf("revoked cookie status %d", w.Code)
	}
	w = request("POST", "/api/v1/access", admin, "email=admin%40example.test", true)
	if w.Code != 400 || !strings.Contains(w.Header().Get("HX-Trigger"), "showToast") {
		t.Fatalf("environment admin editable via UI: %d", w.Code)
	}
}
