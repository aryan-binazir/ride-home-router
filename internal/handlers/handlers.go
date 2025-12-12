package handlers

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"

	"ride-home-router/internal/database"
	"ride-home-router/internal/distance"
	"ride-home-router/internal/geocoding"
	"ride-home-router/internal/routing"
)

// Handler provides common handler utilities and dependencies
type Handler struct {
	DB           *database.DB
	Geocoder     geocoding.Geocoder
	DistanceCalc distance.DistanceCalculator
	Router       routing.Router
}

// ErrorResponse represents an API error
type ErrorResponse struct {
	Error ErrorDetail `json:"error"`
}

// ErrorDetail contains error information
type ErrorDetail struct {
	Code    string      `json:"code"`
	Message string      `json:"message"`
	Details interface{} `json:"details,omitempty"`
}

// isHTMX checks if the request is an htmx request
func (h *Handler) isHTMX(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true"
}

// writeJSON writes a JSON response
func (h *Handler) writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

// writeError writes a JSON error response
func (h *Handler) writeError(w http.ResponseWriter, status int, code, message string, details interface{}) {
	h.writeJSON(w, status, ErrorResponse{
		Error: ErrorDetail{
			Code:    code,
			Message: message,
			Details: details,
		},
	})
}

// handleNotFound handles 404 errors
func (h *Handler) handleNotFound(w http.ResponseWriter, message string) {
	h.writeError(w, http.StatusNotFound, "NOT_FOUND", message, nil)
}

// handleValidationError handles 400 errors
func (h *Handler) handleValidationError(w http.ResponseWriter, message string) {
	h.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", message, nil)
}

// handleGeocodingError handles 422 errors for geocoding failures
func (h *Handler) handleGeocodingError(w http.ResponseWriter, err error) {
	h.writeError(w, http.StatusUnprocessableEntity, "GEOCODING_FAILED", err.Error(), nil)
}

// handleRoutingError handles 422 errors for routing failures
func (h *Handler) handleRoutingError(w http.ResponseWriter, err error) {
	if rerr, ok := err.(*routing.ErrRoutingFailed); ok {
		h.writeError(w, http.StatusUnprocessableEntity, "ROUTING_FAILED", rerr.Reason, map[string]interface{}{
			"unassigned_count":   rerr.UnassignedCount,
			"total_capacity":     rerr.TotalCapacity,
			"total_participants": rerr.TotalParticipants,
		})
		return
	}
	h.writeError(w, http.StatusUnprocessableEntity, "ROUTING_FAILED", err.Error(), nil)
}

// handleConflict handles 409 errors
func (h *Handler) handleConflict(w http.ResponseWriter, message string) {
	h.writeError(w, http.StatusConflict, "CONFLICT", message, nil)
}

// handleInternalError handles 500 errors
func (h *Handler) handleInternalError(w http.ResponseWriter, err error) {
	fmt.Printf("Internal error: %v\n", err)
	h.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "An error occurred. Please try again.", nil)
}

// checkNotFound checks if an error is a not found error
func (h *Handler) checkNotFound(err error) bool {
	return err == sql.ErrNoRows
}
