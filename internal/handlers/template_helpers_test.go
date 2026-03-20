package handlers

import (
	"html/template"
	"io/fs"
	"strings"
	"testing"

	"ride-home-router/internal/templateutil"
	"ride-home-router/web"
)

func loadEmbeddedTemplates(t *testing.T) *TemplateSet {
	t.Helper()

	funcs := templateutil.FuncMap()

	base := template.New("").Funcs(funcs)

	layoutContent, err := fs.ReadFile(web.Templates, "templates/layout.html")
	if err != nil {
		t.Fatalf("read layout template: %v", err)
	}
	if _, err := base.New("layout.html").Parse(string(layoutContent)); err != nil {
		t.Fatalf("parse layout template: %v", err)
	}

	partialFiles, err := fs.Glob(web.Templates, "templates/partials/*.html")
	if err != nil {
		t.Fatalf("glob partial templates: %v", err)
	}
	for _, file := range partialFiles {
		content, err := fs.ReadFile(web.Templates, file)
		if err != nil {
			t.Fatalf("read partial template %s: %v", file, err)
		}
		name := strings.TrimPrefix(file, "templates/partials/")
		if _, err := base.New(name).Parse(string(content)); err != nil {
			t.Fatalf("parse partial template %s: %v", file, err)
		}
	}

	pages := make(map[string]string)
	pageFiles := []string{"index.html", "participants.html", "drivers.html", "activity_locations.html", "vans.html", "settings.html", "history.html"}
	for _, name := range pageFiles {
		content, err := fs.ReadFile(web.Templates, "templates/"+name)
		if err != nil {
			t.Fatalf("read page template %s: %v", name, err)
		}
		pages[name] = string(content)
	}

	return &TemplateSet{
		Base:  base,
		Pages: pages,
		Funcs: funcs,
	}
}
