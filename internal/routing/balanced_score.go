package routing

import (
	"context"
	"fmt"
	"math"
	"sort"

	"github.com/aryan-binazir/ride-home-router/internal/models"
)

type routeObjectiveMetrics struct {
	latestParticipantCompletion    float64
	aggregateParticipantCompletion float64
	driverDetour                   float64
	driveDuration                  float64
	corridorSpread                 int
	used                           bool
}

type solutionScore struct {
	corridorSpread                 int
	latestParticipantCompletion    float64
	maxDriverDetour                float64
	aggregateParticipantCompletion float64
	aggregateDriveDuration         float64
	usedDrivers                    int
}

func (score solutionScore) comparePrefix(other solutionScore) (better, decided bool) {
	if score.usedDrivers != other.usedDrivers {
		return score.usedDrivers > other.usedDrivers, true
	}
	if score.corridorSpread != other.corridorSpread {
		return score.corridorSpread < other.corridorSpread, true
	}
	for _, values := range [][2]float64{{score.latestParticipantCompletion, other.latestParticipantCompletion}, {score.maxDriverDetour, other.maxDriverDetour}} {
		if values[0] < values[1]-scoreImprovementEpsilon {
			return true, true
		}
		if values[0] > values[1]+scoreImprovementEpsilon {
			return false, true
		}
	}
	return false, false
}

func (score solutionScore) betterThan(other solutionScore) bool {
	if better, decided := score.comparePrefix(other); decided {
		return better
	}
	for _, values := range [][2]float64{{score.aggregateParticipantCompletion, other.aggregateParticipantCompletion}, {score.aggregateDriveDuration, other.aggregateDriveDuration}} {
		if values[0] < values[1]-scoreImprovementEpsilon {
			return true
		}
		if values[0] > values[1]+scoreImprovementEpsilon {
			return false
		}
	}
	return false
}

func (rc routeContext) evaluateRouteObjective(ctx context.Context, driver *models.Driver, stops []*models.Participant, knownSpread ...int) (routeObjectiveMetrics, error) {
	if len(stops) == 0 {
		return routeObjectiveMetrics{}, nil
	}
	if driver == nil {
		return routeObjectiveMetrics{}, fmt.Errorf("route driver is required")
	}
	result := routeObjectiveMetrics{used: true}
	prev := rc.origin(driver)
	cumulative := 0.0
	sumOfPickupTimes := 0.0
	firstRiderBoardedAt := -1.0
	for i, stop := range stops {
		if stop == nil {
			return routeObjectiveMetrics{}, fmt.Errorf("route stop %d is missing participant data", i)
		}
		leg, err := rc.distanceValue(ctx, prev, stop.GetCoords())
		if err != nil {
			return routeObjectiveMetrics{}, err
		}
		cumulative += leg.DurationSecs
		if rc.mode != RouteModePickup {
			result.latestParticipantCompletion = max(result.latestParticipantCompletion, cumulative)
			result.aggregateParticipantCompletion += cumulative
		}
		if rc.mode == RouteModePickup {
			sumOfPickupTimes += cumulative
			if firstRiderBoardedAt < 0 {
				firstRiderBoardedAt = cumulative
			}
		}
		prev = stop.GetCoords()
	}
	finalLeg, err := rc.distanceValue(ctx, prev, rc.destination(driver))
	if err != nil {
		return routeObjectiveMetrics{}, err
	}
	baseline, err := rc.distanceValue(ctx, rc.origin(driver), rc.destination(driver))
	if err != nil {
		return routeObjectiveMetrics{}, err
	}
	result.driveDuration = cumulative + finalLeg.DurationSecs
	result.driverDetour = result.driveDuration - baseline.DurationSecs
	if len(knownSpread) != 0 {
		result.corridorSpread = knownSpread[0]
	} else {
		result.corridorSpread = int(math.Round(rc.routeCorridorSpread(stops) / 10.0))
	}
	if rc.mode == RouteModePickup {
		result.latestParticipantCompletion, result.aggregateParticipantCompletion = pickupRiderTimeAboard(result.driveDuration, firstRiderBoardedAt, sumOfPickupTimes, len(stops))
	}
	return result, nil
}

func pickupRiderTimeAboard(driveDuration, firstRiderBoardedAt, sumOfPickupTimes float64, riders int) (longest, total float64) {
	return driveDuration - firstRiderBoardedAt, driveDuration*float64(riders) - sumOfPickupTimes
}

func scoreSolution(routeMetrics map[int64]routeObjectiveMetrics, driverIDs []int64) solutionScore {
	result := solutionScore{maxDriverDetour: math.Inf(-1)}
	for _, driverID := range driverIDs {
		metrics := routeMetrics[driverID]
		if !metrics.used {
			continue
		}
		result.latestParticipantCompletion = max(result.latestParticipantCompletion, metrics.latestParticipantCompletion)
		result.maxDriverDetour = max(result.maxDriverDetour, metrics.driverDetour)
		result.aggregateParticipantCompletion += metrics.aggregateParticipantCompletion
		result.aggregateDriveDuration += metrics.driveDuration
		result.corridorSpread += metrics.corridorSpread
		result.usedDrivers++
	}
	if result.usedDrivers == 0 {
		result.maxDriverDetour = 0
	}
	return result
}

func scoreOrderedSolution(routeMetrics []routeObjectiveMetrics) solutionScore {
	result := solutionScore{maxDriverDetour: math.Inf(-1)}
	for _, metrics := range routeMetrics {
		if !metrics.used {
			continue
		}
		result.latestParticipantCompletion = max(result.latestParticipantCompletion, metrics.latestParticipantCompletion)
		result.maxDriverDetour = max(result.maxDriverDetour, metrics.driverDetour)
		result.aggregateParticipantCompletion += metrics.aggregateParticipantCompletion
		result.aggregateDriveDuration += metrics.driveDuration
		result.corridorSpread += metrics.corridorSpread
		result.usedDrivers++
	}
	if result.usedDrivers == 0 {
		result.maxDriverDetour = 0
	}
	return result
}

func bearingFromInstitute(institute, coordinate models.Coordinates) float64 {
	instituteLatitudeRadians := institute.Lat * math.Pi / 180
	deltaLatitude := coordinate.Lat - institute.Lat
	deltaLongitude := math.Remainder(coordinate.Lng-institute.Lng, 360)
	bearing := math.Atan2(deltaLongitude*math.Cos(instituteLatitudeRadians), deltaLatitude) * 180 / math.Pi
	return math.Mod(bearing+360, 360)
}

func (rc routeContext) routeCorridorSpread(stops []*models.Participant) float64 {
	if len(stops) <= 1 {
		return 0
	}

	bearings := make([]float64, len(stops))
	for i, stop := range stops {
		if facts, ok := rc.participants[stop]; ok {
			bearings[i] = facts.bearing
		} else {
			bearings[i] = bearingFromInstitute(rc.instituteCoords, stop.GetCoords())
		}
	}
	sort.Float64s(bearings)

	largestGap := bearings[0] + 360 - bearings[len(bearings)-1]
	for i := 1; i < len(bearings); i++ {
		largestGap = max(largestGap, bearings[i]-bearings[i-1])
	}
	return 360 - largestGap
}
