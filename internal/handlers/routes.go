package handlers

import (
	"errors"
	"html"
	"log"
	"net/http"
	"net/url"
	"ride-home-router/internal/distance"
	"ride-home-router/internal/httpx"
	"strconv"
	"strings"
	"time"
)

// CalculateRoutesRequest is the route calculation payload.
type CalculateRoutesRequest struct {
	ParticipantIDs     []int64 `json:"participant_ids"`
	DriverIDs          []int64 `json:"driver_ids"`
	ActivityLocationID int64   `json:"activity_location_id"`
	RouteTime          string  `json:"route_time"`
	Mode               string  `json:"mode"`
}

// routeIntakePolicy names the observable differences kept for endpoint compatibility.
type routeIntakePolicy struct {
	logPath                  string
	validateSelectionsFirst  bool
	alwaysRenderResultsHTML  bool
	warnOnShortage           bool
	staleEntityErrorsUseJSON bool
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

func parseRouteForm(form url.Values) (CalculateRoutesRequest, error) {
	var req CalculateRoutesRequest

	for _, idStr := range form["participant_ids"] {
		if id, err := strconv.ParseInt(idStr, 10, 64); err == nil {
			req.ParticipantIDs = append(req.ParticipantIDs, id)
		}
	}
	for _, idStr := range form["driver_ids"] {
		if id, err := strconv.ParseInt(idStr, 10, 64); err == nil {
			req.DriverIDs = append(req.DriverIDs, id)
		}
	}
	if idStr := form.Get("activity_location_id"); idStr != "" {
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			return CalculateRoutesRequest{}, errors.New(messageChooseValidActivityLocation)
		}
		req.ActivityLocationID = id
	}
	req.RouteTime = form.Get("route_time")
	req.Mode = form.Get("mode")

	return req, nil
}

// HandleCalculateRoutes handles POST /api/v1/routes/calculate
func (h *Handler) HandleCalculateRoutes(w http.ResponseWriter, r *http.Request) {
	var req CalculateRoutesRequest
	var form url.Values

	contentType := r.Header.Get(httpx.HeaderContentType)
	if httpx.HasFormContentType(contentType) {
		if err := r.ParseForm(); err != nil {
			log.Printf("[HTTP] POST /api/v1/routes/calculate: form_parse_error err=%v", err)
			// Preserve the existing JSON response for malformed initial HTMX forms.
			h.handleValidationError(w, messageInvalidFormData)
			return
		}
		form = r.Form
		var err error
		req, err = parseRouteForm(form)
		if err != nil {
			h.handleValidationErrorHTMX(w, r, err.Error())
			return
		}
		log.Printf("[HTTP] POST /api/v1/routes/calculate: form_data participants=%v drivers=%v", req.ParticipantIDs, req.DriverIDs)
	} else {
		if err := httpx.DecodeJSON(r, &req); err != nil {
			log.Printf("[HTTP] POST /api/v1/routes/calculate: invalid_json err=%v", err)
			h.handleValidationError(w, messageInvalidRequestBody)
			return
		}
	}

	h.runRouteIntake(w, r, req, form, routeIntakePolicy{
		logPath:                  "/api/v1/routes/calculate",
		validateSelectionsFirst:  true,
		alwaysRenderResultsHTML:  false,
		warnOnShortage:           true,
		staleEntityErrorsUseJSON: true,
	})
}

// HandleCalculateRoutesWithOrgVehicles handles POST /api/v1/routes/calculate-with-org-vehicles.
func (h *Handler) HandleCalculateRoutesWithOrgVehicles(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		log.Printf("[HTTP] POST /api/v1/routes/calculate-with-org-vehicles: form_parse_error err=%v", err)
		h.handleValidationErrorHTMX(w, r, messageInvalidFormData)
		return
	}

	req, err := parseRouteForm(r.Form)
	if err != nil {
		h.handleValidationErrorHTMX(w, r, err.Error())
		return
	}

	h.runRouteIntake(w, r, req, r.Form, routeIntakePolicy{
		logPath:                  "/api/v1/routes/calculate-with-org-vehicles",
		validateSelectionsFirst:  false,
		alwaysRenderResultsHTML:  true,
		warnOnShortage:           false,
		staleEntityErrorsUseJSON: false,
	})
}

func (h *Handler) runRouteIntake(w http.ResponseWriter, r *http.Request, req CalculateRoutesRequest, form url.Values, policy routeIntakePolicy) {
	validateSelections := func() bool {
		if len(req.ParticipantIDs) == 0 {
			log.Printf("[HTTP] POST %s: missing participants", policy.logPath)
			h.handleValidationErrorHTMX(w, r, messageSelectAtLeastOneParticipant)
			return false
		}
		if len(req.DriverIDs) == 0 {
			log.Printf("[HTTP] POST %s: missing drivers", policy.logPath)
			h.handleValidationErrorHTMX(w, r, messageSelectAtLeastOneDriver)
			return false
		}
		return true
	}
	if policy.validateSelectionsFirst && !validateSelections() {
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
	orgVehicleAssignments, err := parseOrgVehicleAssignments(form, req.DriverIDs)
	if err != nil {
		h.handleValidationErrorHTMX(w, r, err.Error())
		return
	}

	if !policy.validateSelectionsFirst && !validateSelections() {
		return
	}
	if req.ActivityLocationID == 0 {
		h.handleValidationErrorHTMX(w, r, messageChooseActivityLocationForEvent)
		return
	}

	log.Printf("[HTTP] POST %s: participants=%d drivers=%d org_assignments=%d mode=%s",
		policy.logPath, len(req.ParticipantIDs), len(req.DriverIDs), len(orgVehicleAssignments), mode)

	outcome := newRouteCalculation(h.DB, h.Router, h.RouteSession).calculate(r.Context(), routeCalculationInput{
		ParticipantIDs:        req.ParticipantIDs,
		DriverIDs:             req.DriverIDs,
		ActivityLocationID:    req.ActivityLocationID,
		RouteTime:             routeTime,
		Mode:                  mode,
		OrgVehicleAssignments: orgVehicleAssignments,
	})
	if outcome.Kind == routeCalculationValidationFailure {
		message := routeCalculationValidationMessage(outcome.Err)
		staleEntity := errors.Is(outcome.Err, errSomeParticipantsNotFound) || errors.Is(outcome.Err, errSomeDriversNotFound)
		if policy.staleEntityErrorsUseJSON && staleEntity {
			// Preserve the initial endpoint's existing JSON response for stale HTMX selections.
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
		log.Printf("[ERROR] POST %s: route calculation failed: err=%v", policy.logPath, outcome.Err)
		h.handleRouteCalculationError(w, r, outcome.Err)
		return
	}
	if outcome.Kind == routeCalculationShortage {
		shortage := outcome.Shortage
		log.Printf("[ERROR] POST %s: routing failed: participants=%d unassigned=%d capacity=%d reason=%s",
			policy.logPath, shortage.RoutingError.TotalParticipants, shortage.RoutingError.UnassignedCount, shortage.RoutingError.TotalCapacity, shortage.RoutingError.Reason)
		if policy.alwaysRenderResultsHTML || h.isHTMX(r) {
			if policy.warnOnShortage {
				h.setHTMXToast(w, messageNotEnoughCapacity(shortage.RoutingError.TotalParticipants-shortage.RoutingError.TotalCapacity), toastTypeWarning)
			}
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
	log.Printf("[HTTP] POST %s: routes calculated: drivers=%d org_vehicles=%d total_distance=%.0f",
		policy.logPath, result.Summary.TotalDriversUsed, result.Summary.OrgVehiclesUsed, result.Summary.TotalDropoffDistanceMeters)

	if policy.alwaysRenderResultsHTML || h.isHTMX(r) {
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
		message = "Google Maps API key is not configured. Set GOOGLE_MAPS_API_KEY on the server."
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
