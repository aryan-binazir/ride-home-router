package routing

import (
	"context"
	"fmt"
	"log"
	"maps"
	"math"
	"ride-home-router/internal/distance"
	"ride-home-router/internal/models"
	"slices"
	"sort"
	"strings"
	"time"
)

// BalancedRouter assigns participants under vehicle and household constraints,
// then improves the complete solution using the participant-first objective.
type BalancedRouter struct {
	distanceCalc distance.SolveSource
}

const (
	scoreImprovementEpsilon           = 0.001
	maxAssignmentCandidateEvaluations = 10000
)

// NewBalancedRouter creates a participant-first bounded-search router.
func NewBalancedRouter(distanceCalc distance.SolveSource) Router {
	return &BalancedRouter{
		distanceCalc: distanceCalc,
	}
}

func (r *BalancedRouter) CalculateRoutes(ctx context.Context, req *RoutingRequest) (*models.RoutingResult, error) {
	totalStart := time.Now()

	mode := normalizeRouteMode(req.Mode)

	log.Printf("[BALANCED] Starting calculation: participants=%d drivers=%d mode=%s",
		len(req.Participants), len(req.Drivers), mode)

	// Handle empty participants
	if len(req.Participants) == 0 {
		return &models.RoutingResult{
			Routes:  []models.CalculatedRoute{},
			Summary: models.RoutingSummary{},
			Mode:    mode,
		}, nil
	}

	// Handle empty drivers
	if len(req.Drivers) == 0 {
		return nil, &ErrRoutingFailed{
			Reason:            "No drivers available",
			UnassignedCount:   len(req.Participants),
			TotalCapacity:     0,
			TotalParticipants: len(req.Participants),
		}
	}

	// Prewarm distance cache with only the directed pairs needed for this solve.
	prewarmStart := time.Now()
	distanceLookup, err := prepareSolveDistances(ctx, r.distanceCalc, req)
	if err != nil {
		return nil, err
	}
	log.Printf("[TIMING] Prewarm cache: %v", time.Since(prewarmStart))
	rc := newRouteContext(distanceLookup, req.InstituteCoords, mode)

	// Initialize routes for each driver
	routes := make(map[int64]*balancedRoute)
	driverIDs := make([]int64, 0, len(req.Drivers))
	for i := range req.Drivers {
		driver := &req.Drivers[i]
		routes[driver.ID] = &balancedRoute{
			driver: driver,
			stops:  []*models.Participant{},
		}
		driverIDs = append(driverIDs, driver.ID)
	}

	// Build unassigned list
	unassigned := make([]*models.Participant, len(req.Participants))
	for i := range req.Participants {
		unassigned[i] = &req.Participants[i]
	}

	// Phase 1: Build a feasible rider-score seed. The complete lexicographic
	// objective is applied by the ordering and assignment phases below.
	phase1Start := time.Now()
	seedName := "bearing-sweep"
	if r.bearingSweepInsertion(req.InstituteCoords, routes, driverIDs, unassigned) {
		unassigned = nil
	} else {
		seedName = "round-robin fallback"
		fallbackUnassigned, err := roundRobinInsertion(ctx, rc, routes, driverIDs, unassigned)
		if err != nil {
			return nil, err
		}
		unassigned = fallbackUnassigned
	}
	if len(unassigned) == 0 {
		repairs, err := r.maximizeNonemptyRoutes(ctx, rc, routes, driverIDs)
		if err != nil {
			return nil, err
		}
		if repairs > 0 {
			log.Printf("[BALANCED] Phase 1 filled %d additional driver routes", repairs)
		}
	}
	log.Printf("[BALANCED] Phase 1 seed: %s", seedName)
	log.Printf("[TIMING] Phase 1 (%s): %v", seedName, time.Since(phase1Start))

	// Phase 2: Improve route order in the context of the complete solution.
	phase2Start := time.Now()
	if err := rc.optimizeRouteOrders(ctx, routes, driverIDs); err != nil {
		return nil, err
	}
	log.Printf("[TIMING] Phase 2 (route ordering): %v", time.Since(phase2Start))

	// Phase 3: Always search relocations and household swaps, including swaps
	// between saturated vehicles.
	phase3Start := time.Now()
	iterations, err := optimizeAssignments(ctx, rc, routes, driverIDs)
	if err != nil {
		return nil, err
	}
	log.Printf("[TIMING] Phase 3 (assignment search): %v (iterations=%d)", time.Since(phase3Start), iterations)

	// Check for unassigned participants
	if len(unassigned) > 0 {
		totalCapacity := 0
		for _, d := range req.Drivers {
			totalCapacity += d.VehicleCapacity
		}
		return nil, &ErrRoutingFailed{
			Reason:            "Cannot assign all participants",
			UnassignedCount:   len(unassigned),
			TotalCapacity:     totalCapacity,
			TotalParticipants: len(req.Participants),
		}
	}

	// Build result
	result, err := buildResult(ctx, rc, routes, len(req.Participants))
	if err != nil {
		return nil, err
	}

	log.Printf("[BALANCED] Complete: drivers_used=%d total_distance=%.0fm",
		result.Summary.TotalDriversUsed, result.Summary.TotalDropoffDistanceMeters)
	log.Printf("[TIMING] TOTAL: %v", time.Since(totalStart))

	return result, nil
}

// balancedRoute tracks the driver and assigned participant order.
type balancedRoute struct {
	driver *models.Driver
	stops  []*models.Participant
}

// bearingSweepInsertion partitions household groups into contiguous bearing
// arcs and matches each arc to the unused driver whose home bearing is nearest.
// It commits assignments only when the entire sweep succeeds.
func (r *BalancedRouter) bearingSweepInsertion(institute models.Coordinates, routes map[int64]*balancedRoute, driverIDs []int64, unassigned []*models.Participant) bool {
	if len(unassigned) == 0 {
		return true
	}
	if len(driverIDs) == 0 {
		return false
	}

	groups := bearingSweepGroups(institute, unassigned)
	workingRoutes := cloneBalancedRoutes(routes, driverIDs)
	if len(workingRoutes) != len(driverIDs) {
		return false
	}

	orderedDriverIDs := slices.Clone(driverIDs)
	slices.Sort(orderedDriverIDs)
	usedDrivers := make(map[int64]struct{}, len(orderedDriverIDs))
	driversWithGroups := make(map[int64]struct{}, len(orderedDriverIDs))
	rejectedForGroup := make(map[int64]struct{}, len(orderedDriverIDs))
	reserveEveryDriver := len(groups) >= len(orderedDriverIDs)

	maxVehicleCapacity := maxRouteVehicleCapacity(workingRoutes)
	splittableHouseholds := make(map[string]struct{})
	for _, group := range groups {
		if len(group.members) > maxVehicleCapacity {
			splittableHouseholds[participantGroupKey(group)] = struct{}{}
		}
	}

	for len(groups) > 0 {
		driverID, ok := closestUnusedDriverByBearing(institute, groups[0], workingRoutes, orderedDriverIDs, usedDrivers, rejectedForGroup)
		if !ok {
			return false
		}
		route := workingRoutes[driverID]
		arcHasGroup := false

		for len(groups) > 0 {
			group := groups[0]
			remainingCapacity := route.driver.VehicleCapacity - len(route.stops)
			if remainingCapacity <= 0 {
				break
			}

			assignedCount := len(group.members)
			if assignedCount > remainingCapacity {
				if _, splittable := splittableHouseholds[participantGroupKey(group)]; !splittable {
					break
				}
				assignedCount = 1
			}

			remainingGroupCount := len(groups)
			if assignedCount == len(group.members) {
				remainingGroupCount--
			}
			remainingUnusedDrivers := len(orderedDriverIDs) - len(usedDrivers)
			if arcHasGroup && remainingGroupCount < remainingUnusedDrivers {
				break
			}
			if !assignmentPreservesCapacityFeasibility(workingRoutes, driverID, groups, 0, assignedCount, splittableHouseholds) {
				break
			}

			route.stops = append(route.stops, group.members[:assignedCount]...)
			usedDrivers[driverID] = struct{}{}
			driversWithGroups[driverID] = struct{}{}
			arcHasGroup = true
			if assignedCount == len(group.members) {
				groups = groups[1:]
			} else {
				group.members = group.members[assignedCount:]
			}
		}
		if !arcHasGroup {
			rejectedForGroup[driverID] = struct{}{}
			continue
		}
		clear(rejectedForGroup)
	}

	if reserveEveryDriver && len(driversWithGroups) != len(orderedDriverIDs) {
		return false
	}
	for _, driverID := range orderedDriverIDs {
		routes[driverID].stops = workingRoutes[driverID].stops
	}
	return true
}

func bearingSweepGroups(institute models.Coordinates, participants []*models.Participant) []*participantGroup {
	groups := groupParticipantsByAddress(participants)
	sort.Slice(groups, func(i, j int) bool {
		iBearing := bearingFromInstitute(institute, models.Coordinates{Lat: groups[i].lat, Lng: groups[i].lng})
		jBearing := bearingFromInstitute(institute, models.Coordinates{Lat: groups[j].lat, Lng: groups[j].lng})
		if iBearing != jBearing {
			return iBearing < jBearing
		}
		return participantGroupKey(groups[i]) < participantGroupKey(groups[j])
	})
	if len(groups) <= 1 {
		return groups
	}

	largestGap := -1.0
	startIndex := 0
	startKey := ""
	for i, group := range groups {
		nextIndex := (i + 1) % len(groups)
		currentBearing := bearingFromInstitute(institute, models.Coordinates{Lat: group.lat, Lng: group.lng})
		nextGroup := groups[nextIndex]
		nextBearing := bearingFromInstitute(institute, models.Coordinates{Lat: nextGroup.lat, Lng: nextGroup.lng})
		gap := math.Mod(nextBearing-currentBearing+360, 360)
		nextKey := participantGroupKey(nextGroup)
		if gap > largestGap || gap == largestGap && nextKey < startKey {
			largestGap = gap
			startIndex = nextIndex
			startKey = nextKey
		}
	}

	ordered := make([]*participantGroup, 0, len(groups))
	ordered = append(ordered, groups[startIndex:]...)
	ordered = append(ordered, groups[:startIndex]...)
	return ordered
}

func cloneBalancedRoutes(routes map[int64]*balancedRoute, driverIDs []int64) map[int64]*balancedRoute {
	cloned := make(map[int64]*balancedRoute, len(driverIDs))
	for _, driverID := range driverIDs {
		route, ok := routes[driverID]
		if !ok || route == nil || route.driver == nil {
			continue
		}
		cloned[driverID] = &balancedRoute{
			driver: route.driver,
			stops:  slices.Clone(route.stops),
		}
	}
	return cloned
}

func closestUnusedDriverByBearing(institute models.Coordinates, group *participantGroup, routes map[int64]*balancedRoute, driverIDs []int64, used, rejected map[int64]struct{}) (int64, bool) {
	groupBearing := bearingFromInstitute(institute, models.Coordinates{Lat: group.lat, Lng: group.lng})
	bestDistance := math.Inf(1)
	var bestDriverID int64
	found := false
	for _, driverID := range driverIDs {
		if _, alreadyUsed := used[driverID]; alreadyUsed {
			continue
		}
		if _, alreadyRejected := rejected[driverID]; alreadyRejected {
			continue
		}
		route, ok := routes[driverID]
		if !ok || route == nil || route.driver == nil {
			continue
		}
		driverBearing := bearingFromInstitute(institute, route.driver.GetCoords())
		distance := angularDistance(groupBearing, driverBearing)
		if !found || distance < bestDistance || distance == bestDistance && driverID < bestDriverID {
			bestDistance = distance
			bestDriverID = driverID
			found = true
		}
	}
	return bestDriverID, found
}

func angularDistance(first, second float64) float64 {
	difference := math.Abs(first - second)
	return min(difference, 360-difference)
}

type nonemptyRouteRepair struct {
	stops map[int64][]*models.Participant
	score solutionScore
	found bool
}

// maximizeNonemptyRoutes augments the seed through chains of household-block
// relocations. A chain may temporarily transfer the empty route between
// drivers, but every committed chain ends at a route with multiple blocks and
// therefore increases the number of nonempty routes by one.
func (r *BalancedRouter) maximizeNonemptyRoutes(ctx context.Context, rc routeContext, routes map[int64]*balancedRoute, driverIDs []int64) (int, error) {
	orderedDriverIDs := slices.Clone(driverIDs)
	slices.Sort(orderedDriverIDs)
	repairs := 0

	for {
		baseStops := make(map[int64][]*models.Participant, len(orderedDriverIDs))
		baseMetrics := make(map[int64]routeObjectiveMetrics, len(orderedDriverIDs))
		for _, driverID := range orderedDriverIDs {
			baseStops[driverID] = slices.Clone(routes[driverID].stops)
			metrics, err := rc.evaluateRouteObjective(ctx, routes[driverID].driver, routes[driverID].stops)
			if err != nil {
				return repairs, err
			}
			baseMetrics[driverID] = metrics
		}
		currentScore := scoreSolution(baseMetrics, orderedDriverIDs)
		best := nonemptyRouteRepair{score: currentScore}

		var search func(int64, map[int64][]*models.Participant, map[int64]struct{}) error
		search = func(emptyDriverID int64, workingStops map[int64][]*models.Participant, visited map[int64]struct{}) error {
			for _, sourceDriverID := range orderedDriverIDs {
				if sourceDriverID == emptyDriverID {
					continue
				}
				if _, alreadyVisited := visited[sourceDriverID]; alreadyVisited {
					continue
				}

				sourcePosition := 0
				for _, sourceGroup := range routeHouseholdBlocks(workingStops[sourceDriverID]) {
					groupSize := len(sourceGroup.members)
					if groupSize > routes[emptyDriverID].driver.VehicleCapacity {
						sourcePosition += groupSize
						continue
					}

					candidateStops := maps.Clone(workingStops)
					candidateStops[sourceDriverID] = removeRange(workingStops[sourceDriverID], sourcePosition, sourcePosition+groupSize)
					candidateStops[emptyDriverID] = slices.Clone(sourceGroup.members)
					if len(candidateStops[sourceDriverID]) == 0 {
						candidateVisited := maps.Clone(visited)
						candidateVisited[emptyDriverID] = struct{}{}
						if err := search(sourceDriverID, candidateStops, candidateVisited); err != nil {
							return err
						}
						sourcePosition += groupSize
						continue
					}

					candidateMetrics := make(map[int64]routeObjectiveMetrics, len(orderedDriverIDs))
					for _, driverID := range orderedDriverIDs {
						metrics, err := rc.evaluateRouteObjective(ctx, routes[driverID].driver, candidateStops[driverID])
						if err != nil {
							return err
						}
						candidateMetrics[driverID] = metrics
					}
					candidateScore := scoreSolution(candidateMetrics, orderedDriverIDs)
					if candidateScore.betterThan(currentScore) && (!best.found || candidateScore.betterThan(best.score)) {
						best = nonemptyRouteRepair{stops: candidateStops, score: candidateScore, found: true}
					}
					sourcePosition += groupSize
				}
			}
			return nil
		}

		for _, emptyDriverID := range orderedDriverIDs {
			if len(baseStops[emptyDriverID]) != 0 {
				continue
			}
			if err := search(emptyDriverID, baseStops, map[int64]struct{}{}); err != nil {
				return repairs, err
			}
		}
		if !best.found {
			return repairs, nil
		}
		for _, driverID := range orderedDriverIDs {
			routes[driverID].stops = best.stops[driverID]
		}
		repairs++
	}
}

// roundRobinInsertion assigns participants by cycling through drivers
// Groups participants from the same household and assigns them together
func roundRobinInsertion(ctx context.Context, rc routeContext, routes map[int64]*balancedRoute, driverIDs []int64, unassigned []*models.Participant) ([]*models.Participant, error) {
	// Sort drivers by ID for consistent ordering
	slices.Sort(driverIDs)

	// Group participants by address
	groups := groupParticipantsByAddress(unassigned)
	maxVehicleCapacity := maxRouteVehicleCapacity(routes)
	splittableHouseholds := make(map[string]struct{})
	for _, group := range groups {
		if len(group.members) > maxVehicleCapacity {
			splittableHouseholds[participantGroupKey(group)] = struct{}{}
		}
	}

	totalParticipants := len(unassigned)
	log.Printf("[BALANCED] Distributing %d participants (%d household groups) across %d drivers",
		totalParticipants, len(groups), len(driverIDs))

	// Round-robin through drivers, assigning best-fit group to each
	driverIndex := 0
	maxRounds := totalParticipants * len(driverIDs) * 2 // Safety limit (based on participants, not groups, to handle household splitting)

	for len(groups) > 0 && maxRounds > 0 {
		maxRounds--

		// Find next driver with capacity for at least 1 participant
		foundDriver := false
		startIndex := driverIndex
		for {
			driverID := driverIDs[driverIndex]
			route := routes[driverID]

			if len(route.stops) < route.driver.VehicleCapacity {
				foundDriver = true
				break
			}

			driverIndex = (driverIndex + 1) % len(driverIDs)
			if driverIndex == startIndex {
				break // All drivers full
			}
		}

		if !foundDriver {
			break // All drivers at capacity
		}

		currentDriverID := driverIDs[driverIndex]
		route := routes[currentDriverID]
		remainingCapacity := route.driver.VehicleCapacity - len(route.stops)
		routeScore, err := rc.riderScore(ctx, route.driver, route.stops)
		if err != nil {
			return nil, err
		}

		// Find best group and position for this driver (minimize insertion cost)
		bestCost := math.Inf(1)
		var bestGroup *participantGroup
		var bestGroupIndex int
		var bestPosition int

		for groupIdx, group := range groups {
			groupSize := len(group.members)

			// Check if group fits in remaining capacity
			if groupSize > remainingCapacity {
				// Group too large - skip; we'll try splitting individuals below
				continue
			}
			if !assignmentPreservesCapacityFeasibility(routes, currentDriverID, groups, groupIdx, groupSize, splittableHouseholds) {
				continue
			}

			// Try all insertion positions for this group
			for _, pos := range householdBoundaryPositions(route.stops) {
				cost, err := rc.groupInsertionDeltaRiderScoreFrom(ctx, route.driver, route.stops, group, pos, routeScore)
				if err != nil {
					return nil, err
				}

				if cost < bestCost {
					bestCost = cost
					bestGroup = group
					bestGroupIndex = groupIdx
					bestPosition = pos
				}
			}
		}

		// If no whole group fits this vehicle, split only households that cannot
		// fit in any selected vehicle.
		if bestGroup == nil {
			for groupIdx, group := range groups {
				if len(group.members) == 0 {
					continue
				}
				if _, ok := splittableHouseholds[participantGroupKey(group)]; !ok {
					continue
				}
				if !assignmentPreservesCapacityFeasibility(routes, currentDriverID, groups, groupIdx, 1, splittableHouseholds) {
					continue
				}

				// Try just the first member of the group, but still only at
				// household boundaries so existing same-address riders stay adjacent.
				for _, pos := range householdBoundaryPositions(route.stops) {
					singleGroup := &participantGroup{
						members: []*models.Participant{group.members[0]},
						address: group.address,
						lat:     group.lat,
						lng:     group.lng,
					}
					cost, err := rc.groupInsertionDeltaRiderScoreFrom(ctx, route.driver, route.stops, singleGroup, pos, routeScore)
					if err != nil {
						return nil, err
					}

					if cost < bestCost {
						bestCost = cost
						bestGroup = singleGroup
						bestGroupIndex = groupIdx
						bestPosition = pos
					}
				}
			}
		}

		if bestGroup == nil {
			driverIndex = (driverIndex + 1) % len(driverIDs)
			if driverIndex == startIndex {
				break
			}
			continue
		}

		// Insert the group
		route.stops = insertGroupAt(route.stops, bestGroup, bestPosition)

		memberNames := make([]string, len(bestGroup.members))
		for i, m := range bestGroup.members {
			memberNames[i] = m.Name
		}

		if len(bestGroup.members) == 1 {
			log.Printf("[BALANCED] Assigned %s to %s (pos=%d, rider_score_delta=%.0f)",
				memberNames[0], route.driver.Name, bestPosition, bestCost)
		} else {
			log.Printf("[BALANCED] Assigned household group [%v] to %s (pos=%d, rider_score_delta=%.0f, size=%d)",
				memberNames, route.driver.Name, bestPosition, bestCost, len(bestGroup.members))
		}

		// Remove assigned participants from the group
		originalGroup := groups[bestGroupIndex]
		if len(bestGroup.members) == len(originalGroup.members) {
			// Entire group was assigned - remove from groups list
			groups = append(groups[:bestGroupIndex], groups[bestGroupIndex+1:]...)
		} else {
			// Partial assignment - remove assigned member from original group
			// This handles the case where we split a group
			assignedID := bestGroup.members[0].ID
			newMembers := make([]*models.Participant, 0, len(originalGroup.members)-1)
			for _, m := range originalGroup.members {
				if m.ID != assignedID {
					newMembers = append(newMembers, m)
				}
			}
			originalGroup.members = newMembers
		}

		// Move to next driver (round-robin)
		driverIndex = (driverIndex + 1) % len(driverIDs)
	}

	// Build list of unassigned participants from remaining groups
	unassignedResult := make([]*models.Participant, 0)
	for _, group := range groups {
		unassignedResult = append(unassignedResult, group.members...)
	}

	return unassignedResult, nil
}

type routeObjectiveMetrics struct {
	latestParticipantCompletion    float64
	aggregateParticipantCompletion float64
	driverDetour                   float64
	driveDuration                  float64
	corridorSpread                 int
	used                           bool
}

type solutionScore struct {
	corridorSpread                 int
	latestParticipantCompletion    float64
	maxDriverDetour                float64
	aggregateParticipantCompletion float64
	aggregateDriveDuration         float64
	usedDrivers                    int
}

func (score solutionScore) betterThan(other solutionScore) bool {
	if score.usedDrivers != other.usedDrivers {
		return score.usedDrivers > other.usedDrivers
	}
	if score.corridorSpread != other.corridorSpread {
		return score.corridorSpread < other.corridorSpread
	}

	for _, values := range [][2]float64{
		{score.latestParticipantCompletion, other.latestParticipantCompletion},
		{score.maxDriverDetour, other.maxDriverDetour},
		{score.aggregateParticipantCompletion, other.aggregateParticipantCompletion},
		{score.aggregateDriveDuration, other.aggregateDriveDuration},
	} {
		if values[0] < values[1]-scoreImprovementEpsilon {
			return true
		}
		if values[0] > values[1]+scoreImprovementEpsilon {
			return false
		}
	}

	return false
}

func (rc routeContext) evaluateRouteObjective(ctx context.Context, driver *models.Driver, stops []*models.Participant) (routeObjectiveMetrics, error) {
	if len(stops) == 0 {
		return routeObjectiveMetrics{}, nil
	}

	metrics, err := rc.evaluateParticipants(ctx, driver, stops)
	if err != nil {
		return routeObjectiveMetrics{}, err
	}

	result := routeObjectiveMetrics{
		driverDetour:  metrics.DetourSecs,
		driveDuration: metrics.RouteDurationSecs,
		corridorSpread: int(math.Round(
			routeCorridorSpread(rc.instituteCoords, stops) / 10.0,
		)),
		used: true,
	}
	if rc.mode == RouteModePickup {
		result.latestParticipantCompletion = metrics.RouteDurationSecs
		result.aggregateParticipantCompletion = metrics.RouteDurationSecs * float64(len(stops))
		return result, nil
	}

	for _, stop := range metrics.Stops {
		result.latestParticipantCompletion = max(result.latestParticipantCompletion, stop.CumulativeDurationSecs)
		result.aggregateParticipantCompletion += stop.CumulativeDurationSecs
	}
	return result, nil
}

func scoreSolution(routeMetrics map[int64]routeObjectiveMetrics, driverIDs []int64) solutionScore {
	result := solutionScore{maxDriverDetour: math.Inf(-1)}
	for _, driverID := range driverIDs {
		metrics := routeMetrics[driverID]
		if !metrics.used {
			continue
		}
		result.latestParticipantCompletion = max(result.latestParticipantCompletion, metrics.latestParticipantCompletion)
		result.maxDriverDetour = max(result.maxDriverDetour, metrics.driverDetour)
		result.aggregateParticipantCompletion += metrics.aggregateParticipantCompletion
		result.aggregateDriveDuration += metrics.driveDuration
		result.corridorSpread += metrics.corridorSpread
		result.usedDrivers++
	}
	if result.usedDrivers == 0 {
		result.maxDriverDetour = 0
	}
	return result
}

func bearingFromInstitute(institute, coordinate models.Coordinates) float64 {
	instituteLatitudeRadians := institute.Lat * math.Pi / 180
	deltaLatitude := coordinate.Lat - institute.Lat
	deltaLongitude := math.Remainder(coordinate.Lng-institute.Lng, 360)
	bearing := math.Atan2(deltaLongitude*math.Cos(instituteLatitudeRadians), deltaLatitude) * 180 / math.Pi
	return math.Mod(bearing+360, 360)
}

func routeCorridorSpread(institute models.Coordinates, stops []*models.Participant) float64 {
	if len(stops) <= 1 {
		return 0
	}

	bearings := make([]float64, len(stops))
	for i, stop := range stops {
		bearings[i] = bearingFromInstitute(institute, stop.GetCoords())
	}
	sort.Float64s(bearings)

	largestGap := bearings[0] + 360 - bearings[len(bearings)-1]
	for i := 1; i < len(bearings); i++ {
		largestGap = max(largestGap, bearings[i]-bearings[i-1])
	}
	return 360 - largestGap
}

func (rc routeContext) optimizeRouteOrders(ctx context.Context, routes map[int64]*balancedRoute, driverIDs []int64) error {
	routeMetrics := make(map[int64]routeObjectiveMetrics, len(driverIDs))
	candidateStops := make(map[int64][]*models.Participant, len(driverIDs))
	for _, driverID := range driverIDs {
		route := routes[driverID]
		stops := coalesceHouseholdStops(route.stops)
		metrics, err := rc.evaluateRouteObjective(ctx, route.driver, stops)
		if err != nil {
			return err
		}
		routeMetrics[driverID] = metrics
		candidateStops[driverID] = stops
	}

	optimizedStops, _, _, err := rc.optimizeStopsForSolution(ctx, routes, routeMetrics, candidateStops, driverIDs)
	if err != nil {
		return err
	}
	for _, driverID := range driverIDs {
		routes[driverID].stops = optimizedStops[driverID]
	}
	return nil
}

// optimizeStopsForSolution reorders only the supplied routes, but evaluates
// every reversal against the complete solution including unchanged peer routes.
func (rc routeContext) optimizeStopsForSolution(
	ctx context.Context,
	routes map[int64]*balancedRoute,
	baseMetrics map[int64]routeObjectiveMetrics,
	changedStops map[int64][]*models.Participant,
	driverIDs []int64,
) (map[int64][]*models.Participant, map[int64]routeObjectiveMetrics, solutionScore, error) {
	currentStops := make(map[int64][]*models.Participant, len(changedStops))
	currentMetrics := make(map[int64]routeObjectiveMetrics, len(baseMetrics))
	maps.Copy(currentMetrics, baseMetrics)
	for driverID, stops := range changedStops {
		stops = coalesceHouseholdStops(stops)
		metrics, err := rc.evaluateRouteObjective(ctx, routes[driverID].driver, stops)
		if err != nil {
			return nil, nil, solutionScore{}, err
		}
		currentStops[driverID] = stops
		currentMetrics[driverID] = metrics
	}

	currentScore := scoreSolution(currentMetrics, driverIDs)
	const maxOrderIterations = 50
	for range maxOrderIterations {
		bestDriverID := int64(0)
		var bestStops []*models.Participant
		var bestMetrics routeObjectiveMetrics
		bestScore := currentScore
		found := false

		for _, driverID := range driverIDs {
			stops, affected := currentStops[driverID]
			if !affected {
				continue
			}
			blocks := routeHouseholdBlocks(stops)
			for i := 0; i < len(blocks)-1; i++ {
				for j := i + 2; j <= len(blocks); j++ {
					candidateBlocks := append([]*participantGroup(nil), blocks...)
					reverseParticipantGroups(candidateBlocks, i, j-1)
					candidateStops := flattenParticipantGroups(candidateBlocks)
					candidateMetrics, err := rc.evaluateRouteObjective(ctx, routes[driverID].driver, candidateStops)
					if err != nil {
						return nil, nil, solutionScore{}, err
					}

					previousMetrics := currentMetrics[driverID]
					currentMetrics[driverID] = candidateMetrics
					candidateScore := scoreSolution(currentMetrics, driverIDs)
					currentMetrics[driverID] = previousMetrics
					if !candidateScore.betterThan(currentScore) || found && !candidateScore.betterThan(bestScore) {
						continue
					}
					bestDriverID = driverID
					bestStops = candidateStops
					bestMetrics = candidateMetrics
					bestScore = candidateScore
					found = true
				}
			}
		}

		if !found {
			return currentStops, currentMetrics, currentScore, nil
		}
		currentStops[bestDriverID] = bestStops
		currentMetrics[bestDriverID] = bestMetrics
		currentScore = bestScore
	}

	return currentStops, currentMetrics, currentScore, nil
}

type assignmentChange struct {
	firstDriverID, secondDriverID int64
	firstStops, secondStops       []*models.Participant
	score                         solutionScore
	found                         bool
}

// optimizeAssignments performs deterministic, bounded local search over whole
// household relocations and pairwise swaps. Every candidate is judged against
// the complete solution so route-local improvements cannot worsen a higher
// priority objective on a peer route.
func optimizeAssignments(ctx context.Context, rc routeContext, routes map[int64]*balancedRoute, driverIDs []int64) (int, error) {
	slices.Sort(driverIDs)
	candidateEvaluations := 0
	routeMetrics := make(map[int64]routeObjectiveMetrics, len(driverIDs))
	for _, driverID := range driverIDs {
		metrics, err := rc.evaluateRouteObjective(ctx, routes[driverID].driver, routes[driverID].stops)
		if err != nil {
			return 0, err
		}
		routeMetrics[driverID] = metrics
	}

	const maxIterations = 50
	for iteration := range maxIterations {
		currentScore := scoreSolution(routeMetrics, driverIDs)
		best := assignmentChange{}
		budgetExhausted := false

		consider := func(firstDriverID, secondDriverID int64, firstStops, secondStops []*models.Participant, countsTowardBudget bool) error {
			if countsTowardBudget {
				if candidateEvaluations >= maxAssignmentCandidateEvaluations {
					budgetExhausted = true
					return nil
				}
				candidateEvaluations++
			}

			optimizedStops, _, candidateScore, err := rc.optimizeStopsForSolution(
				ctx,
				routes,
				routeMetrics,
				map[int64][]*models.Participant{
					firstDriverID:  firstStops,
					secondDriverID: secondStops,
				},
				driverIDs,
			)
			if err != nil {
				return err
			}
			if !candidateScore.betterThan(currentScore) || best.found && !candidateScore.betterThan(best.score) {
				return nil
			}

			best = assignmentChange{
				firstDriverID:  firstDriverID,
				secondDriverID: secondDriverID,
				firstStops:     optimizedStops[firstDriverID],
				secondStops:    optimizedStops[secondDriverID],
				score:          candidateScore,
				found:          true,
			}
			return nil
		}

		// These relocations necessarily increase usedDrivers. Evaluate them before
		// the bounded lower-tier neighborhood so the candidate budget can never
		// hide the objective's highest-priority improvement.
		for _, sourceDriverID := range driverIDs {
			sourceRoute := routes[sourceDriverID]
			sourceBlocks := routeHouseholdBlocks(sourceRoute.stops)
			if len(sourceBlocks) < 2 {
				continue
			}
			sourcePosition := 0
			for _, sourceGroup := range sourceBlocks {
				groupSize := len(sourceGroup.members)
				for _, destinationDriverID := range driverIDs {
					destinationRoute := routes[destinationDriverID]
					if len(destinationRoute.stops) != 0 || groupSize > destinationRoute.driver.VehicleCapacity {
						continue
					}
					newSourceStops := removeRange(sourceRoute.stops, sourcePosition, sourcePosition+groupSize)
					newDestinationStops := slices.Clone(sourceGroup.members)
					if err := consider(sourceDriverID, destinationDriverID, newSourceStops, newDestinationStops, false); err != nil {
						return iteration, err
					}
				}
				sourcePosition += groupSize
			}
		}

		if !best.found {
		relocationSearch:
			for _, sourceDriverID := range driverIDs {
				sourceRoute := routes[sourceDriverID]
				sourceBlocks := routeHouseholdBlocks(sourceRoute.stops)
				sourcePosition := 0
				for _, sourceGroup := range sourceBlocks {
					groupSize := len(sourceGroup.members)
					for _, destinationDriverID := range driverIDs {
						if destinationDriverID == sourceDriverID {
							continue
						}
						destinationRoute := routes[destinationDriverID]
						if len(destinationRoute.stops)+groupSize > destinationRoute.driver.VehicleCapacity {
							continue
						}

						for _, destinationPosition := range householdBoundaryPositions(destinationRoute.stops) {
							newSourceStops := removeRange(sourceRoute.stops, sourcePosition, sourcePosition+groupSize)
							newDestinationStops := insertGroupAt(destinationRoute.stops, sourceGroup, destinationPosition)
							if err := consider(sourceDriverID, destinationDriverID, newSourceStops, newDestinationStops, true); err != nil {
								return iteration, err
							}
							if budgetExhausted {
								break relocationSearch
							}
						}
					}
					sourcePosition += groupSize
				}
			}

		swapSearch:
			for firstIndex, firstDriverID := range driverIDs {
				if budgetExhausted {
					break
				}
				firstRoute := routes[firstDriverID]
				firstPosition := 0
				for _, firstGroup := range routeHouseholdBlocks(firstRoute.stops) {
					firstSize := len(firstGroup.members)
					for _, secondDriverID := range driverIDs[firstIndex+1:] {
						secondRoute := routes[secondDriverID]
						secondPosition := 0
						for _, secondGroup := range routeHouseholdBlocks(secondRoute.stops) {
							secondSize := len(secondGroup.members)
							if len(firstRoute.stops)-firstSize+secondSize <= firstRoute.driver.VehicleCapacity &&
								len(secondRoute.stops)-secondSize+firstSize <= secondRoute.driver.VehicleCapacity {
								newFirstStops := replaceRangeWithGroup(firstRoute.stops, firstPosition, firstPosition+firstSize, secondGroup)
								newSecondStops := replaceRangeWithGroup(secondRoute.stops, secondPosition, secondPosition+secondSize, firstGroup)
								if err := consider(firstDriverID, secondDriverID, newFirstStops, newSecondStops, true); err != nil {
									return iteration, err
								}
								if budgetExhausted {
									break swapSearch
								}
							}
							secondPosition += secondSize
						}
					}
					firstPosition += firstSize
				}
			}
		}

		if !best.found {
			return iteration, nil
		}
		firstRoute := routes[best.firstDriverID]
		secondRoute := routes[best.secondDriverID]
		firstRoute.stops = best.firstStops
		secondRoute.stops = best.secondStops

		// The accepted assignment can expose an ordering improvement on an
		// untouched peer route by changing which route sets the global maximum.
		// Re-establish a full-solution ordering fixed point before evaluating the
		// next assignment neighborhood.
		if err := rc.optimizeRouteOrders(ctx, routes, driverIDs); err != nil {
			return iteration, err
		}
		for _, driverID := range driverIDs {
			metrics, err := rc.evaluateRouteObjective(ctx, routes[driverID].driver, routes[driverID].stops)
			if err != nil {
				return iteration, err
			}
			routeMetrics[driverID] = metrics
		}
		if budgetExhausted {
			return iteration + 1, nil
		}
	}

	return maxIterations, nil
}

func replaceRangeWithGroup(stops []*models.Participant, start, end int, group *participantGroup) []*models.Participant {
	return insertGroupAt(removeRange(stops, start, end), group, start)
}

// buildResult creates the final routing result
func buildResult(ctx context.Context, rc routeContext, routes map[int64]*balancedRoute, totalParticipants int) (*models.RoutingResult, error) {
	calculatedRoutes := make([]models.CalculatedRoute, 0)
	totalDropoff := 0.0
	totalDist := 0.0
	maxDetour := 0.0
	sumDetour := 0.0
	driversUsed := 0

	driverIDs := make([]int64, 0, len(routes))
	for id := range routes {
		driverIDs = append(driverIDs, id)
	}
	slices.Sort(driverIDs)

	for _, driverID := range driverIDs {
		route := routes[driverID]
		// Defensive: keep same-household riders adjacent in the final payload even
		// if a future optimizer path forgets to normalize before build.
		route.stops = coalesceHouseholdStops(route.stops)
		if len(route.stops) == 0 {
			continue
		}

		driversUsed++

		routeStops := make([]models.RouteStop, len(route.stops))
		metrics, err := rc.evaluateParticipants(ctx, route.driver, route.stops)
		if err != nil {
			return nil, err
		}
		for i, p := range route.stops {
			routeStops[i].Participant = p
		}

		totalDropoff += metrics.TotalStopDistanceMeters
		totalDist += metrics.TotalDistanceMeters
		maxDetour = max(maxDetour, metrics.DetourSecs)
		sumDetour += metrics.DetourSecs

		calculatedRoute := models.CalculatedRoute{
			Driver: route.driver,
			Stops:  routeStops,
		}
		rc.applyMetrics(&calculatedRoute, metrics)
		calculatedRoutes = append(calculatedRoutes, calculatedRoute)
	}
	averageDetour := 0.0
	if driversUsed > 0 {
		averageDetour = sumDetour / float64(driversUsed)
	}

	return &models.RoutingResult{
		Routes: calculatedRoutes,
		Summary: models.RoutingSummary{
			TotalParticipants:          totalParticipants,
			TotalDriversUsed:           driversUsed,
			TotalDropoffDistanceMeters: totalDropoff,
			TotalDistanceMeters:        totalDist,
			MaxDetourSecs:              maxDetour,
			SumDetourSecs:              sumDetour,
			AverageDetourSecs:          averageDetour,
			UnassignedParticipants:     []int64{},
		},
		Mode: rc.mode,
	}, nil
}

// participantGroup represents participants from the same household
type participantGroup struct {
	members []*models.Participant
	address string
	lat     float64
	lng     float64
}

// groupParticipantsByAddress groups participants by normalized address, falling
// back to rounded coordinates when an address is unavailable.
func groupParticipantsByAddress(participants []*models.Participant) []*participantGroup {
	householdMap := make(map[string]*participantGroup)

	for _, p := range participants {
		key := householdKey(p)

		if group, exists := householdMap[key]; exists {
			// Add to existing group
			group.members = append(group.members, p)
		} else {
			// Create new group
			householdMap[key] = newParticipantGroup(p)
		}
	}

	// Convert map to slice
	groups := make([]*participantGroup, 0, len(householdMap))
	for _, group := range householdMap {
		groups = append(groups, group)
	}

	// Sort groups by size (larger groups first) for better initial assignment
	sort.Slice(groups, func(i, j int) bool {
		if len(groups[i].members) != len(groups[j].members) {
			return len(groups[i].members) > len(groups[j].members)
		}
		return participantGroupKey(groups[i]) < participantGroupKey(groups[j])
	})

	return groups
}

// coordinateKey creates a unique key for a coordinate pair
func coordinateKey(lat, lng float64) string {
	return fmt.Sprintf("%.5f,%.5f", lat, lng)
}

func normalizeAddress(address string) string {
	return strings.ToLower(strings.Join(strings.Fields(address), " "))
}

func householdKey(participant *models.Participant) string {
	if participant == nil {
		return ""
	}
	if address := normalizeAddress(participant.Address); address != "" {
		return "addr:" + address
	}
	return coordinateKey(models.RoundCoordinate(participant.Lat), models.RoundCoordinate(participant.Lng))
}

func participantGroupKey(group *participantGroup) string {
	if group == nil {
		return ""
	}
	if len(group.members) > 0 {
		return householdKey(group.members[0])
	}
	if address := normalizeAddress(group.address); address != "" {
		return "addr:" + address
	}
	return coordinateKey(group.lat, group.lng)
}

func newParticipantGroup(participant *models.Participant) *participantGroup {
	return &participantGroup{
		members: []*models.Participant{participant},
		address: participant.Address,
		lat:     models.RoundCoordinate(participant.Lat),
		lng:     models.RoundCoordinate(participant.Lng),
	}
}

func routeHouseholdBlocks(stops []*models.Participant) []*participantGroup {
	if len(stops) == 0 {
		return nil
	}

	blocks := make([]*participantGroup, 0, len(stops))
	for _, stop := range stops {
		if len(blocks) == 0 || householdKey(blocks[len(blocks)-1].members[0]) != householdKey(stop) {
			blocks = append(blocks, newParticipantGroup(stop))
			continue
		}
		blocks[len(blocks)-1].members = append(blocks[len(blocks)-1].members, stop)
	}

	return blocks
}

func householdBoundaryPositions(stops []*models.Participant) []int {
	if len(stops) == 0 {
		return []int{0}
	}

	positions := make([]int, 0, len(stops)+1)
	positions = append(positions, 0)
	pos := 0
	for _, block := range routeHouseholdBlocks(stops) {
		pos += len(block.members)
		positions = append(positions, pos)
	}

	return positions
}

func coalesceHouseholdStops(stops []*models.Participant) []*models.Participant {
	if len(stops) < 2 {
		return stops
	}

	orderedKeys := make([]string, 0, len(stops))
	grouped := make(map[string]*participantGroup, len(stops))
	for _, stop := range stops {
		key := householdKey(stop)
		if group, exists := grouped[key]; exists {
			group.members = append(group.members, stop)
			continue
		}

		orderedKeys = append(orderedKeys, key)
		grouped[key] = newParticipantGroup(stop)
	}

	result := make([]*models.Participant, 0, len(stops))
	for _, key := range orderedKeys {
		result = append(result, grouped[key].members...)
	}

	if slices.Equal(result, stops) {
		return stops
	}

	return result
}

func maxRouteVehicleCapacity(routes map[int64]*balancedRoute) int {
	maxCapacity := 0
	for _, route := range routes {
		if route.driver.VehicleCapacity > maxCapacity {
			maxCapacity = route.driver.VehicleCapacity
		}
	}
	return maxCapacity
}

func assignmentPreservesCapacityFeasibility(routes map[int64]*balancedRoute, currentDriverID int64, groups []*participantGroup, assignedGroupIndex, assignedCount int, splittableHouseholds map[string]struct{}) bool {
	capacities := make([]int, 0, len(routes))
	totalCapacity := 0
	for driverID, route := range routes {
		capacity := route.driver.VehicleCapacity - len(route.stops)
		if driverID == currentDriverID {
			capacity -= assignedCount
		}
		if capacity < 0 {
			return false
		}
		capacities = append(capacities, capacity)
		totalCapacity += capacity
	}

	remainingParticipants := 0
	atomicSizes := make([]int, 0, len(groups))
	for groupIdx, group := range groups {
		size := len(group.members)
		if groupIdx == assignedGroupIndex {
			size -= assignedCount
		}
		if size <= 0 {
			continue
		}

		remainingParticipants += size
		if _, splittable := splittableHouseholds[participantGroupKey(group)]; !splittable {
			atomicSizes = append(atomicSizes, size)
		}
	}
	if remainingParticipants > totalCapacity {
		return false
	}

	return canPackAtomicGroupSizes(atomicSizes, capacities)
}

func canPackAtomicGroupSizes(groupSizes []int, capacities []int) bool {
	if len(groupSizes) == 0 {
		return true
	}

	groupSizes = slices.Clone(groupSizes)
	capacities = slices.Clone(capacities)
	sort.Sort(sort.Reverse(sort.IntSlice(groupSizes)))
	sort.Sort(sort.Reverse(sort.IntSlice(capacities)))

	var pack func(int) bool
	pack = func(groupIndex int) bool {
		if groupIndex == len(groupSizes) {
			return true
		}

		size := groupSizes[groupIndex]
		lastTriedCapacity := -1
		for i, capacity := range capacities {
			if capacity < size || capacity == lastTriedCapacity {
				continue
			}

			capacities[i] -= size
			if pack(groupIndex + 1) {
				return true
			}
			capacities[i] += size
			lastTriedCapacity = capacity
		}

		return false
	}

	return pack(0)
}

// insertGroupAt inserts all members of a group consecutively at the specified position
func insertGroupAt(stops []*models.Participant, group *participantGroup, pos int) []*models.Participant {
	// Create new slice with room for the group
	newStops := make([]*models.Participant, len(stops)+len(group.members))

	// Copy elements before insertion point
	copy(newStops, stops[:pos])

	// Insert group members
	copy(newStops[pos:], group.members)

	// Copy elements after insertion point
	copy(newStops[pos+len(group.members):], stops[pos:])

	return newStops
}
