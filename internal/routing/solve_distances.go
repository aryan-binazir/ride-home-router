package routing

import (
	"context"
	"errors"
	"math"
	"ride-home-router/internal/distance"
	"ride-home-router/internal/models"
)

// MaxUncachedDistancePairs is the per-calculation billing ceiling.
const MaxUncachedDistancePairs = distance.MaxUncachedDistancePairs

var ErrTooManyDistancePairs = distance.ErrTooManyDistancePairs

// prepareSolveDistances prewarms and memoizes one solve's directed pairs.
func prepareSolveDistances(ctx context.Context, source distance.SolveSource, req *RoutingRequest) (distance.Lookup, error) {
	pairs, err := collectSolveDistancePairs(ctx, normalizeRouteMode(req.Mode), req.InstituteCoords, req.Participants, req.Drivers)
	if err != nil {
		return nil, err
	}
	if len(pairs) > 0 {
		if err := source.PrewarmPairs(ctx, pairs); err != nil {
			return nil, err
		}
	}

	return &solveDistanceLookup{
		source: source,
		values: make(map[solvePairKey]distance.DistanceResult),
	}, nil
}

func collectSolveDistancePairs(ctx context.Context, mode RouteMode, institute models.Coordinates, participants []models.Participant, drivers []models.Driver) ([]distance.DistancePair, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Round each location once; pair enumeration is quadratic in locations.
	type point struct {
		coords models.Coordinates
		key    [2]uint64
	}
	preparePoint := func(coords models.Coordinates) point {
		key := makeSolvePairKey(coords, coords)
		return point{coords: coords, key: [2]uint64{key[0], key[1]}}
	}
	institutePoint := preparePoint(institute)
	// Bound the capacity hint so large requests do not trigger an excessive
	// eager allocation. Count unique rounded points for shared households.
	participantKeys := make(map[[2]uint64]struct{}, len(participants))
	for i := range participants {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		participantKeys[preparePoint(participants[i].GetCoords()).key] = struct{}{}
	}
	driverKeys := make(map[[2]uint64]struct{}, len(drivers))
	for i := range drivers {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		driverKeys[preparePoint(drivers[i].GetCoords()).key] = struct{}{}
	}
	n, d := min(len(participantKeys), 1024), min(len(driverKeys), 1024)
	hint := min(n*n+n*d+d, 1<<20)
	seen := make(map[solvePairKey]struct{}, hint)
	pairs := make([]distance.DistancePair, 0, hint)

	addPair := func(origin, dest point) {
		// Float comparison intentionally treats signed zeros as the same point
		// and NaNs as different, matching distance.SamePoint.
		if math.Float64frombits(origin.key[0]) == math.Float64frombits(dest.key[0]) &&
			math.Float64frombits(origin.key[1]) == math.Float64frombits(dest.key[1]) {
			return
		}
		key := solvePairKey{origin.key[0], origin.key[1], dest.key[0], dest.key[1]}
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		pairs = append(pairs, distance.DistancePair{Origin: origin.coords, Destination: dest.coords})
	}

	participantCoords := make([]point, len(participants))
	for i := range participants {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		participantCoords[i] = preparePoint(participants[i].GetCoords())
	}

	driverCoords := make([]point, len(drivers))
	for i := range drivers {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		driverCoords[i] = preparePoint(drivers[i].GetCoords())
	}

	if mode == RouteModePickup {
		for _, driverCoord := range driverCoords {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			for _, participantCoord := range participantCoords {
				addPair(driverCoord, participantCoord)
			}
			addPair(driverCoord, institutePoint)
		}
		for i := range participantCoords {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			for j := range participantCoords {
				if i == j {
					continue
				}
				addPair(participantCoords[i], participantCoords[j])
			}
			addPair(participantCoords[i], institutePoint)
		}
		return pairs, nil
	}

	for _, participantCoord := range participantCoords {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		addPair(institutePoint, participantCoord)
	}
	for i := range participantCoords {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for j := range participantCoords {
			if i == j {
				continue
			}
			addPair(participantCoords[i], participantCoords[j])
		}
		for _, driverCoord := range driverCoords {
			addPair(participantCoords[i], driverCoord)
		}
	}
	for _, driverCoord := range driverCoords {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		addPair(institutePoint, driverCoord)
	}

	return pairs, nil
}

// solveDistanceLookup is isolated to one synchronous routing solve.
type solveDistanceLookup struct {
	source distance.Lookup
	values map[solvePairKey]distance.DistanceResult
}

// Numeric keys preserve the persistent key's rounding and signed zero while
// avoiding formatting on every search edge. All NaN spellings share one key.
type solvePairKey [4]uint64

func makeSolvePairKey(origin, dest models.Coordinates) solvePairKey {
	values := [4]float64{origin.Lat, origin.Lng, dest.Lat, dest.Lng}
	var key solvePairKey
	for i, value := range values {
		value = models.RoundCoordinate(value)
		if math.IsNaN(value) {
			key[i] = 0x7ff8000000000000
		} else {
			key[i] = math.Float64bits(value)
		}
	}
	return key
}

func (l *solveDistanceLookup) GetDistance(ctx context.Context, origin, dest models.Coordinates) (*distance.DistanceResult, error) {
	value, err := l.distanceValue(ctx, origin, dest)
	if err != nil {
		return nil, err
	}
	return &value, nil
}

func (l *solveDistanceLookup) distanceValue(ctx context.Context, origin, dest models.Coordinates) (distance.DistanceResult, error) {
	if err := ctx.Err(); err != nil {
		return distance.DistanceResult{}, err
	}
	key := makeSolvePairKey(origin, dest)
	if cached, ok := l.values[key]; ok {
		return cached, nil
	}
	result, err := l.source.GetDistance(ctx, origin, dest)
	if err != nil {
		return distance.DistanceResult{}, err
	}
	if result == nil {
		return distance.DistanceResult{}, errors.New("distance lookup returned no result")
	}
	l.values[key] = *result
	return *result, nil
}
