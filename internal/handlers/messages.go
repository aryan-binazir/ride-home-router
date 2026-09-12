package handlers

import (
	"errors"
	"fmt"
	"ride-home-router/internal/geocoding"
	"ride-home-router/internal/models"
	"unicode/utf8"
)

const (
	messageAddressRequired                               = "Enter an address."
	messageChooseActivityLocationForEvent                = "Please choose an activity location for this event."
	messageChooseRouteTime                               = "Choose a route time."
	messageChooseValidActivityLocation                   = "Please choose a valid activity location."
	messageChooseValidRouteTime                          = "Choose a valid route time."
	messageForbidden                                     = "You do not have access to this page."
	messageDriverNotFound                                = "Driver not found. Refresh the page and try again."
	messageEventDateRequired                             = "Choose an event date."
	messageEventNotFound                                 = "Event not found. Refresh the page and try again."
	messageHouseholdsDoNotFit                            = "Some families do not fit in the selected vehicles. Add a driver or choose larger vehicles."
	messageGenericInternalError                          = "An error occurred. Please try again."
	messageInvalidCapacity                               = "Enter a valid capacity."
	messageInvalidDriverID                               = "That selection is invalid. Refresh the page and try again."
	messageInvalidEventDateFormat                        = "Enter a valid event date."
	messageInvalidEventID                                = "That selection is invalid. Refresh the page and try again."
	messageInvalidFormData                               = "That form could not be read. Reload the page and try again."
	messageInvalidOrganizationVehicleID                  = "That selection is invalid. Refresh the page and try again."
	messageInvalidParticipantID                          = "That selection is invalid. Refresh the page and try again."
	messageInvalidRequestBody                            = "That request could not be read. Reload the page and try again."
	messageInvalidRouteIndex                             = "Choose a valid route. Refresh the page and try again."
	messageInvalidRouteMode                              = "Please choose a valid route mode."
	messageInvalidSMEEmail                               = "Please enter a valid SME email address."
	messageNameAndAddressRequired                        = "Enter a name and address."
	messageNameRequired                                  = "Enter a name."
	messageOrganizationVehicleNotFound                   = "Van not found. Refresh the page and try again."
	messageParticipantNotFound                           = "Rider not found. Refresh the page and try again."
	messagePreferencesSaved                              = "Preferences saved."
	messageRoutePlanExpired                              = "That route plan expired. Calculate it again."
	messageRoutesMustBeBalancedBeforeSaving              = "Give every rider a seat before saving."
	messageMovesRequired                                 = "Choose a rider to move."
	messageTooManyMoves                                  = "Move fewer riders at a time."
	messageSessionNotFound                               = "That route plan expired. Calculate it again."
	messageSelectedActivityLocationNotFound              = "The selected location is no longer available. Choose another location."
	messageSelectedActivityLocationNotFoundChooseAnother = "Selected activity location not found. Choose another location."
	messageSelectAtLeastOneDriver                        = "Please select at least one driver."
	messageSelectAtLeastOneParticipant                   = "Please select at least one participant."
	messageTargetVehicleAtCapacity                       = "That vehicle is full. Choose another vehicle."
	messageOrganizationVehicleCapacityMustBeAtLeastOne   = "Capacity must be at least 1."

	toastTypeError   = "error"
	toastTypeSuccess = "success"
	toastTypeWarning = "warning"
)

func messageEntityAdded(entity, name string) string {
	return fmt.Sprintf("%s '%s' added.", entity, name)
}

func messageEntityDeleted(entity string) string {
	return fmt.Sprintf("%s deleted.", entity)
}

func messageEntityRestored(entity string) string {
	return fmt.Sprintf("%s restored.", entity)
}

func messageEntityUpdated(entity, name string) string {
	return fmt.Sprintf("%s '%s' updated.", entity, name)
}

func messageFailedToGeocodeAddress() string { return messageMobileAddressLookupFailed }

func messageFailedToSaveLocation() string { return messageGenericInternalError }

func messageFailedToSaveVan() string { return messageGenericInternalError }

func messageNotEnoughCapacity(shortage int) string {
	seat := "seats"
	if shortage == 1 {
		seat = "seat"
	}
	return fmt.Sprintf("Add %d more %s to give everyone a seat.", shortage, seat)
}

func messageAddressNameTooLong() string {
	return fmt.Sprintf("Location name must be %d characters or fewer.", models.MaxAddressNameLength)
}

func messageVehicleCapacityOutOfRange() string {
	return fmt.Sprintf("Vehicle capacity must be between %d and %d.", models.MinVehicleCapacity, models.MaxVehicleCapacity)
}

func messageRoutesCalculated(driversAssigned int) string {
	driver := "drivers"
	if driversAssigned == 1 {
		driver = "driver"
	}
	return fmt.Sprintf("Routes calculated. %d %s assigned.", driversAssigned, driver)
}

func messageSettingsSavedUsing(name string) string {
	return fmt.Sprintf("Settings saved for %s.", name)
}

const (
	messageAddressLookupUnavailable      = "Address lookup is temporarily unavailable. Try again shortly."
	messageRouteCalculationNotConfigured = "Google Maps API key is not configured. Ask an administrator to configure it in Settings."
	messageRouteCalculationUnavailable   = "Route calculation is temporarily unavailable. Try again shortly."
	messageStaleRiders                   = "Some riders are no longer available. Refresh the page and select them again."
	messageStaleDrivers                  = "Some drivers are no longer available. Refresh the page and select them again."
)

func geocodingErrorMessage(err error) string {
	if errors.Is(err, geocoding.ErrNoGeocodingResults) {
		return messageFailedToGeocodeAddress()
	}
	if failure, ok := errors.AsType[*geocoding.ErrGeocodingFailed](err); ok && failure.Reason == "no results found" && failure.Cause == nil {
		return messageFailedToGeocodeAddress()
	}
	return messageAddressLookupUnavailable
}

func fieldLengthMessage(field, value string, maximum int) string {
	if utf8.RuneCountInString(value) > maximum {
		return fmt.Sprintf("%s must be %d characters or fewer.", field, maximum)
	}
	return ""
}

func personLengthMessage(name, address string) string {
	if message := fieldLengthMessage("Name", name, models.MaxNameLength); message != "" {
		return message
	}
	return fieldLengthMessage("Address", address, models.MaxAddressLength)
}
