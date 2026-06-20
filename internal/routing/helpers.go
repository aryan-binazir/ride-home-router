package routing

import "ride-home-router/internal/models"

func removeRange(stops []*models.Participant, start, end int) []*models.Participant {
	result := make([]*models.Participant, 0, len(stops)-(end-start))
	result = append(result, stops[:start]...)
	result = append(result, stops[end:]...)
	return result
}

func reverse(stops []*models.Participant, i, j int) {
	for i < j {
		stops[i], stops[j] = stops[j], stops[i]
		i++
		j--
	}
}

func reverseParticipantGroups(groups []*participantGroup, i, j int) {
	for i < j {
		groups[i], groups[j] = groups[j], groups[i]
		i++
		j--
	}
}

func flattenParticipantGroups(groups []*participantGroup) []*models.Participant {
	total := 0
	for _, group := range groups {
		total += len(group.members)
	}

	stops := make([]*models.Participant, 0, total)
	for _, group := range groups {
		stops = append(stops, group.members...)
	}
	return stops
}
