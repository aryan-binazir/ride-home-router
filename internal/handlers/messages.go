package handlers

import (
	"fmt"
	"ride-home-router/internal/models"
)

const (
	messageAddressRequired                               = "address is required"
	messageChooseActivityLocationForEvent                = "Please choose an activity location for this event."
	messageChooseRouteTime                               = "please choose a route time"
	messageChooseValidActivityLocation                   = "Please choose a valid activity location."
	messageChooseValidRouteTime                          = "please choose a valid route time"
	messageForbidden                                     = "Forbidden"
	messageDriverNotFound                                = "driver not found"
	messageEventDateRequired                             = "Event date is required"
	messageEventNotFound                                 = "Event not found"
	messageGenericInternalError                          = "An error occurred. Please try again."
	messageInvalidCapacity                               = "Invalid capacity"
	messageInvalidDriverID                               = "invalid driver ID"
	messageInvalidEventDateFormat                        = "Invalid event date format (use YYYY-MM-DD)"
	messageInvalidEventID                                = "Invalid event ID"
	messageInvalidFormData                               = "Invalid form data"
	messageInvalidOrganizationVehicleID                  = "invalid organization vehicle ID"
	messageInvalidParticipantID                          = "invalid participant ID"
	messageInvalidRequestBody                            = "Invalid request body"
	messageInvalidRouteIndex                             = "Invalid route index"
	messageInvalidRouteMode                              = "Please choose a valid route mode."
	messageInvalidSMEEmail                               = "Please enter a valid SME email address."
	messageNameAndAddressRequired                        = "name and address are required"
	messageNameRequired                                  = "name is required"
	messageOrganizationVehicleNotFound                   = "organization vehicle not found"
	messageParticipantNotFound                           = "participant not found"
	messagePreferencesSaved                              = "Preferences saved!"
	messageRoutePlanExpired                              = "That route plan expired. Calculate it again."
	messageRoutesMustBeBalancedBeforeSaving              = "Routes must be balanced before saving"
	messageMovesRequired                                 = "At least one move is required"
	messageTooManyMoves                                  = "Too many moves in one request"
	messageSessionNotFound                               = "Session not found"
	messageSelectedActivityLocationNotFound              = "Selected activity location not found"
	messageSelectedActivityLocationNotFoundChooseAnother = "Selected activity location not found. Choose another location."
	messageSelectAtLeastOneDriver                        = "Please select at least one driver."
	messageSelectAtLeastOneParticipant                   = "Please select at least one participant."
	messageTargetVehicleAtCapacity                       = "Target vehicle is at capacity"
	messageOrganizationVehicleCapacityMustBeAtLeastOne   = "Capacity must be at least 1"

	toastTypeError   = "error"
	toastTypeSuccess = "success"
	toastTypeWarning = "warning"
)

func messageEntityAdded(entity, name string) string {
	return fmt.Sprintf("%s '%s' added!", entity, name)
}

func messageEntityDeleted(entity string) string {
	return fmt.Sprintf("%s deleted", entity)
}

func messageEntityRestored(entity string) string {
	return fmt.Sprintf("%s restored", entity)
}

func messageEntityUpdated(entity, name string) string {
	return fmt.Sprintf("%s '%s' updated!", entity, name)
}

func messageFailedToGeocodeAddress(err error) string {
	return fmt.Sprintf("Failed to geocode address: %v", err)
}

func messageFailedToSaveLocation(err error) string {
	return fmt.Sprintf("Failed to save location: %v", err)
}

func messageFailedToSaveVan(err error) string {
	return fmt.Sprintf("Failed to save van: %v", err)
}

func messageNotEnoughCapacity(shortage int) string {
	seat := "seats"
	if shortage == 1 {
		seat = "seat"
	}
	return fmt.Sprintf("Not enough capacity - need %d more %s", shortage, seat)
}

func messageAddressNameTooLong() string {
	return fmt.Sprintf("location name must be %d characters or fewer", models.MaxAddressNameLength)
}

func messageVehicleCapacityOutOfRange() string {
	return fmt.Sprintf("vehicle capacity must be between %d and %d", models.MinVehicleCapacity, models.MaxVehicleCapacity)
}

func messageRoutesCalculated(driversAssigned int) string {
	driver := "drivers"
	if driversAssigned == 1 {
		driver = "driver"
	}
	return fmt.Sprintf("Routes calculated! %d %s assigned.", driversAssigned, driver)
}

func messageSettingsSavedUsing(name string) string {
	return fmt.Sprintf("Settings saved! Using: %s", name)
}
