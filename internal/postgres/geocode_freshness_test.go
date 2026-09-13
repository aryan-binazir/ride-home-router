package postgres_test

import (
	"context"
	"errors"
	"ride-home-router/internal/database"
	"ride-home-router/internal/models"
	"ride-home-router/internal/postgres/postgrestest"
	"testing"
	"time"
)

func TestParticipantCoordinateFreshness(t *testing.T) {
	s := postgrestest.Open(t)
	ctx := t.Context()
	p, err := s.Participants().Create(ctx, &models.Participant{Name: "Rider", Address: "1 Main", Lat: 1, Lng: 2})
	if err != nil {
		t.Fatal(err)
	}
	p, err = s.Participants().GetByID(ctx, p.ID)
	if err != nil || p.GeocodedAt.IsZero() {
		t.Fatalf("create freshness: %#v %v", p, err)
	}
	old := time.Now().Add(-31 * 24 * time.Hour).UTC().Truncate(time.Microsecond)
	if err := s.Participants().UpdateCoordinates(ctx, p.ID, p.Address, models.Coordinates{Lat: 3, Lng: 4}, old); err != nil {
		t.Fatal(err)
	}
	p, err = s.Participants().GetByID(ctx, p.ID)
	if err != nil || p.Lat != 3 || p.Lng != 4 || !p.GeocodedAt.Equal(old) {
		t.Fatalf("coordinate update: %#v %v", p, err)
	}
	p.Name = "Renamed"
	if _, err = s.Participants().Update(ctx, p); err != nil {
		t.Fatal(err)
	}
	p, err = s.Participants().GetByID(ctx, p.ID)
	if err != nil || !p.GeocodedAt.Equal(old) {
		t.Fatalf("unchanged address freshness: %#v %v", p, err)
	}
}

func TestDriverAndLocationCoordinateFreshness(t *testing.T) {
	s := postgrestest.Open(t)
	ctx := t.Context()
	old := time.Now().Add(-31 * 24 * time.Hour).UTC().Truncate(time.Microsecond)
	d, err := s.Drivers().Create(ctx, &models.Driver{Name: "Driver", Address: "Home", Lat: 1, Lng: 2, VehicleCapacity: 4})
	if err != nil {
		t.Fatal(err)
	}
	loc, err := s.ActivityLocations().Create(ctx, &models.ActivityLocation{Name: "Gym", Address: "Gym address", Lat: 3, Lng: 4})
	if err != nil {
		t.Fatal(err)
	}
	d, err = s.Drivers().GetByID(ctx, d.ID)
	if err != nil || d.GeocodedAt.IsZero() {
		t.Fatalf("driver create=%#v %v", d, err)
	}
	loc, err = s.ActivityLocations().GetByID(ctx, loc.ID)
	if err != nil || loc.GeocodedAt.IsZero() {
		t.Fatalf("location create=%#v %v", loc, err)
	}
	for _, tc := range []struct {
		name    string
		id      int64
		address string
		update  func(context.Context, int64, string, models.Coordinates, time.Time) error
	}{
		{"driver", d.ID, d.Address, s.Drivers().UpdateCoordinates},
		{"location", loc.ID, loc.Address, s.ActivityLocations().UpdateCoordinates},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.update(ctx, tc.id, tc.address, models.Coordinates{Lat: 41, Lng: -72}, old); err != nil {
				t.Fatal(err)
			}
			if err := tc.update(ctx, tc.id, "changed meanwhile", models.Coordinates{Lat: 99, Lng: 99}, time.Now()); !errors.Is(err, database.ErrNotFound) {
				t.Fatalf("address guard=%v", err)
			}
		})
	}
	d, err = s.Drivers().GetByID(ctx, d.ID)
	if err != nil || d.Lat != 41 || d.Lng != -72 || !d.GeocodedAt.Equal(old) {
		t.Fatalf("driver refresh=%#v %v", d, err)
	}
	loc, err = s.ActivityLocations().GetByID(ctx, loc.ID)
	if err != nil || loc.Lat != 41 || loc.Lng != -72 || !loc.GeocodedAt.Equal(old) {
		t.Fatalf("location refresh=%#v %v", loc, err)
	}
	d.Name = "Renamed driver"
	loc.Name = "Renamed gym"
	if _, err := s.Drivers().Update(ctx, d); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ActivityLocations().Update(ctx, loc); err != nil {
		t.Fatal(err)
	}
	d, err = s.Drivers().GetByID(ctx, d.ID)
	if err != nil || !d.GeocodedAt.Equal(old) {
		t.Fatalf("driver retain=%#v %v", d, err)
	}
	loc, err = s.ActivityLocations().GetByID(ctx, loc.ID)
	if err != nil || !loc.GeocodedAt.Equal(old) {
		t.Fatalf("location retain=%#v %v", loc, err)
	}
}

func TestImportsRetainLookupTime(t *testing.T) {
	s := postgrestest.Open(t)
	ctx := t.Context()
	lookup := time.Now().Add(-time.Hour).UTC().Truncate(time.Microsecond)
	if _, err := s.Participants().UpsertBatch(ctx, []*models.Participant{{Name: "Rider", Address: "Home", Lat: 1, Lng: 2, GeocodedAt: lookup}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Drivers().UpsertBatch(ctx, []*models.Driver{{Name: "Driver", Address: "Home", Lat: 1, Lng: 2, VehicleCapacity: 4, GeocodedAt: lookup}}); err != nil {
		t.Fatal(err)
	}
	ps, err := s.Participants().List(ctx, "")
	if err != nil || len(ps) != 1 || !ps[0].GeocodedAt.Equal(lookup) {
		t.Fatalf("participant import=%#v %v", ps, err)
	}
	ds, err := s.Drivers().List(ctx, "")
	if err != nil || len(ds) != 1 || !ds[0].GeocodedAt.Equal(lookup) {
		t.Fatalf("driver import=%#v %v", ds, err)
	}
	// Duplicate imports preserve coordinates, so must also preserve their age.
	if _, err := s.Participants().UpsertBatch(ctx, []*models.Participant{{Name: "Rider", Address: "Home", Lat: 3, Lng: 4, GeocodedAt: time.Now()}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Drivers().UpsertBatch(ctx, []*models.Driver{{Name: "Driver", Address: "Home", Lat: 3, Lng: 4, VehicleCapacity: 5, GeocodedAt: time.Now()}}); err != nil {
		t.Fatal(err)
	}
	ps, err = s.Participants().List(ctx, "")
	if err != nil || ps[0].Lat != 1 || !ps[0].GeocodedAt.Equal(lookup) {
		t.Fatalf("duplicate participant=%#v %v", ps, err)
	}
	ds, err = s.Drivers().List(ctx, "")
	if err != nil || ds[0].Lat != 1 || !ds[0].GeocodedAt.Equal(lookup) {
		t.Fatalf("duplicate driver=%#v %v", ds, err)
	}
}
