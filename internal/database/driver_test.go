package database

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"ride-home-router/internal/models"
)

func TestDriverCreate(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	d := &models.Driver{
		Name:            "John Driver",
		Address:         "789 Drive Ln, Chicago, IL",
		Lat:             41.8781,
		Lng:             -87.6298,
		VehicleCapacity: 4,
	}

	created, err := db.DriverRepository.Create(ctx, d)
	require.NoError(t, err)
	assert.NotNil(t, created)
	assert.NotZero(t, created.ID)
	assert.Equal(t, "John Driver", created.Name)
	assert.Equal(t, 4, created.VehicleCapacity)
	assert.False(t, created.IsInstituteVehicle)
	assert.NotZero(t, created.CreatedAt)
	assert.NotZero(t, created.UpdatedAt)
}

func TestDriverCreateInstituteVehicle(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	d := &models.Driver{
		Name:               "Institute Van",
		Address:            "School Parking Lot",
		Lat:                40.0,
		Lng:                -75.0,
		VehicleCapacity:    8,
		IsInstituteVehicle: true,
	}

	created, err := db.DriverRepository.Create(ctx, d)
	require.NoError(t, err)
	assert.NotNil(t, created)
	assert.True(t, created.IsInstituteVehicle)
}

func TestDriverOnlyOneInstituteVehicle(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	// Create first institute vehicle
	d1 := &models.Driver{
		Name:               "Institute Van 1",
		Address:            "Lot 1",
		Lat:                40.0,
		Lng:                -75.0,
		VehicleCapacity:    8,
		IsInstituteVehicle: true,
	}
	_, err := db.DriverRepository.Create(ctx, d1)
	require.NoError(t, err)

	// Attempt to create second institute vehicle (should fail due to unique index)
	d2 := &models.Driver{
		Name:               "Institute Van 2",
		Address:            "Lot 2",
		Lat:                41.0,
		Lng:                -76.0,
		VehicleCapacity:    8,
		IsInstituteVehicle: true,
	}
	_, err = db.DriverRepository.Create(ctx, d2)
	assert.Error(t, err)
}

func TestDriverGetByID(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	d := &models.Driver{
		Name: "Jane Driver", Address: "456 Road St", Lat: 42.0, Lng: -76.0, VehicleCapacity: 5,
	}
	created, err := db.DriverRepository.Create(ctx, d)
	require.NoError(t, err)

	found, err := db.DriverRepository.GetByID(ctx, created.ID)
	require.NoError(t, err)
	assert.NotNil(t, found)
	assert.Equal(t, created.ID, found.ID)
	assert.Equal(t, "Jane Driver", found.Name)
	assert.Equal(t, 5, found.VehicleCapacity)
}

func TestDriverGetByIDNotFound(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	found, err := db.DriverRepository.GetByID(ctx, 99999)
	require.NoError(t, err)
	assert.Nil(t, found)
}

func TestDriverGetByIDs(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	d1, _ := db.DriverRepository.Create(ctx, &models.Driver{
		Name: "Driver A", Address: "Addr1", Lat: 40.0, Lng: -75.0, VehicleCapacity: 4,
	})
	_, _ = db.DriverRepository.Create(ctx, &models.Driver{
		Name: "Driver B", Address: "Addr2", Lat: 41.0, Lng: -76.0, VehicleCapacity: 4,
	})
	d3, _ := db.DriverRepository.Create(ctx, &models.Driver{
		Name: "Driver C", Address: "Addr3", Lat: 42.0, Lng: -77.0, VehicleCapacity: 4,
	})

	found, err := db.DriverRepository.GetByIDs(ctx, []int64{d1.ID, d3.ID})
	require.NoError(t, err)
	assert.Len(t, found, 2)

	names := []string{found[0].Name, found[1].Name}
	assert.Contains(t, names, "Driver A")
	assert.Contains(t, names, "Driver C")
}

func TestDriverGetByIDsEmpty(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	found, err := db.DriverRepository.GetByIDs(ctx, []int64{})
	require.NoError(t, err)
	assert.Empty(t, found)
}

func TestDriverList(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	db.DriverRepository.Create(ctx, &models.Driver{
		Name: "Alice Driver", Address: "Addr1", Lat: 40.0, Lng: -75.0, VehicleCapacity: 4,
	})
	db.DriverRepository.Create(ctx, &models.Driver{
		Name: "Bob Driver", Address: "Addr2", Lat: 41.0, Lng: -76.0, VehicleCapacity: 5,
	})
	db.DriverRepository.Create(ctx, &models.Driver{
		Name: "Charlie Driver", Address: "Addr3", Lat: 42.0, Lng: -77.0, VehicleCapacity: 6,
	})

	drivers, err := db.DriverRepository.List(ctx, "")
	require.NoError(t, err)
	assert.Len(t, drivers, 3)

	// Results should be ordered by name
	assert.Equal(t, "Alice Driver", drivers[0].Name)
	assert.Equal(t, "Bob Driver", drivers[1].Name)
	assert.Equal(t, "Charlie Driver", drivers[2].Name)
}

func TestDriverListWithSearch(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	db.DriverRepository.Create(ctx, &models.Driver{
		Name: "Alice Anderson", Address: "Addr1", Lat: 40.0, Lng: -75.0, VehicleCapacity: 4,
	})
	db.DriverRepository.Create(ctx, &models.Driver{
		Name: "Bob Brown", Address: "Addr2", Lat: 41.0, Lng: -76.0, VehicleCapacity: 5,
	})
	db.DriverRepository.Create(ctx, &models.Driver{
		Name: "Alice Baker", Address: "Addr3", Lat: 42.0, Lng: -77.0, VehicleCapacity: 6,
	})

	drivers, err := db.DriverRepository.List(ctx, "Alice")
	require.NoError(t, err)
	assert.Len(t, drivers, 2)
	assert.Contains(t, drivers[0].Name, "Alice")
	assert.Contains(t, drivers[1].Name, "Alice")
}

func TestDriverGetInstituteVehicle(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	// Initially, no institute vehicle
	vehicle, err := db.DriverRepository.GetInstituteVehicle(ctx)
	require.NoError(t, err)
	assert.Nil(t, vehicle)

	// Create a regular driver
	db.DriverRepository.Create(ctx, &models.Driver{
		Name: "Regular Driver", Address: "Addr1", Lat: 40.0, Lng: -75.0, VehicleCapacity: 4,
	})

	// Still no institute vehicle
	vehicle, err = db.DriverRepository.GetInstituteVehicle(ctx)
	require.NoError(t, err)
	assert.Nil(t, vehicle)

	// Create institute vehicle
	institute, _ := db.DriverRepository.Create(ctx, &models.Driver{
		Name:               "Institute Van",
		Address:            "School",
		Lat:                41.0,
		Lng:                -76.0,
		VehicleCapacity:    8,
		IsInstituteVehicle: true,
	})

	// Now we should find it
	vehicle, err = db.DriverRepository.GetInstituteVehicle(ctx)
	require.NoError(t, err)
	assert.NotNil(t, vehicle)
	assert.Equal(t, institute.ID, vehicle.ID)
	assert.True(t, vehicle.IsInstituteVehicle)
}

func TestDriverUpdate(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	d, err := db.DriverRepository.Create(ctx, &models.Driver{
		Name: "Original Driver", Address: "Original Addr", Lat: 40.0, Lng: -75.0, VehicleCapacity: 4,
	})
	require.NoError(t, err)
	originalUpdatedAt := d.UpdatedAt

	d.Name = "Updated Driver"
	d.Address = "Updated Addr"
	d.VehicleCapacity = 6
	d.Lat = 41.0
	d.Lng = -76.0

	updated, err := db.DriverRepository.Update(ctx, d)
	require.NoError(t, err)
	assert.NotNil(t, updated)
	assert.Equal(t, d.ID, updated.ID)
	assert.Equal(t, "Updated Driver", updated.Name)
	assert.Equal(t, 6, updated.VehicleCapacity)
	assert.True(t, updated.UpdatedAt.After(originalUpdatedAt) || updated.UpdatedAt.Equal(originalUpdatedAt))

	found, err := db.DriverRepository.GetByID(ctx, d.ID)
	require.NoError(t, err)
	assert.Equal(t, "Updated Driver", found.Name)
	assert.Equal(t, 6, found.VehicleCapacity)
}

func TestDriverUpdateNotFound(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	d := &models.Driver{
		ID:              99999,
		Name:            "Non-existent",
		Address:         "Nowhere",
		Lat:             0.0,
		Lng:             0.0,
		VehicleCapacity: 4,
	}

	updated, err := db.DriverRepository.Update(ctx, d)
	require.NoError(t, err)
	assert.Nil(t, updated)
}

func TestDriverDelete(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	d, err := db.DriverRepository.Create(ctx, &models.Driver{
		Name: "To Delete", Address: "Delete Me", Lat: 40.0, Lng: -75.0, VehicleCapacity: 4,
	})
	require.NoError(t, err)

	err = db.DriverRepository.Delete(ctx, d.ID)
	require.NoError(t, err)

	found, err := db.DriverRepository.GetByID(ctx, d.ID)
	require.NoError(t, err)
	assert.Nil(t, found)
}

func TestDriverDeleteNotFound(t *testing.T) {
	db := setupTestDB(t)
	ctx := context.Background()

	err := db.DriverRepository.Delete(ctx, 99999)
	assert.Error(t, err)
	assert.Equal(t, sql.ErrNoRows, err)
}
