package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"ride-home-router/internal/access"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestLoginRequiresExactHostAndOrigin(t *testing.T) {
	p, err := newProvider("http://127.0.0.1:19876")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		host, origin string
		status       int
	}{
		{"127.0.0.1:19876", "http://127.0.0.1:19876", http.StatusOK},
		{"evil.test:19876", "http://127.0.0.1:19876", http.StatusForbidden},
		{"127.0.0.1:19876", "https://evil.test", http.StatusForbidden},
		{"127.0.0.1:19876", "", http.StatusForbidden},
	} {
		r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/__dev/session", strings.NewReader(`{"identity":"admin"}`))
		r.Host = tc.host
		r.Header.Set("Origin", tc.origin)
		r.Header.Set("X-Dev-Capability", p.capability)
		w := httptest.NewRecorder()
		p.browser(w, r)
		if w.Code != tc.status {
			t.Errorf("host=%s origin=%s: got %d, want %d", tc.host, tc.origin, w.Code, tc.status)
		}
	}
}

func TestRealGateRejectsInvalidCredentialsAndEnforcesApprovals(t *testing.T) {
	p, err := newProvider("http://127.0.0.1:19876")
	if err != nil {
		t.Fatal(err)
	}
	api := httptest.NewServer(http.HandlerFunc(p.api))
	defer api.Close()
	client := &http.Client{Transport: localTransport{endpoint: strings.TrimPrefix(api.URL, "http://"), base: &http.Transport{Proxy: nil}}}
	gate, err := access.New(p.config(client), approvalStore{})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	gate.Register(mux)
	mux.HandleFunc("/protected", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	handler := gate.Protect(mux)
	for _, identity := range []string{"admin", "member", "denied"} {
		p.sessions[identity] = identity
	}
	token := func(identity string, expiry time.Time) string {
		t.Helper()
		raw, err := p.token(identity, identity, expiry)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	valid := token("admin", time.Now().Add(time.Hour))
	originalIssuer, originalOrigin := p.hostname, p.origin
	p.hostname = "other.clerk.accounts.dev"
	wrongIssuer := token("admin", time.Now().Add(time.Hour))
	p.hostname = originalIssuer
	p.origin = "https://other.example.test"
	wrongOrigin := token("admin", time.Now().Add(time.Hour))
	p.origin = originalOrigin
	for _, tc := range []struct {
		name, raw, path, origin string
		cookie                  bool
		status                  int
	}{
		{name: "wrong issuer", raw: wrongIssuer, path: "/protected", status: 401},
		{name: "wrong authorized party", raw: wrongOrigin, path: "/protected", status: 401},
		{name: "unknown session", raw: token("missing", time.Now().Add(time.Hour)), path: "/protected", status: 401},
		{name: "malformed token", raw: "not-a-jwt", path: "/protected", status: 401},
		{name: "anonymous", path: "/protected", status: 401},
		{name: "wrong signature", raw: valid[:len(valid)-12] + "AAAAAAAAAAAA", path: "/protected", status: 401},
		{name: "expired", raw: token("admin", time.Now().Add(-time.Hour)), path: "/protected", status: 401},
		{name: "admin", raw: valid, path: "/api/v1/access", status: 200},
		{name: "member", raw: token("member", time.Now().Add(time.Hour)), path: "/protected", status: 200},
		{name: "member admin action", raw: token("member", time.Now().Add(time.Hour)), path: "/api/v1/access", status: 403},
		{name: "denied", raw: token("denied", time.Now().Add(time.Hour)), path: "/protected", status: 403},
		{name: "cross origin mutation", raw: valid, path: "/protected", origin: "https://evil.test", cookie: true, status: 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			method := http.MethodGet
			if tc.cookie {
				method = http.MethodPost
			}
			r := httptest.NewRequestWithContext(t.Context(), method, tc.path, nil)
			r.Host = "127.0.0.1:19876"
			if tc.cookie {
				r.Header.Set("Cookie", "__session="+tc.raw)
				r.Header.Set("Origin", tc.origin)
			} else if tc.raw != "" {
				r.Header.Set("Authorization", "Bearer "+tc.raw)
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("got %d, want %d: %s", w.Code, tc.status, w.Body.String())
			}
		})
	}
	production, err := newProvider("https://production.example.test")
	if err != nil {
		t.Fatal(err)
	}
	prodGate, err := access.New(production.config(nil), approvalStore{})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/protected", nil)
	r.Header.Set("Authorization", "Bearer "+valid)
	w := httptest.NewRecorder()
	prodGate.Protect(mux).ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatalf("production trust accepted local key: %d", w.Code)
	}
	for _, name := range []string{"DEV", "AUTH_DISABLED", "SKIP_AUTH", "DEV_MODE", "CLERK_API_URL"} {
		t.Setenv(name, "true")
	}
	if _, err := access.New(access.Config{}, approvalStore{}); err == nil {
		t.Fatal("dev environment disabled required production configuration")
	}
}

type approvalStore struct{}

func (approvalStore) Approved(_ context.Context, emails []string) (bool, error) {
	return slices.Contains(emails, "member@example.test"), nil
}

func (approvalStore) ApprovedEmails(context.Context) ([]string, error) {
	return []string{"member@example.test"}, nil
}
func (approvalStore) AddApprovedEmail(context.Context, string) error    { return nil }
func (approvalStore) RemoveApprovedEmail(context.Context, string) error { return nil }
func (approvalStore) RecordAdminEmails(context.Context, []string) error { return nil }

func TestProviderTransportRejectsUnexpectedDestinations(t *testing.T) {
	transport := localTransport{}
	for _, target := range []string{"https://maps.googleapis.com/maps/api/distancematrix/json", "https://api.clerk.com/v1/jwks", "https://example.com/", "http://api.clerk.com/v1/users/admin", "https://api.clerk.com.evil.test/v1/users/admin"} {
		r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
		if resp, err := transport.RoundTrip(r); err == nil {
			_ = resp.Body.Close()
			t.Fatalf("accepted %s", target)
		}
	}
}

func TestLoginAndRenewalRequireCapability(t *testing.T) {
	p, err := newProvider("http://127.0.0.1:19876")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/__dev/session", "/__dev/token"} {
		for _, capability := range []string{"", "wrong"} {
			r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, path, strings.NewReader(`{"identity":"admin"}`))
			r.Host = "127.0.0.1:19876"
			r.Header.Set("Origin", p.origin)
			r.Header.Set("X-Dev-Capability", capability)
			w := httptest.NewRecorder()
			p.browser(w, r)
			if w.Code != http.StatusForbidden {
				t.Fatalf("%s accepted missing/wrong capability: %d", path, w.Code)
			}
		}
	}
}
