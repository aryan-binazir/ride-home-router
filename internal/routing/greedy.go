package routing

import (
	"context"
	"fmt"

	"ride-home-router/internal/distance"
	"ride-home-router/internal/models"
)

// RoutingRequest contains the input for route calculation
type RoutingRequest struct {
	InstituteCoords          models.Coordinates
	Participants             []models.Participant
	Drivers                  []models.Driver
	InstituteVehicle         *models.Driver
	InstituteVehicleDriverID int64
}

// Router provides route optimization
type Router interface {
	CalculateRoutes(ctx context.Context, req *RoutingRequest) (*models.RoutingResult, error)
}

// ErrRoutingFailed is returned when no valid route solution exists
type ErrRoutingFailed struct {
	Reason            string
	UnassignedCount   int
	TotalCapacity     int
	TotalParticipants int
}

func (e *ErrRoutingFailed) Error() string {
	return fmt.Sprintf("routing failed: %s", e.Reason)
}

type greedyRouter struct {
	distanceCalc distance.DistanceCalculator
}

// NewGreedyRouter creates a new greedy nearest-neighbor router
func NewGreedyRouter(distanceCalc distance.DistanceCalculator) Router {
	return &greedyRouter{
		distanceCalc: distanceCalc,
	}
}

func (r *greedyRouter) CalculateRoutes(ctx context.Context, req *RoutingRequest) (*models.RoutingResult, error) {
	if len(req.Participants) == 0 {
		return &models.RoutingResult{
			Routes:   []models.CalculatedRoute{},
			Summary:  models.RoutingSummary{TotalParticipants: 0, TotalDriversUsed: 0},
			Warnings: []string{},
		}, nil
	}

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

	unassigned := make(map[int64]*models.Participant)
	for i := range req.Participants {
		unassigned[req.Participants[i].ID] = &req.Participants[i]
	}

	var routes []models.CalculatedRoute

	for i := range req.Drivers {
		driver := &req.Drivers[i]
		route, err := r.assignDriverRoute(ctx, driver, req.InstituteCoords, unassigned, false, 0)
		if err != nil {
			return nil, err
		}
		if len(route.Stops) > 0 {
			routes = append(routes, *route)
		}
	}

	if len(unassigned) > 0 && req.InstituteVehicle != nil {
		route, err := r.assignDriverRoute(ctx, req.InstituteVehicle, req.InstituteCoords, unassigned, true, req.InstituteVehicleDriverID)
		if err != nil {
			return nil, err
		}
		if len(route.Stops) > 0 {
			routes = append(routes, *route)
		}
	}

	if len(unassigned) > 0 {
		unassignedIDs := make([]int64, 0, len(unassigned))
		for id := range unassigned {
			unassignedIDs = append(unassignedIDs, id)
		}

		totalCapacity := 0
		for _, d := range req.Drivers {
			totalCapacity += d.VehicleCapacity
		}
		if req.InstituteVehicle != nil {
			totalCapacity += req.InstituteVehicle.VehicleCapacity
		}

		return nil, &ErrRoutingFailed{
			Reason:            "Cannot assign all participants to available drivers",
			UnassignedCount:   len(unassigned),
			TotalCapacity:     totalCapacity,
			TotalParticipants: len(req.Participants),
		}
	}

	totalDropoffDistance := 0.0
	driversUsed := len(routes)
	usedInstituteVehicle := false
	for _, route := range routes {
		totalDropoffDistance += route.TotalDropoffDistanceMeters
		if route.UsedInstituteVehicle {
			usedInstituteVehicle = true
		}
	}

	return &models.RoutingResult{
		Routes: routes,
		Summary: models.RoutingSummary{
			TotalParticipants:          len(req.Participants),
			TotalDriversUsed:           driversUsed,
			TotalDropoffDistanceMeters: totalDropoffDistance,
			UsedInstituteVehicle:       usedInstituteVehicle,
			UnassignedParticipants:     []int64{},
		},
		Warnings: []string{},
	}, nil
}

func (r *greedyRouter) assignDriverRoute(
	ctx context.Context,
	driver *models.Driver,
	instituteCoords models.Coordinates,
	unassigned map[int64]*models.Participant,
	isInstituteVehicle bool,
	instituteVehicleDriverID int64,
) (*models.CalculatedRoute, error) {
	route := &models.CalculatedRoute{
		Driver:                   driver,
		Stops:                    []models.RouteStop{},
		UsedInstituteVehicle:     isInstituteVehicle,
		InstituteVehicleDriverID: instituteVehicleDriverID,
	}

	currentLocation := instituteCoords
	cumulativeDistance := 0.0

	for len(route.Stops) < driver.VehicleCapacity && len(unassigned) > 0 {
		nearest, distanceToNearest, err := r.findNearestParticipant(ctx, currentLocation, unassigned)
		if err != nil {
			return nil, err
		}

		if nearest == nil {
			break
		}

		cumulativeDistance += distanceToNearest

		route.Stops = append(route.Stops, models.RouteStop{
			Order:                    len(route.Stops),
			Participant:              nearest,
			DistanceFromPrevMeters:   distanceToNearest,
			CumulativeDistanceMeters: cumulativeDistance,
		})

		delete(unassigned, nearest.ID)
		currentLocation = nearest.GetCoords()
	}

	route.TotalDropoffDistanceMeters = cumulativeDistance

	if len(route.Stops) > 0 {
		if isInstituteVehicle {
			distResult, err := r.distanceCalc.GetDistance(ctx, currentLocation, instituteCoords)
			if err != nil {
				return nil, err
			}
			route.DistanceToDriverHomeMeters = distResult.DistanceMeters
		} else {
			distResult, err := r.distanceCalc.GetDistance(ctx, currentLocation, driver.GetCoords())
			if err != nil {
				return nil, err
			}
			route.DistanceToDriverHomeMeters = distResult.DistanceMeters
		}
	}

	return route, nil
}

func (r *greedyRouter) findNearestParticipant(
	ctx context.Context,
	currentLocation models.Coordinates,
	unassigned map[int64]*models.Participant,
) (*models.Participant, float64, error) {
	if len(unassigned) == 0 {
		return nil, 0, nil
	}

	var nearest *models.Participant
	minDistance := -1.0

	for _, participant := range unassigned {
		distResult, err := r.distanceCalc.GetDistance(ctx, currentLocation, participant.GetCoords())
		if err != nil {
			return nil, 0, err
		}

		if minDistance < 0 || distResult.DistanceMeters < minDistance {
			minDistance = distResult.DistanceMeters
			nearest = participant
		}
	}

	return nearest, minDistance, nil
}
