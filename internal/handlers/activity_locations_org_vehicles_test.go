package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"ride-home-router/internal/geocoding"
	"ride-home-router/internal/models"
	"ride-home-router/internal/sqlite"
)

type stubGeocoder struct {
	result *geocoding.GeocodingResult
	err    error
}

func (g stubGeocoder) Geocode(_ context.Context, _ string) (*geocoding.GeocodingResult, error) {
	return g.result, g.err
}

func (g stubGeocoder) GeocodeWithRetry(_ context.Context, _ string, _ int) (*geocoding.GeocodingResult, error) {
	return g.result, g.err
}

func (g stubGeocoder) Search(_ context.Context, _ string, _ int) ([]geocoding.GeocodingResult, error) {
	return nil, g.err
}

func newTestManagementHandler(t *testing.T) (*Handler, *sqlite.Store) {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "management-test.db")
	store, err := sqlite.New(dbPath)
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}

	handler := &Handler{
		DB:        store,
		Templates: loadEmbeddedTemplates(t),
		Geocoder: stubGeocoder{
			result: &geocoding.GeocodingResult{
				Coords: models.Coordinates{Lat: 41.25, Lng: -72.75},
			},
		},
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

func TestHandleActivityLocationForm_RendersInlineEditForm(t *testing.T) {
	handler, store := newTestManagementHandler(t)

	location, err := store.ActivityLocations().Create(context.Background(), &models.ActivityLocation{
		Name:    "Gym",
		Address: "1 Main St",
		Lat:     40.1,
		Lng:     -73.9,
	})
	if err != nil {
		t.Fatalf("create activity location: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/activity-locations/"+int64ToString(location.ID)+"/edit", nil)
	req.Header.Set("HX-Request", "true")
	rr := httptest.NewRecorder()

	handler.HandleActivityLocationForm(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%q", rr.Code, http.StatusOK, rr.Body.String())
	}

	body := rr.Body.String()
	if !strings.Contains(body, `hx-put="/api/v1/activity-locations/`+int64ToString(location.ID)+`"`) {
		t.Fatalf("expected edit form to submit to update endpoint, body=%q", body)
	}
	if !strings.Contains(body, "Cancel") {
		t.Fatalf("expected cancel button, body=%q", body)
	}
}

func TestHandleUpdateActivityLocation_HTMXReturnsUpdatedRow(t *testing.T) {
	handler, store := newTestManagementHandler(t)

	location, err := store.ActivityLocations().Create(context.Background(), &models.ActivityLocation{
		Name:    "Gym",
		Address: "1 Main St",
		Lat:     40.1,
		Lng:     -73.9,
	})
	if err != nil {
		t.Fatalf("create activity location: %v", err)
	}

	form := url.Values{}
	form.Set("name", "Updated Gym")
	form.Set("address", "2 Main St")

	req := httptest.NewRequest(http.MethodPut, "/api/v1/activity-locations/"+int64ToString(location.ID), strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	rr := httptest.NewRecorder()

	handler.HandleUpdateActivityLocation(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%q", rr.Code, http.StatusOK, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "Updated Gym") {
		t.Fatalf("expected updated row HTML, body=%q", rr.Body.String())
	}
	if !strings.Contains(rr.Header().Get("HX-Trigger"), "updated!") {
		t.Fatalf("expected success toast trigger, got %q", rr.Header().Get("HX-Trigger"))
	}

	updated, err := store.ActivityLocations().GetByID(context.Background(), location.ID)
	if err != nil {
		t.Fatalf("get updated activity location: %v", err)
	}
	if updated.Name != "Updated Gym" || updated.Address != "2 Main St" {
		t.Fatalf("updated location = %+v", updated)
	}
	if updated.Lat != 41.25 || updated.Lng != -72.75 {
		t.Fatalf("updated coords = (%f, %f), want (41.25, -72.75)", updated.Lat, updated.Lng)
	}
}

func TestHandleOrgVehicleForm_RendersInlineEditForm(t *testing.T) {
	handler, store := newTestManagementHandler(t)

	vehicle, err := store.OrganizationVehicles().Create(context.Background(), &models.OrganizationVehicle{
		Name:     "Overflow Van",
		Capacity: 8,
	})
	if err != nil {
		t.Fatalf("create organization vehicle: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/org-vehicles/"+int64ToString(vehicle.ID)+"/edit", nil)
	req.Header.Set("HX-Request", "true")
	rr := httptest.NewRecorder()

	handler.HandleOrgVehicleForm(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%q", rr.Code, http.StatusOK, rr.Body.String())
	}

	body := rr.Body.String()
	if !strings.Contains(body, `hx-put="/api/v1/org-vehicles/`+int64ToString(vehicle.ID)+`"`) {
		t.Fatalf("expected edit form to submit to update endpoint, body=%q", body)
	}
	if !strings.Contains(body, "Cancel") {
		t.Fatalf("expected cancel button, body=%q", body)
	}
}

func TestHandleUpdateOrgVehicle_HTMXReturnsUpdatedRow(t *testing.T) {
	handler, store := newTestManagementHandler(t)

	vehicle, err := store.OrganizationVehicles().Create(context.Background(), &models.OrganizationVehicle{
		Name:     "Overflow Van",
		Capacity: 8,
	})
	if err != nil {
		t.Fatalf("create organization vehicle: %v", err)
	}

	form := url.Values{}
	form.Set("name", "Updated Van")
	form.Set("capacity", "10")

	req := httptest.NewRequest(http.MethodPut, "/api/v1/org-vehicles/"+int64ToString(vehicle.ID), strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	rr := httptest.NewRecorder()

	handler.HandleUpdateOrgVehicle(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%q", rr.Code, http.StatusOK, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "Updated Van") {
		t.Fatalf("expected updated row HTML, body=%q", rr.Body.String())
	}
	if !strings.Contains(rr.Header().Get("HX-Trigger"), "updated!") {
		t.Fatalf("expected success toast trigger, got %q", rr.Header().Get("HX-Trigger"))
	}

	updated, err := store.OrganizationVehicles().GetByID(context.Background(), vehicle.ID)
	if err != nil {
		t.Fatalf("get updated organization vehicle: %v", err)
	}
	if updated.Name != "Updated Van" || updated.Capacity != 10 {
		t.Fatalf("updated vehicle = %+v", updated)
	}
}
