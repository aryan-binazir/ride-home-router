package routing

import "ride-home-router/internal/models"

func removeRange(stops []*models.Participant, start, end int) []*models.Participant {
	result := make([]*models.Participant, 0, len(stops)-(end-start))
	result = append(result, stops[:start]...)
	result = append(result, stops[end:]...)
	return result
}

func insertParticipantsAt(stops, members []*models.Participant, pos int) []*models.Participant {
	newStops := make([]*models.Participant, len(stops)+len(members))
	copy(newStops, stops[:pos])
	copy(newStops[pos:], members)
	copy(newStops[pos+len(members):], stops[pos:])
	return newStops
}
