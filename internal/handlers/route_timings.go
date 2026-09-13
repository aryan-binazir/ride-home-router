package handlers

import (
	"context"
	"errors"
	"log"
	"ride-home-router/internal/database"
	"ride-home-router/internal/models"
	"ride-home-router/internal/orderedroute"
	"ride-home-router/internal/routesession"
	"ride-home-router/internal/routing"
	"time"
)

// Timing statuses drive what a route card shows. Only "measured" carries numbers.
const (
	timingMeasured    = "measured"
	timingStale       = "stale"
	timingExhausted   = "exhausted"
	timingUnavailable = "unavailable"
	timingFailed      = "failed"
	timingPaused      = "paused"
	timingEmpty       = "empty"

	messageTimingsExhausted   = "Timings unavailable this month."
	messageTimingsUnavailable = "Timings need the Google Maps key. Ask an administrator."
	messageTimingsFailed      = "Could not get timings."
	messageTimingsStale       = "Timings not fetched for this view."
	messageTimingsPaused      = "Unavailable until within capacity."
	messageTimingsEmpty       = "No riders assigned."

	// measurementReserve keeps time to persist and render after measuring;
	// measurementFloor is the least budget worth spending a request on.
	measurementReserve = 2 * time.Second
	measurementFloor   = 3 * time.Second
)

// RouteTiming is one car's provider-measured route for the current response.
// Route is nil unless Status is "measured"; nothing here is ever persisted.
type RouteTiming struct {
	Status  string
	Message string
	Route   *models.CalculatedRoute
}

// RouteTimingJSON is the API shape of RouteTiming.
type RouteTimingJSON struct {
	Status                     string    `json:"status"`
	Message                    string    `json:"message,omitempty"`
	TotalDropoffDistanceMeters float64   `json:"total_dropoff_distance_meters,omitempty"`
	DistanceToDriverHomeMeters float64   `json:"distance_to_driver_home_meters,omitempty"`
	TotalDistanceMeters        float64   `json:"total_distance_meters,omitempty"`
	BaselineDurationSecs       float64   `json:"baseline_duration_secs,omitempty"`
	RouteDurationSecs          float64   `json:"route_duration_secs,omitempty"`
	DetourSecs                 float64   `json:"detour_secs,omitempty"`
	StopCumulativeDurationSecs []float64 `json:"stop_cumulative_duration_secs,omitempty"`
	StopDistanceFromPrevMeters []float64 `json:"stop_distance_from_prev_meters,omitempty"`
}

// allRouteIndexes lists every route index of the snapshot.
func allRouteIndexes(snapshot routesession.Snapshot) []int {
	indexes := make([]int, len(snapshot.Routes))
	for i := range indexes {
		indexes[i] = i
	}
	return indexes
}

// routeTimings measures the requested cars for this response. With the legacy
// matrix engine (no Measurer) the planner's own metrics are the measurement.
func (h *Handler) routeTimings(ctx context.Context, snapshot routesession.Snapshot, indexes []int) []RouteTiming {
	timings := make([]RouteTiming, len(snapshot.Routes))
	for i := range timings {
		timings[i] = RouteTiming{Status: timingStale, Message: messageTimingsStale}
		// Templates hide every car's numbers while the plan is out of balance,
		// so no car is measured (or billed) until it is balanced again.
		if snapshot.IsOutOfBalance {
			timings[i] = RouteTiming{Status: timingPaused, Message: messageTimingsPaused}
		} else if len(snapshot.Routes[i].Stops) == 0 {
			timings[i] = RouteTiming{Status: timingEmpty, Message: messageTimingsEmpty}
		}
	}
	if snapshot.IsOutOfBalance {
		return timings
	}
	if h.Measurer == nil {
		for i := range snapshot.Routes {
			if timings[i].Status != timingPaused {
				route := snapshot.Routes[i]
				timings[i] = RouteTiming{Status: timingMeasured, Route: &route}
			}
		}
		return timings
	}
	wanted := make([]int, 0, len(indexes))
	for _, index := range indexes {
		if index < 0 || index >= len(snapshot.Routes) || timings[index].Status != timingStale {
			continue
		}
		wanted = append(wanted, index)
	}
	if len(wanted) == 0 {
		return timings
	}
	measureCtx := ctx
	if deadline, ok := ctx.Deadline(); ok {
		if time.Until(deadline) < measurementReserve+measurementFloor {
			// Too little budget left to measure anything: reserve nothing.
			for _, index := range wanted {
				timings[index] = RouteTiming{Status: timingFailed, Message: messageTimingsFailed}
			}
			log.Printf("[ROUTES] timings requested=%d measured=0 outcome=budget_exhausted", len(wanted))
			return timings
		}
		var cancel context.CancelFunc
		measureCtx, cancel = context.WithDeadline(ctx, deadline.Add(-measurementReserve))
		defer cancel()
	}
	var institute models.Coordinates
	if snapshot.ActivityLocation != nil {
		institute = snapshot.ActivityLocation.GetCoords()
	}
	measured := 0
	for _, result := range routing.MeasureRoutes(measureCtx, h.Measurer, institute, snapshot.Mode, snapshot.Routes, wanted) {
		if result.Err != nil {
			timings[result.Index] = timingFailure(result.Err)
			continue
		}
		route := result.Route
		timings[result.Index] = RouteTiming{Status: timingMeasured, Route: &route}
		measured++
	}
	log.Printf("[ROUTES] timings requested=%d measured=%d", len(wanted), measured)
	return timings
}

func timingFailure(err error) RouteTiming {
	switch {
	case errors.Is(err, database.ErrUsageExhausted):
		return RouteTiming{Status: timingExhausted, Message: messageTimingsExhausted}
	case errors.Is(err, orderedroute.ErrNotConfigured):
		return RouteTiming{Status: timingUnavailable, Message: messageTimingsUnavailable}
	default:
		return RouteTiming{Status: timingFailed, Message: messageTimingsFailed}
	}
}

// zeroMetrics strips every planning number from the route in place; the stops
// slice is copied so shared snapshot data is untouched.
func zeroMetrics(route *models.CalculatedRoute) {
	stops := make([]models.RouteStop, len(route.Stops))
	copy(stops, route.Stops)
	route.Stops = stops
	routing.ZeroRouteMetrics(route)
}

// itineraryRoutes returns the snapshot routes without any metric values.
func (h *Handler) itineraryRoutes(routes []models.CalculatedRoute) []models.CalculatedRoute {
	if h.Measurer == nil {
		return routes
	}
	out := make([]models.CalculatedRoute, len(routes))
	for i, route := range routes {
		zeroMetrics(&route)
		out[i] = route
	}
	return out
}

// itinerarySummary keeps counts and drops planning distances unless every
// occupied car was measured in this response.
func (h *Handler) itinerarySummary(summary models.RoutingSummary, timings []RouteTiming) (models.RoutingSummary, bool) {
	if h.Measurer == nil {
		return summary, true
	}
	out := models.RoutingSummary{TotalParticipants: summary.TotalParticipants, TotalDriversUsed: summary.TotalDriversUsed, OrgVehiclesUsed: summary.OrgVehiclesUsed, UnassignedParticipants: summary.UnassignedParticipants}
	used := 0
	for _, timing := range timings {
		if timing.Status == timingEmpty {
			continue
		}
		if timing.Status != timingMeasured {
			// Aggregates over a subset of cars would mislead: keep counts only.
			return out, false
		}
		used++
		out.TotalDropoffDistanceMeters += timing.Route.TotalDropoffDistanceMeters
		out.TotalDistanceMeters += timing.Route.TotalDistanceMeters
		out.SumDetourSecs += timing.Route.DetourSecs
		out.MaxDetourSecs = max(out.MaxDetourSecs, timing.Route.DetourSecs)
	}
	if used > 0 {
		out.AverageDetourSecs = out.SumDetourSecs / float64(used)
	}
	return out, true
}

func timingsJSON(timings []RouteTiming) []RouteTimingJSON {
	out := make([]RouteTimingJSON, len(timings))
	for i, timing := range timings {
		out[i] = RouteTimingJSON{Status: timing.Status, Message: timing.Message}
		if timing.Route == nil {
			continue
		}
		route := timing.Route
		out[i].TotalDropoffDistanceMeters, out[i].DistanceToDriverHomeMeters, out[i].TotalDistanceMeters = route.TotalDropoffDistanceMeters, route.DistanceToDriverHomeMeters, route.TotalDistanceMeters
		out[i].BaselineDurationSecs, out[i].RouteDurationSecs, out[i].DetourSecs = route.BaselineDurationSecs, route.RouteDurationSecs, route.DetourSecs
		for _, stop := range route.Stops {
			out[i].StopCumulativeDurationSecs = append(out[i].StopCumulativeDurationSecs, stop.CumulativeDurationSecs)
			out[i].StopDistanceFromPrevMeters = append(out[i].StopDistanceFromPrevMeters, stop.DistanceFromPrevMeters)
		}
	}
	return out
}

// buildTimedRouteResultsView renders a snapshot with this response's timings.
func (h *Handler) buildTimedRouteResultsView(snapshot routesession.Snapshot, timings []RouteTiming) RouteResultsView {
	view := buildRouteResultsView(snapshot)
	view.Routes = h.itineraryRoutes(snapshot.Routes)
	view.Timings = timings
	view.Summary, view.ShowAggregates = h.itinerarySummary(snapshot.Summary, timings)
	if h.Measurer != nil {
		for _, timing := range timings {
			if timing.Status == timingMeasured && len(timing.Route.Stops) > 0 {
				view.Attribution = true
				break
			}
		}
	}
	return view
}

func (h *Handler) routeCalculationResponse(snapshot routesession.Snapshot, timings []RouteTiming) RouteCalculationResponse {
	summary, _ := h.itinerarySummary(snapshot.Summary, timings)
	response := RouteCalculationResponse{Routes: h.itineraryRoutes(snapshot.Routes), Summary: summary, SessionID: snapshot.ID, Mode: snapshot.Mode}
	if h.Measurer != nil {
		response.Timings = timingsJSON(timings)
	}
	return response
}
