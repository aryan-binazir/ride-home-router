package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"ride-home-router/internal/models"
	"strings"
	"testing"
)

// The sticky action bar must stay compact on phones, so the route time and
// mode inputs render above it — but still inside #event-form so
// hx-include="#event-form" keeps posting them.
func TestHandleIndexPage_RouteOptionsRenderInsideFormBeforeActionBar(t *testing.T) {
	handler, _ := newTestPageHandler(t)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()

	handler.HandleIndexPage(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	body := rr.Body.String()

	formStart := strings.Index(body, `<form id="event-form">`)
	if formStart < 0 {
		t.Fatal("expected #event-form to render")
	}
	formEnd := strings.Index(body[formStart:], "</form>")
	if formEnd < 0 {
		t.Fatal("expected #event-form to close")
	}
	form := body[formStart : formStart+formEnd]

	actionBar := strings.Index(form, `class="action-bar"`)
	if actionBar < 0 {
		t.Fatal("expected the action bar inside #event-form")
	}
	for _, field := range []string{`name="route_time"`, `name="mode"`} {
		idx := strings.Index(form, field)
		if idx < 0 {
			t.Fatalf("expected %s inside #event-form", field)
		}
		if idx > actionBar {
			t.Fatalf("expected %s to render before the action bar so the sticky bar stays compact on phones", field)
		}
	}
}

// Card layout on phones hides the table header, which holds the only
// select-all-visible control, so the bulk toolbar needs its own button.
func TestRosterPages_RenderSelectVisibleButtonInBulkToolbar(t *testing.T) {
	handler, store := newTestPageHandler(t)
	if _, err := store.Labels().Create(context.Background(), &models.Label{Name: "Youth"}); err != nil {
		t.Fatalf("create label: %v", err)
	}

	cases := []struct {
		path   string
		serve  http.HandlerFunc
		tbody  string
		tables string
	}{
		{"/participants", handler.HandleParticipantsPage, "participants-tbody", "participant_list"},
		{"/drivers", handler.HandleDriversPage, "drivers-tbody", "driver_list"},
	}
	for _, tc := range cases {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, tc.path, nil)
		rr := httptest.NewRecorder()
		tc.serve(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("%s status = %d", tc.path, rr.Code)
		}
		body := rr.Body.String()
		want := `onclick="selectVisibleTableRows('` + tc.tbody + `', true)"`
		toolbar := strings.Index(body, `class="bulk-toolbar"`)
		btn := strings.Index(body, want)
		if toolbar < 0 || btn < 0 || btn < toolbar {
			t.Fatalf("%s: expected a Select visible button in the bulk toolbar (toolbar=%d btn=%d)", tc.path, toolbar, btn)
		}
	}
}
