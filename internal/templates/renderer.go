package templates

import (
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"path"
	"strings"

	"github.com/aryan-binazir/ride-home-router/internal/templateutil"
	"github.com/aryan-binazir/ride-home-router/web"
)

var pageNames = []string{
	"editor.html",
	"index.html",
	"participants.html",
	"drivers.html",
	"labels.html",
	"activity_locations.html",
	"vans.html",
	"settings.html",
	"history.html",
}

type Renderer struct {
	partials *template.Template
	pages    map[string]*template.Template
}

func New(templatesFS fs.FS, authConfig web.AuthConfig) (*Renderer, error) {
	base := template.New("").Funcs(templateutil.FuncMap()).Funcs(template.FuncMap{
		"authConfig": func() web.AuthConfig { return authConfig },
	})
	if _, err := base.Parse(web.AuthHeadTemplate); err != nil {
		return nil, fmt.Errorf("parse auth head: %w", err)
	}

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

func (r *Renderer) Render(w io.Writer, name string, data any) error {
	if pageName, block, partial := strings.Cut(name, "#"); partial {
		page := r.pages[pageName]
		if page == nil {
			return fmt.Errorf("unknown page %q", pageName)
		}
		return page.ExecuteTemplate(w, block, data)
	}
	if page, ok := r.pages[name]; ok {
		return page.ExecuteTemplate(w, "layout.html", data)
	}
	return r.partials.ExecuteTemplate(w, name, data)
}
