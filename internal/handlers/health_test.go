package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"ride-home-router/internal/database"
	"ride-home-router/internal/postgres/postgrestest"
	"testing"
)

type failingHealthStore struct {
	database.DataStore
}

func (failingHealthStore) HealthCheck(context.Context) error { return errors.New("connection refused") }

type readinessStore struct {
	database.DataStore
	err error
}

func (s readinessStore) ReadinessCheck(context.Context) error { return s.err }

func TestHandleReadinessCheck(t *testing.T) {
	for _, tt := range []struct {
		name       string
		err        error
		wantStatus int
		wantState  string
	}{
		{name: "schema current", wantStatus: http.StatusOK, wantState: "ready"},
		{name: "schema not current", err: errors.New("migration mismatch"), wantStatus: http.StatusServiceUnavailable, wantState: "not_ready"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			handler := &Handler{DB: readinessStore{err: tt.err}}
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/ready", nil)
			rr := httptest.NewRecorder()

			handler.HandleReadinessCheck(rr, req)

			if rr.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d body=%q", rr.Code, tt.wantStatus, rr.Body.String())
			}
			var body map[string]string
			if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			if body["status"] != tt.wantState {
				t.Fatalf("status payload = %q, want %q", body["status"], tt.wantState)
			}
		})
	}
}

func TestHandleHealthCheck(t *testing.T) {
	healthy := &Handler{DB: postgrestest.Open(t)}
	for _, tt := range []struct {
		name       string
		handler    *Handler
		wantStatus int
		wantDB     string
	}{
		{name: "database reachable", handler: healthy, wantStatus: http.StatusOK, wantDB: "connected"},
		{name: "database unreachable", handler: &Handler{DB: failingHealthStore{}}, wantStatus: http.StatusServiceUnavailable, wantDB: "error"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/health", nil)
			rr := httptest.NewRecorder()

			tt.handler.HandleHealthCheck(rr, req)

			if rr.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d body=%q", rr.Code, tt.wantStatus, rr.Body.String())
			}
			var body map[string]string
			if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			if body["database"] != tt.wantDB {
				t.Fatalf("database = %q, want %q", body["database"], tt.wantDB)
			}
		})
	}
}
