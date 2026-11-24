package handlers

import (
	"context"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/aryan-binazir/ride-home-router/internal/database"
	"github.com/aryan-binazir/ride-home-router/internal/models"
	"github.com/aryan-binazir/ride-home-router/internal/routing"
)

const (
	invalidVanAssignmentMessage          = "Choose a valid van assignment."
	unselectedDriverVanAssignmentMessage = "Select a driver before assigning a van."
	duplicateVanAssignmentMessage        = "Assign each van to only one driver."
	selectedVanNotFoundMessage           = "The selected van is no longer available. Refresh the page and try again."
)

var errSelectedVanNotFound = formError{selectedVanNotFoundMessage}

func parseOrgVehicleAssignments(form url.Values, selectedDriverIDs []int64) (map[int64]int64, error) {
	assignments := make(map[int64]int64)
	if len(form) == 0 {
		return assignments, nil
	}

	selectedDrivers := make(map[int64]struct{}, len(selectedDriverIDs))
	for _, driverID := range selectedDriverIDs {
		selectedDrivers[driverID] = struct{}{}
	}

	assignmentKeys := make([]string, 0)
	for key, values := range form {
		if !strings.HasPrefix(key, "org_vehicle_") {
			continue
		}
		if len(values) == 0 || values[0] == "" {
			continue
		}
		assignmentKeys = append(assignmentKeys, key)
	}
	slices.Sort(assignmentKeys)
	for _, key := range assignmentKeys {
		driverID, err := strconv.ParseInt(strings.TrimPrefix(key, "org_vehicle_"), 10, 64)
		if err != nil {
			return assignments, formError{invalidVanAssignmentMessage}
		}
		if _, ok := selectedDrivers[driverID]; !ok {
			return assignments, formError{unselectedDriverVanAssignmentMessage}
		}
	}

	for _, driverID := range selectedDriverIDs {
		values := form["org_vehicle_"+strconv.FormatInt(driverID, 10)]
		if len(values) == 0 || values[0] == "" {
			continue
		}
		vehicleID, err := strconv.ParseInt(values[0], 10, 64)
		if err != nil || vehicleID <= 0 {
			return assignments, formError{invalidVanAssignmentMessage}
		}

		assignments[driverID] = vehicleID
	}

	return assignments, validateUniqueOrgVehicleAssignments(assignments)
}

func validateUniqueOrgVehicleAssignments(assignments map[int64]int64) error {
	vehicleOwners := make(map[int64]int64, len(assignments))
	for driverID, vehicleID := range assignments {
		if ownerID, exists := vehicleOwners[vehicleID]; exists && ownerID != driverID {
			return formError{duplicateVanAssignmentMessage}
		}
		vehicleOwners[vehicleID] = driverID
	}
	return nil
}

func loadAssignedOrgVehicles(ctx context.Context, repository database.OrganizationVehicleRepository, assignments map[int64]int64) (map[int64]*models.OrganizationVehicle, bool, error) {
	driverVehicles := make(map[int64]*models.OrganizationVehicle, len(assignments))
	if len(assignments) == 0 {
		return driverVehicles, true, nil
	}
	vehicleIDs := make([]int64, 0, len(assignments))
	seen := make(map[int64]struct{}, len(assignments))
	for _, vehicleID := range assignments {
		if _, exists := seen[vehicleID]; exists {
			continue
		}
		seen[vehicleID] = struct{}{}
		vehicleIDs = append(vehicleIDs, vehicleID)
	}
	vehicles, err := repository.GetByIDs(ctx, vehicleIDs)
	if err != nil {
		return nil, false, err
	}
	byID := make(map[int64]*models.OrganizationVehicle, len(vehicles))
	for i := range vehicles {
		byID[vehicles[i].ID] = &vehicles[i]
	}
	for driverID, vehicleID := range assignments {
		if vehicle := byID[vehicleID]; vehicle != nil {
			driverVehicles[driverID] = vehicle
		}
	}
	return driverVehicles, len(driverVehicles) == len(assignments), nil
}

func applyOrgVehicleAssignments(drivers []models.Driver, assignedVehicles map[int64]*models.OrganizationVehicle) ([]models.Driver, map[int64]*models.OrganizationVehicle) {
	modifiedDrivers := make([]models.Driver, len(drivers))
	driverVehicles := make(map[int64]*models.OrganizationVehicle, len(assignedVehicles))

	for i, driver := range drivers {
		modifiedDrivers[i] = driver

		vehicle := assignedVehicles[driver.ID]
		if vehicle == nil {
			continue
		}

		modifiedDrivers[i].VehicleCapacity = vehicle.Capacity
		driverVehicles[driver.ID] = vehicle
	}

	return modifiedDrivers, driverVehicles
}

func applyAssignedOrgVehicleMetadata(routes []models.CalculatedRoute, driverVehicles map[int64]*models.OrganizationVehicle) {
	for i := range routes {
		route := &routes[i]
		if route.Driver == nil {
			continue
		}
		if vehicle, ok := driverVehicles[route.Driver.ID]; ok && vehicle != nil {
			route.OrgVehicleID = vehicle.ID
			route.OrgVehicleName = vehicle.Name
			route.EffectiveCapacity = vehicle.Capacity
			continue
		}
		route.EffectiveCapacity = route.Driver.VehicleCapacity
	}
}

func buildCapacityShortageViewData(rerr *routing.ErrRoutingFailed, drivers []models.Driver, orgVehicles []models.OrganizationVehicle, participantIDs []int64, driverIDs []int64, activityLocation *models.ActivityLocation, mode string, useMiles bool, routeTime string, assignments map[int64]int64, driverVehicles map[int64]*models.OrganizationVehicle) CapacityShortageView {
	effectiveCapacityByDriver := make(map[int64]int, len(drivers))
	for _, driver := range drivers {
		effectiveCapacityByDriver[driver.ID] = driver.VehicleCapacity
		if vehicle, ok := driverVehicles[driver.ID]; ok && vehicle != nil {
			effectiveCapacityByDriver[driver.ID] = vehicle.Capacity
		}
	}

	shortage := max(0, rerr.TotalParticipants-rerr.TotalCapacity)
	return CapacityShortageView{
		Error: CapacityShortageErrorView{
			Message:           messageHouseholdsDoNotFit,
			UnassignedCount:   rerr.UnassignedCount,
			TotalCapacity:     rerr.TotalCapacity,
			TotalParticipants: rerr.TotalParticipants,
			Shortage:          shortage,
		},
		Drivers:                   drivers,
		OrgVehicles:               orgVehicles,
		ParticipantIDs:            participantIDs,
		DriverIDs:                 driverIDs,
		ActivityLocation:          activityLocation,
		Mode:                      mode,
		UseMiles:                  useMiles,
		RouteTime:                 routeTime,
		SelectedOrgVehicles:       assignments,
		EffectiveCapacityByDriver: effectiveCapacityByDriver,
		AssignedVehicles:          driverVehicles,
	}
}

func countUsedOrgVehicles(routes []models.CalculatedRoute) int {
	used := make(map[int64]struct{})
	for _, route := range routes {
		if route.OrgVehicleID <= 0 || len(route.Stops) == 0 {
			continue
		}
		used[route.OrgVehicleID] = struct{}{}
	}
	return len(used)
}
