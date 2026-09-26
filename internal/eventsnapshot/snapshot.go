package eventsnapshot

import (
	"errors"

	"ride-home-router/internal/models"
)

var (
	ErrRoutesRequired      = errors.New("routes are required")
	ErrDriverRequired      = errors.New("each route must include a driver")
	ErrParticipantRequired = errors.New("each route stop must include a participant")
	ErrMixedModes          = errors.New("all routes must use the same mode")
)

// SnapshotVersion 3 stores itineraries without provider metrics.
const SnapshotVersion = 3

type Snapshot struct {
	Mode    models.RouteMode
	Routes  []models.EventRoute
	Summary models.EventSummary
}

func Build(result models.RoutingResult) (Snapshot, error) {
	mode, err := models.ParseRouteMode(string(result.Mode))
	if err != nil {
		return Snapshot{}, err
	}
	snapshot := Snapshot{Mode: mode, Routes: make([]models.EventRoute, 0, len(result.Routes))}
	for _, route := range result.Routes {
		if route.Driver == nil {
			return Snapshot{}, ErrDriverRequired
		}
		if len(route.Stops) == 0 {
			continue
		}
		routeMode := route.Mode
		if routeMode == "" {
			routeMode = mode
		}
		routeMode, err = models.ParseRouteMode(string(routeMode))
		if err != nil {
			return Snapshot{}, err
		}
		if routeMode != mode {
			return Snapshot{}, ErrMixedModes
		}
		eventRoute := models.EventRoute{
			RouteOrder:        len(snapshot.Routes),
			DriverID:          route.Driver.ID,
			DriverName:        route.Driver.Name,
			DriverAddress:     route.Driver.Address,
			DriverAddressName: route.Driver.AddressName,
			EffectiveCapacity: route.EffectiveCapacity,
			OrgVehicleID:      route.OrgVehicleID,
			OrgVehicleName:    route.OrgVehicleName,
			Mode:              routeMode,
			SnapshotVersion:   SnapshotVersion,
			MetricsComplete:   false,
			Stops:             make([]models.EventRouteStop, 0, len(route.Stops)),
		}
		if eventRoute.EffectiveCapacity == 0 {
			eventRoute.EffectiveCapacity = route.Driver.VehicleCapacity
		}
		for stopIndex, stop := range route.Stops {
			if stop.Participant == nil {
				return Snapshot{}, ErrParticipantRequired
			}
			eventRoute.Stops = append(eventRoute.Stops, models.EventRouteStop{
				Order:                  stopIndex,
				ParticipantID:          stop.Participant.ID,
				ParticipantName:        stop.Participant.Name,
				ParticipantAddress:     stop.Participant.Address,
				ParticipantAddressName: stop.Participant.AddressName,
			})
			snapshot.Summary.TotalParticipants++
		}
		snapshot.Summary.TotalDrivers++
		if route.OrgVehicleID > 0 {
			snapshot.Summary.OrgVehiclesUsed++
		}
		snapshot.Routes = append(snapshot.Routes, eventRoute)
	}
	if len(snapshot.Routes) == 0 {
		return Snapshot{}, ErrRoutesRequired
	}
	snapshot.Summary.Mode = mode
	return snapshot, nil
}
