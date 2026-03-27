package handlers

import "ride-home-router/internal/models"

type routeModeValidationError struct {
	cause error
}

func (e routeModeValidationError) Error() string {
	return messageInvalidRouteMode
}

func (e routeModeValidationError) Unwrap() error {
	return e.cause
}

func normalizeRouteMode(value string) (models.RouteMode, error) {
	mode, err := models.ParseRouteMode(value)
	if err != nil {
		return "", routeModeValidationError{cause: err}
	}
	return mode, nil
}
