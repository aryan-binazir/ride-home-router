package routing

import (
	"context"
	"log/slog"
	"ride-home-router/internal/distance"
	"ride-home-router/internal/models"
	"slices"
	"strconv"
	"time"
)

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

	slog.Info("Starting route calculation",
		"component", "balanced",
		"participants", len(req.Participants),
		"drivers", len(req.Drivers),
		"mode", string(mode),
	)

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
	slog.Info("Prewarmed cache", "component", "timing", "duration", time.Since(prewarmStart))
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
			slog.Info("Phase 1 filled additional driver routes", "component", "balanced", "routes", repairs)
		}
	}
	slog.Info("Phase 1 seed selected", "component", "balanced", "seed", seedName)
	slog.Info("Phase 1 completed", "component", "timing", "seed", seedName, "duration", time.Since(phase1Start))

	phase2Start := time.Now()
	if err := rc.optimizeRouteOrders(ctx, routes, driverIDs); err != nil {
		return nil, err
	}
	slog.Info("Phase 2 completed route ordering", "component", "timing", "duration", time.Since(phase2Start))

	phase3Start := time.Now()
	iterations, err := optimizeAssignments(ctx, rc, routes, driverIDs)
	if err != nil {
		return nil, err
	}
	slog.Info("Phase 3 completed assignment search",
		"component", "timing",
		"duration", time.Since(phase3Start),
		"iterations", iterations,
	)

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
	slog.Info("Phase 4 completed driver swaps", "component", "timing", "duration", time.Since(phase4Start), "swaps", swaps)

	result, err := buildResult(ctx, rc, routes, len(req.Participants))
	if err != nil {
		return nil, err
	}

	slog.Info("Route calculation completed",
		"component", "balanced",
		"drivers_used", result.Summary.TotalDriversUsed,
		"total_distance_meters", strconv.FormatFloat(result.Summary.TotalDropoffDistanceMeters, 'f', 0, 64),
	)
	slog.Info("Route calculation total", "component", "timing", "duration", time.Since(totalStart))

	return result, nil
}

type balancedRoute struct {
	driver *models.Driver
	stops  []*models.Participant
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
