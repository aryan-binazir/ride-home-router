package httpx

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDecodeJSONRequiresJSONContentType(t *testing.T) {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/", strings.NewReader(`{"name":"Alex"}`))
	req.Header.Set(HeaderContentType, "text/plain")
	var dst struct {
		Name string `json:"name"`
	}

	err := DecodeJSON(req, &dst)

	if !errors.Is(err, ErrJSONContentTypeRequired) {
		t.Fatalf("error = %v, want ErrJSONContentTypeRequired", err)
	}
	if dst.Name != "" {
		t.Fatalf("name = %q, want body not decoded", dst.Name)
	}
}

func TestDecodeJSONAcceptsJSONContentTypeParameters(t *testing.T) {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/", strings.NewReader(`{"name":"Alex"}`))
	req.Header.Set(HeaderContentType, "application/json; charset=utf-8")
	var dst struct {
		Name string `json:"name"`
	}

	if err := DecodeJSON(req, &dst); err != nil {
		t.Fatalf("DecodeJSON() error = %v", err)
	}
	if dst.Name != "Alex" {
		t.Fatalf("name = %q, want Alex", dst.Name)
	}
}

func TestIsLoopbackHost(t *testing.T) {
	for _, host := range []string{"localhost:8080", "127.0.0.1:8080", "[::1]:8080", "::1"} {
		if !IsLoopbackHost(host) {
			t.Errorf("IsLoopbackHost(%q) = false, want true", host)
		}
	}
	for _, host := range []string{"evil.example:8080", "127.0.0.2:8080", "192.0.2.1:8080", "[2001:db8::1]:8080"} {
		if IsLoopbackHost(host) {
			t.Errorf("IsLoopbackHost(%q) = true, want false", host)
		}
	}
}

func TestHasSameOrigin(t *testing.T) {
	for _, tt := range []struct {
		name   string
		host   string
		origin string
		want   bool
	}{
		{name: "missing origin", host: "localhost:8080", want: true},
		{name: "matching IPv6 origin", host: "[::1]:8080", origin: "http://[::1]:8080", want: true},
		{name: "https origin behind TLS-terminating tunnel", host: "routes.example.com", origin: "https://routes.example.com", want: true},
		{name: "other scheme", host: "localhost:8080", origin: "wails://wails.localhost", want: false},
		{name: "https origin for a different host", host: "routes.example.com", origin: "https://evil.example.com", want: false},
		{name: "different loopback host", host: "localhost:8080", origin: "http://127.0.0.1:8080", want: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "http://"+tt.host+"/", nil)
			req.Host = tt.host
			req.Header.Set("Origin", tt.origin)
			if got := HasSameOrigin(req); got != tt.want {
				t.Fatalf("HasSameOrigin() = %v, want %v", got, tt.want)
			}
		})
	}
}
