package handlers

import (
	"log"
	"net/http"
	"ride-home-router/internal/geocoding"
	"ride-home-router/internal/httpx"
)

// HandleAddressSearch handles GET /api/v1/address-search
func (h *Handler) HandleAddressSearch(w http.ResponseWriter, r *http.Request) {
	// Require HTMX so another site cannot exhaust the shared Nominatim limit.
	if !h.isHTMX(r) {
		http.Error(w, messageForbidden, http.StatusForbidden)
		return
	}

	query := r.URL.Query().Get("address")
	log.Printf("[HTTP] GET /api/v1/address-search: outcome=started")

	if len(query) < 4 {
		log.Printf("[HTTP] GET /api/v1/address-search: query too short, returning empty HTML")
		w.Header().Set(httpx.HeaderContentType, httpx.MediaTypeHTML)
		w.WriteHeader(http.StatusOK)
		return
	}

	results, err := h.Geocoder.Search(r.Context(), query, 5)
	if err != nil {
		log.Printf("[ERROR] Failed to search addresses")
		w.Header().Set(httpx.HeaderContentType, httpx.MediaTypeHTML)
		w.WriteHeader(http.StatusOK)
		return
	}
	seenLabels := make(map[string]struct{}, len(results))
	uniqueResults := make([]geocoding.GeocodingResult, 0, len(results))
	for _, result := range results {
		label := result.Label()
		if _, exists := seenLabels[label]; exists {
			continue
		}
		seenLabels[label] = struct{}{}
		uniqueResults = append(uniqueResults, result)
	}
	results = uniqueResults

	//nolint:gosec // G706: dynamic values are numeric, boolean, or escaped with logutil.SafeString.
	log.Printf("[HTTP] GET /api/v1/address-search: results_count=%d", len(results))

	templateName := "address_suggestions.html"
	if r.Header.Get("X-RHR-Mobile") == "1" {
		templateName = "mobile_address_suggestions.html"
	}
	h.renderTemplate(w, templateName, results)
}
