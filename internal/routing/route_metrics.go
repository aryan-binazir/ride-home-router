package routing

import (
	"context"
	"fmt"
	"ride-home-router/internal/distance"
	"ride-home-router/internal/models"
)

type routeContext struct {
	distanceCalc    distance.DistanceCalculator
	instituteCoords models.Coordinates
	mode            RouteMode
}

type routeStopMetric struct {
	DistanceFromPrevMeters   float64
	CumulativeDistanceMeters float64
	DurationFromPrevSecs     float64
	CumulativeDurationSecs   float64
}

type routeMetrics struct {
	Stops                   []routeStopMetric
	TotalStopDistanceMeters float64
	FinalLegDistanceMeters  float64
	TotalDistanceMeters     float64
	TotalStopDurationSecs   float64
	FinalLegDurationSecs    float64
	RouteDurationSecs       float64
	BaselineDurationSecs    float64
	DetourSecs              float64
}

func newRouteContext(distanceCalc distance.DistanceCalculator, instituteCoords models.Coordinates, mode RouteMode) routeContext {
	if mode == "" {
		mode = RouteModeDropoff
	}

	return routeContext{
		distanceCalc:    distanceCalc,
		instituteCoords: instituteCoords,
		mode:            mode,
	}
}

func (rc routeContext) origin(driver *models.Driver) models.Coordinates {
	if rc.mode == RouteModePickup {
		return driver.GetCoords()
	}
	return rc.instituteCoords
}

func (rc routeContext) destination(driver *models.Driver) models.Coordinates {
	if rc.mode == RouteModePickup {
		return rc.instituteCoords
	}
	return driver.GetCoords()
}

func (rc routeContext) riderScore(ctx context.Context, driver *models.Driver, stops []*models.Participant) (float64, error) {
	if driver == nil {
		return 0, fmt.Errorf("route driver is required")
	}
	if len(stops) == 0 {
		return 0, nil
	}

	total := 0.0
	cumulative := 0.0
	prev := rc.origin(driver)
	for i, stop := range stops {
		if stop == nil {
			return 0, fmt.Errorf("route stop %d is missing participant data", i)
		}

		dist, err := rc.distanceCalc.GetDistance(ctx, prev, stop.GetCoords())
		if err != nil {
			return 0, err
		}
		cumulative += dist.DurationSecs
		total += cumulative
		prev = stop.GetCoords()
	}

	return total, nil
}

func (rc routeContext) evaluateParticipants(ctx context.Context, driver *models.Driver, stops []*models.Participant) (*routeMetrics, error) {
	if driver == nil {
		return nil, fmt.Errorf("route driver is required")
	}

	metrics := &routeMetrics{
		Stops: make([]routeStopMetric, len(stops)),
	}

	if len(stops) == 0 {
		return metrics, nil
	}

	origin := rc.origin(driver)
	destination := rc.destination(driver)
	prev := origin

	for i, stop := range stops {
		if stop == nil {
			return nil, fmt.Errorf("route stop %d is missing participant data", i)
		}

		dist, err := rc.distanceCalc.GetDistance(ctx, prev, stop.GetCoords())
		if err != nil {
			return nil, err
		}

		metrics.TotalStopDistanceMeters += dist.DistanceMeters
		metrics.TotalStopDurationSecs += dist.DurationSecs
		metrics.Stops[i] = routeStopMetric{
			DistanceFromPrevMeters:   dist.DistanceMeters,
			CumulativeDistanceMeters: metrics.TotalStopDistanceMeters,
			DurationFromPrevSecs:     dist.DurationSecs,
			CumulativeDurationSecs:   metrics.TotalStopDurationSecs,
		}
		prev = stop.GetCoords()
	}

	finalLeg, err := rc.distanceCalc.GetDistance(ctx, prev, destination)
	if err != nil {
		return nil, err
	}
	baseline, err := rc.distanceCalc.GetDistance(ctx, origin, destination)
	if err != nil {
		return nil, err
	}

	metrics.FinalLegDistanceMeters = finalLeg.DistanceMeters
	metrics.FinalLegDurationSecs = finalLeg.DurationSecs
	metrics.TotalDistanceMeters = metrics.TotalStopDistanceMeters + finalLeg.DistanceMeters
	metrics.RouteDurationSecs = metrics.TotalStopDurationSecs + finalLeg.DurationSecs
	metrics.BaselineDurationSecs = baseline.DurationSecs
	metrics.DetourSecs = metrics.RouteDurationSecs - baseline.DurationSecs

	return metrics, nil
}

func (rc routeContext) groupInsertionDeltaRiderScore(ctx context.Context, driver *models.Driver, stops []*models.Participant, group *participantGroup, pos int) (float64, error) {
	before, err := rc.riderScore(ctx, driver, stops)
	if err != nil {
		return 0, err
	}
	return rc.groupInsertionDeltaRiderScoreFrom(ctx, driver, stops, group, pos, before)
}

func (rc routeContext) groupInsertionDeltaRiderScoreFrom(ctx context.Context, driver *models.Driver, stops []*models.Participant, group *participantGroup, pos int, before float64) (float64, error) {
	if driver == nil {
		return 0, fmt.Errorf("route driver is required")
	}
	if group == nil || len(group.members) == 0 {
		return 0, nil
	}

	afterStops := insertGroupAt(stops, group, pos)
	after, err := rc.riderScore(ctx, driver, afterStops)
	if err != nil {
		return 0, err
	}

	return after - before, nil
}

func PopulateRouteMetrics(ctx context.Context, distanceCalc distance.DistanceCalculator, instituteCoords models.Coordinates, mode RouteMode, route *models.CalculatedRoute) error {
	if route == nil {
		return fmt.Errorf("route is required")
	}

	rc := newRouteContext(distanceCalc, instituteCoords, mode)
	participants := make([]*models.Participant, len(route.Stops))
	for i := range route.Stops {
		participants[i] = route.Stops[i].Participant
	}

	metrics, err := rc.evaluateParticipants(ctx, route.Driver, participants)
	if err != nil {
		return err
	}

	for i := range route.Stops {
		route.Stops[i].Order = i
		route.Stops[i].DistanceFromPrevMeters = metrics.Stops[i].DistanceFromPrevMeters
		route.Stops[i].CumulativeDistanceMeters = metrics.Stops[i].CumulativeDistanceMeters
		route.Stops[i].DurationFromPrevSecs = metrics.Stops[i].DurationFromPrevSecs
		route.Stops[i].CumulativeDurationSecs = metrics.Stops[i].CumulativeDurationSecs
	}

	route.TotalDropoffDistanceMeters = metrics.TotalStopDistanceMeters
	route.DistanceToDriverHomeMeters = metrics.FinalLegDistanceMeters
	route.TotalDistanceMeters = metrics.TotalDistanceMeters
	route.BaselineDurationSecs = metrics.BaselineDurationSecs
	route.RouteDurationSecs = metrics.RouteDurationSecs
	route.DetourSecs = metrics.DetourSecs
	route.Mode = rc.mode
	if route.EffectiveCapacity == 0 && route.Driver != nil {
		route.EffectiveCapacity = route.Driver.VehicleCapacity
	}

	return nil
}

// OptimizeRouteOrder reorders one calculated route using the participant-first
// lexicographic objective, then refreshes its displayed metrics.
func OptimizeRouteOrder(ctx context.Context, distanceCalc distance.DistanceCalculator, instituteCoords models.Coordinates, mode RouteMode, route *models.CalculatedRoute) error {
	if route == nil {
		return fmt.Errorf("route is required")
	}
	if route.Driver == nil {
		return fmt.Errorf("route driver is required")
	}

	rc := newRouteContext(distanceCalc, instituteCoords, mode)
	participants := make([]*models.Participant, len(route.Stops))
	for i := range route.Stops {
		participants[i] = route.Stops[i].Participant
	}

	driverID := route.Driver.ID
	routes := map[int64]*balancedRoute{
		driverID: {
			driver: route.Driver,
			stops:  participants,
		},
	}
	router := &BalancedRouter{distanceCalc: distanceCalc}
	if err := router.optimizeRouteOrders(ctx, rc, routes, []int64{driverID}); err != nil {
		return err
	}
	optimized := routes[driverID].stops

	route.Stops = make([]models.RouteStop, len(optimized))
	for i, participant := range optimized {
		route.Stops[i].Participant = participant
	}

	return PopulateRouteMetrics(ctx, distanceCalc, instituteCoords, mode, route)
}
