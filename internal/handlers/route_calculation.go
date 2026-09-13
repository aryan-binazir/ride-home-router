package handlers

import (
	"context"
	"errors"
	"log"
	"ride-home-router/internal/database"
	"ride-home-router/internal/geocoding"
	"ride-home-router/internal/models"
	"ride-home-router/internal/routesession"
	"ride-home-router/internal/routing"
	"strings"
	"sync"
	"sync/atomic"
	"time"
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
	c.refreshCoordinates(ctx, participants, drivers, activityLocation)
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

// refreshBudget bounds the best-effort coordinate refresh inside the solve budget.
const refreshBudget = 5 * time.Second

// refreshCoordinates re-geocodes stale coordinates before planning, once per
// distinct address. It is best effort: any failure keeps the existing
// coordinates, logs counts only, and never blocks the calculation.
func (c *routeCalculation) refreshCoordinates(ctx context.Context, participants []models.Participant, drivers []models.Driver, location *models.ActivityLocation) {
	if c.geocoder == nil {
		return
	}
	cutoff := time.Now().Add(-models.CoordinateMaxAge)
	type target struct {
		lat, lng *float64
		at       *time.Time
		persist  func(context.Context, models.Coordinates, time.Time) error
	}
	byAddress := make(map[string][]target)
	addressFor := make(map[string]string)
	var order []string
	add := func(address string, at *time.Time, lat, lng *float64, persist func(context.Context, models.Coordinates, time.Time) error) {
		if at.After(cutoff) {
			return
		}
		key := strings.ToLower(strings.Join(strings.Fields(address), " "))
		if _, seen := byAddress[key]; !seen {
			order = append(order, key)
			addressFor[key] = address
		}
		byAddress[key] = append(byAddress[key], target{lat: lat, lng: lng, at: at, persist: persist})
	}
	for i := range participants {
		p := &participants[i]
		add(p.Address, &p.GeocodedAt, &p.Lat, &p.Lng, func(ctx context.Context, coords models.Coordinates, at time.Time) error {
			return c.db.Participants().UpdateCoordinates(ctx, p.ID, p.Address, coords, at)
		})
	}
	for i := range drivers {
		d := &drivers[i]
		add(d.Address, &d.GeocodedAt, &d.Lat, &d.Lng, func(ctx context.Context, coords models.Coordinates, at time.Time) error {
			return c.db.Drivers().UpdateCoordinates(ctx, d.ID, d.Address, coords, at)
		})
	}
	if location != nil {
		add(location.Address, &location.GeocodedAt, &location.Lat, &location.Lng, func(ctx context.Context, coords models.Coordinates, at time.Time) error {
			return c.db.ActivityLocations().UpdateCoordinates(ctx, location.ID, location.Address, coords, at)
		})
	}
	if len(order) == 0 {
		return
	}
	// Refresh must never eat the calculation budget: give it a short deadline.
	ctx, cancel := context.WithTimeout(ctx, refreshBudget)
	defer cancel()
	var refreshed, failed atomic.Int32
	var wg sync.WaitGroup
	slots := make(chan struct{}, 4)
	for _, key := range order {
		targets := byAddress[key]
		address := addressFor[key]
		wg.Go(func() {
			slots <- struct{}{}
			defer func() { <-slots }()
			result, err := c.geocoder.GeocodeWithRetry(ctx, address, 3)
			if err != nil || result == nil {
				failed.Add(1)
				return
			}
			now := time.Now()
			for _, t := range targets {
				if err := t.persist(ctx, result.Coords, now); err != nil {
					failed.Add(1)
					continue
				}
				*t.lat, *t.lng, *t.at = result.Coords.Lat, result.Coords.Lng, now
				refreshed.Add(1)
			}
		})
	}
	wg.Wait()
	log.Printf("[GEOCODING] refresh addresses=%d refreshed=%d failed=%d", len(order), refreshed.Load(), failed.Load())
}
