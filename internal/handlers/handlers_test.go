package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"ride-home-router/internal/database"
	"ride-home-router/internal/distance"
	"ride-home-router/internal/geocoding"
	"ride-home-router/internal/models"
	"ride-home-router/internal/routing"
)

// Mock implementations for testing

type mockGeocoder struct{}

func (m *mockGeocoder) Geocode(ctx context.Context, address string) (*geocoding.GeocodingResult, error) {
	return &geocoding.GeocodingResult{
		Coords:      models.Coordinates{Lat: 40.7128, Lng: -74.0060},
		DisplayName: address,
	}, nil
}

func (m *mockGeocoder) GeocodeWithRetry(ctx context.Context, address string, maxRetries int) (*geocoding.GeocodingResult, error) {
	return m.Geocode(ctx, address)
}

type mockDistanceCalculator struct{}

func (m *mockDistanceCalculator) GetDistance(ctx context.Context, origin, dest models.Coordinates) (*distance.DistanceResult, error) {
	return &distance.DistanceResult{
		DistanceMeters: 1000.0,
		DurationSecs:   100.0,
	}, nil
}

func (m *mockDistanceCalculator) GetDistanceMatrix(ctx context.Context, points []models.Coordinates) ([][]distance.DistanceResult, error) {
	n := len(points)
	matrix := make([][]distance.DistanceResult, n)
	for i := range matrix {
		matrix[i] = make([]distance.DistanceResult, n)
		for j := range matrix[i] {
			if i == j {
				matrix[i][j] = distance.DistanceResult{DistanceMeters: 0, DurationSecs: 0}
			} else {
				matrix[i][j] = distance.DistanceResult{DistanceMeters: 1000.0, DurationSecs: 100.0}
			}
		}
	}
	return matrix, nil
}

func (m *mockDistanceCalculator) GetDistancesFromPoint(ctx context.Context, origin models.Coordinates, destinations []models.Coordinates) ([]distance.DistanceResult, error) {
	results := make([]distance.DistanceResult, len(destinations))
	for i := range results {
		results[i] = distance.DistanceResult{DistanceMeters: 1000.0, DurationSecs: 100.0}
	}
	return results, nil
}

func (m *mockDistanceCalculator) PrewarmCache(ctx context.Context, points []models.Coordinates) error {
	return nil
}

type mockRouter struct {
	shouldFail bool
}

func (m *mockRouter) CalculateRoutes(ctx context.Context, req *routing.RoutingRequest) (*models.RoutingResult, error) {
	if m.shouldFail {
		return nil, &routing.ErrRoutingFailed{
			Reason:            "Insufficient capacity",
			UnassignedCount:   1,
			TotalCapacity:     2,
			TotalParticipants: 3,
		}
	}

	routes := []models.CalculatedRoute{
		{
			Driver: &req.Drivers[0],
			Stops: []models.RouteStop{
				{
					Order:                    0,
					Participant:              &req.Participants[0],
					DistanceFromPrevMeters:   1000.0,
					CumulativeDistanceMeters: 1000.0,
				},
			},
			TotalDropoffDistanceMeters: 1000.0,
			DistanceToDriverHomeMeters: 500.0,
		},
	}

	return &models.RoutingResult{
		Routes: routes,
		Summary: models.RoutingSummary{
			TotalParticipants:          len(req.Participants),
			TotalDriversUsed:           1,
			TotalDropoffDistanceMeters: 1000.0,
		},
	}, nil
}

func setupTestHandler(t *testing.T) *Handler {
	db, err := database.New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	return &Handler{
		DB:           db,
		Geocoder:     &mockGeocoder{},
		DistanceCalc: &mockDistanceCalculator{},
		Router:       &mockRouter{},
	}
}

func TestIsHTMX(t *testing.T) {
	h := &Handler{}

	tests := []struct {
		name     string
		header   string
		expected bool
	}{
		{"HTMX request", "true", true},
		{"Non-HTMX request", "", false},
		{"Invalid value", "false", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			if tt.header != "" {
				req.Header.Set("HX-Request", tt.header)
			}
			assert.Equal(t, tt.expected, h.isHTMX(req))
		})
	}
}

func TestHandleListParticipants(t *testing.T) {
	h := setupTestHandler(t)

	// Create test participants
	h.DB.Participants().Create(context.Background(), &models.Participant{
		Name: "Alice", Address: "123 Main St", Lat: 40.0, Lng: -75.0,
	})
	h.DB.Participants().Create(context.Background(), &models.Participant{
		Name: "Bob", Address: "456 Oak St", Lat: 41.0, Lng: -76.0,
	})

	req := httptest.NewRequest("GET", "/api/v1/participants", nil)
	w := httptest.NewRecorder()

	h.HandleListParticipants(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response ParticipantListResponse
	err := json.NewDecoder(w.Body).Decode(&response)
	require.NoError(t, err)

	assert.Equal(t, 2, response.Total)
	assert.Len(t, response.Participants, 2)
}

func TestHandleListParticipantsWithSearch(t *testing.T) {
	h := setupTestHandler(t)

	h.DB.Participants().Create(context.Background(), &models.Participant{
		Name: "Alice", Address: "123 Main St", Lat: 40.0, Lng: -75.0,
	})
	h.DB.Participants().Create(context.Background(), &models.Participant{
		Name: "Bob", Address: "456 Oak St", Lat: 41.0, Lng: -76.0,
	})

	req := httptest.NewRequest("GET", "/api/v1/participants?search=Alice", nil)
	w := httptest.NewRecorder()

	h.HandleListParticipants(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response ParticipantListResponse
	err := json.NewDecoder(w.Body).Decode(&response)
	require.NoError(t, err)

	assert.Equal(t, 1, response.Total)
	assert.Contains(t, response.Participants[0].Name, "Alice")
}

func TestHandleGetParticipant(t *testing.T) {
	h := setupTestHandler(t)

	created, _ := h.DB.Participants().Create(context.Background(), &models.Participant{
		Name: "Alice", Address: "123 Main St", Lat: 40.0, Lng: -75.0,
	})

	req := httptest.NewRequest("GET", fmt.Sprintf("/api/v1/participants/%d", created.ID), nil)
	w := httptest.NewRecorder()

	h.HandleGetParticipant(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var participant models.Participant
	err := json.NewDecoder(w.Body).Decode(&participant)
	require.NoError(t, err)

	assert.Equal(t, created.ID, participant.ID)
	assert.Equal(t, "Alice", participant.Name)
}

func TestHandleGetParticipantNotFound(t *testing.T) {
	h := setupTestHandler(t)

	req := httptest.NewRequest("GET", "/api/v1/participants/99999", nil)
	w := httptest.NewRecorder()

	h.HandleGetParticipant(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)

	var response ErrorResponse
	err := json.NewDecoder(w.Body).Decode(&response)
	require.NoError(t, err)

	assert.Equal(t, "NOT_FOUND", response.Error.Code)
}

func TestHandleGetParticipantInvalidID(t *testing.T) {
	h := setupTestHandler(t)

	req := httptest.NewRequest("GET", "/api/v1/participants/invalid", nil)
	w := httptest.NewRecorder()

	h.HandleGetParticipant(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)

	var response ErrorResponse
	err := json.NewDecoder(w.Body).Decode(&response)
	require.NoError(t, err)

	assert.Equal(t, "VALIDATION_ERROR", response.Error.Code)
}

func TestHandleCreateParticipant(t *testing.T) {
	h := setupTestHandler(t)

	// Set up settings first
	h.DB.Settings().Update(context.Background(), &models.Settings{
		InstituteAddress: "Test Institute",
		InstituteLat:     40.0,
		InstituteLng:     -75.0,
	})

	reqBody := map[string]string{
		"name":    "Charlie",
		"address": "789 Elm St",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest("POST", "/api/v1/participants", bytes.NewReader(body))
	w := httptest.NewRecorder()

	h.HandleCreateParticipant(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)

	var participant models.Participant
	err := json.NewDecoder(w.Body).Decode(&participant)
	require.NoError(t, err)

	assert.NotZero(t, participant.ID)
	assert.Equal(t, "Charlie", participant.Name)
	assert.Equal(t, "789 Elm St", participant.Address)
	assert.NotZero(t, participant.Lat)
	assert.NotZero(t, participant.Lng)
}

func TestHandleCreateParticipantMissingName(t *testing.T) {
	h := setupTestHandler(t)

	reqBody := map[string]string{
		"address": "789 Elm St",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest("POST", "/api/v1/participants", bytes.NewReader(body))
	w := httptest.NewRecorder()

	h.HandleCreateParticipant(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandleUpdateParticipant(t *testing.T) {
	h := setupTestHandler(t)

	created, _ := h.DB.Participants().Create(context.Background(), &models.Participant{
		Name: "Original", Address: "Original Address", Lat: 40.0, Lng: -75.0,
	})

	reqBody := map[string]string{
		"name":    "Updated",
		"address": "Updated Address",
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest("PUT", fmt.Sprintf("/api/v1/participants/%d", created.ID), bytes.NewReader(body))
	w := httptest.NewRecorder()

	h.HandleUpdateParticipant(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var participant models.Participant
	err := json.NewDecoder(w.Body).Decode(&participant)
	require.NoError(t, err)

	assert.Equal(t, created.ID, participant.ID)
	assert.Equal(t, "Updated", participant.Name)
}

func TestHandleDeleteParticipant(t *testing.T) {
	h := setupTestHandler(t)

	created, _ := h.DB.Participants().Create(context.Background(), &models.Participant{
		Name: "ToDelete", Address: "Delete Me", Lat: 40.0, Lng: -75.0,
	})

	req := httptest.NewRequest("DELETE", fmt.Sprintf("/api/v1/participants/%d", created.ID), nil)
	w := httptest.NewRecorder()

	h.HandleDeleteParticipant(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)

	// Verify deletion
	found, _ := h.DB.Participants().GetByID(context.Background(), created.ID)
	assert.Nil(t, found)
}

func TestHandleCalculateRoutes(t *testing.T) {
	h := setupTestHandler(t)

	// Set up test data
	h.DB.Settings().Update(context.Background(), &models.Settings{
		InstituteAddress: "Test Institute",
		InstituteLat:     40.0,
		InstituteLng:     -75.0,
	})

	p, _ := h.DB.Participants().Create(context.Background(), &models.Participant{
		Name: "Participant", Address: "P Addr", Lat: 40.1, Lng: -75.1,
	})

	d, _ := h.DB.Drivers().Create(context.Background(), &models.Driver{
		Name: "Driver", Address: "D Addr", Lat: 40.2, Lng: -75.2, VehicleCapacity: 4,
	})

	reqBody := CalculateRoutesRequest{
		ParticipantIDs: []int64{p.ID},
		DriverIDs:      []int64{d.ID},
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest("POST", "/api/v1/routes/calculate", bytes.NewReader(body))
	w := httptest.NewRecorder()

	h.HandleCalculateRoutes(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var result models.RoutingResult
	err := json.NewDecoder(w.Body).Decode(&result)
	require.NoError(t, err)

	assert.Len(t, result.Routes, 1)
	assert.Equal(t, 1, result.Summary.TotalParticipants)
}

func TestHandleCalculateRoutesNoParticipants(t *testing.T) {
	h := setupTestHandler(t)

	reqBody := CalculateRoutesRequest{
		ParticipantIDs: []int64{},
		DriverIDs:      []int64{1},
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest("POST", "/api/v1/routes/calculate", bytes.NewReader(body))
	w := httptest.NewRecorder()

	h.HandleCalculateRoutes(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandleCalculateRoutesInsufficientCapacity(t *testing.T) {
	h := setupTestHandler(t)
	h.Router = &mockRouter{shouldFail: true}

	h.DB.Settings().Update(context.Background(), &models.Settings{
		InstituteAddress: "Test Institute",
		InstituteLat:     40.0,
		InstituteLng:     -75.0,
	})

	p, _ := h.DB.Participants().Create(context.Background(), &models.Participant{
		Name: "Participant", Address: "P Addr", Lat: 40.1, Lng: -75.1,
	})

	d, _ := h.DB.Drivers().Create(context.Background(), &models.Driver{
		Name: "Driver", Address: "D Addr", Lat: 40.2, Lng: -75.2, VehicleCapacity: 4,
	})

	reqBody := CalculateRoutesRequest{
		ParticipantIDs: []int64{p.ID},
		DriverIDs:      []int64{d.ID},
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest("POST", "/api/v1/routes/calculate", bytes.NewReader(body))
	w := httptest.NewRecorder()

	h.HandleCalculateRoutes(w, req)

	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)

	var response ErrorResponse
	err := json.NewDecoder(w.Body).Decode(&response)
	require.NoError(t, err)

	assert.Equal(t, "ROUTING_FAILED", response.Error.Code)
}

func TestWriteJSONHelper(t *testing.T) {
	h := &Handler{}
	w := httptest.NewRecorder()

	data := map[string]string{"message": "test"}
	h.writeJSON(w, http.StatusOK, data)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

	var result map[string]string
	err := json.NewDecoder(w.Body).Decode(&result)
	require.NoError(t, err)
	assert.Equal(t, "test", result["message"])
}

func TestWriteErrorHelper(t *testing.T) {
	h := &Handler{}
	w := httptest.NewRecorder()

	h.writeError(w, http.StatusBadRequest, "TEST_ERROR", "Test error message", nil)

	assert.Equal(t, http.StatusBadRequest, w.Code)

	var response ErrorResponse
	err := json.NewDecoder(w.Body).Decode(&response)
	require.NoError(t, err)

	assert.Equal(t, "TEST_ERROR", response.Error.Code)
	assert.Equal(t, "Test error message", response.Error.Message)
}
