package routefeedback

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"ride-home-router/internal/models"
	"ride-home-router/internal/routesession"
	"strings"
	"testing"
	"time"
)

func TestBuildCreatesAllowlistedBeforeAndAfterRecord(t *testing.T) {
	snapshot := feedbackSnapshot()
	record := Build(snapshot)

	if record.SchemaVersion != SchemaVersion || record.SessionID != "session-1" || record.Mode != models.RouteModeDropoff {
		t.Fatalf("record metadata = %#v", record)
	}
	if len(record.Input.Drivers) != 3 {
		t.Fatalf("input drivers = %#v, want all 3 selected drivers", record.Input.Drivers)
	}
	if got := record.Input.Drivers[2].ID; got != 3 {
		t.Fatalf("unused driver ID = %d, want 3", got)
	}
	if len(record.Input.Participants) != 2 || record.Input.Participants[0].ID != 10 || record.Input.Participants[1].ID != 11 {
		t.Fatalf("input participants = %#v, want ordered original-route union", record.Input.Participants)
	}
	if len(record.Proposed) != 2 || len(record.Final) != 2 || reflect.DeepEqual(record.Proposed, record.Final) {
		t.Fatalf("proposed/final routes = %#v / %#v, want edited pair", record.Proposed, record.Final)
	}
	if record.Proposed[0].ParticipantIDs[0] != 10 || record.Final[1].ParticipantIDs[0] != 10 {
		t.Fatalf("participant order not retained: proposed=%#v final=%#v", record.Proposed, record.Final)
	}

	payload, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("marshal record: %v", err)
	}
	for _, forbidden := range []string{`"name":`, `"address_name":`, `"created_at":`, `"updated_at":`} {
		if strings.Contains(string(payload), forbidden) {
			t.Fatalf("feedback JSON contains forbidden key %s: %s", forbidden, payload)
		}
	}
	for _, required := range []string{`"address":"1 Driver Road"`, `"org_vehicle_id":50`, `"participant_ids":[10]`} {
		if !strings.Contains(string(payload), required) {
			t.Fatalf("feedback JSON missing %s: %s", required, payload)
		}
	}
}

func TestBuildRecordsUnchangedSession(t *testing.T) {
	snapshot := feedbackSnapshot()
	snapshot.Final = snapshot.Original
	record := Build(snapshot)
	if !reflect.DeepEqual(record.Proposed, record.Final) {
		t.Fatalf("unchanged session differs: proposed=%#v final=%#v", record.Proposed, record.Final)
	}
}

func TestBuildOmitsRoutesWithoutParticipantsLikeEventSnapshot(t *testing.T) {
	snapshot := feedbackSnapshot()
	snapshot.Final[0].Stops = nil

	record := Build(snapshot)

	if len(record.Final) != 1 || record.Final[0].DriverID != snapshot.Final[1].Driver.ID {
		t.Fatalf("final routes = %#v, want only the route saved in event history", record.Final)
	}
}

func TestShouldCaptureMatchesConfiguredSMEEmail(t *testing.T) {
	tests := []struct {
		name     string
		setting  string
		header   string
		wantMail string
		wantOK   bool
	}{
		{name: "trimmed case insensitive match", setting: "  SME@Example.com  ", header: "sme@example.COM", wantMail: "SME@Example.com", wantOK: true},
		{name: "mismatch", setting: "sme@example.com", header: "other@example.com"},
		{name: "missing header", setting: "sme@example.com"},
		{name: "empty setting", header: "sme@example.com"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/events", nil)
			if tt.header != "" {
				req.Header.Set(AuthenticatedUserEmailHeader, tt.header)
			}
			mail, ok := ShouldCapture(req, &models.Settings{SMEEmail: tt.setting})
			if mail != tt.wantMail || ok != tt.wantOK {
				t.Fatalf("ShouldCapture() = %q, %v; want %q, %v", mail, ok, tt.wantMail, tt.wantOK)
			}
		})
	}
}

func feedbackSnapshot() routesession.CommitSnapshot {
	createdAt := time.Date(2026, time.August, 29, 12, 0, 0, 0, time.UTC)
	driverOne := &models.Driver{ID: 1, Name: "Driver One", Address: "1 Driver Road", AddressName: "Driver Home", Lat: 35.1, Lng: -78.1, VehicleCapacity: 8, CreatedAt: createdAt, UpdatedAt: createdAt}
	driverTwo := &models.Driver{ID: 2, Name: "Driver Two", Address: "2 Driver Road", AddressName: "Second Home", Lat: 35.2, Lng: -78.2, VehicleCapacity: 4, CreatedAt: createdAt, UpdatedAt: createdAt}
	riderOne := &models.Participant{ID: 10, Name: "Rider One", Address: "10 Rider Road", AddressName: "Rider Home", Lat: 35.3, Lng: -78.3, CreatedAt: createdAt, UpdatedAt: createdAt}
	riderTwo := &models.Participant{ID: 11, Name: "Rider Two", Address: "11 Rider Road", AddressName: "Other Home", Lat: 35.4, Lng: -78.4, CreatedAt: createdAt, UpdatedAt: createdAt}
	original := []models.CalculatedRoute{
		{Driver: driverOne, OrgVehicleID: 50, Stops: []models.RouteStop{{Participant: riderOne}}, TotalDistanceMeters: 1000, RouteDurationSecs: 600, DetourSecs: 120},
		{Driver: driverTwo, Stops: []models.RouteStop{{Participant: riderTwo}}, TotalDistanceMeters: 2000, RouteDurationSecs: 900, DetourSecs: 180},
	}
	final := []models.CalculatedRoute{
		{Driver: driverOne, OrgVehicleID: 50, Stops: []models.RouteStop{{Participant: riderTwo}}, TotalDistanceMeters: 1100, RouteDurationSecs: 650, DetourSecs: 130},
		{Driver: driverTwo, Stops: []models.RouteStop{{Participant: riderOne}}, TotalDistanceMeters: 1900, RouteDurationSecs: 850, DetourSecs: 170},
	}
	return routesession.CommitSnapshot{
		SessionID: "session-1",
		Original:  original,
		Final:     final,
		SelectedDrivers: []models.Driver{
			*driverOne,
			*driverTwo,
			{ID: 3, Name: "Unused Driver", Address: "3 Driver Road", AddressName: "Unused Home", Lat: 35.5, Lng: -78.5, VehicleCapacity: 5, CreatedAt: createdAt, UpdatedAt: createdAt},
		},
		DriverOrgVehicles: map[int64]*models.OrganizationVehicle{1: {ID: 50, Name: "Van", Capacity: 8, CreatedAt: createdAt, UpdatedAt: createdAt}},
		ActivityLocation:  &models.ActivityLocation{ID: 100, Name: "HQ", Address: "100 Center Road", Lat: 35, Lng: -78},
		Mode:              models.RouteModeDropoff,
	}
}
