package routing

import (
	"context"
	"errors"
	"ride-home-router/internal/distance"
	"ride-home-router/internal/models"
	"runtime"
	"sync"
	"sync/atomic"
)

const assignmentBatchSize = 32

// Extra workers never queue behind another calculation. Its caller can always
// evaluate inline, and at most three additional search goroutines run process-wide.
var assignmentWorkerSlots = make(chan struct{}, 3)

var errUncachedCandidate = errors.New("candidate needs a serial distance lookup")

type cachedSolveDistances struct{ lookup *solveDistanceLookup }

func (s cachedSolveDistances) distanceValue(ctx context.Context, a, b models.Coordinates) (distance.DistanceResult, error) {
	if err := ctx.Err(); err != nil {
		return distance.DistanceResult{}, err
	}
	if value, ok := s.lookup.values[makeSolvePairKey(a, b)]; ok {
		return value, nil
	}
	return distance.DistanceResult{}, errUncachedCandidate
}

func (s cachedSolveDistances) GetDistance(ctx context.Context, a, b models.Coordinates) (*distance.DistanceResult, error) {
	v, err := s.distanceValue(ctx, a, b)
	if err != nil {
		return nil, err
	}
	return &v, nil
}

type assignmentCandidate struct {
	firstDriverID, secondDriverID int64
	firstStops, secondStops       []*models.Participant
}

type assignmentEvaluation struct {
	stops      map[int64][]*models.Participant
	score      solutionScore
	err        error
	panicked   bool
	panicValue any
}

func (candidate assignmentCandidate) evaluate(ctx context.Context, rc routeContext, routes map[int64]*balancedRoute, metrics map[int64]routeObjectiveMetrics, driverIDs []int64) assignmentEvaluation {
	stops, score, err := rc.optimizeStopsForSolution(ctx, routes, metrics, map[int64][]*models.Participant{
		candidate.firstDriverID:  candidate.firstStops,
		candidate.secondDriverID: candidate.secondStops,
	}, driverIDs)
	return assignmentEvaluation{stops: stops, score: score, err: err}
}

// Each batch owns its child context and joins all workers before returning.
// Workers can only read memoized distances. The coordinator handles cache misses
// later, in candidate order, without changing the source's serial contract.
func evaluateAssignmentBatch(parent context.Context, rc routeContext, routes map[int64]*balancedRoute, metrics map[int64]routeObjectiveMetrics, driverIDs []int64, candidates []assignmentCandidate) []assignmentEvaluation {
	results := make([]assignmentEvaluation, len(candidates))
	extra := 0
	for range min(runtime.GOMAXPROCS(0)-1, cap(assignmentWorkerSlots), len(candidates)-1) {
		select {
		case assignmentWorkerSlots <- struct{}{}:
			extra++
		default:
		}
	}
	if extra == 0 {
		// No speculation or extra allocations when another solve owns the slots.
		for i, candidate := range candidates {
			results[i] = candidate.evaluate(parent, rc, routes, metrics, driverIDs)
			if results[i].err != nil {
				break
			}
		}
		return results
	}
	ctx, cancel := context.WithCancel(parent)
	var workers sync.WaitGroup
	defer func() {
		cancel()
		workers.Wait()
		for range extra {
			<-assignmentWorkerSlots
		}
	}()
	// The mutable memo and all route state remain untouched until the batch joins.
	local := rc
	local.distanceCalc = cachedSolveDistances{lookup: rc.distanceCalc.(*solveDistanceLookup)}
	var next atomic.Int64
	run := func() {
		for {
			if ctx.Err() != nil {
				return
			}
			i := int(next.Add(1)) - 1
			if i >= len(candidates) {
				return
			}
			func() {
				completed := false
				defer func() {
					if !completed {
						results[i] = assignmentEvaluation{panicked: true, panicValue: recover()}
					}
				}()
				results[i] = candidates[i].evaluate(ctx, local, routes, metrics, driverIDs)
				completed = true
			}()
		}
	}
	for range extra {
		workers.Go(run)
	}
	run()
	workers.Wait()
	return results
}
