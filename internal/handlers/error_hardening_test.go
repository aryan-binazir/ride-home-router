package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"ride-home-router/internal/database"
	"ride-home-router/internal/models"
	"ride-home-router/internal/plandraft"
	"ride-home-router/internal/routesession"
	"strings"
	"testing"
)

const internalSentinel = `ERROR: relation "x" does not exist (SQLSTATE 42P01)`

type errorStore struct{ database.DataStore }

func (s errorStore) Settings() database.SettingsRepository { return errorSettings{} }
func (s errorStore) Events() database.EventRepository      { return errorEvents{} }

type errorSettings struct{ database.SettingsRepository }

func (errorSettings) Get(context.Context) (*models.Settings, error) {
	return nil, errors.New(internalSentinel)
}

type errorEvents struct{ database.EventRepository }

func (errorEvents) Create(context.Context, *models.Event, []models.EventRoute, *models.EventSummary) (*models.Event, error) {
	return nil, errors.New(internalSentinel)
}

func assertSafeFailure(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	if strings.Contains(w.Body.String(), "SQLSTATE") || strings.Contains(w.Header().Get("HX-Trigger"), "SQLSTATE") || strings.Contains(w.Header().Get("Location"), "SQLSTATE") {
		t.Fatalf("leaked internal error: %s %s", w.Header(), w.Body.String())
	}
}

func TestSafeErrorResponses(t *testing.T) {
	h := &Handler{DB: errorStore{}, Renderer: loadEmbeddedTemplates(t), Geocoder: stubGeocoder{err: errors.New(internalSentinel)}}
	for _, tc := range []struct {
		name string
		fn   http.HandlerFunc
	}{
		{"settings", h.HandleUpdateSettings},
		{"desktop calculate", func(w http.ResponseWriter, r *http.Request) {
			h.handleRouteCalculationError(w, r, errors.New(internalSentinel))
		}},
		{"geocoding", func(w http.ResponseWriter, r *http.Request) {
			h.handleGeocodingError(w, r, errors.New(internalSentinel))
		}},
		{"location", h.HandleCreateActivityLocation},
		{"address search", h.HandleAddressSearch},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/test?address=sample", strings.NewReader(url.Values{"name": {"Name"}, "address": {"Address"}}.Encode()))
			r.Header.Set("HX-Request", "true")
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			w := httptest.NewRecorder()
			tc.fn(w, r)
			assertSafeFailure(t, w)
		})
	}
}

func TestEmptyParticipantAddressShowsValidationToast(t *testing.T) {
	h := &Handler{}
	r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/participants", strings.NewReader("name=Name&address="))
	r.Header.Set("HX-Request", "true")
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	h.HandleCreateParticipant(w, r)
	if w.Code != 400 || !strings.Contains(w.Header().Get("HX-Trigger"), "Enter a name and address.") {
		t.Fatalf("response: %d %s", w.Code, w.Header())
	}
}

func TestMobileSaveFailurePreservesDateAndNotes(t *testing.T) {
	h, _ := newTestManagementHandler(t)
	h.PlanDraft = plandraft.NewStore()
	t.Cleanup(h.PlanDraft.Close)
	session := h.RouteSession.Create(routesession.CreateInput{Mode: models.RouteModeDropoff, Routes: []models.CalculatedRoute{{Driver: &models.Driver{ID: 1, Name: "Driver", VehicleCapacity: 4}, EffectiveCapacity: 4, Mode: models.RouteModeDropoff, Stops: []models.RouteStop{{Participant: &models.Participant{ID: 1, Name: "Rider"}}}}}})
	id := h.PlanDraft.NewID()
	h.PlanDraft.Update(id, func(d *plandraft.Draft) { d.RouteSessionID = session.ID })
	h.DB = errorStore{h.DB}
	w := postMobileForm(t, mobileTestCookie(id), "/m/routes/save", url.Values{"session_id": {session.ID}, "event_date": {"2026-10-11"}, "notes": {"Keep these notes"}}, h.HandleMobileSave)
	if w.Code != 500 || !strings.Contains(w.Body.String(), `value="2026-10-11"`) || !strings.Contains(w.Body.String(), `value="Keep these notes"`) || w.Header().Get("Location") != "" {
		t.Fatalf("response: %d %s %s", w.Code, w.Header(), w.Body.String())
	}
	assertSafeFailure(t, w)
}

func TestMobileReturnPathPreservedAndRestricted(t *testing.T) {
	for _, target := range []string{"/m/plan/drivers", "https://evil.test/m/plan/drivers", "//evil.test/m/plan/drivers", "/m/\\evil.test"} {
		r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/m/people/drivers/new?return="+url.QueryEscape(target), nil)
		want := "/m/people"
		if target == "/m/plan/drivers" {
			want = target
		}
		if got := mobileReturnPath(r, "/m/people"); got != want {
			t.Fatalf("%q: %q", target, got)
		}
		if want == target && !strings.Contains(mobileFormAction(r), "return=") {
			t.Fatal("return lost from form action")
		}
	}
}

func TestImportAdmissionRejectsBeforeReadingBody(t *testing.T) {
	h, _ := newImportTestHandler(t, &importTestGeocoder{})
	for range cap(importAdmission) {
		importAdmission <- struct{}{}
	}
	defer func() {
		for range cap(importAdmission) {
			<-importAdmission
		}
	}()
	r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/imports", nil)
	r.Header.Set("HX-Request", "true")
	w := httptest.NewRecorder()
	h.HandleCreateImport(w, r)
	if w.Code != 429 || !strings.Contains(w.Header().Get("HX-Trigger"), "showToast") {
		t.Fatalf("response: %d %s %s", w.Code, w.Header(), w.Body.String())
	}
}

func TestMobileSaveRetryReturnsSameEvent(t *testing.T) {
	h, _ := newTestManagementHandler(t)
	h.PlanDraft = plandraft.NewStore()
	t.Cleanup(h.PlanDraft.Close)
	session := h.RouteSession.Create(routesession.CreateInput{Mode: models.RouteModeDropoff, Routes: []models.CalculatedRoute{{Driver: &models.Driver{ID: 1, Name: "Driver", VehicleCapacity: 4}, EffectiveCapacity: 4, Mode: models.RouteModeDropoff, Stops: []models.RouteStop{{Participant: &models.Participant{ID: 1, Name: "Rider"}}}}}})
	id := h.PlanDraft.NewID()
	h.PlanDraft.Update(id, func(d *plandraft.Draft) { d.RouteSessionID = session.ID })
	form := url.Values{"session_id": {session.ID}, "event_date": {"2026-10-11"}}
	first := postMobileForm(t, mobileTestCookie(id), "/m/routes/save", form, h.HandleMobileSave)
	second := postMobileForm(t, mobileTestCookie(id), "/m/routes/save", form, h.HandleMobileSave)
	if first.Code != 303 || !strings.HasPrefix(first.Header().Get("Location"), "/m/history/") || second.Code != 303 || second.Header().Get("Location") != first.Header().Get("Location") {
		t.Fatalf("save/retry: %d %s, %d %s", first.Code, first.Header(), second.Code, second.Header())
	}
}

func TestMobilePersonDatabaseFailurePreservesInput(t *testing.T) {
	h, store := newTestManagementHandler(t)
	h.DB = rosterRefreshFailureStore{DataStore: store, participants: alwaysFailParticipantCreate{store.Participants()}, drivers: store.Drivers(), labels: store.Labels()}
	w := postMobileForm(t, nil, "/m/people/participants/new?return=/m/plan/riders", url.Values{"name": {"Keep my name"}, "address": {"Keep my address"}}, h.HandleMobileParticipantForm)
	if w.Code != 500 || !strings.Contains(w.Body.String(), `value="Keep my name"`) || !strings.Contains(w.Body.String(), `value="Keep my address"`) || !strings.Contains(w.Body.String(), "return=") {
		t.Fatalf("response: %d %s", w.Code, w.Body.String())
	}
	assertSafeFailure(t, w)
}

type alwaysFailParticipantCreate struct{ database.ParticipantRepository }

func (alwaysFailParticipantCreate) CreateWithLabels(context.Context, *models.Participant, []int64) (*models.Participant, error) {
	return nil, errors.New(internalSentinel)
}

func TestMobileHandoffIncludesModeAndTime(t *testing.T) {
	for _, mode := range []models.RouteMode{models.RouteModePickup, models.RouteModeDropoff} {
		snapshot := routesession.Snapshot{Mode: mode, RouteTime: "18:15", ActivityLocation: &models.ActivityLocation{Name: "Grace Center"}}
		expected := "Pickup: arrive at Grace Center by 6:15 PM\n\n"
		if mode == models.RouteModeDropoff {
			expected = "Dropoff: leave Grace Center at 6:15 PM\n\n"
		}
		for _, parents := range []bool{true, false} {
			if text := formatMobileHandoff(snapshot, models.CalculatedRoute{}, parents); !strings.HasPrefix(text, expected) {
				t.Fatalf("handoff: %q", text)
			}
		}
	}
}
