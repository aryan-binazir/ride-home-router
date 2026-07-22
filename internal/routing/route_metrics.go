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

type edgeScoreCache struct {
	ctx          context.Context
	distanceCalc distance.DistanceCalculator
	selector     func(*distance.DistanceResult) float64
	values       map[string]float64
}

func newEdgeScoreCache(ctx context.Context, distanceCalc distance.DistanceCalculator, selector func(*distance.DistanceResult) float64) *edgeScoreCache {
	return &edgeScoreCache{
		ctx:          ctx,
		distanceCalc: distanceCalc,
		selector:     selector,
		values:       make(map[string]float64),
	}
}

func (c *edgeScoreCache) score(origin, dest models.Coordinates) (float64, error) {
	key := distance.PairCacheKey(origin, dest)
	if value, ok := c.values[key]; ok {
		return value, nil
	}

	result, err := c.distanceCalc.GetDistance(c.ctx, origin, dest)
	if err != nil {
		return 0, err
	}
	value := c.selector(result)
	c.values[key] = value
	return value, nil
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
	return rc.totalDriveDuration(ctx, driver, stops)
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

func (rc routeContext) totalDriveDuration(ctx context.Context, driver *models.Driver, stops []*models.Participant) (float64, error) {
	metrics, err := rc.evaluateParticipants(ctx, driver, stops)
	if err != nil {
		return 0, err
	}
	return metrics.RouteDurationSecs, nil
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

// OptimizeRouteOrder reorders one calculated route by total driven time, then refreshes metrics.
func OptimizeRouteOrder(ctx context.Context, distanceCalc distance.DistanceCalculator, instituteCoords models.Coordinates, mode RouteMode, route *models.CalculatedRoute) error {
	if route == nil {
		return fmt.Errorf("route is required")
	}

	rc := newRouteContext(distanceCalc, instituteCoords, mode)
	participants := make([]*models.Participant, len(route.Stops))
	for i := range route.Stops {
		participants[i] = route.Stops[i].Participant
	}

	optimized, err := rc.twoOptRouteDuration(ctx, route.Driver, participants)
	if err != nil {
		return err
	}

	route.Stops = make([]models.RouteStop, len(optimized))
	for i, participant := range optimized {
		route.Stops[i].Participant = participant
	}

	return PopulateRouteMetrics(ctx, distanceCalc, instituteCoords, mode, route)
}

func (rc routeContext) twoOptDistance(ctx context.Context, driver *models.Driver, stops []*models.Participant) ([]*models.Participant, error) {
	scoreCache := newEdgeScoreCache(ctx, rc.distanceCalc, func(result *distance.DistanceResult) float64 {
		return result.DistanceMeters
	})
	return twoOptByDelta(stops, func(candidate []*models.Participant, i, j int) (float64, error) {
		return rc.twoOptDeltaFromScores(driver, candidate, i, j, scoreCache.score, rc.objectiveIncludesTerminal())
	})
}

func (rc routeContext) twoOptRouteDuration(ctx context.Context, driver *models.Driver, stops []*models.Participant) ([]*models.Participant, error) {
	blocks := routeHouseholdBlocks(stops)
	if len(blocks) < 2 {
		return stops, nil
	}

	scoreCache := newEdgeScoreCache(ctx, rc.distanceCalc, func(result *distance.DistanceResult) float64 {
		return result.DurationSecs
	})
	optimizedBlocks, err := twoOptBlocksByDelta(blocks, func(candidate []*participantGroup, i, j int) (float64, error) {
		return rc.twoOptBlockDeltaFromScores(driver, candidate, i, j, scoreCache.score, true)
	})
	if err != nil {
		return nil, err
	}

	return flattenParticipantGroups(optimizedBlocks), nil
}

func blockFirstMember(block *participantGroup) *models.Participant {
	return block.members[0]
}

func blockLastMember(block *participantGroup) *models.Participant {
	return block.members[len(block.members)-1]
}

func twoOptBlocksByDelta(blocks []*participantGroup, deltaFn func([]*participantGroup, int, int) (float64, error)) ([]*participantGroup, error) {
	if len(blocks) < 2 {
		return blocks, nil
	}

	current := append([]*participantGroup(nil), blocks...)
	improved := true
	for improved {
		improved = false
		for i := 0; i < len(current)-1; i++ {
			for j := i + 2; j <= len(current); j++ {
				delta, err := deltaFn(current, i, j)
				if err != nil {
					return nil, err
				}
				if delta < -scoreImprovementEpsilon {
					reverseParticipantGroups(current, i, j-1)
					improved = true
				}
			}
		}
	}

	return current, nil
}

func (rc routeContext) twoOptBlockDelta(ctx context.Context, driver *models.Driver, blocks []*participantGroup, i, j int, selector func(*distance.DistanceResult) float64, includeTerminal bool) (float64, error) {
	scoreCache := newEdgeScoreCache(ctx, rc.distanceCalc, selector)
	return rc.twoOptBlockDeltaFromScores(driver, blocks, i, j, scoreCache.score, includeTerminal)
}

func (rc routeContext) twoOptBlockDeltaFromScores(driver *models.Driver, blocks []*participantGroup, i, j int, score func(models.Coordinates, models.Coordinates) (float64, error), includeTerminal bool) (float64, error) {
	if driver == nil {
		return 0, fmt.Errorf("route driver is required")
	}

	beforeI := rc.origin(driver)
	if prev := i - 1; prev >= 0 && prev < len(blocks) {
		beforeI = blockLastMember(blocks[prev]).GetCoords()
	}

	firstI := blockFirstMember(blocks[i]).GetCoords()
	lastI := blockLastMember(blocks[i]).GetCoords()
	firstJM1 := blockFirstMember(blocks[j-1]).GetCoords()
	lastJM1 := blockLastMember(blocks[j-1]).GetCoords()

	currentFirst, err := score(beforeI, firstI)
	if err != nil {
		return 0, err
	}
	newFirst, err := score(beforeI, firstJM1)
	if err != nil {
		return 0, err
	}

	delta := newFirst - currentFirst
	for k := i; k < j-1; k++ {
		currentEdge, err := score(blockLastMember(blocks[k]).GetCoords(), blockFirstMember(blocks[k+1]).GetCoords())
		if err != nil {
			return 0, err
		}
		newEdge, err := score(blockLastMember(blocks[k+1]).GetCoords(), blockFirstMember(blocks[k]).GetCoords())
		if err != nil {
			return 0, err
		}
		delta += newEdge - currentEdge
	}

	if j < len(blocks) {
		afterJ := blockFirstMember(blocks[j]).GetCoords()
		currentSecond, err := score(lastJM1, afterJ)
		if err != nil {
			return 0, err
		}
		newSecond, err := score(lastI, afterJ)
		if err != nil {
			return 0, err
		}
		delta += newSecond - currentSecond
		return delta, nil
	}

	if includeTerminal {
		destination := rc.destination(driver)
		currentTerminal, err := score(lastJM1, destination)
		if err != nil {
			return 0, err
		}
		newTerminal, err := score(lastI, destination)
		if err != nil {
			return 0, err
		}
		delta += newTerminal - currentTerminal
	}

	return delta, nil
}

func (rc routeContext) twoOptDelta(ctx context.Context, driver *models.Driver, stops []*models.Participant, i, j int, selector func(*distance.DistanceResult) float64, includeTerminal bool) (float64, error) {
	scoreCache := newEdgeScoreCache(ctx, rc.distanceCalc, selector)
	return rc.twoOptDeltaFromScores(driver, stops, i, j, scoreCache.score, includeTerminal)
}

func (rc routeContext) twoOptDeltaFromScores(driver *models.Driver, stops []*models.Participant, i, j int, score func(models.Coordinates, models.Coordinates) (float64, error), includeTerminal bool) (float64, error) {
	if driver == nil {
		return 0, fmt.Errorf("route driver is required")
	}

	beforeI := rc.origin(driver)
	if prev := i - 1; prev >= 0 && prev < len(stops) {
		beforeI = stops[prev].GetCoords()
	}

	currentFirst, err := score(beforeI, stops[i].GetCoords())
	if err != nil {
		return 0, err
	}
	newFirst, err := score(beforeI, stops[j-1].GetCoords())
	if err != nil {
		return 0, err
	}

	delta := newFirst - currentFirst
	for k := i; k < j-1; k++ {
		currentEdge, err := score(stops[k].GetCoords(), stops[k+1].GetCoords())
		if err != nil {
			return 0, err
		}
		newEdge, err := score(stops[k+1].GetCoords(), stops[k].GetCoords())
		if err != nil {
			return 0, err
		}
		delta += newEdge - currentEdge
	}

	if j < len(stops) {
		afterJ := stops[j].GetCoords()
		currentSecond, err := score(stops[j-1].GetCoords(), afterJ)
		if err != nil {
			return 0, err
		}
		newSecond, err := score(stops[i].GetCoords(), afterJ)
		if err != nil {
			return 0, err
		}
		delta += newSecond - currentSecond
		return delta, nil
	}

	if includeTerminal {
		destination := rc.destination(driver)
		currentTerminal, err := score(stops[j-1].GetCoords(), destination)
		if err != nil {
			return 0, err
		}
		newTerminal, err := score(stops[i].GetCoords(), destination)
		if err != nil {
			return 0, err
		}
		delta += newTerminal - currentTerminal
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
				if delta < -scoreImprovementEpsilon {
					reverse(current, i, j-1)
					improved = true
				}
			}
		}
	}

	return current, nil
}
