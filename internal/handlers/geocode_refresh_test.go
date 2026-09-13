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
	for _, subject := range []string{"participant", "driver", "location", "fresh"} {
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
				if err := s.Participants().UpdateCoordinates(ctx, p.ID, p.Address, p.GetCoords(), time.Now().Add(-31*24*time.Hour)); err != nil {
					t.Fatal(err)
				}
			}
			if subject == "driver" {
				if err := s.Drivers().UpdateCoordinates(ctx, d.ID, d.Address, d.GetCoords(), time.Now().Add(-31*24*time.Hour)); err != nil {
					t.Fatal(err)
				}
			}
			if subject == "location" {
				if err := s.ActivityLocations().UpdateCoordinates(ctx, loc.ID, loc.Address, loc.GetCoords(), time.Now().Add(-31*24*time.Hour)); err != nil {
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
			if subject != "fresh" {
				wantCalls = 1
			}
			if subject == "participant" {
				wantLat = 41
			}
			if g.calls.Load() != wantCalls {
				t.Fatalf("calls=%d want=%d", g.calls.Load(), wantCalls)
			}
			if router.lastRequest == nil || router.lastRequest.Participants[0].Lat != wantLat {
				t.Fatalf("solver input=%#v", router.lastRequest)
			}
			if subject == "driver" && router.lastRequest.Drivers[0].Lat != 41 {
				t.Fatal("solver used stale driver")
			}
			if subject == "location" && router.lastRequest.InstituteCoords.Lat != 41 {
				t.Fatal("solver used stale location")
			}
			saved, err := s.Participants().GetByID(ctx, p.ID)
			if err != nil || saved.Lat != wantLat || time.Since(saved.GeocodedAt) > time.Minute {
				t.Fatalf("saved=%#v %v", saved, err)
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
