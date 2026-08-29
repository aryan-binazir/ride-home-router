package postgres_test

import (
	"context"
	"ride-home-router/internal/database"
	"ride-home-router/internal/models"
	"ride-home-router/internal/postgres/postgrestest"
	"testing"
)

func TestSettingsActivityLocationsAndVehicles(t *testing.T) {
	store := postgrestest.Open(t)
	ctx := context.Background()

	settings, err := store.Settings().Get(ctx)
	if err != nil || !settings.UseMiles || settings.SelectedActivityLocationID != 0 {
		t.Fatalf("default Settings().Get() = %#v, %v; want miles and no location", settings, err)
	}

	location, err := store.ActivityLocations().Create(ctx, &models.ActivityLocation{Name: "Gym", Address: "1 Gym St", Lat: 35, Lng: -79})
	if err != nil || location.ID == 0 {
		t.Fatalf("ActivityLocations().Create() = %#v, %v", location, err)
	}
	if err := store.Settings().Update(ctx, &models.Settings{SelectedActivityLocationID: location.ID, UseMiles: false, SMEEmail: "sme@example.com"}); err != nil {
		t.Fatalf("Settings().Update() error = %v", err)
	}
	settings, err = store.Settings().Get(ctx)
	if err != nil || settings.UseMiles || settings.SelectedActivityLocationID != location.ID || settings.SMEEmail != "sme@example.com" {
		t.Fatalf("Settings().Get() = %#v, %v", settings, err)
	}

	location.Name = "Main Gym"
	if _, err := store.ActivityLocations().Update(ctx, location); err != nil {
		t.Fatalf("ActivityLocations().Update() error = %v", err)
	}
	locations, err := store.ActivityLocations().List(ctx)
	if err != nil || len(locations) != 1 || locations[0].Name != "Main Gym" {
		t.Fatalf("ActivityLocations().List() = %#v, %v", locations, err)
	}

	if err := store.ActivityLocations().Delete(ctx, location.ID); err != nil {
		t.Fatalf("ActivityLocations().Delete() error = %v", err)
	}
	if _, err := store.ActivityLocations().GetByID(ctx, location.ID); err != database.ErrNotFound {
		t.Fatalf("GetByID() after delete error = %v, want ErrNotFound", err)
	}
	settings, err = store.Settings().Get(ctx)
	if err != nil || settings.SelectedActivityLocationID != 0 {
		t.Fatalf("Settings().Get() after location delete = %#v, %v; want cleared selection", settings, err)
	}

	vehicle, err := store.OrganizationVehicles().Create(ctx, &models.OrganizationVehicle{Name: "Van A", Capacity: 12})
	if err != nil || vehicle.ID == 0 {
		t.Fatalf("OrganizationVehicles().Create() = %#v, %v", vehicle, err)
	}
	vehicle.Capacity = 14
	if _, err := store.OrganizationVehicles().Update(ctx, vehicle); err != nil {
		t.Fatalf("OrganizationVehicles().Update() error = %v", err)
	}
	vehicles, err := store.OrganizationVehicles().GetByIDs(ctx, []int64{vehicle.ID, 9999})
	if err != nil || len(vehicles) != 1 || vehicles[0].Capacity != 14 {
		t.Fatalf("OrganizationVehicles().GetByIDs() = %#v, %v", vehicles, err)
	}
	if err := store.OrganizationVehicles().Delete(ctx, vehicle.ID); err != nil {
		t.Fatalf("OrganizationVehicles().Delete() error = %v", err)
	}
	if err := store.OrganizationVehicles().Delete(ctx, vehicle.ID); err != database.ErrNotFound {
		t.Fatalf("second Delete() error = %v, want ErrNotFound", err)
	}
	if err := store.HealthCheck(ctx); err != nil {
		t.Fatalf("HealthCheck() error = %v", err)
	}
}
