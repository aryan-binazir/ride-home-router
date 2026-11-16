package web_test

import (
	"bytes"
	"html/template"
	"ride-home-router/web"
	"strings"
	"testing"
)

func TestAuthHeadUsesEscapedPublicConfigAndMatchingCORS(t *testing.T) {
	page := template.Must(template.New("head").Parse(web.AuthHeadTemplate + `{{template "auth-head" .}}`))
	for _, config := range []web.AuthConfig{
		{},
		{PublishableKey: "pk_test_fixture", ScriptURL: "https://fixture.clerk.accounts.dev/npm/@clerk/clerk-js@5/dist/clerk.browser.js"},
		{PublishableKey: `pk_test_"><script>alert(1)</script>`, ScriptURL: "/__dev/clerk.js"},
	} {
		var output bytes.Buffer
		if err := page.Execute(&output, config); err != nil {
			t.Fatal(err)
		}
		html := output.String()
		if config.PublishableKey == "" {
			if html != "" {
				t.Fatalf("unconfigured head: %q", html)
			}
			continue
		}
		if strings.Contains(html, "<script") || !strings.Contains(html, `data-publishable-key="`+template.HTMLEscapeString(config.PublishableKey)+`"`) {
			t.Fatalf("unsafe configuration: %s", html)
		}
		if !strings.Contains(html, `rel="preload" href="`+config.ScriptURL+`" as="script" crossorigin="anonymous"`) {
			t.Fatalf("preload must match the anonymous script request: %s", html)
		}
		if origin := config.Origin(); origin != "" && !strings.Contains(html, `rel="preconnect" href="`+origin+`" crossorigin="anonymous"`) {
			t.Fatalf("missing preconnect: %s", html)
		}
	}
}
