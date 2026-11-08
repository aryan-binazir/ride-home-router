package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"ride-home-router/internal/database"
	"ride-home-router/internal/postgres/postgrestest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
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

type probeStore struct {
	database.DataStore
	calls   atomic.Int32
	started chan struct{}
	release chan struct{}
	err     error
}

func (s *probeStore) probe(ctx context.Context) error {
	s.calls.Add(1)
	if s.started != nil {
		select {
		case s.started <- struct{}{}:
		default:
		}
	}
	if s.release != nil {
		<-s.release
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.err
}

func (s *probeStore) HealthCheck(ctx context.Context) error    { return s.probe(ctx) }
func (s *probeStore) ReadinessCheck(ctx context.Context) error { return s.probe(ctx) }

func TestPublicProbesShareRecentDatabaseResult(t *testing.T) {
	for _, endpoint := range []struct {
		name, path string
		handle     func(*Handler) http.HandlerFunc
	}{
		{"health", "/api/v1/health", func(h *Handler) http.HandlerFunc { return h.HandleHealthCheck }},
		{"ready", "/api/v1/ready", func(h *Handler) http.HandlerFunc { return h.HandleReadinessCheck }},
	} {
		get := func(h *Handler, ctx context.Context) int {
			rr := httptest.NewRecorder()
			endpoint.handle(h)(rr, httptest.NewRequestWithContext(ctx, http.MethodGet, endpoint.path, nil))
			return rr.Code
		}
		t.Run(endpoint.name, func(t *testing.T) {
			t.Run("concurrent callers on a cold cache share one check", func(t *testing.T) {
				store := &probeStore{started: make(chan struct{}, 1), release: make(chan struct{})}
				handler := &Handler{DB: store}
				codes := make(chan int, 20)
				var requests, entered sync.WaitGroup
				entered.Add(cap(codes))
				for range cap(codes) {
					requests.Go(func() {
						entered.Done()
						codes <- get(handler, t.Context())
					})
				}
				<-store.started
				entered.Wait()
				// Hold the first check open so callers queue behind it rather than finding a warm cache.
				time.Sleep(50 * time.Millisecond)
				close(store.release)
				requests.Wait()
				close(codes)
				for code := range codes {
					if code != http.StatusOK {
						t.Fatalf("status=%d", code)
					}
				}
				if got := store.calls.Load(); got != 1 {
					t.Fatalf("database checks=%d, want 1", got)
				}
			})
			t.Run("a disconnected caller cannot cache a failure", func(t *testing.T) {
				store := &probeStore{}
				handler := &Handler{DB: store}
				disconnected, cancel := context.WithCancel(t.Context())
				cancel()
				if code := get(handler, disconnected); code != http.StatusOK {
					t.Fatalf("disconnected caller status=%d", code)
				}
				if code := get(handler, t.Context()); code != http.StatusOK || store.calls.Load() != 1 {
					t.Fatalf("next caller status=%d checks=%d, want 200 and 1", code, store.calls.Load())
				}
			})
			t.Run("failures are shared until the TTL expires", func(t *testing.T) {
				store := &probeStore{err: errors.New("database unavailable")}
				handler := &Handler{DB: store}
				for range 2 {
					if code := get(handler, t.Context()); code != http.StatusServiceUnavailable {
						t.Fatalf("status=%d, want 503", code)
					}
				}
				if got := store.calls.Load(); got != 1 {
					t.Fatalf("database checks=%d, want 1", got)
				}
				store.err = nil
				time.Sleep(probeCacheTTL)
				if code := get(handler, t.Context()); code != http.StatusOK || store.calls.Load() != 2 {
					t.Fatalf("after TTL status=%d checks=%d, want 200 and 2", code, store.calls.Load())
				}
			})
		})
	}
}
