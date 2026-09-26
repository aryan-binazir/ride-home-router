package routing

import (
	"context"
	"fmt"
	"log"
	"maps"
	"math"
	"ride-home-router/internal/distance"
	"ride-home-router/internal/models"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// BalancedRouter prioritizes driver use, corridor spread, then time tiers.
type BalancedRouter struct {
	distanceCalc distance.SolveSource
}

const (
	scoreImprovementEpsilon           = 0.001
	maxAssignmentCandidateEvaluations = 10000
	maxHouseholdPackingSearchNodes    = 10000
	maxNonemptyRouteSearchNodes       = 10000
)

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

	if len(req.Participants) == 0 {
		return &models.RoutingResult{
			Routes:  []models.CalculatedRoute{},
			Summary: models.RoutingSummary{},
			Mode:    mode,
		}, nil
	}

	if len(req.Drivers) == 0 {
		return nil, &ErrRoutingFailed{
			Reason:            "No drivers available",
			UnassignedCount:   len(req.Participants),
			TotalCapacity:     0,
			TotalParticipants: len(req.Participants),
		}
	}

	totalCapacity := 0
	for _, d := range req.Drivers {
		totalCapacity += d.VehicleCapacity
	}
	if totalCapacity < len(req.Participants) {
		return nil, &ErrRoutingFailed{
			Reason:            "Cannot assign all participants",
			UnassignedCount:   len(req.Participants) - totalCapacity,
			TotalCapacity:     totalCapacity,
			TotalParticipants: len(req.Participants),
		}
	}

	prewarmStart := time.Now()
	distanceLookup, err := prepareSolveDistances(ctx, r.distanceCalc, req)
	if err != nil {
		return nil, err
	}
	log.Printf("[TIMING] Prewarm cache: %v", time.Since(prewarmStart))
	rc := newRouteContext(distanceLookup, req.InstituteCoords, mode)

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

	unassigned := make([]*models.Participant, len(req.Participants))
	for i := range req.Participants {
		unassigned[i] = &req.Participants[i]
	}

	rc.prepareParticipants(unassigned)

	phase1Start := time.Now()
	seedName := "bearing-sweep"
	seeded, err := r.bearingSweepInsertion(ctx, req.InstituteCoords, routes, driverIDs, unassigned)
	if err != nil {
		return nil, err
	}
	if seeded {
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

	phase2Start := time.Now()
	if err := rc.optimizeRouteOrders(ctx, routes, driverIDs); err != nil {
		return nil, err
	}
	log.Printf("[TIMING] Phase 2 (route ordering): %v", time.Since(phase2Start))

	phase3Start := time.Now()
	iterations, err := optimizeAssignments(ctx, rc, routes, driverIDs)
	if err != nil {
		return nil, err
	}
	log.Printf("[TIMING] Phase 3 (assignment search): %v (iterations=%d)", time.Since(phase3Start), iterations)

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

	phase4Start := time.Now()
	swaps, err := optimizeDriverAssignments(ctx, rc, routes, driverIDs)
	if err != nil {
		return nil, err
	}
	log.Printf("[TIMING] Phase 4 (driver swaps): %v (swaps=%d)", time.Since(phase4Start), swaps)

	result, err := buildResult(ctx, rc, routes, len(req.Participants))
	if err != nil {
		return nil, err
	}

	log.Printf("[BALANCED] Complete: drivers_used=%d total_distance=%.0fm",
		result.Summary.TotalDriversUsed, result.Summary.TotalDropoffDistanceMeters)
	log.Printf("[TIMING] TOTAL: %v", time.Since(totalStart))

	return result, nil
}

type balancedRoute struct {
	driver *models.Driver
	stops  []*models.Participant
}

func (r *BalancedRouter) bearingSweepInsertion(ctx context.Context, institute models.Coordinates, routes map[int64]*balancedRoute, driverIDs []int64, unassigned []*models.Participant) (bool, error) {
	if len(unassigned) == 0 {
		return true, nil
	}
	if len(driverIDs) == 0 {
		return false, nil
	}

	groups := bearingSweepGroups(institute, unassigned)
	workingRoutes := cloneBalancedRoutes(routes, driverIDs)
	if len(workingRoutes) != len(driverIDs) {
		return false, nil
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
			repaired, err := repairSweep(ctx, institute, workingRoutes, orderedDriverIDs, groups, splittableHouseholds, reserveEveryDriver)
			if err != nil || !repaired {
				log.Printf("[BALANCED] Phase 1 sweep repair failed with %d groups left", len(groups))
				return false, err
			}
			log.Printf("[BALANCED] Phase 1 sweep repair placed %d groups into spare seats", len(groups))
			for _, id := range orderedDriverIDs {
				if len(workingRoutes[id].stops) > 0 {
					driversWithGroups[id] = struct{}{}
				}
			}
			groups = nil
			break
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
			feasible, err := assignmentPreservesCapacityFeasibility(ctx, workingRoutes, driverID, groups, 0, assignedCount, splittableHouseholds)
			if err != nil {
				return false, err
			}
			if !feasible {
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
		return false, nil
	}
	for _, driverID := range orderedDriverIDs {
		routes[driverID].stops = workingRoutes[driverID].stops
	}
	return true, nil
}

func repairSweep(ctx context.Context, institute models.Coordinates, routes map[int64]*balancedRoute, driverIDs []int64, groups []*participantGroup, splittableHouseholds map[string]struct{}, reserveEveryDriver bool) (bool, error) {
	remaining := slices.Clone(groups)
	for _, driverID := range driverIDs {
		route := routes[driverID]
		if len(route.stops) > 0 || !reserveEveryDriver {
			continue
		}
		if err := ctx.Err(); err != nil {
			return false, err
		}
		driverBearing := bearingFromInstitute(institute, route.driver.GetCoords())
		order := make([]int, 0, len(remaining))
		for i, group := range remaining {
			if len(group.members) <= route.driver.VehicleCapacity {
				order = append(order, i)
			}
		}
		sort.Slice(order, func(a, b int) bool {
			da, db := angularDistance(remaining[order[a]].bearing, driverBearing), angularDistance(remaining[order[b]].bearing, driverBearing)
			if da != db {
				return da < db
			}
			return participantGroupKey(remaining[order[a]]) < participantGroupKey(remaining[order[b]])
		})
		placed := false
		for _, i := range order {
			feasible, err := assignmentPreservesCapacityFeasibility(ctx, routes, driverID, remaining, i, len(remaining[i].members), splittableHouseholds)
			if err != nil {
				return false, err
			}
			if !feasible {
				continue
			}
			route.stops = append(route.stops, remaining[i].members...)
			remaining = slices.Delete(remaining, i, i+1)
			placed = true
			break
		}
		if !placed {
			log.Printf("[BALANCED] Phase 1 sweep repair: no remaining group fits empty driver %d", driverID)
			return false, nil
		}
	}
	sort.SliceStable(remaining, func(i, j int) bool {
		if len(remaining[i].members) != len(remaining[j].members) {
			return len(remaining[i].members) > len(remaining[j].members)
		}
		if remaining[i].bearing != remaining[j].bearing {
			return remaining[i].bearing < remaining[j].bearing
		}
		return participantGroupKey(remaining[i]) < participantGroupKey(remaining[j])
	})
	for len(remaining) > 0 {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		group := remaining[0]
		assignedCount := len(group.members)
		if _, splittable := splittableHouseholds[participantGroupKey(group)]; splittable {
			assignedCount = 1
		}
		type candidate struct {
			driverID int64
			distance float64
		}
		var candidates []candidate
		for _, driverID := range driverIDs {
			route := routes[driverID]
			if route.driver.VehicleCapacity-len(route.stops) < assignedCount {
				continue
			}
			candidates = append(candidates, candidate{driverID, angularDistance(group.bearing, bearingFromInstitute(institute, route.driver.GetCoords()))})
		}
		sort.Slice(candidates, func(i, j int) bool {
			if candidates[i].distance != candidates[j].distance {
				return candidates[i].distance < candidates[j].distance
			}
			return candidates[i].driverID < candidates[j].driverID
		})
		placed := false
		for _, c := range candidates {
			feasible, err := assignmentPreservesCapacityFeasibility(ctx, routes, c.driverID, remaining, 0, assignedCount, splittableHouseholds)
			if err != nil {
				return false, err
			}
			if !feasible {
				continue
			}
			routes[c.driverID].stops = append(routes[c.driverID].stops, group.members[:assignedCount]...)
			placed = true
			break
		}
		if !placed {
			log.Printf("[BALANCED] Phase 1 sweep repair: no car with %d seats can take group %s (%d candidates by seats)", assignedCount, participantGroupKey(group), len(candidates))
			return false, nil
		}
		if assignedCount == len(group.members) {
			remaining = remaining[1:]
		} else {
			group.members = group.members[assignedCount:]
		}
	}
	return true, nil
}

func bearingSweepGroups(institute models.Coordinates, participants []*models.Participant) []*participantGroup {
	groups := groupParticipantsByAddress(participants)
	for _, group := range groups {
		group.bearing = bearingFromInstitute(institute, models.Coordinates{Lat: group.lat, Lng: group.lng})
	}
	sort.Slice(groups, func(i, j int) bool {
		iBearing := groups[i].bearing
		jBearing := groups[j].bearing
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
		currentBearing := group.bearing
		nextGroup := groups[nextIndex]
		nextBearing := nextGroup.bearing
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

func (r *BalancedRouter) maximizeNonemptyRoutes(ctx context.Context, rc routeContext, routes map[int64]*balancedRoute, driverIDs []int64) (int, error) {
	orderedDriverIDs := slices.Clone(driverIDs)
	slices.Sort(orderedDriverIDs)
	repairs := 0
	searchNodes := 0
	budgetExhausted := false

	hasUnvisitedMultiBlockRoute := func(stops map[int64][]*models.Participant, visited map[int64]struct{}) bool {
		for _, driverID := range orderedDriverIDs {
			if _, alreadyVisited := visited[driverID]; alreadyVisited {
				continue
			}
			if len(rc.routeHouseholdBlocks(stops[driverID])) >= 2 {
				return true
			}
		}
		return false
	}

	for {
		baseStops := make(map[int64][]*models.Participant, len(orderedDriverIDs))
		for _, driverID := range orderedDriverIDs {
			baseStops[driverID] = slices.Clone(routes[driverID].stops)
		}
		if !hasUnvisitedMultiBlockRoute(baseStops, nil) {
			return repairs, nil
		}

		baseMetrics := make(map[int64]routeObjectiveMetrics, len(orderedDriverIDs))
		for _, driverID := range orderedDriverIDs {
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
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}

			if searchNodes >= maxNonemptyRouteSearchNodes {
				budgetExhausted = true
				return nil
			}
			searchNodes++

			for _, sourceDriverID := range orderedDriverIDs {
				if budgetExhausted {
					return nil
				}
				if sourceDriverID == emptyDriverID {
					continue
				}
				if _, alreadyVisited := visited[sourceDriverID]; alreadyVisited {
					continue
				}

				sourceStops := workingStops[sourceDriverID]
				for _, sourceBlock := range rc.routeHouseholdBlocks(sourceStops) {
					if sourceBlock.end-sourceBlock.start > routes[emptyDriverID].driver.VehicleCapacity {
						continue
					}

					candidateStops := maps.Clone(workingStops)
					candidateStops[sourceDriverID] = removeRange(sourceStops, sourceBlock.start, sourceBlock.end)
					candidateStops[emptyDriverID] = slices.Clone(sourceStops[sourceBlock.start:sourceBlock.end])
					if len(candidateStops[sourceDriverID]) == 0 {
						candidateVisited := maps.Clone(visited)
						candidateVisited[emptyDriverID] = struct{}{}
						if !hasUnvisitedMultiBlockRoute(candidateStops, candidateVisited) {
							continue
						}
						if err := search(sourceDriverID, candidateStops, candidateVisited); err != nil {
							return err
						}
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
				}
			}
			return nil
		}

		for _, emptyDriverID := range orderedDriverIDs {
			if len(baseStops[emptyDriverID]) != 0 {
				continue
			}
			if !hasUnvisitedMultiBlockRoute(baseStops, nil) {
				return repairs, nil
			}
			if err := search(emptyDriverID, baseStops, map[int64]struct{}{}); err != nil {
				return repairs, err
			}
			if budgetExhausted {
				break
			}
		}
		if !best.found {
			return repairs, nil
		}
		for _, driverID := range orderedDriverIDs {
			routes[driverID].stops = best.stops[driverID]
		}
		repairs++
		if budgetExhausted {
			return repairs, nil
		}
	}
}

func roundRobinInsertion(ctx context.Context, rc routeContext, routes map[int64]*balancedRoute, driverIDs []int64, unassigned []*models.Participant) ([]*models.Participant, error) {
	slices.Sort(driverIDs)

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

	driverIndex := 0
	insertionRoundLimit := totalParticipants * len(driverIDs) * 2

	for len(groups) > 0 && insertionRoundLimit > 0 {
		insertionRoundLimit--

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
				break
			}
		}

		if !foundDriver {
			break
		}

		currentDriverID := driverIDs[driverIndex]
		route := routes[currentDriverID]
		remainingCapacity := route.driver.VehicleCapacity - len(route.stops)
		routeScore, err := rc.riderScore(ctx, route.driver, route.stops)
		if err != nil {
			return nil, err
		}

		bestCost := math.Inf(1)
		var bestGroup *participantGroup
		var bestGroupIndex int
		var bestPosition int

		for groupIdx, group := range groups {
			groupSize := len(group.members)

			if groupSize > remainingCapacity {
				continue
			}
			feasible, err := assignmentPreservesCapacityFeasibility(ctx, routes, currentDriverID, groups, groupIdx, groupSize, splittableHouseholds)
			if err != nil {
				return nil, err
			}
			if !feasible {
				continue
			}

			for _, pos := range rc.householdBoundaryPositions(route.stops) {
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

		if bestGroup == nil {
			for groupIdx, group := range groups {
				if len(group.members) == 0 {
					continue
				}
				if _, ok := splittableHouseholds[participantGroupKey(group)]; !ok {
					continue
				}
				feasible, err := assignmentPreservesCapacityFeasibility(ctx, routes, currentDriverID, groups, groupIdx, 1, splittableHouseholds)
				if err != nil {
					return nil, err
				}
				if !feasible {
					continue
				}

				for _, pos := range rc.householdBoundaryPositions(route.stops) {
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

		route.stops = insertParticipantsAt(route.stops, bestGroup.members, bestPosition)

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

		originalGroup := groups[bestGroupIndex]
		if len(bestGroup.members) == len(originalGroup.members) {
			groups = append(groups[:bestGroupIndex], groups[bestGroupIndex+1:]...)
		} else {
			assignedID := bestGroup.members[0].ID
			newMembers := make([]*models.Participant, 0, len(originalGroup.members)-1)
			for _, m := range originalGroup.members {
				if m.ID != assignedID {
					newMembers = append(newMembers, m)
				}
			}
			originalGroup.members = newMembers
		}

		driverIndex = (driverIndex + 1) % len(driverIDs)
	}

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

func (score solutionScore) comparePrefix(other solutionScore) (better, decided bool) {
	if score.usedDrivers != other.usedDrivers {
		return score.usedDrivers > other.usedDrivers, true
	}
	if score.corridorSpread != other.corridorSpread {
		return score.corridorSpread < other.corridorSpread, true
	}
	for _, values := range [][2]float64{{score.latestParticipantCompletion, other.latestParticipantCompletion}, {score.maxDriverDetour, other.maxDriverDetour}} {
		if values[0] < values[1]-scoreImprovementEpsilon {
			return true, true
		}
		if values[0] > values[1]+scoreImprovementEpsilon {
			return false, true
		}
	}
	return false, false
}

func (score solutionScore) betterThan(other solutionScore) bool {
	if better, decided := score.comparePrefix(other); decided {
		return better
	}
	for _, values := range [][2]float64{{score.aggregateParticipantCompletion, other.aggregateParticipantCompletion}, {score.aggregateDriveDuration, other.aggregateDriveDuration}} {
		if values[0] < values[1]-scoreImprovementEpsilon {
			return true
		}
		if values[0] > values[1]+scoreImprovementEpsilon {
			return false
		}
	}
	return false
}

func (rc routeContext) evaluateRouteObjective(ctx context.Context, driver *models.Driver, stops []*models.Participant, knownSpread ...int) (routeObjectiveMetrics, error) {
	if len(stops) == 0 {
		return routeObjectiveMetrics{}, nil
	}
	if driver == nil {
		return routeObjectiveMetrics{}, fmt.Errorf("route driver is required")
	}
	result := routeObjectiveMetrics{used: true}
	prev := rc.origin(driver)
	cumulative := 0.0
	sumOfPickupTimes := 0.0
	firstRiderBoardedAt := -1.0
	for i, stop := range stops {
		if stop == nil {
			return routeObjectiveMetrics{}, fmt.Errorf("route stop %d is missing participant data", i)
		}
		leg, err := rc.distanceValue(ctx, prev, stop.GetCoords())
		if err != nil {
			return routeObjectiveMetrics{}, err
		}
		cumulative += leg.DurationSecs
		if rc.mode != RouteModePickup {
			result.latestParticipantCompletion = max(result.latestParticipantCompletion, cumulative)
			result.aggregateParticipantCompletion += cumulative
		}
		if rc.mode == RouteModePickup {
			sumOfPickupTimes += cumulative
			if firstRiderBoardedAt < 0 {
				firstRiderBoardedAt = cumulative
			}
		}
		prev = stop.GetCoords()
	}
	finalLeg, err := rc.distanceValue(ctx, prev, rc.destination(driver))
	if err != nil {
		return routeObjectiveMetrics{}, err
	}
	baseline, err := rc.distanceValue(ctx, rc.origin(driver), rc.destination(driver))
	if err != nil {
		return routeObjectiveMetrics{}, err
	}
	result.driveDuration = cumulative + finalLeg.DurationSecs
	result.driverDetour = result.driveDuration - baseline.DurationSecs
	if len(knownSpread) != 0 {
		result.corridorSpread = knownSpread[0]
	} else {
		result.corridorSpread = int(math.Round(rc.routeCorridorSpread(stops) / 10.0))
	}
	if rc.mode == RouteModePickup {
		result.latestParticipantCompletion, result.aggregateParticipantCompletion = pickupRiderTimeAboard(result.driveDuration, firstRiderBoardedAt, sumOfPickupTimes, len(stops))
	}
	return result, nil
}

func pickupRiderTimeAboard(driveDuration, firstRiderBoardedAt, sumOfPickupTimes float64, riders int) (longest, total float64) {
	return driveDuration - firstRiderBoardedAt, driveDuration*float64(riders) - sumOfPickupTimes
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

func scoreOrderedSolution(routeMetrics []routeObjectiveMetrics) solutionScore {
	result := solutionScore{maxDriverDetour: math.Inf(-1)}
	for _, metrics := range routeMetrics {
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

func (rc routeContext) routeCorridorSpread(stops []*models.Participant) float64 {
	if len(stops) <= 1 {
		return 0
	}

	bearings := make([]float64, len(stops))
	for i, stop := range stops {
		if facts, ok := rc.participants[stop]; ok {
			bearings[i] = facts.bearing
		} else {
			bearings[i] = bearingFromInstitute(rc.instituteCoords, stop.GetCoords())
		}
	}
	sort.Float64s(bearings)

	largestGap := bearings[0] + 360 - bearings[len(bearings)-1]
	for i := 1; i < len(bearings); i++ {
		largestGap = max(largestGap, bearings[i]-bearings[i-1])
	}
	return 360 - largestGap
}

func (rc routeContext) optimizeRouteOrders(ctx context.Context, routes map[int64]*balancedRoute, driverIDs []int64) error {
	_, err := rc.optimizeRouteOrdersWith(ctx, routes, driverIDs, true)
	return err
}

func (rc routeContext) optimizeRouteOrdersWith(ctx context.Context, routes map[int64]*balancedRoute, driverIDs []int64, relocations bool) (solutionScore, error) {
	routeMetrics := make(map[int64]routeObjectiveMetrics, len(driverIDs))
	candidateStops := make(map[int64][]*models.Participant, len(driverIDs))
	for _, driverID := range driverIDs {
		route := routes[driverID]
		stops := rc.coalesceHouseholdStops(route.stops)
		metrics, err := rc.evaluateRouteObjective(ctx, route.driver, stops)
		if err != nil {
			return solutionScore{}, err
		}
		routeMetrics[driverID] = metrics
		candidateStops[driverID] = stops
	}

	optimizedStops, score, err := rc.optimizeStopsForSolution(ctx, routes, routeMetrics, candidateStops, driverIDs, relocations)
	if err != nil {
		return solutionScore{}, err
	}
	for _, driverID := range driverIDs {
		routes[driverID].stops = optimizedStops[driverID]
	}
	return score, nil
}

const maxRelocationEvaluations = 10000

func (rc routeContext) optimizeStopsForSolution(
	ctx context.Context,
	routes map[int64]*balancedRoute,
	baseMetrics map[int64]routeObjectiveMetrics,
	changedStops map[int64][]*models.Participant,
	driverIDs []int64,
	relocations bool,
) (map[int64][]*models.Participant, solutionScore, error) {
	relocationEvaluations := 0
	currentStops := make(map[int64][]*models.Participant, len(changedStops))
	currentMetrics := make([]routeObjectiveMetrics, len(driverIDs))
	for i, id := range driverIDs {
		currentMetrics[i] = baseMetrics[id]
	}
	for driverID, stops := range changedStops {
		select {
		case <-ctx.Done():
			return nil, solutionScore{}, ctx.Err()
		default:
		}

		stops = rc.coalesceHouseholdStops(stops)
		metrics, err := rc.evaluateRouteObjective(ctx, routes[driverID].driver, stops)
		if err != nil {
			return nil, solutionScore{}, err
		}
		currentStops[driverID] = stops
		currentMetrics[slices.Index(driverIDs, driverID)] = metrics
	}

	currentScore := scoreOrderedSolution(currentMetrics)
	const maxOrderIterations = 50
	for range maxOrderIterations {
		select {
		case <-ctx.Done():
			return nil, solutionScore{}, ctx.Err()
		default:
		}

		bestDriverID := int64(0)
		bestDriverIndex := 0
		var bestStops []*models.Participant
		var bestMetrics routeObjectiveMetrics
		bestScore := currentScore
		found := false

		for driverIndex, driverID := range driverIDs {
			stops, affected := currentStops[driverID]
			if !affected {
				continue
			}
			blocks := rc.routeHouseholdBlocks(stops)
			candidateBlocks := make([]householdBlock, len(blocks))
			candidateStops := make([]*models.Participant, 0, len(stops))
			otherLatest, otherDetour := 0.0, math.Inf(-1)
			prefixCompletion, prefixDuration := 0.0, 0.0
			for i, metrics := range currentMetrics {
				if i < driverIndex && metrics.used {
					prefixCompletion += metrics.aggregateParticipantCompletion
					prefixDuration += metrics.driveDuration
				}
				if i != driverIndex && metrics.used {
					otherLatest = max(otherLatest, metrics.latestParticipantCompletion)
					otherDetour = max(otherDetour, metrics.driverDetour)
				}
			}
			consider := func() error {
				select {
				case <-ctx.Done():
					return ctx.Err()
				default:
				}
				candidateStops = candidateStops[:0]
				for _, block := range candidateBlocks {
					candidateStops = append(candidateStops, stops[block.start:block.end]...)
				}
				candidateMetrics, err := rc.evaluateRouteObjective(ctx, routes[driverID].driver, candidateStops, currentMetrics[driverIndex].corridorSpread)
				if err != nil {
					return err
				}

				prefix := currentScore
				prefix.latestParticipantCompletion = max(otherLatest, candidateMetrics.latestParticipantCompletion)
				prefix.maxDriverDetour = max(otherDetour, candidateMetrics.driverDetour)
				if better, decided := prefix.comparePrefix(currentScore); decided && !better {
					return nil
				}
				if found {
					if better, decided := prefix.comparePrefix(bestScore); decided && !better {
						return nil
					}
				}
				previousMetrics := currentMetrics[driverIndex]
				currentMetrics[driverIndex] = candidateMetrics
				candidateScore := prefix
				candidateScore.aggregateParticipantCompletion = prefixCompletion
				candidateScore.aggregateDriveDuration = prefixDuration
				for _, metrics := range currentMetrics[driverIndex:] {
					if metrics.used {
						candidateScore.aggregateParticipantCompletion += metrics.aggregateParticipantCompletion
						candidateScore.aggregateDriveDuration += metrics.driveDuration
					}
				}
				currentMetrics[driverIndex] = previousMetrics
				if !candidateScore.betterThan(currentScore) || found && !candidateScore.betterThan(bestScore) {
					return nil
				}
				bestDriverID = driverID
				bestDriverIndex = driverIndex
				bestStops = slices.Clone(candidateStops)
				bestMetrics = candidateMetrics
				bestScore = candidateScore
				found = true
				return nil
			}
			for i := 0; i < len(blocks)-1; i++ {
				for j := i + 2; j <= len(blocks); j++ {
					copy(candidateBlocks, blocks)
					slices.Reverse(candidateBlocks[i:j])
					if err := consider(); err != nil {
						return nil, solutionScore{}, err
					}
				}
			}
			if !relocations {
				continue
			}
			for from := range blocks {
				for to := 0; to <= len(blocks); to++ {
					if blockRelocationAlreadyTried(from, to) || relocationEvaluations >= maxRelocationEvaluations {
						continue
					}
					relocationEvaluations++
					candidateBlocks = candidateBlocks[:0]
					for k, block := range blocks {
						if k == to {
							candidateBlocks = append(candidateBlocks, blocks[from])
						}
						if k != from {
							candidateBlocks = append(candidateBlocks, block)
						}
					}
					if to == len(blocks) {
						candidateBlocks = append(candidateBlocks, blocks[from])
					}
					if err := consider(); err != nil {
						return nil, solutionScore{}, err
					}
				}
			}
		}

		if !found {
			return currentStops, currentScore, nil
		}
		currentStops[bestDriverID] = bestStops
		currentMetrics[bestDriverIndex] = bestMetrics
		currentScore = bestScore
	}

	return currentStops, currentScore, nil
}

func blockRelocationAlreadyTried(from, to int) bool {
	return to == from || to == from+1 || to == from+2 || to == from-1
}

type assignmentChange struct {
	firstDriverID, secondDriverID int64
	firstStops, secondStops       []*models.Participant
	score                         solutionScore
	found                         bool
}

func optimizeAssignments(ctx context.Context, rc routeContext, routes map[int64]*balancedRoute, driverIDs []int64) (int, error) {
	slices.Sort(driverIDs)
	candidateEvaluations := 0
	routeMetrics := make(map[int64]routeObjectiveMetrics, len(driverIDs))
	for _, driverID := range driverIDs {
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		default:
		}

		metrics, err := rc.evaluateRouteObjective(ctx, routes[driverID].driver, routes[driverID].stops)
		if err != nil {
			return 0, err
		}
		routeMetrics[driverID] = metrics
	}

	const maxIterations = 50
	for iteration := range maxIterations {
		select {
		case <-ctx.Done():
			return iteration, ctx.Err()
		default:
		}

		currentScore := scoreSolution(routeMetrics, driverIDs)
		best := assignmentChange{}
		budgetExhausted := false

		_, memoized := rc.distanceCalc.(*solveDistanceLookup)
		parallel := memoized && runtime.GOMAXPROCS(0) > 1
		var pending []assignmentCandidate
		if parallel {
			pending = make([]assignmentCandidate, 0, assignmentBatchSize)
		}
		accept := func(candidate assignmentCandidate, result assignmentEvaluation) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if result.panicked {
				panic(result.panicValue)
			}
			if result.err == errUncachedCandidate {
				result = candidate.evaluate(ctx, rc, routes, routeMetrics, driverIDs)
			}
			if result.err != nil {
				return result.err
			}
			if !result.score.betterThan(currentScore) || best.found && !result.score.betterThan(best.score) {
				return nil
			}
			best = assignmentChange{
				firstDriverID: candidate.firstDriverID, secondDriverID: candidate.secondDriverID,
				firstStops: result.stops[candidate.firstDriverID], secondStops: result.stops[candidate.secondDriverID], score: result.score, found: true,
			}
			return nil
		}
		flush := func() error {
			if len(pending) == 0 {
				return ctx.Err()
			}
			results := evaluateAssignmentBatch(ctx, rc, routes, routeMetrics, driverIDs, pending)
			misses := 0
			for _, result := range results {
				if result.err == errUncachedCandidate {
					misses++
				}
			}
			if misses*2 > len(pending) {
				parallel = false
			}
			for i, candidate := range pending {
				if err := accept(candidate, results[i]); err != nil {
					return err
				}
			}
			clear(pending)
			pending = pending[:0]
			return nil
		}
		considerCandidate := func(candidate assignmentCandidate) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if candidateEvaluations >= maxAssignmentCandidateEvaluations {
				budgetExhausted = true
				return nil
			}
			candidateEvaluations++
			if !parallel {
				return accept(candidate, candidate.evaluate(ctx, rc, routes, routeMetrics, driverIDs))
			}
			pending = append(pending, candidate)
			if len(pending) == assignmentBatchSize {
				return flush()
			}
			return nil
		}
		consider := func(firstDriverID, secondDriverID int64, firstStops, secondStops []*models.Participant) error {
			return considerCandidate(assignmentCandidate{firstDriverID: firstDriverID, secondDriverID: secondDriverID, firstStops: firstStops, secondStops: secondStops})
		}

	routeActivationSearch:
		for _, sourceDriverID := range driverIDs {
			sourceRoute := routes[sourceDriverID]
			sourceBlocks := rc.routeHouseholdBlocks(sourceRoute.stops)
			if len(sourceBlocks) < 2 {
				continue
			}
			for _, sourceBlock := range sourceBlocks {
				groupSize := sourceBlock.end - sourceBlock.start
				for _, destinationDriverID := range driverIDs {
					select {
					case <-ctx.Done():
						return iteration, ctx.Err()
					default:
					}

					destinationRoute := routes[destinationDriverID]
					if len(destinationRoute.stops) != 0 || groupSize > destinationRoute.driver.VehicleCapacity {
						continue
					}
					newSourceStops := removeRange(sourceRoute.stops, sourceBlock.start, sourceBlock.end)
					newDestinationStops := slices.Clone(sourceRoute.stops[sourceBlock.start:sourceBlock.end])
					if err := consider(sourceDriverID, destinationDriverID, newSourceStops, newDestinationStops); err != nil {
						return iteration, err
					}
					if budgetExhausted {
						break routeActivationSearch
					}
				}
			}
		}

		if err := flush(); err != nil {
			return iteration, err
		}

		if !best.found && !budgetExhausted {
		relocationSearch:
			for _, sourceDriverID := range driverIDs {
				sourceRoute := routes[sourceDriverID]
				for _, sourceBlock := range rc.routeHouseholdBlocks(sourceRoute.stops) {
					groupSize := sourceBlock.end - sourceBlock.start
					members := sourceRoute.stops[sourceBlock.start:sourceBlock.end]
					for _, destinationDriverID := range driverIDs {
						select {
						case <-ctx.Done():
							return iteration, ctx.Err()
						default:
						}

						if destinationDriverID == sourceDriverID {
							continue
						}
						destinationRoute := routes[destinationDriverID]
						if len(destinationRoute.stops)+groupSize > destinationRoute.driver.VehicleCapacity {
							continue
						}

						for _, destinationPosition := range rc.householdBoundaryPositions(destinationRoute.stops) {
							newSourceStops := removeRange(sourceRoute.stops, sourceBlock.start, sourceBlock.end)
							newDestinationStops := insertParticipantsAt(destinationRoute.stops, members, destinationPosition)
							if err := consider(sourceDriverID, destinationDriverID, newSourceStops, newDestinationStops); err != nil {
								return iteration, err
							}
							if budgetExhausted {
								break relocationSearch
							}
						}
					}
				}
			}

		swapSearch:
			for firstIndex, firstDriverID := range driverIDs {
				if budgetExhausted {
					break
				}
				firstRoute := routes[firstDriverID]
				for _, firstBlock := range rc.routeHouseholdBlocks(firstRoute.stops) {
					firstSize := firstBlock.end - firstBlock.start
					firstMembers := firstRoute.stops[firstBlock.start:firstBlock.end]
					for _, secondDriverID := range driverIDs[firstIndex+1:] {
						select {
						case <-ctx.Done():
							return iteration, ctx.Err()
						default:
						}

						secondRoute := routes[secondDriverID]
						for _, secondBlock := range rc.routeHouseholdBlocks(secondRoute.stops) {
							select {
							case <-ctx.Done():
								return iteration, ctx.Err()
							default:
							}

							secondSize := secondBlock.end - secondBlock.start
							if len(firstRoute.stops)-firstSize+secondSize <= firstRoute.driver.VehicleCapacity &&
								len(secondRoute.stops)-secondSize+firstSize <= secondRoute.driver.VehicleCapacity {
								secondMembers := secondRoute.stops[secondBlock.start:secondBlock.end]
								newFirstStops := replaceRangeWithParticipants(firstRoute.stops, firstBlock.start, firstBlock.end, secondMembers)
								newSecondStops := replaceRangeWithParticipants(secondRoute.stops, secondBlock.start, secondBlock.end, firstMembers)
								if err := consider(firstDriverID, secondDriverID, newFirstStops, newSecondStops); err != nil {
									return iteration, err
								}
								if budgetExhausted {
									break swapSearch
								}
							}
						}
					}
				}
			}
		}

		if err := flush(); err != nil {
			return iteration, err
		}

		if err := flush(); err != nil {
			return iteration, err
		}

		if !best.found {
			return iteration, nil
		}
		firstRoute := routes[best.firstDriverID]
		secondRoute := routes[best.secondDriverID]
		firstRoute.stops = best.firstStops
		secondRoute.stops = best.secondStops

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

func optimizeDriverAssignments(ctx context.Context, rc routeContext, routes map[int64]*balancedRoute, driverIDs []int64) (int, error) {
	const (
		maxPairEvaluations = 100000
		softBudget         = 2 * time.Second
	)
	maxPasses := max(20, len(driverIDs))
	deadline := time.Now().Add(softBudget)
	routeMetrics := make(map[int64]routeObjectiveMetrics, len(driverIDs))
	for _, driverID := range driverIDs {
		metrics, err := rc.evaluateRouteObjective(ctx, routes[driverID].driver, routes[driverID].stops)
		if err != nil {
			return 0, err
		}
		routeMetrics[driverID] = metrics
	}
	baseline := scoreSolution(routeMetrics, driverIDs)
	evaluations := 0
	swaps := 0
	for range maxPasses {
		currentScore := scoreSolution(routeMetrics, driverIDs)
		var best struct {
			first, second   int64
			firstM, secondM routeObjectiveMetrics
			score           solutionScore
			found           bool
		}
		for firstIndex, firstDriverID := range driverIDs {
			firstRoute := routes[firstDriverID]
			for _, secondDriverID := range driverIDs[firstIndex+1:] {
				if err := ctx.Err(); err != nil {
					return swaps, err
				}
				if evaluations >= maxPairEvaluations || time.Now().After(deadline) {
					break
				}
				secondRoute := routes[secondDriverID]
				if len(firstRoute.stops) == 0 && len(secondRoute.stops) == 0 {
					continue
				}
				if len(secondRoute.stops) > firstRoute.driver.VehicleCapacity || len(firstRoute.stops) > secondRoute.driver.VehicleCapacity {
					continue
				}
				evaluations++
				firstM, err := rc.evaluateRouteObjective(ctx, firstRoute.driver, secondRoute.stops)
				if err != nil {
					return swaps, err
				}
				secondM, err := rc.evaluateRouteObjective(ctx, secondRoute.driver, firstRoute.stops)
				if err != nil {
					return swaps, err
				}
				previousFirst, previousSecond := routeMetrics[firstDriverID], routeMetrics[secondDriverID]
				routeMetrics[firstDriverID], routeMetrics[secondDriverID] = firstM, secondM
				score := scoreSolution(routeMetrics, driverIDs)
				routeMetrics[firstDriverID], routeMetrics[secondDriverID] = previousFirst, previousSecond
				if !score.betterThan(currentScore) || score.aggregateDriveDuration > currentScore.aggregateDriveDuration+scoreImprovementEpsilon {
					continue
				}
				if best.found && !score.betterThan(best.score) {
					continue
				}
				best.first, best.second, best.firstM, best.secondM, best.score, best.found = firstDriverID, secondDriverID, firstM, secondM, score, true
			}
		}
		if !best.found {
			break
		}
		routes[best.first].stops, routes[best.second].stops = routes[best.second].stops, routes[best.first].stops
		routeMetrics[best.first], routeMetrics[best.second] = best.firstM, best.secondM
		swaps++
		if evaluations >= maxPairEvaluations || time.Now().After(deadline) {
			break
		}
	}
	if swaps == 0 {
		return 0, nil
	}
	fixedOrder := make(map[int64][]*models.Participant, len(driverIDs))
	for _, driverID := range driverIDs {
		fixedOrder[driverID] = slices.Clone(routes[driverID].stops)
	}
	if err := rc.optimizeRouteOrders(ctx, routes, driverIDs); err != nil {
		return swaps, err
	}
	for _, driverID := range driverIDs {
		metrics, err := rc.evaluateRouteObjective(ctx, routes[driverID].driver, routes[driverID].stops)
		if err != nil {
			return swaps, err
		}
		routeMetrics[driverID] = metrics
	}
	if scoreSolution(routeMetrics, driverIDs).aggregateDriveDuration > baseline.aggregateDriveDuration+scoreImprovementEpsilon {
		for _, driverID := range driverIDs {
			routes[driverID].stops = fixedOrder[driverID]
		}
	}
	return swaps, nil
}

func replaceRangeWithParticipants(stops []*models.Participant, start, end int, members []*models.Participant) []*models.Participant {
	result := make([]*models.Participant, len(stops)-(end-start)+len(members))
	copy(result, stops[:start])
	copy(result[start:], members)
	copy(result[start+len(members):], stops[end:])
	return result
}

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
		route.stops = rc.coalesceHouseholdStops(route.stops)
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

type participantGroup struct {
	key     string
	bearing float64
	members []*models.Participant
	address string
	lat     float64
	lng     float64
}

func groupParticipantsByAddress(participants []*models.Participant) []*participantGroup {
	householdMap := make(map[string]*participantGroup)

	for _, p := range participants {
		key := householdKey(p)

		if group, exists := householdMap[key]; exists {
			group.members = append(group.members, p)
		} else {
			householdMap[key] = &participantGroup{key: key, members: []*models.Participant{p}, address: p.Address, lat: models.RoundCoordinate(p.Lat), lng: models.RoundCoordinate(p.Lng)}
		}
	}

	groups := make([]*participantGroup, 0, len(householdMap))
	for _, group := range householdMap {
		groups = append(groups, group)
	}

	sort.Slice(groups, func(i, j int) bool {
		if len(groups[i].members) != len(groups[j].members) {
			return len(groups[i].members) > len(groups[j].members)
		}
		return participantGroupKey(groups[i]) < participantGroupKey(groups[j])
	})

	return groups
}

func coordinateKey(lat, lng float64) string {
	return fmt.Sprintf("%.5f,%.5f", lat, lng)
}

func normalizeAddress(address string) string {
	address = strings.Map(func(r rune) rune {
		if unicode.IsPunct(r) {
			if r == '#' {
				return r
			}
			return -1
		}
		return unicode.ToLower(r)
	}, address)
	words := strings.Fields(strings.ReplaceAll(address, "#", " unit "))
	for i, word := range words {
		switch word {
		case "st":
			words[i] = "street"
		case "ave":
			words[i] = "avenue"
		case "rd":
			words[i] = "road"
		case "dr":
			words[i] = "drive"
		case "blvd":
			words[i] = "boulevard"
		case "ln":
			words[i] = "lane"
		case "ct":
			words[i] = "court"
		case "pl":
			words[i] = "place"
		case "n":
			words[i] = "north"
		case "s":
			words[i] = "south"
		case "e":
			words[i] = "east"
		case "w":
			words[i] = "west"
		case "apt", "apartment":
			words[i] = "unit"
		}
	}
	normalized := words[:0]
	for _, word := range words {
		if word == "unit" && len(normalized) > 0 && normalized[len(normalized)-1] == "unit" {
			continue
		}
		normalized = append(normalized, word)
	}
	return strings.Join(normalized, " ")
}

// HouseholdKey identifies riders who share a home and therefore ride together.
func HouseholdKey(participant *models.Participant) string { return householdKey(participant) }

func householdKey(participant *models.Participant) string {
	if participant == nil {
		return ""
	}
	if address := normalizeAddress(participant.Address); address != "" {
		key := "addr:" + address
		if participant.Lat != 0 || participant.Lng != 0 {
			key += "|coords:" + coordinateKey(models.RoundCoordinate(participant.Lat), models.RoundCoordinate(participant.Lng))
		}
		return key
	}
	return coordinateKey(models.RoundCoordinate(participant.Lat), models.RoundCoordinate(participant.Lng))
}

func participantGroupKey(group *participantGroup) string {
	if group == nil {
		return ""
	}
	if len(group.members) > 0 {
		if group.key != "" {
			return group.key
		}
		return householdKey(group.members[0])
	}
	if address := normalizeAddress(group.address); address != "" {
		return "addr:" + address
	}
	return coordinateKey(group.lat, group.lng)
}

func (rc routeContext) newParticipantGroup(participant *models.Participant) *participantGroup {
	return &participantGroup{
		members: []*models.Participant{participant},
		key:     rc.householdKey(participant),
		address: participant.Address,
		lat:     models.RoundCoordinate(participant.Lat),
		lng:     models.RoundCoordinate(participant.Lng),
	}
}

type householdBlock struct {
	start, end int
}

func (rc routeContext) routeHouseholdBlocks(stops []*models.Participant) []householdBlock {
	if len(stops) == 0 {
		return nil
	}

	blocks := make([]householdBlock, 0, len(stops))
	start := 0
	for i := 1; i <= len(stops); i++ {
		if i == len(stops) || rc.householdKey(stops[start]) != rc.householdKey(stops[i]) {
			blocks = append(blocks, householdBlock{start: start, end: i})
			start = i
		}
	}

	return blocks
}

func (rc routeContext) householdBoundaryPositions(stops []*models.Participant) []int {
	positions := make([]int, 0, len(stops)+1)
	positions = append(positions, 0)
	for i := 1; i < len(stops); i++ {
		if rc.householdKey(stops[i-1]) != rc.householdKey(stops[i]) {
			positions = append(positions, i)
		}
	}
	if len(stops) > 0 {
		positions = append(positions, len(stops))
	}

	return positions
}

func (rc routeContext) coalesceHouseholdStops(stops []*models.Participant) []*models.Participant {
	if len(stops) < 2 {
		return stops
	}

	orderedKeys := make([]string, 0, len(stops))
	grouped := make(map[string]*participantGroup, len(stops))
	for _, stop := range stops {
		key := rc.householdKey(stop)
		if group, exists := grouped[key]; exists {
			group.members = append(group.members, stop)
			continue
		}

		orderedKeys = append(orderedKeys, key)
		grouped[key] = rc.newParticipantGroup(stop)
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

func assignmentPreservesCapacityFeasibility(ctx context.Context, routes map[int64]*balancedRoute, currentDriverID int64, groups []*participantGroup, assignedGroupIndex, assignedCount int, splittableHouseholds map[string]struct{}) (bool, error) {
	select {
	case <-ctx.Done():
		return false, ctx.Err()
	default:
	}

	capacities := make([]int, 0, len(routes))
	totalCapacity := 0
	for driverID, route := range routes {
		capacity := route.driver.VehicleCapacity - len(route.stops)
		if driverID == currentDriverID {
			capacity -= assignedCount
		}
		if capacity < 0 {
			return false, nil
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
		if len(splittableHouseholds) == 0 {
			atomicSizes = append(atomicSizes, size)
		} else if _, splittable := splittableHouseholds[participantGroupKey(group)]; !splittable {
			atomicSizes = append(atomicSizes, size)
		}
	}
	if remainingParticipants > totalCapacity {
		return false, nil
	}

	allSingletons := len(atomicSizes) > 0 && len(atomicSizes)+1 <= maxHouseholdPackingSearchNodes
	for _, size := range atomicSizes {
		if size != 1 {
			allSingletons = false
			break
		}
	}
	if allSingletons {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		return true, nil
	}
	return canPackAtomicGroupSizes(ctx, atomicSizes, capacities)
}

type householdPackingState struct {
	groupIndex          int
	remainingCapacities string
}

func canPackAtomicGroupSizes(ctx context.Context, groupSizes []int, capacities []int) (bool, error) {
	if len(groupSizes) == 0 {
		return true, nil
	}

	groupSizes = slices.Clone(groupSizes)
	capacities = slices.Clone(capacities)
	sort.Sort(sort.Reverse(sort.IntSlice(groupSizes)))
	sort.Sort(sort.Reverse(sort.IntSlice(capacities)))

	failedStates := make(map[householdPackingState]struct{})
	searchNodes := 0
	var pack func(int) (bool, error)
	pack = func(groupIndex int) (bool, error) {
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		default:
		}

		searchNodes++
		if searchNodes > maxHouseholdPackingSearchNodes {
			return false, nil
		}
		if groupIndex == len(groupSizes) {
			return true, nil
		}

		state := householdPackingState{
			groupIndex:          groupIndex,
			remainingCapacities: normalizedCapacitiesKey(capacities),
		}
		if _, failed := failedStates[state]; failed {
			return false, nil
		}

		size := groupSizes[groupIndex]
		lastTriedCapacity := -1
		for i, capacity := range capacities {
			if capacity < size || capacity == lastTriedCapacity {
				continue
			}

			capacities[i] -= size
			packed, err := pack(groupIndex + 1)
			capacities[i] += size
			if err != nil {
				return false, err
			}
			if packed {
				return true, nil
			}
			lastTriedCapacity = capacity
		}

		failedStates[state] = struct{}{}
		return false, nil
	}

	return pack(0)
}

func normalizedCapacitiesKey(capacities []int) string {
	normalized := slices.Clone(capacities)
	sort.Sort(sort.Reverse(sort.IntSlice(normalized)))
	key := make([]byte, 0, len(normalized)*3)
	for _, capacity := range normalized {
		key = strconv.AppendInt(key, int64(capacity), 10)
		key = append(key, ',')
	}
	return string(key)
}

func insertParticipantsAt(stops, members []*models.Participant, pos int) []*models.Participant {
	newStops := make([]*models.Participant, len(stops)+len(members))
	copy(newStops, stops[:pos])
	copy(newStops[pos:], members)
	copy(newStops[pos+len(members):], stops[pos:])
	return newStops
}
