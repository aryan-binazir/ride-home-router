package handlers

import (
	"context"
	"ride-home-router/internal/database"
	"ride-home-router/internal/geocoding"
	"ride-home-router/internal/models"
)

// rosterEditor accepts validated, normalized edits. Adapters own validation and
// existing-record lookup so their error precedence remains unchanged.
type rosterEditor struct {
	db       database.DataStore
	geocoder geocoding.Geocoder
}

type participantEdit struct {
	Name, Address, AddressName string
	LabelIDs                   []int64
	// SetLabels distinguishes an omitted JSON field from an explicit replacement.
	// Creates always attach LabelIDs.
	SetLabels bool
}

type rosterGeocodeError struct{ err error }

func (e rosterGeocodeError) Error() string { return e.err.Error() }
func (e rosterGeocodeError) Unwrap() error { return e.err }

func (e rosterEditor) geocode(ctx context.Context, address string) (models.Coordinates, error) {
	result, err := e.geocoder.GeocodeWithRetry(ctx, address, 3)
	if err != nil {
		return models.Coordinates{}, rosterGeocodeError{err}
	}
	return result.Coords, nil
}

func (e rosterEditor) createParticipant(ctx context.Context, edit participantEdit) (*models.Participant, error) {
	coords, err := e.geocode(ctx, edit.Address)
	if err != nil {
		return nil, err
	}
	participant := &models.Participant{
		Name: edit.Name, Address: edit.Address, AddressName: edit.AddressName,
		Lat: coords.Lat, Lng: coords.Lng,
	}
	return e.db.Participants().CreateWithLabels(ctx, participant, edit.LabelIDs)
}

func (e rosterEditor) updateParticipant(ctx context.Context, existing *models.Participant, edit participantEdit) (*models.Participant, error) {
	participant := &models.Participant{
		ID: existing.ID, Name: edit.Name, Address: edit.Address, AddressName: edit.AddressName,
		Lat: existing.Lat, Lng: existing.Lng, CreatedAt: existing.CreatedAt,
	}
	if edit.Address != existing.Address {
		coords, err := e.geocode(ctx, edit.Address)
		if err != nil {
			return nil, err
		}
		participant.Lat, participant.Lng = coords.Lat, coords.Lng
	}
	if edit.SetLabels {
		return e.db.Participants().UpdateWithLabels(ctx, participant, edit.LabelIDs)
	}
	return e.db.Participants().Update(ctx, participant)
}

type driverEdit struct {
	participantEdit
	VehicleCapacity int
}

func (e rosterEditor) createDriver(ctx context.Context, edit driverEdit) (*models.Driver, error) {
	coords, err := e.geocode(ctx, edit.Address)
	if err != nil {
		return nil, err
	}
	driver := &models.Driver{
		Name: edit.Name, Address: edit.Address, AddressName: edit.AddressName,
		VehicleCapacity: edit.VehicleCapacity, Lat: coords.Lat, Lng: coords.Lng,
	}
	return e.db.Drivers().CreateWithLabels(ctx, driver, edit.LabelIDs)
}

func (e rosterEditor) updateDriver(ctx context.Context, existing *models.Driver, edit driverEdit) (*models.Driver, error) {
	driver := &models.Driver{
		ID: existing.ID, Name: edit.Name, Address: edit.Address, AddressName: edit.AddressName,
		VehicleCapacity: edit.VehicleCapacity, Lat: existing.Lat, Lng: existing.Lng,
		CreatedAt: existing.CreatedAt,
	}
	if edit.Address != existing.Address {
		coords, err := e.geocode(ctx, edit.Address)
		if err != nil {
			return nil, err
		}
		driver.Lat, driver.Lng = coords.Lat, coords.Lng
	}
	if edit.SetLabels {
		return e.db.Drivers().UpdateWithLabels(ctx, driver, edit.LabelIDs)
	}
	return e.db.Drivers().Update(ctx, driver)
}
