package handlers

import (
	"log"
	"net/http"
)

// HandleAddressSearch handles GET /api/v1/address-search
func (h *Handler) HandleAddressSearch(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("address")
	log.Printf("[HTTP] GET /api/v1/address-search: query=%s", query)

	if len(query) < 4 {
		log.Printf("[HTTP] GET /api/v1/address-search: query too short, returning empty HTML")
		if h.isHTMX(r) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			return
		}
		h.writeJSON(w, http.StatusOK, []interface{}{})
		return
	}

	results, err := h.Geocoder.Search(r.Context(), query, 5)
	if err != nil {
		log.Printf("[ERROR] Failed to search addresses: query=%s err=%v", query, err)
		if h.isHTMX(r) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			return
		}
		h.writeJSON(w, http.StatusOK, []interface{}{})
		return
	}

	log.Printf("[HTTP] GET /api/v1/address-search: query=%s results_count=%d", query, len(results))

	if h.isHTMX(r) {
		h.renderTemplate(w, "address_suggestions.html", results)
		return
	}

	h.writeJSON(w, http.StatusOK, results)
}
