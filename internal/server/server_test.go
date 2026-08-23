package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"ride-home-router/internal/importer"
	"strings"
	"testing"
)

func TestNewWiresAndShutdownClosesImportSessionStore(t *testing.T) {
	server, err := New(Config{Addr: "127.0.0.1:0", DBPath: filepath.Join(t.TempDir(), "server.db")})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if server.handler == nil || server.handler.ImportSession == nil {
		t.Fatal("New() did not wire the import session store")
	}
	if err := server.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	grid, err := importer.Parse(strings.NewReader("name,address\nRider,1 Main St\n"), importer.FormatCSV, "")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if _, err := server.handler.ImportSession.Create(importer.KindParticipant, "closed.csv", grid); !errors.Is(err, importer.ErrStoreClosed) {
		t.Fatalf("Create() after Shutdown error = %v, want ErrStoreClosed", err)
	}
}

func TestHandleMethods_RejectsUnsupportedMethod(t *testing.T) {
	handler := handleMethods(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}, nil, nil, nil)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/participants", nil)
	rec := httptest.NewRecorder()

	handler(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
	if rec.Body.String() != serverMessageMethodNotAllowed+"\n" {
		t.Fatalf("body = %q, want %q", rec.Body.String(), serverMessageMethodNotAllowed+"\n")
	}
}

func TestHandleResourcePath_UsesEditHandlerAndRejectsCollectionPath(t *testing.T) {
	var editCalled bool

	handler := handleResourcePath(
		"/api/v1/participants/",
		"/edit",
		func(w http.ResponseWriter, _ *http.Request) {
			editCalled = true
			w.WriteHeader(http.StatusNoContent)
		},
		nil,
		nil,
		nil,
	)

	editReq := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/participants/42/edit", nil)
	editRec := httptest.NewRecorder()
	handler(editRec, editReq)

	if !editCalled {
		t.Fatal("expected edit handler to be called")
	}
	if editRec.Code != http.StatusNoContent {
		t.Fatalf("edit status = %d, want %d", editRec.Code, http.StatusNoContent)
	}

	emptyReq := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/participants/", nil)
	emptyRec := httptest.NewRecorder()
	handler(emptyRec, emptyReq)

	if emptyRec.Code != http.StatusNotFound {
		t.Fatalf("empty path status = %d, want %d", emptyRec.Code, http.StatusNotFound)
	}
	if emptyRec.Body.String() != serverMessageNotFound+"\n" {
		t.Fatalf("empty path body = %q, want %q", emptyRec.Body.String(), serverMessageNotFound+"\n")
	}
}
