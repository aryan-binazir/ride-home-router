package handlers

import (
	"context"
	"fmt"
	"sync"
	"time"

	"ride-home-router/internal/database"
	"ride-home-router/internal/geocoding"
	"ride-home-router/internal/models"
)

type rosterEditor struct {
	db       database.DataStore
	geocoder geocoding.Geocoder
}

type participantEdit struct {
	Name, Address, AddressName string
	LabelIDs                   []int64
	SetLabels                  bool
}

var rosterCreateMu sync.Mutex

type rosterDuplicateError struct{ name string }

func (e rosterDuplicateError) Error() string {
	return fmt.Sprintf("%s at that address is already in the roster.", e.name)
}

type rosterGeocodeError struct{ err error }

func (e rosterGeocodeError) Error() string { return e.err.Error() }
func (e rosterGeocodeError) Unwrap() error { return e.err }

func (e rosterEditor) geocode(ctx context.Context, address string) (*geocoding.GeocodingResult, error) {
	result, err := e.geocoder.GeocodeWithRetry(ctx, address, 3)
	if err != nil {
		return nil, rosterGeocodeError{err}
	}
	return result, nil
}

func (e rosterEditor) createParticipant(ctx context.Context, edit participantEdit) (*models.Participant, error) {
	result, err := e.geocode(ctx, edit.Address)
	if err != nil {
		return nil, err
	}
	participant := &models.Participant{
		Name: edit.Name, Address: edit.Address, AddressName: edit.AddressName,
		Lat: result.Coords.Lat, Lng: result.Coords.Lng, GeocodedAt: time.Now(),
		MatchedAddress: result.FormattedAddress, AddressMatch: models.AddressMatchFor(result.Guessed),
	}
	rosterCreateMu.Lock()
	defer rosterCreateMu.Unlock()
	existing, err := e.db.Participants().List(ctx, "")
	if err != nil {
		return nil, err
	}
	key := models.RosterKey(edit.Name, edit.Address)
	for _, row := range existing {
		if key != "" && models.RosterKey(row.Name, row.Address) == key {
			return nil, rosterDuplicateError{edit.Name}
		}
	}
	return e.db.Participants().CreateWithLabels(ctx, participant, edit.LabelIDs)
}

func (e rosterEditor) updateParticipant(ctx context.Context, existing *models.Participant, edit participantEdit) (*models.Participant, error) {
	participant := &models.Participant{
		ID: existing.ID, Name: edit.Name, Address: edit.Address, AddressName: edit.AddressName,
		Lat: existing.Lat, Lng: existing.Lng, CreatedAt: existing.CreatedAt, GeocodedAt: existing.GeocodedAt,
		MatchedAddress: existing.MatchedAddress, AddressMatch: existing.AddressMatch,
	}
	if edit.Address != existing.Address {
		result, err := e.geocode(ctx, edit.Address)
		if err != nil {
			return nil, err
		}
		participant.Lat, participant.Lng = result.Coords.Lat, result.Coords.Lng
		participant.GeocodedAt = time.Now()
		participant.MatchedAddress, participant.AddressMatch = result.FormattedAddress, models.AddressMatchFor(result.Guessed)
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
	result, err := e.geocode(ctx, edit.Address)
	if err != nil {
		return nil, err
	}
	driver := &models.Driver{
		Name: edit.Name, Address: edit.Address, AddressName: edit.AddressName,
		VehicleCapacity: edit.VehicleCapacity, Lat: result.Coords.Lat, Lng: result.Coords.Lng, GeocodedAt: time.Now(),
		MatchedAddress: result.FormattedAddress, AddressMatch: models.AddressMatchFor(result.Guessed),
	}
	rosterCreateMu.Lock()
	defer rosterCreateMu.Unlock()
	existing, err := e.db.Drivers().List(ctx, "")
	if err != nil {
		return nil, err
	}
	key := models.RosterKey(edit.Name, edit.Address)
	for _, row := range existing {
		if key != "" && models.RosterKey(row.Name, row.Address) == key {
			return nil, rosterDuplicateError{edit.Name}
		}
	}
	return e.db.Drivers().CreateWithLabels(ctx, driver, edit.LabelIDs)
}

func (e rosterEditor) updateDriver(ctx context.Context, existing *models.Driver, edit driverEdit) (*models.Driver, error) {
	driver := &models.Driver{
		ID: existing.ID, Name: edit.Name, Address: edit.Address, AddressName: edit.AddressName,
		VehicleCapacity: edit.VehicleCapacity, Lat: existing.Lat, Lng: existing.Lng,
		CreatedAt: existing.CreatedAt, GeocodedAt: existing.GeocodedAt,
		MatchedAddress: existing.MatchedAddress, AddressMatch: existing.AddressMatch,
	}
	if edit.Address != existing.Address {
		result, err := e.geocode(ctx, edit.Address)
		if err != nil {
			return nil, err
		}
		driver.Lat, driver.Lng = result.Coords.Lat, result.Coords.Lng
		driver.GeocodedAt = time.Now()
		driver.MatchedAddress, driver.AddressMatch = result.FormattedAddress, models.AddressMatchFor(result.Guessed)
	}
	if edit.SetLabels {
		return e.db.Drivers().UpdateWithLabels(ctx, driver, edit.LabelIDs)
	}
	return e.db.Drivers().Update(ctx, driver)
}
