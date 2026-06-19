package handlers

import (
	"context"
	"encoding/json"
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

func TestHandleUpdateLabel_JSONReturnsMembershipCounts(t *testing.T) {
	handler, store := newTestManagementHandler(t)

	label, err := store.Labels().Create(context.Background(), &models.Label{Name: "Old"})
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

	req := httptest.NewRequest(http.MethodPut, "/api/v1/labels/"+int64ToString(label.ID), strings.NewReader(`{"name":"New"}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handler.HandleUpdateLabel(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%q", rr.Code, http.StatusOK, rr.Body.String())
	}
	var response models.Label
	if err := json.NewDecoder(rr.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Name != "New" || response.ParticipantCount != 1 {
		t.Fatalf("response = %#v, want renamed label with participant count", response)
	}
}

func TestValidateLabelIDsAcceptsDuplicateExistingLabels(t *testing.T) {
	handler, store := newTestManagementHandler(t)

	label, err := store.Labels().Create(context.Background(), &models.Label{Name: "Youth Conference"})
	if err != nil {
		t.Fatalf("create label: %v", err)
	}

	if err := handler.validateLabelIDs(context.Background(), []int64{label.ID, label.ID}); err != nil {
		t.Fatalf("validateLabelIDs() error = %v, want nil", err)
	}
}

func TestValidateLabelIDsRejectsNonPositiveLabelIDs(t *testing.T) {
	handler, _ := newTestManagementHandler(t)

	if err := handler.validateLabelIDs(context.Background(), []int64{0}); err == nil {
		t.Fatal("validateLabelIDs() error = nil, want invalid label error")
	}
}

func TestValidateLabelIDsRejectsMissingLabelIDs(t *testing.T) {
	handler, _ := newTestManagementHandler(t)

	if err := handler.validateLabelIDs(context.Background(), []int64{9999}); err == nil {
		t.Fatal("validateLabelIDs() error = nil, want missing label error")
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

func TestHandleAddParticipantsToLabel_HTMXRejectsInvalidParticipantIDWithoutPartialMutation(t *testing.T) {
	handler, store := newTestManagementHandler(t)

	label, err := store.Labels().Create(context.Background(), &models.Label{Name: "Summer Camp"})
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

	form := url.Values{}
	form.Set("label_id", int64ToString(label.ID))
	form.Add("participant_ids", int64ToString(participant.ID))
	form.Add("participant_ids", "9999")

	req := httptest.NewRequest(http.MethodPost, "/api/v1/participants/labels/add", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	rr := httptest.NewRecorder()

	handler.HandleAddParticipantsToLabel(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d body=%q", rr.Code, http.StatusBadRequest, rr.Body.String())
	}
	if got := rr.Header().Get("HX-Reswap"); got != "none" {
		t.Fatalf("HX-Reswap = %q, want %q", got, "none")
	}
	labels, err := store.Labels().ListLabelsForParticipant(context.Background(), participant.ID)
	if err != nil {
		t.Fatalf("ListLabelsForParticipant() error = %v", err)
	}
	if len(labels) != 0 {
		t.Fatalf("participant labels = %#v, want no partial mutation", labels)
	}
}

func TestHandleRemoveParticipantsFromLabel_HTMXRejectsInvalidParticipantIDWithoutPartialMutation(t *testing.T) {
	handler, store := newTestManagementHandler(t)

	label, err := store.Labels().Create(context.Background(), &models.Label{Name: "Summer Camp"})
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

	form := url.Values{}
	form.Set("label_id", int64ToString(label.ID))
	form.Add("participant_ids", int64ToString(participant.ID))
	form.Add("participant_ids", "9999")

	req := httptest.NewRequest(http.MethodPost, "/api/v1/participants/labels/remove", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	rr := httptest.NewRecorder()

	handler.HandleRemoveParticipantsFromLabel(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d body=%q", rr.Code, http.StatusBadRequest, rr.Body.String())
	}
	labels, err := store.Labels().ListLabelsForParticipant(context.Background(), participant.ID)
	if err != nil {
		t.Fatalf("ListLabelsForParticipant() error = %v", err)
	}
	if len(labels) != 1 || labels[0].ID != label.ID {
		t.Fatalf("participant labels = %#v, want original label preserved", labels)
	}
}

func TestHandleAddDriversToLabel_HTMXRejectsInvalidDriverIDWithoutPartialMutation(t *testing.T) {
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

	form := url.Values{}
	form.Set("label_id", int64ToString(label.ID))
	form.Add("driver_ids", int64ToString(driver.ID))
	form.Add("driver_ids", "9999")

	req := httptest.NewRequest(http.MethodPost, "/api/v1/drivers/labels/add", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	rr := httptest.NewRecorder()

	handler.HandleAddDriversToLabel(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d body=%q", rr.Code, http.StatusBadRequest, rr.Body.String())
	}
	labels, err := store.Labels().ListLabelsForDriver(context.Background(), driver.ID)
	if err != nil {
		t.Fatalf("ListLabelsForDriver() error = %v", err)
	}
	if len(labels) != 0 {
		t.Fatalf("driver labels = %#v, want no partial mutation", labels)
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

func TestHandleRemoveDriversFromLabel_HTMXRejectsInvalidDriverIDWithoutPartialMutation(t *testing.T) {
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
	if err := store.Labels().SetLabelsForDriver(context.Background(), driver.ID, []int64{label.ID}); err != nil {
		t.Fatalf("SetLabelsForDriver() error = %v", err)
	}

	form := url.Values{}
	form.Set("label_id", int64ToString(label.ID))
	form.Add("driver_ids", int64ToString(driver.ID))
	form.Add("driver_ids", "9999")

	req := httptest.NewRequest(http.MethodPost, "/api/v1/drivers/labels/remove", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	rr := httptest.NewRecorder()

	handler.HandleRemoveDriversFromLabel(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d body=%q", rr.Code, http.StatusBadRequest, rr.Body.String())
	}
	labels, err := store.Labels().ListLabelsForDriver(context.Background(), driver.ID)
	if err != nil {
		t.Fatalf("ListLabelsForDriver() error = %v", err)
	}
	if len(labels) != 1 || labels[0].ID != label.ID {
		t.Fatalf("driver labels = %#v, want original label preserved", labels)
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

func TestHandleListParticipants_HTMXRendersAssignedLabels(t *testing.T) {
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

	req := httptest.NewRequest(http.MethodGet, "/api/v1/participants", nil)
	req.Header.Set("HX-Request", "true")
	rr := httptest.NewRecorder()

	handler.HandleListParticipants(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%q", rr.Code, http.StatusOK, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "Youth Conference") {
		t.Fatalf("expected assigned label name in participant list, body=%q", rr.Body.String())
	}
}

func TestHandleListDrivers_HTMXRendersAssignedLabels(t *testing.T) {
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
	if err := store.Labels().SetLabelsForDriver(context.Background(), driver.ID, []int64{label.ID}); err != nil {
		t.Fatalf("SetLabelsForDriver() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/drivers", nil)
	req.Header.Set("HX-Request", "true")
	rr := httptest.NewRecorder()

	handler.HandleListDrivers(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%q", rr.Code, http.StatusOK, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "Drivers") {
		t.Fatalf("expected assigned label name in driver list, body=%q", rr.Body.String())
	}
}

func TestHandleUpdateParticipant_JSONOmittedLabelIDsPreservesLabels(t *testing.T) {
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

	body := `{"name":"Participant One Updated","address":"1 Rider Way"}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/participants/"+int64ToString(participant.ID), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handler.HandleUpdateParticipant(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%q", rr.Code, http.StatusOK, rr.Body.String())
	}
	labels, err := store.Labels().ListLabelsForParticipant(context.Background(), participant.ID)
	if err != nil {
		t.Fatalf("ListLabelsForParticipant() error = %v", err)
	}
	if len(labels) != 1 || labels[0].ID != label.ID {
		t.Fatalf("participant labels = %#v, want existing label preserved", labels)
	}
}

func TestHandleUpdateDriver_JSONOmittedLabelIDsPreservesLabels(t *testing.T) {
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
	if err := store.Labels().SetLabelsForDriver(context.Background(), driver.ID, []int64{label.ID}); err != nil {
		t.Fatalf("SetLabelsForDriver() error = %v", err)
	}

	body := `{"name":"Driver One Updated","address":"1 Driver Way","vehicle_capacity":4}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/drivers/"+int64ToString(driver.ID), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handler.HandleUpdateDriver(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%q", rr.Code, http.StatusOK, rr.Body.String())
	}
	labels, err := store.Labels().ListLabelsForDriver(context.Background(), driver.ID)
	if err != nil {
		t.Fatalf("ListLabelsForDriver() error = %v", err)
	}
	if len(labels) != 1 || labels[0].ID != label.ID {
		t.Fatalf("driver labels = %#v, want existing label preserved", labels)
	}
}

func TestHandleGetDriver_JSONIncludesLabelIDs(t *testing.T) {
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
	if err := store.Labels().SetLabelsForDriver(context.Background(), driver.ID, []int64{label.ID}); err != nil {
		t.Fatalf("SetLabelsForDriver() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/drivers/"+int64ToString(driver.ID), nil)
	rr := httptest.NewRecorder()

	handler.HandleGetDriver(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%q", rr.Code, http.StatusOK, rr.Body.String())
	}
	var response struct {
		ID       int64   `json:"id"`
		LabelIDs []int64 `json:"label_ids"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.ID != driver.ID || len(response.LabelIDs) != 1 || response.LabelIDs[0] != label.ID {
		t.Fatalf("response = %#v, want driver with label_ids [%d]", response, label.ID)
	}
}

func TestHandleListDrivers_JSONIncludesLabelIDs(t *testing.T) {
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
	if err := store.Labels().SetLabelsForDriver(context.Background(), driver.ID, []int64{label.ID}); err != nil {
		t.Fatalf("SetLabelsForDriver() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/drivers", nil)
	rr := httptest.NewRecorder()

	handler.HandleListDrivers(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%q", rr.Code, http.StatusOK, rr.Body.String())
	}
	var response struct {
		Drivers []struct {
			ID       int64   `json:"id"`
			LabelIDs []int64 `json:"label_ids"`
		} `json:"drivers"`
		Total int `json:"total"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Total != 1 || len(response.Drivers) != 1 {
		t.Fatalf("response = %#v, want one driver", response)
	}
	got := response.Drivers[0]
	if got.ID != driver.ID || len(got.LabelIDs) != 1 || got.LabelIDs[0] != label.ID {
		t.Fatalf("driver response = %#v, want label_ids [%d]", got, label.ID)
	}
}

func TestHandleGetParticipant_JSONIncludesLabelIDs(t *testing.T) {
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

	req := httptest.NewRequest(http.MethodGet, "/api/v1/participants/"+int64ToString(participant.ID), nil)
	rr := httptest.NewRecorder()

	handler.HandleGetParticipant(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%q", rr.Code, http.StatusOK, rr.Body.String())
	}
	var response struct {
		ID       int64   `json:"id"`
		LabelIDs []int64 `json:"label_ids"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.ID != participant.ID || len(response.LabelIDs) != 1 || response.LabelIDs[0] != label.ID {
		t.Fatalf("response = %#v, want participant with label_ids [%d]", response, label.ID)
	}
}

func TestHandleListParticipants_JSONIncludesLabelIDs(t *testing.T) {
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

	req := httptest.NewRequest(http.MethodGet, "/api/v1/participants", nil)
	rr := httptest.NewRecorder()

	handler.HandleListParticipants(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%q", rr.Code, http.StatusOK, rr.Body.String())
	}
	var response struct {
		Participants []struct {
			ID       int64   `json:"id"`
			LabelIDs []int64 `json:"label_ids"`
		} `json:"participants"`
		Total int `json:"total"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Total != 1 || len(response.Participants) != 1 {
		t.Fatalf("response = %#v, want one participant", response)
	}
	got := response.Participants[0]
	if got.ID != participant.ID || len(got.LabelIDs) != 1 || got.LabelIDs[0] != label.ID {
		t.Fatalf("participant response = %#v, want label_ids [%d]", got, label.ID)
	}
}

func TestHandleCreateParticipant_JSONReturnsLabelIDs(t *testing.T) {
	handler, store := newTestManagementHandler(t)

	label, err := store.Labels().Create(context.Background(), &models.Label{Name: "Youth Conference"})
	if err != nil {
		t.Fatalf("create label: %v", err)
	}

	body := `{"name":"Participant One","address":"1 Rider Way","label_ids":[` + int64ToString(label.ID) + `]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/participants", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handler.HandleCreateParticipant(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d body=%q", rr.Code, http.StatusCreated, rr.Body.String())
	}
	var response struct {
		ID       int64   `json:"id"`
		LabelIDs []int64 `json:"label_ids"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.ID == 0 || len(response.LabelIDs) != 1 || response.LabelIDs[0] != label.ID {
		t.Fatalf("response = %#v, want created participant with label_ids [%d]", response, label.ID)
	}
}

func TestHandleCreateDriver_JSONReturnsLabelIDs(t *testing.T) {
	handler, store := newTestManagementHandler(t)

	label, err := store.Labels().Create(context.Background(), &models.Label{Name: "Drivers"})
	if err != nil {
		t.Fatalf("create label: %v", err)
	}

	body := `{"name":"Driver One","address":"1 Driver Way","vehicle_capacity":4,"label_ids":[` + int64ToString(label.ID) + `]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/drivers", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handler.HandleCreateDriver(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d body=%q", rr.Code, http.StatusCreated, rr.Body.String())
	}
	var response struct {
		ID       int64   `json:"id"`
		LabelIDs []int64 `json:"label_ids"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.ID == 0 || len(response.LabelIDs) != 1 || response.LabelIDs[0] != label.ID {
		t.Fatalf("response = %#v, want created driver with label_ids [%d]", response, label.ID)
	}
}

func TestHandleUpdateParticipant_JSONReturnsLabelIDs(t *testing.T) {
	handler, store := newTestManagementHandler(t)

	oldLabel, err := store.Labels().Create(context.Background(), &models.Label{Name: "Old"})
	if err != nil {
		t.Fatalf("create old label: %v", err)
	}
	newLabel, err := store.Labels().Create(context.Background(), &models.Label{Name: "New"})
	if err != nil {
		t.Fatalf("create new label: %v", err)
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
	if err := store.Labels().SetLabelsForParticipant(context.Background(), participant.ID, []int64{oldLabel.ID}); err != nil {
		t.Fatalf("SetLabelsForParticipant() error = %v", err)
	}

	body := `{"name":"Participant One Updated","address":"1 Rider Way","label_ids":[` + int64ToString(newLabel.ID) + `]}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/participants/"+int64ToString(participant.ID), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handler.HandleUpdateParticipant(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%q", rr.Code, http.StatusOK, rr.Body.String())
	}
	var response struct {
		ID       int64   `json:"id"`
		LabelIDs []int64 `json:"label_ids"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.ID != participant.ID || len(response.LabelIDs) != 1 || response.LabelIDs[0] != newLabel.ID {
		t.Fatalf("response = %#v, want updated participant with label_ids [%d]", response, newLabel.ID)
	}
}

func TestHandleUpdateDriver_JSONReturnsLabelIDs(t *testing.T) {
	handler, store := newTestManagementHandler(t)

	oldLabel, err := store.Labels().Create(context.Background(), &models.Label{Name: "Old"})
	if err != nil {
		t.Fatalf("create old label: %v", err)
	}
	newLabel, err := store.Labels().Create(context.Background(), &models.Label{Name: "New"})
	if err != nil {
		t.Fatalf("create new label: %v", err)
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
	if err := store.Labels().SetLabelsForDriver(context.Background(), driver.ID, []int64{oldLabel.ID}); err != nil {
		t.Fatalf("SetLabelsForDriver() error = %v", err)
	}

	body := `{"name":"Driver One Updated","address":"1 Driver Way","vehicle_capacity":4,"label_ids":[` + int64ToString(newLabel.ID) + `]}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/drivers/"+int64ToString(driver.ID), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handler.HandleUpdateDriver(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%q", rr.Code, http.StatusOK, rr.Body.String())
	}
	var response struct {
		ID       int64   `json:"id"`
		LabelIDs []int64 `json:"label_ids"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.ID != driver.ID || len(response.LabelIDs) != 1 || response.LabelIDs[0] != newLabel.ID {
		t.Fatalf("response = %#v, want updated driver with label_ids [%d]", response, newLabel.ID)
	}
}

func TestHandleUpdateParticipant_JSONEmptyLabelIDsClearsLabels(t *testing.T) {
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

	body := `{"name":"Participant One Updated","address":"1 Rider Way","label_ids":[]}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/participants/"+int64ToString(participant.ID), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handler.HandleUpdateParticipant(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%q", rr.Code, http.StatusOK, rr.Body.String())
	}
	var response struct {
		LabelIDs []int64 `json:"label_ids"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(response.LabelIDs) != 0 {
		t.Fatalf("response label_ids = %#v, want empty", response.LabelIDs)
	}
	labels, err := store.Labels().ListLabelsForParticipant(context.Background(), participant.ID)
	if err != nil {
		t.Fatalf("ListLabelsForParticipant() error = %v", err)
	}
	if len(labels) != 0 {
		t.Fatalf("participant labels = %#v, want cleared", labels)
	}
}

func TestHandleUpdateDriver_JSONEmptyLabelIDsClearsLabels(t *testing.T) {
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
	if err := store.Labels().SetLabelsForDriver(context.Background(), driver.ID, []int64{label.ID}); err != nil {
		t.Fatalf("SetLabelsForDriver() error = %v", err)
	}

	body := `{"name":"Driver One Updated","address":"1 Driver Way","vehicle_capacity":4,"label_ids":[]}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/drivers/"+int64ToString(driver.ID), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handler.HandleUpdateDriver(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%q", rr.Code, http.StatusOK, rr.Body.String())
	}
	var response struct {
		LabelIDs []int64 `json:"label_ids"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(response.LabelIDs) != 0 {
		t.Fatalf("response label_ids = %#v, want empty", response.LabelIDs)
	}
	labels, err := store.Labels().ListLabelsForDriver(context.Background(), driver.ID)
	if err != nil {
		t.Fatalf("ListLabelsForDriver() error = %v", err)
	}
	if len(labels) != 0 {
		t.Fatalf("driver labels = %#v, want cleared", labels)
	}
}

func TestHandleUpdateParticipant_JSONInvalidLabelDoesNotMutateParticipant(t *testing.T) {
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

	body := `{"name":"Changed","address":"1 Rider Way","label_ids":[9999]}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/participants/"+int64ToString(participant.ID), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handler.HandleUpdateParticipant(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d body=%q", rr.Code, http.StatusBadRequest, rr.Body.String())
	}
	unchanged, err := store.Participants().GetByID(context.Background(), participant.ID)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if unchanged.Name != "Participant One" {
		t.Fatalf("participant name = %q, want unchanged", unchanged.Name)
	}
	labels, err := store.Labels().ListLabelsForParticipant(context.Background(), participant.ID)
	if err != nil {
		t.Fatalf("ListLabelsForParticipant() error = %v", err)
	}
	if len(labels) != 1 || labels[0].ID != label.ID {
		t.Fatalf("participant labels = %#v, want existing label preserved", labels)
	}
}

func TestHandleCreateParticipant_JSONInvalidLabelDoesNotCreateParticipant(t *testing.T) {
	handler, store := newTestManagementHandler(t)

	body := `{"name":"Participant One","address":"1 Rider Way","label_ids":[9999]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/participants", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handler.HandleCreateParticipant(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d body=%q", rr.Code, http.StatusBadRequest, rr.Body.String())
	}
	participants, err := store.Participants().List(context.Background(), "")
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(participants) != 0 {
		t.Fatalf("participants = %#v, want none created", participants)
	}
}

func TestHandleUpdateDriver_JSONInvalidLabelDoesNotMutateDriver(t *testing.T) {
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
	if err := store.Labels().SetLabelsForDriver(context.Background(), driver.ID, []int64{label.ID}); err != nil {
		t.Fatalf("SetLabelsForDriver() error = %v", err)
	}

	body := `{"name":"Changed","address":"1 Driver Way","vehicle_capacity":4,"label_ids":[9999]}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/drivers/"+int64ToString(driver.ID), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handler.HandleUpdateDriver(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d body=%q", rr.Code, http.StatusBadRequest, rr.Body.String())
	}
	unchanged, err := store.Drivers().GetByID(context.Background(), driver.ID)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if unchanged.Name != "Driver One" {
		t.Fatalf("driver name = %q, want unchanged", unchanged.Name)
	}
	labels, err := store.Labels().ListLabelsForDriver(context.Background(), driver.ID)
	if err != nil {
		t.Fatalf("ListLabelsForDriver() error = %v", err)
	}
	if len(labels) != 1 || labels[0].ID != label.ID {
		t.Fatalf("driver labels = %#v, want existing label preserved", labels)
	}
}

func TestHandleCreateDriver_JSONInvalidLabelDoesNotCreateDriver(t *testing.T) {
	handler, store := newTestManagementHandler(t)

	body := `{"name":"Driver One","address":"1 Driver Way","vehicle_capacity":4,"label_ids":[9999]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/drivers", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handler.HandleCreateDriver(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d body=%q", rr.Code, http.StatusBadRequest, rr.Body.String())
	}
	drivers, err := store.Drivers().List(context.Background(), "")
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(drivers) != 0 {
		t.Fatalf("drivers = %#v, want none created", drivers)
	}
}
