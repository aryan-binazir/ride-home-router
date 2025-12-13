package handlers

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"

	"ride-home-router/internal/models"
	"ride-home-router/internal/routing"
)

// CalculateRoutesRequest represents the request for route calculation
type CalculateRoutesRequest struct {
	ParticipantIDs           []int64 `json:"participant_ids"`
	DriverIDs                []int64 `json:"driver_ids"`
	InstituteVehicleDriverID int64   `json:"institute_vehicle_driver_id,omitempty"`
}

// HandleCalculateRoutes handles POST /api/v1/routes/calculate
func (h *Handler) HandleCalculateRoutes(w http.ResponseWriter, r *http.Request) {
	var req CalculateRoutesRequest

	contentType := r.Header.Get("Content-Type")

	// Handle form data (from htmx)
	if strings.Contains(contentType, "application/x-www-form-urlencoded") || strings.Contains(contentType, "multipart/form-data") {
		if err := r.ParseForm(); err != nil {
			log.Printf("[HTTP] POST /api/v1/routes/calculate: form_parse_error err=%v", err)
			h.handleValidationError(w, "Invalid form data")
			return
		}

		// Parse participant_ids (multiple values with same name)
		for _, idStr := range r.Form["participant_ids"] {
			id, err := strconv.ParseInt(idStr, 10, 64)
			if err == nil {
				req.ParticipantIDs = append(req.ParticipantIDs, id)
			}
		}

		// Parse driver_ids (multiple values with same name)
		for _, idStr := range r.Form["driver_ids"] {
			id, err := strconv.ParseInt(idStr, 10, 64)
			if err == nil {
				req.DriverIDs = append(req.DriverIDs, id)
			}
		}

		// Parse institute_vehicle_driver_id (single value)
		if idStr := r.FormValue("institute_vehicle_driver_id"); idStr != "" {
			id, err := strconv.ParseInt(idStr, 10, 64)
			if err == nil {
				req.InstituteVehicleDriverID = id
			}
		}

		log.Printf("[HTTP] POST /api/v1/routes/calculate: form_data participants=%v drivers=%v", req.ParticipantIDs, req.DriverIDs)
	} else {
		// Handle JSON
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			log.Printf("[HTTP] POST /api/v1/routes/calculate: invalid_json err=%v", err)
			h.handleValidationError(w, "Invalid request body")
			return
		}
	}

	if len(req.ParticipantIDs) == 0 {
		log.Printf("[HTTP] POST /api/v1/routes/calculate: missing participants")
		h.handleValidationErrorHTMX(w, r, "Please select at least one participant.")
		return
	}

	if len(req.DriverIDs) == 0 {
		log.Printf("[HTTP] POST /api/v1/routes/calculate: missing drivers")
		h.handleValidationErrorHTMX(w, r, "Please select at least one driver.")
		return
	}

	log.Printf("[HTTP] POST /api/v1/routes/calculate: participants=%d drivers=%d", len(req.ParticipantIDs), len(req.DriverIDs))

	settings, err := h.DB.Settings().Get(r.Context())
	if err != nil {
		h.handleInternalError(w, err)
		return
	}

	if settings.SelectedActivityLocationID == 0 {
		h.handleValidationErrorHTMX(w, r, "Activity location not configured. Please set it in Settings.")
		return
	}

	// Get the selected activity location
	activityLocation, err := h.DB.ActivityLocations().GetByID(r.Context(), settings.SelectedActivityLocationID)
	if err != nil {
		h.handleInternalError(w, err)
		return
	}

	if activityLocation == nil {
		h.handleValidationErrorHTMX(w, r, "Selected activity location not found. Please update Settings.")
		return
	}

	participants, err := h.DB.Participants().GetByIDs(r.Context(), req.ParticipantIDs)
	if err != nil {
		h.handleInternalError(w, err)
		return
	}

	if len(participants) != len(req.ParticipantIDs) {
		h.handleValidationError(w, "Some participants not found")
		return
	}

	drivers, err := h.DB.Drivers().GetByIDs(r.Context(), req.DriverIDs)
	if err != nil {
		h.handleInternalError(w, err)
		return
	}

	if len(drivers) != len(req.DriverIDs) {
		h.handleValidationError(w, "Some drivers not found")
		return
	}

	regularDrivers := []models.Driver{}
	for _, d := range drivers {
		if !d.IsInstituteVehicle {
			regularDrivers = append(regularDrivers, d)
		}
	}

	instituteVehicle, err := h.DB.Drivers().GetInstituteVehicle(r.Context())
	if err != nil {
		h.handleInternalError(w, err)
		return
	}

	routingReq := &routing.RoutingRequest{
		InstituteCoords:          activityLocation.GetCoords(),
		Participants:             participants,
		Drivers:                  regularDrivers,
		InstituteVehicle:         instituteVehicle,
		InstituteVehicleDriverID: req.InstituteVehicleDriverID,
	}

	result, err := h.Router.CalculateRoutes(r.Context(), routingReq)
	if err != nil {
		if rerr, ok := err.(*routing.ErrRoutingFailed); ok {
			log.Printf("[ERROR] Routing failed: participants=%d unassigned=%d capacity=%d reason=%s", rerr.TotalParticipants, rerr.UnassignedCount, rerr.TotalCapacity, rerr.Reason)
			h.handleRoutingError(w, err)
			return
		}
		log.Printf("[ERROR] Route calculation failed: err=%v", err)
		h.handleInternalError(w, err)
		return
	}

	log.Printf("[HTTP] Routes calculated successfully: drivers=%d total_distance=%.0f", result.Summary.TotalDriversUsed, result.Summary.TotalDropoffDistanceMeters)

	// Create a session for route editing
	session := h.RouteSession.Create(result.Routes, activityLocation, settings.UseMiles)

	// Return HTML for htmx, JSON for API calls
	if h.isHTMX(r) {
		h.renderTemplate(w, "route_results", map[string]interface{}{
			"Routes":           result.Routes,
			"Summary":          result.Summary,
			"UseMiles":         settings.UseMiles,
			"ActivityLocation": activityLocation,
			"SessionID":        session.ID,
			"IsEditing":        false,
		})
		return
	}

	h.writeJSON(w, http.StatusOK, map[string]interface{}{
		"routes":     result.Routes,
		"summary":    result.Summary,
		"session_id": session.ID,
	})
}

// HandleGeocodeAddress handles POST /api/v1/geocode
func (h *Handler) HandleGeocodeAddress(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Address string `json:"address"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Printf("[HTTP] POST /api/v1/geocode: invalid_body err=%v", err)
		h.handleValidationError(w, "Invalid request body")
		return
	}

	if req.Address == "" {
		log.Printf("[HTTP] POST /api/v1/geocode: missing address")
		h.handleValidationError(w, "Address is required")
		return
	}

	log.Printf("[HTTP] POST /api/v1/geocode: address=%s", req.Address)
	result, err := h.Geocoder.GeocodeWithRetry(r.Context(), req.Address, 3)
	if err != nil {
		log.Printf("[ERROR] Failed to geocode address: address=%s err=%v", req.Address, err)
		h.handleGeocodingError(w, err)
		return
	}

	log.Printf("[HTTP] Geocoded address: address=%s lat=%.6f lng=%.6f", req.Address, result.Coords.Lat, result.Coords.Lng)
	response := map[string]interface{}{
		"address":      req.Address,
		"lat":          result.Coords.Lat,
		"lng":          result.Coords.Lng,
		"display_name": result.DisplayName,
	}

	h.writeJSON(w, http.StatusOK, response)
}
