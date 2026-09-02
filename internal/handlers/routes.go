package handlers

import (
	"context"
	"errors"
	"fmt"
	"html"
	"log"
	"net/http"
	"net/url"
	"ride-home-router/internal/distance"
	"ride-home-router/internal/httpx"
	"ride-home-router/internal/logutil"
	"ride-home-router/internal/plandraft"
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
	validateSelectionsFirst  bool
	alwaysRenderResultsHTML  bool
	warnOnShortage           bool
	staleEntityErrorsUseJSON bool
}

const (
	routeSolveTimeout          = 30 * time.Second
	messageCalculationTimedOut = "calculation timed out — reduce the selection"
)

var (
	errInvalidRouteActivityLocation = errors.New("invalid route activity location")
	errInvalidRouteSelection        = errors.New("invalid route selection")
)

func routeSelectionLimitMessage(selection string) string {
	return fmt.Sprintf("Choose no more than %d %s.", plandraft.MaxSelectionSize, selection)
}

func routeCalculationTimeoutError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return err
	}
	return nil
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
	participantIDs, err := parseRouteIDs(form["participant_ids"])
	if err != nil {
		return CalculateRoutesRequest{}, err
	}
	driverIDs, err := parseRouteIDs(form["driver_ids"])
	if err != nil {
		return CalculateRoutesRequest{}, err
	}
	req := CalculateRoutesRequest{ParticipantIDs: participantIDs, DriverIDs: driverIDs}
	if idStr := form.Get("activity_location_id"); idStr != "" {
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			return CalculateRoutesRequest{}, errInvalidRouteActivityLocation
		}
		req.ActivityLocationID = id
	}
	req.RouteTime = form.Get("route_time")
	req.Mode = form.Get("mode")

	return req, nil
}

func parseRouteIDs(values []string) ([]int64, error) {
	if len(values) > plandraft.MaxSelectionSize {
		return nil, errInvalidRouteSelection
	}
	ids := make([]int64, 0, len(values))
	seen := make(map[int64]struct{}, len(values))
	for _, value := range values {
		id, err := strconv.ParseInt(value, 10, 64)
		if err != nil || id <= 0 {
			return nil, errInvalidRouteSelection
		}
		if _, exists := seen[id]; exists {
			return nil, errInvalidRouteSelection
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids, nil
}

func routeFormValidationMessage(err error) string {
	if errors.Is(err, errInvalidRouteActivityLocation) {
		return messageChooseValidActivityLocation
	}
	return messageInvalidFormData
}

// HandleCalculateRoutes handles POST /api/v1/routes/calculate
func (h *Handler) HandleCalculateRoutes(w http.ResponseWriter, r *http.Request) {
	var req CalculateRoutesRequest

	contentType := r.Header.Get(httpx.HeaderContentType)
	if httpx.HasFormContentType(contentType) {
		if err := r.ParseForm(); err != nil {
			log.Printf("[HTTP] POST /api/v1/routes/calculate: form_parse_error err=%v", err)
			// Preserve the existing JSON response for malformed initial HTMX forms.
			h.handleValidationError(w, messageInvalidFormData)
			return
		}
		var err error
		req, err = parseRouteForm(r.Form)
		if err != nil {
			h.handleValidationErrorHTMX(w, r, routeFormValidationMessage(err))
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

	h.runRouteIntake(w, r, req, routeIntakePolicy{
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
		h.handleValidationErrorHTMX(w, r, routeFormValidationMessage(err))
		return
	}

	h.runRouteIntake(w, r, req, routeIntakePolicy{
		validateSelectionsFirst:  false,
		alwaysRenderResultsHTML:  true,
		warnOnShortage:           false,
		staleEntityErrorsUseJSON: false,
	})
}

func (h *Handler) runRouteIntake(w http.ResponseWriter, r *http.Request, req CalculateRoutesRequest, policy routeIntakePolicy) {
	validateSelections := func() bool {
		if len(req.ParticipantIDs) == 0 {
			//nolint:gosec // G706: dynamic values are numeric, boolean, or escaped with logutil.SafeString.
			log.Printf("[HTTP] POST %s: missing participants", logutil.SafeString(r.URL.Path))
			h.handleValidationErrorHTMX(w, r, messageSelectAtLeastOneParticipant)
			return false
		}
		if len(req.DriverIDs) == 0 {
			//nolint:gosec // G706: dynamic values are numeric, boolean, or escaped with logutil.SafeString.
			log.Printf("[HTTP] POST %s: missing drivers", logutil.SafeString(r.URL.Path))
			h.handleValidationErrorHTMX(w, r, messageSelectAtLeastOneDriver)
			return false
		}
		if len(req.ParticipantIDs) > plandraft.MaxSelectionSize {
			h.handleValidationErrorHTMX(w, r, routeSelectionLimitMessage("participants"))
			return false
		}
		if len(req.DriverIDs) > plandraft.MaxSelectionSize {
			h.handleValidationErrorHTMX(w, r, routeSelectionLimitMessage("drivers"))
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
	orgVehicleAssignments, err := parseOrgVehicleAssignments(r.Form, req.DriverIDs)
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

	//nolint:gosec // G706: dynamic values are numeric, boolean, or escaped with logutil.SafeString.
	log.Printf("[HTTP] POST %s: participants=%d drivers=%d org_assignments=%d mode=%s",
		logutil.SafeString(r.URL.Path), len(req.ParticipantIDs), len(req.DriverIDs), len(orgVehicleAssignments), logutil.SafeString(string(mode)))

	calculationCtx, cancel := context.WithTimeout(r.Context(), routeSolveTimeout)
	defer cancel()
	outcome := newRouteCalculation(h.DB, h.Router, h.RouteSession).calculate(calculationCtx, routeCalculationInput{
		ParticipantIDs:        req.ParticipantIDs,
		DriverIDs:             req.DriverIDs,
		ActivityLocationID:    req.ActivityLocationID,
		RouteTime:             routeTime,
		Mode:                  mode,
		OrgVehicleAssignments: orgVehicleAssignments,
	})
	if timeoutErr := routeCalculationTimeoutError(calculationCtx, outcome.Err); timeoutErr != nil {
		h.handleRouteCalculationError(w, r, timeoutErr)
		return
	}
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
		//nolint:gosec // G706: dynamic values are numeric, boolean, or escaped with logutil.SafeString.
		log.Printf("[ERROR] POST %s: route calculation failed: err=%s", logutil.SafeString(r.URL.Path), logutil.SafeString(outcome.Err.Error()))
		h.handleRouteCalculationError(w, r, outcome.Err)
		return
	}
	if outcome.Kind == routeCalculationShortage {
		shortage := outcome.Shortage
		//nolint:gosec // G706: dynamic values are numeric, boolean, or escaped with logutil.SafeString.
		log.Printf("[ERROR] POST %s: routing failed: participants=%d unassigned=%d capacity=%d reason=%s",
			logutil.SafeString(r.URL.Path), shortage.RoutingError.TotalParticipants, shortage.RoutingError.UnassignedCount, shortage.RoutingError.TotalCapacity, logutil.SafeString(shortage.RoutingError.Reason))
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
	//nolint:gosec // G706: dynamic values are numeric, boolean, or escaped with logutil.SafeString.
	log.Printf("[HTTP] POST %s: routes calculated: drivers=%d org_vehicles=%d total_distance=%.0f",
		logutil.SafeString(r.URL.Path), result.Summary.TotalDriversUsed, result.Summary.OrgVehiclesUsed, result.Summary.TotalDropoffDistanceMeters)

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

	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		message = messageCalculationTimedOut
		code = "CALCULATION_TIMED_OUT"
	} else if errors.Is(err, distance.ErrProviderNotConfigured) {
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
