package handlers

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"ride-home-router/internal/httpx"
	"ride-home-router/internal/routing"
)

// CalculateRoutesRequest represents the request for route calculation
type CalculateRoutesRequest struct {
	ParticipantIDs     []int64 `json:"participant_ids"`
	DriverIDs          []int64 `json:"driver_ids"`
	ActivityLocationID int64   `json:"activity_location_id"`
	RouteTime          string  `json:"route_time"`
}

func parseRouteTime(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", errors.New(messageChooseRouteTime)
	}
	if _, err := time.Parse("15:04", trimmed); err != nil {
		return "", errors.New(messageChooseValidRouteTime)
	}
	return trimmed, nil
}

// HandleCalculateRoutes handles POST /api/v1/routes/calculate
func (h *Handler) HandleCalculateRoutes(w http.ResponseWriter, r *http.Request) {
	var req CalculateRoutesRequest

	contentType := r.Header.Get(httpx.HeaderContentType)

	// Handle form data (from htmx)
	if httpx.HasFormContentType(contentType) {
		if err := r.ParseForm(); err != nil {
			log.Printf("[HTTP] POST /api/v1/routes/calculate: form_parse_error err=%v", err)
			h.handleValidationError(w, messageInvalidFormData)
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

		if idStr := r.FormValue("activity_location_id"); idStr != "" {
			id, err := strconv.ParseInt(idStr, 10, 64)
			if err != nil {
				h.handleValidationErrorHTMX(w, r, messageChooseValidActivityLocation)
				return
			}
			req.ActivityLocationID = id
		}
		req.RouteTime = r.FormValue("route_time")

		log.Printf("[HTTP] POST /api/v1/routes/calculate: form_data participants=%v drivers=%v", req.ParticipantIDs, req.DriverIDs)
	} else {
		// Handle JSON
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			log.Printf("[HTTP] POST /api/v1/routes/calculate: invalid_json err=%v", err)
			h.handleValidationError(w, messageInvalidRequestBody)
			return
		}
	}

	if len(req.ParticipantIDs) == 0 {
		log.Printf("[HTTP] POST /api/v1/routes/calculate: missing participants")
		h.handleValidationErrorHTMX(w, r, messageSelectAtLeastOneParticipant)
		return
	}

	if len(req.DriverIDs) == 0 {
		log.Printf("[HTTP] POST /api/v1/routes/calculate: missing drivers")
		h.handleValidationErrorHTMX(w, r, messageSelectAtLeastOneDriver)
		return
	}

	routeTime, err := parseRouteTime(req.RouteTime)
	if err != nil {
		h.handleValidationErrorHTMX(w, r, err.Error())
		return
	}

	// Parse mode (default to "dropoff" if not provided)
	mode := r.FormValue("mode")
	if mode == "" {
		mode = "dropoff"
	}

	orgVehicleAssignments, err := parseOrgVehicleAssignments(r.Form, req.DriverIDs)
	if err != nil {
		h.handleValidationErrorHTMX(w, r, err.Error())
		return
	}

	log.Printf("[HTTP] POST /api/v1/routes/calculate: participants=%d drivers=%d mode=%s", len(req.ParticipantIDs), len(req.DriverIDs), mode)

	settings, err := h.DB.Settings().Get(r.Context())
	if err != nil {
		h.handleInternalError(w, err)
		return
	}

	activityLocationID := req.ActivityLocationID
	if activityLocationID == 0 {
		h.handleValidationErrorHTMX(w, r, messageChooseActivityLocationForEvent)
		return
	}

	// Get the selected activity location
	activityLocation, err := h.DB.ActivityLocations().GetByID(r.Context(), activityLocationID)
	if err != nil {
		h.handleInternalError(w, err)
		return
	}

	if activityLocation == nil {
		log.Printf("[HTTP] POST /api/v1/routes/calculate: activity location id=%d not found", activityLocationID)
		h.handleValidationErrorHTMX(w, r, messageSelectedActivityLocationNotFoundChooseAnother)
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

	orgVehicleMap, err := h.loadAssignedOrgVehicles(r.Context(), orgVehicleAssignments)
	if err != nil {
		if errors.Is(err, errSelectedVanNotFound) {
			h.handleValidationErrorHTMX(w, r, err.Error())
		} else {
			h.handleInternalError(w, err)
		}
		return
	}
	modifiedDrivers, driverOrgVehicle := applyOrgVehicleAssignments(drivers, orgVehicleAssignments, orgVehicleMap)

	routingReq := &routing.RoutingRequest{
		InstituteCoords: activityLocation.GetCoords(),
		Participants:    participants,
		Drivers:         modifiedDrivers,
		Mode:            routing.RouteMode(mode),
	}

	result, err := h.Router.CalculateRoutes(r.Context(), routingReq)
	if err != nil {
		if rerr, ok := err.(*routing.ErrRoutingFailed); ok {
			log.Printf("[ERROR] Routing failed: participants=%d unassigned=%d capacity=%d reason=%s", rerr.TotalParticipants, rerr.UnassignedCount, rerr.TotalCapacity, rerr.Reason)

			// For HTMX requests, show the capacity shortage UI with org vehicle assignment options
			if h.isHTMX(r) {
				orgVehicles, _ := h.DB.OrganizationVehicles().List(r.Context())

				h.setHTMXToast(w, messageNotEnoughCapacity(rerr.TotalParticipants-rerr.TotalCapacity), toastTypeWarning)

				h.renderTemplate(w, "capacity_shortage", buildCapacityShortageViewData(
					rerr,
					drivers,
					orgVehicles,
					req.ParticipantIDs,
					req.DriverIDs,
					activityLocation,
					mode,
					settings.UseMiles,
					routeTime,
					orgVehicleAssignments,
					driverOrgVehicle,
				))
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

	applyAssignedOrgVehicleMetadata(result.Routes, driverOrgVehicle)
	result.Summary.OrgVehiclesUsed = countUsedOrgVehicles(result.Routes)

	// Create a session for route editing
	session := h.RouteSession.Create(result.Routes, modifiedDrivers, activityLocation, settings.UseMiles, routeTime, mode, driverOrgVehicle)

	// Return HTML for htmx, JSON for API calls
	if h.isHTMX(r) {
		h.setHTMXToast(w, messageRoutesCalculated(result.Summary.TotalDriversUsed), toastTypeSuccess)
		h.renderTemplate(w, "route_results", buildRouteResultsView(result.Routes, result.Summary, activityLocation, settings.UseMiles, routeTime, session.ID, false, getUnusedDrivers(session), mode))
		return
	}

	h.writeJSON(w, http.StatusOK, RouteCalculationResponse{
		Routes:    result.Routes,
		Summary:   result.Summary,
		SessionID: session.ID,
	})
}

// HandleCalculateRoutesWithOrgVehicles handles POST /api/v1/routes/calculate-with-org-vehicles
// This endpoint is called when the user assigns org vehicles to drivers to overcome capacity shortage
func (h *Handler) HandleCalculateRoutesWithOrgVehicles(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		log.Printf("[HTTP] POST /api/v1/routes/calculate-with-org-vehicles: form_parse_error err=%v", err)
		h.handleValidationErrorHTMX(w, r, messageInvalidFormData)
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

	activityLocationID := int64(0)
	if idStr := r.FormValue("activity_location_id"); idStr != "" {
		parsedID, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			h.handleValidationErrorHTMX(w, r, messageChooseValidActivityLocation)
			return
		}
		activityLocationID = parsedID
	}
	routeTime, err := parseRouteTime(r.FormValue("route_time"))
	if err != nil {
		h.handleValidationErrorHTMX(w, r, err.Error())
		return
	}

	// Parse mode (default to "dropoff" if not provided)
	mode := r.FormValue("mode")
	if mode == "" {
		mode = "dropoff"
	}

	orgVehicleAssignments, err := parseOrgVehicleAssignments(r.Form, driverIDs)
	if err != nil {
		h.handleValidationErrorHTMX(w, r, err.Error())
		return
	}

	log.Printf("[HTTP] POST /api/v1/routes/calculate-with-org-vehicles: participants=%d drivers=%d org_assignments=%d mode=%s",
		len(participantIDs), len(driverIDs), len(orgVehicleAssignments), mode)

	if len(participantIDs) == 0 {
		h.handleValidationErrorHTMX(w, r, messageSelectAtLeastOneParticipant)
		return
	}

	if len(driverIDs) == 0 {
		h.handleValidationErrorHTMX(w, r, messageSelectAtLeastOneDriver)
		return
	}

	settings, err := h.DB.Settings().Get(r.Context())
	if err != nil {
		h.handleInternalError(w, err)
		return
	}

	if activityLocationID == 0 {
		h.handleValidationErrorHTMX(w, r, messageChooseActivityLocationForEvent)
		return
	}

	activityLocation, err := h.DB.ActivityLocations().GetByID(r.Context(), activityLocationID)
	if err != nil || activityLocation == nil {
		h.handleValidationErrorHTMX(w, r, messageSelectedActivityLocationNotFoundChooseAnother)
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

	orgVehicleMap, err := h.loadAssignedOrgVehicles(r.Context(), orgVehicleAssignments)
	if err != nil {
		if errors.Is(err, errSelectedVanNotFound) {
			h.handleValidationErrorHTMX(w, r, err.Error())
		} else {
			h.handleInternalError(w, err)
		}
		return
	}
	modifiedDrivers, driverOrgVehicle := applyOrgVehicleAssignments(drivers, orgVehicleAssignments, orgVehicleMap)
	for _, driver := range drivers {
		if vehicle, ok := driverOrgVehicle[driver.ID]; ok && vehicle != nil {
			log.Printf("[ROUTING] Driver %s assigned org vehicle %s (capacity %d -> %d)",
				driver.Name, vehicle.Name, driver.VehicleCapacity, vehicle.Capacity)
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

			h.renderTemplate(w, "capacity_shortage", buildCapacityShortageViewData(
				rerr,
				drivers,
				allOrgVehicles,
				participantIDs,
				driverIDs,
				activityLocation,
				mode,
				settings.UseMiles,
				routeTime,
				orgVehicleAssignments,
				driverOrgVehicle,
			))
			return
		}
		h.handleInternalError(w, err)
		return
	}

	applyAssignedOrgVehicleMetadata(result.Routes, driverOrgVehicle)

	// Count org vehicles used in summary
	result.Summary.OrgVehiclesUsed = countUsedOrgVehicles(result.Routes)

	log.Printf("[HTTP] Routes calculated with org vehicles: drivers=%d org_vehicles=%d total_distance=%.0f",
		result.Summary.TotalDriversUsed, result.Summary.OrgVehiclesUsed, result.Summary.TotalDropoffDistanceMeters)

	// Create a session for route editing
	session := h.RouteSession.Create(result.Routes, modifiedDrivers, activityLocation, settings.UseMiles, routeTime, mode, driverOrgVehicle)

	h.setHTMXToast(w, messageRoutesCalculated(result.Summary.TotalDriversUsed), toastTypeSuccess)
	h.renderTemplate(w, "route_results", buildRouteResultsView(result.Routes, result.Summary, activityLocation, settings.UseMiles, routeTime, session.ID, false, getUnusedDrivers(session), mode))
}
