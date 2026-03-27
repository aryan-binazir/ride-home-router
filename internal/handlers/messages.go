package handlers

import "fmt"

const (
	messageAddressRequired                               = "Address is required"
	messageChooseActivityLocationForEvent                = "Please choose an activity location for this event."
	messageChooseRouteTime                               = "Please choose a route time."
	messageChooseValidActivityLocation                   = "Please choose a valid activity location."
	messageChooseValidRouteTime                          = "Please choose a valid route time."
	messageDatabasePathMustBeAbsolute                    = "Database path must be absolute"
	messageDatabasePathUpdatedRestart                    = "Database path updated. Restart the application to apply changes."
	messageDriverNotFound                                = "Driver not found"
	messageEventDateRequired                             = "Event date is required"
	messageEventNotFound                                 = "Event not found"
	messageGenericInternalError                          = "An error occurred. Please try again."
	messageInvalidCapacity                               = "Invalid capacity"
	messageInvalidDriverID                               = "Invalid driver ID"
	messageInvalidEventDateFormat                        = "Invalid event date format (use YYYY-MM-DD)"
	messageInvalidEventID                                = "Invalid event ID"
	messageInvalidFormData                               = "Invalid form data"
	messageInvalidOrganizationVehicleID                  = "Invalid organization vehicle ID"
	messageInvalidParticipantID                          = "Invalid participant ID"
	messageInvalidRequestBody                            = "Invalid request body"
	messageInvalidRouteIndex                             = "Invalid route index"
	messageInvalidRouteMode                              = "Please choose a valid route mode."
	messageInvalidRoutesData                             = "Invalid routes data"
	messageNameAndAddressRequired                        = "Name and address are required"
	messageNameRequired                                  = "Name is required"
	messageOrganizationVehicleNotFound                   = "Organization vehicle not found"
	messageParticipantNotFound                           = "Participant not found"
	messagePreferencesSaved                              = "Preferences saved!"
	messageRoutesRequired                                = "Routes are required"
	messageSessionNotFound                               = "Session not found"
	messageSelectedActivityLocationNotFound              = "Selected activity location not found"
	messageSelectedActivityLocationNotFoundChooseAnother = "Selected activity location not found. Choose another location."
	messageSelectAtLeastOneDriver                        = "Please select at least one driver."
	messageSelectAtLeastOneParticipant                   = "Please select at least one participant."
	messageTargetVehicleAtCapacity                       = "Target vehicle is at capacity"
	messageVehicleCapacityMustBeGreaterThanZero          = "Vehicle capacity must be greater than 0"
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

func messageEntityUpdated(entity, name string) string {
	return fmt.Sprintf("%s '%s' updated!", entity, name)
}

func messageFailedToCreateDirectory(err error) string {
	return fmt.Sprintf("Failed to create directory: %v", err)
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
	return fmt.Sprintf("Not enough capacity - need %d more seats", shortage)
}

func messageRoutesCalculated(driversAssigned int) string {
	return fmt.Sprintf("Routes calculated! %d drivers assigned.", driversAssigned)
}

func messageSettingsSavedUsing(name string) string {
	return fmt.Sprintf("Settings saved! Using: %s", name)
}
