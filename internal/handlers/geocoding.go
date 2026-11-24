package handlers

import (
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"strings"

	"github.com/aryan-binazir/ride-home-router/internal/geocoding"
	"github.com/aryan-binazir/ride-home-router/internal/httpx"
)

func (h *Handler) HandleAddressSearch(w http.ResponseWriter, r *http.Request) {
	// Require HTMX so another site cannot spend the Google Places quota through this endpoint.
	if !h.isHTMX(r) {
		http.Error(w, messageForbidden, http.StatusForbidden)
		return
	}

	query := strings.TrimSpace(r.URL.Query().Get("address"))
	slog.Info("Address search started",
		"component", "http",
		"method", "GET",
		"path", "/api/v1/address-search",
		"outcome", "started",
		"query_len", len(query),
	)

	if len(query) < 4 {
		slog.Info("Query too short, returning empty HTML", "component", "http", "method", "GET", "path", "/api/v1/address-search")
		w.Header().Set(httpx.HeaderContentType, httpx.MediaTypeHTML)
		w.WriteHeader(http.StatusOK)
		return
	}

	results, err := h.Geocoder.Search(r.Context(), query, 5)
	if err != nil {
		slog.Error("Failed to search addresses")
		h.setHTMXToast(w, geocodingErrorMessage(err), toastTypeError)
		w.Header().Set(httpx.HeaderContentType, httpx.MediaTypeHTML)
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, `<div class="address-no-results" role="alert">%s</div>`, html.EscapeString(geocodingErrorMessage(err)))
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

	slog.Info("Address search completed",
		"component", "http",
		"method", "GET",
		"path", "/api/v1/address-search",
		"results_count", len(results),
	)

	h.renderTemplate(w, "address_suggestions.html", results)
}
