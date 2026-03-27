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

func (rc routeContext) routeDuration(ctx context.Context, driver *models.Driver, stops []*models.Participant) (float64, error) {
	return rc.objectiveCost(ctx, driver, stops, func(result *distance.DistanceResult) float64 {
		return result.DurationSecs
	})
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

func (rc routeContext) objectiveIncludesTerminal() bool {
	return rc.mode == RouteModePickup
}

func (rc routeContext) objectiveCost(ctx context.Context, driver *models.Driver, stops []*models.Participant, selector func(*distance.DistanceResult) float64) (float64, error) {
	if driver == nil {
		return 0, fmt.Errorf("route driver is required")
	}
	if len(stops) == 0 {
		return 0, nil
	}

	total := 0.0
	prev := rc.origin(driver)
	for i, stop := range stops {
		if stop == nil {
			return 0, fmt.Errorf("route stop %d is missing participant data", i)
		}

		dist, err := rc.distanceCalc.GetDistance(ctx, prev, stop.GetCoords())
		if err != nil {
			return 0, err
		}
		total += selector(dist)
		prev = stop.GetCoords()
	}

	if rc.objectiveIncludesTerminal() {
		dist, err := rc.distanceCalc.GetDistance(ctx, prev, rc.destination(driver))
		if err != nil {
			return 0, err
		}
		total += selector(dist)
	}

	return total, nil
}

func (rc routeContext) insertionDeltaDistance(ctx context.Context, driver *models.Driver, stops []*models.Participant, participant *models.Participant, pos int) (float64, error) {
	return rc.insertionDelta(ctx, driver, stops, participant, pos, func(result *distance.DistanceResult) float64 {
		return result.DistanceMeters
	})
}

func (rc routeContext) insertionDeltaDuration(ctx context.Context, driver *models.Driver, stops []*models.Participant, participant *models.Participant, pos int) (float64, error) {
	return rc.insertionDelta(ctx, driver, stops, participant, pos, func(result *distance.DistanceResult) float64 {
		return result.DurationSecs
	})
}

func (rc routeContext) insertionDelta(ctx context.Context, driver *models.Driver, stops []*models.Participant, participant *models.Participant, pos int, selector func(*distance.DistanceResult) float64) (float64, error) {
	if driver == nil {
		return 0, fmt.Errorf("route driver is required")
	}
	if participant == nil {
		return 0, fmt.Errorf("participant is required")
	}

	prev := rc.origin(driver)
	if pos > 0 {
		prev = stops[pos-1].GetCoords()
	}

	if pos == len(stops) && !rc.objectiveIncludesTerminal() {
		prevToParticipant, err := rc.distanceCalc.GetDistance(ctx, prev, participant.GetCoords())
		if err != nil {
			return 0, err
		}
		return selector(prevToParticipant), nil
	}

	next := rc.destination(driver)
	if pos < len(stops) {
		next = stops[pos].GetCoords()
	}

	prevToNext, err := rc.distanceCalc.GetDistance(ctx, prev, next)
	if err != nil {
		return 0, err
	}
	prevToParticipant, err := rc.distanceCalc.GetDistance(ctx, prev, participant.GetCoords())
	if err != nil {
		return 0, err
	}
	participantToNext, err := rc.distanceCalc.GetDistance(ctx, participant.GetCoords(), next)
	if err != nil {
		return 0, err
	}

	return selector(prevToParticipant) + selector(participantToNext) - selector(prevToNext), nil
}

func (rc routeContext) groupInsertionDeltaDuration(ctx context.Context, driver *models.Driver, stops []*models.Participant, group *participantGroup, pos int) (float64, error) {
	if driver == nil {
		return 0, fmt.Errorf("route driver is required")
	}
	if group == nil || len(group.members) == 0 {
		return 0, nil
	}

	prev := rc.origin(driver)
	if pos > 0 {
		prev = stops[pos-1].GetCoords()
	}

	if pos == len(stops) && !rc.objectiveIncludesTerminal() {
		total := 0.0
		current := prev
		for i, member := range group.members {
			if member == nil {
				return 0, fmt.Errorf("group member %d is missing participant data", i)
			}

			dist, err := rc.distanceCalc.GetDistance(ctx, current, member.GetCoords())
			if err != nil {
				return 0, err
			}
			total += dist.DurationSecs
			current = member.GetCoords()
		}
		return total, nil
	}

	next := rc.destination(driver)
	if pos < len(stops) {
		next = stops[pos].GetCoords()
	}

	total := 0.0
	current := prev
	for i, member := range group.members {
		if member == nil {
			return 0, fmt.Errorf("group member %d is missing participant data", i)
		}

		dist, err := rc.distanceCalc.GetDistance(ctx, current, member.GetCoords())
		if err != nil {
			return 0, err
		}
		total += dist.DurationSecs
		current = member.GetCoords()
	}

	lastToNext, err := rc.distanceCalc.GetDistance(ctx, current, next)
	if err != nil {
		return 0, err
	}
	prevToNext, err := rc.distanceCalc.GetDistance(ctx, prev, next)
	if err != nil {
		return 0, err
	}

	return total + lastToNext.DurationSecs - prevToNext.DurationSecs, nil
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

func (rc routeContext) twoOptDistance(ctx context.Context, driver *models.Driver, stops []*models.Participant) ([]*models.Participant, error) {
	return twoOptByDelta(stops, func(candidate []*models.Participant, i, j int) (float64, error) {
		return rc.twoOptDelta(ctx, driver, candidate, i, j, func(result *distance.DistanceResult) float64 {
			return result.DistanceMeters
		})
	})
}

func (rc routeContext) twoOptDuration(ctx context.Context, driver *models.Driver, stops []*models.Participant) ([]*models.Participant, error) {
	return twoOptByDelta(stops, func(candidate []*models.Participant, i, j int) (float64, error) {
		return rc.twoOptDelta(ctx, driver, candidate, i, j, func(result *distance.DistanceResult) float64 {
			return result.DurationSecs
		})
	})
}

func (rc routeContext) twoOptDelta(ctx context.Context, driver *models.Driver, stops []*models.Participant, i, j int, selector func(*distance.DistanceResult) float64) (float64, error) {
	if driver == nil {
		return 0, fmt.Errorf("route driver is required")
	}

	beforeI := rc.origin(driver)
	if i > 0 {
		beforeI = stops[i-1].GetCoords()
	}

	currentFirst, err := rc.distanceCalc.GetDistance(ctx, beforeI, stops[i].GetCoords())
	if err != nil {
		return 0, err
	}
	newFirst, err := rc.distanceCalc.GetDistance(ctx, beforeI, stops[j-1].GetCoords())
	if err != nil {
		return 0, err
	}

	delta := selector(newFirst) - selector(currentFirst)
	if j < len(stops) {
		afterJ := stops[j].GetCoords()
		currentSecond, err := rc.distanceCalc.GetDistance(ctx, stops[j-1].GetCoords(), afterJ)
		if err != nil {
			return 0, err
		}
		newSecond, err := rc.distanceCalc.GetDistance(ctx, stops[i].GetCoords(), afterJ)
		if err != nil {
			return 0, err
		}
		delta += selector(newSecond) - selector(currentSecond)
		return delta, nil
	}

	if rc.objectiveIncludesTerminal() {
		destination := rc.destination(driver)
		currentTerminal, err := rc.distanceCalc.GetDistance(ctx, stops[j-1].GetCoords(), destination)
		if err != nil {
			return 0, err
		}
		newTerminal, err := rc.distanceCalc.GetDistance(ctx, stops[i].GetCoords(), destination)
		if err != nil {
			return 0, err
		}
		delta += selector(newTerminal) - selector(currentTerminal)
	}

	return delta, nil
}

func twoOptByDelta(stops []*models.Participant, deltaFn func([]*models.Participant, int, int) (float64, error)) ([]*models.Participant, error) {
	if len(stops) < 3 {
		return stops, nil
	}

	current := append([]*models.Participant(nil), stops...)
	improved := true
	for improved {
		improved = false
		for i := 0; i < len(current)-1; i++ {
			for j := i + 2; j <= len(current); j++ {
				delta, err := deltaFn(current, i, j)
				if err != nil {
					return nil, err
				}
				if delta < 0 {
					reverse(current, i, j-1)
					improved = true
				}
			}
		}
	}

	return current, nil
}
