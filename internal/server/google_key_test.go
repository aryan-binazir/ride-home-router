package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"regexp"
	"ride-home-router/internal/access/accesstest"
	"ride-home-router/internal/postgres/postgrestest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestGoogleMapsKeySecurityIntegration(t *testing.T) {
	databaseURL := postgrestest.DatabaseURL(t)
	audit, err := os.CreateTemp(t.TempDir(), "audit-*.log")
	if err != nil {
		t.Fatal(err)
	}
	previousLog := log.Writer()
	log.SetOutput(audit)
	t.Cleanup(func() {
		log.SetOutput(previousLog)
		if err := audit.Close(); err != nil {
			t.Error(err)
		}
	})
	const endpoint = "/api/v1/settings/google-maps-key"
	const firstKey = "SyntheticFirst-Q7m9_google-secret-0123456789"
	const secondKey = "SyntheticSecond-R8n0_google-secret-9876543210"
	const envKey = "SyntheticEnv-T9p1_must-never-be-used-123456"
	t.Setenv("GOOGLE_MAPS_API_KEY", envKey)
	f := accesstest.New(t)
	admin := f.Admin()
	f.User("second", []string{"second@example.test"}, nil)
	f.Session("second_session", "second", "active")
	second := f.Token("second", "second_session")
	f.User("member", []string{"member@example.test"}, nil)
	f.Session("member_session", "member", "active")
	member := f.Token("member", "member_session", map[string]any{"org_role": "org:admin"})
	start := func() (*Server, string) {
		t.Helper()
		s, err := New(t.Context(), Config{Addr: "127.0.0.1:0", AllowedHosts: []string{"127.0.0.1"}, DatabaseURL: databaseURL, Auth: f.Config("admin@example.test", "second@example.test")})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := s.Shutdown(context.Background()); err != nil {
				t.Error(err)
			}
		})
		addr, err := s.Start()
		if err != nil {
			t.Fatal(err)
		}
		return s, "http://" + addr
	}
	first, a := start()
	replica, b := start()
	h := accessHTTP{t, &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	conn, err := pgx.Connect(t.Context(), databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	request := func(t *testing.T, base, method, path, token, body, contentType string, headers map[string]string, want int) string {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), method, base+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		if contentType == "" {
			contentType = "application/json"
		}
		req.Header.Set("Content-Type", contentType)
		req.Header.Set("Origin", base)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if headers["Cookie"] != "" {
			// The TCP listener uses a random port; send the configured browser
			// host and origin so cookie requests exercise the real CSRF checks.
			req.Host = "127.0.0.1"
			req.Header.Set("Origin", accesstest.Origin)
		}
		for name, value := range headers {
			req.Header.Set(name, value)
		}
		res, err := h.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		status, response, responseHeaders := res.StatusCode, string(data), res.Header
		// Check headers too: errors and HTMX toasts must not echo submitted credentials.
		exposed := response + fmt.Sprint(responseHeaders)
		for _, key := range []string{firstKey, secondKey, envKey} {
			if strings.Contains(exposed, key) || strings.Contains(exposed, key[:12]) {
				t.Fatalf("%s %s disclosed a credential or its prefix", method, path)
			}
		}
		if status != want {
			t.Errorf("%s %s: status %d, want %d; body=%q", method, path, status, want, response)
		}
		return response
	}
	statusOnly := func(t *testing.T, body string, configured bool) {
		t.Helper()
		var got map[string]any
		if err := json.Unmarshal([]byte(body), &got); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, map[string]any{"configured": configured}) {
			t.Fatalf("unexpected credential response: %s", body)
		}
	}
	stored := func(t *testing.T, want string) {
		t.Helper()
		got, err := replica.db.Settings().GoogleMapsKey(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Error("server-only Postgres repository returned unexpected credential")
		}
	}
	panel := func(t *testing.T, body string, configured bool) {
		t.Helper()
		inputs := regexp.MustCompile(`<input\b[^>]*>`).FindAllString(body, -1)
		found := false
		for _, input := range inputs {
			if strings.Contains(input, `name="api_key"`) {
				found = true
				if !strings.Contains(input, `type="password"`) || regexp.MustCompile(`\bvalue\s*=\s*["'][^"']+`).MatchString(input) {
					t.Error("credential input is not an empty password field")
				}
			}
		}
		if !found {
			t.Error("missing credential password field")
		}
		if strings.Contains(body, "••••••••") != configured {
			t.Error("credential panel does not use fixed configured bullets")
		}
	}
	put := func(t *testing.T, base, token, key string) {
		t.Helper()
		statusOnly(t, request(t, base, "PUT", endpoint, token, `{"api_key":"`+key+`"}`, "", nil, 200), true)
		stored(t, key)
	}
	for _, token := range []string{admin, second} {
		statusOnly(t, request(t, a, "GET", endpoint, token, "", "", nil, 200), false)
	}
	stored(t, "")
	request(t, a, "POST", "/api/v1/access", admin, `{"email":"member@example.test"}`, "", nil, 200)
	put(t, a, admin, firstKey)
	statusOnly(t, request(t, b, "GET", endpoint, second, "", "", nil, 200), true)

	t.Run("denied identities cannot read or mutate", func(t *testing.T) {
		f.Session("revoked", "user_admin", "revoked")
		cases := []struct {
			name, token string
			status      int
		}{
			{"member", member, 403},
			{"missing", "", 401},
			{"forged", accesstest.New(t).Token("user_admin", "sess_admin"), 401},
			{"expired", f.Token("user_admin", "sess_admin", map[string]any{"exp": time.Now().Add(-time.Hour).Unix()}), 401},
			{"revoked", f.Token("user_admin", "revoked"), 401},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				before := accessDatabaseSnapshot(t, conn)
				for _, base := range []string{a, b} {
					for _, method := range []string{"GET", "PUT", "DELETE"} {
						for _, hx := range []bool{false, true} {
							headers := map[string]string{"X-Admin": "true"}
							body, contentType := `{"api_key":"`+secondKey+`"}`, "application/json"
							if hx {
								headers["HX-Request"] = "true"
								body = url.Values{"api_key": {secondKey}}.Encode()
								contentType = "application/x-www-form-urlencoded"
							}
							response := request(t, base, method, endpoint, tc.token, body, contentType, headers, tc.status)
							if strings.TrimSpace(response) != http.StatusText(tc.status) {
								t.Errorf("non-generic denial: %q", response)
							}
						}
					}
				}
				if !reflect.DeepEqual(before, accessDatabaseSnapshot(t, conn)) {
					t.Error("denied requests changed database")
				}
			})
		}
	})

	for _, token := range []string{admin, second, member} {
		body := request(t, a, "GET", "/api/v1/settings", token, "", "", nil, 200)
		for _, marker := range []string{"api_key", "google_maps", "google-maps-key", "••••"} {
			if strings.Contains(body, marker) {
				t.Errorf("general settings expose credential control %q", marker)
			}
		}
		body = request(t, b, "GET", "/settings", token, "", "", nil, 200)
		if token == member {
			for _, marker := range []string{"google-maps-key", "api_key", "••••", "/api/v1/access"} {
				if strings.Contains(body, marker) {
					t.Errorf("member settings expose management %q", marker)
				}
			}
		} else if !strings.Contains(body, endpoint) {
			t.Error("admin settings omit key management")
		}
	}
	t.Run("invalid writes preserve stored key", func(t *testing.T) {
		cases := []struct {
			name, path, body, contentType string
			hx                            bool
		}{
			{"empty", endpoint, `{"api_key":""}`, "", false},
			{"blank", endpoint, `{"api_key":"   "}`, "", false},
			{"missing", endpoint, `{}`, "", false},
			{"invalid JSON", endpoint, `{"api_key":`, "", false},
			{"trailing JSON", endpoint, `{"api_key":"` + secondKey + `"} {}`, "", false},
			{"trailing garbage", endpoint, `{"api_key":"` + secondKey + `"} invalid`, "", false},
			{"escaped key", endpoint, `{"api_key":"synthetic\\key"}`, "", false},
			{"quoted key", endpoint, `{"api_key":"synthetic\"key"}`, "", false},
			{"wrong type", endpoint, `{"api_key":123}`, "", false},
			{"unknown field", endpoint, `{"api_key":"` + secondKey + `","reveal":true}`, "", false},
			{"mask", endpoint, `{"api_key":"••••••••"}`, "", false},
			{"whitespace inside", endpoint, `{"api_key":"invalid key"}`, "", false},
			{"oversized", endpoint, `{"api_key":"` + strings.Repeat("x", 4097) + `"}`, "", false},
			{"query only", endpoint + "?api_key=" + secondKey, "", "", false},
			{"HTMX query only", endpoint + "?api_key=" + secondKey, "", "application/x-www-form-urlencoded", true},
			{"HTMX blank", endpoint, "api_key=", "application/x-www-form-urlencoded", true},
			{"HTMX mask", endpoint, url.Values{"api_key": {"••••••••"}}.Encode(), "application/x-www-form-urlencoded", true},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				headers := map[string]string{}
				if tc.hx {
					headers["HX-Request"] = "true"
				}
				request(t, a, "PUT", tc.path, admin, tc.body, tc.contentType, headers, 400)
				stored(t, firstKey)
			})
		}
	})
	put(t, b, second, secondKey)
	if err := first.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
	_, restarted := start()
	statusOnly(t, request(t, restarted, "GET", endpoint, admin, "", "", nil, 200), true)
	stored(t, secondKey)
	statusOnly(t, request(t, restarted, "DELETE", endpoint, admin, "", "", nil, 200), false)
	statusOnly(t, request(t, b, "GET", endpoint, second, "", "", nil, 200), false)
	stored(t, "")

	cookie := map[string]string{"Cookie": "__session=" + second, "HX-Request": "true"}
	panel(t, request(t, b, "PUT", endpoint, "", url.Values{"api_key": {firstKey}}.Encode(), "application/x-www-form-urlencoded", cookie, 200), true)
	stored(t, firstKey)
	panel(t, request(t, restarted, "GET", endpoint, admin, "", "", map[string]string{"HX-Request": "true"}, 200), true)
	for _, method := range []string{"PUT", "DELETE"} {
		before := accessDatabaseSnapshot(t, conn)
		request(t, b, method, endpoint, "", url.Values{"api_key": {secondKey}}.Encode(), "application/x-www-form-urlencoded", map[string]string{"Cookie": "__session=" + second, "HX-Request": "true", "Origin": ""}, 403)
		if !reflect.DeepEqual(before, accessDatabaseSnapshot(t, conn)) {
			t.Error("missing-origin mutation changed database")
		}
	}
	panel(t, request(t, b, "DELETE", endpoint, "", "", "application/x-www-form-urlencoded", cookie, 200), false)
	stored(t, "")
	statusOnly(t, request(t, restarted, "GET", endpoint, admin, "", "", nil, 200), false)

	t.Run("database failure stays generic", func(t *testing.T) {
		put(t, b, second, secondKey)
		// Only this test's isolated credential table is made unavailable; authentication still works.
		if _, err := conn.Exec(t.Context(), `ALTER TABLE google_maps_credentials RENAME TO google_maps_credentials_unavailable`); err != nil {
			t.Fatal(err)
		}
		defer func() {
			if _, err := conn.Exec(context.Background(), `ALTER TABLE google_maps_credentials_unavailable RENAME TO google_maps_credentials`); err != nil {
				t.Error(err)
			}
		}()
		for _, method := range []string{"GET", "PUT", "DELETE"} {
			for _, hx := range []bool{false, true} {
				headers := map[string]string{}
				body, contentType := `{"api_key":"`+firstKey+`"}`, "application/json"
				if hx {
					headers["HX-Request"] = "true"
					body = url.Values{"api_key": {firstKey}}.Encode()
					contentType = "application/x-www-form-urlencoded"
				}
				response := request(t, b, method, endpoint, second, body, contentType, headers, 503)
				allowed := map[string]bool{"Unable to load key status": true, "Unable to save key": true, "Unable to delete key": true, "Service Unavailable": true}
				if !allowed[strings.TrimSpace(response)] {
					t.Errorf("non-generic database error: %q", response)
				}
			}
		}
	})
	stored(t, secondKey)
	logs, err := os.ReadFile(audit.Name())
	if err != nil {
		t.Fatal(err)
	}
	for _, actor := range []string{`actor="user_admin"`, `actor="second"`} {
		for _, action := range []string{"replaced", "deleted"} {
			if !strings.Contains(string(logs), "Google Maps credential "+action+": "+actor) {
				t.Errorf("credential audit omitted %s by verified %s", action, actor)
			}
		}
	}
	for _, key := range []string{firstKey, secondKey, envKey} {
		if strings.Contains(string(logs), key) {
			t.Error("audit logs disclosed a credential")
		}
	}
}
