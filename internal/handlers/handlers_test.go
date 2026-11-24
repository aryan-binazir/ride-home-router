package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aryan-binazir/ride-home-router/internal/geocoding"
	"github.com/aryan-binazir/ride-home-router/internal/httpx"
)

var errAddressSearchGeocodeUnused = errors.New("address search geocoder does not implement geocoding")

type addressSearchGeocoder struct {
	results []geocoding.GeocodingResult
}

func (g addressSearchGeocoder) Geocode(context.Context, string) (*geocoding.GeocodingResult, error) {
	return nil, errAddressSearchGeocodeUnused
}

func (g addressSearchGeocoder) GeocodeWithRetry(context.Context, string, int) (*geocoding.GeocodingResult, error) {
	return nil, errAddressSearchGeocodeUnused
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

	t.Run("short query returns an empty fragment", func(t *testing.T) {
		h := &Handler{}
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/address-search?address=123", nil)
		req.Header.Set(httpx.HeaderHXRequest, httpx.HTMXTrue)
		w := httptest.NewRecorder()
		h.HandleAddressSearch(w, req)
		if w.Code != http.StatusOK || w.Body.Len() != 0 || w.Header().Get(httpx.HeaderContentType) != httpx.MediaTypeHTML {
			t.Fatalf("short query: status=%d header=%v body=%s", w.Code, w.Header(), w.Body.String())
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

	t.Run("duplicate rendered addresses appear once", func(t *testing.T) {
		handler.Geocoder = addressSearchGeocoder{results: []geocoding.GeocodingResult{
			{FormattedAddress: "123 Main Street", DisplayName: "First source"},
			{FormattedAddress: "123 Main Street", DisplayName: "Second source"},
			{FormattedAddress: "456 Oak Avenue"},
		}}
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/address-search?address=1234", nil)
		req.Header.Set(httpx.HeaderHXRequest, httpx.HTMXTrue)
		rec := httptest.NewRecorder()

		handler.HandleAddressSearch(rec, req)

		if body := rec.Body.String(); rec.Code != http.StatusOK || strings.Count(body, `class="address-suggestion"`) != 2 {
			t.Fatalf("deduplicated response = %d body=%q", rec.Code, body)
		}
	})
}

func TestNoSwapConflictIncludesReadableMessage(t *testing.T) {
	h := &Handler{}
	r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/routes/move", nil)
	r.Header.Set("HX-Request", "true")
	w := httptest.NewRecorder()
	h.handleHTMXErrorNoSwap(w, r, 409, "CONFLICT", "Review <this> plan and try again.")
	if w.Code != 409 || !strings.Contains(w.Body.String(), "Review &lt;this&gt; plan and try again.") {
		t.Fatalf("response: %d %s", w.Code, w.Body.String())
	}
}

func TestInternalErrorContractDoesNotLeak(t *testing.T) {
	h := &Handler{}
	r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/participants", nil)
	r.Header.Set("HX-Request", "true")
	w := httptest.NewRecorder()
	h.renderError(w, r, errors.New(`ERROR: relation "x" does not exist (SQLSTATE 42P01)`))
	if strings.Contains(w.Body.String(), "SQLSTATE") {
		t.Fatalf("internal error leaked: %s", w.Body.String())
	}
	if !strings.Contains(w.Header().Get("HX-Trigger"), "showToast") {
		t.Fatal("missing toast")
	}
}
