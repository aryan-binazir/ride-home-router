// Package eventsnapshot converts live routing results into immutable event history snapshots.
package eventsnapshot

import (
	"errors"
	"ride-home-router/internal/models"
)

var (
	// ErrRoutesRequired indicates that no route contains any stops.
	ErrRoutesRequired = errors.New("routes are required")
	// ErrDriverRequired indicates that a route has no assigned driver.
	ErrDriverRequired = errors.New("each route must include a driver")
	// ErrParticipantRequired indicates that a route stop has no participant.
	ErrParticipantRequired = errors.New("each route stop must include a participant")
	// ErrMixedModes indicates that the saved routes do not share one mode.
	ErrMixedModes = errors.New("all routes must use the same mode")
)

// SnapshotVersion 3 stores itineraries without provider metrics.
const SnapshotVersion = 3

// Snapshot contains the immutable routes and summary persisted for an event.
type Snapshot struct {
	Mode    models.RouteMode
	Routes  []models.EventRoute
	Summary models.EventSummary
}

// Build validates a live routing result and converts it to an event snapshot.
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
		// Version 3 snapshots are itineraries only: provider distances and
		// durations are not stored, so history shows who rides with whom.
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
