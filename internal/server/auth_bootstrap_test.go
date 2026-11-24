package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/aryan-binazir/ride-home-router/internal/access/accesstest"
	"github.com/aryan-binazir/ride-home-router/internal/postgres/postgrestest"
)

func TestAuthBootstrapSharesOnlyPublicConfigAndPreservesHeaders(t *testing.T) {
	fixture := accesstest.New(t)
	_, base := startAccessServer(t, postgrestest.DatabaseURL(t), fixture)
	client := accessHTTP{t, &http.Client{Timeout: 10 * time.Second}}
	status, body, headers := client.request(base, "GET", "/auth/config", "", "", "", nil)
	if status != http.StatusOK || headers.Get("Cache-Control") != "no-store" {
		t.Fatalf("config status=%d cache=%q", status, headers.Get("Cache-Control"))
	}
	var fields map[string]string
	if err := json.Unmarshal([]byte(body), &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 2 || fields["publishableKey"] != fixture.Config().PublishableKey || !strings.HasSuffix(fields["scriptURL"], "/npm/@clerk/clerk-js@5/dist/clerk.browser.js") {
		t.Fatal("public config contract changed")
	}
	admin := fixture.Admin()
	for _, path := range []string{"/sign-in", "/participants", "/drivers"} {
		status, body, headers = client.request(base, "GET", path, admin, "", "", nil)
		if status != http.StatusOK || headers.Get("Cache-Control") != "no-store" || headers.Get("Content-Security-Policy") != "frame-ancestors 'none'; base-uri 'self'; object-src 'none'" {
			t.Fatalf("%s status=%d cache=%q csp=%q", path, status, headers.Get("Cache-Control"), headers.Get("Content-Security-Policy"))
		}
		for _, value := range []string{`data-publishable-key="` + fields["publishableKey"] + `"`, `data-script-url="` + fields["scriptURL"] + `"`, `rel="preload" href="` + fields["scriptURL"] + `" as="script" crossorigin="anonymous"`} {
			if !strings.Contains(body, value) {
				t.Fatalf("%s missing public auth bootstrap", path)
			}
		}
		if strings.Contains(body, fixture.Config().SecretKey) || strings.Contains(body, fixture.Config().JWTKey) || strings.Contains(body, "/__dev/") {
			t.Fatalf("%s exposed private or dev configuration", path)
		}
	}
}

func TestAuthBootstrapKeepsUnconfiguredStartupAndUnavailableAdmission(t *testing.T) {
	databaseURL := postgrestest.DatabaseURL(t)
	_, err := New(t.Context(), Config{DatabaseURL: databaseURL, CredentialEncryptionKey: postgrestest.EncryptionKey})
	if err == nil || err.Error() != "authentication configuration: CLERK_SECRET_KEY, CLERK_PUBLISHABLE_KEY and CLERK_JWT_KEY are required" {
		t.Fatalf("unconfigured startup: %v", err)
	}
	fixture := accesstest.New(t)
	_, base := startAccessServer(t, databaseURL, fixture)
	admin := fixture.Admin()
	fixture.Fail("/v1/sessions/sess_admin", http.StatusServiceUnavailable)
	client := accessHTTP{t, &http.Client{Timeout: 10 * time.Second}}
	status, body, headers := client.request(base, "GET", "/api/v1/settings", admin, "", "", nil)
	assertAccessDenied(t, status, body, headers, http.StatusServiceUnavailable, "GET")
	status, body, _ = client.request(base, "GET", "/sign-in", "", "", "", nil)
	if status != http.StatusOK || !strings.Contains(body, `id="auth-retry"`) || !strings.Contains(body, `id="clerk-sign-in"`) {
		t.Fatal("unavailable admission must retain sign-in and retry UI")
	}
}
