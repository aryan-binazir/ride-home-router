package routing

import (
	"context"
	"ride-home-router/internal/distance"
	"ride-home-router/internal/models"
)

// prepareSolveDistances collects and prewarms the directed pairs needed by one
// solve, then returns an isolated lookup that loads each pair at most once.
func prepareSolveDistances(ctx context.Context, source distance.SolveSource, req *RoutingRequest, mode RouteMode) (distance.Lookup, error) {
	pairs := collectSolveDistancePairs(mode, req.InstituteCoords, req.Participants, req.Drivers)
	if len(pairs) > 0 {
		if err := source.PrewarmPairs(ctx, pairs); err != nil {
			return nil, err
		}
	}

	return newSolveDistanceLookup(source), nil
}

func collectSolveDistancePairs(mode RouteMode, institute models.Coordinates, participants []models.Participant, drivers []models.Driver) []distance.DistancePair {
	seen := make(map[string]struct{})
	pairs := make([]distance.DistancePair, 0)

	addPair := func(origin, dest models.Coordinates) {
		if models.RoundCoordinate(origin.Lat) == models.RoundCoordinate(dest.Lat) &&
			models.RoundCoordinate(origin.Lng) == models.RoundCoordinate(dest.Lng) {
			return
		}
		key := distance.PairCacheKey(origin, dest)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		pairs = append(pairs, distance.DistancePair{Origin: origin, Destination: dest})
	}

	participantCoords := make([]models.Coordinates, len(participants))
	for i := range participants {
		participantCoords[i] = participants[i].GetCoords()
	}

	driverCoords := make([]models.Coordinates, len(drivers))
	for i := range drivers {
		driverCoords[i] = drivers[i].GetCoords()
	}

	if mode == RouteModePickup {
		for _, driverCoord := range driverCoords {
			for _, participantCoord := range participantCoords {
				addPair(driverCoord, participantCoord)
			}
			addPair(driverCoord, institute)
		}
		for i := range participantCoords {
			for j := range participantCoords {
				if i == j {
					continue
				}
				addPair(participantCoords[i], participantCoords[j])
			}
			addPair(participantCoords[i], institute)
		}
		return pairs
	}

	for _, participantCoord := range participantCoords {
		addPair(institute, participantCoord)
	}
	for i := range participantCoords {
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
		addPair(institute, driverCoord)
	}

	return pairs
}

// solveDistanceLookup is isolated to one synchronous routing solve.
type solveDistanceLookup struct {
	source distance.Lookup
	values map[string]distance.DistanceResult
}

func newSolveDistanceLookup(source distance.Lookup) *solveDistanceLookup {
	return &solveDistanceLookup{
		source: source,
		values: make(map[string]distance.DistanceResult),
	}
}

func (l *solveDistanceLookup) GetDistance(ctx context.Context, origin, dest models.Coordinates) (*distance.DistanceResult, error) {
	key := distance.PairCacheKey(origin, dest)
	if cached, ok := l.values[key]; ok {
		result := cached
		return &result, nil
	}

	result, err := l.source.GetDistance(ctx, origin, dest)
	if err != nil {
		return nil, err
	}
	l.values[key] = *result
	copy := *result
	return &copy, nil
}
