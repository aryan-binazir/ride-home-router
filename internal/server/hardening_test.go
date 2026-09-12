package server

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"ride-home-router/internal/access/accesstest"
	"ride-home-router/internal/postgres/postgrestest"
	"strings"
	"testing"
	"time"
)

func TestRecoveryReportsFailureAndToast(t *testing.T) {
	for _, htmx := range []bool{false, true} {
		var logs bytes.Buffer
		previous := log.Writer()
		log.SetOutput(&logs)
		t.Cleanup(func() { log.SetOutput(previous) })
		handler := recoverMiddleware(securityHeadersMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("test failure") })))
		req := httptest.NewRequestWithContext(t.Context(), "GET", "/panic", nil)
		if htmx {
			req.Header.Set("HX-Request", "true")
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != 500 {
			t.Fatalf("status=%d", w.Code)
		}
		if htmx && (!strings.Contains(w.Header().Get("HX-Trigger"), "An error occurred. Please try again.") || w.Header().Get("HX-Reswap") != "none") {
			t.Fatalf("headers=%v", w.Header())
		}
		if !strings.Contains(logs.String(), "GET /panic 500") {
			t.Fatalf("log=%s", logs.String())
		}
	}
}

func TestHTMXBodyLimitShowsToast(t *testing.T) {
	req := httptest.NewRequestWithContext(t.Context(), "POST", "/api/v1/imports", strings.NewReader(strings.Repeat("x", 11<<20)))
	req.Header.Set("HX-Request", "true")
	w := httptest.NewRecorder()
	requestBodyMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("oversized request reached handler") })).ServeHTTP(w, req)
	if w.Code != 413 || !strings.Contains(w.Header().Get("HX-Trigger"), "That upload is too large.") {
		t.Fatalf("status=%d headers=%v", w.Code, w.Header())
	}
}

func TestDesktopPreferenceBehindHTTPSProxy(t *testing.T) {
	req := httptest.NewRequestWithContext(t.Context(), "GET", "http://router.example/m/desktop", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	w := httptest.NewRecorder()
	handleSetDesktopPreference(w, req)
	if cookies := w.Result().Cookies(); len(cookies) != 1 || !cookies[0].Secure {
		t.Fatalf("cookies=%v", cookies)
	}
}

func TestInvalidDatabaseURLDoesNotExposeSecrets(t *testing.T) {
	_, err := New(t.Context(), Config{DatabaseURL: "postgres://u@h/db?password=SECRET&sslmode=bogus"})
	if err == nil || strings.Contains(err.Error(), "SECRET") || strings.Contains(err.Error(), "postgres://") {
		t.Fatalf("unsafe error: %v", err)
	}
}

func TestSecurityHeadersOnAuthenticatedHTMLAndHealth(t *testing.T) {
	fixture := accesstest.New(t)
	token := fixture.Admin()
	server, base := startAccessServer(t, postgrestest.DatabaseURL(t), fixture)
	if server.httpServer.ReadHeaderTimeout != 10*time.Second || server.httpServer.MaxHeaderBytes != 64<<10 {
		t.Fatalf("header limits=%+v", server.httpServer)
	}
	client := accessHTTP{t: t, client: &http.Client{Timeout: 5 * time.Second}}
	for _, path := range []string{"/", "/api/v1/health"} {
		auth := token
		if path == "/api/v1/health" {
			auth = ""
		}
		status, body, headers := client.request(base, "GET", path, auth, "", "", nil)
		if status != 200 {
			t.Fatalf("%s status=%d body=%s", path, status, body)
		}
		for name, want := range map[string]string{"X-Content-Type-Options": "nosniff", "X-Frame-Options": "DENY", "Referrer-Policy": "strict-origin-when-cross-origin", "Content-Security-Policy": "frame-ancestors 'none'; base-uri 'self'; object-src 'none'", "Permissions-Policy": "camera=(), microphone=(), geolocation=()"} {
			if headers.Get(name) != want {
				t.Fatalf("%s %s=%q", path, name, headers.Get(name))
			}
		}
	}
}

func TestRecoveryAbortsPartialResponses(t *testing.T) {
	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(previous)
	handler := recoverMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("partial")); panic("failed") }))
	defer func() {
		if got := recover(); got != http.ErrAbortHandler {
			t.Fatalf("panic=%v", got)
		}
		if !strings.Contains(logs.String(), "GET /partial 500") {
			t.Fatalf("log=%s", logs.String())
		}
	}()
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(t.Context(), "GET", "/partial", nil))
}

func TestRecoveryClearsStaleResponseHeaders(t *testing.T) {
	handler := recoverMiddleware(securityHeadersMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "/wrong")
		w.Header().Set("HX-Redirect", "/wrong")
		w.Header().Set("Content-Length", "999")
		panic("before write")
	})))
	r := httptest.NewRequestWithContext(t.Context(), "GET", "/", nil)
	r.Header.Set("HX-Request", "true")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 500 || w.Header().Get("Location") != "" || w.Header().Get("HX-Redirect") != "" || w.Header().Get("Content-Length") != "" {
		t.Fatalf("stale headers %v", w.Header())
	}
}
