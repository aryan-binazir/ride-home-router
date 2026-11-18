package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"ride-home-router/internal/distance"
	"ride-home-router/internal/models"
	"strings"
	"testing"
)

func TestCalculateDistanceFailureMessages(t *testing.T) {
	h, store := newTestRouteHandler(t)
	ctx := t.Context()
	rider, err := store.Participants().Create(ctx, &models.Participant{Name: "Rider", Address: "1 Rider Rd", Lat: 40.1, Lng: -73.9})
	if err != nil {
		t.Fatal(err)
	}
	driver, err := store.Drivers().Create(ctx, &models.Driver{Name: "Driver", Address: "2 Driver Rd", Lat: 40.2, Lng: -73.8, VehicleCapacity: 4})
	if err != nil {
		t.Fatal(err)
	}
	location, err := store.ActivityLocations().Create(ctx, &models.ActivityLocation{Name: "Event", Address: "3 Event Ave", Lat: 42, Lng: -75})
	if err != nil {
		t.Fatal(err)
	}
	form := url.Values{"participant_ids": {fmt.Sprint(rider.ID)}, "driver_ids": {fmt.Sprint(driver.ID)}, "activity_location_id": {fmt.Sprint(location.ID)}, "route_time": {"18:30"}, "mode": {"dropoff"}}
	for _, tc := range []struct {
		name          string
		err           error
		status        int
		code, message string
	}{
		{"pair cap", fmt.Errorf("prewarm: %w", distance.ErrTooManyDistancePairs), http.StatusBadRequest, "TOO_MANY_DISTANCE_PAIRS", messageTooManyForOneCalculation},
		{"temporary provider timeout", fmt.Errorf("provider: %w", os.ErrDeadlineExceeded), http.StatusServiceUnavailable, "DISTANCE_PROVIDER_FAILED", messageRouteCalculationUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h.Router = &captureRouter{err: tc.err}
			for _, htmx := range []bool{false, true} {
				t.Run(fmt.Sprintf("desktop HTMX=%v", htmx), func(t *testing.T) {
					handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if htmx {
							r.Header.Set("HX-Request", "true")
						}
						h.HandleCalculateRoutes(w, r)
					})
					response := postForm(t, "/api/v1/routes/calculate", form, handler)
					if response.Code != tc.status {
						t.Fatalf("status = %d, body=%s", response.Code, response.Body)
					}
					if htmx {
						if !strings.Contains(response.Body.String(), tc.message) || !strings.Contains(response.Header().Get("HX-Trigger"), tc.message) {
							t.Fatalf("response = %s %s", response.Header(), response.Body)
						}
					} else {
						var body ErrorResponse
						if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
							t.Fatal(err)
						}
						if body.Error.Code != tc.code || body.Error.Message != tc.message {
							t.Fatalf("error = %#v", body.Error)
						}
					}
				})
			}
		})
	}
}
