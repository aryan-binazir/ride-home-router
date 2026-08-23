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
