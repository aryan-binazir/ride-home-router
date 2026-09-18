package postgres_test

import (
	"context"
	"ride-home-router/internal/models"
	"ride-home-router/internal/postgres/postgrestest"
	"testing"
)

func TestParticipantAddressMatchRoundTrip(t *testing.T) {
	store := postgrestest.Open(t)
	ctx := context.Background()

	legacy, err := store.Participants().Create(ctx, &models.Participant{Name: "Legacy", Address: "1 Old Rd", Lat: 40, Lng: -73})
	if err != nil {
		t.Fatalf("Create() legacy error = %v", err)
	}
	got, err := store.Participants().GetByID(ctx, legacy.ID)
	if err != nil || got.AddressMatch != models.AddressMatchVerified || got.MatchedAddress != "" {
		t.Fatalf("blank status should default to verified: %#v, %v", got, err)
	}

	guessed, err := store.Participants().Create(ctx, &models.Participant{
		Name: "Guess", Address: "12 Oak St Apt 4, Raliegh", Lat: 35.7, Lng: -78.6,
		MatchedAddress: "12 Oak Street, Raleigh, NC 27601", AddressMatch: models.AddressMatchGuessed,
	})
	if err != nil {
		t.Fatalf("Create() guessed error = %v", err)
	}
	got, err = store.Participants().GetByID(ctx, guessed.ID)
	if err != nil || got.AddressMatch != models.AddressMatchGuessed || got.MatchedAddress != "12 Oak Street, Raleigh, NC 27601" {
		t.Fatalf("GetByID() = %#v, %v", got, err)
	}

	got.Address, got.AddressMatch = got.MatchedAddress, models.AddressMatchConfirmed
	if _, err := store.Participants().Update(ctx, got); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	list, err := store.Participants().List(ctx, "guess")
	if err != nil || len(list) != 1 || list[0].AddressMatch != models.AddressMatchConfirmed || list[0].Address != "12 Oak Street, Raleigh, NC 27601" {
		t.Fatalf("List() = %#v, %v", list, err)
	}

	// A re-import of the same name and address carries the new lookup's verdict.
	result, err := store.Participants().UpsertBatch(ctx, []*models.Participant{{
		Name: "Guess", Address: "12 Oak Street, Raleigh, NC 27601", Lat: 35.7, Lng: -78.6,
		MatchedAddress: "12 Oak Street, Raleigh, NC 27601", AddressMatch: models.AddressMatchVerified,
	}})
	if err != nil || result.Updated != 1 {
		t.Fatalf("UpsertBatch() = %#v, %v", result, err)
	}
	got, err = store.Participants().GetByID(ctx, guessed.ID)
	if err != nil || got.AddressMatch != models.AddressMatchVerified {
		t.Fatalf("GetByID() after import = %#v, %v", got, err)
	}
}

func TestDriverAddressMatchRoundTrip(t *testing.T) {
	store := postgrestest.Open(t)
	ctx := context.Background()

	driver, err := store.Drivers().Create(ctx, &models.Driver{
		Name: "Guess", Address: "5 Elm", Lat: 35.7, Lng: -78.6, VehicleCapacity: 4,
		MatchedAddress: "5 Elm St, Durham, NC 27701", AddressMatch: models.AddressMatchGuessed,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	got, err := store.Drivers().GetByID(ctx, driver.ID)
	if err != nil || got.AddressMatch != models.AddressMatchGuessed || got.MatchedAddress != "5 Elm St, Durham, NC 27701" {
		t.Fatalf("GetByID() = %#v, %v", got, err)
	}
	got.AddressMatch = models.AddressMatchConfirmed
	if _, err := store.Drivers().Update(ctx, got); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	result, err := store.Drivers().UpsertBatch(ctx, []*models.Driver{{
		Name: "Guess", Address: "5 Elm", Lat: 35.7, Lng: -78.6,
		MatchedAddress: "5 Elm St, Durham, NC 27701", AddressMatch: models.AddressMatchGuessed,
	}})
	if err != nil || result.Updated != 1 {
		t.Fatalf("UpsertBatch() = %#v, %v", result, err)
	}
	got, err = store.Drivers().GetByID(ctx, driver.ID)
	if err != nil || got.AddressMatch != models.AddressMatchGuessed || got.VehicleCapacity != 4 {
		t.Fatalf("GetByID() after import = %#v, %v", got, err)
	}
}
