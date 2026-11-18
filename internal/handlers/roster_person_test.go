package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRosterJSONCapacityIsDriverOnly(t *testing.T) {
	h, _ := newTestManagementHandler(t)
	const body = `{"name":"Alex","address":"1 Oak Way","vehicle_capacity":"invalid"}`

	t.Run("participant ignores the driver-only field", func(t *testing.T) {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/participants", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		res := httptest.NewRecorder()
		h.HandleCreateParticipant(res, req)

		if res.Code != http.StatusCreated || strings.Contains(res.Body.String(), "vehicle_capacity") {
			t.Fatalf("participant response = %d %s", res.Code, res.Body.String())
		}
	})

	t.Run("driver rejects malformed capacity", func(t *testing.T) {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/drivers", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		res := httptest.NewRecorder()
		h.HandleCreateDriver(res, req)

		want := `{"error":{"code":"VALIDATION_ERROR","message":"` + messageInvalidRequestBody + `"}}` + "\n"
		if res.Code != http.StatusBadRequest || res.Body.String() != want {
			t.Fatalf("driver response = %d %q, want 400 %q", res.Code, res.Body.String(), want)
		}
	})
}
