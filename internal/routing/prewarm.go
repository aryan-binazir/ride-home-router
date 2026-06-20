package routing

import (
	"context"

	"ride-home-router/internal/distance"
	"ride-home-router/internal/models"
)

func prewarmRoutingDistances(ctx context.Context, calc distance.DistanceCalculator, req *RoutingRequest, mode RouteMode) error {
	pairs := collectRoutingPrewarmPairs(mode, req.InstituteCoords, req.Participants, req.Drivers)
	return distance.PrewarmRoutingPairs(ctx, calc, pairs)
}

func collectRoutingPrewarmPairs(mode RouteMode, institute models.Coordinates, participants []models.Participant, drivers []models.Driver) []distance.DistancePair {
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
