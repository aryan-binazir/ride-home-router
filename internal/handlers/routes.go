package handlers

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"

	"ride-home-router/internal/models"
	"ride-home-router/internal/routing"
)

// CalculateRoutesRequest represents the request for route calculation
type CalculateRoutesRequest struct {
	ParticipantIDs []int64 `json:"participant_ids"`
	DriverIDs      []int64 `json:"driver_ids"`
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

	// Parse mode (default to "dropoff" if not provided)
	mode := r.FormValue("mode")
	if mode == "" {
		mode = "dropoff"
	}

	log.Printf("[HTTP] POST /api/v1/routes/calculate: participants=%d drivers=%d mode=%s", len(req.ParticipantIDs), len(req.DriverIDs), mode)

	settings, err := h.DB.Settings().Get(r.Context())
	if err != nil {
		h.handleInternalError(w, err)
		return
	}

	if settings.SelectedActivityLocationID == 0 {
		log.Printf("[HTTP] POST /api/v1/routes/calculate: activity location not configured")
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
		log.Printf("[HTTP] POST /api/v1/routes/calculate: activity location id=%d not found", settings.SelectedActivityLocationID)
		h.handleValidationErrorHTMX(w, r, "Selected activity location not found. Please update Settings.")
		return
	}

	participants, err := h.DB.Participants().GetByIDs(r.Context(), req.ParticipantIDs)
	if err != nil {
		h.handleInternalError(w, err)
		return
	}

	if len(participants) != len(req.ParticipantIDs) {
		log.Printf("[HTTP] POST /api/v1/routes/calculate: participants mismatch requested=%d found=%d", len(req.ParticipantIDs), len(participants))
		h.handleValidationError(w, "Some participants not found")
		return
	}

	drivers, err := h.DB.Drivers().GetByIDs(r.Context(), req.DriverIDs)
	if err != nil {
		h.handleInternalError(w, err)
		return
	}

	if len(drivers) != len(req.DriverIDs) {
		log.Printf("[HTTP] POST /api/v1/routes/calculate: drivers mismatch requested=%d found=%d", len(req.DriverIDs), len(drivers))
		h.handleValidationError(w, "Some drivers not found")
		return
	}

	routingReq := &routing.RoutingRequest{
		InstituteCoords: activityLocation.GetCoords(),
		Participants:    participants,
		Drivers:         drivers,
		Mode:            routing.RouteMode(mode),
	}

	result, err := h.Router.CalculateRoutes(r.Context(), routingReq)
	if err != nil {
		if rerr, ok := err.(*routing.ErrRoutingFailed); ok {
			log.Printf("[ERROR] Routing failed: participants=%d unassigned=%d capacity=%d reason=%s", rerr.TotalParticipants, rerr.UnassignedCount, rerr.TotalCapacity, rerr.Reason)

			// For HTMX requests, show the capacity shortage UI with org vehicle assignment options
			if h.isHTMX(r) {
				orgVehicles, _ := h.DB.OrganizationVehicles().List(r.Context())
				shortage := rerr.TotalParticipants - rerr.TotalCapacity

				// Trigger a warning toast
				w.Header().Set("HX-Trigger", `{"showToast":{"message":"Not enough capacity - need `+strconv.Itoa(shortage)+` more seats","type":"warning"}}`)

				h.renderTemplate(w, "capacity_shortage", map[string]interface{}{
					"Error": map[string]interface{}{
						"Message":           rerr.Reason,
						"UnassignedCount":   rerr.UnassignedCount,
						"TotalCapacity":     rerr.TotalCapacity,
						"TotalParticipants": rerr.TotalParticipants,
						"Shortage":          shortage,
					},
					"Drivers":          drivers,
					"OrgVehicles":      orgVehicles,
					"ParticipantIDs":   req.ParticipantIDs,
					"DriverIDs":        req.DriverIDs,
					"ActivityLocation": activityLocation,
					"UseMiles":         settings.UseMiles,
				})
				return
			}

			h.handleRoutingError(w, err)
			return
		}
		log.Printf("[ERROR] Route calculation failed: err=%v", err)
		h.handleInternalError(w, err)
		return
	}

	log.Printf("[HTTP] Routes calculated successfully: drivers=%d total_distance=%.0f", result.Summary.TotalDriversUsed, result.Summary.TotalDropoffDistanceMeters)

	// Create a session for route editing
	session := h.RouteSession.Create(result.Routes, drivers, activityLocation, settings.UseMiles, mode)

	// Return HTML for htmx, JSON for API calls
	if h.isHTMX(r) {
		w.Header().Set("HX-Trigger", fmt.Sprintf(`{"showToast": {"message": "Routes calculated! %d drivers assigned.", "type": "success"}}`, result.Summary.TotalDriversUsed))
		h.renderTemplate(w, "route_results", map[string]interface{}{
			"Routes":           result.Routes,
			"Summary":          result.Summary,
			"UseMiles":         settings.UseMiles,
			"ActivityLocation": activityLocation,
			"SessionID":        session.ID,
			"IsEditing":        false,
			"UnusedDrivers":    getUnusedDrivers(session),
			"Mode":             mode,
		})
		return
	}

	h.writeJSON(w, http.StatusOK, map[string]interface{}{
		"routes":     result.Routes,
		"summary":    result.Summary,
		"session_id": session.ID,
	})
}

// HandleCalculateRoutesWithOrgVehicles handles POST /api/v1/routes/calculate-with-org-vehicles
// This endpoint is called when the user assigns org vehicles to drivers to overcome capacity shortage
func (h *Handler) HandleCalculateRoutesWithOrgVehicles(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		log.Printf("[HTTP] POST /api/v1/routes/calculate-with-org-vehicles: form_parse_error err=%v", err)
		h.handleValidationErrorHTMX(w, r, "Invalid form data")
		return
	}

	// Parse participant_ids
	var participantIDs []int64
	for _, idStr := range r.Form["participant_ids"] {
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err == nil {
			participantIDs = append(participantIDs, id)
		}
	}

	// Parse driver_ids
	var driverIDs []int64
	for _, idStr := range r.Form["driver_ids"] {
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err == nil {
			driverIDs = append(driverIDs, id)
		}
	}

	// Parse mode (default to "dropoff" if not provided)
	mode := r.FormValue("mode")
	if mode == "" {
		mode = "dropoff"
	}

	// Parse org vehicle assignments (org_vehicle_{driverID} = orgVehicleID)
	orgVehicleAssignments := make(map[int64]int64) // driverID -> orgVehicleID
	for key, values := range r.Form {
		if strings.HasPrefix(key, "org_vehicle_") && len(values) > 0 && values[0] != "" {
			driverIDStr := strings.TrimPrefix(key, "org_vehicle_")
			driverID, err := strconv.ParseInt(driverIDStr, 10, 64)
			if err != nil {
				continue
			}
			orgVehicleID, err := strconv.ParseInt(values[0], 10, 64)
			if err != nil {
				continue
			}
			orgVehicleAssignments[driverID] = orgVehicleID
		}
	}

	log.Printf("[HTTP] POST /api/v1/routes/calculate-with-org-vehicles: participants=%d drivers=%d org_assignments=%d mode=%s",
		len(participantIDs), len(driverIDs), len(orgVehicleAssignments), mode)

	if len(participantIDs) == 0 {
		h.handleValidationErrorHTMX(w, r, "Please select at least one participant.")
		return
	}

	if len(driverIDs) == 0 {
		h.handleValidationErrorHTMX(w, r, "Please select at least one driver.")
		return
	}

	settings, err := h.DB.Settings().Get(r.Context())
	if err != nil {
		h.handleInternalError(w, err)
		return
	}

	activityLocation, err := h.DB.ActivityLocations().GetByID(r.Context(), settings.SelectedActivityLocationID)
	if err != nil || activityLocation == nil {
		h.handleValidationErrorHTMX(w, r, "Activity location not configured.")
		return
	}

	participants, err := h.DB.Participants().GetByIDs(r.Context(), participantIDs)
	if err != nil {
		h.handleInternalError(w, err)
		return
	}

	drivers, err := h.DB.Drivers().GetByIDs(r.Context(), driverIDs)
	if err != nil {
		h.handleInternalError(w, err)
		return
	}

	// Load org vehicles that are assigned
	var orgVehicleIDs []int64
	for _, ovID := range orgVehicleAssignments {
		orgVehicleIDs = append(orgVehicleIDs, ovID)
	}
	orgVehicles, err := h.DB.OrganizationVehicles().GetByIDs(r.Context(), orgVehicleIDs)
	if err != nil {
		h.handleInternalError(w, err)
		return
	}
	orgVehicleMap := make(map[int64]*models.OrganizationVehicle)
	for i := range orgVehicles {
		orgVehicleMap[orgVehicles[i].ID] = &orgVehicles[i]
	}

	// Modify driver capacities based on org vehicle assignments
	modifiedDrivers := make([]models.Driver, len(drivers))
	driverOrgVehicle := make(map[int64]*models.OrganizationVehicle) // track which driver has which org vehicle
	for i, d := range drivers {
		modifiedDrivers[i] = d
		if ovID, ok := orgVehicleAssignments[d.ID]; ok {
			if ov, exists := orgVehicleMap[ovID]; exists {
				modifiedDrivers[i].VehicleCapacity = ov.Capacity
				driverOrgVehicle[d.ID] = ov
				log.Printf("[ROUTING] Driver %s assigned org vehicle %s (capacity %d -> %d)",
					d.Name, ov.Name, d.VehicleCapacity, ov.Capacity)
			}
		}
	}

	routingReq := &routing.RoutingRequest{
		InstituteCoords: activityLocation.GetCoords(),
		Participants:    participants,
		Drivers:         modifiedDrivers,
		Mode:            routing.RouteMode(mode),
	}

	result, err := h.Router.CalculateRoutes(r.Context(), routingReq)
	if err != nil {
		if rerr, ok := err.(*routing.ErrRoutingFailed); ok {
			// Still not enough capacity - show the UI again
			allOrgVehicles, _ := h.DB.OrganizationVehicles().List(r.Context())
			shortage := rerr.TotalParticipants - rerr.TotalCapacity

			h.renderTemplate(w, "capacity_shortage", map[string]interface{}{
				"Error": map[string]interface{}{
					"Message":           rerr.Reason,
					"UnassignedCount":   rerr.UnassignedCount,
					"TotalCapacity":     rerr.TotalCapacity,
					"TotalParticipants": rerr.TotalParticipants,
					"Shortage":          shortage,
				},
				"Drivers":          drivers, // Original drivers for display
				"OrgVehicles":      allOrgVehicles,
				"ParticipantIDs":   participantIDs,
				"DriverIDs":        driverIDs,
				"ActivityLocation": activityLocation,
				"UseMiles":         settings.UseMiles,
			})
			return
		}
		h.handleInternalError(w, err)
		return
	}

	// Update routes with org vehicle info
	for i := range result.Routes {
		route := &result.Routes[i]
		if ov, ok := driverOrgVehicle[route.Driver.ID]; ok {
			route.OrgVehicleID = ov.ID
			route.OrgVehicleName = ov.Name
			route.EffectiveCapacity = ov.Capacity
		} else {
			route.EffectiveCapacity = route.Driver.VehicleCapacity
		}
	}

	// Count org vehicles used in summary
	result.Summary.OrgVehiclesUsed = len(driverOrgVehicle)

	log.Printf("[HTTP] Routes calculated with org vehicles: drivers=%d org_vehicles=%d total_distance=%.0f",
		result.Summary.TotalDriversUsed, result.Summary.OrgVehiclesUsed, result.Summary.TotalDropoffDistanceMeters)

	// Create a session for route editing
	session := h.RouteSession.Create(result.Routes, drivers, activityLocation, settings.UseMiles, mode)

	w.Header().Set("HX-Trigger", fmt.Sprintf(`{"showToast": {"message": "Routes calculated! %d drivers assigned.", "type": "success"}}`, result.Summary.TotalDriversUsed))
	h.renderTemplate(w, "route_results", map[string]interface{}{
		"Routes":           result.Routes,
		"Summary":          result.Summary,
		"UseMiles":         settings.UseMiles,
		"ActivityLocation": activityLocation,
		"SessionID":        session.ID,
		"IsEditing":        false,
		"UnusedDrivers":    getUnusedDrivers(session),
		"Mode":             mode,
	})
}

