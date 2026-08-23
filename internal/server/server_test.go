package server

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"ride-home-router/internal/handlers"
	"ride-home-router/internal/importer"
	"ride-home-router/web"
	"strings"
	"testing"
)

func TestNewWiresAndShutdownClosesImportSessionStore(t *testing.T) {
	server, err := New(Config{Addr: "127.0.0.1:0", DBPath: filepath.Join(t.TempDir(), "server.db")})
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
	allowlist, err := newRequestAllowlist("127.0.0.1:8080")
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

	// Port-less forms imply port 80, which is not where this server listens.
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
	allowlist, err := newRequestAllowlist("127.0.0.1:8080")
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
		allowlist, err := newRequestAllowlist("127.0.0.1:80")
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
		allowlist, err := newRequestAllowlist("127.0.0.2:8080")
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

	t.Run("wildcard bind allows only interface IPs", func(t *testing.T) {
		allowlist, err := newRequestAllowlistWithInterfaceAddrs("[::]:8080", func() ([]net.Addr, error) {
			return []net.Addr{
				&net.IPNet{IP: net.ParseIP("192.0.2.10"), Mask: net.CIDRMask(24, 32)},
				&net.IPNet{IP: net.ParseIP("2001:db8::10"), Mask: net.CIDRMask(64, 128)},
			}, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if !allowlist.allowsHost("192.0.2.10:8080") {
			t.Error("LAN host on the bound port must be allowed for a wildcard bind")
		}
		if !allowlist.allowsHost("[2001:db8::10]:8080") {
			t.Error("IPv6 interface host on the bound port must be allowed for a wildcard bind")
		}
		if allowlist.allowsHost("192.0.2.20:8080") {
			t.Error("non-interface IP must be rejected for a wildcard bind")
		}
		if allowlist.allowsHost("attacker.example:8080") {
			t.Error("hostname must be rejected for a wildcard bind")
		}
		if allowlist.allowsHost("192.0.2.10:9090") {
			t.Error("wrong port must be rejected even for a wildcard bind")
		}
	})

	t.Run("wildcard port 80 allows bare interface IPs", func(t *testing.T) {
		allowlist, err := newRequestAllowlistWithInterfaceAddrs("0.0.0.0:80", func() ([]net.Addr, error) {
			return []net.Addr{
				&net.IPNet{IP: net.ParseIP("192.0.2.10"), Mask: net.CIDRMask(24, 32)},
			}, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if !allowlist.allowsHost("192.0.2.10") {
			t.Error("bare interface IP must be allowed for a wildcard bind on port 80")
		}
	})

	t.Run("wildcard bind fails when interface enumeration fails", func(t *testing.T) {
		_, err := newRequestAllowlistWithInterfaceAddrs("0.0.0.0:8080", func() ([]net.Addr, error) {
			return nil, errors.New("interface enumeration failed")
		})
		if err == nil {
			t.Fatal("newRequestAllowlistWithInterfaceAddrs() error = nil, want interface enumeration error")
		}
		if !strings.Contains(err.Error(), "failed to enumerate interface addresses") {
			t.Fatalf("error = %q, want interface enumeration context", err)
		}
	})

	t.Run("wildcard bind fails when interface enumeration returns no IPs", func(t *testing.T) {
		_, err := newRequestAllowlistWithInterfaceAddrs("0.0.0.0:8080", func() ([]net.Addr, error) {
			return nil, nil
		})
		if err == nil {
			t.Fatal("newRequestAllowlistWithInterfaceAddrs() error = nil, want no usable IP addresses error")
		}
		if !strings.Contains(err.Error(), "no usable IP addresses") {
			t.Fatalf("error = %q, want no usable IP addresses context", err)
		}
	})
}

func TestRequestSecurityMiddlewareAnyHostSameOrigin(t *testing.T) {
	allowlist, err := newRequestAllowlistWithInterfaceAddrs("0.0.0.0:8080", func() ([]net.Addr, error) {
		return []net.Addr{
			&net.IPNet{IP: net.ParseIP("192.0.2.10"), Mask: net.CIDRMask(24, 32)},
		}, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, tt := range []struct {
		name       string
		origin     string
		wantStatus int
	}{
		{name: "same origin", origin: "http://192.0.2.10:8080", wantStatus: http.StatusNoContent},
		{name: "same port attacker", origin: "http://attacker.example:8080", wantStatus: http.StatusForbidden},
	} {
		t.Run(tt.name, func(t *testing.T) {
			handler := requestSecurityMiddleware(allowlist, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			}))
			req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "http://192.0.2.10:8080/write", strings.NewReader("{}"))
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

func TestRequestAllowlistRejectsMalformedHostInEveryMode(t *testing.T) {
	interfaceAddrs := func() ([]net.Addr, error) {
		return []net.Addr{
			&net.IPNet{IP: net.ParseIP("192.0.2.10"), Mask: net.CIDRMask(24, 32)},
		}, nil
	}
	for _, tt := range []struct {
		name           string
		addr           string
		interfaceAddrs func() ([]net.Addr, error)
	}{
		{name: "loopback", addr: "127.0.0.1:8080", interfaceAddrs: interfaceAddrs},
		{name: "any host", addr: "0.0.0.0:8080", interfaceAddrs: interfaceAddrs},
	} {
		t.Run(tt.name, func(t *testing.T) {
			allowlist, err := newRequestAllowlistWithInterfaceAddrs(tt.addr, tt.interfaceAddrs)
			if err != nil {
				t.Fatal(err)
			}
			if allowlist.allowsHost("evil.example:8080:9") {
				t.Error("malformed Host must be rejected")
			}
		})
	}
}

func TestCORSMiddlewareReflectsOnlySameOrigin(t *testing.T) {
	for _, tt := range []struct {
		name                 string
		origin               string
		wantAllowOrigin      string
		wantAllowCredentials string
	}{
		{
			name:                 "same origin",
			origin:               "http://127.0.0.1:8080",
			wantAllowOrigin:      "http://127.0.0.1:8080",
			wantAllowCredentials: "true",
		},
		{
			name:   "foreign origin",
			origin: "http://attacker.example:8080",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			handler := corsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			}))
			req := httptest.NewRequestWithContext(context.Background(), http.MethodOptions, "http://127.0.0.1:8080/write", nil)
			req.Header.Set("Origin", tt.origin)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
			}
			if got := rec.Header().Get("Access-Control-Allow-Origin"); got != tt.wantAllowOrigin {
				t.Errorf("Access-Control-Allow-Origin = %q, want %q", got, tt.wantAllowOrigin)
			}
			if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != tt.wantAllowCredentials {
				t.Errorf("Access-Control-Allow-Credentials = %q, want %q", got, tt.wantAllowCredentials)
			}
		})
	}
}

func TestRequestSecurityMiddlewareRejectsOversizedBody(t *testing.T) {
	allowlist, err := newRequestAllowlist("127.0.0.1:8080")
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
