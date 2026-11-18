package routing

import (
	"context"
	"log/slog"
	"maps"
	"math"
	"ride-home-router/internal/models"
	"slices"
	"sort"
	"strconv"
)

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
				slog.Info("Phase 1 sweep repair failed", "component", "balanced", "groups_remaining", len(groups))
				return false, err
			}
			slog.Info("Phase 1 sweep repair placed groups into spare seats", "component", "balanced", "groups_placed", len(groups))
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
			slog.Info("Phase 1 sweep repair found no group that fits empty driver", "component", "balanced", "driver_id", driverID)
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
			slog.Info("Phase 1 sweep repair found no vehicle that fits group",
				"component", "balanced",
				"seats", assignedCount,
				"group", participantGroupKey(group),
				"candidates", len(candidates),
			)
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
	slog.Info("Distributing participants across drivers",
		"component", "balanced",
		"participants", totalParticipants,
		"household_groups", len(groups),
		"drivers", len(driverIDs),
	)

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
			slog.Info("Assigned rider to driver",
				"component", "balanced",
				"rider", memberNames[0],
				"driver", route.driver.Name,
				"position", bestPosition,
				"rider_score_delta", strconv.FormatFloat(bestCost, 'f', 0, 64),
			)
		} else {
			slog.Info("Assigned household group to driver",
				"component", "balanced",
				"riders", memberNames,
				"driver", route.driver.Name,
				"position", bestPosition,
				"rider_score_delta", strconv.FormatFloat(bestCost, 'f', 0, 64),
				"size", len(bestGroup.members),
			)
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

func maxRouteVehicleCapacity(routes map[int64]*balancedRoute) int {
	maxCapacity := 0
	for _, route := range routes {
		if route.driver.VehicleCapacity > maxCapacity {
			maxCapacity = route.driver.VehicleCapacity
		}
	}
	return maxCapacity
}
