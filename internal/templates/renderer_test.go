package templates_test

import (
	"bytes"
	"fmt"
	"ride-home-router/internal/templates"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
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

func validTemplatesFS() fstest.MapFS {
	templatesFS := fstest.MapFS{
		"templates/layout.html":                       {Data: []byte(`<header>Ride Home Router</header><main>{{template "content" .}}</main>`)},
		"templates/partials/badge.html":               {Data: []byte(`{{define "badge"}}<strong>{{.}}</strong>{{end}}`)},
		"templates/partials/address_suggestions.html": {Data: []byte(`address: {{.}}`)},
	}
	for _, name := range pageNames {
		templatesFS["templates/"+name] = &fstest.MapFile{Data: []byte(`{{define "content"}}page{{end}}`)}
	}
	templatesFS["templates/index.html"] = &fstest.MapFile{Data: []byte(`{{define "content"}}Welcome, {{template "badge" .}}{{end}}`)}
	return templatesFS
}

func TestRendererRendersPageWithLayoutAndPartials(t *testing.T) {
	renderer, err := templates.New(validTemplatesFS())
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

func TestRendererRendersPartialsWithoutLayout(t *testing.T) {
	renderer, err := templates.New(validTemplatesFS())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	tests := []struct {
		name string
		want string
	}{
		{name: "badge", want: "<strong>Ar</strong>"},
		{name: "address_suggestions.html", want: "address: Ar"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			if err := renderer.Render(&output, test.name, "Ar"); err != nil {
				t.Fatalf("Render() error = %v", err)
			}
			if got := output.String(); got != test.want {
				t.Fatalf("Render() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestNewFailsForMissingOrInvalidRequiredTemplates(t *testing.T) {
	t.Run("missing layout", func(t *testing.T) {
		templatesFS := validTemplatesFS()
		delete(templatesFS, "templates/layout.html")

		if _, err := templates.New(templatesFS); err == nil {
			t.Fatal("New() error = nil, want missing layout error")
		}
	})

	t.Run("invalid page", func(t *testing.T) {
		templatesFS := validTemplatesFS()
		templatesFS["templates/index.html"] = &fstest.MapFile{Data: []byte(`{{define "content"}}`)}

		if _, err := templates.New(templatesFS); err == nil {
			t.Fatal("New() error = nil, want invalid page error")
		}
	})
}

func TestRendererReturnsErrorForUnknownName(t *testing.T) {
	renderer, err := templates.New(validTemplatesFS())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	var output bytes.Buffer
	err = renderer.Render(&output, "unknown", nil)
	if err == nil {
		t.Fatal("Render() error = nil, want unknown template error")
	}
	if !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("Render() error = %q, want template name", err)
	}
}

func TestRendererSupportsConcurrentPageAndPartialRendering(t *testing.T) {
	renderer, err := templates.New(validTemplatesFS())
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
