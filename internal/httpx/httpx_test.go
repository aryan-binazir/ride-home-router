package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/iotest"
)

func TestDecodeJSONAcceptsJSONContentTypeParameters(t *testing.T) {
	for _, contentType := range []string{MediaTypeJSON, "APPLICATION/JSON", "application/json; charset=utf-8", `application/json; charset="utf-8"`, "application/json;", "application/json; charset=utf-16"} {
		t.Run(contentType, func(t *testing.T) {
			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", strings.NewReader(`{"name":"Alex"}`))
			req.Header.Set(HeaderContentType, contentType)
			var dst struct {
				Name string `json:"name"`
			}
			if err := DecodeJSON(req, &dst); err != nil {
				t.Fatalf("DecodeJSON() error = %v", err)
			}
			if dst.Name != "Alex" {
				t.Fatalf("name = %q, want Alex", dst.Name)
			}
		})
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

func TestDecodeJSONRejectsTrailingInput(t *testing.T) {
	for _, suffix := range []string{` {}`, ` trailing-invalid-json`, ` null`, ` true`, ` 42`, ` []`, ` "second"`, ` {`, ` /* comment */`, ` 1e309`, `}`, `]`, "\v", "\u00a0"} {
		t.Run(suffix, func(t *testing.T) {
			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", strings.NewReader(`{"name":"Alex"}`+suffix))
			req.Header.Set(HeaderContentType, MediaTypeJSON)
			var dst struct {
				Name string `json:"name"`
			}
			if err := DecodeJSON(req, &dst); err == nil {
				t.Fatal("DecodeJSON accepted trailing input")
			}
		})
	}
}

func TestDecodeJSONSingleValue(t *testing.T) {
	for _, body := range []string{`{"name":"Alex"}`, " \n" + `{"name":"Alex","unknown":true}` + " \t\r\n"} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", strings.NewReader(body))
		req.Header.Set(HeaderContentType, MediaTypeJSON)
		var dst struct {
			Name string `json:"name"`
		}
		if err := DecodeJSON(req, &dst); err != nil {
			t.Fatalf("DecodeJSON(%q): %v", body, err)
		}
		if dst.Name != "Alex" {
			t.Fatalf("name = %q, want Alex", dst.Name)
		}
	}
}

func TestDecodeJSONRejectsEmptyOrInvalidBody(t *testing.T) {
	for _, body := range []string{"", " \t\r\n", `{`, `invalid`, `{"name":42}`} {
		t.Run(body, func(t *testing.T) {
			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", strings.NewReader(body))
			req.Header.Set(HeaderContentType, MediaTypeJSON)
			var dst struct {
				Name string `json:"name"`
			}
			if err := DecodeJSON(req, &dst); err == nil {
				t.Fatal("DecodeJSON accepted empty or invalid body")
			}
		})
	}
}

func TestDecodeJSONRequiresJSONContentType(t *testing.T) {
	for _, contentType := range []string{"", "text/plain", MediaTypeForm, "application/problem+json", "application/json; charset"} {
		t.Run(contentType, func(t *testing.T) {
			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", strings.NewReader(`{"name":"Alex"}`))
			req.Header.Set(HeaderContentType, contentType)
			var dst struct {
				Name string `json:"name"`
			}
			if err := DecodeJSON(req, &dst); !errors.Is(err, ErrJSONContentTypeRequired) {
				t.Fatalf("error = %v, want ErrJSONContentTypeRequired", err)
			}
			if dst.Name != "" {
				t.Fatalf("name = %q, want body not decoded", dst.Name)
			}
		})
	}
}

func TestDecodeJSONPropagatesReaderErrors(t *testing.T) {
	readErr := errors.New("request body read failed")
	for _, prefix := range []string{"", `{"name":"Alex"}`, `{"name":"Alex"}` + " \t\r\n"} {
		t.Run(prefix, func(t *testing.T) {
			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", io.MultiReader(strings.NewReader(prefix), iotest.ErrReader(readErr)))
			req.Header.Set(HeaderContentType, MediaTypeJSON)
			var dst struct {
				Name string `json:"name"`
			}
			if err := DecodeJSON(req, &dst); !errors.Is(err, readErr) {
				t.Fatalf("error = %v, want reader error", err)
			}
		})
	}
}

func TestDecodeJSONAcceptsNonObjectValues(t *testing.T) {
	for _, body := range []string{`null`, `true`, `42`, `[]`, `"single"`} {
		t.Run(body, func(t *testing.T) {
			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", strings.NewReader(body+" \t\r\n"))
			req.Header.Set(HeaderContentType, MediaTypeJSON)
			var dst json.RawMessage
			if err := DecodeJSON(req, &dst); err != nil {
				t.Fatalf("DecodeJSON() error = %v", err)
			}
			if string(dst) != body {
				t.Fatalf("value = %s, want %s", dst, body)
			}
		})
	}
}
