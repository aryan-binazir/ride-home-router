package templates_test

import (
	"bytes"
	"fmt"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/aryan-binazir/ride-home-router/internal/templates"
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

func validTemplatesFS() fstest.MapFS {
	templatesFS := fstest.MapFS{
		"templates/layout.html":                       {Data: []byte(`<header>Ride Home Router</header><main>{{template "content" .}}</main>`)},
		"templates/partials/badge.html":               {Data: []byte(`{{define "badge"}}<strong>{{.}}</strong>{{end}}`)},
		"templates/partials/status.html":              {Data: []byte(`{{define "status"}}desktop {{.}}{{end}}`)},
		"templates/partials/address_suggestions.html": {Data: []byte(`address: {{.}}`)},
	}
	for _, name := range pageNames {
		templatesFS["templates/"+name] = &fstest.MapFile{Data: []byte(`{{define "content"}}page{{end}}`)}
	}
	templatesFS["templates/index.html"] = &fstest.MapFile{Data: []byte(`{{define "content"}}Welcome, {{template "badge" .}}{{end}}`)}
	return templatesFS
}

func TestRendererRendersPageWithLayoutAndPartials(t *testing.T) {
	renderer, err := templates.New(validTemplatesFS(), web.AuthConfig{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	var output bytes.Buffer
	if err := renderer.Render(&output, "index.html", "Ar"); err != nil {
		t.Fatalf("Render() error = %v", err)
	}

	const want = `<header>Ride Home Router</header><main>Welcome, <strong>Ar</strong></main>`
	if got := output.String(); got != want {
		t.Fatalf("Render() = %q, want %q", got, want)
	}
}

func TestNewFailsForMissingOrInvalidRequiredTemplates(t *testing.T) {
	t.Run("missing layout", func(t *testing.T) {
		templatesFS := validTemplatesFS()
		delete(templatesFS, "templates/layout.html")

		if _, err := templates.New(templatesFS, web.AuthConfig{}); err == nil {
			t.Fatal("New() error = nil, want missing layout error")
		}
	})

	t.Run("invalid page", func(t *testing.T) {
		templatesFS := validTemplatesFS()
		templatesFS["templates/index.html"] = &fstest.MapFile{Data: []byte(`{{define "content"}}`)}

		if _, err := templates.New(templatesFS, web.AuthConfig{}); err == nil {
			t.Fatal("New() error = nil, want invalid page error")
		}
	})
}

func TestRendererSupportsConcurrentPageAndPartialRendering(t *testing.T) {
	renderer, err := templates.New(validTemplatesFS(), web.AuthConfig{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	const renders = 50
	var wg sync.WaitGroup
	errors := make(chan error, renders*2)
	for range renders {
		wg.Add(2)
		go func() {
			defer wg.Done()
			var output bytes.Buffer
			if err := renderer.Render(&output, "index.html", "Ar"); err != nil {
				errors <- err
				return
			}
			const want = `<header>Ride Home Router</header><main>Welcome, <strong>Ar</strong></main>`
			if got := output.String(); got != want {
				errors <- fmt.Errorf("Render() = %q, want %q", got, want)
			}
		}()
		go func() {
			defer wg.Done()
			var output bytes.Buffer
			if err := renderer.Render(&output, "address_suggestions.html", "Ar"); err != nil {
				errors <- err
				return
			}
			const want = "address: Ar"
			if got := output.String(); got != want {
				errors <- fmt.Errorf("Render() = %q, want %q", got, want)
			}
		}()
	}
	wg.Wait()
	close(errors)

	for err := range errors {
		t.Error(err)
	}
}

func TestLayoutDefinesAuthFetchBeforePageScripts(t *testing.T) {
	renderer, err := templates.New(web.Templates, web.AuthConfig{})
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := renderer.Render(&output, "settings.html", map[string]any{"Title": "Fixture", "ActivePage": "settings", "IsAdmin": true}); err != nil {
		t.Fatal(err)
	}
	page := output.String()
	auth := strings.Index(page, `js/auth.js`)
	if auth < 0 {
		t.Fatal("layout does not load auth.js")
	}
	for _, script := range []string{"js/settings.js", "js/event-planner.js", "js/editors.js", "js/roster.js"} {
		if at := strings.Index(page, script); at < 0 || at < auth {
			t.Errorf("%s must load after auth.js so its first requests use authFetch (auth.js at %d, %s at %d)", script, auth, script, at)
		}
	}
}
