package handlers

import (
	"encoding/json"
	"net/http"

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

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.handleValidationError(w, "Invalid request body")
		return
	}

	if len(req.ParticipantIDs) == 0 {
		h.handleValidationError(w, "At least one participant is required")
		return
	}

	if len(req.DriverIDs) == 0 {
		h.handleValidationError(w, "At least one driver is required")
		return
	}

	settings, err := h.DB.SettingsRepository.Get(r.Context())
	if err != nil {
		h.handleInternalError(w, err)
		return
	}

	if settings.InstituteAddress == "" {
		h.handleValidationError(w, "Institute address not configured")
		return
	}

	participants, err := h.DB.ParticipantRepository.GetByIDs(r.Context(), req.ParticipantIDs)
	if err != nil {
		h.handleInternalError(w, err)
		return
	}

	if len(participants) != len(req.ParticipantIDs) {
		h.handleValidationError(w, "Some participants not found")
		return
	}

	drivers, err := h.DB.DriverRepository.GetByIDs(r.Context(), req.DriverIDs)
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

	instituteVehicle, err := h.DB.DriverRepository.GetInstituteVehicle(r.Context())
	if err != nil {
		h.handleInternalError(w, err)
		return
	}

	routingReq := &routing.RoutingRequest{
		InstituteCoords:          settings.GetCoords(),
		Participants:             participants,
		Drivers:                  regularDrivers,
		InstituteVehicle:         instituteVehicle,
		InstituteVehicleDriverID: req.InstituteVehicleDriverID,
	}

	result, err := h.Router.CalculateRoutes(r.Context(), routingReq)
	if err != nil {
		if _, ok := err.(*routing.ErrRoutingFailed); ok {
			h.handleRoutingError(w, err)
			return
		}
		h.handleInternalError(w, err)
		return
	}

	h.writeJSON(w, http.StatusOK, result)
}

// HandleGeocodeAddress handles POST /api/v1/geocode
func (h *Handler) HandleGeocodeAddress(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Address string `json:"address"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.handleValidationError(w, "Invalid request body")
		return
	}

	if req.Address == "" {
		h.handleValidationError(w, "Address is required")
		return
	}

	result, err := h.Geocoder.GeocodeWithRetry(r.Context(), req.Address, 3)
	if err != nil {
		h.handleGeocodingError(w, err)
		return
	}

	response := map[string]interface{}{
		"address":      req.Address,
		"lat":          result.Coords.Lat,
		"lng":          result.Coords.Lng,
		"display_name": result.DisplayName,
	}

	h.writeJSON(w, http.StatusOK, response)
}
