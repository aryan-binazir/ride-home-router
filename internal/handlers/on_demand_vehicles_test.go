package handlers

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"ride-home-router/internal/models"
	"strings"
	"testing"
)

func TestCapacityShortageDoesNotRepeatVehicleChoices(t *testing.T) {
	view := CapacityShortageView{ActivityLocation: &models.ActivityLocation{}, EffectiveCapacityByDriver: map[int64]int{}}
	for i := range 100 {
		view.Drivers = append(view.Drivers, models.Driver{ID: int64(i + 1), Name: "Driver", VehicleCapacity: 4})
	}
	for i := range 100 {
		view.OrgVehicles = append(view.OrgVehicles, models.OrganizationVehicle{ID: int64(i + 1), Name: "Unselected fleet van", Capacity: 12})
	}
	var page bytes.Buffer
	if err := loadEmbeddedTemplates(t).Render(&page, "capacity_shortage", view); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(page.String(), "Unselected fleet van") || strings.Count(page.String(), "<option") != 100 {
		t.Fatal("shortage view renders every driver's full vehicle catalog")
	}
}

func TestMobileDriverPickerDoesNotRepeatTheVanCatalog(t *testing.T) {
	view := mobileDriversView{}
	for i := range 500 {
		view.Drivers = append(view.Drivers, models.Driver{ID: int64(i + 1), Name: fmt.Sprintf("Driver %d", i), VehicleCapacity: 4})
	}
	for i := range 150 {
		view.Vehicles = append(view.Vehicles, models.OrganizationVehicle{ID: int64(i + 1), Name: fmt.Sprintf("Catalog Van %d", i), Capacity: 12})
	}
	var page bytes.Buffer
	if err := loadEmbeddedTemplates(t).Render(&page, "mobile/drivers.html", view); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(page.String(), "Catalog Van") || strings.Count(page.String(), "<option") > 500 {
		t.Fatal("mobile picker eagerly rendered the van catalog per driver")
	}
	if !strings.Contains(page.String(), "/api/v1/planner/vehicle-editor") {
		t.Fatal("missing on-demand vehicle action")
	}
}

func TestRestoringVehicleAssignmentsReturnsOnlySelectedCurrentValues(t *testing.T) {
	h, store := newTestPageHandler(t)
	driver, err := store.Drivers().Create(t.Context(), &models.Driver{Name: "Driver", Address: "1 Test St", VehicleCapacity: 4})
	if err != nil {
		t.Fatal(err)
	}
	vehicle, err := store.OrganizationVehicles().Create(t.Context(), &models.OrganizationVehicle{Name: "Chosen van", Capacity: 12})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.OrganizationVehicles().Create(t.Context(), &models.OrganizationVehicle{Name: "Unrelated van", Capacity: 6}); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/planner/vehicle-assignments", strings.NewReader(fmt.Sprintf("driver_ids=%d&org_vehicle_%d=%d", driver.ID, driver.ID, vehicle.ID)))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("HX-Request", "true")
	w := httptest.NewRecorder()
	h.HandleVehicleAssignments(w, r)
	body := w.Body.String()
	if w.Code != 200 || !strings.Contains(body, "Chosen van") || !strings.Contains(body, `data-capacity="12"`) || strings.Contains(body, "Unrelated van") || strings.Count(body, "<option") != 1 {
		t.Fatalf("unexpected restored assignment: status=%d body=%s", w.Code, body)
	}
}

func TestVehicleEditorReturnsBoundedChoicesWithoutChangingPlan(t *testing.T) {
	h, store := newTestPageHandler(t)
	driver, err := store.Drivers().Create(t.Context(), &models.Driver{Name: "Driver One", Address: "1 Test St", VehicleCapacity: 4})
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 60; i++ {
		if _, err := store.OrganizationVehicles().Create(t.Context(), &models.OrganizationVehicle{Name: fmt.Sprintf("Van %02d", i), Capacity: 10}); err != nil {
			t.Fatal(err)
		}
	}
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, fmt.Sprintf("/api/v1/planner/vehicle-editor?driver_id=%d", driver.ID), strings.NewReader(""))
	r.Header.Set("HX-Request", "true")
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.HandleVehicleEditor(w, r)
	body := w.Body.String()
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, body)
	}
	if strings.Count(body, `name="vehicle_id"`) > 26 || strings.Contains(body, "Van 60") || len(body) > 20_000 {
		t.Fatal("vehicle catalog was not bounded before rendering")
	}
	if !strings.Contains(body, "Van 01") || !strings.Contains(body, "Personal vehicle") || !strings.Contains(body, "Next") {
		t.Fatal("missing usable vehicle choices/pagination")
	}
}

func TestSelectedVanPresentationToleratesMissingCurrentVans(t *testing.T) {
	h, store := newTestPageHandler(t)
	driver, err := store.Drivers().Create(t.Context(), &models.Driver{Name: "Selected driver", Address: "1 Test St", VehicleCapacity: 4})
	if err != nil {
		t.Fatal(err)
	}
	van, err := store.OrganizationVehicles().Create(t.Context(), &models.OrganizationVehicle{Name: "Deleted van", Capacity: 12})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.OrganizationVehicles().Delete(t.Context(), van.ID); err != nil {
		t.Fatal(err)
	}
	for _, vehicleID := range []int64{van.ID, van.ID + 1000} {
		for _, endpoint := range []struct {
			path   string
			handle http.HandlerFunc
		}{
			{"/api/v1/planner/drivers", h.HandlePlannerPicker},
			{"/api/v1/planner/vehicle-assignments", h.HandleVehicleAssignments},
		} {
			t.Run(fmt.Sprintf("%s/%d", endpoint.path, vehicleID), func(t *testing.T) {
				r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, endpoint.path, strings.NewReader(fmt.Sprintf("driver_ids=%d&org_vehicle_%d=%d", driver.ID, driver.ID, vehicleID)))
				r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				r.Header.Set("HX-Request", "true")
				w := httptest.NewRecorder()
				endpoint.handle(w, r)
				body := w.Body.String()
				if w.Code != http.StatusOK || !strings.Contains(body, fmt.Sprintf(`name="org_vehicle_%d"`, driver.ID)) || strings.Contains(body, "Deleted van") || strings.Contains(body, `data-capacity="12"`) || w.Header().Get("HX-Trigger") != "" {
					t.Fatalf("missing van presentation: status=%d trigger=%q body=%s", w.Code, w.Header().Get("HX-Trigger"), body)
				}
			})
		}
	}
}

func TestDriverPickerPreservesChosenVanForHiddenSelectedDriver(t *testing.T) {
	h, store := newTestPageHandler(t)
	driver, err := store.Drivers().Create(t.Context(), &models.Driver{Name: "Chosen driver", Address: "1 Test St", VehicleCapacity: 4})
	if err != nil {
		t.Fatal(err)
	}
	van, err := store.OrganizationVehicles().Create(t.Context(), &models.OrganizationVehicle{Name: "Chosen van", Capacity: 12})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.OrganizationVehicles().Create(t.Context(), &models.OrganizationVehicle{Name: "Unrelated van", Capacity: 6}); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/planner/drivers", strings.NewReader(fmt.Sprintf("search=Nobody&driver_ids=%d&org_vehicle_%d=%d", driver.ID, driver.ID, van.ID)))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("HX-Request", "true")
	w := httptest.NewRecorder()
	h.HandlePlannerPicker(w, r)
	body := w.Body.String()
	if w.Code != http.StatusOK || !strings.Contains(body, "Chosen van") || !strings.Contains(body, `data-capacity="12"`) || strings.Contains(body, "Unrelated van") || !strings.Contains(body, fmt.Sprintf(`name="driver_ids" value="%d"`, driver.ID)) {
		t.Fatalf("hidden selected driver: status=%d body=%s", w.Code, body)
	}
}

func TestSelectedVanPresentationPropagatesLookupFailure(t *testing.T) {
	h, store := newTestPageHandler(t)
	driver, err := store.Drivers().Create(t.Context(), &models.Driver{Name: "Driver", Address: "1 Test St", VehicleCapacity: 4})
	if err != nil {
		t.Fatal(err)
	}
	h.DB = testDataStore{DataStore: store, orgVehicleRepo: orgVehicleRepoWithError{OrganizationVehicleRepository: store.OrganizationVehicles(), err: errors.New("van lookup unavailable")}}
	for _, endpoint := range []struct {
		path   string
		handle http.HandlerFunc
	}{
		{"/api/v1/planner/drivers", h.HandlePlannerPicker},
		{"/api/v1/planner/vehicle-assignments", h.HandleVehicleAssignments},
	} {
		r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, endpoint.path, strings.NewReader(fmt.Sprintf("driver_ids=%d&org_vehicle_%d=1", driver.ID, driver.ID)))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("HX-Request", "true")
		w := httptest.NewRecorder()
		endpoint.handle(w, r)
		if w.Code != http.StatusInternalServerError || !strings.Contains(w.Header().Get("HX-Trigger"), messageGenericInternalError) {
			t.Fatalf("lookup failure %s: status=%d trigger=%q", endpoint.path, w.Code, w.Header().Get("HX-Trigger"))
		}
	}
}
