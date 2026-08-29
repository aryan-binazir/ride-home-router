package server

import (
	"bytes"
	"context"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"ride-home-router/internal/handlers"
	"ride-home-router/internal/importer"
	"ride-home-router/internal/models"
	"ride-home-router/internal/postgres/postgrestest"
	appTemplates "ride-home-router/internal/templates"
	"ride-home-router/web"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestNewDoesNotApplyDatabaseMigrations(t *testing.T) {
	databaseURL := postgrestest.UnmigratedDatabase(t)
	server, err := New(context.Background(), Config{Addr: "127.0.0.1:0", DatabaseURL: databaseURL})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := server.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}

	connection, err := pgx.Connect(t.Context(), databaseURL)
	if err != nil {
		t.Fatalf("connect unmigrated schema: %v", err)
	}
	defer func() {
		if err := connection.Close(context.Background()); err != nil {
			t.Errorf("close unmigrated schema connection: %v", err)
		}
	}()
	var migrationTable, applicationTable bool
	if err := connection.QueryRow(t.Context(), `
		SELECT
			to_regclass(current_schema() || '.schema_migrations') IS NOT NULL,
			to_regclass(current_schema() || '.participants') IS NOT NULL
	`).Scan(&migrationTable, &applicationTable); err != nil {
		t.Fatalf("inspect schema after New: %v", err)
	}
	if migrationTable || applicationTable {
		t.Fatalf("New() created schema = migrations:%t participants:%t, want neither", migrationTable, applicationTable)
	}
}

func TestNewWiresAndShutdownClosesImportSessionStore(t *testing.T) {
	server, err := New(context.Background(), Config{Addr: "127.0.0.1:0", DatabaseURL: postgrestest.DatabaseURL(t)})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if server.handler == nil || server.handler.ImportSession == nil {
		t.Fatal("New() did not wire the import session store")
	}
	if err := server.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	grid, err := importer.Parse(strings.NewReader("name,address\nRider,1 Main St\n"), importer.FormatCSV, "")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if _, err := server.handler.ImportSession.Create(importer.KindParticipant, "closed.csv", grid); !errors.Is(err, importer.ErrStoreClosed) {
		t.Fatalf("Create() after Shutdown error = %v, want ErrStoreClosed", err)
	}
}

func TestHandleMethods_RejectsUnsupportedMethod(t *testing.T) {
	handler := handleMethods(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}, nil, nil, nil)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/participants", nil)
	rec := httptest.NewRecorder()

	handler(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
	if rec.Body.String() != serverMessageMethodNotAllowed+"\n" {
		t.Fatalf("body = %q, want %q", rec.Body.String(), serverMessageMethodNotAllowed+"\n")
	}
}

func TestSetupRoutesHasNoServerSideURLOpener(t *testing.T) {
	mux := setupRoutes(&handlers.Handler{}, web.Static)
	request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/open-url", strings.NewReader(`{"url":"https://maps.google.com"}`))
	request.Host = "localhost:8080"
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d: the browser client opens URLs itself", recorder.Code, http.StatusNotFound)
	}
}

func TestSetupRoutesRegistersImportEndpoints(t *testing.T) {
	mux := setupRoutes(&handlers.Handler{}, web.Static)
	request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/imports", nil)
	request.Host = "localhost:8080"
	recorder := httptest.NewRecorder()

	mux.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want import handler response %d body=%q", recorder.Code, http.StatusForbidden, recorder.Body.String())
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
}

func TestSetupRoutesDispatchesParticipantRestoreAndDeletedCollectionActions(t *testing.T) {
	store := postgrestest.Open(t)
	renderer, err := appTemplates.New(web.Templates)
	if err != nil {
		t.Fatalf("load templates: %v", err)
	}
	handler := &handlers.Handler{DB: store, Renderer: renderer}
	participant, err := store.Participants().Create(context.Background(), &models.Participant{
		Name: "Route Dispatch Rider", Address: "1 Route Road", Lat: 40.1, Lng: -73.9,
	})
	if err != nil {
		t.Fatalf("create participant: %v", err)
	}
	if err := store.Participants().Delete(context.Background(), participant.ID); err != nil {
		t.Fatalf("delete participant: %v", err)
	}

	mux := setupRoutes(handler, web.Static)
	form := url.Values{"id": {strconv.FormatInt(participant.ID, 10)}}
	restoreReq := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/participants/restore", strings.NewReader(form.Encode()))
	restoreReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	restoreReq.Header.Set("HX-Request", "true")
	restoreRR := httptest.NewRecorder()
	mux.ServeHTTP(restoreRR, restoreReq)
	if restoreRR.Code != http.StatusOK {
		t.Fatalf("restore status = %d, want %d body=%q", restoreRR.Code, http.StatusOK, restoreRR.Body.String())
	}
	if !strings.Contains(restoreRR.Header().Get("HX-Trigger"), `"rosterRestored":true`) {
		t.Fatalf("restore HX-Trigger = %q, want rosterRestored", restoreRR.Header().Get("HX-Trigger"))
	}

	if err := store.Participants().Delete(context.Background(), participant.ID); err != nil {
		t.Fatalf("delete participant again: %v", err)
	}
	deletedReq := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/participants/deleted", nil)
	deletedReq.Header.Set("HX-Request", "true")
	deletedRR := httptest.NewRecorder()
	mux.ServeHTTP(deletedRR, deletedReq)
	if deletedRR.Code != http.StatusOK {
		t.Fatalf("deleted list status = %d, want %d body=%q", deletedRR.Code, http.StatusOK, deletedRR.Body.String())
	}
	for _, want := range []string{"Route Dispatch Rider", "/api/v1/participants/restore"} {
		if !strings.Contains(deletedRR.Body.String(), want) {
			t.Fatalf("deleted list missing %q, body=%q", want, deletedRR.Body.String())
		}
	}
}

func TestSetupRoutesRedirectsMobileClientHint(t *testing.T) {
	mux := setupRoutes(&handlers.Handler{}, web.Static)
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	request.Header.Set("Sec-CH-UA-Mobile", "?1")
	response := httptest.NewRecorder()

	mux.ServeHTTP(response, request)

	if response.Code != http.StatusTemporaryRedirect || response.Header().Get("Location") != "/m" {
		t.Fatalf("redirect = %d %q, want 307 /m", response.Code, response.Header().Get("Location"))
	}
}

func TestSetupRoutesRedirectsMobileQueryFlag(t *testing.T) {
	mux := setupRoutes(&handlers.Handler{}, web.Static)
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/?m=1", nil)
	response := httptest.NewRecorder()

	mux.ServeHTTP(response, request)

	if response.Code != http.StatusTemporaryRedirect || response.Header().Get("Location") != "/m" {
		t.Fatalf("redirect = %d %q, want 307 /m", response.Code, response.Header().Get("Location"))
	}
}

func TestSetupRoutesRedirectsMobileUserAgents(t *testing.T) {
	mux := setupRoutes(&handlers.Handler{}, web.Static)

	for _, test := range []struct {
		name      string
		userAgent string
	}{
		{name: "Mobile", userAgent: "Mozilla/5.0 Mobile/15E148"},
		{name: "Android", userAgent: "Mozilla/5.0 (Linux; Android 15; Pixel 9)"},
		{name: "iPhone", userAgent: "Mozilla/5.0 (iPhone; CPU iPhone OS 18_6 like Mac OS X)"},
		{name: "iPad", userAgent: "Mozilla/5.0 (iPad; CPU OS 18_6 like Mac OS X)"},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
			request.Header.Set("User-Agent", test.userAgent)
			response := httptest.NewRecorder()

			mux.ServeHTTP(response, request)

			if response.Code != http.StatusTemporaryRedirect || response.Header().Get("Location") != "/m" {
				t.Fatalf("redirect = %d %q, want 307 /m", response.Code, response.Header().Get("Location"))
			}
		})
	}
}

func TestShouldRedirectToMobileHonorsDesktopPreference(t *testing.T) {
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/?m=1", nil)
	request.Header.Set("Sec-CH-UA-Mobile", "?1")
	request.Header.Set("User-Agent", "Mozilla/5.0 (iPhone)")
	request.AddCookie(&http.Cookie{
		Name:     "prefer_desktop",
		Value:    "1",
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})

	if shouldRedirectToMobile(request) {
		t.Fatal("shouldRedirectToMobile() = true, want prefer_desktop to suppress redirect")
	}
}

func TestSetupRoutesSetsAndClearsDesktopPreference(t *testing.T) {
	mux := setupRoutes(&handlers.Handler{}, web.Static)

	setRequest := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/m/desktop", nil)
	setResponse := httptest.NewRecorder()
	mux.ServeHTTP(setResponse, setRequest)
	if setResponse.Code != http.StatusSeeOther || setResponse.Header().Get("Location") != "/" {
		t.Fatalf("set redirect = %d %q, want 303 /", setResponse.Code, setResponse.Header().Get("Location"))
	}
	setCookies := setResponse.Result().Cookies()
	if len(setCookies) != 1 || setCookies[0].Name != "prefer_desktop" || setCookies[0].Value != "1" || setCookies[0].MaxAge <= 0 {
		t.Fatalf("set cookies = %#v, want persistent prefer_desktop=1", setCookies)
	}

	clearRequest := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/m/desktop-preference?clear=1", nil)
	clearRequest.AddCookie(setCookies[0])
	clearResponse := httptest.NewRecorder()
	mux.ServeHTTP(clearResponse, clearRequest)
	if clearResponse.Code != http.StatusSeeOther || clearResponse.Header().Get("Location") != "/m" {
		t.Fatalf("clear redirect = %d %q, want 303 /m", clearResponse.Code, clearResponse.Header().Get("Location"))
	}
	clearCookies := clearResponse.Result().Cookies()
	if len(clearCookies) != 1 || clearCookies[0].Name != "prefer_desktop" || clearCookies[0].MaxAge >= 0 {
		t.Fatalf("clear cookies = %#v, want expired prefer_desktop", clearCookies)
	}
}

func TestSetupRoutesRejectsDesktopPreferenceWithoutClearFlag(t *testing.T) {
	mux := setupRoutes(&handlers.Handler{}, web.Static)
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/m/desktop-preference", nil)
	response := httptest.NewRecorder()

	mux.ServeHTTP(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", response.Code)
	}
}

func TestHandleResourcePath_UsesEditHandlerAndRejectsCollectionPath(t *testing.T) {
	var editCalled bool

	handler := handleResourcePath(
		"/api/v1/participants/",
		"/edit",
		func(w http.ResponseWriter, _ *http.Request) {
			editCalled = true
			w.WriteHeader(http.StatusNoContent)
		},
		nil,
		nil,
		nil,
	)

	editReq := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/participants/42/edit", nil)
	editRec := httptest.NewRecorder()
	handler(editRec, editReq)

	if !editCalled {
		t.Fatal("expected edit handler to be called")
	}
	if editRec.Code != http.StatusNoContent {
		t.Fatalf("edit status = %d, want %d", editRec.Code, http.StatusNoContent)
	}

	emptyReq := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/participants/", nil)
	emptyRec := httptest.NewRecorder()
	handler(emptyRec, emptyReq)

	if emptyRec.Code != http.StatusNotFound {
		t.Fatalf("empty path status = %d, want %d", emptyRec.Code, http.StatusNotFound)
	}
	if emptyRec.Body.String() != serverMessageNotFound+"\n" {
		t.Fatalf("empty path body = %q, want %q", emptyRec.Body.String(), serverMessageNotFound+"\n")
	}
}

func TestRequestSecurityMiddlewareHostAllowlist(t *testing.T) {
	allowlist, err := newRequestAllowlist("127.0.0.1:8080", nil)
	if err != nil {
		t.Fatal(err)
	}

	for _, host := range []string{
		"localhost:8080", "127.0.0.1:8080", "[::1]:8080",
	} {
		t.Run("allows_"+host, func(t *testing.T) {
			called := false
			handler := requestSecurityMiddleware(allowlist, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				called = true
				w.WriteHeader(http.StatusNoContent)
			}))
			req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://localhost/", nil)
			req.Host = host
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusNoContent || !called {
				t.Fatalf("status = %d, called = %v; want 204 and handler called", rec.Code, called)
			}
		})
	}

	for _, host := range []string{
		"evil.com:8080", "192.168.1.20:8080",
		"localhost", "127.0.0.1", "[::1]",
	} {
		t.Run("rejects_"+host, func(t *testing.T) {
			called := false
			handler := requestSecurityMiddleware(allowlist, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				called = true
			}))
			req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://localhost/", nil)
			req.Host = host
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusForbidden || called {
				t.Fatalf("status = %d, called = %v; want 403 and handler not called", rec.Code, called)
			}
		})
	}
}

func TestRequestSecurityMiddlewareWriteRejection(t *testing.T) {
	allowlist, err := newRequestAllowlist("127.0.0.1:8080", nil)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name        string
		host        string
		contentType string
		origin      string
		htmx        bool
		wantStatus  int
		wantCalled  bool
	}{
		{
			name:        "cross-site text post",
			contentType: "text/plain",
			origin:      "https://attacker.example",
			wantStatus:  http.StatusForbidden,
		},
		{
			name:        "htmx post",
			contentType: "application/x-www-form-urlencoded",
			htmx:        true,
			wantStatus:  http.StatusNoContent,
			wantCalled:  true,
		},
		{
			name:        "same-origin loopback IP post",
			host:        "127.0.0.1:8080",
			contentType: "application/json",
			origin:      "http://127.0.0.1:8080",
			wantStatus:  http.StatusNoContent,
			wantCalled:  true,
		},
		{
			name:        "different loopback name post",
			host:        "127.0.0.1:8080",
			contentType: "application/json",
			origin:      "http://localhost:8080",
			wantStatus:  http.StatusForbidden,
		},
		{
			name:        "different loopback socket post",
			host:        "127.0.0.1:8080",
			contentType: "application/json",
			origin:      "http://[::1]:8080",
			wantStatus:  http.StatusForbidden,
		},
		{
			name:        "loopback port 80 origin",
			contentType: "application/x-www-form-urlencoded",
			origin:      "http://localhost",
			wantStatus:  http.StatusForbidden,
		},
		{
			name:        "wails origin",
			contentType: "application/json",
			origin:      "wails://wails.localhost",
			wantStatus:  http.StatusForbidden,
		},
		{
			name:        "no content type no htmx",
			contentType: "",
			wantStatus:  http.StatusForbidden,
		},
		{
			name:        "multipart form post",
			host:        "127.0.0.1:8080",
			contentType: "multipart/form-data; boundary=xyz",
			origin:      "http://127.0.0.1:8080",
			wantStatus:  http.StatusNoContent,
			wantCalled:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			handler := requestSecurityMiddleware(allowlist, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				called = true
				w.WriteHeader(http.StatusNoContent)
			}))
			req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "http://localhost:8080/write", strings.NewReader("{}"))
			if tt.host != "" {
				req.Host = tt.host
			}
			req.Header.Set("Content-Type", tt.contentType)
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}
			if tt.htmx {
				req.Header.Set("HX-Request", "true")
			}
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus || called != tt.wantCalled {
				t.Fatalf("status = %d, called = %v; want %d and called = %v", rec.Code, called, tt.wantStatus, tt.wantCalled)
			}
		})
	}
}

func TestNewRequestAllowlistBindHostVariants(t *testing.T) {
	t.Run("port 80 allows bare host forms", func(t *testing.T) {
		allowlist, err := newRequestAllowlist("127.0.0.1:80", nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, host := range []string{"localhost", "127.0.0.1", "[::1]", "localhost:80"} {
			if !allowlist.allowsHost(host) {
				t.Errorf("allowsHost(%q) = false, want true", host)
			}
		}
	})

	t.Run("non-standard loopback bind host is allowed", func(t *testing.T) {
		allowlist, err := newRequestAllowlist("127.0.0.2:8080", nil)
		if err != nil {
			t.Fatal(err)
		}
		if !allowlist.allowsHost("127.0.0.2:8080") {
			t.Error("bound host 127.0.0.2:8080 must be allowed")
		}
		if allowlist.allowsHost("127.0.0.3:8080") {
			t.Error("unbound loopback host must be rejected")
		}
	})

	t.Run("configured hosts are allowed bare and on the bound port", func(t *testing.T) {
		allowlist, err := newRequestAllowlist("127.0.0.1:8080", []string{"routes.example.com"})
		if err != nil {
			t.Fatal(err)
		}
		for _, host := range []string{"routes.example.com", "ROUTES.example.com:8080", "localhost:8080"} {
			if !allowlist.allowsHost(host) {
				t.Errorf("allowsHost(%q) = false, want true", host)
			}
		}
		for _, host := range []string{"evil.example", "routes.example.com:9090", "evil.example:8080:9"} {
			if allowlist.allowsHost(host) {
				t.Errorf("allowsHost(%q) = true, want false", host)
			}
		}
	})

	t.Run("non-loopback bind requires configured hosts", func(t *testing.T) {
		_, err := newRequestAllowlist("0.0.0.0:8080", nil)
		if err == nil || !strings.Contains(err.Error(), "allowed-hosts") {
			t.Fatalf("newRequestAllowlist() error = %v, want allowed-hosts guidance", err)
		}
	})

	t.Run("non-loopback bind allows only configured hosts", func(t *testing.T) {
		allowlist, err := newRequestAllowlist("[::]:8080", []string{"routes.example.com"})
		if err != nil {
			t.Fatal(err)
		}
		if !allowlist.allowsHost("routes.example.com") {
			t.Error("configured host must be allowed")
		}
		for _, host := range []string{"192.0.2.10:8080", "attacker.example:8080", "[::]:8080"} {
			if allowlist.allowsHost(host) {
				t.Errorf("allowsHost(%q) = true, want false", host)
			}
		}
	})
}

func TestRequestSecurityMiddlewareTunnelledWrite(t *testing.T) {
	allowlist, err := newRequestAllowlist("127.0.0.1:8080", []string{"routes.example.com"})
	if err != nil {
		t.Fatal(err)
	}

	for _, tt := range []struct {
		name       string
		origin     string
		wantStatus int
	}{
		{name: "https origin via tunnel", origin: "https://routes.example.com", wantStatus: http.StatusNoContent},
		{name: "foreign origin", origin: "https://attacker.example", wantStatus: http.StatusForbidden},
	} {
		t.Run(tt.name, func(t *testing.T) {
			handler := requestSecurityMiddleware(allowlist, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			}))
			req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "http://routes.example.com/write", strings.NewReader("{}"))
			req.Host = "routes.example.com"
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Origin", tt.origin)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
		})
	}
}

func TestRequestSecurityMiddlewareRejectsOversizedBody(t *testing.T) {
	allowlist, err := newRequestAllowlist("127.0.0.1:8080", nil)
	if err != nil {
		t.Fatal(err)
	}
	called := false
	handler := requestSecurityMiddleware(allowlist, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	req := httptest.NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		"http://localhost:8080/write",
		strings.NewReader(strings.Repeat("x", int(maxRequestBodyBytes)+1)),
	)
	req.Header.Set("HX-Request", "true")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge || called {
		t.Fatalf("status = %d, called = %v; want 413 and handler not called", rec.Code, called)
	}
}

func TestRequestSecurityMiddlewareUsesImportUploadBudgetOnRealRoutes(t *testing.T) {
	server, err := New(context.Background(), Config{Addr: "127.0.0.1:0", DatabaseURL: postgrestest.DatabaseURL(t)})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	t.Cleanup(func() {
		server.handler.ImportSession.Close()
		if err := server.db.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})
	allowlist, err := newRequestAllowlist("[::1]:8080", nil)
	if err != nil {
		t.Fatalf("newRequestAllowlist() error = %v", err)
	}
	handler := loggingMiddleware(requestSecurityMiddleware(allowlist, server.httpServer.Handler))

	t.Run("import over default limit succeeds from IPv6 loopback", func(t *testing.T) {
		req := newMiddlewareImportRequest(t, int(maxRequestBodyBytes)+1)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusCreated {
			t.Fatalf("status = %d, want %d body=%q", rec.Code, http.StatusCreated, rec.Body.String())
		}
	})

	t.Run("import over upload limit is rejected", func(t *testing.T) {
		req := newMiddlewareImportRequest(t, int(handlers.MaxImportUploadBytes)+1)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("status = %d, want %d body=%q", rec.Code, http.StatusRequestEntityTooLarge, rec.Body.String())
		}
	})

	t.Run("non-import over default limit is rejected", func(t *testing.T) {
		req := httptest.NewRequestWithContext(
			context.Background(),
			http.MethodPut,
			"http://[::1]:8080/api/v1/settings",
			strings.NewReader(strings.Repeat("x", int(maxRequestBodyBytes)+1)),
		)
		req.Host = "[::1]:8080"
		req.Header.Set("Origin", "http://[::1]:8080")
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("status = %d, want %d body=%q", rec.Code, http.StatusRequestEntityTooLarge, rec.Body.String())
		}
	})
}

func newMiddlewareImportRequest(t *testing.T, paddingBytes int) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("kind", string(importer.KindParticipant)); err != nil {
		t.Fatalf("write kind: %v", err)
	}
	if err := writer.WriteField("padding", strings.Repeat("x", paddingBytes)); err != nil {
		t.Fatalf("write padding: %v", err)
	}
	file, err := writer.CreateFormFile("file", "participants.csv")
	if err != nil {
		t.Fatalf("create file field: %v", err)
	}
	if _, err := file.Write([]byte("name,address,lat,lng\nAlex,1 Main St,40,-73\n")); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "http://[::1]:8080/api/v1/imports", &body)
	req.Host = "[::1]:8080"
	req.Header.Set("Origin", "http://[::1]:8080")
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("HX-Request", "true")
	return req
}
