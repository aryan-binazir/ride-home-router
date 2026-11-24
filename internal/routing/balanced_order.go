package routing

import (
	"context"
	"math"
	"slices"

	"github.com/aryan-binazir/ride-home-router/internal/models"
)

func (rc routeContext) optimizeRouteOrders(ctx context.Context, routes map[int64]*balancedRoute, driverIDs []int64) error {
	_, err := rc.optimizeRouteOrdersWith(ctx, routes, driverIDs, true)
	return err
}

func (rc routeContext) optimizeRouteOrdersWith(ctx context.Context, routes map[int64]*balancedRoute, driverIDs []int64, relocations bool) (solutionScore, error) {
	routeMetrics := make(map[int64]routeObjectiveMetrics, len(driverIDs))
	candidateStops := make(map[int64][]*models.Participant, len(driverIDs))
	for _, driverID := range driverIDs {
		route := routes[driverID]
		stops := rc.coalesceHouseholdStops(route.stops)
		metrics, err := rc.evaluateRouteObjective(ctx, route.driver, stops)
		if err != nil {
			return solutionScore{}, err
		}
		routeMetrics[driverID] = metrics
		candidateStops[driverID] = stops
	}

	optimizedStops, score, err := rc.optimizeStopsForSolution(ctx, routes, routeMetrics, candidateStops, driverIDs, relocations)
	if err != nil {
		return solutionScore{}, err
	}
	for _, driverID := range driverIDs {
		routes[driverID].stops = optimizedStops[driverID]
	}
	return score, nil
}

const maxRelocationEvaluations = 10000

func (rc routeContext) optimizeStopsForSolution(
	ctx context.Context,
	routes map[int64]*balancedRoute,
	baseMetrics map[int64]routeObjectiveMetrics,
	changedStops map[int64][]*models.Participant,
	driverIDs []int64,
	relocations bool,
) (map[int64][]*models.Participant, solutionScore, error) {
	relocationEvaluations := 0
	currentStops := make(map[int64][]*models.Participant, len(changedStops))
	currentMetrics := make([]routeObjectiveMetrics, len(driverIDs))
	for i, id := range driverIDs {
		currentMetrics[i] = baseMetrics[id]
	}
	for driverID, stops := range changedStops {
		select {
		case <-ctx.Done():
			return nil, solutionScore{}, ctx.Err()
		default:
		}

		stops = rc.coalesceHouseholdStops(stops)
		metrics, err := rc.evaluateRouteObjective(ctx, routes[driverID].driver, stops)
		if err != nil {
			return nil, solutionScore{}, err
		}
		currentStops[driverID] = stops
		currentMetrics[slices.Index(driverIDs, driverID)] = metrics
	}

	currentScore := scoreOrderedSolution(currentMetrics)
	const maxOrderIterations = 50
	for range maxOrderIterations {
		select {
		case <-ctx.Done():
			return nil, solutionScore{}, ctx.Err()
		default:
		}

		bestDriverID := int64(0)
		bestDriverIndex := 0
		var bestStops []*models.Participant
		var bestMetrics routeObjectiveMetrics
		bestScore := currentScore
		found := false

		for driverIndex, driverID := range driverIDs {
			stops, affected := currentStops[driverID]
			if !affected {
				continue
			}
			blocks := rc.routeHouseholdBlocks(stops)
			candidateBlocks := make([]householdBlock, len(blocks))
			candidateStops := make([]*models.Participant, 0, len(stops))
			otherLatest, otherDetour := 0.0, math.Inf(-1)
			prefixCompletion, prefixDuration := 0.0, 0.0
			for i, metrics := range currentMetrics {
				if i < driverIndex && metrics.used {
					prefixCompletion += metrics.aggregateParticipantCompletion
					prefixDuration += metrics.driveDuration
				}
				if i != driverIndex && metrics.used {
					otherLatest = max(otherLatest, metrics.latestParticipantCompletion)
					otherDetour = max(otherDetour, metrics.driverDetour)
				}
			}
			consider := func() error {
				select {
				case <-ctx.Done():
					return ctx.Err()
				default:
				}
				candidateStops = candidateStops[:0]
				for _, block := range candidateBlocks {
					candidateStops = append(candidateStops, stops[block.start:block.end]...)
				}
				candidateMetrics, err := rc.evaluateRouteObjective(ctx, routes[driverID].driver, candidateStops, currentMetrics[driverIndex].corridorSpread)
				if err != nil {
					return err
				}

				prefix := currentScore
				prefix.latestParticipantCompletion = max(otherLatest, candidateMetrics.latestParticipantCompletion)
				prefix.maxDriverDetour = max(otherDetour, candidateMetrics.driverDetour)
				if better, decided := prefix.comparePrefix(currentScore); decided && !better {
					return nil
				}
				if found {
					if better, decided := prefix.comparePrefix(bestScore); decided && !better {
						return nil
					}
				}
				previousMetrics := currentMetrics[driverIndex]
				currentMetrics[driverIndex] = candidateMetrics
				candidateScore := prefix
				candidateScore.aggregateParticipantCompletion = prefixCompletion
				candidateScore.aggregateDriveDuration = prefixDuration
				for _, metrics := range currentMetrics[driverIndex:] {
					if metrics.used {
						candidateScore.aggregateParticipantCompletion += metrics.aggregateParticipantCompletion
						candidateScore.aggregateDriveDuration += metrics.driveDuration
					}
				}
				currentMetrics[driverIndex] = previousMetrics
				if !candidateScore.betterThan(currentScore) || found && !candidateScore.betterThan(bestScore) {
					return nil
				}
				bestDriverID = driverID
				bestDriverIndex = driverIndex
				bestStops = slices.Clone(candidateStops)
				bestMetrics = candidateMetrics
				bestScore = candidateScore
				found = true
				return nil
			}
			for i := 0; i < len(blocks)-1; i++ {
				for j := i + 2; j <= len(blocks); j++ {
					copy(candidateBlocks, blocks)
					slices.Reverse(candidateBlocks[i:j])
					if err := consider(); err != nil {
						return nil, solutionScore{}, err
					}
				}
			}
			if !relocations {
				continue
			}
			for from := range blocks {
				for to := 0; to <= len(blocks); to++ {
					if blockRelocationAlreadyTried(from, to) || relocationEvaluations >= maxRelocationEvaluations {
						continue
					}
					relocationEvaluations++
					candidateBlocks = candidateBlocks[:0]
					for k, block := range blocks {
						if k == to {
							candidateBlocks = append(candidateBlocks, blocks[from])
						}
						if k != from {
							candidateBlocks = append(candidateBlocks, block)
						}
					}
					if to == len(blocks) {
						candidateBlocks = append(candidateBlocks, blocks[from])
					}
					if err := consider(); err != nil {
						return nil, solutionScore{}, err
					}
				}
			}
		}

		if !found {
			return currentStops, currentScore, nil
		}
		currentStops[bestDriverID] = bestStops
		currentMetrics[bestDriverIndex] = bestMetrics
		currentScore = bestScore
	}

	return currentStops, currentScore, nil
}

func blockRelocationAlreadyTried(from, to int) bool {
	return to == from || to == from+1 || to == from+2 || to == from-1
}
