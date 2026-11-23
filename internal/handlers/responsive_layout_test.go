package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"ride-home-router/internal/models"
	"strings"
	"testing"
)

func TestHandleIndexPage_RouteOptionsRenderInsideFormBeforeActionBar(t *testing.T) {
	handler, _ := newTestPageHandler(t)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()

	handler.HandleIndexPage(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	body := rr.Body.String()

	formStart := strings.Index(body, `id="event-form"`)
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

func TestRosterPages_RenderBulkToolbarAndTargetTable(t *testing.T) {
	handler, store := newTestPageHandler(t)
	ctx := context.Background()
	if _, err := store.Labels().Create(ctx, &models.Label{Name: "Youth"}); err != nil {
		t.Fatalf("create label: %v", err)
	}
	if _, err := store.Participants().Create(ctx, &models.Participant{Name: "Pat Rider", Address: "1 Main St", Lat: 35.9, Lng: -79.0}); err != nil {
		t.Fatalf("create participant: %v", err)
	}
	if _, err := store.Drivers().Create(ctx, &models.Driver{Name: "Dee Driver", Address: "2 Main St", Lat: 35.9, Lng: -79.0, VehicleCapacity: 4}); err != nil {
		t.Fatalf("create driver: %v", err)
	}

	cases := []struct {
		name  string
		path  string
		serve http.HandlerFunc
		tbody string
	}{
		{"participants", "/participants", handler.HandleParticipantsPage, "participants-tbody"},
		{"drivers", "/drivers", handler.HandleDriversPage, "drivers-tbody"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(ctx, http.MethodGet, tc.path, nil)
			rr := httptest.NewRecorder()
			tc.serve(rr, req)
			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d", rr.Code)
			}
			body := rr.Body.String()
			for _, want := range []string{`class="bulk-toolbar"`, ">Select visible<", ">Clear selection<"} {
				if !strings.Contains(body, want) {
					t.Fatalf("expected %s in the rendered roster page", want)
				}
			}
			if !strings.Contains(body, `id="`+tc.tbody+`"`) {
				t.Fatalf("expected %s to render so the toolbar buttons have rows to act on", tc.tbody)
			}
		})
	}
}
