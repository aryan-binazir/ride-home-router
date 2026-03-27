package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandleMethods_RejectsUnsupportedMethod(t *testing.T) {
	handler := handleMethods(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}, nil, nil, nil)

	req := httptest.NewRequest(http.MethodPost, "/participants", nil)
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

	editReq := httptest.NewRequest(http.MethodGet, "/api/v1/participants/42/edit", nil)
	editRec := httptest.NewRecorder()
	handler(editRec, editReq)

	if !editCalled {
		t.Fatal("expected edit handler to be called")
	}
	if editRec.Code != http.StatusNoContent {
		t.Fatalf("edit status = %d, want %d", editRec.Code, http.StatusNoContent)
	}

	emptyReq := httptest.NewRequest(http.MethodGet, "/api/v1/participants/", nil)
	emptyRec := httptest.NewRecorder()
	handler(emptyRec, emptyReq)

	if emptyRec.Code != http.StatusNotFound {
		t.Fatalf("empty path status = %d, want %d", emptyRec.Code, http.StatusNotFound)
	}
	if emptyRec.Body.String() != serverMessageNotFound+"\n" {
		t.Fatalf("empty path body = %q, want %q", emptyRec.Body.String(), serverMessageNotFound+"\n")
	}
}
