package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"ride-home-router/internal/distance"
	"ride-home-router/internal/models"
	"ride-home-router/internal/routesession"
	"testing"
)

type routeEditDistanceCalculator struct{}

func (routeEditDistanceCalculator) GetDistance(_ context.Context, origin, dest models.Coordinates) (*distance.DistanceResult, error) {
	d := math.Hypot(dest.Lat-origin.Lat, dest.Lng-origin.Lng) * 1000
	return &distance.DistanceResult{DistanceMeters: d, DurationSecs: d}, nil
}

func (c routeEditDistanceCalculator) GetDistanceMatrix(ctx context.Context, points []models.Coordinates) ([][]distance.DistanceResult, error) {
	result := make([][]distance.DistanceResult, len(points))
	for i := range points {
		result[i] = make([]distance.DistanceResult, len(points))
		for j := range points {
			d, _ := c.GetDistance(ctx, points[i], points[j])
			result[i][j] = *d
		}
	}
	return result, nil
}

func (c routeEditDistanceCalculator) GetDistancesFromPoint(ctx context.Context, origin models.Coordinates, destinations []models.Coordinates) ([]distance.DistanceResult, error) {
	result := make([]distance.DistanceResult, len(destinations))
	for i := range destinations {
		d, _ := c.GetDistance(ctx, origin, destinations[i])
		result[i] = *d
	}
	return result, nil
}

func (routeEditDistanceCalculator) PrewarmCache(context.Context, []models.Coordinates) error {
	return nil
}

func TestHandleMoveParticipantPreservesLegacyClaimedSourceValidation(t *testing.T) {
	store := routesession.NewStore(routeEditDistanceCalculator{})
	t.Cleanup(store.Close)
	h := &Handler{RouteSession: store}
	session := store.Create(routesession.CreateInput{
		Routes:           []models.CalculatedRoute{{Driver: &models.Driver{ID: 1, VehicleCapacity: 2}, Stops: []models.RouteStop{{Participant: &models.Participant{ID: 10}}}}, {Driver: &models.Driver{ID: 2, VehicleCapacity: 2}}},
		ActivityLocation: &models.ActivityLocation{}, RouteTime: "18:30", Mode: models.RouteModeDropoff,
	})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/routes/edit/move-participant", bytes.NewBufferString(`{"session_id":"`+session.ID+`","participant_id":10,"from_route_index":1,"to_route_index":1,"insert_at_position":-1}`))
	w := httptest.NewRecorder()
	h.HandleMoveParticipant(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	if want := "Participant not found in source route"; !bytes.Contains(w.Body.Bytes(), []byte(want)) {
		t.Fatalf("body = %q, want %q", w.Body.String(), want)
	}
}

func TestHandleGetRouteSessionMissingReturnsNoContent(t *testing.T) {
	store := routesession.NewStore(routeEditDistanceCalculator{})
	t.Cleanup(store.Close)
	h := &Handler{RouteSession: store}
	w := httptest.NewRecorder()
	h.HandleGetRouteSession(w, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/routes/session?session_id=missing", nil))
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", w.Code)
	}
}

func TestHandleMoveParticipantTranslatesBatchAndReturnsJSON(t *testing.T) {
	h, created := newRouteEditHandler(t)
	body := `{"session_id":"` + created.ID + `","moves":[{"participant_id":10,"from_route_index":99,"to_route_index":1,"insert_at_position":-1}]}`
	w := httptest.NewRecorder()
	h.HandleMoveParticipant(w, httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/routes/edit/move-participant", bytes.NewBufferString(body)))
	response := decodeRouteResponse(t, w)
	if len(response.Routes[0].Stops) != 0 || len(response.Routes[1].Stops) != 1 {
		t.Fatalf("routes = %#v", response.Routes)
	}
}

func TestHandleSwapDriversReturnsJSON(t *testing.T) {
	h, created := newRouteEditHandler(t)
	body := `{"session_id":"` + created.ID + `","route_index_1":0,"route_index_2":1}`
	w := httptest.NewRecorder()
	h.HandleSwapDrivers(w, httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/routes/edit/swap-drivers", bytes.NewBufferString(body)))
	response := decodeRouteResponse(t, w)
	if response.Routes[0].Driver.ID != 2 || response.Routes[1].Driver.ID != 1 {
		t.Fatalf("drivers were not swapped: %#v", response.Routes)
	}
}

func TestHandleResetRoutesReturnsOriginalJSON(t *testing.T) {
	h, created := newRouteEditHandler(t)
	if _, err := h.RouteSession.ApplyMoves(context.Background(), created.ID, []routesession.Move{{ParticipantID: 10, ToRouteIndex: 1, InsertAtPosition: -1}}, routesession.ApplyMovesOptions{}); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	h.HandleResetRoutes(w, httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/routes/edit/reset?session_id="+created.ID, nil))
	response := decodeRouteResponse(t, w)
	if len(response.Routes[0].Stops) != 1 || len(response.Routes[1].Stops) != 0 {
		t.Fatalf("routes were not reset: %#v", response.Routes)
	}
}

func TestHandleAddDriverReturnsJSON(t *testing.T) {
	h, created := newRouteEditHandler(t)
	body := `{"session_id":"` + created.ID + `","driver_id":3}`
	w := httptest.NewRecorder()
	h.HandleAddDriver(w, httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/routes/edit/add-driver", bytes.NewBufferString(body)))
	response := decodeRouteResponse(t, w)
	if len(response.Routes) != 3 || response.Routes[2].Driver.ID != 3 {
		t.Fatalf("driver route was not added: %#v", response.Routes)
	}
}

func TestHandleGetRouteSessionReturnsHTMXFragment(t *testing.T) {
	h, created := newRouteEditHandler(t)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/routes/session?session_id="+created.ID, nil)
	req.Header.Set("HX-Request", "true")
	w := httptest.NewRecorder()
	h.HandleGetRouteSession(w, req)
	if w.Code != http.StatusOK || !bytes.Contains(w.Body.Bytes(), []byte(`data-session-id="`+created.ID+`"`)) {
		t.Fatalf("status=%d body=%q", w.Code, w.Body.String())
	}
}

func TestHandleMoveParticipantPreservesBatchRequestValidation(t *testing.T) {
	h, created := newRouteEditHandler(t)
	tests := []struct {
		name string
		body []byte
		want string
	}{
		{
			name: "empty batch",
			body: []byte(`{"session_id":"` + created.ID + `","moves":[]}`),
			want: messageMovesRequired,
		},
		{
			name: "too many moves",
			body: func() []byte {
				moves := make([]participantMove, maxParticipantMovesPerBatch+1)
				for i := range moves {
					moves[i].ParticipantID = 10
				}
				payload, err := json.Marshal(map[string]any{"session_id": created.ID, "moves": moves})
				if err != nil {
					t.Fatalf("marshal request: %v", err)
				}
				return payload
			}(),
			want: messageTooManyMoves,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/routes/edit/move-participant", bytes.NewReader(tt.body))
			h.HandleMoveParticipant(w, req)
			if w.Code != http.StatusBadRequest || !bytes.Contains(w.Body.Bytes(), []byte(tt.want)) {
				t.Fatalf("status=%d body=%q, want 400 containing %q", w.Code, w.Body.String(), tt.want)
			}
		})
	}
}

func TestHandleResetRoutesPreservesHTMXResetControl(t *testing.T) {
	h, created := newRouteEditHandler(t)
	if _, err := h.RouteSession.ApplyMoves(context.Background(), created.ID, []routesession.Move{{
		ParticipantID: 10, ToRouteIndex: 1, InsertAtPosition: -1,
	}}, routesession.ApplyMovesOptions{}); err != nil {
		t.Fatal(err)
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/routes/edit/reset?session_id="+created.ID, nil)
	req.Header.Set("HX-Request", "true")
	h.HandleResetRoutes(w, req)

	if w.Code != http.StatusOK || !bytes.Contains(w.Body.Bytes(), []byte("Reset to Original")) {
		t.Fatalf("status=%d body=%q, want reset control", w.Code, w.Body.String())
	}
}

func newRouteEditHandler(t *testing.T) (*Handler, routesession.Snapshot) {
	t.Helper()
	store := routesession.NewStore(routeEditDistanceCalculator{})
	t.Cleanup(store.Close)
	drivers := []models.Driver{{ID: 1, Name: "One", VehicleCapacity: 2}, {ID: 2, Name: "Two", VehicleCapacity: 2}, {ID: 3, Name: "Three", VehicleCapacity: 2}}
	created := store.Create(routesession.CreateInput{
		Routes: []models.CalculatedRoute{
			{Driver: &drivers[0], EffectiveCapacity: 2, Stops: []models.RouteStop{{Participant: &models.Participant{ID: 10, Name: "Rider", Lat: 1}}}},
			{Driver: &drivers[1], EffectiveCapacity: 2, Stops: []models.RouteStop{}},
		},
		SelectedDrivers: drivers, ActivityLocation: &models.ActivityLocation{ID: 1, Name: "HQ"},
		RouteTime: "18:30", Mode: models.RouteModeDropoff,
	})
	return &Handler{RouteSession: store, Renderer: loadEmbeddedTemplates(t)}, created
}

func decodeRouteResponse(t *testing.T, w *httptest.ResponseRecorder) RouteCalculationResponse {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var response RouteCalculationResponse
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return response
}
