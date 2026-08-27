package postgres_test

import (
	"context"
	"ride-home-router/internal/database"
	"ride-home-router/internal/models"
	"ride-home-router/internal/postgres/postgrestest"
	"testing"
)

func TestDriverRepositoryRoundTrip(t *testing.T) {
	store := postgrestest.Open(t)
	ctx := context.Background()

	driver, err := store.Drivers().CreateWithLabels(ctx, &models.Driver{
		Name: "Driver One", Address: "200 Driver Lane", AddressName: "North Campus",
		Lat: 40.2, Lng: -73.8, VehicleCapacity: 4,
	}, nil)
	if err != nil {
		t.Fatalf("CreateWithLabels() error = %v", err)
	}
	if driver.ID == 0 || driver.AddressName != "North Campus" {
		t.Fatalf("created driver = %#v", driver)
	}

	got, err := store.Drivers().GetByID(ctx, driver.ID)
	if err != nil || got.AddressName != "North Campus" || got.VehicleCapacity != 4 {
		t.Fatalf("GetByID() = %#v, %v", got, err)
	}
	drivers, err := store.Drivers().List(ctx, "DRIVER")
	if err != nil || len(drivers) != 1 || drivers[0].AddressName != "North Campus" {
		t.Fatalf("List() = %#v, %v; want case-insensitive match", drivers, err)
	}

	got.AddressName = "South Campus"
	got.VehicleCapacity = 6
	if _, err := store.Drivers().Update(ctx, got); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	updated, err := store.Drivers().GetByID(ctx, driver.ID)
	if err != nil || updated.AddressName != "South Campus" || updated.VehicleCapacity != 6 {
		t.Fatalf("GetByID() after update = %#v, %v", updated, err)
	}

	byIDs, err := store.Drivers().GetByIDs(ctx, []int64{driver.ID, 9999})
	if err != nil || len(byIDs) != 1 {
		t.Fatalf("GetByIDs() = %#v, %v; want 1", byIDs, err)
	}
	if err := store.Drivers().Delete(ctx, driver.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, err := store.Drivers().GetByID(ctx, driver.ID); err != database.ErrNotFound {
		t.Fatalf("GetByID() after delete error = %v, want ErrNotFound", err)
	}
}
