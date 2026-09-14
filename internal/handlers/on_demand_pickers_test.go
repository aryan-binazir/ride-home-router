package handlers

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"ride-home-router/internal/models"
	"ride-home-router/internal/plandraft"
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

func TestRosterManagementPagesAreBounded(t *testing.T) {
	h, store := newTestPageHandler(t)
	for i := 1; i <= 60; i++ {
		if _, err := store.Drivers().Create(t.Context(), &models.Driver{Name: fmt.Sprintf("Driver %03d", i), Address: "1 Test St", VehicleCapacity: 4}); err != nil {
			t.Fatal(err)
		}
	}
	w := httptest.NewRecorder()
	h.HandleDriversPage(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/drivers", nil))
	if strings.Contains(w.Body.String(), "Driver 060") || !strings.Contains(w.Body.String(), "Next") {
		t.Fatal("roster management loads the entire roster")
	}
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/drivers?offset=50", nil)
	r.Header.Set("HX-Request", "true")
	w = httptest.NewRecorder()
	h.HandleListDrivers(w, r)
	if !strings.Contains(w.Body.String(), "Driver 060") || strings.Count(w.Body.String(), "data-bulk-row") != 10 {
		t.Fatal("roster next page was not bounded/reachable")
	}
}

func TestMobilePeopleAndLocationPagesAreBounded(t *testing.T) {
	h, store := newTestPageHandler(t)
	h.PlanDraft = plandraft.NewStore()
	t.Cleanup(h.PlanDraft.Close)
	for i := 1; i <= 60; i++ {
		if _, err := store.Drivers().Create(t.Context(), &models.Driver{Name: fmt.Sprintf("Driver %03d", i), Address: "1 Test St", VehicleCapacity: 4}); err != nil {
			t.Fatal(err)
		}
		if _, err := store.ActivityLocations().Create(t.Context(), &models.ActivityLocation{Name: fmt.Sprintf("Location %03d", i), Address: "1 Test St"}); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		url, absent string
		page        http.HandlerFunc
	}{{"/m/people", "Driver 060", h.HandleMobilePeople}, {"/m/plan/location", "Location 060", h.HandleMobileLocation}} {
		w := httptest.NewRecorder()
		tc.page(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, tc.url, nil))
		if strings.Contains(w.Body.String(), tc.absent) || !strings.Contains(w.Body.String(), "Next") {
			t.Fatalf("%s renders an unbounded list", tc.url)
		}
	}
}

func TestRosterDoesNotPreloadBulkLabelMenus(t *testing.T) {
	h, store := newTestPageHandler(t)
	if _, err := store.Labels().Create(t.Context(), &models.Label{Name: "Unselected label"}); err != nil {
		t.Fatal(err)
	}
	for _, page := range []http.HandlerFunc{h.HandleParticipantsPage, h.HandleDriversPage} {
		w := httptest.NewRecorder()
		page(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/participants", nil))
		if strings.Contains(w.Body.String(), "Unselected label") {
			t.Fatal("roster page preloads unused label menus")
		}
		if !strings.Contains(w.Body.String(), "/api/v1/planner/label-editor") {
			t.Fatal("missing label editor action")
		}
	}
}

func TestInitialPlannerDoesNotRenderLocationCatalog(t *testing.T) {
	h, store := newTestPageHandler(t)
	for i := range 60 {
		if _, err := store.ActivityLocations().Create(t.Context(), &models.ActivityLocation{Name: fmt.Sprintf("Unused location %03d", i), Address: "1 Test St"}); err != nil {
			t.Fatal(err)
		}
	}
	w := httptest.NewRecorder()
	h.HandleIndexPage(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))
	if strings.Contains(w.Body.String(), "Unused location") {
		t.Fatal("initial planner preloads location choices")
	}
	if !strings.Contains(w.Body.String(), "Choose location") {
		t.Fatal("missing on-demand location action")
	}
}

func TestInitialPlannerRendersBoundedRosterPages(t *testing.T) {
	h, store := newTestPageHandler(t)
	for i := 1; i <= 60; i++ {
		if _, err := store.Drivers().Create(t.Context(), &models.Driver{Name: fmt.Sprintf("Driver %03d", i), Address: "1 Test St", VehicleCapacity: 4}); err != nil {
			t.Fatal(err)
		}
	}
	w := httptest.NewRecorder()
	h.HandleIndexPage(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))
	body := w.Body.String()
	if w.Code != 200 || strings.Count(body, `name="driver_ids"`) != 50 || strings.Contains(body, "Driver 060") {
		t.Fatalf("initial planner contains an unbounded roster: status=%d rows=%d", w.Code, strings.Count(body, `name="driver_ids"`))
	}
	if !strings.Contains(body, "Next") {
		t.Fatal("missing roster pagination")
	}
}
