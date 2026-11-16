package planeval

import (
	"context"
	"fmt"
	"log"
	"os"
	"reflect"
	"ride-home-router/internal/distance"
	"ride-home-router/internal/routing"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func evaluationWorkers() (int, error) {
	workers := min(2, runtime.GOMAXPROCS(0))
	if value := os.Getenv("PLANNER_EVAL_WORKERS"); value != "" {
		var err error
		workers, err = strconv.Atoi(value)
		if err != nil || workers < 1 || workers > 24 {
			return 0, fmt.Errorf("PLANNER_EVAL_WORKERS must be an integer from 1 to 24")
		}
	}
	return workers, nil
}

func runParallel(ctx context.Context, newRouter func() routing.Router, scenarios []Scenario, seeds uint64, workers int) ([]Result, error) {
	batches := make([][]Result, len(scenarios))
	errors := make([]error, len(scenarios))
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range min(workers, len(scenarios)) {
		wg.Go(func() {
			for i := range jobs {
				batches[i], errors[i] = Run(ctx, newRouter, scenarios[i:i+1], seeds)
			}
		})
	}
	for i := range scenarios {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	var results []Result
	for i, batch := range batches {
		if errors[i] != nil {
			return nil, errors[i]
		}
		results = append(results, batch...)
	}
	return results, nil
}

// The enclosing phase timer starts before the planner's two-second deadline.
// Rejecting its full elapsed time is conservative: post-search work can also
// exceed the threshold. Missing logs must fail closed if the planner changes.
type phase4Timings struct {
	count   int
	maximum time.Duration
	err     error
}

func (p *phase4Timings) Write(data []byte) (int, error) {
	const marker = "[TIMING] Phase 4 (driver swaps): "
	if _, tail, ok := strings.Cut(string(data), marker); ok {
		p.count++
		fields := strings.Fields(tail)
		if len(fields) == 0 {
			p.err = fmt.Errorf("missing phase-4 duration")
			return len(data), nil
		}
		elapsed, err := time.ParseDuration(fields[0])
		if err != nil || elapsed < 0 {
			p.err = fmt.Errorf("invalid phase-4 duration %q", fields[0])
			return len(data), p.err
		}
		p.maximum = max(p.maximum, elapsed)
	}
	return len(data), nil
}

func (p *phase4Timings) check(expected int) error {
	if p.err != nil {
		return p.err
	}
	if p.count != expected {
		return fmt.Errorf("phase-4 timing count %d, expected %d; evaluation cannot verify the wall-clock budget", p.count, expected)
	}
	if p.maximum >= 2*time.Second {
		return fmt.Errorf("phase-4 elapsed %s reached the 2s wall-clock budget; results may be truncated; rerun with PLANNER_EVAL_WORKERS=1 or less machine load", p.maximum)
	}
	return nil
}

func runEvaluation(t *testing.T) []Result {
	t.Helper()
	workers, err := evaluationWorkers()
	if err != nil {
		t.Fatal(err)
	}
	timings := &phase4Timings{}
	previous := log.Writer()
	// log.Logger serializes writes, including writes from different scenarios.
	log.SetOutput(timings)
	defer log.SetOutput(previous)
	started := time.Now()
	results, err := runParallel(context.Background(), func() routing.Router { return routing.NewBalancedRouter(distance.NewEstimator()) }, Scenarios(), 3, workers)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("workers=%d runs=%d elapsed=%s maximum_phase4=%s", workers, len(results), time.Since(started), timings.maximum)
	if err := timings.check(len(results)); err != nil {
		t.Fatal(err)
	}
	return results
}

func TestPhase4TimingsFailClosed(t *testing.T) {
	for _, tc := range []struct {
		name, message string
		count         int
		bad           bool
	}{
		{"headroom", "[TIMING] Phase 4 (driver swaps): 1.999s (swaps=0)", 1, false},
		{"boundary", "[TIMING] Phase 4 (driver swaps): 2s (swaps=0)", 1, true},
		{"overrun", "[TIMING] Phase 4 (driver swaps): 2.1s (swaps=0)", 1, true},
		{"missing", "other log", 1, true},
		{"malformed", "[TIMING] Phase 4 (driver swaps): invalid", 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var p phase4Timings
			_, _ = p.Write([]byte(tc.message))
			if err := p.check(tc.count); (err != nil) != tc.bad {
				t.Fatalf("check = %v", err)
			}
		})
	}
}

func TestParallelMatchesSerial(t *testing.T) {
	scenarios := []Scenario{
		{Name: "first", Riders: 12, SeatFactor: 1.5, RiderMix: Mix{Durham: 1}},
		{Name: "second", Riders: 8, SeatFactor: 1.5, RiderMix: Mix{Carrboro: 1}},
	}
	factory := func() routing.Router { return routing.NewBalancedRouter(distance.NewEstimator()) }
	serial, err := Run(context.Background(), factory, scenarios, 2)
	if err != nil {
		t.Fatal(err)
	}
	for _, workers := range []int{1, 2, 8} {
		parallel, err := runParallel(context.Background(), factory, scenarios, 2, workers)
		if err != nil {
			t.Fatal(err)
		}
		for i := range serial {
			serial[i].Metrics.SolveMs = 0
			parallel[i].Metrics.SolveMs = 0
		}
		if !reflect.DeepEqual(serial, parallel) || Report(serial, serial) != Report(serial, parallel) || len(Compare(serial, parallel)) != 0 {
			t.Fatalf("workers=%d differs from serial", workers)
		}
	}
}
