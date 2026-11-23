package server

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"ride-home-router/internal/access/accesstest"
	"ride-home-router/internal/handlers"
	"ride-home-router/internal/postgres/postgrestest"
	"ride-home-router/web"
	"strings"
	"testing"
	"testing/fstest"
)

func TestStaticCachingMatchesFileServer(t *testing.T) {
	files, err := fs.Sub(web.Static, "static")
	if err != nil {
		t.Fatal(err)
	}
	old := securityHeadersMiddleware(http.StripPrefix("/static/", http.FileServer(http.FS(files))))
	current := securityHeadersMiddleware(setupRoutes(&handlers.Handler{}, web.Static))
	err = fs.WalkDir(files, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		versioned := web.AssetURL(name)
		if !strings.HasPrefix(versioned, "/static/"+name+"?v=") {
			t.Fatalf("AssetURL(%q) = %q, want a versioned URL for that file", name, versioned)
		}
		cases := []struct {
			name, method, target, byteRange string
			cache                           bool
		}{
			{"match", "GET", versioned, "", true},
			{"head", "HEAD", versioned, "", true},
			{"range", "GET", versioned, "bytes=0-3", true},
			{"bad-range", "GET", versioned, "bytes=999999999-", false},
			{"missing", "GET", "/static/" + name, "", false},
			{"wrong", "GET", "/static/" + name + "?v=wrong", "", false},
			{"duplicate", "GET", versioned + "&v=wrong", "", false},
			{"post", "POST", versioned, "", false},
		}
		for _, tc := range cases {
			t.Run(name+"/"+tc.name, func(t *testing.T) {
				serve := func(h http.Handler) *httptest.ResponseRecorder {
					req := httptest.NewRequestWithContext(t.Context(), tc.method, tc.target, nil)
					req.Header.Set("X-Forwarded-Proto", "https")
					if tc.byteRange != "" {
						req.Header.Set("Range", tc.byteRange)
					}
					rr := httptest.NewRecorder()
					h.ServeHTTP(rr, req)
					return rr
				}
				before, after := serve(old), serve(current)
				expected := ""
				if tc.cache {
					expected = "public, max-age=31536000, immutable"
				}
				if got := after.Header().Get("Cache-Control"); got != expected {
					t.Fatalf("cache %q want %q", got, expected)
				}
				after.Header().Del("Cache-Control")
				if before.Code != after.Code || before.Body.String() != after.Body.String() || !reflect.DeepEqual(before.Header(), after.Header()) {
					t.Fatalf("FileServer response changed: %d vs %d; headers %v vs %v", before.Code, after.Code, before.Header(), after.Header())
				}
				if after.Header().Get("Strict-Transport-Security") == "" || after.Header().Get("Content-Security-Policy") == "" {
					t.Fatal("missing security headers")
				}
			})
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"/static/missing.js?v=abc", "/static/", "/static/js", "/static/js/", "/static/js/../css/style.css?v=abc"} {
		before, after := httptest.NewRecorder(), httptest.NewRecorder()
		old.ServeHTTP(before, httptest.NewRequestWithContext(t.Context(), "GET", target, nil))
		current.ServeHTTP(after, httptest.NewRequestWithContext(t.Context(), "GET", target, nil))
		if after.Header().Get("Cache-Control") != "" {
			t.Fatalf("cached %s", target)
		}
		if target == "/static/js/../css/style.css?v=abc" {
			continue
		} // ServeMux canonicalizes paths before FileServer.
		if before.Code != after.Code || before.Body.String() != after.Body.String() || !reflect.DeepEqual(before.Header(), after.Header()) {
			t.Fatalf("changed response for %s", target)
		}
	}
}

func TestStaticHashBelongsToServedFilesystem(t *testing.T) {
	parsed, err := url.Parse(web.AssetURL("js/auth.js"))
	if err != nil {
		t.Fatal(err)
	}
	files := fstest.MapFS{"js/auth.js": &fstest.MapFile{Data: []byte("different release")}}
	handler, err := staticFileHandler(files)
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), "GET", "/js/auth.js?"+parsed.RawQuery, nil))
	if rr.Code != 200 || rr.Header().Get("Cache-Control") != "" || strings.TrimSpace(rr.Body.String()) != "different release" {
		t.Fatalf("wrong filesystem hash accepted: %v", rr)
	}
}

func TestStaticCachingLeavesHTMLNoStore(t *testing.T) {
	fixture := accesstest.New(t)
	_, base := startAccessServer(t, postgrestest.DatabaseURL(t), fixture)
	client := accessHTTP{t: t, client: &http.Client{}}
	for _, path := range []string{"/", "/sign-in"} {
		status, _, headers := client.request(base, "GET", path, fixture.Admin(), "", "", nil)
		if status != 200 || headers.Get("Cache-Control") != "no-store" {
			t.Fatalf("%s: status=%d cache=%q", path, status, headers.Get("Cache-Control"))
		}
	}
}
