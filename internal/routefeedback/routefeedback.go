// Package routefeedback builds privacy-safe records for offline route analysis.
package routefeedback

import (
	"net/http"
	"ride-home-router/internal/models"
	"ride-home-router/internal/routesession"
	"strings"
)

const (
	// SchemaVersion identifies the JSON payload contract stored in Postgres.
	SchemaVersion = 1
	// AuthenticatedUserEmailHeader is set by Cloudflare Access for authenticated requests.
	AuthenticatedUserEmailHeader = "Cf-Access-Authenticated-User-Email"
)

type (
	Record      = models.RouteFeedbackRecord
	Input       = models.RouteFeedbackInput
	Activity    = models.RouteFeedbackActivity
	Driver      = models.RouteFeedbackDriver
	Participant = models.RouteFeedbackParticipant
	Route       = models.RouteFeedbackRoute
)

// Build projects a commit snapshot into typed DTOs that cannot expose person names or timestamps.
func Build(snapshot routesession.CommitSnapshot) Record {
	input := models.RouteFeedbackInput{
		Drivers:      make([]models.RouteFeedbackDriver, 0, len(snapshot.SelectedDrivers)),
		Participants: participantsFromRoutes(snapshot.Original),
	}
	if snapshot.ActivityLocation != nil {
		input.Activity = models.RouteFeedbackActivity{
			ID: snapshot.ActivityLocation.ID, Lat: snapshot.ActivityLocation.Lat, Lng: snapshot.ActivityLocation.Lng,
		}
	}
	for _, driver := range snapshot.SelectedDrivers {
		inputDriver := models.RouteFeedbackDriver{
			ID: driver.ID, Address: driver.Address, Lat: driver.Lat, Lng: driver.Lng, Capacity: driver.VehicleCapacity,
		}
		if vehicle := snapshot.DriverOrgVehicles[driver.ID]; vehicle != nil {
			inputDriver.OrgVehicleID = new(vehicle.ID)
		}
		input.Drivers = append(input.Drivers, inputDriver)
	}

	return models.RouteFeedbackRecord{
		SessionID: snapshot.SessionID, SchemaVersion: SchemaVersion, Mode: snapshot.Mode,
		Input: input, Proposed: routesFrom(snapshot.Original), Final: routesFrom(snapshot.Final),
	}
}

// ShouldCapture reports whether the request belongs to the configured SME.
func ShouldCapture(r *http.Request, settings *models.Settings) (string, bool) {
	if r == nil || settings == nil {
		return "", false
	}
	configured := strings.TrimSpace(settings.SMEEmail)
	authenticated := strings.TrimSpace(r.Header.Get(AuthenticatedUserEmailHeader))
	if configured == "" || authenticated == "" || !strings.EqualFold(configured, authenticated) {
		return "", false
	}
	return configured, true
}

func participantsFromRoutes(routes []models.CalculatedRoute) []models.RouteFeedbackParticipant {
	participants := make([]models.RouteFeedbackParticipant, 0)
	seen := make(map[int64]struct{})
	for _, route := range routes {
		for _, stop := range route.Stops {
			if stop.Participant == nil {
				continue
			}
			participant := stop.Participant
			if _, ok := seen[participant.ID]; ok {
				continue
			}
			seen[participant.ID] = struct{}{}
			participants = append(participants, models.RouteFeedbackParticipant{
				ID: participant.ID, Address: participant.Address, Lat: participant.Lat, Lng: participant.Lng,
			})
		}
	}
	return participants
}

func routesFrom(routes []models.CalculatedRoute) []models.RouteFeedbackRoute {
	result := make([]models.RouteFeedbackRoute, 0, len(routes))
	for _, route := range routes {
		if route.Driver == nil {
			continue
		}
		feedbackRoute := models.RouteFeedbackRoute{
			DriverID: route.Driver.ID, ParticipantIDs: make([]int64, 0, len(route.Stops)),
			TotalDistanceMeters: route.TotalDistanceMeters, RouteDurationSecs: route.RouteDurationSecs, DetourSecs: route.DetourSecs,
		}
		if route.OrgVehicleID != 0 {
			feedbackRoute.OrgVehicleID = new(route.OrgVehicleID)
		}
		for _, stop := range route.Stops {
			if stop.Participant != nil {
				feedbackRoute.ParticipantIDs = append(feedbackRoute.ParticipantIDs, stop.Participant.ID)
			}
		}
		result = append(result, feedbackRoute)
	}
	return result
}
