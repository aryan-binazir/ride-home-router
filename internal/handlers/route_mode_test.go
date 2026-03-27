package handlers

import (
	"errors"
	"testing"

	"ride-home-router/internal/models"
)

func TestNormalizeRouteMode_WrapsInvalidModeError(t *testing.T) {
	_, err := normalizeRouteMode("sideways")
	if err == nil {
		t.Fatal("expected invalid mode error")
	}
	if !errors.Is(err, models.ErrInvalidRouteMode) {
		t.Fatalf("expected wrapped ErrInvalidRouteMode, got %v", err)
	}
	if err.Error() != messageInvalidRouteMode {
		t.Fatalf("unexpected error message %q", err.Error())
	}
}
