package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"ride-home-router/internal/importer"
	"ride-home-router/internal/models"
	"strconv"
	"strings"
	"testing"
)

func TestHandleCreateDriver_JSONRejectsCapacityAboveImportLimit(t *testing.T) {
	handler, store := newTestManagementHandler(t)
	body := `{"name":"Driver One","address":"1 Driver Way","vehicle_capacity":` + strconv.Itoa(importer.MaxCapacity+1) + `}`
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/drivers", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handler.HandleCreateDriver(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d body=%q", rr.Code, http.StatusBadRequest, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), messageVehicleCapacityOutOfRange()) {
		t.Fatalf("body = %q, want %q", rr.Body.String(), messageVehicleCapacityOutOfRange())
	}
	drivers, err := store.Drivers().List(context.Background(), "")
	if err != nil {
		t.Fatalf("list drivers: %v", err)
	}
	if len(drivers) != 0 {
		t.Fatalf("drivers = %#v, want none after validation failure", drivers)
	}
}

func TestHandleUpdateDriver_JSONRejectsCapacityAboveImportLimit(t *testing.T) {
	handler, store := newTestManagementHandler(t)
	driver, err := store.Drivers().Create(context.Background(), &models.Driver{
		Name:            "Driver One",
		Address:         "1 Driver Way",
		Lat:             40.1,
		Lng:             -73.9,
		VehicleCapacity: 4,
	})
	if err != nil {
		t.Fatalf("create driver: %v", err)
	}
	body := `{"name":"Changed","address":"1 Driver Way","vehicle_capacity":` + strconv.Itoa(importer.MaxCapacity+1) + `}`
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPut, "/api/v1/drivers/"+strconv.FormatInt(driver.ID, 10), strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handler.HandleUpdateDriver(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d body=%q", rr.Code, http.StatusBadRequest, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), messageVehicleCapacityOutOfRange()) {
		t.Fatalf("body = %q, want %q", rr.Body.String(), messageVehicleCapacityOutOfRange())
	}
	unchanged, err := store.Drivers().GetByID(context.Background(), driver.ID)
	if err != nil {
		t.Fatalf("get driver: %v", err)
	}
	if unchanged.Name != driver.Name || unchanged.VehicleCapacity != driver.VehicleCapacity {
		t.Fatalf("driver = %#v, want original name and capacity", unchanged)
	}
}
