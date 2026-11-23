package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"ride-home-router/internal/models"
	"strings"
	"testing"
)

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

func TestVehicleEditorChoosesOnlyAvailableVans(t *testing.T) {
	h, store := newTestPageHandler(t)
	driver, err := store.Drivers().Create(t.Context(), &models.Driver{Name: "Driver", Address: "1 Test St", VehicleCapacity: 4})
	if err != nil {
		t.Fatal(err)
	}
	van, err := store.OrganizationVehicles().Create(t.Context(), &models.OrganizationVehicle{Name: "Available van", Capacity: 8})
	if err != nil {
		t.Fatal(err)
	}
	taken, err := store.OrganizationVehicles().Create(t.Context(), &models.OrganizationVehicle{Name: "Assigned van", Capacity: 12})
	if err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/v1/planner/vehicle-editor?driver_id=%d&shortage=1", driver.ID)
	assignment := fmt.Sprintf("org_vehicle_%d", driver.ID+1)
	form := url.Values{assignment: {fmt.Sprint(taken.ID)}}

	w := postForm(t, path, form, h.HandleVehicleEditor)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Available van") || strings.Contains(w.Body.String(), "Assigned van") {
		t.Fatalf("choices: %d %s", w.Code, w.Body.String())
	}
	form.Set("choose", "1")
	form.Set("vehicle_id", fmt.Sprint(taken.ID))
	w = postForm(t, path, form, h.HandleVehicleEditor)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "Choose an available vehicle.") {
		t.Fatalf("taken van: %d %s", w.Code, w.Body.String())
	}
	form.Set("vehicle_id", fmt.Sprint(van.ID))
	w = postForm(t, path, form, h.HandleVehicleEditor)
	if w.Code != http.StatusOK || w.Header().Get("HX-Retarget") != fmt.Sprintf("#shortage-van-control-%d", driver.ID) ||
		!strings.Contains(w.Body.String(), "Available van") || !strings.Contains(w.Body.String(), `data-capacity="8"`) {
		t.Fatalf("selected van: %d %v %s", w.Code, w.Header(), w.Body.String())
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

func TestVehicleAssignmentEndpointsRejectInvalidAssignments(t *testing.T) {
	h, _ := newTestPageHandler(t)
	for _, endpoint := range []struct {
		path   string
		handle http.HandlerFunc
	}{
		{"/api/v1/planner/drivers", h.HandlePlannerPicker},
		{"/api/v1/planner/vehicle-assignments", h.HandleVehicleAssignments},
	} {
		for _, test := range []struct {
			name, key, value, message string
		}{
			{"invalid driver id", "org_vehicle_bad", "9", "Choose a valid van assignment."},
			{"invalid van id", "org_vehicle_1", "bad", "Choose a valid van assignment."},
			{"unselected driver", "org_vehicle_3", "9", "Select a driver before assigning a van."},
			{"duplicate van", "org_vehicle_2", "9", "Assign each van to only one driver."},
		} {
			t.Run(endpoint.path+"/"+test.name, func(t *testing.T) {
				form := url.Values{"driver_ids": {"1", "2"}, "org_vehicle_1": {"9"}}
				form.Set(test.key, test.value)
				w := postForm(t, endpoint.path, form, endpoint.handle)
				if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), test.message) {
					t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
				}
			})
		}
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
