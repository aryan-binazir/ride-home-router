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

func TestMobileDriverPickerFilteredPostPreservesSelectionsAndAssignments(t *testing.T) {
	for _, test := range []struct {
		name            string
		assignHiddenVan bool
	}{
		{name: "hidden driver uses personal vehicle"},
		{name: "hidden driver uses organization van", assignHiddenVan: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler, store := newTestManagementHandler(t)
			handler.PlanDraft = plandraft.NewStore()
			t.Cleanup(handler.PlanDraft.Close)
			ctx := context.Background()
			visible, err := store.Drivers().Create(ctx, &models.Driver{Name: "Alpha Driver", Address: "1 Driver", Lat: 1, Lng: 1, VehicleCapacity: 4})
			if err != nil {
				t.Fatal(err)
			}
			hidden, err := store.Drivers().Create(ctx, &models.Driver{Name: "Beta Driver", Address: "2 Driver", Lat: 2, Lng: 2, VehicleCapacity: 4})
			if err != nil {
				t.Fatal(err)
			}
			van, err := store.OrganizationVehicles().Create(ctx, &models.OrganizationVehicle{Name: "Organization Van", Capacity: 8})
			if err != nil {
				t.Fatal(err)
			}
			id := handler.PlanDraft.NewID()
			handler.PlanDraft.Update(id, func(*plandraft.Draft) {})
			cookie := mobileTestCookie(id)

			query := url.Values{
				"search":     {"Alpha"},
				"driver_ids": {fmt.Sprint(visible.ID), fmt.Sprint(hidden.ID)},
			}
			if test.assignHiddenVan {
				query.Set(fmt.Sprintf("org_vehicle_%d", hidden.ID), fmt.Sprint(van.ID))
			}
			filtered := getMobileHTMX(t, cookie, "/m/plan/drivers?"+query.Encode(), handler.HandleMobileDrivers)
			body := filtered.Body.String()
			hiddenID, ok := hiddenInputValue(body, "driver_ids")
			if !ok || hiddenID != fmt.Sprint(hidden.ID) {
				t.Fatalf("filtered picker hidden driver = %q, found=%v body=%q", hiddenID, ok, body)
			}

			form := url.Values{"driver_ids": {fmt.Sprint(visible.ID), hiddenID}}
			assignmentName := fmt.Sprintf("org_vehicle_%d", hidden.ID)
			if assignment, rendered := hiddenInputValue(body, assignmentName); rendered {
				form.Set(assignmentName, assignment)
			}
			response := postMobileForm(t, cookie, "/m/plan/drivers", form, handler.HandleMobileDrivers)
			assertMobileRedirect(t, response, "/m")

			draft, ok := handler.PlanDraft.Get(id)
			if !ok || len(draft.DriverIDs) != 2 || draft.DriverIDs[0] != visible.ID || draft.DriverIDs[1] != hidden.ID {
				t.Fatalf("saved driver IDs = %#v, draft found=%v", draft.DriverIDs, ok)
			}
			if test.assignHiddenVan {
				if len(draft.DriverVehicleIDs) != 1 || draft.DriverVehicleIDs[hidden.ID] != van.ID {
					t.Fatalf("saved van assignments = %#v, want driver %d assigned van %d", draft.DriverVehicleIDs, hidden.ID, van.ID)
				}
			} else if len(draft.DriverVehicleIDs) != 0 {
				t.Fatalf("saved van assignments = %#v, want none", draft.DriverVehicleIDs)
			}
		})
	}
}

func TestMobileDriverPickerHTMXSearchPreservesAssignmentWithHiddenVanlessDriver(t *testing.T) {
	handler, store := newTestManagementHandler(t)
	handler.PlanDraft = plandraft.NewStore()
	t.Cleanup(handler.PlanDraft.Close)
	ctx := context.Background()
	visible, err := store.Drivers().Create(ctx, &models.Driver{Name: "Alpha Driver", Address: "1 Driver", Lat: 1, Lng: 1, VehicleCapacity: 4})
	if err != nil {
		t.Fatal(err)
	}
	hidden, err := store.Drivers().Create(ctx, &models.Driver{Name: "Beta Driver", Address: "2 Driver", Lat: 2, Lng: 2, VehicleCapacity: 4})
	if err != nil {
		t.Fatal(err)
	}
	van, err := store.OrganizationVehicles().Create(ctx, &models.OrganizationVehicle{Name: "Organization Van", Capacity: 8})
	if err != nil {
		t.Fatal(err)
	}
	id := handler.PlanDraft.NewID()
	handler.PlanDraft.Update(id, func(*plandraft.Draft) {})
	cookie := mobileTestCookie(id)
	assignmentName := fmt.Sprintf("org_vehicle_%d", visible.ID)
	query := url.Values{
		"search":       {"Alpha"},
		"driver_ids":   {fmt.Sprint(visible.ID), fmt.Sprint(hidden.ID)},
		assignmentName: {fmt.Sprint(van.ID)},
	}

	firstResponse := getMobileHTMX(t, cookie, "/m/plan/drivers?"+query.Encode(), handler.HandleMobileDrivers)
	body := firstResponse.Body.String()
	hiddenID, ok := hiddenInputValue(body, "driver_ids")
	if !ok || hiddenID != fmt.Sprint(hidden.ID) {
		t.Fatalf("filtered picker hidden driver = %q, found=%v body=%q", hiddenID, ok, body)
	}
	if hiddenAssignment, rendered := hiddenInputValue(body, fmt.Sprintf("org_vehicle_%d", hidden.ID)); rendered {
		query.Set(fmt.Sprintf("org_vehicle_%d", hidden.ID), hiddenAssignment)
	}

	secondResponse := getMobileHTMX(t, cookie, "/m/plan/drivers?"+query.Encode(), handler.HandleMobileDrivers)
	body = secondResponse.Body.String()
	if strings.Contains(body, invalidVanAssignmentMessage) || !strings.Contains(body, fmt.Sprintf(`<option value="%d" selected>`, van.ID)) {
		t.Fatalf("HTMX search did not preserve in-flight van assignment: %s", body)
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

func hiddenInputValue(body, name string) (string, bool) {
	prefix := fmt.Sprintf(`<input type="hidden" name="%s" value="`, name)
	start := strings.Index(body, prefix)
	if start < 0 {
		return "", false
	}
	start += len(prefix)
	end := strings.Index(body[start:], `"`)
	if end < 0 {
		return "", false
	}
	return body[start : start+end], true
}
