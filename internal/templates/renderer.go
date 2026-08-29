package templates

import (
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"path"
	"ride-home-router/internal/templateutil"
	"strings"
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

var mobilePageNames = []string{
	"plan.html", "location.html", "riders.html", "drivers.html", "when.html", "routes.html",
	"people.html", "person_form.html", "places.html", "place_form.html", "history.html", "history_detail.html",
}

// Renderer loads and executes the application's page and partial templates.
type Renderer struct {
	partials       *template.Template
	pages          map[string]*template.Template
	mobilePages    map[string]*template.Template
	mobilePartials *template.Template
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
		partials:    base,
		pages:       make(map[string]*template.Template, len(pageNames)),
		mobilePages: make(map[string]*template.Template, len(mobilePageNames)),
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

	if mobileLayout, readErr := fs.ReadFile(templatesFS, "templates/mobile/layout.html"); readErr == nil {
		mobileBase := template.New("").Funcs(templateutil.FuncMap())
		if _, err := mobileBase.New("mobile_layout.html").Parse(string(mobileLayout)); err != nil {
			return nil, fmt.Errorf("parse mobile layout: %w", err)
		}
		mobilePartials, err := fs.Glob(templatesFS, "templates/mobile/partials/*.html")
		if err != nil {
			return nil, fmt.Errorf("glob mobile partials: %w", err)
		}
		for _, file := range mobilePartials {
			content, err := fs.ReadFile(templatesFS, file)
			if err != nil {
				return nil, fmt.Errorf("read mobile partial %s: %w", file, err)
			}
			if _, err := mobileBase.New(path.Base(file)).Parse(string(content)); err != nil {
				return nil, fmt.Errorf("parse mobile partial %s: %w", file, err)
			}
		}
		renderer.mobilePartials = mobileBase
		for _, name := range mobilePageNames {
			content, err := fs.ReadFile(templatesFS, "templates/mobile/"+name)
			if err != nil {
				return nil, fmt.Errorf("read mobile page %s: %w", name, err)
			}
			page, err := mobileBase.Clone()
			if err != nil {
				return nil, fmt.Errorf("clone mobile templates for page %s: %w", name, err)
			}
			if _, err := page.New(name).Parse(string(content)); err != nil {
				return nil, fmt.Errorf("parse mobile page %s: %w", name, err)
			}
			renderer.mobilePages["mobile/"+name] = page
		}
	} else if !errors.Is(readErr, fs.ErrNotExist) {
		return nil, fmt.Errorf("read mobile layout: %w", readErr)
	}

	return renderer, nil
}

// Render executes name with data into w.
func (r *Renderer) Render(w io.Writer, name string, data any) error {
	if page, ok := r.pages[name]; ok {
		return page.ExecuteTemplate(w, "layout.html", data)
	}
	if page, ok := r.mobilePages[name]; ok {
		return page.ExecuteTemplate(w, "mobile_layout.html", data)
	}
	if strings.HasPrefix(name, "mobile_") && r.mobilePartials != nil && r.mobilePartials.Lookup(name) != nil {
		return r.mobilePartials.ExecuteTemplate(w, name, data)
	}
	return r.partials.ExecuteTemplate(w, name, data)
}
