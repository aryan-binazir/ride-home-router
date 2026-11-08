package access_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"ride-home-router/internal/access"
	"ride-home-router/internal/access/accesstest"
	"testing"
	"testing/synctest"
	"time"
)

type verificationApprovals struct{ access.Store }

func (verificationApprovals) Approved(context.Context, []string) (bool, error)  { return true, nil }
func (verificationApprovals) RecordAdminEmails(context.Context, []string) error { return nil }

func verificationRequest(h http.Handler, token string) int {
	r := httptest.NewRequestWithContext(context.Background(), "GET", "http://127.0.0.1/private", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w.Code
}

func TestVerificationIdentityCacheExpiry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := accesstest.New(t)
		token := f.Admin()
		a, err := access.New(f.Config(), verificationApprovals{})
		if err != nil {
			t.Fatal(err)
		}
		h := a.Protect(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
		if got := verificationRequest(h, token); got != 204 {
			t.Fatal(got)
		}
		f.Session("sess_admin", "user_admin", "revoked")
		time.Sleep(29 * time.Second)
		if got := verificationRequest(h, token); got != 204 {
			t.Fatalf("before expiry: %d", got)
		}
		time.Sleep(time.Second)
		if got := verificationRequest(h, token); got != 401 {
			t.Fatalf("at expiry: %d", got)
		}
	})
}

func TestVerificationExpiredJWTNeverUsesWarmIdentity(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := accesstest.New(t)
		f.Admin()
		token := f.Token("user_admin", "sess_admin", map[string]any{"exp": time.Now().Add(2 * time.Second).Unix()})
		a, err := access.New(f.Config(), verificationApprovals{})
		if err != nil {
			t.Fatal(err)
		}
		h := a.Protect(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
		if got := verificationRequest(h, token); got != 204 {
			t.Fatal(got)
		}
		time.Sleep(2 * time.Second)
		if got := verificationRequest(h, token); got != 401 {
			t.Fatalf("expired JWT admitted: %d", got)
		}
	})
}
