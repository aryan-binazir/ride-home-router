package postgres_test

import (
	"context"
	"ride-home-router/internal/database"
	"ride-home-router/internal/models"
	"ride-home-router/internal/postgres/postgrestest"
	"testing"
	"time"
)

func TestEventRepositoryListAndSummaries(t *testing.T) {
	store := postgrestest.Open(t)
	ctx := context.Background()
	createEvent := func(eventDate string, totalDistance float64) int64 {
		date, err := time.Parse("2006-01-02", eventDate)
		if err != nil {
			t.Fatalf("time.Parse() error = %v", err)
		}
		routes := []models.EventRoute{{
			DriverID: 1, DriverName: "Driver", DriverAddress: "1 Driver Way", EffectiveCapacity: 4,
			TotalDropoffDistanceMeters: totalDistance, TotalDistanceMeters: totalDistance, Mode: "dropoff",
			Stops: []models.EventRouteStop{{
				ParticipantID: 1, ParticipantName: "Passenger", ParticipantAddress: "1 Rider Road",
				DistanceFromPrevMeters: totalDistance,
			}},
		}}
		summary := &models.EventSummary{TotalParticipants: 1, TotalDrivers: 1, TotalDistanceMeters: totalDistance, Mode: "dropoff"}
		created, err := store.Events().Create(ctx, &models.Event{EventDate: date, Notes: eventDate, Mode: "dropoff"}, routes, summary)
		if err != nil {
			t.Fatalf("Create() error = %v", err)
		}
		return created.ID
	}

	firstID := createEvent("2026-03-14", 1500)
	secondID := createEvent("2026-03-15", 2300)

	summaries, err := store.Events().GetSummariesByEventIDs(ctx, []int64{firstID, secondID, 999})
	if err != nil {
		t.Fatalf("GetSummariesByEventIDs() error = %v", err)
	}
	if len(summaries) != 2 || summaries[firstID].TotalDistanceMeters != 1500 || summaries[secondID].TotalDistanceMeters != 2300 {
		t.Fatalf("summaries = %#v", summaries)
	}

	events, total, err := store.Events().List(ctx, 1, 0)
	if err != nil || total != 2 || len(events) != 1 || events[0].ID != secondID {
		t.Fatalf("List(1, 0) = %#v, %d, %v; want newest first with total 2", events, total, err)
	}
	if events[0].Notes != "2026-03-15" || events[0].Mode != models.RouteModeDropoff {
		t.Fatalf("listed event = %#v", events[0])
	}

	if err := store.Events().Delete(ctx, firstID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if err := store.Events().Delete(ctx, firstID); err != database.ErrNotFound {
		t.Fatalf("second Delete() error = %v, want ErrNotFound", err)
	}
	if _, _, _, err := store.Events().GetByID(ctx, firstID); err != database.ErrNotFound {
		t.Fatalf("GetByID() after delete error = %v, want ErrNotFound", err)
	}
}

func TestEventRepositoryPersistsFullRouteSnapshot(t *testing.T) {
	store := postgrestest.Open(t)
	ctx := context.Background()
	eventDate := time.Date(2026, time.March, 14, 0, 0, 0, 0, time.UTC)

	fullRouteDistance := 2300.0
	dropoffDistance := 1700.0
	routes := []models.EventRoute{{
		DriverID: 42, DriverName: "Driver One", DriverAddress: "1 Driver Way", DriverAddressName: "North Lot",
		EffectiveCapacity: 4, OrgVehicleID: 3, OrgVehicleName: "Van A",
		TotalDropoffDistanceMeters: dropoffDistance, DistanceToDriverHomeMeters: 600, TotalDistanceMeters: fullRouteDistance,
		BaselineDurationSecs: 900, RouteDurationSecs: 1200, DetourSecs: 300, Mode: "pickup",
		Stops: []models.EventRouteStop{
			{Order: 0, ParticipantID: 7, ParticipantName: "Passenger One", ParticipantAddress: "7 Rider Road", ParticipantAddressName: "Rider Hall", DistanceFromPrevMeters: 1000, CumulativeDistanceMeters: 1000, DurationFromPrevSecs: 600, CumulativeDurationSecs: 600},
			{Order: 1, ParticipantID: 8, ParticipantName: "Passenger Two", ParticipantAddress: "8 Rider Road", ParticipantAddressName: "South Dorm", DistanceFromPrevMeters: 700, CumulativeDistanceMeters: dropoffDistance, DurationFromPrevSecs: 300, CumulativeDurationSecs: 900},
		},
	}}
	summary := &models.EventSummary{TotalParticipants: 2, TotalDrivers: 1, TotalDistanceMeters: fullRouteDistance, OrgVehiclesUsed: 1, Mode: "pickup"}

	created, err := store.Events().Create(ctx, &models.Event{EventDate: eventDate, Notes: "Persist full route totals", Mode: "pickup"}, routes, summary)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	gotEvent, gotRoutes, gotSummary, err := store.Events().GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if gotEvent.Mode != models.RouteModePickup || !gotEvent.EventDate.Equal(eventDate) || gotEvent.Notes != "Persist full route totals" {
		t.Fatalf("event = %#v", gotEvent)
	}
	if gotSummary == nil || gotSummary.TotalDistanceMeters != fullRouteDistance || gotSummary.Mode != models.RouteModePickup || gotSummary.OrgVehiclesUsed != 1 {
		t.Fatalf("summary = %#v", gotSummary)
	}
	if len(gotRoutes) != 1 {
		t.Fatalf("routes = %#v, want 1", gotRoutes)
	}
	route := gotRoutes[0]
	if route.TotalDistanceMeters != fullRouteDistance || route.TotalDropoffDistanceMeters != dropoffDistance || route.Mode != models.RouteModePickup {
		t.Fatalf("route distances/mode = %#v", route)
	}
	if route.OrgVehicleID != 3 || route.OrgVehicleName != "Van A" || route.DriverAddressName != "North Lot" {
		t.Fatalf("route vehicle/address snapshot = %#v", route)
	}
	if route.SnapshotVersion != 2 || !route.MetricsComplete {
		t.Fatalf("route snapshot version/metrics = %d/%v, want 2/true", route.SnapshotVersion, route.MetricsComplete)
	}
	if len(route.Stops) != 2 || route.Stops[0].ParticipantAddressName != "Rider Hall" || route.Stops[1].ParticipantAddressName != "South Dorm" || route.Stops[1].CumulativeDurationSecs != 900 {
		t.Fatalf("stops did not round-trip: %#v", route.Stops)
	}
}

func TestEventRepositoryPreservesLegacySnapshotVersion(t *testing.T) {
	store := postgrestest.Open(t)
	ctx := context.Background()

	created, err := store.Events().Create(ctx, &models.Event{EventDate: time.Now(), Mode: "dropoff"}, []models.EventRoute{{
		DriverID: 1, DriverName: "Driver", DriverAddress: "1 Driver Way", Mode: "dropoff", SnapshotVersion: 1,
	}}, nil)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	_, routes, summary, err := store.Events().GetByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if summary != nil {
		t.Fatalf("summary = %#v, want nil when none was saved", summary)
	}
	if len(routes) != 1 || routes[0].SnapshotVersion != 1 || routes[0].MetricsComplete || len(routes[0].Stops) != 0 {
		t.Fatalf("routes = %#v, want legacy v1 snapshot with incomplete metrics", routes)
	}
}
