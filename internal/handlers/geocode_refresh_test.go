package handlers

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"ride-home-router/internal/geocoding"
	"ride-home-router/internal/models"
	"ride-home-router/internal/plandraft"
	"ride-home-router/internal/routesession"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type refreshGeocoder struct {
	geocoding.Geocoder
	calls  atomic.Int32
	err    error
	before func(context.Context) error
}

func (g *refreshGeocoder) GeocodeWithRetry(ctx context.Context, _ string, _ int) (*geocoding.GeocodingResult, error) {
	g.calls.Add(1)
	if g.before != nil {
		if err := g.before(ctx); err != nil {
			return nil, err
		}
	}
	if g.err != nil {
		return nil, g.err
	}
	return &geocoding.GeocodingResult{Coords: models.Coordinates{Lat: 41, Lng: -72}}, nil
}

func TestCalculateRefreshesExpiredCoordinates(t *testing.T) {
	for _, tc := range []struct {
		name    string
		age     time.Duration
		refresh bool
	}{
		{"expired", 31 * 24 * time.Hour, true},
		{"expires during session", models.CoordinateMaxAge - routesession.DefaultTTL - routeSolveTimeout + time.Second, true},
		{"fresh through session", models.CoordinateMaxAge - routesession.DefaultTTL - routeSolveTimeout - time.Hour, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, subject := range []string{"participant", "driver", "location"} {
				t.Run(subject, func(t *testing.T) {
					h, s := newTestRouteHandler(t)
					g := &refreshGeocoder{}
					h.Geocoder = g
					ctx := t.Context()
					p, err := s.Participants().Create(ctx, &models.Participant{Name: "Rider", Address: "1 Rider Rd", Lat: 1, Lng: 2})
					if err != nil {
						t.Fatal(err)
					}
					d, err := s.Drivers().Create(ctx, &models.Driver{Name: "Driver", Address: "2 Driver Rd", Lat: 3, Lng: 4, VehicleCapacity: 4})
					if err != nil {
						t.Fatal(err)
					}
					loc, err := s.ActivityLocations().Create(ctx, &models.ActivityLocation{Name: "Gym", Address: "3 Gym Rd", Lat: 5, Lng: 6})
					if err != nil {
						t.Fatal(err)
					}
					if subject == "participant" {
						if err := s.Participants().UpdateCoordinates(ctx, p.ID, p.Address, p.GetCoords(), time.Now().Add(-tc.age)); err != nil {
							t.Fatal(err)
						}
					}
					if subject == "driver" {
						if err := s.Drivers().UpdateCoordinates(ctx, d.ID, d.Address, d.GetCoords(), time.Now().Add(-tc.age)); err != nil {
							t.Fatal(err)
						}
					}
					if subject == "location" {
						if err := s.ActivityLocations().UpdateCoordinates(ctx, loc.ID, loc.Address, loc.GetCoords(), time.Now().Add(-tc.age)); err != nil {
							t.Fatal(err)
						}
					}
					router := &captureRouter{result: &models.RoutingResult{Routes: []models.CalculatedRoute{{Driver: d, Stops: []models.RouteStop{{Participant: p}}}}}}
					h.Router = router
					form := url.Values{"participant_ids": {fmt.Sprint(p.ID)}, "driver_ids": {fmt.Sprint(d.ID)}, "activity_location_id": {fmt.Sprint(loc.ID)}, "route_time": {"18:30"}, "mode": {"dropoff"}}
					response := postMobileForm(t, nil, "/api/v1/routes/calculate", form, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						r.Header.Set("HX-Request", "true")
						h.HandleCalculateRoutes(w, r)
					}))
					if response.Code != http.StatusOK {
						t.Fatalf("status=%d body=%s", response.Code, response.Body)
					}
					wantCalls := int32(0)
					wantLat := float64(1)
					if tc.refresh {
						wantCalls = 1
					}
					if subject == "participant" && tc.refresh {
						wantLat = 41
					}
					if g.calls.Load() != wantCalls {
						t.Fatalf("calls=%d want=%d", g.calls.Load(), wantCalls)
					}
					if router.lastRequest == nil || router.lastRequest.Participants[0].Lat != wantLat {
						t.Fatalf("solver input=%#v", router.lastRequest)
					}
					if subject == "driver" && tc.refresh && router.lastRequest.Drivers[0].Lat != 41 {
						t.Fatal("solver used stale driver")
					}
					if subject == "location" && tc.refresh && router.lastRequest.InstituteCoords.Lat != 41 {
						t.Fatal("solver used stale location")
					}
					saved, err := s.Participants().GetByID(ctx, p.ID)
					if err != nil || saved.Lat != wantLat || time.Since(saved.GeocodedAt) > models.CoordinateMaxAge-routesession.DefaultTTL-routeSolveTimeout {
						t.Fatalf("saved=%#v %v", saved, err)
					}
					savedDriver, err := s.Drivers().GetByID(ctx, d.ID)
					if err != nil {
						t.Fatal(err)
					}
					savedLocation, err := s.ActivityLocations().GetByID(ctx, loc.ID)
					if err != nil {
						t.Fatal(err)
					}
					for name, at := range map[string]time.Time{"participant": saved.GeocodedAt, "driver": savedDriver.GeocodedAt, "location": savedLocation.GeocodedAt} {
						if time.Since(at) > models.CoordinateMaxAge-routesession.DefaultTTL-routeSolveTimeout {
							t.Fatalf("%s coordinates expire before the session can end: %s", name, at)
						}
					}
				})
			}
		})
	}
}

func TestCalculateRefreshErrorMessages(t *testing.T) {
	for _, tc := range []struct {
		name    string
		err     error
		message string
		status  int
	}{
		{"no results", geocoding.ErrNoGeocodingResults, "The address for Rider could not be found. Update it and try again.", 400},
		{"not configured", geocoding.ErrNotConfigured, messageAddressLookupNotConfigured, 503},
		{"temporary", &geocoding.ErrGeocodingFailed{Temporary: true}, messageAddressLookupUnavailable, 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, s := newTestRouteHandler(t)
			h.Geocoder = &refreshGeocoder{err: tc.err}
			h.PlanDraft = plandraft.NewStore()
			t.Cleanup(h.PlanDraft.Close)
			ctx := t.Context()
			p, err := s.Participants().Create(ctx, &models.Participant{Name: "Rider", Address: "1 Rider Rd", Lat: 1, Lng: 2})
			if err != nil {
				t.Fatal(err)
			}
			d, err := s.Drivers().Create(ctx, &models.Driver{Name: "Driver", Address: "2 Driver Rd", Lat: 3, Lng: 4, VehicleCapacity: 4})
			if err != nil {
				t.Fatal(err)
			}
			loc, err := s.ActivityLocations().Create(ctx, &models.ActivityLocation{Name: "Gym", Address: "3 Gym Rd", Lat: 5, Lng: 6})
			if err != nil {
				t.Fatal(err)
			}
			if err := s.Participants().UpdateCoordinates(ctx, p.ID, p.Address, p.GetCoords(), time.Now().Add(-31*24*time.Hour)); err != nil {
				t.Fatal(err)
			}
			form := url.Values{"participant_ids": {fmt.Sprint(p.ID)}, "driver_ids": {fmt.Sprint(d.ID)}, "activity_location_id": {fmt.Sprint(loc.ID)}, "route_time": {"18:30"}, "mode": {"dropoff"}}
			response := postMobileForm(t, nil, "/api/v1/routes/calculate", form, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				r.Header.Set("HX-Request", "true")
				h.HandleCalculateRoutes(w, r)
			}))
			if response.Code != tc.status || !strings.Contains(response.Header().Get("HX-Trigger"), tc.message) {
				t.Fatalf("desktop=%d %s %s", response.Code, response.Header(), response.Body)
			}
			id := h.PlanDraft.NewID()
			h.PlanDraft.Update(id, func(draft *plandraft.Draft) {
				draft.ParticipantIDs = []int64{p.ID}
				draft.DriverIDs = []int64{d.ID}
				draft.LocationID = loc.ID
				draft.RouteTime = "18:30"
				draft.Mode = "dropoff"
			})
			mobile := postMobileForm(t, mobileTestCookie(id), "/m/calculate", nil, h.HandleMobileCalculate)
			target, err := url.Parse(mobile.Header().Get("Location"))
			if err != nil || mobile.Code != 303 || target.Query().Get("error") != tc.message {
				t.Fatalf("mobile=%d %s %v", mobile.Code, mobile.Header(), err)
			}
			saved, err := s.Participants().GetByID(ctx, p.ID)
			if err != nil || saved.Lat != 1 || time.Since(saved.GeocodedAt) < 30*24*time.Hour {
				t.Fatalf("failed refresh changed row=%#v %v", saved, err)
			}
		})
	}
}

func TestCalculateBoundsConcurrentRefresh(t *testing.T) {
	h, s := newTestRouteHandler(t)
	ctx := t.Context()
	old := time.Now().Add(-31 * 24 * time.Hour)
	var ids []string
	var rider *models.Participant
	for i := range 6 {
		p, err := s.Participants().Create(ctx, &models.Participant{Name: fmt.Sprintf("Rider %d", i), Address: fmt.Sprintf("Home %d", i), Lat: 1, Lng: 2, GeocodedAt: old})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, fmt.Sprint(p.ID))
		rider = p
	}
	d, err := s.Drivers().Create(ctx, &models.Driver{Name: "Driver", Address: "Home", Lat: 3, Lng: 4, VehicleCapacity: 8})
	if err != nil {
		t.Fatal(err)
	}
	loc, err := s.ActivityLocations().Create(ctx, &models.ActivityLocation{Name: "Gym", Address: "Gym", Lat: 5, Lng: 6})
	if err != nil {
		t.Fatal(err)
	}
	h.Router = &captureRouter{result: &models.RoutingResult{Routes: []models.CalculatedRoute{{Driver: d, Stops: []models.RouteStop{{Participant: rider}}}}}}
	gate := make(chan struct{})
	started := make(chan struct{}, 6)
	var active, peak atomic.Int32
	g := &refreshGeocoder{before: func(ctx context.Context) error {
		n := active.Add(1)
		defer active.Add(-1)
		for previous := peak.Load(); n > previous; previous = peak.Load() {
			if peak.CompareAndSwap(previous, n) {
				break
			}
		}
		started <- struct{}{}
		select {
		case <-gate:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}}
	h.Geocoder = g
	form := url.Values{"participant_ids": ids, "driver_ids": {fmt.Sprint(d.ID)}, "activity_location_id": {fmt.Sprint(loc.ID)}, "route_time": {"18:30"}, "mode": {"dropoff"}}
	req := httptest.NewRequestWithContext(ctx, http.MethodPost, "/api/v1/routes/calculate", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { defer close(done); h.HandleCalculateRoutes(response, req) }()
	for range 4 {
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			close(gate)
			<-done
			t.Fatal("four workers did not start")
		}
	}
	close(gate)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("calculation did not finish")
	}
	if peak.Load() != 4 || g.calls.Load() != 6 || response.Code != 200 {
		t.Fatalf("peak=%d calls=%d status=%d body=%s", peak.Load(), g.calls.Load(), response.Code, response.Body)
	}
}

func TestMobileLocationEditsRetainAndRenewFreshness(t *testing.T) {
	h, s := newTestManagementHandler(t)
	ctx := t.Context()
	loc, err := s.ActivityLocations().Create(ctx, &models.ActivityLocation{Name: "Gym", Address: "Home", Lat: 1, Lng: 2, GeocodedAt: time.Now().Add(-31 * 24 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	loc, err = s.ActivityLocations().GetByID(ctx, loc.ID)
	if err != nil {
		t.Fatal(err)
	}
	old := loc.GeocodedAt
	path := fmt.Sprintf("/m/places/locations/%d/edit", loc.ID)
	for _, address := range []string{"Home", "New address"} {
		response := postMobileForm(t, nil, path, url.Values{"name": {"Renamed gym"}, "address": {address}}, h.HandleMobileLocationForm)
		if response.Code != 303 {
			t.Fatalf("status=%d body=%s", response.Code, response.Body)
		}
		saved, err := s.ActivityLocations().GetByID(ctx, loc.ID)
		if err != nil {
			t.Fatal(err)
		}
		if address == "Home" && !saved.GeocodedAt.Equal(old) {
			t.Fatalf("unchanged timestamp=%s", saved.GeocodedAt)
		}
		if address != "Home" && time.Since(saved.GeocodedAt) > time.Minute {
			t.Fatalf("new lookup timestamp=%s", saved.GeocodedAt)
		}
	}
}

func TestAddDriverRefreshesCoordinates(t *testing.T) {
	for _, persistent := range []bool{false, true} {
		for _, mobile := range []bool{false, true} {
			for _, stale := range []bool{false, true} {
				t.Run(fmt.Sprintf("postgres=%v/mobile=%v/stale=%v", persistent, mobile, stale), func(t *testing.T) {
					h, db := newTestRouteHandler(t)
					if persistent {
						h.RouteSession = routesession.NewPersistentStore(routeEditDistanceCalculator{}, db.Workflows())
					}
					g := &refreshGeocoder{}
					h.Geocoder = g
					driver, err := db.Drivers().Create(t.Context(), &models.Driver{Name: "Extra", Address: "4 Driver Rd", Lat: 3, Lng: 4, VehicleCapacity: 4})
					if err != nil {
						t.Fatal(err)
					}
					at := time.Now().Add(-time.Hour).Truncate(time.Microsecond)
					if stale {
						at = time.Now().Add(-models.CoordinateMaxAge + coordinateRefreshMargin - time.Minute)
					}
					if err := db.Drivers().UpdateCoordinates(t.Context(), driver.ID, driver.Address, driver.GetCoords(), at); err != nil {
						t.Fatal(err)
					}
					deadline := time.Now().Add(models.CoordinateMaxAge)
					created, err := h.RouteSession.CreateContext(t.Context(), routesession.CreateInput{CoordinatesFreshUntil: deadline, SelectedDrivers: []models.Driver{*driver}, ActivityLocation: &models.ActivityLocation{}, Mode: models.RouteModeDropoff})
					if err != nil {
						t.Fatal(err)
					}
					var response *httptest.ResponseRecorder
					if mobile {
						h.PlanDraft = plandraft.NewStore()
						t.Cleanup(h.PlanDraft.Close)
						id := h.PlanDraft.NewID()
						h.PlanDraft.Update(id, func(d *plandraft.Draft) { d.RouteSessionID = created.ID })
						response = postMobileForm(t, mobileTestCookie(id), "/m/routes/add-driver", url.Values{"session_id": {created.ID}, "driver_id": {fmt.Sprint(driver.ID)}}, h.HandleMobileAddDriver)
						if response.Code != 303 || response.Header().Get("Location") != "/m/routes" {
							t.Fatalf("mobile: %d %s", response.Code, response.Header())
						}
					} else {
						response = httptest.NewRecorder()
						h.HandleAddDriver(response, newRouteEditJSONRequest("/api/v1/routes/edit/add-driver", []byte(fmt.Sprintf(`{"session_id":%q,"driver_id":%d}`, created.ID, driver.ID))))
						if response.Code != 200 {
							t.Fatalf("desktop: %d %s", response.Code, response.Body)
						}
					}
					wantCalls := int32(0)
					wantLat := float64(3)
					if stale {
						wantCalls = 1
						wantLat = 41
					}
					saved, err := db.Drivers().GetByID(t.Context(), driver.ID)
					if err != nil || saved.Lat != wantLat || g.calls.Load() != wantCalls {
						t.Fatalf("driver=%+v calls=%d err=%v", saved, g.calls.Load(), err)
					}
					snapshot, ok, err := h.RouteSession.Load(t.Context(), created.ID)
					if err != nil || !ok || len(snapshot.Routes) != 1 || snapshot.Routes[0].Driver.Lat != wantLat {
						t.Fatalf("snapshot=%+v %v", snapshot, err)
					}
					if !stale && !snapshot.CoordinatesFreshUntil.Equal(at.Add(models.CoordinateMaxAge)) {
						t.Fatalf("deadline not lowered: %v", snapshot.CoordinatesFreshUntil)
					}
				})
			}
		}
	}
}

func TestCalculationStoresEarliestCoordinateDeadline(t *testing.T) {
	for _, subject := range []string{"participant", "driver", "location"} {
		t.Run(subject, func(t *testing.T) {
			h, db := newTestRouteHandler(t)
			p, err := db.Participants().Create(t.Context(), &models.Participant{Name: "Rider", Address: "1 Rd", Lat: 1, Lng: 2})
			if err != nil {
				t.Fatal(err)
			}
			d, err := db.Drivers().Create(t.Context(), &models.Driver{Name: "Driver", Address: "2 Rd", Lat: 3, Lng: 4, VehicleCapacity: 4})
			if err != nil {
				t.Fatal(err)
			}
			loc, err := db.ActivityLocations().Create(t.Context(), &models.ActivityLocation{Name: "Gym", Address: "3 Rd", Lat: 5, Lng: 6})
			if err != nil {
				t.Fatal(err)
			}
			at := time.Now().Add(-models.CoordinateMaxAge + coordinateRefreshMargin + time.Hour).Truncate(time.Microsecond)
			switch subject {
			case "participant":
				err = db.Participants().UpdateCoordinates(t.Context(), p.ID, p.Address, p.GetCoords(), at)
			case "driver":
				err = db.Drivers().UpdateCoordinates(t.Context(), d.ID, d.Address, d.GetCoords(), at)
			case "location":
				err = db.ActivityLocations().UpdateCoordinates(t.Context(), loc.ID, loc.Address, loc.GetCoords(), at)
			}
			if err != nil {
				t.Fatal(err)
			}
			outcome := newRouteCalculation(db, &captureRouter{result: &models.RoutingResult{}}, h.RouteSession, nil).calculate(t.Context(), routeCalculationInput{ParticipantIDs: []int64{p.ID}, DriverIDs: []int64{d.ID}, ActivityLocationID: loc.ID, Mode: models.RouteModeDropoff})
			if outcome.Kind != routeCalculationSuccess {
				t.Fatalf("calculation: %+v", outcome)
			}
			if !outcome.Session.CoordinatesFreshUntil.Equal(at.Add(models.CoordinateMaxAge)) {
				t.Fatalf("deadline=%v", outcome.Session.CoordinatesFreshUntil)
			}
		})
	}
}

func TestExpiredCoordinatePlanMessages(t *testing.T) {
	h, _ := newTestRouteHandler(t)
	h.PlanDraft = plandraft.NewStore()
	t.Cleanup(h.PlanDraft.Close)
	for _, action := range []string{"move", "swap", "reset", "add-driver", "save"} {
		t.Run(action, func(t *testing.T) {
			created := h.RouteSession.Create(routesession.CreateInput{CoordinatesFreshUntil: time.Now().Add(-time.Second)})
			body := fmt.Sprintf(`{"session_id":%q,"participant_id":1,"driver_id":1,"event_date":"2026-09-12"}`, created.ID)
			req := newRouteEditJSONRequest("/api/v1/action?session_id="+created.ID, []byte(body))
			req.Header.Set("HX-Request", "true")
			w := httptest.NewRecorder()
			desktop := map[string]http.HandlerFunc{"move": h.HandleMoveParticipant, "swap": h.HandleSwapDrivers, "reset": h.HandleResetRoutes, "add-driver": h.HandleAddDriver, "save": h.HandleCreateEvent}
			desktop[action](w, req)
			if w.Code != 409 || !strings.Contains(w.Header().Get("HX-Trigger"), messageRoutePlanExpired) {
				t.Fatalf("desktop %d %s %s", w.Code, w.Header(), w.Body)
			}
			id := h.PlanDraft.NewID()
			h.PlanDraft.Update(id, func(d *plandraft.Draft) { d.RouteSessionID = created.ID })
			mobile := map[string]http.HandlerFunc{"move": h.HandleMobileMove, "swap": h.HandleMobileSwap, "reset": h.HandleMobileReset, "add-driver": h.HandleMobileAddDriver, "save": h.HandleMobileSave}
			values := url.Values{"session_id": {created.ID}, "participant_id": {"1"}, "from_route_index": {"0"}, "to_route_index": {"1"}, "route_index_1": {"0"}, "route_index_2": {"1"}, "driver_id": {"1"}, "event_date": {"2026-09-12"}}
			response := postMobileForm(t, mobileTestCookie(id), "/m/routes/"+action, values, mobile[action])
			if action == "save" {
				if response.Code != 409 || !strings.Contains(response.Body.String(), messageRoutePlanExpired) {
					t.Fatalf("mobile save %d %s", response.Code, response.Body)
				}
				return
			}
			target, err := url.Parse(response.Header().Get("Location"))
			if err != nil || response.Code != 303 || target.Query().Get("error") != messageRoutePlanExpired {
				t.Fatalf("mobile %d %s", response.Code, response.Header())
			}
		})
	}
}

func TestAddDriverRefreshFailureMessages(t *testing.T) {
	for _, tc := range []struct {
		name    string
		err     error
		message string
		status  int
	}{
		{"missing address", geocoding.ErrNoGeocodingResults, "The address for Extra could not be found. Update it and try again.", 400},
		{"unconfigured", geocoding.ErrNotConfigured, messageAddressLookupNotConfigured, 503},
		{"unavailable", &geocoding.ErrGeocodingFailed{Temporary: true}, messageAddressLookupUnavailable, 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, db := newTestRouteHandler(t)
			h.Geocoder = &refreshGeocoder{err: tc.err}
			d, err := db.Drivers().Create(t.Context(), &models.Driver{Name: "Extra", Address: "1 Rd", Lat: 1, Lng: 2, VehicleCapacity: 4})
			if err != nil {
				t.Fatal(err)
			}
			if err := db.Drivers().UpdateCoordinates(t.Context(), d.ID, d.Address, d.GetCoords(), time.Now().Add(-models.CoordinateMaxAge)); err != nil {
				t.Fatal(err)
			}
			created := h.RouteSession.Create(routesession.CreateInput{SelectedDrivers: []models.Driver{*d}, ActivityLocation: &models.ActivityLocation{}})
			req := newRouteEditJSONRequest("/api/v1/routes/edit/add-driver", []byte(fmt.Sprintf(`{"session_id":%q,"driver_id":%d}`, created.ID, d.ID)))
			req.Header.Set("HX-Request", "true")
			w := httptest.NewRecorder()
			h.HandleAddDriver(w, req)
			if w.Code != tc.status || !strings.Contains(w.Header().Get("HX-Trigger"), tc.message) {
				t.Fatalf("desktop %d %s", w.Code, w.Header())
			}
			h.PlanDraft = plandraft.NewStore()
			t.Cleanup(h.PlanDraft.Close)
			id := h.PlanDraft.NewID()
			h.PlanDraft.Update(id, func(draft *plandraft.Draft) { draft.RouteSessionID = created.ID })
			response := postMobileForm(t, mobileTestCookie(id), "/m/routes/add-driver", url.Values{"session_id": {created.ID}, "driver_id": {fmt.Sprint(d.ID)}}, h.HandleMobileAddDriver)
			target, err := url.Parse(response.Header().Get("Location"))
			if err != nil || response.Code != 303 || target.Query().Get("error") != tc.message {
				t.Fatalf("mobile %d %s", response.Code, response.Header())
			}
			snapshot, ok, _ := h.RouteSession.Load(t.Context(), created.ID)
			if !ok || len(snapshot.Routes) != 0 {
				t.Fatal("failed refresh changed routes")
			}
		})
	}
}
