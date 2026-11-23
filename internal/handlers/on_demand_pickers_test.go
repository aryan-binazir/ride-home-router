package handlers

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"ride-home-router/internal/models"
	"strings"
	"testing"
)

func TestPlannerDriverPagePreservesOffPageSelectionInCompactForm(t *testing.T) {
	h, store := newTestPageHandler(t)
	var last int64
	for i := 1; i <= 70; i++ {
		driver, err := store.Drivers().Create(t.Context(), &models.Driver{Name: fmt.Sprintf("Driver %03d", i), Address: "1 Test St", VehicleCapacity: 4})
		if err != nil {
			t.Fatal(err)
		}
		last = driver.ID
	}
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/planner/drivers", strings.NewReader(fmt.Sprintf("driver_ids=%d", last)))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("HX-Request", "true")
	w := httptest.NewRecorder()
	h.HandlePlannerPicker(w, r)
	body := w.Body.String()
	if w.Code != 200 || strings.Contains(body, "<html") || strings.Contains(body, "Driver 070") || strings.Count(body, `class="select-row"`) != 50 || strings.Count(body, `name="driver_ids"`) != 51 {
		t.Fatalf("unexpected bounded selection response: status=%d bytes=%d", w.Code, len(body))
	}
	if !strings.Contains(body, fmt.Sprintf(`value="%d" data-capacity="4" checked`, last)) {
		t.Fatal("off-page driver selection/capacity was lost")
	}
}

func TestPickerEndpointsEnforceSelectionLimitAfterDeduplication(t *testing.T) {
	h, _ := newTestPageHandler(t)
	ids := make([]string, 501)
	for i := range ids {
		ids[i] = fmt.Sprint(i + 1)
	}
	for _, endpoint := range []struct {
		path, field string
		handle      http.HandlerFunc
	}{
		{"/api/v1/planner/participants", "participant_ids", h.HandlePlannerPicker},
		{"/api/v1/planner/drivers", "driver_ids", h.HandlePlannerPicker},
		{"/api/v1/planner/vehicle-assignments", "driver_ids", h.HandleVehicleAssignments},
	} {
		t.Run(endpoint.path, func(t *testing.T) {
			for _, test := range []struct {
				name   string
				values []string
				status int
			}{
				{"too many distinct ids", ids, http.StatusBadRequest},
				{"duplicates and invalid ids do not count", append(append([]string{}, ids[:500]...), "1", "invalid", "0"), http.StatusOK},
			} {
				t.Run(test.name, func(t *testing.T) {
					w := postForm(t, endpoint.path, url.Values{endpoint.field: test.values}, func(w http.ResponseWriter, r *http.Request) {
						r.Header.Set("HX-Request", "true")
						endpoint.handle(w, r)
					})
					if w.Code != test.status || test.status == http.StatusBadRequest && !strings.Contains(w.Body.String(), "Choose no more than 500 people.") {
						t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
					}
				})
			}
		})
	}
}

func TestPickerSelectAllEnforcesSelectionLimit(t *testing.T) {
	h, store := newTestPageHandler(t)
	for i := range 501 {
		name := fmt.Sprintf("Person %03d", i)
		if _, err := store.Participants().Create(t.Context(), &models.Participant{Name: name, Address: "1 Test St"}); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Drivers().Create(t.Context(), &models.Driver{Name: name, Address: "1 Test St", VehicleCapacity: 4}); err != nil {
			t.Fatal(err)
		}
	}
	for _, kind := range []string{"participants", "drivers"} {
		w := postForm(t, "/api/v1/planner/"+kind, url.Values{"select": {"all"}}, h.HandlePlannerPicker)
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "Choose no more than 500 people.") {
			t.Fatalf("%s: status=%d body=%s", kind, w.Code, w.Body.String())
		}
	}
}

func TestLocationEditorPaginatesAndChoosesTheRequestedLocation(t *testing.T) {
	h, store := newTestPageHandler(t)
	var last int64
	for i := 1; i <= 26; i++ {
		location, err := store.ActivityLocations().Create(t.Context(), &models.ActivityLocation{Name: fmt.Sprintf("Venue %02d", i), Address: "1 Test St"})
		if err != nil {
			t.Fatal(err)
		}
		last = location.ID
	}
	const path = "/api/v1/planner/location-editor"
	for _, test := range []struct {
		query, present, absent string
	}{
		{"", "Venue 01", "Venue 26"},
		{"?offset=25", "Venue 26", "Venue 01"},
	} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path+test.query, nil)
		req.Header.Set("HX-Request", "true")
		w := httptest.NewRecorder()
		h.HandleLocationEditor(w, req)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), test.present) || strings.Contains(w.Body.String(), test.absent) {
			t.Fatalf("location page %q: %d %s", test.query, w.Code, w.Body.String())
		}
	}
	w := postForm(t, path, url.Values{"location_id": {fmt.Sprint(last)}}, h.HandleLocationEditor)
	if w.Code != http.StatusOK || w.Header().Get("HX-Retarget") != "#event-activity-location-select" || !strings.Contains(w.Body.String(), "Venue 26") {
		t.Fatalf("location choice: %d %v %s", w.Code, w.Header(), w.Body.String())
	}
}
