package routing

import (
	"context"
	"fmt"

	"ride-home-router/internal/models"
)

// RouteMode defines the direction of the route calculation.
type RouteMode = models.RouteMode

const (
	RouteModeDropoff RouteMode = models.RouteModeDropoff // Activity Location → Participants → Driver Home
	RouteModePickup  RouteMode = models.RouteModePickup  // Driver Home → Participants → Activity Location
)

// RoutingRequest contains the input for route calculation
type RoutingRequest struct {
	InstituteCoords models.Coordinates
	Participants    []models.Participant
	Drivers         []models.Driver
	Mode            RouteMode
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
