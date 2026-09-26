package routesession

import (
	"context"
	"fmt"
	"ride-home-router/internal/models"
	"strings"
)

const (
	ChangeParticipantMoved = "participant_moved"
	ChangeDriversSwapped   = "drivers_swapped"
	ChangeDriverReplaced   = "driver_replaced"
)

// RouteChange names are for display only; feedback records keep the IDs.
type RouteChange struct {
	Kind            string
	ParticipantID   int64
	ParticipantName string
	FromDriverID    int64
	FromDriverName  string
	ToDriverID      int64
	ToDriverName    string
}

func (c RouteChange) Description() string {
	switch c.Kind {
	case ChangeParticipantMoved:
		return fmt.Sprintf("%s moved from %s to %s.", c.ParticipantName, c.FromDriverName, c.ToDriverName)
	case ChangeDriversSwapped:
		return fmt.Sprintf("%s and %s swapped routes.", c.FromDriverName, c.ToDriverName)
	case ChangeDriverReplaced:
		return fmt.Sprintf("%s took over %s's route.", c.ToDriverName, c.FromDriverName)
	}
	return ""
}

func (s *Store) SetReviewerNote(ctx context.Context, id, note string) (Snapshot, error) {
	return s.update(ctx, id, func(state *session) (Snapshot, error) {
		state.reviewerNote = strings.TrimSpace(note)
		return snapshotOf(state), nil
	})
}

func routeChanges(original, current []models.CalculatedRoute) []RouteChange {
	changes := make([]RouteChange, 0)
	swapped := make(map[int]bool)
	for i := range current {
		if swapped[i] || i >= len(original) || driverID(current[i].Driver) == driverID(original[i].Driver) {
			continue
		}
		for j := i + 1; j < len(current) && j < len(original); j++ {
			if !swapped[j] && driverID(current[i].Driver) == driverID(original[j].Driver) && driverID(current[j].Driver) == driverID(original[i].Driver) && current[i].Driver != nil && current[j].Driver != nil {
				swapped[i], swapped[j] = true, true
				changes = append(changes, RouteChange{Kind: ChangeDriversSwapped, FromDriverID: original[i].Driver.ID, FromDriverName: original[i].Driver.Name, ToDriverID: original[j].Driver.ID, ToDriverName: original[j].Driver.Name})
				break
			}
		}
		if !swapped[i] && current[i].Driver != nil && original[i].Driver != nil {
			swapped[i] = true
			changes = append(changes, RouteChange{Kind: ChangeDriverReplaced, FromDriverID: original[i].Driver.ID, FromDriverName: original[i].Driver.Name, ToDriverID: current[i].Driver.ID, ToDriverName: current[i].Driver.Name})
		}
	}
	currentIndex := make(map[int64]int)
	for i, route := range current {
		for _, stop := range route.Stops {
			if stop.Participant != nil {
				currentIndex[stop.Participant.ID] = i
			}
		}
	}
	for i, route := range original {
		for _, stop := range route.Stops {
			if stop.Participant == nil {
				continue
			}
			to, ok := currentIndex[stop.Participant.ID]
			if !ok || to == i || current[to].Driver == nil {
				continue
			}
			from := route.Driver
			if from == nil {
				continue
			}
			changes = append(changes, RouteChange{Kind: ChangeParticipantMoved, ParticipantID: stop.Participant.ID, ParticipantName: stop.Participant.Name, FromDriverID: from.ID, FromDriverName: from.Name, ToDriverID: current[to].Driver.ID, ToDriverName: current[to].Driver.Name})
		}
	}
	return changes
}
