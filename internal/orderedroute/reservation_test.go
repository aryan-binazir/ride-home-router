package orderedroute

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"ride-home-router/internal/database"
	"ride-home-router/internal/postgres/postgrestest"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
)

type reservationTurn struct {
	user  int
	units int
}

func reservationRequests(user int, costs ...int) []Request {
	var requests []Request
	for i, cost := range costs {
		group := fmt.Sprintf("user-%d-car-%d", user, i)
		requests = append(requests,
			Request{ID: group + "-route", Group: group, Points: points((cost-2)*11 + 3)},
			Request{ID: group + "-baseline", Group: group, Points: points(2)},
		)
	}
	return requests
}

func TestReservationDifferential(t *testing.T) {
	for _, tc := range []struct {
		name      string
		remaining int
		failure   bool
		order     []int
	}{
		{name: "plenty", remaining: 100},
		{name: "exact ceiling", remaining: 9},
		{name: "part exhausted small groups first", remaining: 5, order: []int{2, 3, 4}},
		{name: "part exhausted large group first", remaining: 5, order: []int{4, 2, 3}},
		{name: "fully exhausted", order: []int{3, 4, 2}},
		{name: "provider failure mid chunked route", remaining: 100, failure: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests := reservationRequests(0, 2, 3, 4)
			var turns []reservationTurn
			for _, units := range tc.order {
				turns = append(turns, reservationTurn{units: units})
			}
			before, ledgerBefore, callsBefore, _ := runReservationScenario(t, false, tc.remaining, tc.failure, [][]Request{requests}, turns)
			after, ledgerAfter, callsAfter, reservations := runReservationScenario(t, true, tc.remaining, tc.failure, [][]Request{requests}, turns)
			if !reflect.DeepEqual(before, after) || ledgerBefore != ledgerAfter || !slices.Equal(callsBefore, callsAfter) {
				t.Fatalf("before=%v ledger=%d calls=%v\nafter=%v ledger=%d calls=%v", before, ledgerBefore, callsBefore, after, ledgerAfter, callsAfter)
			}
			wantTransactions := 1
			if tc.remaining < 9 {
				wantTransactions = 4
			}
			if reservations != wantTransactions {
				t.Fatalf("reservation transactions=%d want=%d", reservations, wantTransactions)
			}
		})
	}
}

func TestReservationConcurrentFallbackDifferential(t *testing.T) {
	requests := [][]Request{reservationRequests(0, 2, 3), reservationRequests(1, 4, 5)}
	for _, turns := range [][]reservationTurn{
		{{0, 2}, {1, 4}, {0, 3}, {1, 5}},
		{{1, 4}, {0, 2}, {1, 5}, {0, 3}},
		{{0, 3}, {1, 5}, {1, 4}, {0, 2}},
	} {
		t.Run(fmt.Sprint(turns), func(t *testing.T) {
			before, ledgerBefore, callsBefore, _ := runReservationScenario(t, false, 4, false, requests, turns)
			after, ledgerAfter, callsAfter, _ := runReservationScenario(t, true, 4, false, requests, turns)
			if !reflect.DeepEqual(before, after) || ledgerBefore != ledgerAfter || !slices.Equal(callsBefore, callsAfter) {
				t.Fatalf("before=%v ledger=%d calls=%v\nafter=%v ledger=%d calls=%v", before, ledgerBefore, callsBefore, after, ledgerAfter, callsAfter)
			}
		})
	}
}

type reservationOutcome struct {
	ID    string
	Legs  []Leg
	Error string
	Class string
}

func runReservationScenario(t *testing.T, batch bool, remaining int, providerFailure bool, requests [][]Request, turns []reservationTurn) ([][]reservationOutcome, int, []string, int) {
	t.Helper()
	databaseURL := postgrestest.DatabaseURL(t)
	store := postgrestest.OpenURL(t, databaseURL)
	ledger := store.GoogleUsage()
	const seeded = 17
	if err := ledger.SetCeiling(t.Context(), database.UsageSKURoutes, seeded+remaining); err != nil {
		t.Fatal(err)
	}
	if err := ledger.Seed(t.Context(), database.UsageSKURoutes, seeded); err != nil {
		t.Fatal(err)
	}
	permits := make(map[reservationTurn]chan struct{})
	for _, turn := range turns {
		permits[turn] = make(chan struct{})
	}
	if len(turns) > 0 {
		close(permits[turns[0]])
	}
	var scheduleMu sync.Mutex
	completed := make(map[reservationTurn]bool)
	position := 0
	finish := func(user, units int, wholeBatch bool) {
		scheduleMu.Lock()
		defer scheduleMu.Unlock()
		for _, turn := range turns {
			if turn.user == user && (wholeBatch || turn.units == units) {
				completed[turn] = true
			}
		}
		for position < len(turns) && completed[turns[position]] {
			position++
			if position < len(turns) {
				close(permits[turns[position]])
			}
		}
	}
	server, seen, mu := fakeRoutes(t, 500, 60)
	var transactions atomic.Int64
	results := make([][]reservationOutcome, len(requests))
	var wg sync.WaitGroup
	for user, input := range requests {
		userLedger := ledger
		if user != 0 {
			userLedger = postgrestest.OpenURL(t, databaseURL).GoogleUsage()
		}
		client := newClient(staticKey("synthetic-key"), func(ctx context.Context, units int) error {
			turn := reservationTurn{user, units}
			isBatch := batch && units == requestUnits(input)
			if permit := permits[turn]; permit != nil && !isBatch {
				select {
				case <-permit:
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			transactions.Add(1)
			err := userLedger.Reserve(ctx, database.UsageSKURoutes, units)
			if !isBatch || err == nil {
				finish(user, units, isBatch)
			}
			return err
		}, server.Client(), server.URL)
		if providerFailure {
			transport := client.httpClient.Transport
			client.httpClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				var payload computeRoutesRequest
				if err := jsonDecodeRequest(r, &payload); err != nil {
					return nil, err
				}
				if payload.Origin.Location.LatLng.Latitude == points(12)[11].Lat {
					return &http.Response{StatusCode: http.StatusServiceUnavailable, Body: http.NoBody, Header: make(http.Header)}, nil
				}
				return transport.RoundTrip(r)
			})}
		}
		wg.Go(func() {
			measure := client.measureBeforeBatching
			if batch {
				measure = client.Measure
			}
			for _, result := range measure(t.Context(), input) {
				outcome := reservationOutcome{ID: result.ID, Legs: result.Legs}
				if result.Err != nil {
					outcome.Error = result.Err.Error()
					switch {
					case errors.Is(result.Err, database.ErrUsageExhausted):
						outcome.Class = "quota"
					default:
						if status, ok := errors.AsType[*StatusError](result.Err); ok {
							outcome.Class = fmt.Sprintf("HTTP %d", status.Status)
						} else {
							outcome.Class = fmt.Sprintf("%T", result.Err)
						}
					}
				}
				results[user] = append(results[user], outcome)
			}
		})
	}
	wg.Wait()
	used, err := ledger.Reserved(t.Context(), database.UsageSKURoutes)
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	var calls []string
	for _, call := range *seen {
		calls = append(calls, fmt.Sprint(call.body))
	}
	slices.Sort(calls)
	return results, used, calls, int(transactions.Load())
}

func requestUnits(requests []Request) int {
	units := 0
	for _, request := range requests {
		units += RequestsFor(len(request.Points))
	}
	return units
}

func jsonDecodeRequest(r *http.Request, payload *computeRoutesRequest) error {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	return json.Unmarshal(body, payload)
}

func TestReservationBatchDatabaseErrorIsNotRetried(t *testing.T) {
	refusal := errors.New("commit acknowledgement lost")
	var calls atomic.Int64
	server, seen, mu := fakeRoutes(t, 500, 60)
	client := newClient(staticKey("k"), func(context.Context, int) error {
		calls.Add(1)
		return refusal
	}, server.Client(), server.URL)
	for _, result := range client.Measure(t.Context(), reservationRequests(0, 2, 3)) {
		if !errors.Is(result.Err, refusal) || result.Err.Error() != "orderedroute: commit acknowledgement lost" {
			t.Fatalf("error=%v", result.Err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if calls.Load() != 1 || len(*seen) != 0 {
		t.Fatalf("reservations=%d provider calls=%d", calls.Load(), len(*seen))
	}
}

func TestReservationValidationDoesNotCharge(t *testing.T) {
	for _, key := range []string{"", "k"} {
		var calls atomic.Int64
		client := newClient(staticKey(key), func(context.Context, int) error { calls.Add(1); return nil }, http.DefaultClient, "unused")
		for _, input := range [][]Request{nil, {{ID: "bad"}}, {{ID: "ok", Points: points(2)}, {ID: "bad"}}} {
			before := client.measureBeforeBatching(t.Context(), input)
			after := client.Measure(t.Context(), input)
			if fmt.Sprint(before) != fmt.Sprint(after) || calls.Load() != 0 {
				t.Fatalf("before=%v after=%v reservations=%d", before, after, calls.Load())
			}
		}
	}
}
