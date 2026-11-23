package web

import (
	"bytes"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestVersionedAssetsServeTheEmbeddedRelease(t *testing.T) {
	files, err := fs.Sub(Static, "static")
	if err != nil {
		t.Fatal(err)
	}
	handler := http.StripPrefix("/static/", http.FileServer(http.FS(files)))
	for _, name := range []string{"css/style.css", "css/login.css", "js/navigation.js", "js/event-planner.js", "js/auth.js"} {
		url := AssetURL(name)
		if !strings.HasPrefix(url, "/static/"+name+"?v=") {
			t.Fatalf("unversioned asset %q", url)
		}
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodGet, url, nil))
		content, err := Static.ReadFile("static/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if rr.Code != 200 || !bytes.Equal(rr.Body.Bytes(), content) {
			t.Fatalf("versioned %s did not serve this release", name)
		}
	}
}
