package postgres_test

import (
	"context"
	"errors"
	"fmt"
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
	if locations, err := store.ActivityLocations().List(ctx); err != nil || len(locations) != 0 {
		t.Fatalf("List() after delete = %#v, %v; want none", locations, err)
	}
	deleted, err := store.ActivityLocations().ListDeleted(ctx)
	if err != nil || len(deleted) != 1 || deleted[0].ID != location.ID || deleted[0].DeletedAt == nil {
		t.Fatalf("ListDeleted() = %#v, %v; want archived location", deleted, err)
	}
	location.Name = "Should Not Change"
	if _, err := store.ActivityLocations().Update(ctx, location); !errors.Is(err, database.ErrNotFound) {
		t.Fatalf("Update() archived location error = %v, want ErrNotFound", err)
	}
	if err := store.ActivityLocations().Delete(ctx, location.ID); !errors.Is(err, database.ErrNotFound) {
		t.Fatalf("second Delete() error = %v, want ErrNotFound", err)
	}
	if err := store.Settings().Update(ctx, &models.Settings{SelectedActivityLocationID: location.ID, UseMiles: true}); !errors.Is(err, database.ErrNotFound) {
		t.Fatalf("Settings().Update() archived location error = %v, want ErrNotFound", err)
	}
	settings, err = store.Settings().Get(ctx)
	if err != nil || settings.SelectedActivityLocationID != 0 {
		t.Fatalf("Settings().Get() after location delete = %#v, %v; want cleared selection", settings, err)
	}
	if err := store.ActivityLocations().Restore(ctx, location.ID); err != nil {
		t.Fatalf("ActivityLocations().Restore() error = %v", err)
	}
	if err := store.ActivityLocations().Restore(ctx, location.ID); !errors.Is(err, database.ErrNotFound) {
		t.Fatalf("second Restore() error = %v, want ErrNotFound", err)
	}
	restored, err := store.ActivityLocations().GetByID(ctx, location.ID)
	if err != nil || restored.Name != "Main Gym" || restored.DeletedAt != nil {
		t.Fatalf("GetByID() restored location = %#v, %v", restored, err)
	}
	settings, err = store.Settings().Get(ctx)
	if err != nil || settings.SelectedActivityLocationID != 0 {
		t.Fatalf("Settings().Get() after restore = %#v, %v; want location to stay unselected", settings, err)
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

func TestSettingsGetHidesDirectlyArchivedSelectedLocation(t *testing.T) {
	databaseURL := postgrestest.DatabaseURL(t)
	store := openStore(t, databaseURL)
	ctx := context.Background()
	location, err := store.ActivityLocations().Create(ctx, &models.ActivityLocation{Name: "Gym", Address: "1 Gym St", Lat: 35, Lng: -79})
	if err != nil {
		t.Fatalf("create activity location: %v", err)
	}
	if err := store.Settings().Update(ctx, &models.Settings{SelectedActivityLocationID: location.ID, UseMiles: true}); err != nil {
		t.Fatalf("Settings().Update() error = %v", err)
	}
	execSQL(t, databaseURL, fmt.Sprintf(`UPDATE activity_locations SET deleted_at = now() WHERE id = %d`, location.ID))

	settings, err := store.Settings().Get(ctx)
	if err != nil || settings.SelectedActivityLocationID != 0 {
		t.Fatalf("Settings().Get() with archived reference = %#v, %v; want unset", settings, err)
	}
}
