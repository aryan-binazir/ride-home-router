package routing

import (
	"context"
	"errors"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/aryan-binazir/ride-home-router/internal/distance"
	"github.com/aryan-binazir/ride-home-router/internal/models"
)

const (
	assignmentBatchSize       = 32
	maxExtraAssignmentWorkers = 3
)

var assignmentWorkerSlots = make(chan struct{}, maxExtraAssignmentWorkers)

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
	}, driverIDs, false)
	return assignmentEvaluation{stops: stops, score: score, err: err}
}

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

type assignmentChange struct {
	firstDriverID, secondDriverID int64
	firstStops, secondStops       []*models.Participant
	score                         solutionScore
	found                         bool
}

func optimizeAssignments(ctx context.Context, rc routeContext, routes map[int64]*balancedRoute, driverIDs []int64) (int, error) {
	slices.Sort(driverIDs)
	candidateEvaluations := 0
	routeMetrics := make(map[int64]routeObjectiveMetrics, len(driverIDs))
	for _, driverID := range driverIDs {
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		default:
		}

		metrics, err := rc.evaluateRouteObjective(ctx, routes[driverID].driver, routes[driverID].stops)
		if err != nil {
			return 0, err
		}
		routeMetrics[driverID] = metrics
	}

	const maxIterations = 50
	for iteration := range maxIterations {
		select {
		case <-ctx.Done():
			return iteration, ctx.Err()
		default:
		}

		currentScore := scoreSolution(routeMetrics, driverIDs)
		best := assignmentChange{}
		budgetExhausted := false

		_, memoized := rc.distanceCalc.(*solveDistanceLookup)
		parallel := memoized && runtime.GOMAXPROCS(0) > 1
		var pending []assignmentCandidate
		if parallel {
			pending = make([]assignmentCandidate, 0, assignmentBatchSize)
		}
		accept := func(candidate assignmentCandidate, result assignmentEvaluation) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if result.panicked {
				panic(result.panicValue)
			}
			if result.err == errUncachedCandidate {
				result = candidate.evaluate(ctx, rc, routes, routeMetrics, driverIDs)
			}
			if result.err != nil {
				return result.err
			}
			if !result.score.betterThan(currentScore) || best.found && !result.score.betterThan(best.score) {
				return nil
			}
			best = assignmentChange{
				firstDriverID: candidate.firstDriverID, secondDriverID: candidate.secondDriverID,
				firstStops: result.stops[candidate.firstDriverID], secondStops: result.stops[candidate.secondDriverID], score: result.score, found: true,
			}
			return nil
		}
		flush := func() error {
			if len(pending) == 0 {
				return ctx.Err()
			}
			results := evaluateAssignmentBatch(ctx, rc, routes, routeMetrics, driverIDs, pending)
			misses := 0
			for _, result := range results {
				if result.err == errUncachedCandidate {
					misses++
				}
			}
			if misses*2 > len(pending) {
				parallel = false
			}
			for i, candidate := range pending {
				if err := accept(candidate, results[i]); err != nil {
					return err
				}
			}
			clear(pending)
			pending = pending[:0]
			return nil
		}
		considerCandidate := func(candidate assignmentCandidate) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if candidateEvaluations >= maxAssignmentCandidateEvaluations {
				budgetExhausted = true
				return nil
			}
			candidateEvaluations++
			if !parallel {
				return accept(candidate, candidate.evaluate(ctx, rc, routes, routeMetrics, driverIDs))
			}
			pending = append(pending, candidate)
			if len(pending) == assignmentBatchSize {
				return flush()
			}
			return nil
		}
		consider := func(firstDriverID, secondDriverID int64, firstStops, secondStops []*models.Participant) error {
			return considerCandidate(assignmentCandidate{firstDriverID: firstDriverID, secondDriverID: secondDriverID, firstStops: firstStops, secondStops: secondStops})
		}

	routeActivationSearch:
		for _, sourceDriverID := range driverIDs {
			sourceRoute := routes[sourceDriverID]
			sourceBlocks := rc.routeHouseholdBlocks(sourceRoute.stops)
			if len(sourceBlocks) < 2 {
				continue
			}
			for _, sourceBlock := range sourceBlocks {
				groupSize := sourceBlock.end - sourceBlock.start
				for _, destinationDriverID := range driverIDs {
					select {
					case <-ctx.Done():
						return iteration, ctx.Err()
					default:
					}

					destinationRoute := routes[destinationDriverID]
					if len(destinationRoute.stops) != 0 || groupSize > destinationRoute.driver.VehicleCapacity {
						continue
					}
					newSourceStops := removeRange(sourceRoute.stops, sourceBlock.start, sourceBlock.end)
					newDestinationStops := slices.Clone(sourceRoute.stops[sourceBlock.start:sourceBlock.end])
					if err := consider(sourceDriverID, destinationDriverID, newSourceStops, newDestinationStops); err != nil {
						return iteration, err
					}
					if budgetExhausted {
						break routeActivationSearch
					}
				}
			}
		}

		if err := flush(); err != nil {
			return iteration, err
		}

		if !best.found && !budgetExhausted {
		relocationSearch:
			for _, sourceDriverID := range driverIDs {
				sourceRoute := routes[sourceDriverID]
				for _, sourceBlock := range rc.routeHouseholdBlocks(sourceRoute.stops) {
					groupSize := sourceBlock.end - sourceBlock.start
					members := sourceRoute.stops[sourceBlock.start:sourceBlock.end]
					for _, destinationDriverID := range driverIDs {
						select {
						case <-ctx.Done():
							return iteration, ctx.Err()
						default:
						}

						if destinationDriverID == sourceDriverID {
							continue
						}
						destinationRoute := routes[destinationDriverID]
						if len(destinationRoute.stops)+groupSize > destinationRoute.driver.VehicleCapacity {
							continue
						}

						for _, destinationPosition := range rc.householdBoundaryPositions(destinationRoute.stops) {
							newSourceStops := removeRange(sourceRoute.stops, sourceBlock.start, sourceBlock.end)
							newDestinationStops := insertParticipantsAt(destinationRoute.stops, members, destinationPosition)
							if err := consider(sourceDriverID, destinationDriverID, newSourceStops, newDestinationStops); err != nil {
								return iteration, err
							}
							if budgetExhausted {
								break relocationSearch
							}
						}
					}
				}
			}

		swapSearch:
			for firstIndex, firstDriverID := range driverIDs {
				if budgetExhausted {
					break
				}
				firstRoute := routes[firstDriverID]
				for _, firstBlock := range rc.routeHouseholdBlocks(firstRoute.stops) {
					firstSize := firstBlock.end - firstBlock.start
					firstMembers := firstRoute.stops[firstBlock.start:firstBlock.end]
					for _, secondDriverID := range driverIDs[firstIndex+1:] {
						select {
						case <-ctx.Done():
							return iteration, ctx.Err()
						default:
						}

						secondRoute := routes[secondDriverID]
						for _, secondBlock := range rc.routeHouseholdBlocks(secondRoute.stops) {
							select {
							case <-ctx.Done():
								return iteration, ctx.Err()
							default:
							}

							secondSize := secondBlock.end - secondBlock.start
							if len(firstRoute.stops)-firstSize+secondSize <= firstRoute.driver.VehicleCapacity &&
								len(secondRoute.stops)-secondSize+firstSize <= secondRoute.driver.VehicleCapacity {
								secondMembers := secondRoute.stops[secondBlock.start:secondBlock.end]
								newFirstStops := replaceRangeWithParticipants(firstRoute.stops, firstBlock.start, firstBlock.end, secondMembers)
								newSecondStops := replaceRangeWithParticipants(secondRoute.stops, secondBlock.start, secondBlock.end, firstMembers)
								if err := consider(firstDriverID, secondDriverID, newFirstStops, newSecondStops); err != nil {
									return iteration, err
								}
								if budgetExhausted {
									break swapSearch
								}
							}
						}
					}
				}
			}
		}

		if err := flush(); err != nil {
			return iteration, err
		}

		if err := flush(); err != nil {
			return iteration, err
		}

		if !best.found {
			return iteration, nil
		}
		firstRoute := routes[best.firstDriverID]
		secondRoute := routes[best.secondDriverID]
		firstRoute.stops = best.firstStops
		secondRoute.stops = best.secondStops

		if err := rc.optimizeRouteOrders(ctx, routes, driverIDs); err != nil {
			return iteration, err
		}
		for _, driverID := range driverIDs {
			metrics, err := rc.evaluateRouteObjective(ctx, routes[driverID].driver, routes[driverID].stops)
			if err != nil {
				return iteration, err
			}
			routeMetrics[driverID] = metrics
		}
		if budgetExhausted {
			return iteration + 1, nil
		}
	}

	return maxIterations, nil
}

func replaceRangeWithParticipants(stops []*models.Participant, start, end int, members []*models.Participant) []*models.Participant {
	result := make([]*models.Participant, len(stops)-(end-start)+len(members))
	copy(result, stops[:start])
	copy(result[start:], members)
	copy(result[start+len(members):], stops[end:])
	return result
}
