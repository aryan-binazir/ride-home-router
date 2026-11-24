package handlers

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/aryan-binazir/ride-home-router/internal/geocoding"
	"github.com/aryan-binazir/ride-home-router/internal/models"
)

type rosterTestGeocoder struct {
	stubGeocoder
	addresses []string
	ctx       context.Context
	retries   int
	cancel    context.CancelFunc
}

func (g *rosterTestGeocoder) GeocodeWithRetry(ctx context.Context, address string, retries int) (*geocoding.GeocodingResult, error) {
	g.addresses = append(g.addresses, address)
	g.ctx, g.retries = ctx, retries
	if g.cancel != nil {
		g.cancel()
		return nil, ctx.Err()
	}
	return g.result, g.err
}

func TestRosterEditorChangedAddressAndFailedEdit(t *testing.T) {
	for _, kind := range []string{"participant", "driver"} {
		t.Run(kind, func(t *testing.T) {
			h, store := newTestManagementHandler(t)
			ctx := context.Background()
			label, err := store.Labels().Create(ctx, &models.Label{Name: "Keep"})
			if err != nil {
				t.Fatal(err)
			}
			editor := rosterEditor{db: store, geocoder: h.Geocoder}
			edit := participantEdit{Name: "Original", Address: "Old address", LabelIDs: []int64{label.ID}}
			var participant *models.Participant
			var driver *models.Driver
			if kind == "participant" {
				participant, err = editor.createParticipant(ctx, edit)
			} else {
				driver, err = editor.createDriver(ctx, rosterEdit{participantEdit: edit, VehicleCapacity: 4})
			}
			if err != nil {
				t.Fatal(err)
			}
			provider := &rosterTestGeocoder{result: &geocoding.GeocodingResult{Coords: models.Coordinates{Lat: 42, Lng: -71}}}
			editor.geocoder = provider
			addressWithOnlyTrailingSpace := "Old address "
			edit.Address = addressWithOnlyTrailingSpace
			if kind == "participant" {
				participant, err = editor.updateParticipant(ctx, participant, edit)
			} else {
				driver, err = editor.updateDriver(ctx, driver, rosterEdit{participantEdit: edit, VehicleCapacity: 5})
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(provider.addresses) != 1 || provider.addresses[0] != addressWithOnlyTrailingSpace || provider.ctx != ctx || provider.retries != 3 {
				t.Fatalf("provider = %#v", provider)
			}
			if kind == "participant" && (participant.Lat != 42 || participant.Lng != -71) {
				t.Fatalf("participant = %#v", participant)
			}
			if kind == "driver" && (driver.Lat != 42 || driver.Lng != -71 || driver.VehicleCapacity != 5) {
				t.Fatalf("driver = %#v", driver)
			}
			// Compare stored snapshots so PostgreSQL timestamp precision does not
			// obscure whether a failed edit changed the persisted record.
			if kind == "participant" {
				participant, err = store.Participants().GetByID(ctx, participant.ID)
			} else {
				driver, err = store.Drivers().GetByID(ctx, driver.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, failure := range []string{"provider", "cancellation", "labels"} {
				t.Run(failure, func(t *testing.T) {
					requestCtx, cancel := context.WithCancel(ctx)
					defer cancel()
					provider.cancel = nil
					provider.err = nil
					wantErr := geocoding.ErrNoGeocodingResults
					switch failure {
					case "provider":
						provider.err = wantErr
					case "cancellation":
						provider.cancel = cancel
						wantErr = context.Canceled
					}
					failedEdit := participantEdit{Name: "Must not persist", Address: "Changed again", SetLabels: true}
					if failure == "labels" {
						failedEdit.LabelIDs = []int64{999999}
					}
					if kind == "participant" {
						_, err = editor.updateParticipant(requestCtx, participant, failedEdit)
					} else {
						_, err = editor.updateDriver(requestCtx, driver, rosterEdit{participantEdit: failedEdit, VehicleCapacity: 6})
					}
					if err == nil {
						t.Fatal("expected failed edit")
					}
					if failure != "labels" {
						if _, ok := errors.AsType[rosterGeocodeError](err); !ok || !errors.Is(err, wantErr) {
							t.Fatalf("error = %T %v", err, err)
						}
						if provider.ctx != requestCtx {
							t.Fatal("request context not preserved")
						}
					}
					if kind == "participant" {
						got, getErr := store.Participants().GetByID(ctx, participant.ID)
						if getErr != nil || !reflect.DeepEqual(got, participant) {
							t.Fatalf("persisted = %#v, want %#v, err=%v", got, participant, getErr)
						}
						labels, getErr := store.Labels().ListLabelsForParticipant(ctx, participant.ID)
						if getErr != nil || len(labels) != 1 || labels[0].ID != label.ID {
							t.Fatalf("labels = %v, %v", labels, getErr)
						}
					} else {
						got, getErr := store.Drivers().GetByID(ctx, driver.ID)
						if getErr != nil || !reflect.DeepEqual(got, driver) {
							t.Fatalf("persisted = %#v, want %#v, err=%v", got, driver, getErr)
						}
						labels, getErr := store.Labels().ListLabelsForDriver(ctx, driver.ID)
						if getErr != nil || len(labels) != 1 || labels[0].ID != label.ID {
							t.Fatalf("labels = %v, %v", labels, getErr)
						}
					}
				})
			}
		})
	}
}

func TestRosterEditorGeocodeFreshness(t *testing.T) {
	h, s := newTestManagementHandler(t)
	ctx := t.Context()
	editor := rosterEditor{db: s, geocoder: h.Geocoder}
	p, err := editor.createParticipant(ctx, participantEdit{Name: "Rider", Address: "Old address"})
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-31 * 24 * time.Hour).UTC().Truncate(time.Microsecond)
	if err := s.Participants().UpdateCoordinates(ctx, p.ID, p.Address, p.GetCoords(), old); err != nil {
		t.Fatal(err)
	}
	p, err = s.Participants().GetByID(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	p, err = editor.updateParticipant(ctx, p, participantEdit{Name: "New name", Address: p.Address})
	if err != nil {
		t.Fatal(err)
	}
	p, err = s.Participants().GetByID(ctx, p.ID)
	if err != nil || !p.GeocodedAt.Equal(old) {
		t.Fatalf("unchanged address: %#v %v", p, err)
	}
	p, err = editor.updateParticipant(ctx, p, participantEdit{Name: p.Name, Address: "New address"})
	if err != nil {
		t.Fatal(err)
	}
	p, err = s.Participants().GetByID(ctx, p.ID)
	if err != nil || time.Since(p.GeocodedAt) > time.Minute {
		t.Fatalf("new geocode: %#v %v", p, err)
	}
}

func TestRosterEditorDriverGeocodeFreshness(t *testing.T) {
	h, s := newTestManagementHandler(t)
	ctx := t.Context()
	editor := rosterEditor{db: s, geocoder: h.Geocoder}
	d, err := editor.createDriver(ctx, rosterEdit{Name: "Driver", Address: "Home", VehicleCapacity: 4})
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-31 * 24 * time.Hour).UTC().Truncate(time.Microsecond)
	if err := s.Drivers().UpdateCoordinates(ctx, d.ID, d.Address, d.GetCoords(), old); err != nil {
		t.Fatal(err)
	}
	d, err = s.Drivers().GetByID(ctx, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	d, err = editor.updateDriver(ctx, d, rosterEdit{Name: "Renamed", Address: d.Address, VehicleCapacity: 5})
	if err != nil {
		t.Fatal(err)
	}
	d, err = s.Drivers().GetByID(ctx, d.ID)
	if err != nil || !d.GeocodedAt.Equal(old) {
		t.Fatalf("unchanged=%#v %v", d, err)
	}
	d, err = editor.updateDriver(ctx, d, rosterEdit{Name: d.Name, Address: "New address", VehicleCapacity: 5})
	if err != nil {
		t.Fatal(err)
	}
	d, err = s.Drivers().GetByID(ctx, d.ID)
	if err != nil || time.Since(d.GeocodedAt) > time.Minute {
		t.Fatalf("changed=%#v %v", d, err)
	}
}
