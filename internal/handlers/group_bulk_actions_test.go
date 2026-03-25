package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"ride-home-router/internal/models"
)

func TestHandleAddParticipantsToGroup_HTMXAddsMembershipsIdempotently(t *testing.T) {
	handler, store := newTestManagementHandler(t)

	group, err := store.Groups().Create(context.Background(), &models.Group{Name: "Youth Conference"})
	if err != nil {
		t.Fatalf("create group: %v", err)
	}
	participantOne, err := store.Participants().Create(context.Background(), &models.Participant{
		Name:    "Participant One",
		Address: "1 Rider Way",
		Lat:     40.1,
		Lng:     -73.9,
	})
	if err != nil {
		t.Fatalf("create participant one: %v", err)
	}
	participantTwo, err := store.Participants().Create(context.Background(), &models.Participant{
		Name:    "Participant Two",
		Address: "2 Rider Way",
		Lat:     40.2,
		Lng:     -73.8,
	})
	if err != nil {
		t.Fatalf("create participant two: %v", err)
	}

	form := url.Values{}
	form.Set("group_id", int64ToString(group.ID))
	form.Add("participant_ids", int64ToString(participantOne.ID))
	form.Add("participant_ids", int64ToString(participantTwo.ID))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/participants/groups/add", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	rr := httptest.NewRecorder()

	handler.HandleAddParticipantsToGroup(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%q", rr.Code, http.StatusOK, rr.Body.String())
	}
	expectedTrigger := `{"showToast":{"message":"2 participants added to 'Youth Conference'.","type":"success"}}`
	if got := rr.Header().Get("HX-Trigger"); got != expectedTrigger {
		t.Fatalf("HX-Trigger = %q, want %q", got, expectedTrigger)
	}

	groupsOne, err := store.Groups().ListGroupsForParticipant(context.Background(), participantOne.ID)
	if err != nil {
		t.Fatalf("ListGroupsForParticipant(participantOne): %v", err)
	}
	if len(groupsOne) != 1 || groupsOne[0].ID != group.ID {
		t.Fatalf("participant one groups = %#v, want one group %d", groupsOne, group.ID)
	}

	secondReq := httptest.NewRequest(http.MethodPost, "/api/v1/participants/groups/add", strings.NewReader(form.Encode()))
	secondReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	secondReq.Header.Set("HX-Request", "true")
	secondRR := httptest.NewRecorder()

	handler.HandleAddParticipantsToGroup(secondRR, secondReq)

	if secondRR.Code != http.StatusOK {
		t.Fatalf("second status = %d, want %d body=%q", secondRR.Code, http.StatusOK, secondRR.Body.String())
	}
	groupsTwo, err := store.Groups().ListGroupsForParticipant(context.Background(), participantTwo.ID)
	if err != nil {
		t.Fatalf("ListGroupsForParticipant(participantTwo): %v", err)
	}
	if len(groupsTwo) != 1 || groupsTwo[0].ID != group.ID {
		t.Fatalf("participant two groups after repeated add = %#v, want one group %d", groupsTwo, group.ID)
	}
}

func TestHandleRemoveDriversFromGroup_HTMXRemovesMembershipsIdempotently(t *testing.T) {
	handler, store := newTestManagementHandler(t)

	group, err := store.Groups().Create(context.Background(), &models.Group{Name: "Summer Camp"})
	if err != nil {
		t.Fatalf("create group: %v", err)
	}
	driver, err := store.Drivers().Create(context.Background(), &models.Driver{
		Name:            "Driver One",
		Address:         "1 Driver Way",
		Lat:             40.1,
		Lng:             -73.9,
		VehicleCapacity: 4,
	})
	if err != nil {
		t.Fatalf("create driver: %v", err)
	}
	if err := store.Groups().AddGroupToDrivers(context.Background(), group.ID, []int64{driver.ID}); err != nil {
		t.Fatalf("AddGroupToDrivers: %v", err)
	}

	form := url.Values{}
	form.Set("group_id", int64ToString(group.ID))
	form.Add("driver_ids", int64ToString(driver.ID))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/drivers/groups/remove", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	rr := httptest.NewRecorder()

	handler.HandleRemoveDriversFromGroup(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%q", rr.Code, http.StatusOK, rr.Body.String())
	}
	expectedTrigger := `{"showToast":{"message":"1 driver removed from 'Summer Camp'.","type":"success"}}`
	if got := rr.Header().Get("HX-Trigger"); got != expectedTrigger {
		t.Fatalf("HX-Trigger = %q, want %q", got, expectedTrigger)
	}

	driverGroups, err := store.Groups().ListGroupsForDriver(context.Background(), driver.ID)
	if err != nil {
		t.Fatalf("ListGroupsForDriver(): %v", err)
	}
	if len(driverGroups) != 0 {
		t.Fatalf("driver groups = %#v, want none", driverGroups)
	}

	secondReq := httptest.NewRequest(http.MethodPost, "/api/v1/drivers/groups/remove", strings.NewReader(form.Encode()))
	secondReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	secondReq.Header.Set("HX-Request", "true")
	secondRR := httptest.NewRecorder()

	handler.HandleRemoveDriversFromGroup(secondRR, secondReq)

	if secondRR.Code != http.StatusOK {
		t.Fatalf("second status = %d, want %d body=%q", secondRR.Code, http.StatusOK, secondRR.Body.String())
	}
}
