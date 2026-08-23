package handlers

import (
	"log"
	"net/http"
	"ride-home-router/internal/httpx"
)

// HandleAddressSearch handles GET /api/v1/address-search
func (h *Handler) HandleAddressSearch(w http.ResponseWriter, r *http.Request) {
	// GET is exempt from the server's Origin check, and every request here
	// costs a slot on the shared 1 req/sec Nominatim rate limiter — so a
	// cross-site page could wedge geocoding via no-preflight <img> requests.
	// The app's own autocomplete always goes through htmx.
	if !h.isHTMX(r) {
		http.Error(w, messageForbidden, http.StatusForbidden)
		return
	}

	query := r.URL.Query().Get("address")
	log.Printf("[HTTP] GET /api/v1/address-search: query=%s", query)

	if len(query) < 4 {
		log.Printf("[HTTP] GET /api/v1/address-search: query too short, returning empty HTML")
		if h.isHTMX(r) {
			w.Header().Set(httpx.HeaderContentType, httpx.MediaTypeHTML)
			w.WriteHeader(http.StatusOK)
			return
		}
		h.writeJSON(w, http.StatusOK, []any{})
		return
	}

	results, err := h.Geocoder.Search(r.Context(), query, 5)
	if err != nil {
		log.Printf("[ERROR] Failed to search addresses: query=%s err=%v", query, err)
		if h.isHTMX(r) {
			w.Header().Set(httpx.HeaderContentType, httpx.MediaTypeHTML)
			w.WriteHeader(http.StatusOK)
			return
		}
		h.writeJSON(w, http.StatusOK, []any{})
		return
	}

	log.Printf("[HTTP] GET /api/v1/address-search: query=%s results_count=%d", query, len(results))

	if h.isHTMX(r) {
		h.renderTemplate(w, "address_suggestions.html", results)
		return
	}

	h.writeJSON(w, http.StatusOK, results)
}
