package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"ride-home-router/internal/models"
	"ride-home-router/internal/routesession"
	"strings"
	"testing"
)

func TestMobileRedirectErrorFallsBackForInvalidURL(t *testing.T) {
	t.Parallel()
	handler := &Handler{}
	request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/m", nil)
	response := httptest.NewRecorder()

	handler.mobileRedirectError(response, request, "/m/\x00", messageMobileInvalidForm)

	if response.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", response.Code)
	}
	target, err := url.Parse(response.Header().Get("Location"))
	if err != nil || target.Path != "/m" || target.Query().Get("error") != messageMobileInvalidForm {
		t.Fatalf("redirect target = %q parsed=%#v err=%v", response.Header().Get("Location"), target, err)
	}
}

func TestValidMobileDraftID(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		id   string
		want bool
	}{
		{id: "0123456789abcdef0123456789abcdef", want: true},
		{id: "0123456789ABCDEF0123456789ABCDEF"},
		{id: "0123456789abcdef"},
		{id: "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz"},
	} {
		if got := validMobileDraftID(test.id); got != test.want {
			t.Errorf("validMobileDraftID(%q) = %v, want %v", test.id, got, test.want)
		}
	}
}

func TestMobileIDRejectsNonPositiveIDs(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"/m/history/0", "/m/history/-5", "/m/history/nope"} {
		if _, err := mobileID(path, "/m/history/", ""); err == nil {
			t.Errorf("mobileID(%q) error = nil", path)
		}
	}
}

func TestFormatMobileHandoffNumbersRenderedStopsAndOmitsEmptyAddress(t *testing.T) {
	t.Parallel()

	snapshot := routesession.Snapshot{
		ActivityLocation: &models.ActivityLocation{Name: "Wednesday Night Church", Address: "1 Church Road", Lat: 40.4, Lng: -74.4},
		RouteTime:        "12:00",
		Mode:             models.RouteModeDropoff,
	}
	route := models.CalculatedRoute{
		Driver: &models.Driver{Name: "Casey Driver", Address: "10 Driver Lane", Lat: 40.6, Lng: -74.6},
		Stops: []models.RouteStop{
			{Participant: nil},
			{Participant: &models.Participant{Name: "Taylor Rider", Lat: 40.5, Lng: -74.5}, CumulativeDurationSecs: 1200},
		},
	}

	got := formatMobileHandoff(snapshot, route, false)

	if !strings.Contains(got, "1. 12:22 PM - Taylor Rider\n") {
		t.Fatalf("handoff should number emitted stops from one:\n%s", got)
	}
	if strings.Contains(got, "2. 12:22 PM") {
		t.Fatalf("handoff used the source slice index after skipping a nil participant:\n%s", got)
	}
	if strings.Contains(got, "Taylor Rider - \n") {
		t.Fatalf("handoff has a dangling address separator:\n%s", got)
	}
}

func TestMobileMapsURLRejectsUnresolvedLocations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		snapshot routesession.Snapshot
		route    models.CalculatedRoute
	}{
		{
			name:     "activity location",
			snapshot: routesession.Snapshot{ActivityLocation: &models.ActivityLocation{}, Mode: models.RouteModeDropoff},
			route: models.CalculatedRoute{
				Driver: &models.Driver{Address: "10 Driver Lane"},
				Stops:  []models.RouteStop{{Participant: &models.Participant{Address: "5 Rider Street"}}},
			},
		},
		{
			name:     "driver",
			snapshot: routesession.Snapshot{ActivityLocation: &models.ActivityLocation{Address: "1 Church Road"}, Mode: models.RouteModeDropoff},
			route: models.CalculatedRoute{
				Driver: &models.Driver{},
				Stops:  []models.RouteStop{{Participant: &models.Participant{Address: "5 Rider Street"}}},
			},
		},
		{
			name:     "stop",
			snapshot: routesession.Snapshot{ActivityLocation: &models.ActivityLocation{Address: "1 Church Road"}, Mode: models.RouteModeDropoff},
			route: models.CalculatedRoute{
				Driver: &models.Driver{Address: "10 Driver Lane"},
				Stops:  []models.RouteStop{{Participant: &models.Participant{}}},
			},
		},
		{
			name:     "nil stop participant",
			snapshot: routesession.Snapshot{ActivityLocation: &models.ActivityLocation{Address: "1 Church Road"}, Mode: models.RouteModeDropoff},
			route: models.CalculatedRoute{
				Driver: &models.Driver{Address: "10 Driver Lane"},
				Stops:  []models.RouteStop{{Participant: nil}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := mobileMapsURL(tt.snapshot, tt.route); got != "" {
				t.Fatalf("mobileMapsURL() = %q, want blank URL", got)
			}
		})
	}
}

func TestMobileMapsURLFormatsCoordinatesToSixDecimalPlaces(t *testing.T) {
	t.Parallel()

	snapshot := routesession.Snapshot{
		ActivityLocation: &models.ActivityLocation{Lat: 40.41234567, Lng: -74.41234567},
		Mode:             models.RouteModePickup,
	}
	route := models.CalculatedRoute{
		Driver: &models.Driver{Lat: 40.61234567, Lng: -74.61234567},
		Stops: []models.RouteStop{{
			Participant: &models.Participant{Lat: 40.51234567, Lng: -74.51234567},
		}},
	}

	got := mobileMapsURL(snapshot, route)
	parsed, err := url.Parse(got)
	if err != nil {
		t.Fatalf("parse Maps URL: %v", err)
	}
	query := parsed.Query()
	if got, want := query.Get("destination"), "40.412346,-74.412346"; got != want {
		t.Errorf("destination = %q, want %q", got, want)
	}
	if got, want := query.Get("waypoints"), "40.512346,-74.512346"; got != want {
		t.Errorf("waypoints = %q, want %q", got, want)
	}
	if strings.Contains(got, "40.61234567") || strings.Contains(got, "40.51234567") {
		t.Errorf("Maps URL retained coordinates beyond six decimal places: %s", got)
	}
}
