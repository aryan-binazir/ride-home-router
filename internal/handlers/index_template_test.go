package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleIndexPage_LoadsEventPlannerScript(t *testing.T) {
	handler, _ := newTestPageHandler(t)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()

	handler.HandleIndexPage(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	body := rr.Body.String()
	if !strings.Contains(body, `<script src="/static/js/event-planner.js" defer></script>`) {
		t.Fatal("rendered index page should defer event-planner.js")
	}
	if !strings.Contains(body, `<script src="/static/js/ui.js" defer></script>`) {
		t.Fatal("rendered index page should defer ui.js so it runs after the DOM is parsed")
	}
	if strings.Contains(body, `/static/js/route-copy.js`) {
		t.Fatal("rendered index page should not load the removed route-copy.js")
	}
	for _, fragment := range []string{
		`id="planner-workspace"`,
		`id="plan-pane-toggle"`,
		`aria-expanded="true"`,
		`aria-controls="plan-pane-content"`,
		`id="results-section"`,
	} {
		if !strings.Contains(body, fragment) {
			t.Fatalf("rendered index page should contain two-pane control markup %q", fragment)
		}
	}
}
