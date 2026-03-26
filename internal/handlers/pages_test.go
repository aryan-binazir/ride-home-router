package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"ride-home-router/internal/models"
	"ride-home-router/internal/sqlite"
)

func TestHandleVansPage_RendersNavAndSavedVans(t *testing.T) {
	handler, store := newTestPageHandler(t)

	if _, err := store.OrganizationVehicles().Create(context.Background(), &models.OrganizationVehicle{
		Name:     "North Campus Van",
		Capacity: 8,
	}); err != nil {
		t.Fatalf("create van: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/vans", nil)
	rr := httptest.NewRecorder()

	handler.HandleVansPage(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	body := rr.Body.String()
	if !strings.Contains(body, `href="/vans" class="active"`) {
		t.Fatalf("expected Vans nav item to be active, body=%q", body)
	}
	if !strings.Contains(body, "Saved Vans") {
		t.Fatalf("expected Vans page content, body=%q", body)
	}
	if !strings.Contains(body, "North Campus Van") {
		t.Fatalf("expected saved van to be rendered, body=%q", body)
	}
}

func TestHandleSettingsPage_DoesNotRenderVanManagement(t *testing.T) {
	handler, _ := newTestPageHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/settings", nil)
	rr := httptest.NewRecorder()

	handler.HandleSettingsPage(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	body := rr.Body.String()
	if strings.Contains(body, "Saved Vans") || strings.Contains(body, "Add Van") {
		t.Fatalf("expected Settings page to omit van management, body=%q", body)
	}
	if !strings.Contains(body, `href="/vans"`) {
		t.Fatalf("expected Settings page to link to Vans page, body=%q", body)
	}
}

func TestHandleIndexPage_RendersVanAssignmentsPanelWhenVansExist(t *testing.T) {
	handler, store := newTestPageHandler(t)

	if _, err := store.ActivityLocations().Create(context.Background(), &models.ActivityLocation{
		Name:    "Gym",
		Address: "1 Gym Way",
		Lat:     40.10,
		Lng:     -73.90,
	}); err != nil {
		t.Fatalf("create activity location: %v", err)
	}
	if _, err := store.Drivers().Create(context.Background(), &models.Driver{
		Name:            "Driver One",
		Address:         "2 Driver Lane",
		Lat:             40.20,
		Lng:             -73.80,
		VehicleCapacity: 4,
	}); err != nil {
		t.Fatalf("create driver: %v", err)
	}
	if _, err := store.OrganizationVehicles().Create(context.Background(), &models.OrganizationVehicle{
		Name:     "Overflow Van",
		Capacity: 7,
	}); err != nil {
		t.Fatalf("create van: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()

	handler.HandleIndexPage(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	body := rr.Body.String()
	if !strings.Contains(body, "Vehicle for this event") {
		t.Fatalf("expected Event Planning page to render inline van assignment controls, body=%q", body)
	}
	if !strings.Contains(body, `name="route_time"`) {
		t.Fatalf("expected Event Planning page to render the route time input, body=%q", body)
	}
	if !strings.Contains(body, "Depart activity location at") {
		t.Fatalf("expected Event Planning page to render the route time label, body=%q", body)
	}
	if !strings.Contains(body, `id="event-org-vehicles"`) {
		t.Fatalf("expected Event Planning page to include vans JSON payload, body=%q", body)
	}
	if !strings.Contains(body, "Overflow Van") {
		t.Fatalf("expected Event Planning page to include saved van data, body=%q", body)
	}
}

func newTestPageHandler(t *testing.T) (*Handler, *sqlite.Store) {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "pages-test.db")
	store, err := sqlite.New(dbPath)
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}

	handler := &Handler{
		DB:           store,
		Templates:    loadEmbeddedTemplates(t),
		RouteSession: NewRouteSessionStore(),
	}

	t.Cleanup(func() {
		handler.RouteSession.Close()
		if err := store.Close(); err != nil {
			t.Fatalf("close sqlite store: %v", err)
		}
	})

	return handler, store
}
