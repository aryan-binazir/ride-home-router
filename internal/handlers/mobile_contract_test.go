package handlers

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"ride-home-router/internal/httpx"
	"ride-home-router/internal/models"
	"ride-home-router/internal/plandraft"
	"ride-home-router/internal/routesession"
	"strings"
	"testing"
)

func TestMobileDraftRejectsUnknownCookieAndRefreshesKnownCookie(t *testing.T) {
	handler := &Handler{PlanDraft: plandraft.NewStore()}
	t.Cleanup(handler.PlanDraft.Close)

	unknownRequest := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/m", nil)
	unknownRequest.AddCookie(mobileTestCookie("0123456789abcdef0123456789abcdef"))
	unknownResponse := httptest.NewRecorder()
	newID, _, notice := handler.mobileDraft(unknownResponse, unknownRequest)
	if newID == "0123456789abcdef0123456789abcdef" || notice == "" {
		t.Fatalf("unknown cookie returned id=%q notice=%q", newID, notice)
	}

	knownRequest := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/m", nil)
	knownRequest.AddCookie(mobileTestCookie(newID))
	knownResponse := httptest.NewRecorder()
	gotID, _, notice := handler.mobileDraft(knownResponse, knownRequest)
	if gotID != newID || notice != "" {
		t.Fatalf("known cookie returned id=%q notice=%q", gotID, notice)
	}
	cookies := knownResponse.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != mobileDraftCookie || cookies[0].Value != newID || cookies[0].MaxAge <= 0 {
		t.Fatalf("refreshed cookies = %#v", cookies)
	}
}

func TestMobilePickerSearchPreservesUnsavedSelectionsAndAssignments(t *testing.T) {
	handler, store := newTestManagementHandler(t)
	handler.PlanDraft = plandraft.NewStore()
	t.Cleanup(handler.PlanDraft.Close)
	ctx := context.Background()
	firstRider, _ := store.Participants().Create(ctx, &models.Participant{Name: "Alpha Rider", Address: "1 Rider", Lat: 1, Lng: 1})
	secondRider, _ := store.Participants().Create(ctx, &models.Participant{Name: "Beta Rider", Address: "2 Rider", Lat: 2, Lng: 2})
	firstDriver, _ := store.Drivers().Create(ctx, &models.Driver{Name: "Alpha Driver", Address: "1 Driver", Lat: 1, Lng: 1, VehicleCapacity: 4})
	secondDriver, _ := store.Drivers().Create(ctx, &models.Driver{Name: "Beta Driver", Address: "2 Driver", Lat: 2, Lng: 2, VehicleCapacity: 4})
	van, _ := store.OrganizationVehicles().Create(ctx, &models.OrganizationVehicle{Name: "Shared Van", Capacity: 8})
	id := handler.PlanDraft.NewID()
	handler.PlanDraft.Update(id, func(*plandraft.Draft) {})
	cookie := mobileTestCookie(id)

	riderQuery := url.Values{"search": {"no match"}, "participant_ids": {fmt.Sprint(firstRider.ID), fmt.Sprint(secondRider.ID)}}
	riders := getMobileHTMX(t, cookie, "/m/plan/riders?"+riderQuery.Encode(), handler.HandleMobileRiders)
	for _, riderID := range []int64{firstRider.ID, secondRider.ID} {
		if !strings.Contains(riders.Body.String(), fmt.Sprintf(`type="hidden" name="participant_ids" value="%d"`, riderID)) {
			t.Fatalf("rider search dropped selection %d: %s", riderID, riders.Body.String())
		}
	}

	driverQuery := url.Values{
		"search": {"Alpha"}, "driver_ids": {fmt.Sprint(firstDriver.ID), fmt.Sprint(secondDriver.ID)},
		fmt.Sprintf("org_vehicle_%d", firstDriver.ID):  {fmt.Sprint(van.ID)},
		fmt.Sprintf("org_vehicle_%d", secondDriver.ID): {fmt.Sprint(van.ID + 1)},
	}
	drivers := getMobileHTMX(t, cookie, "/m/plan/drivers?"+driverQuery.Encode(), handler.HandleMobileDrivers)
	body := drivers.Body.String()
	if strings.Contains(body, fmt.Sprintf(`type="hidden" name="driver_ids" value="%d"`, firstDriver.ID)) {
		t.Fatalf("visible driver %d was also rendered hidden: %s", firstDriver.ID, body)
	}
	for _, want := range []string{
		fmt.Sprintf(`type="hidden" name="driver_ids" value="%d"`, secondDriver.ID),
		fmt.Sprintf(`name="org_vehicle_%d"`, firstDriver.ID),
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("driver search missing %q: %s", want, body)
		}
	}
}

func TestParseOrgVehicleAssignmentsRejectsDuplicateVanOnMobileFields(t *testing.T) {
	form := url.Values{
		"org_vehicle_1": {"9"},
		"org_vehicle_2": {"9"},
	}
	if _, err := parseOrgVehicleAssignments(form, []int64{1, 2}); err == nil || err.Error() != duplicateVanAssignmentMessage {
		t.Fatalf("parseOrgVehicleAssignments() error = %v, want %q", err, duplicateVanAssignmentMessage)
	}
}

func TestMobileMoveRejectsSameAndMalformedDestinationWithoutMutation(t *testing.T) {
	store := routesession.NewStore(routeEditDistanceCalculator{})
	t.Cleanup(store.Close)
	draftStore := plandraft.NewStore()
	t.Cleanup(draftStore.Close)
	driver := models.Driver{ID: 1, Name: "Driver", VehicleCapacity: 4}
	rider := models.Participant{ID: 2, Name: "Rider", Address: "1 Rider", Lat: 1, Lng: 1}
	session := store.Create(routesession.CreateInput{Routes: []models.CalculatedRoute{{Driver: &driver, EffectiveCapacity: 4, Stops: []models.RouteStop{{Participant: &rider}}}}, SelectedDrivers: []models.Driver{driver}, ActivityLocation: &models.ActivityLocation{ID: 1, Name: "HQ"}, RouteTime: "18:30", Mode: models.RouteModeDropoff})
	id := draftStore.NewID()
	draftStore.Update(id, func(d *plandraft.Draft) { d.RouteSessionID = session.ID })
	handler := &Handler{PlanDraft: draftStore, RouteSession: store}
	cookie := mobileTestCookie(id)

	for _, destination := range []string{"0", "bad"} {
		response := postMobileForm(t, cookie, "/m/routes/move", url.Values{"participant_id": {"2"}, "from_route_index": {"0"}, "to_route_index": {destination}}, handler.HandleMobileMove)
		if response.Code != http.StatusSeeOther || !strings.HasPrefix(response.Header().Get("Location"), "/m/routes?error=") {
			t.Fatalf("destination %q response = %d %q", destination, response.Code, response.Header().Get("Location"))
		}
		snapshot, ok := store.Snapshot(session.ID)
		if !ok || snapshot.IsEditing || len(snapshot.Routes[0].Stops) != 1 || snapshot.Routes[0].Stops[0].Participant.ID != rider.ID {
			t.Fatalf("destination %q mutated snapshot: %#v", destination, snapshot)
		}
	}
}

func getMobileHTMX(t *testing.T, cookie *http.Cookie, path string, serve http.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, nil)
	request.Header.Set(httpx.HeaderHXRequest, httpx.HTMXTrue)
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	serve(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("GET %s = %d body=%q", path, response.Code, response.Body.String())
	}
	return response
}
