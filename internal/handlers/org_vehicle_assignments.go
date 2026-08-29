package handlers

import (
	"errors"
	"net/url"
	"ride-home-router/internal/models"
	"ride-home-router/internal/routing"
	"slices"
	"strconv"
	"strings"
)

const (
	invalidVanAssignmentMessage          = "please choose a valid van assignment"
	unselectedDriverVanAssignmentMessage = "only selected drivers can be assigned vans"
	duplicateVanAssignmentMessage        = "a van can only be assigned to one driver per event"
	selectedVanNotFoundMessage           = "selected van not found; refresh and try again"
)

var errSelectedVanNotFound = errors.New(selectedVanNotFoundMessage)

// parseOrgVehicleAssignments returns submitted choices with validation errors so
// the form can re-render them. Callers must not use the map when err is non-nil.
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
	// Stable key validation avoids map-order-dependent errors for malformed forms.
	slices.Sort(assignmentKeys)
	for _, key := range assignmentKeys {
		driverID, err := strconv.ParseInt(strings.TrimPrefix(key, "org_vehicle_"), 10, 64)
		if err != nil {
			return assignments, errors.New(invalidVanAssignmentMessage)
		}
		if _, ok := selectedDrivers[driverID]; !ok {
			return assignments, errors.New(unselectedDriverVanAssignmentMessage)
		}
	}

	for _, driverID := range selectedDriverIDs {
		values := form["org_vehicle_"+strconv.FormatInt(driverID, 10)]
		if len(values) == 0 || values[0] == "" {
			continue
		}
		vehicleID, err := strconv.ParseInt(values[0], 10, 64)
		if err != nil || vehicleID <= 0 {
			return assignments, errors.New(invalidVanAssignmentMessage)
		}

		assignments[driverID] = vehicleID
	}

	return assignments, validateUniqueOrgVehicleAssignments(assignments)
}

func validateUniqueOrgVehicleAssignments(assignments map[int64]int64) error {
	vehicleOwners := make(map[int64]int64, len(assignments))
	for driverID, vehicleID := range assignments {
		if ownerID, exists := vehicleOwners[vehicleID]; exists && ownerID != driverID {
			return errors.New(duplicateVanAssignmentMessage)
		}
		vehicleOwners[vehicleID] = driverID
	}
	return nil
}

func orgVehicleSeatCount(drivers []models.Driver, assignments map[int64]int64, vehiclesByID map[int64]models.OrganizationVehicle) int {
	total := 0
	usedVehicles := make(map[int64]struct{}, len(assignments))
	for _, driver := range drivers {
		total += driver.VehicleCapacity
		vehicleID, assigned := assignments[driver.ID]
		if !assigned {
			continue
		}
		if _, used := usedVehicles[vehicleID]; used {
			continue
		}
		vehicle, exists := vehiclesByID[vehicleID]
		if !exists {
			continue
		}
		total += vehicle.Capacity - driver.VehicleCapacity
		usedVehicles[vehicleID] = struct{}{}
	}
	return total
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

func buildCapacityShortageViewData(rerr *routing.ErrRoutingFailed, drivers []models.Driver, orgVehicles []models.OrganizationVehicle, participantIDs []int64, driverIDs []int64, activityLocation *models.ActivityLocation, mode string, useMiles bool, routeTime string, assignments map[int64]int64, driverVehicles map[int64]*models.OrganizationVehicle) CapacityShortageView {
	effectiveCapacityByDriver := make(map[int64]int, len(drivers))
	for _, driver := range drivers {
		effectiveCapacityByDriver[driver.ID] = driver.VehicleCapacity
		if vehicle, ok := driverVehicles[driver.ID]; ok && vehicle != nil {
			effectiveCapacityByDriver[driver.ID] = vehicle.Capacity
		}
	}

	shortage := rerr.TotalParticipants - rerr.TotalCapacity
	return CapacityShortageView{
		Error: CapacityShortageErrorView{
			Message:           rerr.Reason,
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
