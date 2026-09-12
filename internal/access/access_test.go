package access_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"ride-home-router/internal/access"
	"ride-home-router/internal/access/accesstest"
	"strings"
	"testing"
)

type approvals struct {
	allowed bool
	err     error
}

func (s *approvals) Approved(context.Context, []string) (bool, error)  { return s.allowed, s.err }
func (s *approvals) ApprovedEmails(context.Context) ([]string, error)  { return nil, s.err }
func (s *approvals) AddApprovedEmail(context.Context, string) error    { return s.err }
func (s *approvals) RemoveApprovedEmail(context.Context, string) error { return s.err }

func TestConfigurationFailsClosed(t *testing.T) {
	f := accesstest.New(t)
	for name, change := range map[string]func(*access.Config){
		"secret":          func(c *access.Config) { c.SecretKey = "" },
		"publishable":     func(c *access.Config) { c.PublishableKey = "invalid" },
		"key":             func(c *access.Config) { c.JWTKey = "invalid" },
		"admins":          func(c *access.Config) { c.AdminEmails = "" },
		"malformed admin": func(c *access.Config) { c.AdminEmails = "Name <admin@example.test>" },
		"origins":         func(c *access.Config) { c.AuthorizedParties = "" },
		"wildcard":        func(c *access.Config) { c.AuthorizedParties = "*" },
		"http public":     func(c *access.Config) { c.AuthorizedParties = "http://router.example.com" },
		"origin path":     func(c *access.Config) { c.AuthorizedParties = "https://router.example.com/" },
	} {
		t.Run(name, func(t *testing.T) {
			cfg := f.Config()
			change(&cfg)
			if _, err := access.New(cfg, &approvals{}); err == nil {
				t.Fatal("accepted invalid configuration")
			}
		})
	}
}

func TestAdmissionFailureAndCookieCSRF(t *testing.T) {
	f := accesstest.New(t)
	admin := f.Admin()
	f.User("user_member", []string{"member@example.test"}, nil)
	f.Session("sess_member", "user_member", "active")
	store := &approvals{allowed: true}
	gate, err := access.New(f.Config(), store)
	if err != nil {
		t.Fatal(err)
	}
	reached := 0
	handler := gate.Protect(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached++; w.WriteHeader(http.StatusNoContent) }))
	for _, tc := range []struct {
		name, method, token, origin, authorization string
		duplicate                                  bool
		status                                     int
	}{
		{name: "cookie read", method: "GET", token: admin, status: 204},
		{name: "cookie write no origin", method: "POST", token: admin, status: 403},
		{name: "cookie write foreign origin", method: "POST", token: admin, origin: "https://evil.example", status: 403},
		{name: "cookie write wrong host", method: "POST", token: admin, origin: accesstest.Origin, status: 403},
		{name: "cookie write same origin", method: "POST", token: admin, origin: "http://localhost:8080", status: 204},
		{name: "duplicate cookie", method: "GET", token: admin, duplicate: true, status: 401},
		{name: "bad bearer overrides cookie", method: "GET", token: admin, authorization: "Bearer forged", status: 401},
		{name: "pending token", method: "GET", token: f.Token("user_admin", "sess_admin", map[string]any{"sts": "pending"}), status: 401},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := reached
			r := httptest.NewRequestWithContext(t.Context(), tc.method, "http://localhost:8080/api/v1/settings", nil)
			r.AddCookie(&http.Cookie{Name: "__session", Value: tc.token, Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode})
			if tc.duplicate {
				r.AddCookie(&http.Cookie{Name: "__session", Value: tc.token, Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode})
			}
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			if tc.authorization != "" {
				r.Header.Set("Authorization", tc.authorization)
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("status %d body %s", w.Code, w.Body.String())
			}
			if tc.status != 204 && reached != before {
				t.Fatal("denial reached downstream")
			}
		})
	}
	store.err = errors.New("private database failure")
	r := httptest.NewRequestWithContext(t.Context(), "GET", "/participants", nil)
	r.Header.Set("Authorization", "Bearer "+f.Token("user_member", "sess_member"))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 503 || strings.Contains(w.Body.String(), "private") {
		t.Fatalf("failure disclosed details or did not deny: %d %s", w.Code, w.Body.String())
	}
}

func TestNavigationAndHTMXDenials(t *testing.T) {
	f := accesstest.New(t)
	gate, err := access.New(f.Config(), &approvals{})
	if err != nil {
		t.Fatal(err)
	}
	handler := gate.Protect(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("unauthenticated request reached app") }))
	for _, tc := range []struct {
		path, accept, hx string
		status           int
		redirect         string
	}{
		{"/settings", "text/html", "", 303, "/sign-in"},
		{"/api/v1/settings", "text/html", "", 401, ""},
		{"/m/plan/routes", "", "true", 401, "/sign-in"},
	} {
		r := httptest.NewRequestWithContext(t.Context(), "GET", tc.path, nil)
		r.Header.Set("Accept", tc.accept)
		r.Header.Set("HX-Request", tc.hx)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		redirect := w.Header().Get("Location")
		if tc.hx != "" {
			redirect = w.Header().Get("HX-Redirect")
		}
		if w.Code != tc.status || redirect != tc.redirect || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s: %d %v", tc.path, w.Code, w.Header())
		}
	}
}
