package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"ride-home-router/internal/database"
	"ride-home-router/internal/distance"
	"ride-home-router/internal/models"
	"ride-home-router/internal/orderedroute"
	"ride-home-router/internal/routesession"
	"ride-home-router/internal/routing"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCalculateReservationCost(t *testing.T) {
	if os.Getenv("ROUTE_RESERVATION_COST") != "1" {
		t.Skip("set ROUTE_RESERVATION_COST=1 to time full calculate requests against local Postgres")
	}
	load, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("loadavg=%s", strings.TrimSpace(string(load)))
	previous := http.DefaultTransport
	http.DefaultTransport = reservationCostProvider{}
	t.Cleanup(func() { http.DefaultTransport = previous })
	for _, size := range []struct{ riders, drivers int }{{30, 8}, {72, 14}, {72, 56}} {
		t.Run(fmt.Sprintf("%d-%d", size.riders, size.drivers), func(t *testing.T) {
			handler, store := newTestRouteHandler(t)
			handler.Router = routing.NewBalancedRouter(distance.NewEstimator())
			handler.RouteSession.Close()
			handler.RouteSession = routesession.NewPersistentStore(distance.NewEstimator(), store.Workflows())
			t.Cleanup(handler.RouteSession.Close)
			ledger := store.GoogleUsage()
			if err := ledger.SetCeiling(t.Context(), database.UsageSKURoutes, 1000000); err != nil {
				t.Fatal(err)
			}
			var transactions, commits atomic.Int64
			handler.Measurer = orderedroute.NewClient(func(context.Context) (string, error) { return "synthetic-key", nil }, func(ctx context.Context, n int) error {
				transactions.Add(1)
				err := ledger.Reserve(ctx, database.UsageSKURoutes, n)
				if err == nil {
					commits.Add(1)
				}
				return err
			})
			location, err := store.ActivityLocations().Create(t.Context(), &models.ActivityLocation{Name: "Synthetic venue", Address: "Venue", Lat: 40, Lng: -74})
			if err != nil {
				t.Fatal(err)
			}
			form := url.Values{"activity_location_id": {int64ToString(location.ID)}, "route_time": {"18:30"}, "mode": {"dropoff"}}
			for i := range size.riders {
				angle := float64(i) * 2 * math.Pi / float64(size.riders)
				p, err := store.Participants().Create(t.Context(), &models.Participant{Name: fmt.Sprintf("Rider %d", i), Address: fmt.Sprintf("Rider address %d", i), Lat: 40 + .04*math.Sin(angle), Lng: -74 + .04*math.Cos(angle)})
				if err != nil {
					t.Fatal(err)
				}
				form.Add("participant_ids", int64ToString(p.ID))
			}
			for i := range size.drivers {
				angle := float64(i) * 2 * math.Pi / float64(size.drivers)
				d, err := store.Drivers().Create(t.Context(), &models.Driver{Name: fmt.Sprintf("Driver %d", i), Address: fmt.Sprintf("Driver address %d", i), Lat: 40 + .06*math.Sin(angle), Lng: -74 + .06*math.Cos(angle), VehicleCapacity: max(4, (size.riders+size.drivers-1)/size.drivers)})
				if err != nil {
					t.Fatal(err)
				}
				form.Add("driver_ids", int64ToString(d.ID))
			}
			for sample := range 6 {
				transactions.Store(0)
				commits.Store(0)
				before, err := ledger.Reserved(t.Context(), database.UsageSKURoutes)
				if err != nil {
					t.Fatal(err)
				}
				started := time.Now()
				response := postCalculate(handler, form, true)
				elapsed := time.Since(started)
				if response.Code != http.StatusOK {
					t.Fatalf("calculate status=%d body=%s", response.Code, response.Body.String())
				}
				measured := strings.Count(response.Body.String(), `data-timings="measured"`)
				if measured == 0 || strings.Contains(response.Body.String(), `data-timings="failed"`) {
					t.Fatal("calculate did not measure occupied cars")
				}
				after, err := ledger.Reserved(t.Context(), database.UsageSKURoutes)
				if err != nil || after-before != 2*measured {
					t.Fatalf("ledger delta=%d err=%v measured=%d", after-before, err, measured)
				}
				t.Logf("sample=%d calculate_ms=%.3f reservation_transactions=%d reservation_commits=%d measured_cars=%d reserved_units=%d", sample, float64(elapsed)/float64(time.Millisecond), transactions.Load(), commits.Load(), measured, after-before)
			}
		})
	}
}

type reservationCostProvider struct{}

func (reservationCostProvider) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Host != "routes.googleapis.com" {
		return nil, fmt.Errorf("unexpected external request to %s", r.URL.Host)
	}
	var request struct {
		Intermediates []json.RawMessage `json:"intermediates"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		return nil, err
	}
	legs := make([]string, len(request.Intermediates)+1)
	for i := range legs {
		legs[i] = `{"distanceMeters":1000,"duration":"60s"}`
	}
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"routes":[{"legs":[` + strings.Join(legs, ",") + `]}]}`))}, nil
}
