package handlers

import (
	"ride-home-router/internal/templates"
	"ride-home-router/web"
	"testing"
)

func loadEmbeddedTemplates(t *testing.T) *templates.Renderer {
	t.Helper()

	renderer, err := templates.New(web.Templates)
	if err != nil {
		t.Fatalf("load embedded templates: %v", err)
	}
	return renderer
}
