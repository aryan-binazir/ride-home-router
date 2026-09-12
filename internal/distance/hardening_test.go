package distance

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"ride-home-router/internal/database"
	"ride-home-router/internal/models"
	"strings"
	"sync/atomic"
	"testing"
)

func TestGoogleErrorsHideTransportAndDecodeDetails(t *testing.T) {
	for _, err := range []error{&googleTransportError{Cause: &url.Error{Op: "Post", URL: "https://secret.example", Err: errors.New("failed")}}, googleMatrixPublicError(errors.New("json: private"))} {
		if strings.Contains(err.Error(), "https://") || strings.Contains(err.Error(), "json:") {
			t.Fatalf("unsafe error %s", err)
		}
	}
	_, err := parseGoogleMatrixElements(strings.NewReader(`[{"duration":false}]`))
	if err == nil || strings.Contains(err.Error(), "json:") {
		t.Fatalf("decode error %v", err)
	}
}

func TestTemporaryDistanceFailures(t *testing.T) {
	for _, err := range []error{&googleHTTPError{StatusCode: 429}, &googleHTTPError{StatusCode: 503}, context.DeadlineExceeded} {
		if !IsTemporary(err) {
			t.Fatalf("not temporary: %v", err)
		}
	}
}

func TestPrewarmCapsUncachedPairsBeforeBilling(t *testing.T) {
	calls := 0
	calc, _ := newTestGoogleCalculator(t, func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(500) })
	pairs := make([]DistancePair, 60001)
	for i := range pairs {
		pairs[i] = DistancePair{Origin: models.Coordinates{Lat: 10 + float64(i)/100000, Lng: 20}, Destination: models.Coordinates{Lat: 30, Lng: 40}}
	}
	if err := calc.PrewarmPairs(t.Context(), pairs); !errors.Is(err, ErrTooManyDistancePairs) {
		t.Fatalf("error=%v", err)
	}
	if calls != 0 {
		t.Fatalf("billed calls=%d", calls)
	}
}

func TestPrewarmPacksColdRiderDriverPairs(t *testing.T) {
	var calls atomic.Int32
	calc, cache := newTestGoogleCalculator(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var request googleMatrixRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		if len(request.Origins)*len(request.Destinations) > 625 {
			t.Error("oversized matrix")
		}
		elements := []googleMatrixElement{}
		for i := range request.Origins {
			for j := range request.Destinations {
				elements = append(elements, googleMatrixElement{OriginIndex: i, DestinationIndex: j, Condition: "ROUTE_EXISTS", DistanceMeters: 1200, Duration: "300s"})
			}
		}
		_ = json.NewEncoder(w).Encode(elements)
	})
	points := make([]models.Coordinates, 126)
	for i := range points {
		points[i] = models.Coordinates{Lat: 10 + float64(i)/100, Lng: 20}
	}
	pairs := []DistancePair{}
	for i := range 125 {
		for j := range 101 {
			dest := j
			if j == 100 {
				dest = 125
			}
			if i != dest {
				pairs = append(pairs, DistancePair{Origin: points[i], Destination: points[dest]})
			}
		}
	}
	if err := calc.PrewarmPairs(t.Context(), pairs); err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got > 25 {
		t.Fatalf("requests=%d want <=25", got)
	}
	for _, pair := range pairs {
		result, err := calc.GetDistance(t.Context(), pair.Origin, pair.Destination)
		if err != nil || result.DistanceMeters != 1200 {
			t.Fatalf("distance=%v err=%v", result, err)
		}
	}
	if got := calls.Load(); got > 25 {
		t.Fatalf("lookup missed prewarm: %d", got)
	}
	for _, point := range points {
		if _, err := cache.Get(t.Context(), point, point); !errors.Is(err, database.ErrCacheMiss) {
			t.Fatalf("self result was cached: %v", err)
		}
	}
}

func TestPrewarmAllowsLargeWarmCache(t *testing.T) {
	calls := 0
	calc, cache := newTestGoogleCalculator(t, func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(500) })
	pairs := make([]DistancePair, 60001)
	entries := make([]models.DistanceCacheEntry, len(pairs))
	for i := range pairs {
		origin := models.Coordinates{Lat: 10 + float64(i)/100000, Lng: 20}
		dest := models.Coordinates{Lat: 30, Lng: 40}
		pairs[i] = DistancePair{Origin: origin, Destination: dest}
		entries[i] = models.DistanceCacheEntry{Origin: origin, Destination: dest, DistanceMeters: 1, DurationSecs: 1}
	}
	if err := cache.SetBatch(t.Context(), entries); err != nil {
		t.Fatal(err)
	}
	if err := calc.PrewarmPairs(t.Context(), pairs); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatalf("warm cache billed %d calls", calls)
	}
}

func TestPrewarmAcceptsExactlyTheBillingLimit(t *testing.T) {
	var billed atomic.Int64
	calc, _ := newTestGoogleCalculator(t, func(w http.ResponseWriter, r *http.Request) {
		var request googleMatrixRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		billed.Add(int64(len(request.Origins)) * int64(len(request.Destinations)))
		elements := []googleMatrixElement{}
		for i := range request.Origins {
			for j := range request.Destinations {
				elements = append(elements, googleMatrixElement{OriginIndex: i, DestinationIndex: j, Condition: "ROUTE_EXISTS", DistanceMeters: 1, Duration: "1s"})
			}
		}
		_ = json.NewEncoder(w).Encode(elements)
	})
	points := make([]models.Coordinates, 246)
	for i := range points {
		points[i] = models.Coordinates{Lat: 10 + float64(i)/100, Lng: 20}
	}
	pairs := []DistancePair{}
	for i := range 245 {
		for j := range 245 {
			if i != j {
				pairs = append(pairs, DistancePair{Origin: points[i], Destination: points[j]})
			}
		}
	}
	for j := range 220 {
		pairs = append(pairs, DistancePair{Origin: points[245], Destination: points[j]})
	}
	if err := calc.PrewarmPairs(t.Context(), pairs); err != nil {
		t.Fatal(err)
	}
	if got := billed.Load(); got != 60000 {
		t.Fatalf("billed elements=%d want60000", got)
	}
}
