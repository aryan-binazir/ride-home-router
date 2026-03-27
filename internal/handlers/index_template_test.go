package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleIndexPage_RendersDraftSaveAbortBeforeClearingSession(t *testing.T) {
	handler, _ := newTestPageHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()

	handler.HandleIndexPage(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	body := rr.Body.String()
	saveStart := strings.Index(body, "function saveEventPlannerDraft() {")
	if saveStart == -1 {
		t.Fatal("saveEventPlannerDraft function not found in rendered index page")
	}

	restoreAbort := strings.Index(body[saveStart:], "if (restoreController) restoreController.abort();")
	if restoreAbort == -1 {
		t.Fatal("saveEventPlannerDraft should abort an in-flight restore request")
	}
	restoreAbort += saveStart

	clearSession := strings.Index(body[saveStart:], "clearActiveSessionId();")
	if clearSession == -1 {
		t.Fatal("saveEventPlannerDraft should clear the active session id after local edits")
	}
	clearSession += saveStart

	if restoreAbort > clearSession {
		t.Fatalf("saveEventPlannerDraft clears the active session before aborting restore: abort=%d clear=%d", restoreAbort, clearSession)
	}
}

func TestHandleIndexPage_RendersRouteRestoreFetchHooks(t *testing.T) {
	handler, _ := newTestPageHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()

	handler.HandleIndexPage(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	body := rr.Body.String()
	restoreStart := strings.Index(body, "function restoreRouteSession(sessionId) {")
	if restoreStart == -1 {
		t.Fatal("restoreRouteSession function not found in rendered index page")
	}

	restoreBody := body[restoreStart:]
	if !strings.Contains(restoreBody, "restoreController = new AbortController();") {
		t.Fatal("restoreRouteSession should create an AbortController")
	}
	if !strings.Contains(restoreBody, "signal: restoreController.signal") {
		t.Fatal("restoreRouteSession fetch should use the AbortController signal")
	}
	if !strings.Contains(body, "const ACTIVE_SESSION_KEY = 'ride-home-router:active-session-id';") {
		t.Fatal("index page should render the active session localStorage key")
	}
}
