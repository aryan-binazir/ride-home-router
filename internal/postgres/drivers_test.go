package postgres_test

import (
	"context"
	"errors"
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
	if _, err := store.Drivers().Update(ctx, driver); !errors.Is(err, database.ErrNotFound) {
		t.Fatalf("Update() after delete error = %v, want ErrNotFound", err)
	}
}

func TestDriverRepositorySoftDeleteAndRestore(t *testing.T) {
	store := postgrestest.Open(t)
	ctx := context.Background()
	label, err := store.Labels().Create(ctx, &models.Label{Name: "Retained"})
	if err != nil {
		t.Fatalf("create label: %v", err)
	}
	driver, err := store.Drivers().CreateWithLabels(ctx, &models.Driver{
		Name: "Archived Driver", Address: "1 Archive Way", Lat: 40, Lng: -73, VehicleCapacity: 4,
	}, []int64{label.ID})
	if err != nil {
		t.Fatalf("CreateWithLabels() error = %v", err)
	}
	if err := store.Drivers().Restore(ctx, driver.ID); !errors.Is(err, database.ErrNotFound) {
		t.Fatalf("Restore() live row error = %v, want ErrNotFound", err)
	}
	if err := store.Drivers().Delete(ctx, driver.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if err := store.Drivers().Delete(ctx, driver.ID); !errors.Is(err, database.ErrNotFound) {
		t.Fatalf("second Delete() error = %v, want ErrNotFound", err)
	}

	for _, search := range []string{"", "Archived"} {
		drivers, err := store.Drivers().List(ctx, search)
		if err != nil || len(drivers) != 0 {
			t.Fatalf("List(%q) = %#v, %v; want no archived rows", search, drivers, err)
		}
	}
	if _, err := store.Drivers().GetByID(ctx, driver.ID); !errors.Is(err, database.ErrNotFound) {
		t.Fatalf("GetByID() archived row error = %v, want ErrNotFound", err)
	}
	if drivers, err := store.Drivers().GetByIDs(ctx, []int64{driver.ID}); err != nil || len(drivers) != 0 {
		t.Fatalf("GetByIDs() archived row = %#v, %v; want none", drivers, err)
	}
	deleted, err := store.Drivers().ListDeleted(ctx)
	if err != nil || len(deleted) != 1 || deleted[0].ID != driver.ID || deleted[0].DeletedAt == nil {
		t.Fatalf("ListDeleted() = %#v, %v; want archived row with DeletedAt", deleted, err)
	}

	driver.Name = "Should Not Change"
	if _, err := store.Drivers().UpdateWithLabels(ctx, driver, nil); !errors.Is(err, database.ErrNotFound) {
		t.Fatalf("UpdateWithLabels() archived row error = %v, want ErrNotFound", err)
	}
	if err := store.Drivers().Restore(ctx, driver.ID); err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	restored, err := store.Drivers().GetByID(ctx, driver.ID)
	if err != nil || restored.Name != "Archived Driver" || restored.DeletedAt != nil {
		t.Fatalf("GetByID() restored row = %#v, %v", restored, err)
	}
	labels, err := store.Labels().ListLabelsForDriver(ctx, driver.ID)
	if err != nil || len(labels) != 1 || labels[0].ID != label.ID {
		t.Fatalf("restored labels = %#v, %v; want retained label", labels, err)
	}
	if deleted, err := store.Drivers().ListDeleted(ctx); err != nil || len(deleted) != 0 {
		t.Fatalf("ListDeleted() after restore = %#v, %v; want none", deleted, err)
	}
}

func TestDriverRepositoryRestoreAllowsLiveDuplicate(t *testing.T) {
	store := postgrestest.Open(t)
	ctx := context.Background()
	driver, err := store.Drivers().Create(ctx, &models.Driver{
		Name: "Archived Driver", Address: "1 Archive Way", Lat: 40, Lng: -73, VehicleCapacity: 4,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := store.Drivers().Delete(ctx, driver.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	imported := &models.Driver{
		Name: "  ARCHIVED driver ", Address: "1  Archive Way", Lat: 41, Lng: -72, VehicleCapacity: 6,
	}
	result, err := store.Drivers().UpsertBatch(ctx, []*models.Driver{imported})
	if err != nil || result.Created != 1 {
		t.Fatalf("UpsertBatch() = %#v, %v; want one imported live duplicate", result, err)
	}
	if err := store.Drivers().Restore(ctx, driver.ID); err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	live, err := store.Drivers().List(ctx, "")
	if err != nil || len(live) != 2 {
		t.Fatalf("List() = %#v, %v; want imported and restored rows live", live, err)
	}
	for _, id := range []int64{driver.ID, imported.ID} {
		if _, err := store.Drivers().GetByID(ctx, id); err != nil {
			t.Fatalf("GetByID(%d) error = %v; want live row", id, err)
		}
	}
	if deleted, err := store.Drivers().ListDeleted(ctx); err != nil || len(deleted) != 0 {
		t.Fatalf("ListDeleted() = %#v, %v; want none", deleted, err)
	}
}
