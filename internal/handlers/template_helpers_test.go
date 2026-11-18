package handlers

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"ride-home-router/internal/templates"
	"ride-home-router/web"
	"strings"
	"testing"
)

func loadEmbeddedTemplates(t *testing.T) *templates.Renderer {
	t.Helper()

	renderer, err := templates.New(web.Templates, web.AuthConfig{})
	if err != nil {
		t.Fatalf("load embedded templates: %v", err)
	}
	return renderer
}

func postForm(t *testing.T, path string, form url.Values, handler http.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, path, strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	handler(response, request)
	return response
}
