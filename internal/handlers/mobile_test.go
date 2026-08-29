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
	thirdDriver, err := store.Drivers().Create(ctx, &models.Driver{Name: "Taylor Brooks", Address: "6 Driver Rd", Lat: 40.5, Lng: -73.5, VehicleCapacity: 3})
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
	assertMobilePage(t, draftCookie, "/m", handler.HandleMobilePlan, "Plan")
	assertMobilePage(t, draftCookie, "/m/plan/location", handler.HandleMobileLocation, "Grace Center")
	assertMobilePage(t, draftCookie, "/m/plan/riders", handler.HandleMobileRiders, "Maya Chen")
	assertMobilePage(t, draftCookie, "/m/plan/drivers", handler.HandleMobileDrivers, "Dana Whitfield")
	assertMobilePage(t, draftCookie, "/m/plan/when", handler.HandleMobileWhen, "Dropoff")
	whenResponse := postMobileForm(t, draftCookie, "/m/plan/when", url.Values{"route_time": {"18:30"}, "mode": {"dropoff"}}, handler.HandleMobileWhen)
	assertMobileRedirect(t, whenResponse, "/m")

	ridersResponse := postMobileForm(t, draftCookie, "/m/plan/riders", url.Values{"participant_ids": {fmt.Sprint(firstRider.ID), fmt.Sprint(secondRider.ID)}}, handler.HandleMobileRiders)
	assertMobileRedirect(t, ridersResponse, "/m")
	driversResponse := postMobileForm(t, draftCookie, "/m/plan/drivers", url.Values{"driver_ids": {fmt.Sprint(firstDriver.ID), fmt.Sprint(secondDriver.ID), fmt.Sprint(thirdDriver.ID)}}, handler.HandleMobileDrivers)
	assertMobileRedirect(t, driversResponse, "/m")

	calculateResponse := postMobileForm(t, draftCookie, "/m/calculate", nil, handler.HandleMobileCalculate)
	assertMobileRedirect(t, calculateResponse, "/m/routes")
	draft, ok := handler.PlanDraft.Get(draftCookie.Value)
	if !ok {
		t.Fatal("calculated draft was not found")
	}
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

	swapResponse := postMobileForm(t, draftCookie, "/m/routes/swap", url.Values{"route_index_1": {"0"}, "route_index_2": {"1"}}, handler.HandleMobileSwap)
	assertMobileRedirect(t, swapResponse, "/m/routes")
	resetResponse := postMobileForm(t, draftCookie, "/m/routes/reset", nil, handler.HandleMobileReset)
	assertMobileRedirect(t, resetResponse, "/m/routes")
	addDriverResponse := postMobileForm(t, draftCookie, "/m/routes/add-driver", url.Values{"driver_id": {fmt.Sprint(thirdDriver.ID)}}, handler.HandleMobileAddDriver)
	assertMobileRedirect(t, addDriverResponse, "/m/routes")
	resetResponse = postMobileForm(t, draftCookie, "/m/routes/reset", nil, handler.HandleMobileReset)
	assertMobileRedirect(t, resetResponse, "/m/routes")

	saveResponse := postMobileForm(t, draftCookie, "/m/routes/save", url.Values{"event_date": {"2026-08-29"}, "notes": {"Mobile test event"}}, handler.HandleMobileSave)
	if saveResponse.Code != http.StatusSeeOther || !strings.HasPrefix(saveResponse.Header().Get("Location"), "/m/history/") {
		t.Fatalf("save redirect = %d %q body=%q", saveResponse.Code, saveResponse.Header().Get("Location"), saveResponse.Body.String())
	}
	assertMobilePage(t, draftCookie, "/m/history", handler.HandleMobileHistory, "Mobile test event")
	assertMobilePage(t, draftCookie, saveResponse.Header().Get("Location"), handler.HandleMobileHistoryDetail, "Copy for parents")
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

func TestMobilePeopleAndPlacesHandlersCreateEditAndRender(t *testing.T) {
	handler, store := newTestManagementHandler(t)
	ctx := context.Background()

	assertMobilePage(t, nil, "/m/people", handler.HandleMobilePeople, "People")
	assertMobilePage(t, nil, "/m/people/participants/new", handler.HandleMobileParticipantForm, "Add participant")
	participantCreate := postMobileForm(t, nil, "/m/people/participants/new", url.Values{
		"name": {"Mobile Rider"}, "address": {"10 Rider Road"}, "address_name": {"Home"},
	}, handler.HandleMobileParticipantForm)
	assertMobileRedirect(t, participantCreate, "/m/people")
	participants, err := store.Participants().List(ctx, "Mobile Rider")
	if err != nil || len(participants) != 1 {
		t.Fatalf("created participants = %#v err=%v", participants, err)
	}
	participantEditPath := fmt.Sprintf("/m/people/participants/%d/edit", participants[0].ID)
	assertMobilePage(t, nil, participantEditPath, handler.HandleMobileParticipantForm, "Mobile Rider")
	participantEdit := postMobileForm(t, nil, participantEditPath, url.Values{
		"name": {"Mobile Rider Updated"}, "address": {"10 Rider Road"}, "address_name": {"Home"},
	}, handler.HandleMobileParticipantForm)
	assertMobileRedirect(t, participantEdit, "/m/people")

	assertMobilePage(t, nil, "/m/people/drivers/new", handler.HandleMobileDriverForm, `value="4"`)
	driverCreate := postMobileForm(t, nil, "/m/people/drivers/new", url.Values{
		"name": {"Mobile Driver"}, "address": {"20 Driver Road"}, "address_name": {"Home"}, "vehicle_capacity": {"4"},
	}, handler.HandleMobileDriverForm)
	assertMobileRedirect(t, driverCreate, "/m/people")
	drivers, err := store.Drivers().List(ctx, "Mobile Driver")
	if err != nil || len(drivers) != 1 {
		t.Fatalf("created drivers = %#v err=%v", drivers, err)
	}
	driverEditPath := fmt.Sprintf("/m/people/drivers/%d/edit", drivers[0].ID)
	assertMobilePage(t, nil, driverEditPath, handler.HandleMobileDriverForm, "Mobile Driver")
	driverEdit := postMobileForm(t, nil, driverEditPath, url.Values{
		"name": {"Mobile Driver Updated"}, "address": {"20 Driver Road"}, "address_name": {"Home"}, "vehicle_capacity": {"5"},
	}, handler.HandleMobileDriverForm)
	assertMobileRedirect(t, driverEdit, "/m/people")

	assertMobilePage(t, nil, "/m/places", handler.HandleMobilePlaces, "Places")
	assertMobilePage(t, nil, "/m/places/locations/new", handler.HandleMobileLocationForm, "Add location")
	locationCreate := postMobileForm(t, nil, "/m/places/locations/new", url.Values{
		"name": {"Mobile Hall"}, "address": {"30 Hall Road"},
	}, handler.HandleMobileLocationForm)
	assertMobileRedirect(t, locationCreate, "/m/places")
	locations, err := store.ActivityLocations().List(ctx)
	if err != nil || len(locations) != 1 {
		t.Fatalf("created locations = %#v err=%v", locations, err)
	}
	locationEditPath := fmt.Sprintf("/m/places/locations/%d/edit", locations[0].ID)
	assertMobilePage(t, nil, locationEditPath, handler.HandleMobileLocationForm, "Mobile Hall")
	locationEdit := postMobileForm(t, nil, locationEditPath, url.Values{
		"name": {"Mobile Hall Updated"}, "address": {"30 Hall Road"},
	}, handler.HandleMobileLocationForm)
	assertMobileRedirect(t, locationEdit, "/m/places")

	assertMobilePage(t, nil, "/m/places/vans/new", handler.HandleMobileVanForm, `value="8"`)
	vanCreate := postMobileForm(t, nil, "/m/places/vans/new", url.Values{
		"name": {"Mobile Van"}, "capacity": {"8"},
	}, handler.HandleMobileVanForm)
	assertMobileRedirect(t, vanCreate, "/m/places")
	vans, err := store.OrganizationVehicles().List(ctx)
	if err != nil || len(vans) != 1 {
		t.Fatalf("created vans = %#v err=%v", vans, err)
	}
	vanEditPath := fmt.Sprintf("/m/places/vans/%d/edit", vans[0].ID)
	assertMobilePage(t, nil, vanEditPath, handler.HandleMobileVanForm, "Mobile Van")
	vanEdit := postMobileForm(t, nil, vanEditPath, url.Values{
		"name": {"Mobile Van Updated"}, "capacity": {"9"},
	}, handler.HandleMobileVanForm)
	assertMobileRedirect(t, vanEdit, "/m/places")
}

func TestMobileValidationRerendersSubmittedValuesAndHTMLNotFound(t *testing.T) {
	handler, _ := newTestManagementHandler(t)
	tooLong := strings.Repeat("x", models.MaxAddressNameLength+1)
	response := postMobileForm(t, nil, "/m/people/participants/new", url.Values{
		"name": {"Submitted Rider"}, "address": {"10 Main Street"}, "address_name": {tooLong},
	}, handler.HandleMobileParticipantForm)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "Submitted Rider") || !strings.Contains(response.Body.String(), messageAddressNameTooLong()) {
		t.Fatalf("validation response = %d body=%q", response.Code, response.Body.String())
	}

	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/m/people/participants/999999/edit", nil)
	notFound := httptest.NewRecorder()
	handler.HandleMobileParticipantForm(notFound, request)
	if notFound.Code != http.StatusNotFound || !strings.Contains(notFound.Header().Get("Content-Type"), "text/html") || strings.Contains(notFound.Body.String(), `{"error"`) {
		t.Fatalf("not found = %d content-type=%q body=%q", notFound.Code, notFound.Header().Get("Content-Type"), notFound.Body.String())
	}
}

func TestMobileRidersPrunesSoftDeletedParticipantFromDraft(t *testing.T) {
	handler, store := newTestManagementHandler(t)
	handler.PlanDraft = plandraft.NewStore()
	t.Cleanup(handler.PlanDraft.Close)
	ctx := context.Background()
	deleted, err := store.Participants().Create(ctx, &models.Participant{Name: "Archived Mobile Rider", Address: "1 Old Road", Lat: 40, Lng: -73})
	if err != nil {
		t.Fatal(err)
	}
	active, err := store.Participants().Create(ctx, &models.Participant{Name: "Active Mobile Rider", Address: "2 New Road", Lat: 40.1, Lng: -73.1})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Participants().Delete(ctx, deleted.ID); err != nil {
		t.Fatal(err)
	}
	id := handler.PlanDraft.NewID()
	handler.PlanDraft.Update(id, func(d *plandraft.Draft) {
		d.ParticipantIDs = []int64{deleted.ID, active.ID}
	})
	cookie := mobileTestCookie(id)
	request := httptest.NewRequestWithContext(ctx, http.MethodGet, "/m/plan/riders", nil)
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	handler.HandleMobileRiders(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%q", response.Code, response.Body.String())
	}
	body := response.Body.String()
	if strings.Contains(body, deleted.Name) || !strings.Contains(body, active.Name) || !strings.Contains(body, "Some unavailable people were removed from this plan.") {
		t.Fatalf("riders body did not exclude deleted participant and show notice: %q", body)
	}
	draft, ok := handler.PlanDraft.Get(id)
	if !ok || len(draft.ParticipantIDs) != 1 || draft.ParticipantIDs[0] != active.ID {
		t.Fatalf("pruned draft = %#v ok=%v", draft, ok)
	}
}

func TestMobilePickerSearchPreservesSubmittedSelections(t *testing.T) {
	handler, store := newTestManagementHandler(t)
	handler.PlanDraft = plandraft.NewStore()
	t.Cleanup(handler.PlanDraft.Close)
	first, err := store.Participants().Create(context.Background(), &models.Participant{Name: "Alpha Rider", Address: "1 Main", Lat: 40, Lng: -73})
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Participants().Create(context.Background(), &models.Participant{Name: "Beta Rider", Address: "2 Main", Lat: 40.1, Lng: -73.1})
	if err != nil {
		t.Fatal(err)
	}
	id := handler.PlanDraft.NewID()
	handler.PlanDraft.Update(id, func(d *plandraft.Draft) { d.ParticipantIDs = []int64{first.ID, second.ID} })
	path := fmt.Sprintf("/m/plan/riders?search=Alpha&participant_ids=%d&participant_ids=%d", first.ID, second.ID)
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, nil)
	request.Header.Set("HX-Request", "true")
	request.AddCookie(mobileTestCookie(id))
	response := httptest.NewRecorder()
	handler.HandleMobileRiders(response, request)
	body := response.Body.String()
	if response.Code != http.StatusOK || !strings.Contains(body, `value="`+fmt.Sprint(first.ID)+`" checked`) || !strings.Contains(body, `type="hidden" name="participant_ids" value="`+fmt.Sprint(second.ID)+`"`) {
		t.Fatalf("picker response = %d body=%q", response.Code, body)
	}
}

func TestMobileDriversRejectsDuplicateVanAssignment(t *testing.T) {
	handler, store := newTestManagementHandler(t)
	handler.PlanDraft = plandraft.NewStore()
	t.Cleanup(handler.PlanDraft.Close)
	first, err := store.Drivers().Create(context.Background(), &models.Driver{Name: "First Driver", Address: "1 Main", Lat: 40, Lng: -73, VehicleCapacity: 4})
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Drivers().Create(context.Background(), &models.Driver{Name: "Second Driver", Address: "2 Main", Lat: 40.1, Lng: -73.1, VehicleCapacity: 4})
	if err != nil {
		t.Fatal(err)
	}
	van, err := store.OrganizationVehicles().Create(context.Background(), &models.OrganizationVehicle{Name: "Shared Van", Capacity: 8})
	if err != nil {
		t.Fatal(err)
	}
	id := handler.PlanDraft.NewID()
	handler.PlanDraft.Update(id, func(*plandraft.Draft) {})
	values := url.Values{
		"driver_ids":                             {fmt.Sprint(first.ID), fmt.Sprint(second.ID)},
		fmt.Sprintf("org_vehicle_%d", first.ID):  {fmt.Sprint(van.ID)},
		fmt.Sprintf("org_vehicle_%d", second.ID): {fmt.Sprint(van.ID)},
	}
	response := postMobileForm(t, mobileTestCookie(id), "/m/plan/drivers", values, handler.HandleMobileDrivers)
	target, err := url.Parse(response.Header().Get("Location"))
	if err != nil || response.Code != http.StatusSeeOther || target.Query().Get("error") != duplicateVanAssignmentMessage {
		t.Fatalf("duplicate van response = %d location=%q err=%v", response.Code, response.Header().Get("Location"), err)
	}
}

func TestMobileDraftAccessRefreshesCookieAndRouteMoveRejectsMalformedTargets(t *testing.T) {
	handler, _ := newTestManagementHandler(t)
	handler.PlanDraft = plandraft.NewStore()
	t.Cleanup(handler.PlanDraft.Close)
	id := handler.PlanDraft.NewID()
	handler.PlanDraft.Update(id, func(*plandraft.Draft) {})
	cookie := mobileTestCookie(id)

	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/m", nil)
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	handler.HandleMobilePlan(response, request)
	refreshed := response.Result().Cookies()
	if response.Code != http.StatusOK || len(refreshed) == 0 || refreshed[0].MaxAge != int(mobileDraftCookieMaxAge.Seconds()) {
		t.Fatalf("draft access = %d cookies=%#v", response.Code, refreshed)
	}

	for _, target := range []string{"", "garbage", "0"} {
		move := postMobileForm(t, cookie, "/m/routes/move", url.Values{
			"participant_id": {"1"}, "from_route_index": {"0"}, "to_route_index": {target},
		}, handler.HandleMobileMove)
		parsed, err := url.Parse(move.Header().Get("Location"))
		if err != nil || parsed.Query().Get("error") == "" {
			t.Fatalf("malformed move %q = %d location=%q err=%v", target, move.Code, move.Header().Get("Location"), err)
		}
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

func mobileTestCookie(id string) *http.Cookie {
	return &http.Cookie{
		Name: mobileDraftCookie, Value: id, Path: "/", Secure: true, HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	}
}

func assertMobilePage(t *testing.T, cookie *http.Cookie, path string, handler http.HandlerFunc, want string) {
	t.Helper()
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, nil)
	if cookie != nil {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	handler(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), want) {
		t.Fatalf("GET %s = %d body=%q, want %q", path, response.Code, response.Body.String(), want)
	}
}
