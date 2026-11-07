package templateutil

import (
	"bytes"
	"html/template"
	"testing"
	"time"
)

func TestFormatLongDateRendersCalendarDayInUTC(t *testing.T) {
	tmpl, err := template.New("date").Funcs(FuncMap()).Parse(`{{formatLongDate .}}`)
	if err != nil {
		t.Fatalf("parse template: %v", err)
	}

	var rendered bytes.Buffer
	day := time.Date(2026, time.November, 6, 23, 30, 0, 0, time.FixedZone("EST", -5*60*60))
	if err := tmpl.Execute(&rendered, day); err != nil {
		t.Fatalf("execute template: %v", err)
	}
	if got, want := rendered.String(), "Sat, Nov 7, 2026"; got != want {
		t.Fatalf("rendered date = %q, want %q", got, want)
	}
}

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
