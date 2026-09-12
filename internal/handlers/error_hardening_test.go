package handlers

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"ride-home-router/internal/database"
	"ride-home-router/internal/models"
	"ride-home-router/internal/plandraft"
	"ride-home-router/internal/routesession"
	"strings"
	"testing"
	"time"
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
	for _, target := range []string{"/m/plan/drivers", "https://evil.test/m/plan/drivers", "//evil.test/m/plan/drivers", "/m/\\evil.test", "/m/../outside"} {
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
	r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/imports", unreadImportBody{t})
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

type saveOutageWorkflows struct {
	database.WorkflowRepository
	failed  bool
	failure error
}

func (s *saveOutageWorkflows) Transact(context.Context, string, string, time.Duration, func(*database.WorkflowRecord, database.WorkflowWrites) error) error {
	s.failed = true
	if s.failure != nil {
		return s.failure
	}
	return errors.New(internalSentinel)
}

func (s *saveOutageWorkflows) Load(ctx context.Context, kind, id string, ttl time.Duration) (database.WorkflowRecord, error) {
	if s.failed {
		return database.WorkflowRecord{}, errors.New(internalSentinel)
	}
	return s.WorkflowRepository.Load(ctx, kind, id, ttl)
}

func TestMobileSaveOutagePreservesInputWithoutReloading(t *testing.T) {
	h, store := newTestManagementHandler(t)
	records := &saveOutageWorkflows{WorkflowRepository: store.Workflows()}
	h.RouteSession = routesession.NewPersistentStore(nil, records)
	h.PlanDraft = plandraft.NewStore()
	t.Cleanup(h.PlanDraft.Close)
	session, err := h.RouteSession.CreateContext(t.Context(), routesession.CreateInput{Mode: models.RouteModeDropoff, Routes: []models.CalculatedRoute{{Driver: &models.Driver{ID: 1, Name: "Driver", VehicleCapacity: 4}, EffectiveCapacity: 4, Mode: models.RouteModeDropoff, Stops: []models.RouteStop{{Participant: &models.Participant{ID: 1, Name: "Rider"}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	id := h.PlanDraft.NewID()
	h.PlanDraft.Update(id, func(d *plandraft.Draft) { d.RouteSessionID = session.ID })
	w := postMobileForm(t, mobileTestCookie(id), "/m/routes/save", url.Values{"session_id": {session.ID}, "event_date": {"2026-10-11"}, "notes": {"Keep outage notes"}}, h.HandleMobileSave)
	if w.Code != 500 || !strings.Contains(w.Body.String(), `value="Keep outage notes"`) || !strings.Contains(w.Body.String(), `value="2026-10-11"`) {
		t.Fatalf("outage lost input: %d %s", w.Code, w.Body.String())
	}
	assertSafeFailure(t, w)
}

func TestMobileExpiredSaveShowsRecovery(t *testing.T) {
	h, _ := newTestManagementHandler(t)
	h.PlanDraft = plandraft.NewStore()
	t.Cleanup(h.PlanDraft.Close)
	id := h.PlanDraft.NewID()
	h.PlanDraft.Update(id, func(d *plandraft.Draft) { d.RouteSessionID = "expired" })
	w := postMobileForm(t, mobileTestCookie(id), "/m/routes/save", url.Values{"session_id": {"expired"}, "event_date": {"2026-10-11"}}, h.HandleMobileSave)
	if w.Code != 409 || !strings.Contains(w.Body.String(), messageRoutePlanExpired) {
		t.Fatalf("expired save: %d %s", w.Code, w.Body.String())
	}
}

type unreadImportBody struct{ t *testing.T }

func (b unreadImportBody) Read([]byte) (int, error) {
	b.t.Fatal("rejected import body was read")
	return 0, errors.New("unexpected read")
}

func TestMobileSaveKnownFailuresKeepActionableStatus(t *testing.T) {
	for _, tc := range []struct {
		name    string
		err     error
		status  int
		message string
	}{{"unbalanced", routesession.ErrUnbalanced, 400, messageRoutesMustBeBalancedBeforeSaving}, {"conflict", database.ErrWorkflowConflict, 409, "This route plan changed. Review the current routes and try again."}} {
		t.Run(tc.name, func(t *testing.T) {
			h, store := newTestManagementHandler(t)
			records := &saveOutageWorkflows{WorkflowRepository: store.Workflows(), failure: tc.err}
			h.RouteSession = routesession.NewPersistentStore(nil, records)
			h.PlanDraft = plandraft.NewStore()
			t.Cleanup(h.PlanDraft.Close)
			session, err := h.RouteSession.CreateContext(t.Context(), routesession.CreateInput{Mode: models.RouteModeDropoff})
			if err != nil {
				t.Fatal(err)
			}
			id := h.PlanDraft.NewID()
			h.PlanDraft.Update(id, func(d *plandraft.Draft) { d.RouteSessionID = session.ID })
			w := postMobileForm(t, mobileTestCookie(id), "/m/routes/save", url.Values{"session_id": {session.ID}, "event_date": {"2026-10-11"}, "notes": {"Keep notes"}}, h.HandleMobileSave)
			if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.message) || !strings.Contains(w.Body.String(), `value="Keep notes"`) {
				t.Fatalf("save: %d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestMobileLabelsOutageRetryRetainsMemberships(t *testing.T) {
	h, store := newTestManagementHandler(t)
	label, err := store.Labels().Create(t.Context(), &models.Label{Name: "Keep label"})
	if err != nil {
		t.Fatal(err)
	}
	person, err := store.Participants().CreateWithLabels(t.Context(), &models.Participant{Name: "Rider", Address: "Same address"}, []int64{label.ID})
	if err != nil {
		t.Fatal(err)
	}
	h.DB = rosterRefreshFailureStore{DataStore: store, participants: store.Participants(), drivers: store.Drivers(), labels: failedRosterLabels{store.Labels()}}
	path := fmt.Sprintf("/m/people/participants/%d/edit", person.ID)
	values := url.Values{"name": {"Rider"}, "address": {"Same address"}, "label_ids": {fmt.Sprint(label.ID)}}
	failed := postMobileForm(t, nil, path, values, h.HandleMobileParticipantForm)
	if failed.Code != 500 {
		t.Fatalf("failure: %d", failed.Code)
	}
	match := regexp.MustCompile(`<form[^>]*action="([^"]+)"`).FindStringSubmatch(failed.Body.String())
	if len(match) != 2 {
		t.Fatal("missing retry form")
	}
	h.DB = store
	values.Del("label_ids") // Failed lookup rendered no label controls.
	retry := postMobileForm(t, nil, html.UnescapeString(match[1]), values, h.HandleMobileParticipantForm)
	if retry.Code != 303 {
		t.Fatalf("retry: %d %s", retry.Code, retry.Body.String())
	}
	labels, err := store.Labels().ListLabelsForParticipant(t.Context(), person.ID)
	if err != nil || len(labels) != 1 || labels[0].ID != label.ID {
		t.Fatalf("labels lost: %v %v", labels, err)
	}
}
