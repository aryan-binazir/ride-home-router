package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"ride-home-router/internal/models"
	"ride-home-router/internal/postgres"
	"ride-home-router/internal/postgres/postgrestest"
	"ride-home-router/internal/routesession"
	"strings"
	"testing"
)

func TestHandleVansPage_RendersNavAndSavedVans(t *testing.T) {
	handler, store := newTestPageHandler(t)

	if _, err := store.OrganizationVehicles().Create(context.Background(), &models.OrganizationVehicle{
		Name:     "North Campus Van",
		Capacity: 8,
	}); err != nil {
		t.Fatalf("create van: %v", err)
	}

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/vans", nil)
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

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/settings", nil)
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

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
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

func TestHandleLabelsPage_RendersLabelsNavAndTable(t *testing.T) {
	handler, store := newTestPageHandler(t)

	if _, err := store.Labels().Create(context.Background(), &models.Label{Name: "Youth Conference"}); err != nil {
		t.Fatalf("create label: %v", err)
	}

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/labels", nil)
	rr := httptest.NewRecorder()

	handler.HandleLabelsPage(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%q", rr.Code, http.StatusOK, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, `href="/labels" class="active"`) {
		t.Fatalf("expected Labels nav item to be active, body=%q", body)
	}
	if !strings.Contains(body, "Youth Conference") {
		t.Fatalf("expected saved label to render, body=%q", body)
	}
}

func TestHandleIndexPage_RendersLabelFiltersAndRowMetadata(t *testing.T) {
	handler, store := newTestPageHandler(t)

	label, err := store.Labels().Create(context.Background(), &models.Label{Name: "Summer Camp"})
	if err != nil {
		t.Fatalf("create label: %v", err)
	}
	participant, err := store.Participants().Create(context.Background(), &models.Participant{
		Name:    "Participant One",
		Address: "1 Rider Way",
		Lat:     40.1,
		Lng:     -73.9,
	})
	if err != nil {
		t.Fatalf("create participant: %v", err)
	}
	driver, err := store.Drivers().Create(context.Background(), &models.Driver{
		Name:            "Driver One",
		Address:         "1 Driver Way",
		Lat:             40.2,
		Lng:             -73.8,
		VehicleCapacity: 4,
	})
	if err != nil {
		t.Fatalf("create driver: %v", err)
	}
	if err := store.Labels().SetLabelsForParticipant(context.Background(), participant.ID, []int64{label.ID}); err != nil {
		t.Fatalf("SetLabelsForParticipant() error = %v", err)
	}
	if err := store.Labels().SetLabelsForDriver(context.Background(), driver.ID, []int64{label.ID}); err != nil {
		t.Fatalf("SetLabelsForDriver() error = %v", err)
	}

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()

	handler.HandleIndexPage(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%q", rr.Code, http.StatusOK, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, `data-label-id="`+int64ToString(label.ID)+`"`) {
		t.Fatalf("expected label filter chip hook, body=%q", body)
	}
	if !strings.Contains(body, `data-labels="`+int64ToString(label.ID)+`"`) {
		t.Fatalf("expected row label metadata, body=%q", body)
	}
}

func newTestPageHandler(t *testing.T) (*Handler, *postgres.Store) {
	t.Helper()

	store := postgrestest.Open(t)

	handler := &Handler{
		DB:           store,
		Renderer:     loadEmbeddedTemplates(t),
		RouteSession: routesession.NewStore(routeEditDistanceCalculator{}),
	}

	t.Cleanup(handler.RouteSession.Close)

	return handler, store
}
