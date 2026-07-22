package handlers

import (
	"encoding/json"
	"errors"
	"html"
	"log"
	"net/http"
	"ride-home-router/internal/distance"
	"ride-home-router/internal/httpx"
	"strconv"
	"strings"
	"time"
)

// CalculateRoutesRequest represents the request for route calculation
type CalculateRoutesRequest struct {
	ParticipantIDs     []int64 `json:"participant_ids"`
	DriverIDs          []int64 `json:"driver_ids"`
	ActivityLocationID int64   `json:"activity_location_id"`
	RouteTime          string  `json:"route_time"`
	Mode               string  `json:"mode"`
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
		req.Mode = r.FormValue("mode")

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

	mode, err := normalizeRouteMode(req.Mode)
	if err != nil {
		h.handleValidationErrorHTMX(w, r, err.Error())
		return
	}

	orgVehicleAssignments, err := parseOrgVehicleAssignments(r.Form, req.DriverIDs)
	if err != nil {
		h.handleValidationErrorHTMX(w, r, err.Error())
		return
	}

	log.Printf("[HTTP] POST /api/v1/routes/calculate: participants=%d drivers=%d mode=%s", len(req.ParticipantIDs), len(req.DriverIDs), mode)

	activityLocationID := req.ActivityLocationID
	if activityLocationID == 0 {
		h.handleValidationErrorHTMX(w, r, messageChooseActivityLocationForEvent)
		return
	}
	outcome := newRouteCalculation(h.DB, h.Router, h.RouteSession).calculate(r.Context(), routeCalculationInput{
		ParticipantIDs:        req.ParticipantIDs,
		DriverIDs:             req.DriverIDs,
		ActivityLocationID:    activityLocationID,
		RouteTime:             routeTime,
		Mode:                  mode,
		OrgVehicleAssignments: orgVehicleAssignments,
	})
	if outcome.Kind == routeCalculationValidationFailure {
		message := routeCalculationValidationMessage(outcome.Err)
		if errors.Is(outcome.Err, errSomeParticipantsNotFound) || errors.Is(outcome.Err, errSomeDriversNotFound) {
			h.handleValidationError(w, message)
		} else {
			h.handleValidationErrorHTMX(w, r, message)
		}
		return
	}
	if outcome.Kind == routeCalculationInternalFailure {
		h.handleInternalError(w, outcome.Err)
		return
	}
	if outcome.Kind == routeCalculationRouteFailure {
		log.Printf("[ERROR] Route calculation failed: err=%v", outcome.Err)
		h.handleRouteCalculationError(w, r, outcome.Err)
		return
	}
	if outcome.Kind == routeCalculationShortage {
		shortage := outcome.Shortage
		log.Printf("[ERROR] Routing failed: participants=%d unassigned=%d capacity=%d reason=%s", shortage.RoutingError.TotalParticipants, shortage.RoutingError.UnassignedCount, shortage.RoutingError.TotalCapacity, shortage.RoutingError.Reason)
		if h.isHTMX(r) {
			h.setHTMXToast(w, messageNotEnoughCapacity(shortage.RoutingError.TotalParticipants-shortage.RoutingError.TotalCapacity), toastTypeWarning)
			h.renderTemplate(w, "capacity_shortage", buildCapacityShortageViewData(
				shortage.RoutingError,
				shortage.Drivers,
				shortage.AvailableOrgVehicles,
				shortage.ParticipantIDs,
				shortage.DriverIDs,
				shortage.ActivityLocation,
				string(shortage.Mode),
				shortage.UseMiles,
				shortage.RouteTime,
				shortage.OrgVehicleAssignments,
				shortage.DriverOrgVehicles,
			))
			return
		}
		h.handleRoutingError(w, shortage.RoutingError)
		return
	}

	result := outcome.Result
	session := outcome.Session
	log.Printf("[HTTP] Routes calculated successfully: drivers=%d total_distance=%.0f", result.Summary.TotalDriversUsed, result.Summary.TotalDropoffDistanceMeters)

	// Return HTML for htmx, JSON for API calls
	if h.isHTMX(r) {
		h.setHTMXToast(w, messageRoutesCalculated(result.Summary.TotalDriversUsed), toastTypeSuccess)
		h.renderTemplate(w, "route_results", buildRouteResultsView(session))
		return
	}

	h.writeJSON(w, http.StatusOK, RouteCalculationResponse{
		Routes:    result.Routes,
		Summary:   result.Summary,
		SessionID: session.ID,
		Mode:      mode,
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

	mode, err := normalizeRouteMode(r.FormValue("mode"))
	if err != nil {
		h.handleValidationErrorHTMX(w, r, err.Error())
		return
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

	if activityLocationID == 0 {
		h.handleValidationErrorHTMX(w, r, messageChooseActivityLocationForEvent)
		return
	}
	outcome := newRouteCalculation(h.DB, h.Router, h.RouteSession).calculate(r.Context(), routeCalculationInput{
		ParticipantIDs:        participantIDs,
		DriverIDs:             driverIDs,
		ActivityLocationID:    activityLocationID,
		RouteTime:             routeTime,
		Mode:                  mode,
		OrgVehicleAssignments: orgVehicleAssignments,
	})
	if outcome.Kind == routeCalculationValidationFailure {
		h.handleValidationErrorHTMX(w, r, routeCalculationValidationMessage(outcome.Err))
		return
	}
	if outcome.Kind == routeCalculationInternalFailure {
		h.handleInternalError(w, outcome.Err)
		return
	}
	if outcome.Kind == routeCalculationRouteFailure {
		h.handleRouteCalculationError(w, r, outcome.Err)
		return
	}
	if outcome.Kind == routeCalculationShortage {
		shortage := outcome.Shortage
		h.renderTemplate(w, "capacity_shortage", buildCapacityShortageViewData(
			shortage.RoutingError,
			shortage.Drivers,
			shortage.AvailableOrgVehicles,
			shortage.ParticipantIDs,
			shortage.DriverIDs,
			shortage.ActivityLocation,
			string(shortage.Mode),
			shortage.UseMiles,
			shortage.RouteTime,
			shortage.OrgVehicleAssignments,
			shortage.DriverOrgVehicles,
		))
		return
	}

	result := outcome.Result
	session := outcome.Session

	log.Printf("[HTTP] Routes calculated with org vehicles: drivers=%d org_vehicles=%d total_distance=%.0f",
		result.Summary.TotalDriversUsed, result.Summary.OrgVehiclesUsed, result.Summary.TotalDropoffDistanceMeters)

	h.setHTMXToast(w, messageRoutesCalculated(result.Summary.TotalDriversUsed), toastTypeSuccess)
	h.renderTemplate(w, "route_results", buildRouteResultsView(session))
}

func routeCalculationValidationMessage(err error) string {
	switch {
	case errors.Is(err, errActivityLocationNotFound):
		return messageSelectedActivityLocationNotFoundChooseAnother
	case errors.Is(err, errSomeParticipantsNotFound):
		return "Some participants not found"
	case errors.Is(err, errSomeDriversNotFound):
		return "Some drivers not found"
	default:
		return err.Error()
	}
}

func (h *Handler) handleRouteCalculationError(w http.ResponseWriter, r *http.Request, err error) {
	message := err.Error()
	status := http.StatusServiceUnavailable
	code := "DISTANCE_PROVIDER_FAILED"

	if errors.Is(err, distance.ErrProviderNotConfigured) {
		message = "Google Maps API key is not configured. Add it in Settings."
		status = http.StatusBadRequest
		code = "DISTANCE_PROVIDER_NOT_CONFIGURED"
	}

	if h.isHTMX(r) {
		h.setHTMXToast(w, message, toastTypeError)
		w.Header().Set(httpx.HeaderContentType, httpx.MediaTypeHTML)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`<div class="alert alert-warning">` + html.EscapeString(message) + `</div>`))
		return
	}

	h.writeError(w, status, code, message, nil)
}
