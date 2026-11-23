package accesstest_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"ride-home-router/internal/access"
	"ride-home-router/internal/access/accesstest"
	"testing"
	"time"
)

type closedStore struct{}

func (closedStore) Approved(context.Context, []string) (bool, error)  { return false, nil }
func (closedStore) ApprovedEmails(context.Context) ([]string, error)  { return nil, nil }
func (closedStore) AddApprovedEmail(context.Context, string) error    { return nil }
func (closedStore) RemoveApprovedEmail(context.Context, string) error { return nil }

func TestFixtureInstanceIsolation(t *testing.T) {
	first := accesstest.NewInstance(t, "first.clerk.accounts.dev")
	second := accesstest.NewInstance(t, "second.clerk.accounts.dev")
	if first.Config().SecretKey == second.Config().SecretKey || first.Config().JWTKey == second.Config().JWTKey || first.Config().PublishableKey == second.Config().PublishableKey {
		t.Fatal("instances share credentials")
	}
	token := first.Admin()
	for _, tc := range []struct {
		name   string
		config access.Config
		token  string
		want   int
	}{
		{"own", first.Config(), token, 204},
		{"foreign", second.Config(), token, 401},
		{"wrong_secret", func() access.Config { c := first.Config(); c.SecretKey = second.Config().SecretKey; return c }(), token, 503},
		{"wrong_issuer", first.Config(), first.Token("user_admin", "sess_admin", map[string]any{"iss": "https://second.clerk.accounts.dev"}), 401},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gate, err := access.New(tc.config, closedStore{})
			if err != nil {
				t.Fatal(err)
			}
			handler := gate.Protect(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
			r := httptest.NewRequestWithContext(t.Context(), "GET", accesstest.Origin+"/private", nil)
			r.Header.Set("Authorization", "Bearer "+tc.token)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("status %d want %d: %s", w.Code, tc.want, w.Body.String())
			}
		})
	}
	for _, secret := range []string{"", second.Config().SecretKey} {
		r := httptest.NewRequestWithContext(t.Context(), "GET", "https://api.clerk.com/v1/users/user_admin", nil)
		r.Header.Set("Authorization", "Bearer "+secret)
		response, err := first.RoundTrip(r)
		if response != nil {
			_ = response.Body.Close()
		}
		if err == nil {
			t.Fatal("foreign or missing API secret accepted")
		}
	}
}

func (closedStore) RecordAdminEmails(context.Context, []string) error { return nil }

func TestBrowserDenialAndClerkOutage(t *testing.T) {
	f := accesstest.New(t)
	valid := f.Admin()
	gate, err := access.New(f.Config(), closedStore{})
	if err != nil {
		t.Fatal(err)
	}
	handler := gate.Protect(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("denied request reached handler") }))
	for _, tc := range []struct {
		name, path, token string
		hx                bool
		want              int
	}{
		{"expired page post", "/participants", f.Token("user_admin", "sess_admin", map[string]any{"exp": time.Now().Add(-time.Minute).Unix()}), false, 303},
		{"expired API", "/api/v1/routes", "invalid", false, 401},
		{"Clerk outage page", "/participants", valid, false, 503},
		{"Clerk outage HTMX", "/participants", valid, true, 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f.Fail("/v1/sessions/sess_admin", 503)
			r := httptest.NewRequestWithContext(t.Context(), "POST", accesstest.Origin+tc.path, nil)
			r.Header.Set("Authorization", "Bearer "+tc.token)
			r.Header.Set("Accept", "text/html")
			if tc.hx {
				r.Header.Set("HX-Request", "true")
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("status=%d want=%d", w.Code, tc.want)
			}
			if tc.want == 503 && (w.Header().Get("HX-Redirect") != "" || w.Header().Get("Location") != "") {
				t.Fatal("outage redirected to sign-in")
			}
		})
	}
}
