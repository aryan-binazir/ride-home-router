package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"ride-home-router/internal/models"
	"ride-home-router/internal/routing"
)

const (
	invalidVanAssignmentMessage          = "Please choose a valid van assignment."
	unselectedDriverVanAssignmentMessage = "Only selected drivers can be assigned vans."
	duplicateVanAssignmentMessage        = "A van can only be assigned to one driver per event."
	selectedVanNotFoundMessage           = "Selected van not found. Refresh and try again."
)

var errSelectedVanNotFound = errors.New(selectedVanNotFoundMessage)

func parseOrgVehicleAssignments(form url.Values, selectedDriverIDs []int64) (map[int64]int64, error) {
	assignments := make(map[int64]int64)
	if len(form) == 0 {
		return assignments, nil
	}

	selectedDrivers := make(map[int64]struct{}, len(selectedDriverIDs))
	for _, driverID := range selectedDriverIDs {
		selectedDrivers[driverID] = struct{}{}
	}

	vehicleOwners := make(map[int64]int64)
	for key, values := range form {
		if !strings.HasPrefix(key, "org_vehicle_") {
			continue
		}
		if len(values) == 0 || values[0] == "" {
			continue
		}

		driverID, err := strconv.ParseInt(strings.TrimPrefix(key, "org_vehicle_"), 10, 64)
		if err != nil {
			return nil, fmt.Errorf(invalidVanAssignmentMessage)
		}
		if _, ok := selectedDrivers[driverID]; !ok {
			return nil, fmt.Errorf(unselectedDriverVanAssignmentMessage)
		}

		vehicleID, err := strconv.ParseInt(values[0], 10, 64)
		if err != nil || vehicleID <= 0 {
			return nil, fmt.Errorf(invalidVanAssignmentMessage)
		}

		if ownerID, exists := vehicleOwners[vehicleID]; exists && ownerID != driverID {
			return nil, fmt.Errorf(duplicateVanAssignmentMessage)
		}

		assignments[driverID] = vehicleID
		vehicleOwners[vehicleID] = driverID
	}

	return assignments, nil
}

func (h *Handler) loadAssignedOrgVehicles(ctx context.Context, assignments map[int64]int64) (map[int64]*models.OrganizationVehicle, error) {
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

	vehicles, err := h.DB.OrganizationVehicles().GetByIDs(ctx, vehicleIDs)
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

func applyOrgVehicleAssignments(drivers []models.Driver, assignments map[int64]int64, vehicleMap map[int64]*models.OrganizationVehicle) ([]models.Driver, map[int64]*models.OrganizationVehicle) {
	modifiedDrivers := make([]models.Driver, len(drivers))
	driverVehicles := make(map[int64]*models.OrganizationVehicle, len(assignments))

	for i, driver := range drivers {
		modifiedDrivers[i] = driver

		vehicleID, ok := assignments[driver.ID]
		if !ok {
			continue
		}

		vehicle := vehicleMap[vehicleID]
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

func buildCapacityShortageViewData(rerr *routing.ErrRoutingFailed, drivers []models.Driver, orgVehicles []models.OrganizationVehicle, participantIDs []int64, driverIDs []int64, activityLocation *models.ActivityLocation, mode string, useMiles bool, assignments map[int64]int64, driverVehicles map[int64]*models.OrganizationVehicle) map[string]interface{} {
	effectiveCapacityByDriver := make(map[int64]int, len(drivers))
	for _, driver := range drivers {
		effectiveCapacityByDriver[driver.ID] = driver.VehicleCapacity
		if vehicle, ok := driverVehicles[driver.ID]; ok && vehicle != nil {
			effectiveCapacityByDriver[driver.ID] = vehicle.Capacity
		}
	}

	shortage := rerr.TotalParticipants - rerr.TotalCapacity
	return map[string]interface{}{
		"Error": map[string]interface{}{
			"Message":           rerr.Reason,
			"UnassignedCount":   rerr.UnassignedCount,
			"TotalCapacity":     rerr.TotalCapacity,
			"TotalParticipants": rerr.TotalParticipants,
			"Shortage":          shortage,
		},
		"Drivers":                   drivers,
		"OrgVehicles":               orgVehicles,
		"ParticipantIDs":            participantIDs,
		"DriverIDs":                 driverIDs,
		"ActivityLocation":          activityLocation,
		"Mode":                      mode,
		"UseMiles":                  useMiles,
		"SelectedOrgVehicles":       assignments,
		"EffectiveCapacityByDriver": effectiveCapacityByDriver,
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
