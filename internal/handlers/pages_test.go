package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"ride-home-router/internal/access"
	"ride-home-router/internal/access/accesstest"
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
	if strings.Contains(body, "How Vans Work") || !strings.Contains(body, "Add shared vans and assign one to a selected driver when planning an event.") {
		t.Fatalf("expected concise van guidance, body=%q", body)
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
	if strings.Contains(body, "now live") || strings.Contains(body, "alert alert-info") {
		t.Fatalf("expected Settings page to omit migration copy, body=%q", body)
	}
}

func TestHandleHistoryPage_RendersKeyboardOperableEventToggle(t *testing.T) {
	handler, store := newTestPageHandler(t)
	event := createTestEvent(t, store, "2026-08-29", "Accessible history")

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/history", nil)
	rr := httptest.NewRecorder()
	handler.HandleHistoryPage(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	body := rr.Body.String()
	for _, want := range []string{
		`class="event-toggle"`,
		`aria-expanded="false"`,
		`aria-controls="event-detail-` + int64ToString(event.ID) + `"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("history page missing %q, body=%q", want, body)
		}
	}
	if strings.Contains(body, `onclick="toggleEventDetail(this,`) {
		t.Fatalf("history event row remains pointer-only, body=%q", body)
	}
}

func TestSharedLayoutOffersHeaderSignOut(t *testing.T) {
	handler, _ := newTestPageHandler(t)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/settings", nil)
	rr := httptest.NewRecorder()
	handler.HandleSettingsPage(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	body := rr.Body.String()
	headerEnd := strings.Index(body, "</header>")
	signOut := strings.Index(body, "data-sign-out")
	if headerEnd < 0 || signOut < 0 || signOut > headerEnd || strings.Contains(body, ">Mobile site</a>") {
		t.Fatal("shared layout must place sign-out in the header and omit the retired mobile link")
	}
}

func TestHandleSettingsPage_RendersSMEEmailControl(t *testing.T) {
	handler, store := newTestPageHandler(t)
	if err := store.Settings().Update(context.Background(), &models.Settings{UseMiles: true, SMEEmail: "sme@example.com"}); err != nil {
		t.Fatalf("update settings: %v", err)
	}

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/settings", nil)
	rr := httptest.NewRecorder()
	serveSettingsAsAdmin(t, store, handler.HandleSettingsPage, rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusOK)
	}
	body := rr.Body.String()
	for _, want := range []string{
		`type="email"`,
		`name="sme_email"`,
		`value="sme@example.com"`,
		`Reviewer email`,
		`Route edits saved by this person help improve route suggestions. Leave blank to turn this off.`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("settings page missing %q, body=%q", want, body)
		}
	}
}

func TestHandleUpdateSettings_TrimsAndRoundTripsSMEEmail(t *testing.T) {
	handler, store := newTestPageHandler(t)
	payload, err := json.Marshal(map[string]any{"use_miles": true, "sme_email": "  SME@Example.com  "})
	if err != nil {
		t.Fatalf("marshal settings: %v", err)
	}
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPut, "/api/v1/settings", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	serveSettingsAsAdmin(t, store, handler.HandleUpdateSettings, rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%q", rr.Code, http.StatusOK, rr.Body.String())
	}
	settings, err := store.Settings().Get(context.Background())
	if err != nil {
		t.Fatalf("get settings: %v", err)
	}
	if settings.SMEEmail != "SME@Example.com" {
		t.Fatalf("SMEEmail = %q, want trimmed value", settings.SMEEmail)
	}

	var response models.Settings
	if err := json.NewDecoder(rr.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.SMEEmail != settings.SMEEmail {
		t.Fatalf("response SMEEmail = %q, stored = %q", response.SMEEmail, settings.SMEEmail)
	}
}

func TestHandleUpdateSettings_BlankFormSMEEmailDisablesCapture(t *testing.T) {
	handler, store := newTestPageHandler(t)
	if err := store.Settings().Update(context.Background(), &models.Settings{UseMiles: true, SMEEmail: "sme@example.com"}); err != nil {
		t.Fatalf("seed settings: %v", err)
	}

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPut, "/api/v1/settings", strings.NewReader("use_miles=on&sme_email=++"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	rr := httptest.NewRecorder()
	serveSettingsAsAdmin(t, store, handler.HandleUpdateSettings, rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d body=%q", rr.Code, http.StatusNoContent, rr.Body.String())
	}
	settings, err := store.Settings().Get(context.Background())
	if err != nil {
		t.Fatalf("get settings: %v", err)
	}
	if settings.SMEEmail != "" {
		t.Fatalf("SMEEmail = %q, want blank", settings.SMEEmail)
	}
}

func TestHandleUpdateSettings_RejectsInvalidFormSMEEmail(t *testing.T) {
	handler, store := newTestPageHandler(t)
	if err := store.Settings().Update(context.Background(), &models.Settings{UseMiles: true, SMEEmail: "sme@example.com"}); err != nil {
		t.Fatalf("seed settings: %v", err)
	}

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPut, "/api/v1/settings", strings.NewReader("use_miles=on&sme_email=not-an-email"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	rr := httptest.NewRecorder()
	serveSettingsAsAdmin(t, store, handler.HandleUpdateSettings, rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d body=%q", rr.Code, http.StatusBadRequest, rr.Body.String())
	}
	if got := rr.Header().Get("HX-Trigger"); !strings.Contains(got, "Please enter a valid SME email address.") {
		t.Fatalf("HX-Trigger = %q, want invalid email toast", got)
	}
	settings, err := store.Settings().Get(context.Background())
	if err != nil {
		t.Fatalf("get settings: %v", err)
	}
	if settings.SMEEmail != "sme@example.com" {
		t.Fatalf("SMEEmail = %q, want previous value preserved", settings.SMEEmail)
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
	if strings.Contains(body, `id="event-org-vehicles"`) || strings.Contains(body, "Overflow Van") {
		t.Fatal("initial planner must not preload the vehicle catalog")
	}
	if !strings.Contains(body, "/api/v1/planner/vehicle-editor") {
		t.Fatal("missing on-demand vehicle editor")
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

func serveSettingsAsAdmin(t *testing.T, store access.Store, handler http.HandlerFunc, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	fixture := accesstest.New(t)
	token := fixture.Admin()
	gate, err := access.New(fixture.Config(), store)
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Authorization", "Bearer "+token)
	gate.Protect(handler).ServeHTTP(w, r)
}

func TestReviewerSettingsNonAdmin(t *testing.T) {
	handler, store := newTestPageHandler(t)
	if err := store.Settings().Update(t.Context(), &models.Settings{UseMiles: true, SMEEmail: "reviewer@example.test"}); err != nil {
		t.Fatal(err)
	}
	page := httptest.NewRecorder()
	handler.HandleSettingsPage(page, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/settings", nil))
	for _, hidden := range []string{"sme_email", "Reviewer email", "reviewer@example.test"} {
		if strings.Contains(page.Body.String(), hidden) {
			t.Errorf("non-admin page exposes %q", hidden)
		}
	}
	for _, tc := range []struct {
		body, content string
		htmx          bool
	}{
		{`{"sme_email":"attacker@example.test"}`, "application/json", false},
		{"sme_email=", "application/x-www-form-urlencoded", true},
	} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPut, "/api/v1/settings", strings.NewReader(tc.body))
		req.Header.Set("Content-Type", tc.content)
		if tc.htmx {
			req.Header.Set("HX-Request", "true")
		}
		response := httptest.NewRecorder()
		handler.HandleUpdateSettings(response, req)
		if response.Code != http.StatusForbidden {
			t.Fatalf("non-admin mutation status %d", response.Code)
		}
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPut, "/api/v1/settings", strings.NewReader(`{"use_miles":false}`))
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.HandleUpdateSettings(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("ordinary preferences status %d", response.Code)
	}
	if strings.Contains(response.Body.String(), "reviewer@example.test") {
		t.Fatal("update exposes reviewer email")
	}
	settings, err := store.Settings().Get(t.Context())
	if err != nil || settings.SMEEmail != "reviewer@example.test" || settings.UseMiles {
		t.Fatalf("settings not preserved: %#v, %v", settings, err)
	}
	response = httptest.NewRecorder()
	handler.HandleGetSettings(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/settings", nil))
	if strings.Contains(response.Body.String(), "reviewer@example.test") {
		t.Fatal("GET exposes reviewer email")
	}
}
