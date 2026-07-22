package templates

import (
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"path"
	"ride-home-router/internal/templateutil"
)

var pageNames = []string{
	"index.html",
	"participants.html",
	"drivers.html",
	"labels.html",
	"activity_locations.html",
	"vans.html",
	"settings.html",
	"history.html",
}

// Renderer loads and executes the application's page and partial templates.
type Renderer struct {
	partials *template.Template
	pages    map[string]*template.Template
}

// New loads and precompiles all required templates from templatesFS.
func New(templatesFS fs.FS) (*Renderer, error) {
	base := template.New("").Funcs(templateutil.FuncMap())

	layout, err := fs.ReadFile(templatesFS, "templates/layout.html")
	if err != nil {
		return nil, fmt.Errorf("read layout: %w", err)
	}
	if _, err := base.New("layout.html").Parse(string(layout)); err != nil {
		return nil, fmt.Errorf("parse layout: %w", err)
	}

	partialFiles, err := fs.Glob(templatesFS, "templates/partials/*.html")
	if err != nil {
		return nil, fmt.Errorf("glob partials: %w", err)
	}
	for _, file := range partialFiles {
		content, err := fs.ReadFile(templatesFS, file)
		if err != nil {
			return nil, fmt.Errorf("read partial %s: %w", file, err)
		}
		if _, err := base.New(path.Base(file)).Parse(string(content)); err != nil {
			return nil, fmt.Errorf("parse partial %s: %w", file, err)
		}
	}

	renderer := &Renderer{
		partials: base,
		pages:    make(map[string]*template.Template, len(pageNames)),
	}
	for _, name := range pageNames {
		content, err := fs.ReadFile(templatesFS, "templates/"+name)
		if err != nil {
			return nil, fmt.Errorf("read page %s: %w", name, err)
		}

		page, err := base.Clone()
		if err != nil {
			return nil, fmt.Errorf("clone templates for page %s: %w", name, err)
		}
		if _, err := page.New(name).Parse(string(content)); err != nil {
			return nil, fmt.Errorf("parse page %s: %w", name, err)
		}
		renderer.pages[name] = page
	}

	return renderer, nil
}

// Render executes name with data into w.
func (r *Renderer) Render(w io.Writer, name string, data any) error {
	if page, ok := r.pages[name]; ok {
		return page.ExecuteTemplate(w, "layout.html", data)
	}
	return r.partials.ExecuteTemplate(w, name, data)
}
