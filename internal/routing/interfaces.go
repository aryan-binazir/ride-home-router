package routing

import (
	"context"
	"fmt"
	"ride-home-router/internal/models"
)

type RouteMode = models.RouteMode

const (
	RouteModeDropoff RouteMode = models.RouteModeDropoff // Activity Location → Participants → Driver Home
	RouteModePickup  RouteMode = models.RouteModePickup  // Driver Home → Participants → Activity Location
)

func normalizeRouteMode(mode RouteMode) RouteMode {
	if mode == "" {
		return RouteModeDropoff
	}
	return mode
}

type RoutingRequest struct {
	InstituteCoords models.Coordinates
	Participants    []models.Participant
	Drivers         []models.Driver
	Mode            RouteMode
}

type Router interface {
	CalculateRoutes(ctx context.Context, req *RoutingRequest) (*models.RoutingResult, error)
}

type ErrRoutingFailed struct {
	Reason            string
	UnassignedCount   int
	TotalCapacity     int
	TotalParticipants int
}

func (e *ErrRoutingFailed) Error() string {
	return fmt.Sprintf("routing failed: %s", e.Reason)
}
