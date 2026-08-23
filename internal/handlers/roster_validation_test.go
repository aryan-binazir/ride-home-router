package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"ride-home-router/internal/models"
	"strconv"
	"strings"
	"testing"
)

func TestRosterHTMXValidationReturnsBadRequestToast(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		path       func(int64) string
		form       func() url.Values
		want       string
		createBase func(*testing.T, *Handler) int64
		invoke     func(*Handler, http.ResponseWriter, *http.Request)
	}{
		{
			name: "create participant address name", method: http.MethodPost,
			path: func(int64) string { return "/api/v1/participants" },
			form: func() url.Values {
				return participantValidationForm(strings.Repeat("x", models.MaxAddressNameLength+1))
			},
			want: messageAddressNameTooLong(), invoke: (*Handler).HandleCreateParticipant,
		},
		{
			name: "update participant address name", method: http.MethodPut,
			path: func(id int64) string { return "/api/v1/participants/" + strconv.FormatInt(id, 10) },
			form: func() url.Values {
				return participantValidationForm(strings.Repeat("x", models.MaxAddressNameLength+1))
			},
			want: messageAddressNameTooLong(), createBase: createValidationParticipant,
			invoke: (*Handler).HandleUpdateParticipant,
		},
		{
			name: "create driver address name", method: http.MethodPost,
			path: func(int64) string { return "/api/v1/drivers" },
			form: func() url.Values { return driverValidationForm(strings.Repeat("x", models.MaxAddressNameLength+1), 4) },
			want: messageAddressNameTooLong(), invoke: (*Handler).HandleCreateDriver,
		},
		{
			name: "update driver address name", method: http.MethodPut,
			path: func(id int64) string { return "/api/v1/drivers/" + strconv.FormatInt(id, 10) },
			form: func() url.Values { return driverValidationForm(strings.Repeat("x", models.MaxAddressNameLength+1), 4) },
			want: messageAddressNameTooLong(), createBase: createValidationDriver,
			invoke: (*Handler).HandleUpdateDriver,
		},
		{
			name: "create driver capacity", method: http.MethodPost,
			path: func(int64) string { return "/api/v1/drivers" },
			form: func() url.Values { return driverValidationForm("", models.MaxVehicleCapacity+1) },
			want: messageVehicleCapacityOutOfRange(), invoke: (*Handler).HandleCreateDriver,
		},
		{
			name: "update driver capacity", method: http.MethodPut,
			path: func(id int64) string { return "/api/v1/drivers/" + strconv.FormatInt(id, 10) },
			form: func() url.Values { return driverValidationForm("", models.MaxVehicleCapacity+1) },
			want: messageVehicleCapacityOutOfRange(), createBase: createValidationDriver,
			invoke: (*Handler).HandleUpdateDriver,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler, _ := newTestManagementHandler(t)
			var id int64
			if test.createBase != nil {
				id = test.createBase(t, handler)
			}
			req := httptest.NewRequestWithContext(context.Background(), test.method, test.path(id), strings.NewReader(test.form().Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set("HX-Request", "true")
			recorder := httptest.NewRecorder()

			test.invoke(handler, recorder, req)

			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 body=%q", recorder.Code, recorder.Body.String())
			}
			if !strings.Contains(recorder.Header().Get("HX-Trigger"), test.want) {
				t.Fatalf("HX-Trigger = %q, want %q", recorder.Header().Get("HX-Trigger"), test.want)
			}
		})
	}
}

func participantValidationForm(addressName string) url.Values {
	return url.Values{"name": {"Participant"}, "address": {"1 Rider Way"}, "address_name": {addressName}}
}

func driverValidationForm(addressName string, capacity int) url.Values {
	return url.Values{
		"name": {"Driver"}, "address": {"1 Driver Way"}, "address_name": {addressName},
		"vehicle_capacity": {strconv.Itoa(capacity)},
	}
}

func createValidationParticipant(t *testing.T, handler *Handler) int64 {
	t.Helper()
	participant, err := handler.DB.Participants().Create(context.Background(), &models.Participant{Name: "Participant", Address: "1 Rider Way", Lat: 40, Lng: -73})
	if err != nil {
		t.Fatalf("create participant: %v", err)
	}
	return participant.ID
}

func createValidationDriver(t *testing.T, handler *Handler) int64 {
	t.Helper()
	driver, err := handler.DB.Drivers().Create(context.Background(), &models.Driver{Name: "Driver", Address: "1 Driver Way", Lat: 40, Lng: -73, VehicleCapacity: 4})
	if err != nil {
		t.Fatalf("create driver: %v", err)
	}
	return driver.ID
}
