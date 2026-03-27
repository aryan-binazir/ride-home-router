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
