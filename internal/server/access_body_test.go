package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"ride-home-router/internal/access/accesstest"
	"ride-home-router/internal/handlers"
	"ride-home-router/internal/postgres/postgrestest"
	"testing"
)

type observedUpload struct{ reads int }

func (b *observedUpload) Read(p []byte) (int, error) { b.reads++; clear(p); return len(p), nil }
func (b *observedUpload) Close() error               { return nil }

func TestUnauthenticatedImportDoesNotReadRequestBody(t *testing.T) {
	f := accesstest.New(t)
	// Drive the mounted production handler with an instrumented body to prove
	// rejection happens before any application body read, not just before writes.
	s, err := New(t.Context(), Config{Addr: "127.0.0.1:0", DatabaseURL: postgrestest.DatabaseURL(t), Auth: f.Config()})
	if err != nil {
		t.Fatal(err)
	}
	addr, err := s.Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	})
	body := &observedUpload{}
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "http://"+addr+"/api/v1/imports", nil)
	r.Body = body
	r.ContentLength = handlers.MaxImportUploadBytes + 1
	r.Header.Set("Content-Type", "multipart/form-data; boundary=test")
	r.Header.Set("HX-Request", "true")
	w := httptest.NewRecorder()
	s.httpServer.Handler.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized || body.reads != 0 {
		t.Fatalf("status=%d body reads=%d; want 401 before reading body", w.Code, body.reads)
	}
}
