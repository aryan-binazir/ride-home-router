package eventsnapshot_test

import (
	"errors"
	"reflect"
	"ride-home-router/internal/eventsnapshot"
	"ride-home-router/internal/models"
	"testing"
)

func TestValidationSentinelText(t *testing.T) {
	tests := []struct {
		err  error
		want string
	}{
		{eventsnapshot.ErrRoutesRequired, "routes are required"},
		{eventsnapshot.ErrDriverRequired, "each route must include a driver"},
		{eventsnapshot.ErrParticipantRequired, "each route stop must include a participant"},
		{eventsnapshot.ErrMixedModes, "all routes must use the same mode"},
	}
	for _, test := range tests {
		if got := test.err.Error(); got != test.want {
			t.Errorf("sentinel text = %q, want %q", got, test.want)
		}
	}
}

func TestBuildRequiresAtLeastOneNonEmptyRoute(t *testing.T) {
	result := models.RoutingResult{
		Mode: models.RouteModeDropoff,
		Routes: []models.CalculatedRoute{{
			Driver: &models.Driver{ID: 1, VehicleCapacity: 2},
			Mode:   models.RouteModeDropoff,
		}},
	}

	_, err := eventsnapshot.Build(result)

	if !errors.Is(err, eventsnapshot.ErrRoutesRequired) {
		t.Fatalf("Build error = %v, want ErrRoutesRequired", err)
	}
}

func TestBuildRequiresDriverBeforeSkippingEmptyRoute(t *testing.T) {
	result := models.RoutingResult{
		Mode:   models.RouteModeDropoff,
		Routes: []models.CalculatedRoute{{Mode: models.RouteModeDropoff}},
	}

	_, err := eventsnapshot.Build(result)

	if !errors.Is(err, eventsnapshot.ErrDriverRequired) {
		t.Fatalf("Build error = %v, want ErrDriverRequired", err)
	}
}

func TestBuildRequiresParticipantForEveryStop(t *testing.T) {
	result := models.RoutingResult{
		Mode: models.RouteModeDropoff,
		Routes: []models.CalculatedRoute{{
			Driver: &models.Driver{ID: 1, VehicleCapacity: 2},
			Stops:  []models.RouteStop{{}},
			Mode:   models.RouteModeDropoff,
		}},
	}

	_, err := eventsnapshot.Build(result)

	if !errors.Is(err, eventsnapshot.ErrParticipantRequired) {
		t.Fatalf("Build error = %v, want ErrParticipantRequired", err)
	}
}

func TestBuildNormalizesBlankModesToDropoff(t *testing.T) {
	result := models.RoutingResult{
		Routes: []models.CalculatedRoute{{
			Driver: &models.Driver{ID: 1, VehicleCapacity: 2},
			Stops:  []models.RouteStop{{Participant: &models.Participant{ID: 10}}},
		}},
	}

	snapshot, err := eventsnapshot.Build(result)
	if err != nil {
		t.Fatalf("Build error = %v", err)
	}
	if snapshot.Mode != models.RouteModeDropoff {
		t.Fatalf("snapshot mode = %q, want dropoff", snapshot.Mode)
	}
	if len(snapshot.Routes) != 1 || snapshot.Routes[0].Mode != models.RouteModeDropoff {
		t.Fatalf("snapshot routes = %#v, want one dropoff route", snapshot.Routes)
	}
}

func TestBuildRejectsMixedModes(t *testing.T) {
	result := models.RoutingResult{
		Mode: models.RouteModeDropoff,
		Routes: []models.CalculatedRoute{{
			Driver: &models.Driver{ID: 1, VehicleCapacity: 2},
			Stops:  []models.RouteStop{{Participant: &models.Participant{ID: 10}}},
			Mode:   models.RouteModePickup,
		}},
	}

	_, err := eventsnapshot.Build(result)

	if !errors.Is(err, eventsnapshot.ErrMixedModes) {
		t.Fatalf("Build error = %v, want ErrMixedModes", err)
	}
}

func TestBuildCreatesCompleteEventSnapshot(t *testing.T) {
	result := models.RoutingResult{
		Mode: models.RouteModePickup,
		Routes: []models.CalculatedRoute{
			{
				Driver: &models.Driver{ID: 99, Name: "Unused", VehicleCapacity: 1},
				Mode:   models.RouteMode("ignored-invalid-mode"),
			},
			{
				Driver:                     &models.Driver{ID: 1, Name: "Driver One", Address: "1 Driver Way", VehicleCapacity: 3},
				OrgVehicleID:               7,
				OrgVehicleName:             "Shared Van",
				TotalDropoffDistanceMeters: 1000,
				DistanceToDriverHomeMeters: 250,
				TotalDistanceMeters:        1250,
				BaselineDurationSecs:       600,
				RouteDurationSecs:          900,
				DetourSecs:                 300,
				Stops: []models.RouteStop{
					{
						Participant:              &models.Participant{ID: 10, Name: "Alice", Address: "10 Rider Road"},
						DistanceFromPrevMeters:   400,
						CumulativeDistanceMeters: 400,
						DurationFromPrevSecs:     240,
						CumulativeDurationSecs:   240,
					},
					{
						Participant:              &models.Participant{ID: 11, Name: "Bob", Address: "11 Rider Road"},
						DistanceFromPrevMeters:   600,
						CumulativeDistanceMeters: 1000,
						DurationFromPrevSecs:     360,
						CumulativeDurationSecs:   600,
					},
				},
			},
			{
				Driver:                     &models.Driver{ID: 2, Name: "Driver Two", Address: "2 Driver Way", VehicleCapacity: 4},
				EffectiveCapacity:          5,
				OrgVehicleID:               7,
				OrgVehicleName:             "Shared Van",
				TotalDropoffDistanceMeters: 900,
				DistanceToDriverHomeMeters: 350,
				TotalDistanceMeters:        1250,
				BaselineDurationSecs:       700,
				RouteDurationSecs:          1000,
				DetourSecs:                 300,
				Mode:                       models.RouteModePickup,
				Stops: []models.RouteStop{{
					Participant:              &models.Participant{ID: 12, Name: "Casey", Address: "12 Rider Road"},
					DistanceFromPrevMeters:   900,
					CumulativeDistanceMeters: 900,
					DurationFromPrevSecs:     540,
					CumulativeDurationSecs:   540,
				}},
			},
		},
	}
	want := eventsnapshot.Snapshot{
		Mode: models.RouteModePickup,
		Routes: []models.EventRoute{
			{
				RouteOrder: 0, DriverID: 1, DriverName: "Driver One", DriverAddress: "1 Driver Way",
				EffectiveCapacity: 3, OrgVehicleID: 7, OrgVehicleName: "Shared Van",
				TotalDropoffDistanceMeters: 1000, DistanceToDriverHomeMeters: 250, TotalDistanceMeters: 1250,
				BaselineDurationSecs: 600, RouteDurationSecs: 900, DetourSecs: 300,
				Mode: models.RouteModePickup, SnapshotVersion: 2, MetricsComplete: true,
				Stops: []models.EventRouteStop{
					{Order: 0, ParticipantID: 10, ParticipantName: "Alice", ParticipantAddress: "10 Rider Road", DistanceFromPrevMeters: 400, CumulativeDistanceMeters: 400, DurationFromPrevSecs: 240, CumulativeDurationSecs: 240},
					{Order: 1, ParticipantID: 11, ParticipantName: "Bob", ParticipantAddress: "11 Rider Road", DistanceFromPrevMeters: 600, CumulativeDistanceMeters: 1000, DurationFromPrevSecs: 360, CumulativeDurationSecs: 600},
				},
			},
			{
				RouteOrder: 1, DriverID: 2, DriverName: "Driver Two", DriverAddress: "2 Driver Way",
				EffectiveCapacity: 5, OrgVehicleID: 7, OrgVehicleName: "Shared Van",
				TotalDropoffDistanceMeters: 900, DistanceToDriverHomeMeters: 350, TotalDistanceMeters: 1250,
				BaselineDurationSecs: 700, RouteDurationSecs: 1000, DetourSecs: 300,
				Mode: models.RouteModePickup, SnapshotVersion: 2, MetricsComplete: true,
				Stops: []models.EventRouteStop{{Order: 0, ParticipantID: 12, ParticipantName: "Casey", ParticipantAddress: "12 Rider Road", DistanceFromPrevMeters: 900, CumulativeDistanceMeters: 900, DurationFromPrevSecs: 540, CumulativeDurationSecs: 540}},
			},
		},
		Summary: models.EventSummary{TotalParticipants: 3, TotalDrivers: 2, TotalDistanceMeters: 2500, OrgVehiclesUsed: 2, Mode: models.RouteModePickup},
	}

	got, err := eventsnapshot.Build(result)
	if err != nil {
		t.Fatalf("Build error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Build = %#v, want %#v", got, want)
	}
}

func TestBuildPreservesInvalidModeIdentity(t *testing.T) {
	_, err := eventsnapshot.Build(models.RoutingResult{Mode: models.RouteMode("invalid")})
	if !errors.Is(err, models.ErrInvalidRouteMode) {
		t.Fatalf("Build error = %v, want ErrInvalidRouteMode", err)
	}
}

func TestBuildValidatesRouteModeBeforeParticipants(t *testing.T) {
	result := models.RoutingResult{
		Mode: models.RouteModeDropoff,
		Routes: []models.CalculatedRoute{{
			Driver: &models.Driver{ID: 1, VehicleCapacity: 2},
			Stops:  []models.RouteStop{{}},
			Mode:   models.RouteMode("invalid"),
		}},
	}

	_, err := eventsnapshot.Build(result)

	if !errors.Is(err, models.ErrInvalidRouteMode) {
		t.Fatalf("Build error = %v, want ErrInvalidRouteMode", err)
	}
}
