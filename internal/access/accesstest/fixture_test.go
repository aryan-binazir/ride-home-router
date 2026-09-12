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

func TestFixtureUsesRealClerkVerification(t *testing.T) {
	f := accesstest.New(t)
	valid := f.Admin()
	gate, err := access.New(f.Config(), closedStore{})
	if err != nil {
		t.Fatal(err)
	}
	handler := gate.Protect(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !access.IsAdmin(r.Context()) {
			t.Error("admin identity missing")
		}
		w.WriteHeader(204)
	}))
	check := func(token string, want int) {
		t.Helper()
		r := httptest.NewRequestWithContext(t.Context(), "GET", "http://127.0.0.1/private", nil)
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("status %d want %d: %s", w.Code, want, w.Body.String())
		}
	}
	check(valid, 204)
	check(accesstest.New(t).Token("user_admin", "sess_admin"), 401)
	for _, claims := range []map[string]any{{"exp": time.Now().Add(-time.Hour).Unix()}, {"nbf": time.Now().Add(time.Hour).Unix()}, {"iss": "https://wrong.example"}, {"azp": "https://wrong.example"}, {"sid": nil}} {
		check(f.Token("user_admin", "sess_admin", claims), 401)
	}
	f.Fail("/v1/users/user_admin", 503)
	check(valid, 401)
	f.Fail("/v1/users/user_admin", 0)
	f.Fail("/v1/sessions/sess_admin", 503)
	check(valid, 401)
	f.Fail("/v1/sessions/sess_admin", 0)
	f.User("user_admin", nil, []string{"admin@example.test"})
	check(valid, 403)
	f.User("user_admin", []string{"admin@example.test"}, nil)
	check(valid, 204)
	f.Session("sess_admin", "user_admin", "revoked")
	check(valid, 401)
}
