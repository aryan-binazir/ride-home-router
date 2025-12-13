package routing

import (
	"context"
	"log"
	"math"
	"time"

	"ride-home-router/internal/distance"
	"ride-home-router/internal/models"
)

const (
	// epsilonSecs is the tolerance for comparing detours (5 seconds)
	epsilonSecs = 5.0
)

// calculateBearing returns the bearing in degrees from point A to point B
// 0° = North, 90° = East, 180° = South, 270° = West
func calculateBearing(from, to models.Coordinates) float64 {
	lat1 := from.Lat * math.Pi / 180
	lat2 := to.Lat * math.Pi / 180
	dLng := (to.Lng - from.Lng) * math.Pi / 180

	y := math.Sin(dLng) * math.Cos(lat2)
	x := math.Cos(lat1)*math.Sin(lat2) - math.Sin(lat1)*math.Cos(lat2)*math.Cos(dLng)

	bearing := math.Atan2(y, x) * 180 / math.Pi
	// Normalize to 0-360
	if bearing < 0 {
		bearing += 360
	}
	return bearing
}

// bearingDifference returns the smallest angle between two bearings (0-180)
func bearingDifference(b1, b2 float64) float64 {
	diff := math.Abs(b1 - b2)
	if diff > 180 {
		diff = 360 - diff
	}
	return diff
}

// fairnessTuple represents the optimization objective
type fairnessTuple struct {
	unassignedCount            int     // FIRST priority: making progress always wins
	unusedDrivers              int     // Number of volunteer drivers with 0 stops
	maxDetour                  float64 // Maximum detour among volunteer drivers (seconds)
	sumDetour                  float64 // Sum of volunteer detours (seconds)
	instituteVehicleDuration   float64 // Institute vehicle total time (tracked separately)
}

// route represents an internal route being built
type route struct {
	driver             *models.Driver
	stops              []*models.Participant
	path               []models.Coordinates // [Institute, stop_1, ..., stop_n, driver_home or Institute]
	baseline           float64              // Baseline duration (seconds)
	currentDuration    float64              // Current route duration (seconds)
	isInstituteVehicle bool
	instituteDriverID  int64
}

// fairnessRouter implements the fairness-first routing algorithm
type fairnessRouter struct {
	distanceCalc distance.DistanceCalculator
}

// NewFairnessRouter creates a new fairness-first router
func NewFairnessRouter(distanceCalc distance.DistanceCalculator) Router {
	return &fairnessRouter{
		distanceCalc: distanceCalc,
	}
}

func (r *fairnessRouter) CalculateRoutes(ctx context.Context, req *RoutingRequest) (*models.RoutingResult, error) {
	totalStart := time.Now()
	log.Printf("[FAIRNESS] Starting calculation: participants=%d drivers=%d institute_vehicle=%v",
		len(req.Participants), len(req.Drivers), req.InstituteVehicle != nil)

	// Handle empty participants
	if len(req.Participants) == 0 {
		log.Printf("[FAIRNESS] No participants to route")
		return &models.RoutingResult{
			Routes:   []models.CalculatedRoute{},
			Summary:  models.RoutingSummary{TotalParticipants: 0, TotalDriversUsed: 0},
			Warnings: []string{},
		}, nil
	}

	// Prewarm distance cache
	prewarmStart := time.Now()
	allPoints := []models.Coordinates{req.InstituteCoords}
	for _, p := range req.Participants {
		allPoints = append(allPoints, p.GetCoords())
	}
	for _, d := range req.Drivers {
		allPoints = append(allPoints, d.GetCoords())
	}
	if req.InstituteVehicle != nil {
		allPoints = append(allPoints, req.InstituteVehicle.GetCoords())
	}

	if err := r.distanceCalc.PrewarmCache(ctx, allPoints); err != nil {
		return nil, err
	}
	log.Printf("[TIMING] Prewarm cache: %v (points=%d)", time.Since(prewarmStart), len(allPoints))

	// Phase 0: Initialization
	phase0Start := time.Now()
	routes, baselines, err := r.initializeRoutes(ctx, req.InstituteCoords, req.Drivers)
	if err != nil {
		return nil, err
	}
	log.Printf("[TIMING] Phase 0 (init): %v", time.Since(phase0Start))

	unassigned := make([]*models.Participant, len(req.Participants))
	for i := range req.Participants {
		unassigned[i] = &req.Participants[i]
	}

	log.Printf("[FAIRNESS] Phase 0: Initialized %d routes with baselines", len(routes))

	// Phase 1: Guaranteed Assignment
	phase1Start := time.Now()
	if len(unassigned) >= len(routes) {
		log.Printf("[FAIRNESS] Phase 1: Guaranteed assignment (P >= D)")
		unassigned, err = r.phase1GuaranteedAssignment(ctx, req.InstituteCoords, routes, unassigned)
		if err != nil {
			return nil, err
		}
	} else {
		log.Printf("[FAIRNESS] Phase 1: Skipped (more drivers than participants)")
	}
	log.Printf("[TIMING] Phase 1 (seeding): %v", time.Since(phase1Start))

	// Phase 2: Cheapest Insertion
	phase2Start := time.Now()
	log.Printf("[FAIRNESS] Phase 2: Cheapest insertion with fairness (remaining=%d)", len(unassigned))
	unassigned, err = r.phase2CheapestInsertion(ctx, routes, unassigned)
	if err != nil {
		return nil, err
	}
	log.Printf("[TIMING] Phase 2 (insertion): %v", time.Since(phase2Start))

	// Phase 3: Intra-Route 2-opt
	phase3Start := time.Now()
	log.Printf("[FAIRNESS] Phase 3: Intra-route 2-opt optimization")
	if err := r.phase3IntraRoute2Opt(ctx, routes); err != nil {
		return nil, err
	}
	log.Printf("[TIMING] Phase 3 (2-opt): %v", time.Since(phase3Start))

	// Phase 4: Inter-Route Optimization
	phase4Start := time.Now()
	log.Printf("[FAIRNESS] Phase 4: Inter-route relocate/swap optimization")
	iterations, err := r.phase4InterRouteOptimization(ctx, routes)
	if err != nil {
		return nil, err
	}
	log.Printf("[TIMING] Phase 4 (inter-route): %v (iterations=%d)", time.Since(phase4Start), iterations)

	// Phase 5: Institute Vehicle Fallback
	phase5Start := time.Now()
	if len(unassigned) > 0 {
		log.Printf("[FAIRNESS] Phase 5: Institute vehicle fallback for %d participants", len(unassigned))
		unassigned, err = r.phase5InstituteVehicle(ctx, req.InstituteCoords, req.InstituteVehicle, req.InstituteVehicleDriverID, routes, unassigned)
		if err != nil {
			return nil, err
		}
		log.Printf("[TIMING] Phase 5 (fallback): %v", time.Since(phase5Start))
	}

	// Check for remaining unassigned
	if len(unassigned) > 0 {
		totalCapacity := 0
		for _, d := range req.Drivers {
			totalCapacity += d.VehicleCapacity
		}
		if req.InstituteVehicle != nil {
			totalCapacity += req.InstituteVehicle.VehicleCapacity
		}

		log.Printf("[ERROR] Routing failed: unassigned=%d total_capacity=%d total_participants=%d",
			len(unassigned), totalCapacity, len(req.Participants))
		return nil, &ErrRoutingFailed{
			Reason:            "Cannot assign all participants to available drivers",
			UnassignedCount:   len(unassigned),
			TotalCapacity:     totalCapacity,
			TotalParticipants: len(req.Participants),
		}
	}

	// Build final result
	buildStart := time.Now()
	result, err := r.buildResult(ctx, req.InstituteCoords, routes, baselines, len(req.Participants))
	if err != nil {
		return nil, err
	}
	log.Printf("[TIMING] Build result: %v", time.Since(buildStart))

	log.Printf("[FAIRNESS] Calculation complete: drivers_used=%d max_detour=%.0fs sum_detour=%.0fs",
		result.Summary.TotalDriversUsed, result.Summary.MaxDetourSecs, result.Summary.SumDetourSecs)
	log.Printf("[TIMING] TOTAL: %v", time.Since(totalStart))

	return result, nil
}

// Phase 0: Initialize routes with baselines
func (r *fairnessRouter) initializeRoutes(ctx context.Context, instituteCoords models.Coordinates, drivers []models.Driver) (map[int64]*route, map[int64]float64, error) {
	routes := make(map[int64]*route)
	baselines := make(map[int64]float64)

	for i := range drivers {
		driver := &drivers[i]

		// Calculate baseline duration (Institute -> driver_home)
		distResult, err := r.distanceCalc.GetDistance(ctx, instituteCoords, driver.GetCoords())
		if err != nil {
			return nil, nil, err
		}

		baseline := distResult.DurationSecs
		baselines[driver.ID] = baseline

		// Initialize route
		routes[driver.ID] = &route{
			driver:          driver,
			stops:           []*models.Participant{},
			path:            []models.Coordinates{instituteCoords, driver.GetCoords()},
			baseline:        baseline,
			currentDuration: baseline,
		}

		log.Printf("[FAIRNESS] Initialized route for %s: baseline=%.0fs", driver.Name, baseline)
	}

	return routes, baselines, nil
}

// Phase 1: Guaranteed Assignment
func (r *fairnessRouter) phase1GuaranteedAssignment(ctx context.Context, instituteCoords models.Coordinates, routes map[int64]*route, unassigned []*models.Participant) ([]*models.Participant, error) {
	if len(unassigned) < len(routes) {
		return unassigned, nil
	}

	// Build cost matrix: cost[driver_id][participant_id] = insertion cost
	type costEntry struct {
		driverID      int64
		participantID int64
		cost          float64
	}

	var costList []costEntry
	for driverID, route := range routes {
		for _, participant := range unassigned {
			// Calculate insertion cost between Institute and driver_home
			cost, err := r.calculateInsertionCost(ctx, instituteCoords, route.driver.GetCoords(), participant.GetCoords())
			if err != nil {
				return nil, err
			}
			costList = append(costList, costEntry{
				driverID:      driverID,
				participantID: participant.ID,
				cost:          cost,
			})
		}
	}

	// Greedy matching: find cheapest (driver, participant) pairs
	usedDrivers := make(map[int64]bool)
	usedParticipants := make(map[int64]bool)

	for len(usedDrivers) < len(routes) && len(usedParticipants) < len(unassigned) {
		// Find cheapest unassigned pair
		bestCost := math.Inf(1)
		var bestDriverID int64
		var bestParticipant *models.Participant

		for _, entry := range costList {
			if usedDrivers[entry.driverID] || usedParticipants[entry.participantID] {
				continue
			}
			if entry.cost < bestCost {
				bestCost = entry.cost
				bestDriverID = entry.driverID
				// Find participant
				for _, p := range unassigned {
					if p.ID == entry.participantID {
						bestParticipant = p
						break
					}
				}
			}
		}

		if bestParticipant == nil {
			break
		}

		// Insert at position 0
		if err := r.insertParticipant(ctx, routes[bestDriverID], bestParticipant, 0); err != nil {
			return nil, err
		}

		usedDrivers[bestDriverID] = true
		usedParticipants[bestParticipant.ID] = true

		log.Printf("[FAIRNESS] Phase 1: Seeded %s with %s (cost=%.0fs)",
			routes[bestDriverID].driver.Name, bestParticipant.Name, bestCost)
	}

	// Remove assigned participants from unassigned
	remaining := make([]*models.Participant, 0)
	for _, p := range unassigned {
		if !usedParticipants[p.ID] {
			remaining = append(remaining, p)
		}
	}

	return remaining, nil
}

// Phase 2: Cheapest Insertion with Fairness
func (r *fairnessRouter) phase2CheapestInsertion(ctx context.Context, routes map[int64]*route, unassigned []*models.Participant) ([]*models.Participant, error) {
	for len(unassigned) > 0 {
		type moveCandidate struct {
			driverID    int64
			participant *models.Participant
			position    int
			fairness    fairnessTuple
		}

		var bestMove *moveCandidate

		for _, participant := range unassigned {
			for driverID, route := range routes {
				if route.isInstituteVehicle {
					continue // Institute vehicle handled in Phase 5
				}
				if len(route.stops) >= route.driver.VehicleCapacity {
					continue // Driver is full
				}

				// Try inserting at each position
				for position := 0; position <= len(route.stops); position++ {
					// Simulate insertion and calculate new fairness
					newFairness, err := r.simulateInsertion(ctx, routes, driverID, participant, position, len(unassigned)-1)
					if err != nil {
						return nil, err
					}

					if bestMove == nil || r.isBetterFairness(newFairness, bestMove.fairness) {
						bestMove = &moveCandidate{
							driverID:    driverID,
							participant: participant,
							position:    position,
							fairness:    newFairness,
						}
					}
				}
			}
		}

		if bestMove == nil {
			// No valid insertion found; all drivers full
			log.Printf("[FAIRNESS] Phase 2: All drivers full, %d participants remaining", len(unassigned))
			break
		}

		// Execute the best move
		if err := r.insertParticipant(ctx, routes[bestMove.driverID], bestMove.participant, bestMove.position); err != nil {
			return nil, err
		}

		// Remove from unassigned
		newUnassigned := make([]*models.Participant, 0, len(unassigned)-1)
		for _, p := range unassigned {
			if p.ID != bestMove.participant.ID {
				newUnassigned = append(newUnassigned, p)
			}
		}
		unassigned = newUnassigned
	}

	return unassigned, nil
}

// Phase 3: Intra-Route 2-Opt
func (r *fairnessRouter) phase3IntraRoute2Opt(ctx context.Context, routes map[int64]*route) error {
	for driverID, route := range routes {
		if len(route.stops) < 3 {
			continue // 2-opt needs at least 3 stops
		}

		optimized, err := r.twoOptOptimize(ctx, route.path[0], route.path[len(route.path)-1], route.stops)
		if err != nil {
			return err
		}

		// Update route with optimized stops
		route.stops = optimized
		if err := r.updateRoutePath(ctx, route); err != nil {
			return err
		}

		log.Printf("[FAIRNESS] Phase 3: 2-opt optimized route for %s: duration=%.0fs",
			routes[driverID].driver.Name, route.currentDuration)
	}

	return nil
}

// Phase 4: Inter-Route Optimization
func (r *fairnessRouter) phase4InterRouteOptimization(ctx context.Context, routes map[int64]*route) (int, error) {
	maxIterations := 50
	iteration := 0
	improved := true

	// Get driver IDs as slice for iteration
	driverIDs := make([]int64, 0, len(routes))
	for driverID := range routes {
		if !routes[driverID].isInstituteVehicle {
			driverIDs = append(driverIDs, driverID)
		}
	}

	for improved && iteration < maxIterations {
		improved = false
		iteration++

		for i := 0; i < len(driverIDs); i++ {
			for j := i + 1; j < len(driverIDs); j++ {
				driverA := driverIDs[i]
				driverB := driverIDs[j]

				// Try relocating from A to B
				didImprove, err := r.tryRelocate(ctx, routes, driverA, driverB)
				if err != nil {
					return iteration, err
				}
				if didImprove {
					improved = true
				}

				// Try relocating from B to A
				didImprove, err = r.tryRelocate(ctx, routes, driverB, driverA)
				if err != nil {
					return iteration, err
				}
				if didImprove {
					improved = true
				}

				// Try swapping between A and B
				didImprove, err = r.trySwap(ctx, routes, driverA, driverB)
				if err != nil {
					return iteration, err
				}
				if didImprove {
					improved = true
				}
			}
		}
	}

	return iteration, nil
}

// Phase 5: Institute Vehicle Fallback
func (r *fairnessRouter) phase5InstituteVehicle(ctx context.Context, instituteCoords models.Coordinates, instituteVehicle *models.Driver, instituteDriverID int64, routes map[int64]*route, unassigned []*models.Participant) ([]*models.Participant, error) {
	if len(unassigned) == 0 {
		return unassigned, nil
	}

	if instituteVehicle == nil {
		return unassigned, nil // Will be caught as error later
	}

	// Create institute vehicle route
	instituteRoute := &route{
		driver:             instituteVehicle,
		stops:              []*models.Participant{},
		path:               []models.Coordinates{instituteCoords, instituteCoords},
		baseline:           0, // Returns to institute, so baseline is 0
		currentDuration:    0,
		isInstituteVehicle: true,
		instituteDriverID:  instituteDriverID,
	}

	// Assign remaining participants using greedy nearest-neighbor
	currentLocation := instituteCoords
	for len(unassigned) > 0 && len(instituteRoute.stops) < instituteVehicle.VehicleCapacity {
		// Find nearest to current location
		var nearest *models.Participant
		minDist := math.Inf(1)

		for _, p := range unassigned {
			distResult, err := r.distanceCalc.GetDistance(ctx, currentLocation, p.GetCoords())
			if err != nil {
				return nil, err
			}
			if distResult.DurationSecs < minDist {
				minDist = distResult.DurationSecs
				nearest = p
			}
		}

		if nearest == nil {
			break
		}

		if err := r.insertParticipant(ctx, instituteRoute, nearest, len(instituteRoute.stops)); err != nil {
			return nil, err
		}

		currentLocation = nearest.GetCoords()

		// Remove from unassigned
		newUnassigned := make([]*models.Participant, 0)
		for _, p := range unassigned {
			if p.ID != nearest.ID {
				newUnassigned = append(newUnassigned, p)
			}
		}
		unassigned = newUnassigned
	}

	// Optimize institute route with 2-opt
	if len(instituteRoute.stops) >= 3 {
		optimized, err := r.twoOptOptimize(ctx, instituteCoords, instituteCoords, instituteRoute.stops)
		if err != nil {
			return nil, err
		}
		instituteRoute.stops = optimized
		if err := r.updateRoutePath(ctx, instituteRoute); err != nil {
			return nil, err
		}
	}

	// Add to routes
	routes[instituteVehicle.ID] = instituteRoute

	log.Printf("[FAIRNESS] Phase 5: Institute vehicle assigned %d participants, duration=%.0fs",
		len(instituteRoute.stops), instituteRoute.currentDuration)

	return unassigned, nil
}

// Helper: Calculate insertion cost
func (r *fairnessRouter) calculateInsertionCost(ctx context.Context, a, b, p models.Coordinates) (float64, error) {
	// ins(a, b, p) = time(a, p) + time(p, b) - time(a, b)
	distAP, err := r.distanceCalc.GetDistance(ctx, a, p)
	if err != nil {
		return 0, err
	}

	distPB, err := r.distanceCalc.GetDistance(ctx, p, b)
	if err != nil {
		return 0, err
	}

	distAB, err := r.distanceCalc.GetDistance(ctx, a, b)
	if err != nil {
		return 0, err
	}

	return distAP.DurationSecs + distPB.DurationSecs - distAB.DurationSecs, nil
}

// Helper: Insert participant into route
func (r *fairnessRouter) insertParticipant(ctx context.Context, route *route, participant *models.Participant, position int) error {
	// Insert into stops list
	newStops := make([]*models.Participant, len(route.stops)+1)
	copy(newStops[:position], route.stops[:position])
	newStops[position] = participant
	copy(newStops[position+1:], route.stops[position:])
	route.stops = newStops

	// Update path and duration
	return r.updateRoutePath(ctx, route)
}

// Helper: Update route path and duration
func (r *fairnessRouter) updateRoutePath(ctx context.Context, route *route) error {
	// Rebuild path
	destination := route.driver.GetCoords()
	if route.isInstituteVehicle {
		destination = route.path[0] // Institute coords (stored as first element)
	}

	newPath := []models.Coordinates{route.path[0]} // Start with institute
	for _, stop := range route.stops {
		newPath = append(newPath, stop.GetCoords())
	}
	newPath = append(newPath, destination)
	route.path = newPath

	// Recalculate route duration
	totalDuration := 0.0
	for i := 0; i < len(route.path)-1; i++ {
		distResult, err := r.distanceCalc.GetDistance(ctx, route.path[i], route.path[i+1])
		if err != nil {
			return err
		}
		totalDuration += distResult.DurationSecs
	}

	route.currentDuration = totalDuration
	return nil
}

// Helper: Simulate insertion and return new fairness
func (r *fairnessRouter) simulateInsertion(ctx context.Context, routes map[int64]*route, driverID int64, participant *models.Participant, position int, unassignedCount int) (fairnessTuple, error) {
	// Calculate delta instead of deep copying
	route := routes[driverID]

	// Calculate insertion cost
	var prevCoord, nextCoord models.Coordinates
	if position == 0 {
		prevCoord = route.path[0] // Institute
	} else {
		prevCoord = route.stops[position-1].GetCoords()
	}

	if position < len(route.stops) {
		nextCoord = route.stops[position].GetCoords()
	} else {
		nextCoord = route.path[len(route.path)-1] // Driver home
	}

	insertionCost, err := r.calculateInsertionCost(ctx, prevCoord, nextCoord, participant.GetCoords())
	if err != nil {
		return fairnessTuple{}, err
	}

	newDuration := route.currentDuration + insertionCost
	newDetour := newDuration - route.baseline

	// Calculate fairness with this change
	return r.calculateFairnessDelta(routes, driverID, newDetour, unassignedCount), nil
}

// Helper: Calculate fairness tuple with a delta for one route
func (r *fairnessRouter) calculateFairnessDelta(routes map[int64]*route, changedDriverID int64, newDetour float64, unassignedCount int) fairnessTuple {
	unusedDrivers := 0
	maxDetour := 0.0
	sumDetour := 0.0
	instituteVehicleDuration := 0.0

	for driverID, route := range routes {
		if route.isInstituteVehicle {
			instituteVehicleDuration = route.currentDuration
			continue
		}

		var detour float64
		if driverID == changedDriverID {
			detour = newDetour
		} else {
			detour = route.currentDuration - route.baseline
		}

		sumDetour += detour

		if detour > maxDetour {
			maxDetour = detour
		}

		if len(route.stops) == 0 {
			unusedDrivers++
		}
	}

	return fairnessTuple{
		unassignedCount:          unassignedCount,
		unusedDrivers:            unusedDrivers,
		maxDetour:                maxDetour,
		sumDetour:                sumDetour,
		instituteVehicleDuration: instituteVehicleDuration,
	}
}

// Helper: Calculate current fairness tuple
func (r *fairnessRouter) currentFairnessTuple(routes map[int64]*route, unassignedCount int) fairnessTuple {
	unusedDrivers := 0
	maxDetour := 0.0
	sumDetour := 0.0
	instituteVehicleDuration := 0.0

	for _, route := range routes {
		if route.isInstituteVehicle {
			instituteVehicleDuration = route.currentDuration
			continue
		}

		detour := route.currentDuration - route.baseline
		sumDetour += detour

		if detour > maxDetour {
			maxDetour = detour
		}

		if len(route.stops) == 0 {
			unusedDrivers++
		}
	}

	return fairnessTuple{
		unassignedCount:          unassignedCount,
		unusedDrivers:            unusedDrivers,
		maxDetour:                maxDetour,
		sumDetour:                sumDetour,
		instituteVehicleDuration: instituteVehicleDuration,
	}
}

// Helper: Compare fairness tuples
func (r *fairnessRouter) isBetterFairness(a, b fairnessTuple) bool {
	// 1. Compare unassigned count FIRST
	if a.unassignedCount < b.unassignedCount {
		return true
	}
	if a.unassignedCount > b.unassignedCount {
		return false
	}

	// 2. Compare unused drivers
	if a.unusedDrivers < b.unusedDrivers {
		return true
	}
	if a.unusedDrivers > b.unusedDrivers {
		return false
	}

	// 3. Compare max detour (with tolerance)
	if a.maxDetour < b.maxDetour-epsilonSecs {
		return true
	}
	if a.maxDetour > b.maxDetour+epsilonSecs {
		return false
	}

	// 4. Compare sum detour (with tolerance)
	if a.sumDetour < b.sumDetour-epsilonSecs {
		return true
	}

	return false
}

// Helper: Try relocate from src to dest
func (r *fairnessRouter) tryRelocate(ctx context.Context, routes map[int64]*route, srcDriverID, destDriverID int64) (bool, error) {
	srcRoute := routes[srcDriverID]
	destRoute := routes[destDriverID]

	if len(srcRoute.stops) == 0 {
		return false, nil
	}

	if len(destRoute.stops) >= destRoute.driver.VehicleCapacity {
		return false, nil
	}

	currentFairness := r.currentFairnessTuple(routes, 0)
	bestFairness := currentFairness
	var bestSrcPos, bestDestPos int
	var bestParticipant *models.Participant
	foundBetter := false

	// Try relocating each stop from src to each position in dest
	for srcPos := 0; srcPos < len(srcRoute.stops); srcPos++ {
		participant := srcRoute.stops[srcPos]

		for destPos := 0; destPos <= len(destRoute.stops); destPos++ {
			// Simulate the move
			newFairness, err := r.simulateRelocate(ctx, routes, srcDriverID, srcPos, destDriverID, destPos)
			if err != nil {
				return false, err
			}

			if r.isBetterFairness(newFairness, bestFairness) {
				bestFairness = newFairness
				bestSrcPos = srcPos
				bestDestPos = destPos
				bestParticipant = participant
				foundBetter = true
			}
		}
	}

	if !foundBetter {
		return false, nil
	}

	// Execute the move
	// Remove from src
	newSrcStops := make([]*models.Participant, 0, len(srcRoute.stops)-1)
	for i, p := range srcRoute.stops {
		if i != bestSrcPos {
			newSrcStops = append(newSrcStops, p)
		}
	}
	srcRoute.stops = newSrcStops
	if err := r.updateRoutePath(ctx, srcRoute); err != nil {
		return false, err
	}

	// Insert into dest
	if err := r.insertParticipant(ctx, destRoute, bestParticipant, bestDestPos); err != nil {
		return false, err
	}

	// Re-run 2-opt on both routes
	if len(srcRoute.stops) >= 3 {
		optimized, err := r.twoOptOptimize(ctx, srcRoute.path[0], srcRoute.path[len(srcRoute.path)-1], srcRoute.stops)
		if err != nil {
			return false, err
		}
		srcRoute.stops = optimized
		if err := r.updateRoutePath(ctx, srcRoute); err != nil {
			return false, err
		}
	}

	if len(destRoute.stops) >= 3 {
		optimized, err := r.twoOptOptimize(ctx, destRoute.path[0], destRoute.path[len(destRoute.path)-1], destRoute.stops)
		if err != nil {
			return false, err
		}
		destRoute.stops = optimized
		if err := r.updateRoutePath(ctx, destRoute); err != nil {
			return false, err
		}
	}

	log.Printf("[FAIRNESS] Relocated %s from %s to %s",
		bestParticipant.Name, srcRoute.driver.Name, destRoute.driver.Name)

	return true, nil
}

// Helper: Simulate relocate
func (r *fairnessRouter) simulateRelocate(ctx context.Context, routes map[int64]*route, srcDriverID int64, srcPos int, destDriverID int64, destPos int) (fairnessTuple, error) {
	srcRoute := routes[srcDriverID]
	destRoute := routes[destDriverID]

	// Calculate new duration for src (remove participant at srcPos)
	participant := srcRoute.stops[srcPos]

	// Calculate removal cost for src
	var prevCoord, nextCoord models.Coordinates
	if srcPos == 0 {
		prevCoord = srcRoute.path[0]
	} else {
		prevCoord = srcRoute.stops[srcPos-1].GetCoords()
	}

	if srcPos < len(srcRoute.stops)-1 {
		nextCoord = srcRoute.stops[srcPos+1].GetCoords()
	} else {
		nextCoord = srcRoute.path[len(srcRoute.path)-1]
	}

	// Current: prev -> participant -> next
	distPrevP, err := r.distanceCalc.GetDistance(ctx, prevCoord, participant.GetCoords())
	if err != nil {
		return fairnessTuple{}, err
	}
	distPNext, err := r.distanceCalc.GetDistance(ctx, participant.GetCoords(), nextCoord)
	if err != nil {
		return fairnessTuple{}, err
	}
	distPrevNext, err := r.distanceCalc.GetDistance(ctx, prevCoord, nextCoord)
	if err != nil {
		return fairnessTuple{}, err
	}

	removalCost := distPrevP.DurationSecs + distPNext.DurationSecs - distPrevNext.DurationSecs
	newSrcDuration := srcRoute.currentDuration - removalCost
	newSrcDetour := newSrcDuration - srcRoute.baseline

	// Calculate insertion cost for dest
	if destPos == 0 {
		prevCoord = destRoute.path[0]
	} else {
		prevCoord = destRoute.stops[destPos-1].GetCoords()
	}

	if destPos < len(destRoute.stops) {
		nextCoord = destRoute.stops[destPos].GetCoords()
	} else {
		nextCoord = destRoute.path[len(destRoute.path)-1]
	}

	insertionCost, err := r.calculateInsertionCost(ctx, prevCoord, nextCoord, participant.GetCoords())
	if err != nil {
		return fairnessTuple{}, err
	}

	newDestDuration := destRoute.currentDuration + insertionCost
	newDestDetour := newDestDuration - destRoute.baseline

	// Calculate fairness with both changes
	return r.calculateFairnessDeltaTwo(routes, srcDriverID, newSrcDetour, destDriverID, newDestDetour, 0), nil
}

// Helper: Calculate fairness tuple with deltas for two routes
func (r *fairnessRouter) calculateFairnessDeltaTwo(routes map[int64]*route, changedDriverID1 int64, newDetour1 float64, changedDriverID2 int64, newDetour2 float64, unassignedCount int) fairnessTuple {
	unusedDrivers := 0
	maxDetour := 0.0
	sumDetour := 0.0
	instituteVehicleDuration := 0.0

	for driverID, route := range routes {
		if route.isInstituteVehicle {
			instituteVehicleDuration = route.currentDuration
			continue
		}

		var detour float64
		if driverID == changedDriverID1 {
			detour = newDetour1
		} else if driverID == changedDriverID2 {
			detour = newDetour2
		} else {
			detour = route.currentDuration - route.baseline
		}

		sumDetour += detour

		if detour > maxDetour {
			maxDetour = detour
		}

		// For unused drivers check: if this is one of the changed routes, we need to check if it would be empty
		stopsCount := len(route.stops)
		if driverID == changedDriverID1 && newDetour1 == -route.baseline {
			// This route would be empty after removal
			stopsCount = 0
		}
		if stopsCount == 0 {
			unusedDrivers++
		}
	}

	return fairnessTuple{
		unassignedCount:          unassignedCount,
		unusedDrivers:            unusedDrivers,
		maxDetour:                maxDetour,
		sumDetour:                sumDetour,
		instituteVehicleDuration: instituteVehicleDuration,
	}
}

// Helper: Try swap between two routes
func (r *fairnessRouter) trySwap(ctx context.Context, routes map[int64]*route, driverAID, driverBID int64) (bool, error) {
	routeA := routes[driverAID]
	routeB := routes[driverBID]

	if len(routeA.stops) == 0 || len(routeB.stops) == 0 {
		return false, nil
	}

	currentFairness := r.currentFairnessTuple(routes, 0)
	bestFairness := currentFairness
	var bestPosA, bestPosB int
	foundBetter := false

	// Try swapping each pair of stops
	for posA := 0; posA < len(routeA.stops); posA++ {
		for posB := 0; posB < len(routeB.stops); posB++ {
			newFairness, err := r.simulateSwap(ctx, routes, driverAID, posA, driverBID, posB)
			if err != nil {
				return false, err
			}

			if r.isBetterFairness(newFairness, bestFairness) {
				bestFairness = newFairness
				bestPosA = posA
				bestPosB = posB
				foundBetter = true
			}
		}
	}

	if !foundBetter {
		return false, nil
	}

	// Execute the swap
	participantA := routeA.stops[bestPosA]
	participantB := routeB.stops[bestPosB]

	routeA.stops[bestPosA] = participantB
	routeB.stops[bestPosB] = participantA

	if err := r.updateRoutePath(ctx, routeA); err != nil {
		return false, err
	}
	if err := r.updateRoutePath(ctx, routeB); err != nil {
		return false, err
	}

	// Re-run 2-opt on both routes
	if len(routeA.stops) >= 3 {
		optimized, err := r.twoOptOptimize(ctx, routeA.path[0], routeA.path[len(routeA.path)-1], routeA.stops)
		if err != nil {
			return false, err
		}
		routeA.stops = optimized
		if err := r.updateRoutePath(ctx, routeA); err != nil {
			return false, err
		}
	}

	if len(routeB.stops) >= 3 {
		optimized, err := r.twoOptOptimize(ctx, routeB.path[0], routeB.path[len(routeB.path)-1], routeB.stops)
		if err != nil {
			return false, err
		}
		routeB.stops = optimized
		if err := r.updateRoutePath(ctx, routeB); err != nil {
			return false, err
		}
	}

	log.Printf("[FAIRNESS] Swapped %s (%s) with %s (%s)",
		participantA.Name, routeA.driver.Name, participantB.Name, routeB.driver.Name)

	return true, nil
}

// Helper: Simulate swap
func (r *fairnessRouter) simulateSwap(ctx context.Context, routes map[int64]*route, driverAID int64, posA int, driverBID int64, posB int) (fairnessTuple, error) {
	routeA := routes[driverAID]
	routeB := routes[driverBID]

	participantA := routeA.stops[posA]
	participantB := routeB.stops[posB]

	// Calculate change in route A (replacing A with B)
	var prevCoordA, nextCoordA models.Coordinates
	if posA == 0 {
		prevCoordA = routeA.path[0]
	} else {
		prevCoordA = routeA.stops[posA-1].GetCoords()
	}
	if posA < len(routeA.stops)-1 {
		nextCoordA = routeA.stops[posA+1].GetCoords()
	} else {
		nextCoordA = routeA.path[len(routeA.path)-1]
	}

	// Current cost: prev -> A -> next
	distPrevA, err := r.distanceCalc.GetDistance(ctx, prevCoordA, participantA.GetCoords())
	if err != nil {
		return fairnessTuple{}, err
	}
	distANext, err := r.distanceCalc.GetDistance(ctx, participantA.GetCoords(), nextCoordA)
	if err != nil {
		return fairnessTuple{}, err
	}

	// New cost: prev -> B -> next
	distPrevB, err := r.distanceCalc.GetDistance(ctx, prevCoordA, participantB.GetCoords())
	if err != nil {
		return fairnessTuple{}, err
	}
	distBNext, err := r.distanceCalc.GetDistance(ctx, participantB.GetCoords(), nextCoordA)
	if err != nil {
		return fairnessTuple{}, err
	}

	deltaA := (distPrevB.DurationSecs + distBNext.DurationSecs) - (distPrevA.DurationSecs + distANext.DurationSecs)
	newDurationA := routeA.currentDuration + deltaA
	newDetourA := newDurationA - routeA.baseline

	// Calculate change in route B (replacing B with A)
	var prevCoordB, nextCoordB models.Coordinates
	if posB == 0 {
		prevCoordB = routeB.path[0]
	} else {
		prevCoordB = routeB.stops[posB-1].GetCoords()
	}
	if posB < len(routeB.stops)-1 {
		nextCoordB = routeB.stops[posB+1].GetCoords()
	} else {
		nextCoordB = routeB.path[len(routeB.path)-1]
	}

	// Current cost: prev -> B -> next
	distPrevBOrig, err := r.distanceCalc.GetDistance(ctx, prevCoordB, participantB.GetCoords())
	if err != nil {
		return fairnessTuple{}, err
	}
	distBNextOrig, err := r.distanceCalc.GetDistance(ctx, participantB.GetCoords(), nextCoordB)
	if err != nil {
		return fairnessTuple{}, err
	}

	// New cost: prev -> A -> next
	distPrevANew, err := r.distanceCalc.GetDistance(ctx, prevCoordB, participantA.GetCoords())
	if err != nil {
		return fairnessTuple{}, err
	}
	distANextNew, err := r.distanceCalc.GetDistance(ctx, participantA.GetCoords(), nextCoordB)
	if err != nil {
		return fairnessTuple{}, err
	}

	deltaB := (distPrevANew.DurationSecs + distANextNew.DurationSecs) - (distPrevBOrig.DurationSecs + distBNextOrig.DurationSecs)
	newDurationB := routeB.currentDuration + deltaB
	newDetourB := newDurationB - routeB.baseline

	// Calculate fairness with both changes
	return r.calculateFairnessDeltaTwo(routes, driverAID, newDetourA, driverBID, newDetourB, 0), nil
}

// Helper: 2-opt optimization
func (r *fairnessRouter) twoOptOptimize(ctx context.Context, start, end models.Coordinates, stops []*models.Participant) ([]*models.Participant, error) {
	if len(stops) < 3 {
		return stops, nil
	}

	improved := true
	for improved {
		improved = false
		for i := 0; i < len(stops)-1; i++ {
			for j := i + 2; j <= len(stops); j++ {
				// Calculate current distance for edges
				var fromBeforeI, toAfterJ models.Coordinates
				if i == 0 {
					fromBeforeI = start
				} else {
					fromBeforeI = stops[i-1].GetCoords()
				}

				if j < len(stops) {
					toAfterJ = stops[j].GetCoords()
				} else {
					toAfterJ = end
				}

				// Current: fromBeforeI -> stops[i] and stops[j-1] -> toAfterJ
				distCurr1, err := r.distanceCalc.GetDistance(ctx, fromBeforeI, stops[i].GetCoords())
				if err != nil {
					return nil, err
				}

				distCurr2, err := r.distanceCalc.GetDistance(ctx, stops[j-1].GetCoords(), toAfterJ)
				if err != nil {
					return nil, err
				}

				currentDist := distCurr1.DurationSecs + distCurr2.DurationSecs

				// New (after reversing segment [i..j-1]): fromBeforeI -> stops[j-1] and stops[i] -> toAfterJ
				distNew1, err := r.distanceCalc.GetDistance(ctx, fromBeforeI, stops[j-1].GetCoords())
				if err != nil {
					return nil, err
				}

				distNew2, err := r.distanceCalc.GetDistance(ctx, stops[i].GetCoords(), toAfterJ)
				if err != nil {
					return nil, err
				}

				newDist := distNew1.DurationSecs + distNew2.DurationSecs

				if newDist < currentDist {
					// Reverse segment [i..j-1]
					for left, right := i, j-1; left < right; left, right = left+1, right-1 {
						stops[left], stops[right] = stops[right], stops[left]
					}
					improved = true
				}
			}
		}
	}

	return stops, nil
}

// Helper: Build final result
func (r *fairnessRouter) buildResult(ctx context.Context, instituteCoords models.Coordinates, routes map[int64]*route, baselines map[int64]float64, totalParticipants int) (*models.RoutingResult, error) {
	calculatedRoutes := make([]models.CalculatedRoute, 0, len(routes))
	maxDetour := 0.0
	sumDetour := 0.0
	totalDropoffDistance := 0.0
	totalDistance := 0.0
	driversUsed := 0
	usedInstituteVehicle := false

	for _, route := range routes {
		if len(route.stops) == 0 {
			continue // Skip unused drivers
		}

		driversUsed++

		// Build route stops with distances and durations
		routeStops := make([]models.RouteStop, len(route.stops))
		cumulativeDistance := 0.0
		cumulativeDuration := 0.0

		for i, participant := range route.stops {
			var prevCoord models.Coordinates
			if i == 0 {
				prevCoord = route.path[0]
			} else {
				prevCoord = route.stops[i-1].GetCoords()
			}

			distResult, err := r.distanceCalc.GetDistance(ctx, prevCoord, participant.GetCoords())
			if err != nil {
				return nil, err
			}

			cumulativeDistance += distResult.DistanceMeters
			cumulativeDuration += distResult.DurationSecs

			routeStops[i] = models.RouteStop{
				Order:                    i,
				Participant:              participant,
				DistanceFromPrevMeters:   distResult.DistanceMeters,
				CumulativeDistanceMeters: cumulativeDistance,
				DurationFromPrevSecs:     distResult.DurationSecs,
				CumulativeDurationSecs:   cumulativeDuration,
			}
		}

		// Calculate distance to driver home
		lastStopCoord := route.stops[len(route.stops)-1].GetCoords()
		destination := route.driver.GetCoords()
		if route.isInstituteVehicle {
			destination = instituteCoords
			usedInstituteVehicle = true
		}

		distToHome, err := r.distanceCalc.GetDistance(ctx, lastStopCoord, destination)
		if err != nil {
			return nil, err
		}

		detour := route.currentDuration - route.baseline
		if !route.isInstituteVehicle {
			if detour > maxDetour {
				maxDetour = detour
			}
			sumDetour += detour
		}

		totalDropoffDistance += cumulativeDistance
		totalDistance += cumulativeDistance + distToHome.DistanceMeters

		calculatedRoutes = append(calculatedRoutes, models.CalculatedRoute{
			Driver:                     route.driver,
			Stops:                      routeStops,
			TotalDropoffDistanceMeters: cumulativeDistance,
			DistanceToDriverHomeMeters: distToHome.DistanceMeters,
			TotalDistanceMeters:        cumulativeDistance + distToHome.DistanceMeters,
			UsedInstituteVehicle:       route.isInstituteVehicle,
			InstituteVehicleDriverID:   route.instituteDriverID,
			BaselineDurationSecs:       route.baseline,
			RouteDurationSecs:          route.currentDuration,
			DetourSecs:                 detour,
		})
	}

	avgDetour := 0.0
	if driversUsed > 0 {
		avgDetour = sumDetour / float64(driversUsed)
	}

	return &models.RoutingResult{
		Routes: calculatedRoutes,
		Summary: models.RoutingSummary{
			TotalParticipants:          totalParticipants,
			TotalDriversUsed:           driversUsed,
			TotalDropoffDistanceMeters: totalDropoffDistance,
			TotalDistanceMeters:        totalDistance,
			UsedInstituteVehicle:       usedInstituteVehicle,
			UnassignedParticipants:     []int64{},
			MaxDetourSecs:              maxDetour,
			SumDetourSecs:              sumDetour,
			AverageDetourSecs:          avgDetour,
		},
		Warnings: []string{},
	}, nil
}
