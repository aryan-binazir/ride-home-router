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

func TestHandleCreateLabel_HTMXTrimsAndRendersList(t *testing.T) {
	handler, store := newTestManagementHandler(t)

	form := url.Values{}
	form.Set("name", "  Youth Conference  ")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/labels", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	rr := httptest.NewRecorder()

	handler.HandleCreateLabel(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%q", rr.Code, http.StatusOK, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "Youth Conference") {
		t.Fatalf("expected rendered label list, body=%q", rr.Body.String())
	}
	labels, err := store.Labels().List(context.Background())
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(labels) != 1 || labels[0].Name != "Youth Conference" {
		t.Fatalf("labels = %#v, want one trimmed label", labels)
	}
}

func TestHandleUpdateLabel_HTMXRejectsDuplicateName(t *testing.T) {
	handler, store := newTestManagementHandler(t)

	first, err := store.Labels().Create(context.Background(), &models.Label{Name: "First"})
	if err != nil {
		t.Fatalf("create first label: %v", err)
	}
	if _, err := store.Labels().Create(context.Background(), &models.Label{Name: "Second"}); err != nil {
		t.Fatalf("create second label: %v", err)
	}

	form := url.Values{}
	form.Set("name", "Second")
	req := httptest.NewRequest(http.MethodPut, "/api/v1/labels/"+int64ToString(first.ID), strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	rr := httptest.NewRecorder()

	handler.HandleUpdateLabel(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d body=%q", rr.Code, http.StatusBadRequest, rr.Body.String())
	}
	if !strings.Contains(rr.Header().Get("HX-Trigger"), messageDuplicateLabelName) {
		t.Fatalf("expected duplicate label toast, HX-Trigger=%q", rr.Header().Get("HX-Trigger"))
	}
}

func TestHandleAddParticipantsToLabel_HTMXAddsMembershipsIdempotently(t *testing.T) {
	handler, store := newTestManagementHandler(t)

	label, err := store.Labels().Create(context.Background(), &models.Label{Name: "Summer Camp"})
	if err != nil {
		t.Fatalf("create label: %v", err)
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
	form.Set("label_id", int64ToString(label.ID))
	form.Add("participant_ids", int64ToString(participantOne.ID))
	form.Add("participant_ids", int64ToString(participantTwo.ID))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/participants/labels/add", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	rr := httptest.NewRecorder()

	handler.HandleAddParticipantsToLabel(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%q", rr.Code, http.StatusOK, rr.Body.String())
	}
	if !strings.Contains(rr.Header().Get("HX-Trigger"), "2 participants added to") {
		t.Fatalf("expected bulk success toast, HX-Trigger=%q", rr.Header().Get("HX-Trigger"))
	}

	secondReq := httptest.NewRequest(http.MethodPost, "/api/v1/participants/labels/add", strings.NewReader(form.Encode()))
	secondReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	secondReq.Header.Set("HX-Request", "true")
	secondRR := httptest.NewRecorder()

	handler.HandleAddParticipantsToLabel(secondRR, secondReq)

	if secondRR.Code != http.StatusOK {
		t.Fatalf("second status = %d, want %d body=%q", secondRR.Code, http.StatusOK, secondRR.Body.String())
	}
	labelIDs, err := store.Labels().ListLabelIDsForParticipants(context.Background())
	if err != nil {
		t.Fatalf("ListLabelIDsForParticipants() error = %v", err)
	}
	if got := labelIDs[participantOne.ID]; len(got) != 1 || got[0] != label.ID {
		t.Fatalf("participant one label IDs = %#v, want [%d]", got, label.ID)
	}
	if got := labelIDs[participantTwo.ID]; len(got) != 1 || got[0] != label.ID {
		t.Fatalf("participant two label IDs = %#v, want [%d]", got, label.ID)
	}
}

func TestHandleRemoveDriversFromLabel_HTMXRemovesMembershipsIdempotently(t *testing.T) {
	handler, store := newTestManagementHandler(t)

	label, err := store.Labels().Create(context.Background(), &models.Label{Name: "Drivers"})
	if err != nil {
		t.Fatalf("create label: %v", err)
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
	if err := store.Labels().AddLabelToDrivers(context.Background(), label.ID, []int64{driver.ID}); err != nil {
		t.Fatalf("AddLabelToDrivers() error = %v", err)
	}

	form := url.Values{}
	form.Set("label_id", int64ToString(label.ID))
	form.Add("driver_ids", int64ToString(driver.ID))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/drivers/labels/remove", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	rr := httptest.NewRecorder()

	handler.HandleRemoveDriversFromLabel(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%q", rr.Code, http.StatusOK, rr.Body.String())
	}
	driverLabels, err := store.Labels().ListLabelsForDriver(context.Background(), driver.ID)
	if err != nil {
		t.Fatalf("ListLabelsForDriver() error = %v", err)
	}
	if len(driverLabels) != 0 {
		t.Fatalf("driver labels = %#v, want empty", driverLabels)
	}

	secondReq := httptest.NewRequest(http.MethodPost, "/api/v1/drivers/labels/remove", strings.NewReader(form.Encode()))
	secondReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	secondReq.Header.Set("HX-Request", "true")
	secondRR := httptest.NewRecorder()

	handler.HandleRemoveDriversFromLabel(secondRR, secondReq)

	if secondRR.Code != http.StatusOK {
		t.Fatalf("second status = %d, want %d body=%q", secondRR.Code, http.StatusOK, secondRR.Body.String())
	}
}

func TestHandleParticipantForm_RendersSelectedLabels(t *testing.T) {
	handler, store := newTestManagementHandler(t)

	label, err := store.Labels().Create(context.Background(), &models.Label{Name: "Youth Conference"})
	if err != nil {
		t.Fatalf("create label: %v", err)
	}
	participant, err := store.Participants().Create(context.Background(), &models.Participant{
		Name:    "Participant One",
		Address: "1 Rider Way",
		Lat:     40.1,
		Lng:     -73.9,
	})
	if err != nil {
		t.Fatalf("create participant: %v", err)
	}
	if err := store.Labels().SetLabelsForParticipant(context.Background(), participant.ID, []int64{label.ID}); err != nil {
		t.Fatalf("SetLabelsForParticipant() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/participants/"+int64ToString(participant.ID)+"/edit", nil)
	req.Header.Set("HX-Request", "true")
	rr := httptest.NewRecorder()

	handler.HandleParticipantForm(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%q", rr.Code, http.StatusOK, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, `name="label_ids"`) || !strings.Contains(body, `checked`) {
		t.Fatalf("expected selected label checkbox, body=%q", body)
	}
}
