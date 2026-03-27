package routing

import "ride-home-router/internal/models"

func insertAt(stops []*models.Participant, p *models.Participant, pos int) []*models.Participant {
	result := make([]*models.Participant, len(stops)+1)
	copy(result[:pos], stops[:pos])
	result[pos] = p
	copy(result[pos+1:], stops[pos:])
	return result
}

func removeAt(stops []*models.Participant, pos int) []*models.Participant {
	result := make([]*models.Participant, len(stops)-1)
	copy(result[:pos], stops[:pos])
	copy(result[pos:], stops[pos+1:])
	return result
}

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
