package orderedroute

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"ride-home-router/internal/database"
	"ride-home-router/internal/models"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type recordedRequest struct {
	key, fieldMask string
	body           map[string]any
}

func fakeRoutes(t *testing.T, legMeters float64, legSecs int) (*httptest.Server, *[]recordedRequest, *sync.Mutex) {
	t.Helper()
	var mu sync.Mutex
	var seen []recordedRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var decoded map[string]any
		_ = json.Unmarshal(body, &decoded)
		mu.Lock()
		seen = append(seen, recordedRequest{key: r.Header.Get("X-Goog-Api-Key"), fieldMask: r.Header.Get("X-Goog-FieldMask"), body: decoded})
		mu.Unlock()
		if r.URL.Query().Get("key") != "" || r.Method != http.MethodPost {
			t.Errorf("unexpected request shape: %s %s", r.Method, r.URL)
		}
		intermediates, _ := decoded["intermediates"].([]any)
		legs := make([]string, 0, len(intermediates)+1)
		for range len(intermediates) + 1 {
			legs = append(legs, fmt.Sprintf(`{"distanceMeters":%v,"duration":"%ds"}`, legMeters, legSecs))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"routes":[{"legs":[%s]}]}`, strings.Join(legs, ","))
	}))
	t.Cleanup(server.Close)
	return server, &seen, &mu
}

func points(n int) []models.Coordinates {
	out := make([]models.Coordinates, n)
	for i := range out {
		out[i] = models.Coordinates{Lat: 42 + float64(i)*0.01, Lng: -71}
	}
	return out
}

func staticKey(k string) func(context.Context) (string, error) {
	return func(context.Context) (string, error) { return k, nil }
}

func TestMeasureSendsEssentialsRequestAndDecodesLegs(t *testing.T) {
	server, seen, mu := fakeRoutes(t, 1000, 120)
	reserved := 0
	client := newClient(staticKey("test-key"), func(_ context.Context, n int) error { reserved += n; return nil }, server.Client(), server.URL)
	results := client.Measure(context.Background(), []Request{{ID: "car-1", Points: points(3)}})
	if len(results) != 1 || results[0].Err != nil {
		t.Fatalf("Measure() = %+v", results)
	}
	if len(results[0].Legs) != 2 || results[0].Legs[0].DistanceMeters != 1000 || results[0].Legs[1].DurationSecs != 120 {
		t.Fatalf("legs = %+v, want two 1000 m / 120 s legs", results[0].Legs)
	}
	if reserved != 1 {
		t.Fatalf("reserved = %d attempts, want 1", reserved)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(*seen) != 1 {
		t.Fatalf("requests = %d, want 1", len(*seen))
	}
	got := (*seen)[0]
	if got.key != "test-key" || got.fieldMask != "routes.legs.distanceMeters,routes.legs.duration" {
		t.Fatalf("headers = %+v", got)
	}
	if got.body["travelMode"] != "DRIVE" || got.body["routingPreference"] != "TRAFFIC_UNAWARE" {
		t.Fatalf("body = %v, want DRIVE + TRAFFIC_UNAWARE", got.body)
	}
	for _, pro := range []string{"optimizeWaypointOrder", "computeAlternativeRoutes", "extraComputations", "trafficModel", "departureTime"} {
		if _, present := got.body[pro]; present {
			t.Fatalf("body contains Pro-tier field %q", pro)
		}
	}
	if intermediates, _ := got.body["intermediates"].([]any); len(intermediates) != 1 {
		t.Fatalf("intermediates = %v, want the single household stop", got.body["intermediates"])
	}
}

func TestMeasureChunksLongRoutesAtTenIntermediates(t *testing.T) {
	cases := []struct{ points, requests int }{{3, 1}, {12, 1}, {13, 2}, {23, 2}, {24, 3}, {52, 5}}
	for _, tc := range cases {
		n, wantRequests := tc.points, tc.requests
		server, seen, mu := fakeRoutes(t, 500, 60)
		reserved := 0
		client := newClient(staticKey("k"), func(_ context.Context, c int) error { reserved += c; return nil }, server.Client(), server.URL)
		results := client.Measure(context.Background(), []Request{{ID: "car", Points: points(n)}})
		if results[0].Err != nil {
			t.Fatalf("%d points: %v", n, results[0].Err)
		}
		if len(results[0].Legs) != n-1 {
			t.Fatalf("%d points: legs = %d, want %d", n, len(results[0].Legs), n-1)
		}
		mu.Lock()
		requests := len(*seen)
		for _, r := range *seen {
			if intermediates, _ := r.body["intermediates"].([]any); len(intermediates) > 10 {
				t.Fatalf("%d points: a request carried %d intermediates", n, len(intermediates))
			}
		}
		mu.Unlock()
		if requests != wantRequests || reserved != wantRequests {
			t.Fatalf("%d points: requests=%d reserved=%d, want %d", n, requests, reserved, wantRequests)
		}
	}
}

func TestMeasureReservesBeforeDispatchAndStopsWhenExhausted(t *testing.T) {
	server, seen, mu := fakeRoutes(t, 1, 1)
	client := newClient(staticKey("k"), func(context.Context, int) error { return database.ErrUsageExhausted }, server.Client(), server.URL)
	results := client.Measure(context.Background(), []Request{{ID: "a", Points: points(3)}, {ID: "b", Points: points(30)}})
	for _, result := range results {
		if !errors.Is(result.Err, database.ErrUsageExhausted) {
			t.Fatalf("result %s err = %v, want ErrUsageExhausted", result.ID, result.Err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(*seen) != 0 {
		t.Fatalf("requests dispatched after refusal: %d", len(*seen))
	}
}

func TestMeasureFailsClosedWithoutKeyAndHidesSecretsInErrors(t *testing.T) {
	server, seen, mu := fakeRoutes(t, 1, 1)
	client := newClient(staticKey(""), func(context.Context, int) error { return nil }, server.Client(), server.URL)
	results := client.Measure(context.Background(), []Request{{ID: "a", Points: points(3)}})
	if !errors.Is(results[0].Err, ErrNotConfigured) {
		t.Fatalf("missing key err = %v", results[0].Err)
	}
	mu.Lock()
	if len(*seen) != 0 {
		t.Fatal("request sent without a key")
	}
	mu.Unlock()

	refusing := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return nil, &url.Error{Op: "Post", URL: request.URL.String(), Err: errors.New("connection refused")}
	})}
	client = newClient(staticKey("AIzaSecret"), func(context.Context, int) error { return nil }, refusing, "https://routes.example/directions/v2:computeRoutes")
	results = client.Measure(context.Background(), []Request{{ID: "a", Points: points(3)}})
	for current := results[0].Err; current != nil; current = errors.Unwrap(current) {
		if strings.Contains(current.Error(), "AIzaSecret") || strings.Contains(current.Error(), "routes.example") {
			t.Fatalf("error leaks secret or URL: %v", current)
		}
	}
	if results[0].Err == nil {
		t.Fatal("transport failure should surface as an error")
	}
}

func TestMeasureRejectsMalformedResponsesAndBoundsConcurrency(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"routes":[{"legs":[{"distanceMeters":5,"duration":"5s"}]}]}`)
	}))
	t.Cleanup(bad.Close)
	client := newClient(staticKey("k"), func(context.Context, int) error { return nil }, bad.Client(), bad.URL)
	if results := client.Measure(context.Background(), []Request{{ID: "a", Points: points(3)}}); results[0].Err == nil {
		t.Fatal("leg count mismatch should fail")
	}

	var inFlight, peak atomic.Int32
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		current := inFlight.Add(1)
		for {
			old := peak.Load()
			if current <= old || peak.CompareAndSwap(old, current) {
				break
			}
		}
		time.Sleep(30 * time.Millisecond)
		inFlight.Add(-1)
		_, _ = io.WriteString(w, `{"routes":[{"legs":[{"distanceMeters":5,"duration":"5s"},{"distanceMeters":5,"duration":"5s"}]}]}`)
	}))
	t.Cleanup(slow.Close)
	client = newClient(staticKey("k"), func(context.Context, int) error { return nil }, slow.Client(), slow.URL)
	requests := make([]Request, 40)
	for i := range requests {
		requests[i] = Request{ID: fmt.Sprint(i), Points: points(3)}
	}
	results := client.Measure(context.Background(), requests)
	for _, result := range results {
		if result.Err != nil {
			t.Fatalf("result %s: %v", result.ID, result.Err)
		}
	}
	if peak.Load() > maxConcurrentRequests || peak.Load() < 2 {
		t.Fatalf("peak concurrency = %d, want between 2 and %d", peak.Load(), maxConcurrentRequests)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
