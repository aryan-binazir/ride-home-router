package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandleValidationErrorHTMX_SetsToastHeader(t *testing.T) {
	handler := &Handler{}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/routes/calculate", nil)
	req.Header.Set("HX-Request", "true")
	rec := httptest.NewRecorder()

	handler.handleValidationErrorHTMX(rec, req, "Activity location not configured. Please set it in Settings.")

	res := rec.Result()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", res.StatusCode, http.StatusBadRequest)
	}

	expectedTrigger := `{"showToast":{"message":"Activity location not configured. Please set it in Settings.","type":"error"}}`
	if got := res.Header.Get("HX-Trigger"); got != expectedTrigger {
		t.Fatalf("HX-Trigger = %q, want %q", got, expectedTrigger)
	}
}
