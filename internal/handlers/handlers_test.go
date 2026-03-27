package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

type triggerHeader struct {
	ShowToast struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"showToast"`
}

type triggerHeaderWithEvent struct {
	ShowToast struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"showToast"`
	ParticipantCreated bool `json:"participantCreated"`
}

func TestHandleValidationErrorHTMX_SetsToastHeader(t *testing.T) {
	handler := &Handler{}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/routes/calculate", nil)
	req.Header.Set("HX-Request", "true")
	rec := httptest.NewRecorder()

	handler.handleValidationErrorHTMX(rec, req, "Please choose an activity location for this event.")

	res := rec.Result()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", res.StatusCode, http.StatusBadRequest)
	}

	var got triggerHeader
	if err := json.Unmarshal([]byte(res.Header.Get("HX-Trigger")), &got); err != nil {
		t.Fatalf("unmarshal HX-Trigger: %v", err)
	}
	if got.ShowToast.Message != "Please choose an activity location for this event." {
		t.Fatalf("toast message = %q", got.ShowToast.Message)
	}
	if got.ShowToast.Type != toastTypeError {
		t.Fatalf("toast type = %q, want %q", got.ShowToast.Type, toastTypeError)
	}
}

func TestSetHTMXToastWithEvent_SetsToastAndEvent(t *testing.T) {
	handler := &Handler{}
	rec := httptest.NewRecorder()

	handler.setHTMXToastWithEvent(rec, "participantCreated", "Participant 'Alex' added!", toastTypeSuccess)

	var got triggerHeaderWithEvent
	if err := json.Unmarshal([]byte(rec.Header().Get("HX-Trigger")), &got); err != nil {
		t.Fatalf("unmarshal HX-Trigger: %v", err)
	}
	if !got.ParticipantCreated {
		t.Fatal("expected participantCreated event to be true")
	}
	if got.ShowToast.Message != "Participant 'Alex' added!" {
		t.Fatalf("toast message = %q", got.ShowToast.Message)
	}
	if got.ShowToast.Type != toastTypeSuccess {
		t.Fatalf("toast type = %q, want %q", got.ShowToast.Type, toastTypeSuccess)
	}
}
