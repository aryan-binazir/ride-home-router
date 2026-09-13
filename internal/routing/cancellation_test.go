package routing_test

import (
	"context"
	"encoding/json"
	"errors"
	"ride-home-router/internal/distance"
	"ride-home-router/internal/models"
	"ride-home-router/internal/routing"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Count resource lifetimes at the package boundary, without depending on worker
// function names or a private scheduler hook. The tests themselves are external.
func routingGoroutines() int {
	stacks := make([]byte, 1<<20)
	n := runtime.Stack(stacks, true)
	count := 0
	for stack := range strings.SplitSeq(string(stacks[:n]), "\n\n") {
		if strings.Contains(stack, "ride-home-router/internal/routing.") {
			count++
		}
	}
	return count
}

func TestCalculationResultsAgreeAcrossParallelism(t *testing.T) {
	discardRoutingLogs(t)
	previous := runtime.GOMAXPROCS(1)
	t.Cleanup(func() { runtime.GOMAXPROCS(previous) })
	for _, mode := range []routing.RouteMode{routing.RouteModePickup, routing.RouteModeDropoff} {
		for _, households := range []bool{false, true} {
			// Keep repeated determinism checks small under race and coverage
			// instrumentation. Large solves belong in BenchmarkCalculateRoutesWarm.
			req, source := performanceFixture(12, 3, households)
			req.Mode = mode
			runtime.GOMAXPROCS(1)
			expected, err := routing.NewBalancedRouter(source).CalculateRoutes(t.Context(), &req)
			if err != nil {
				t.Fatal(err)
			}
			want, err := json.Marshal(expected)
			if err != nil {
				t.Fatal(err)
			}
			for _, procs := range []int{1, 2, 4} {
				runtime.GOMAXPROCS(procs)
				for range 7 {
					actual, err := routing.NewBalancedRouter(source).CalculateRoutes(t.Context(), &req)
					if err != nil {
						t.Fatal(err)
					}
					got, err := json.Marshal(actual)
					if err != nil {
						t.Fatal(err)
					}
					if string(got) != string(want) {
						t.Fatalf("route or exact metric changed: mode=%s households=%t procs=%d", mode, households, procs)
					}
				}
			}
		}
	}
}

type interruptedDistances struct {
	warmDistances
	calls, stopAt int
	failure       error
	panics        bool
}

func (s *interruptedDistances) GetDistance(ctx context.Context, a, b models.Coordinates) (*distance.DistanceResult, error) {
	// Deliberately unsynchronized: the public source remains serial even when
	// search is parallel, and the race suite protects that contract.
	s.calls++
	if s.calls == s.stopAt {
		if s.panics {
			panic(s.failure)
		}
		return nil, s.failure
	}
	return s.warmDistances.GetDistance(ctx, a, b)
}

func TestCalculationJoinsSearchBeforeSourceFailureReturns(t *testing.T) {
	discardRoutingLogs(t)
	previous := runtime.GOMAXPROCS(4)
	t.Cleanup(func() { runtime.GOMAXPROCS(previous) })
	req, warm := performanceFixture(100, 20, false)
	for _, stopAt := range []int{1, 100, 500, 1000} {
		for _, panics := range []bool{false, true} {
			failure := errors.New("source failure")
			source := &interruptedDistances{warmDistances: warm, stopAt: stopAt, failure: failure, panics: panics}
			func() {
				defer func() {
					value := recover()
					if panics && value != failure {
						t.Errorf("panic = %v, want original source panic", value)
					}
					if !panics && value != nil {
						t.Errorf("unexpected panic: %v", value)
					}
				}()
				_, err := routing.NewBalancedRouter(source).CalculateRoutes(t.Context(), &req)
				if !panics && !errors.Is(err, failure) {
					t.Errorf("error = %v, want original source failure", err)
				}
			}()
			if count := routingGoroutines(); count != 0 {
				t.Fatalf("%d routing goroutines remain after source failure", count)
			}
		}
	}
}

func TestParallelCalculationJoinsWorkersOnParentCancellation(t *testing.T) {
	discardRoutingLogs(t)
	previous := runtime.GOMAXPROCS(4)
	t.Cleanup(func() { runtime.GOMAXPROCS(previous) })
	req, source := performanceFixture(40, 8, false)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := routing.NewBalancedRouter(source).CalculateRoutes(ctx, &req); done <- err }()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case err := <-done:
			t.Fatalf("calculation ended before parallel search was observed: %v", err)
		case <-deadline.C:
			cancel()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("calculation ignored cancellation")
			}
			t.Fatal("parallel calculation did not start")
		default:
			if routingGoroutines() < 2 {
				runtime.Gosched()
				continue
			}
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancellation error = %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("workers did not stop after parent cancellation")
			}
			if count := routingGoroutines(); count != 0 {
				t.Fatalf("calculation returned with %d routing goroutines still alive", count)
			}
			return
		}
	}
}

func TestConcurrentCalculationsBoundWorkersAndJoinOnCancellation(t *testing.T) {
	discardRoutingLogs(t)
	previous := runtime.GOMAXPROCS(4)
	t.Cleanup(func() { runtime.GOMAXPROCS(previous) })
	for range 5 {
		req, source := performanceFixture(40, 8, false)
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 4)
		for range 4 {
			go func() {
				// Each calculation owns its mutable input slices.
				local := req
				local.Participants = append([]models.Participant(nil), req.Participants...)
				local.Drivers = append([]models.Driver(nil), req.Drivers...)
				_, err := routing.NewBalancedRouter(source).CalculateRoutes(ctx, &local)
				done <- err
			}()
		}
		until := time.Now().Add(5 * time.Second)
		observed := false
		for time.Now().Before(until) {
			count := routingGoroutines()
			if count > 7 {
				t.Errorf("%d routing goroutines exceed four callers plus three shared workers", count)
				break
			}
			if count > 4 {
				observed = true
				break
			}
			runtime.Gosched()
		}
		cancel()
		for range 4 {
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Errorf("cancellation error = %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("concurrent calculation ignored parent cancellation")
			}
		}
		if !observed {
			t.Fatal("concurrent search workers were not observed")
		}
		if count := routingGoroutines(); count != 0 {
			t.Fatalf("%d routing goroutines survived canceled concurrent calculations", count)
		}
	}
}
