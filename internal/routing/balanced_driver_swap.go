package routing

import (
	"context"
	"slices"
	"time"

	"github.com/aryan-binazir/ride-home-router/internal/models"
)

func optimizeDriverAssignments(ctx context.Context, rc routeContext, routes map[int64]*balancedRoute, driverIDs []int64) (int, error) {
	const (
		maxPairEvaluations = 100000
		softBudget         = 2 * time.Second
	)
	maxPasses := max(20, len(driverIDs))
	deadline := time.Now().Add(softBudget)
	routeMetrics := make(map[int64]routeObjectiveMetrics, len(driverIDs))
	for _, driverID := range driverIDs {
		metrics, err := rc.evaluateRouteObjective(ctx, routes[driverID].driver, routes[driverID].stops)
		if err != nil {
			return 0, err
		}
		routeMetrics[driverID] = metrics
	}
	baseline := scoreSolution(routeMetrics, driverIDs)
	evaluations := 0
	swaps := 0
	for range maxPasses {
		currentScore := scoreSolution(routeMetrics, driverIDs)
		var best struct {
			first, second   int64
			firstM, secondM routeObjectiveMetrics
			score           solutionScore
			found           bool
		}
		for firstIndex, firstDriverID := range driverIDs {
			firstRoute := routes[firstDriverID]
			for _, secondDriverID := range driverIDs[firstIndex+1:] {
				if err := ctx.Err(); err != nil {
					return swaps, err
				}
				if evaluations >= maxPairEvaluations || time.Now().After(deadline) {
					break
				}
				secondRoute := routes[secondDriverID]
				if len(firstRoute.stops) == 0 && len(secondRoute.stops) == 0 {
					continue
				}
				if len(secondRoute.stops) > firstRoute.driver.VehicleCapacity || len(firstRoute.stops) > secondRoute.driver.VehicleCapacity {
					continue
				}
				evaluations++
				firstM, err := rc.evaluateRouteObjective(ctx, firstRoute.driver, secondRoute.stops)
				if err != nil {
					return swaps, err
				}
				secondM, err := rc.evaluateRouteObjective(ctx, secondRoute.driver, firstRoute.stops)
				if err != nil {
					return swaps, err
				}
				previousFirst, previousSecond := routeMetrics[firstDriverID], routeMetrics[secondDriverID]
				routeMetrics[firstDriverID], routeMetrics[secondDriverID] = firstM, secondM
				score := scoreSolution(routeMetrics, driverIDs)
				routeMetrics[firstDriverID], routeMetrics[secondDriverID] = previousFirst, previousSecond
				if !score.betterThan(currentScore) || score.aggregateDriveDuration > currentScore.aggregateDriveDuration+scoreImprovementEpsilon {
					continue
				}
				if best.found && !score.betterThan(best.score) {
					continue
				}
				best.first, best.second, best.firstM, best.secondM, best.score, best.found = firstDriverID, secondDriverID, firstM, secondM, score, true
			}
		}
		if !best.found {
			break
		}
		routes[best.first].stops, routes[best.second].stops = routes[best.second].stops, routes[best.first].stops
		routeMetrics[best.first], routeMetrics[best.second] = best.firstM, best.secondM
		swaps++
		if evaluations >= maxPairEvaluations || time.Now().After(deadline) {
			break
		}
	}
	if swaps == 0 {
		return 0, nil
	}
	fixedOrder := make(map[int64][]*models.Participant, len(driverIDs))
	for _, driverID := range driverIDs {
		fixedOrder[driverID] = slices.Clone(routes[driverID].stops)
	}
	if err := rc.optimizeRouteOrders(ctx, routes, driverIDs); err != nil {
		return swaps, err
	}
	for _, driverID := range driverIDs {
		metrics, err := rc.evaluateRouteObjective(ctx, routes[driverID].driver, routes[driverID].stops)
		if err != nil {
			return swaps, err
		}
		routeMetrics[driverID] = metrics
	}
	if scoreSolution(routeMetrics, driverIDs).aggregateDriveDuration > baseline.aggregateDriveDuration+scoreImprovementEpsilon {
		for _, driverID := range driverIDs {
			routes[driverID].stops = fixedOrder[driverID]
		}
	}
	return swaps, nil
}
