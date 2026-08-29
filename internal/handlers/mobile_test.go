package handlers

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"ride-home-router/internal/models"
	"ride-home-router/internal/plandraft"
	"strings"
	"testing"
)

func TestMobileDraftFlowCalculatesRendersAndMovesParticipant(t *testing.T) {
	handler, store := newTestRouteHandler(t)
	handler.PlanDraft = plandraft.NewStore()
	t.Cleanup(handler.PlanDraft.Close)
	ctx := context.Background()

	location, err := store.ActivityLocations().Create(ctx, &models.ActivityLocation{Name: "Grace Center", Address: "1 Grace Way", Lat: 40, Lng: -73})
	if err != nil {
		t.Fatal(err)
	}
	firstRider, err := store.Participants().Create(ctx, &models.Participant{Name: "Maya Chen", Address: "2 Oak Ave", Lat: 40.1, Lng: -73.1})
	if err != nil {
		t.Fatal(err)
	}
	secondRider, err := store.Participants().Create(ctx, &models.Participant{Name: "Liam Ortiz", Address: "3 Birch Ln", Lat: 40.2, Lng: -73.2})
	if err != nil {
		t.Fatal(err)
	}
	firstDriver, err := store.Drivers().Create(ctx, &models.Driver{Name: "Dana Whitfield", Address: "4 Driver Rd", Lat: 40.3, Lng: -73.3, VehicleCapacity: 3})
	if err != nil {
		t.Fatal(err)
	}
	secondDriver, err := store.Drivers().Create(ctx, &models.Driver{Name: "Marcus Hill", Address: "5 Driver Rd", Lat: 40.4, Lng: -73.4, VehicleCapacity: 3})
	if err != nil {
		t.Fatal(err)
	}

	handler.Router = &captureRouter{result: &models.RoutingResult{Routes: []models.CalculatedRoute{
		{Driver: firstDriver, Stops: []models.RouteStop{{Participant: firstRider}}, EffectiveCapacity: 3, Mode: models.RouteModeDropoff},
		{Driver: secondDriver, Stops: []models.RouteStop{{Participant: secondRider}}, EffectiveCapacity: 3, Mode: models.RouteModeDropoff},
	}, Summary: models.RoutingSummary{TotalParticipants: 2, TotalDriversUsed: 2}, Mode: models.RouteModeDropoff}}

	locationResponse := postMobileForm(t, nil, "/m/plan/location", url.Values{"location_id": {fmt.Sprint(location.ID)}}, handler.HandleMobileLocation)
	cookies := locationResponse.Result().Cookies()
	if len(cookies) == 0 || cookies[0].Name != mobileDraftCookie {
		t.Fatalf("draft cookie = %#v", cookies)
	}
	draftCookie := cookies[0]
	assertMobileRedirect(t, locationResponse, "/m")

	ridersResponse := postMobileForm(t, draftCookie, "/m/plan/riders", url.Values{"participant_ids": {fmt.Sprint(firstRider.ID), fmt.Sprint(secondRider.ID)}}, handler.HandleMobileRiders)
	assertMobileRedirect(t, ridersResponse, "/m")
	driversResponse := postMobileForm(t, draftCookie, "/m/plan/drivers", url.Values{"driver_ids": {fmt.Sprint(firstDriver.ID), fmt.Sprint(secondDriver.ID)}}, handler.HandleMobileDrivers)
	assertMobileRedirect(t, driversResponse, "/m")

	calculateResponse := postMobileForm(t, draftCookie, "/m/calculate", nil, handler.HandleMobileCalculate)
	assertMobileRedirect(t, calculateResponse, "/m/routes")
	draft := handler.PlanDraft.Get(draftCookie.Value)
	if draft.RouteSessionID == "" {
		t.Fatal("calculate did not save the route session ID in the draft")
	}

	routesRequest := httptest.NewRequestWithContext(ctx, http.MethodGet, "/m/routes", nil)
	routesRequest.AddCookie(draftCookie)
	routesResponse := httptest.NewRecorder()
	handler.HandleMobileRoutes(routesResponse, routesRequest)
	if routesResponse.Code != http.StatusOK {
		t.Fatalf("routes status = %d, body=%q", routesResponse.Code, routesResponse.Body.String())
	}
	for _, want := range []string{"Dana Whitfield", "Marcus Hill", "Maya Chen", "Copy for parents"} {
		if !strings.Contains(routesResponse.Body.String(), want) {
			t.Fatalf("routes page missing %q", want)
		}
	}

	moveResponse := postMobileForm(t, draftCookie, "/m/routes/move", url.Values{
		"participant_id": {fmt.Sprint(firstRider.ID)}, "from_route_index": {"0"}, "to_route_index": {"1"},
	}, handler.HandleMobileMove)
	assertMobileRedirect(t, moveResponse, "/m/routes")
	snapshot, ok := handler.RouteSession.Snapshot(draft.RouteSessionID)
	if !ok || len(snapshot.Routes[0].Stops) != 0 || len(snapshot.Routes[1].Stops) != 2 {
		t.Fatalf("routes after move = %#v", snapshot.Routes)
	}
}

func TestFormatSavedMobileHandoffKeepsParentCopyPrivate(t *testing.T) {
	route := models.EventRoute{DriverName: "Dana", DriverAddress: "1 Driver Rd", Stops: []models.EventRouteStop{{ParticipantName: "Maya", ParticipantAddress: "2 Rider Rd"}}}
	driverText := formatSavedMobileHandoff(route, false)
	parentText := formatSavedMobileHandoff(route, true)
	if !strings.Contains(driverText, "1 Driver Rd") || !strings.Contains(driverText, "2 Rider Rd") {
		t.Fatalf("driver copy = %q", driverText)
	}
	if strings.Contains(parentText, "Driver Rd") || strings.Contains(parentText, "Rider Rd") || !strings.Contains(parentText, "Maya") {
		t.Fatalf("parent copy = %q", parentText)
	}
}

func postMobileForm(t *testing.T, cookie *http.Cookie, path string, values url.Values, handler http.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	body := ""
	if values != nil {
		body = values.Encode()
	}
	request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if cookie != nil {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	handler(response, request)
	return response
}

func assertMobileRedirect(t *testing.T, response *httptest.ResponseRecorder, location string) {
	t.Helper()
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != location {
		t.Fatalf("redirect = %d %q, want 303 %q; body=%q", response.Code, response.Header().Get("Location"), location, response.Body.String())
	}
}
