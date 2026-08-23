package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"ride-home-router/internal/geocoding"
	"ride-home-router/internal/httpx"
	"strings"
	"testing"
)

type addressSearchGeocoder struct {
	results []geocoding.GeocodingResult
}

func (g addressSearchGeocoder) Geocode(context.Context, string) (*geocoding.GeocodingResult, error) {
	return nil, nil
}

func (g addressSearchGeocoder) GeocodeWithRetry(context.Context, string, int) (*geocoding.GeocodingResult, error) {
	return nil, nil
}

func (g addressSearchGeocoder) Search(context.Context, string, int) ([]geocoding.GeocodingResult, error) {
	return g.results, nil
}

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
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/routes/calculate", nil)
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

func TestHandleAddressSearchRequiresHTMXAndRendersHTML(t *testing.T) {
	handler := &Handler{
		Geocoder: addressSearchGeocoder{results: []geocoding.GeocodingResult{
			{FormattedAddress: "123 Main Street"},
		}},
		Renderer: loadEmbeddedTemplates(t),
	}

	t.Run("non-HTMX request is forbidden", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/address-search?address=1234", nil)
		rec := httptest.NewRecorder()

		handler.HandleAddressSearch(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
		}
	})

	t.Run("HTMX request renders address suggestions HTML", func(t *testing.T) {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/address-search?address=1234", nil)
		req.Header.Set(httpx.HeaderHXRequest, httpx.HTMXTrue)
		rec := httptest.NewRecorder()

		handler.HandleAddressSearch(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
		}
		if got := rec.Header().Get(httpx.HeaderContentType); got != httpx.MediaTypeHTML {
			t.Fatalf("Content-Type = %q, want %q", got, httpx.MediaTypeHTML)
		}
		if body := rec.Body.String(); !strings.Contains(body, "123 Main Street") {
			t.Fatalf("body = %q, want rendered address suggestion", body)
		}
	})
}
