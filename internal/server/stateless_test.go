package server

import (
	"context"
	"io"
	"net/http"
	"ride-home-router/internal/access/accesstest"
	"ride-home-router/internal/postgres/postgrestest"
	"strings"
	"testing"
)

func TestSharedPagesOnDesktopAndMobile(t *testing.T) {
	fixture := accesstest.New(t)
	token := fixture.Admin()
	srv, err := New(t.Context(), Config{CredentialEncryptionKey: postgrestest.EncryptionKey, Auth: fixture.Config(), Addr: "127.0.0.1:0", DatabaseURL: postgrestest.DatabaseURL(t)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })
	addr, err := srv.Start()
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: accesstest.BearerTransport{Token: token}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for _, ua := range []string{"Desktop", "Mozilla/5.0 iPhone Mobile", "Android", "iPad"} {
		for _, path := range []string{"/?m=1", "/participants", "/drivers", "/settings"} {
			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+addr+path, nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("User-Agent", ua)
			req.Header.Set("Sec-CH-UA-Mobile", "?1")
			resp, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != 200 || !strings.Contains(string(body), `id="primary-navigation"`) || strings.Contains(string(body), `class="mobile-shell"`) {
				t.Fatalf("%s %s: status %d, missing shared shell", ua, path, resp.StatusCode)
			}
		}
	}
}
