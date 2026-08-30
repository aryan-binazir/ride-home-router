package templateutil

import (
	"bytes"
	"html/template"
	"testing"
)

func TestPluralizeRendersCountAwareNouns(t *testing.T) {
	tmpl, err := template.New("counts").Funcs(FuncMap()).Parse(`1 {{pluralize 1 "passenger"}}|2 {{pluralize 2 "passenger"}}`)
	if err != nil {
		t.Fatalf("parse template: %v", err)
	}

	var rendered bytes.Buffer
	if err := tmpl.Execute(&rendered, nil); err != nil {
		t.Fatalf("execute template: %v", err)
	}
	if got, want := rendered.String(), "1 passenger|2 passengers"; got != want {
		t.Fatalf("rendered counts = %q, want %q", got, want)
	}
}
