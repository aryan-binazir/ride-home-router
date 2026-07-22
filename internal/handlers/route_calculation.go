package handlers

import (
	"context"
	"errors"
	"ride-home-router/internal/database"
	"ride-home-router/internal/models"
	"ride-home-router/internal/routesession"
	"ride-home-router/internal/routing"
)

type routeCalculationKind int

var (
	errActivityLocationNotFound = errors.New("activity location not found")
	errSomeParticipantsNotFound = errors.New("some participants not found")
	errSomeDriversNotFound      = errors.New("some drivers not found")
)

const (
	routeCalculationUnknown routeCalculationKind = iota
	routeCalculationSuccess
	routeCalculationShortage
	routeCalculationValidationFailure
	routeCalculationRouteFailure
	routeCalculationInternalFailure
)

type routeCalculationInput struct {
	ParticipantIDs        []int64
	DriverIDs             []int64
	ActivityLocationID    int64
	RouteTime             string
	Mode                  models.RouteMode
	OrgVehicleAssignments map[int64]int64
}

type routeCalculationOutcome struct {
	Kind             routeCalculationKind
	Result           *models.RoutingResult
	Session          routesession.Snapshot
	Shortage         *routeCalculationShortageContext
	ActivityLocation *models.ActivityLocation
	UseMiles         bool
	Err              error
}

type routeCalculationShortageContext struct {
	RoutingError          *routing.ErrRoutingFailed
	Drivers               []models.Driver
	AvailableOrgVehicles  []models.OrganizationVehicle
	ParticipantIDs        []int64
	DriverIDs             []int64
	ActivityLocation      *models.ActivityLocation
	Mode                  models.RouteMode
	UseMiles              bool
	RouteTime             string
	OrgVehicleAssignments map[int64]int64
	DriverOrgVehicles     map[int64]*models.OrganizationVehicle
}

type routeCalculation struct {
	db       database.DataStore
	router   routing.Router
	sessions *routesession.Store
}

func newRouteCalculation(db database.DataStore, router routing.Router, sessions *routesession.Store) *routeCalculation {
	return &routeCalculation{db: db, router: router, sessions: sessions}
}

func (c *routeCalculation) calculate(ctx context.Context, input routeCalculationInput) routeCalculationOutcome {
	settings, err := c.db.Settings().Get(ctx)
	if err != nil {
		return routeCalculationOutcome{Kind: routeCalculationInternalFailure, Err: err}
	}
	activityLocation, err := c.db.ActivityLocations().GetByID(ctx, input.ActivityLocationID)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return routeCalculationOutcome{Kind: routeCalculationValidationFailure, Err: errActivityLocationNotFound}
		}
		return routeCalculationOutcome{Kind: routeCalculationInternalFailure, Err: err}
	}
	participants, err := c.db.Participants().GetByIDs(ctx, input.ParticipantIDs)
	if err != nil {
		return routeCalculationOutcome{Kind: routeCalculationInternalFailure, Err: err}
	}
	if len(participants) != len(input.ParticipantIDs) {
		return routeCalculationOutcome{Kind: routeCalculationValidationFailure, Err: errSomeParticipantsNotFound}
	}
	drivers, err := c.db.Drivers().GetByIDs(ctx, input.DriverIDs)
	if err != nil {
		return routeCalculationOutcome{Kind: routeCalculationInternalFailure, Err: err}
	}
	if len(drivers) != len(input.DriverIDs) {
		return routeCalculationOutcome{Kind: routeCalculationValidationFailure, Err: errSomeDriversNotFound}
	}
	orgVehicleMap, err := c.loadAssignedOrgVehicles(ctx, input.OrgVehicleAssignments)
	if err != nil {
		if errors.Is(err, errSelectedVanNotFound) {
			return routeCalculationOutcome{Kind: routeCalculationValidationFailure, Err: err}
		}
		return routeCalculationOutcome{Kind: routeCalculationInternalFailure, Err: err}
	}
	modifiedDrivers, driverOrgVehicles := applyOrgVehicleAssignments(drivers, input.OrgVehicleAssignments, orgVehicleMap)

	result, err := c.router.CalculateRoutes(ctx, &routing.RoutingRequest{
		InstituteCoords: activityLocation.GetCoords(),
		Participants:    participants,
		Drivers:         modifiedDrivers,
		Mode:            input.Mode,
	})
	if err != nil {
		if routingFailure, ok := err.(*routing.ErrRoutingFailed); ok {
			availableOrgVehicles, _ := c.db.OrganizationVehicles().List(ctx)
			return routeCalculationOutcome{
				Kind: routeCalculationShortage,
				Shortage: &routeCalculationShortageContext{
					RoutingError:          routingFailure,
					Drivers:               drivers,
					AvailableOrgVehicles:  availableOrgVehicles,
					ParticipantIDs:        input.ParticipantIDs,
					DriverIDs:             input.DriverIDs,
					ActivityLocation:      activityLocation,
					Mode:                  input.Mode,
					UseMiles:              settings.UseMiles,
					RouteTime:             input.RouteTime,
					OrgVehicleAssignments: input.OrgVehicleAssignments,
					DriverOrgVehicles:     driverOrgVehicles,
				},
			}
		}
		return routeCalculationOutcome{Kind: routeCalculationRouteFailure, Err: err}
	}

	applyAssignedOrgVehicleMetadata(result.Routes, driverOrgVehicles)
	result.Summary.OrgVehiclesUsed = countUsedOrgVehicles(result.Routes)
	session := c.sessions.Create(routesession.CreateInput{
		Routes: result.Routes, SelectedDrivers: modifiedDrivers, ActivityLocation: activityLocation,
		UseMiles: settings.UseMiles, RouteTime: input.RouteTime, Mode: input.Mode, DriverOrgVehicles: driverOrgVehicles,
	})

	return routeCalculationOutcome{
		Kind:             routeCalculationSuccess,
		Result:           result,
		Session:          session,
		ActivityLocation: activityLocation,
		UseMiles:         settings.UseMiles,
	}
}

func (c *routeCalculation) loadAssignedOrgVehicles(ctx context.Context, assignments map[int64]int64) (map[int64]*models.OrganizationVehicle, error) {
	if len(assignments) == 0 {
		return map[int64]*models.OrganizationVehicle{}, nil
	}

	vehicleIDs := make([]int64, 0, len(assignments))
	seen := make(map[int64]struct{}, len(assignments))
	for _, vehicleID := range assignments {
		if _, ok := seen[vehicleID]; ok {
			continue
		}
		seen[vehicleID] = struct{}{}
		vehicleIDs = append(vehicleIDs, vehicleID)
	}

	vehicles, err := c.db.OrganizationVehicles().GetByIDs(ctx, vehicleIDs)
	if err != nil {
		return nil, err
	}
	if len(vehicles) != len(vehicleIDs) {
		return nil, errSelectedVanNotFound
	}

	vehicleMap := make(map[int64]*models.OrganizationVehicle, len(vehicles))
	for i := range vehicles {
		vehicleMap[vehicles[i].ID] = &vehicles[i]
	}
	return vehicleMap, nil
}
