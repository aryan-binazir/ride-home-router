package handlers

import (
	"context"
	"errors"
	"fmt"
	"log"
	"ride-home-router/internal/database"
	"ride-home-router/internal/geocoding"
	"ride-home-router/internal/models"
	"ride-home-router/internal/routesession"
	"ride-home-router/internal/routing"
	"sync/atomic"
	"time"

	"golang.org/x/sync/errgroup"
)

// Reserve the calculation budget and full session lifetime so route edits keep
// using coordinates younger than CoordinateMaxAge until the session expires.
const coordinateRefreshMargin = routesession.DefaultTTL + routeSolveTimeout

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
	geocoder geocoding.Geocoder
	db       database.DataStore
	router   routing.Router
	sessions *routesession.Store
}

func newRouteCalculation(db database.DataStore, router routing.Router, sessions *routesession.Store, geocoder geocoding.Geocoder) *routeCalculation {
	return &routeCalculation{db: db, router: router, sessions: sessions, geocoder: geocoder}
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
	if err := c.refreshCoordinates(ctx, participants, drivers, activityLocation); err != nil {
		kind := routeCalculationRouteFailure
		if _, ok := errors.AsType[*refreshAddressNotFound](err); ok {
			kind = routeCalculationValidationFailure
		}
		return routeCalculationOutcome{Kind: kind, Err: err}
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
	if err := ctx.Err(); err != nil {
		return routeCalculationOutcome{Kind: routeCalculationRouteFailure, Err: err}
	}

	applyAssignedOrgVehicleMetadata(result.Routes, driverOrgVehicles)
	result.Summary.OrgVehiclesUsed = countUsedOrgVehicles(result.Routes)
	session, err := c.sessions.CreateContext(ctx, routesession.CreateInput{
		Routes: result.Routes, SelectedDrivers: modifiedDrivers, ActivityLocation: activityLocation,
		UseMiles: settings.UseMiles, RouteTime: input.RouteTime, Mode: input.Mode, DriverOrgVehicles: driverOrgVehicles,
	})
	if err != nil {
		return routeCalculationOutcome{Kind: routeCalculationInternalFailure, Err: err}
	}
	if err := ctx.Err(); err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		_ = c.sessions.DeleteContext(cleanupCtx, session.ID)
		return routeCalculationOutcome{Kind: routeCalculationRouteFailure, Err: err}
	}

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

// These errors keep provider details and address text out of calculation logs.
type refreshAddressNotFound struct{ name string }

func (e *refreshAddressNotFound) Error() string { return "coordinate refresh address not found" }

type refreshProviderError struct{ cause error }

func (e *refreshProviderError) Error() string { return "coordinate refresh unavailable" }
func (e *refreshProviderError) Unwrap() error { return e.cause }

func (c *routeCalculation) refreshCoordinates(ctx context.Context, participants []models.Participant, drivers []models.Driver, location *models.ActivityLocation) error {
	group, workCtx := errgroup.WithContext(ctx)
	group.SetLimit(4)
	cutoff := time.Now().Add(-(models.CoordinateMaxAge - coordinateRefreshMargin))
	var refreshed atomic.Int32
	submit := func(id int64, name, address string, lat, lng *float64, at *time.Time, persist func(context.Context, int64, string, models.Coordinates, time.Time) error) {
		if at.After(cutoff) {
			return
		}
		group.Go(func() error {
			if err := workCtx.Err(); err != nil {
				return &refreshProviderError{err}
			}
			if c.geocoder == nil {
				return &refreshProviderError{geocoding.ErrNotConfigured}
			}
			result, err := c.geocoder.GeocodeWithRetry(workCtx, address, 3)
			if err != nil {
				if errors.Is(err, geocoding.ErrNoGeocodingResults) {
					return &refreshAddressNotFound{name}
				}
				return &refreshProviderError{err}
			}
			if result == nil {
				return &refreshProviderError{fmt.Errorf("empty geocode response")}
			}
			now := time.Now()
			if err := persist(workCtx, id, address, result.Coords, now); err != nil {
				return &refreshProviderError{err}
			}
			*lat, *lng, *at = result.Coords.Lat, result.Coords.Lng, now
			refreshed.Add(1)
			return nil
		})
	}
	for i := range participants {
		p := &participants[i]
		submit(p.ID, p.Name, p.Address, &p.Lat, &p.Lng, &p.GeocodedAt, c.db.Participants().UpdateCoordinates)
	}
	for i := range drivers {
		d := &drivers[i]
		submit(d.ID, d.Name, d.Address, &d.Lat, &d.Lng, &d.GeocodedAt, c.db.Drivers().UpdateCoordinates)
	}
	submit(location.ID, location.Name, location.Address, &location.Lat, &location.Lng, &location.GeocodedAt, c.db.ActivityLocations().UpdateCoordinates)
	err := group.Wait()
	if err != nil {
		log.Printf("[GEOCODING] refresh outcome=failed refreshed=%d", refreshed.Load())
		return err
	}
	log.Printf("[GEOCODING] refresh outcome=success refreshed=%d", refreshed.Load())
	return nil
}
