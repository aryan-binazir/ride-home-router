package routing

import (
	"context"
	"errors"
	"fmt"
	"ride-home-router/internal/distance"
	"ride-home-router/internal/models"
	"ride-home-router/internal/orderedroute"
)

// Measurer measures ordered routes with the provider; see orderedroute.Client.
type Measurer interface {
	Measure(ctx context.Context, requests []orderedroute.Request) []orderedroute.Result
}

// RouteMeasurement is one car's route with provider-measured metrics, or the
// reason it could not be measured. Values are meant for the current response
// only and must never be persisted.
type RouteMeasurement struct {
	Index int
	Route models.CalculatedRoute
	Err   error
}

// MeasureRoutes measures the selected routes once: contiguous household stops
// share one waypoint, every car also gets a direct origin→destination baseline
// for its detour, and legs are folded into the existing metric fields. Input
// routes are not modified.
func MeasureRoutes(ctx context.Context, measurer Measurer, institute models.Coordinates, mode RouteMode, routes []models.CalculatedRoute, indexes []int) []RouteMeasurement {
	mode = normalizeRouteMode(mode)
	rc := newRouteContext(nil, institute, mode)
	results := make([]RouteMeasurement, len(indexes))
	requests := make([]orderedroute.Request, 0, 2*len(indexes))
	type plan struct {
		waypointOfStop []int // stop index → waypoint index (0-based among household stops)
		routeID        string
		baselineID     string
	}
	plans := make([]plan, len(indexes))

	for i, index := range indexes {
		results[i].Index = index
		if index < 0 || index >= len(routes) {
			results[i].Err = fmt.Errorf("route index %d out of range", index)
			continue
		}
		route := cloneRoute(routes[index])
		results[i].Route = route
		if route.Driver == nil {
			results[i].Err = errors.New("route driver is required")
			continue
		}
		if len(route.Stops) == 0 {
			// An empty car has nothing to measure; its planning numbers must not
			// pass as a measurement either.
			route.Mode = mode
			ZeroRouteMetrics(&route)
			results[i].Route = route
			continue
		}
		points := []models.Coordinates{rc.origin(route.Driver)}
		waypointOfStop := make([]int, len(route.Stops))
		var previousKey string
		for s, stop := range route.Stops {
			if stop.Participant == nil {
				results[i].Err = fmt.Errorf("route stop %d is missing participant data", s)
				break
			}
			key := householdKey(stop.Participant)
			if s == 0 || key != previousKey {
				points = append(points, stop.Participant.GetCoords())
			}
			waypointOfStop[s] = len(points) - 2
			previousKey = key
		}
		if results[i].Err != nil {
			continue
		}
		points = append(points, rc.destination(route.Driver))
		group := fmt.Sprintf("car-%d", index)
		plans[i] = plan{waypointOfStop: waypointOfStop, routeID: fmt.Sprintf("route-%d", index)}
		requests = append(requests, orderedroute.Request{ID: plans[i].routeID, Group: group, Points: points})
		// A driver who lives at the activity location has a zero baseline; skip the call.
		if origin, destination := rc.origin(route.Driver), rc.destination(route.Driver); !distance.SamePoint(origin, destination) {
			plans[i].baselineID = fmt.Sprintf("baseline-%d", index)
			requests = append(requests, orderedroute.Request{ID: plans[i].baselineID, Group: group, Points: []models.Coordinates{origin, destination}})
		}
	}
	if len(requests) == 0 {
		return results
	}

	measured := make(map[string]orderedroute.Result, len(requests))
	for _, result := range measurer.Measure(ctx, requests) {
		measured[result.ID] = result
	}
	for i := range indexes {
		if results[i].Err != nil || plans[i].routeID == "" {
			continue
		}
		routeResult := measured[plans[i].routeID]
		if routeResult.Err != nil {
			results[i].Err = routeResult.Err
			continue
		}
		var baseline orderedroute.Leg
		if plans[i].baselineID != "" {
			baselineResult := measured[plans[i].baselineID]
			if baselineResult.Err != nil {
				results[i].Err = baselineResult.Err
				continue
			}
			if len(baselineResult.Legs) != 1 {
				results[i].Err = errors.New("baseline measurement returned an unexpected number of legs")
				continue
			}
			baseline = baselineResult.Legs[0]
		}
		route := &results[i].Route
		metrics, err := assembleMeasuredMetrics(routeResult.Legs, baseline, plans[i].waypointOfStop, len(route.Stops))
		if err != nil {
			results[i].Err = err
			continue
		}
		rc.applyMetrics(route, metrics)
	}
	return results
}

// assembleMeasuredMetrics folds waypoint legs back onto stops. Household
// siblings after the first receive zero incremental distance and identical
// cumulative values.
func assembleMeasuredMetrics(legs []orderedroute.Leg, baseline orderedroute.Leg, waypointOfStop []int, stopCount int) (*routeMetrics, error) {
	waypoints := 0
	if stopCount > 0 {
		waypoints = waypointOfStop[stopCount-1] + 1
	}
	if len(legs) != waypoints+1 {
		return nil, errors.New("measured legs do not match the route's stops")
	}
	metrics := &routeMetrics{Stops: make([]routeStopMetric, stopCount)}
	consumed := -1
	for s := range stopCount {
		wp := waypointOfStop[s]
		if wp != consumed {
			leg := legs[wp]
			metrics.TotalStopDistanceMeters += leg.DistanceMeters
			metrics.TotalStopDurationSecs += leg.DurationSecs
			metrics.Stops[s] = routeStopMetric{
				DistanceFromPrevMeters: leg.DistanceMeters,
				DurationFromPrevSecs:   leg.DurationSecs,
			}
			consumed = wp
		}
		metrics.Stops[s].CumulativeDistanceMeters = metrics.TotalStopDistanceMeters
		metrics.Stops[s].CumulativeDurationSecs = metrics.TotalStopDurationSecs
	}
	final := legs[len(legs)-1]
	metrics.FinalLegDistanceMeters = final.DistanceMeters
	metrics.FinalLegDurationSecs = final.DurationSecs
	metrics.TotalDistanceMeters = metrics.TotalStopDistanceMeters + final.DistanceMeters
	metrics.RouteDurationSecs = metrics.TotalStopDurationSecs + final.DurationSecs
	metrics.BaselineDurationSecs = baseline.DurationSecs
	metrics.DetourSecs = metrics.RouteDurationSecs - baseline.DurationSecs
	return metrics, nil
}

// ZeroRouteMetrics strips every distance and duration from a route in place so
// planning estimates can never be mistaken for measurements.
func ZeroRouteMetrics(route *models.CalculatedRoute) {
	route.TotalDropoffDistanceMeters, route.DistanceToDriverHomeMeters, route.TotalDistanceMeters = 0, 0, 0
	route.BaselineDurationSecs, route.RouteDurationSecs, route.DetourSecs = 0, 0, 0
	for i := range route.Stops {
		route.Stops[i].DistanceFromPrevMeters, route.Stops[i].CumulativeDistanceMeters = 0, 0
		route.Stops[i].DurationFromPrevSecs, route.Stops[i].CumulativeDurationSecs = 0, 0
	}
}

func cloneRoute(route models.CalculatedRoute) models.CalculatedRoute {
	copied := route
	copied.Stops = make([]models.RouteStop, len(route.Stops))
	copy(copied.Stops, route.Stops)
	return copied
}
