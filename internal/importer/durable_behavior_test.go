package importer

import (
	"context"
	"errors"
	"fmt"
	"ride-home-router/internal/geocoding"
	"ride-home-router/internal/models"
	"ride-home-router/internal/postgres"
	"ride-home-router/internal/postgres/postgrestest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func durableTestStore(t *testing.T, db *postgres.Store, geocoder geocoding.Geocoder) *Store {
	t.Helper()
	s := NewPersistentStore(t.Context(), geocoder, db, db.Workflows(), db.ImportJobs())
	t.Cleanup(s.Close)
	return s
}

func stageDurableImport(t *testing.T, s *Store, kind Kind, csv string) Snapshot {
	t.Helper()
	grid := testGrid(t, csv)
	created, err := s.CreateContext(t.Context(), kind, "synthetic.csv", grid)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := s.ApplyMapping(t.Context(), created.ID, AutoMap(grid.Headers))
	if err != nil {
		t.Fatal(err)
	}
	return preview
}

func waitDurableImport(t *testing.T, s *Store, id string) Snapshot {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		snapshot, ok, err := s.Load(t.Context(), id)
		if err != nil || !ok {
			t.Fatalf("load import: found=%t err=%v", ok, err)
		}
		pending := false
		for _, row := range snapshot.Rows {
			pending = pending || row.NeedsGeocoding
		}
		if !snapshot.GeocodeProgress.Running && !pending {
			return snapshot
		}
		if time.Now().After(deadline) {
			t.Fatalf("geocoding did not finish: %+v", snapshot.GeocodeProgress)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestDurableImportUsesLatestSelectionAndCommitsCountsOnce(t *testing.T) {
	db := postgrestest.Open(t)
	if _, err := db.Participants().Create(t.Context(), &models.Participant{Name: "Second", Address: "2 Main St", Lat: 40, Lng: -73}); err != nil {
		t.Fatal(err)
	}
	s := durableTestStore(t, db, successfulTestGeocoder())
	preview := stageDurableImport(t, s, KindParticipant, "name,address\nFirst,1 Main St\nSecond,2 Main St\nThird,3 Main St\nFourth,4 Main St\n")
	if !preview.Rows[1].DuplicateOfExisting || !preview.Selected[1] {
		t.Fatalf("existing row preview = %+v selected=%v", preview.Rows[1], preview.Selected[1])
	}
	if preview.Rows[0].DuplicateOfExisting {
		t.Fatalf("first row was already a duplicate at preview: %+v", preview.Rows[0])
	}
	waitDurableImport(t, s, preview.ID)
	// The duplicate appears after preview, so commit must use current roster keys.
	if _, err := db.Participants().Create(t.Context(), &models.Participant{Name: " First ", Address: "1 MAIN ST", Lat: 40, Lng: -73}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SelectRowsContext(t.Context(), preview.ID, []bool{false, false, false, false}); err != nil {
		t.Fatal(err)
	}
	selection, err := s.SelectRowsContext(t.Context(), preview.ID, []bool{true, true, false, true})
	if err != nil || selection.Selected[2] {
		t.Fatalf("latest selection = %+v err=%v", selection.Selected, err)
	}
	result, err := s.Commit(t.Context(), preview.ID, nil)
	if err != nil || result != (CommitResult{Created: 1, Updated: 2, NotSelected: 1}) {
		t.Fatalf("commit = %+v err=%v", result, err)
	}
	if _, err := s.Commit(t.Context(), preview.ID, nil); !errors.Is(err, ErrCommitConsumed) {
		t.Fatalf("second commit = %v, want consumed", err)
	}
	rows, err := db.Participants().List(t.Context(), "")
	if err != nil || len(rows) != 3 {
		t.Fatalf("roster rows = %+v err=%v", rows, err)
	}
}

func TestDurableDriverImportCapacity(t *testing.T) {
	for _, tc := range []struct {
		name, csv      string
		existing, want int
	}{
		{name: "mapped", csv: "name,address,capacity\nDriver,1 Main St,6\n", want: 6},
		{name: "unmapped new", csv: "name,address\nDriver,1 Main St\n", want: models.DefaultVehicleCapacity},
		{name: "unmapped existing", csv: "name,address\nDriver,1 Main St\n", existing: 8, want: 8},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := postgrestest.Open(t)
			if tc.existing > 0 {
				if _, err := db.Drivers().Create(t.Context(), &models.Driver{Name: "Driver", Address: "1 Main St", Lat: 40, Lng: -73, VehicleCapacity: tc.existing}); err != nil {
					t.Fatal(err)
				}
			}
			s := durableTestStore(t, db, successfulTestGeocoder())
			preview := stageDurableImport(t, s, KindDriver, tc.csv)
			waitDurableImport(t, s, preview.ID)
			if _, err := s.Commit(t.Context(), preview.ID, nil); err != nil {
				t.Fatal(err)
			}
			rows, err := db.Drivers().List(t.Context(), "")
			if err != nil || len(rows) != 1 || rows[0].VehicleCapacity != tc.want {
				t.Fatalf("drivers = %+v err=%v, want capacity %d", rows, err, tc.want)
			}
		})
	}
}

func TestDurableGeocodeDedupFailureAndGuessedAddress(t *testing.T) {
	db := postgrestest.Open(t)
	g := &fakeGeocoder{result: func(_ context.Context, address string, retries int) (*geocoding.GeocodingResult, error) {
		if retries != geocodeMaxRetries {
			t.Errorf("geocode retries = %d", retries)
		}
		if strings.Contains(address, "Bad") {
			return nil, geocoding.ErrNoGeocodingResults
		}
		return &geocoding.GeocodingResult{Coords: models.Coordinates{Lat: 40, Lng: -73}, FormattedAddress: "Matched " + address, Guessed: strings.Contains(address, "Raliegh")}, nil
	}}
	s := durableTestStore(t, db, g)
	preview := stageDurableImport(t, s, KindParticipant, "name,address\nFirst,1 Main St\nSecond,  1 MAIN st \nThird,2 Bad St\nFourth,3 Raliegh St\n")
	finished := waitDurableImport(t, s, preview.ID)
	if g.callCount() != 3 || finished.GeocodeProgress != (GeocodeProgress{Done: 3, Total: 3}) {
		t.Fatalf("calls=%d progress=%+v", g.callCount(), finished.GeocodeProgress)
	}
	if finished.Selected[2] || len(finished.Rows[2].Errors) == 0 || finished.Rows[2].HasCoordinates || !finished.Rows[3].AddressGuessed {
		t.Fatalf("failed/guessed rows = %+v %+v", finished.Rows[2], finished.Rows[3])
	}
	result, err := s.Commit(t.Context(), preview.ID, nil)
	if err != nil || result != (CommitResult{Created: 3, NotSelected: 1, Guessed: 1}) {
		t.Fatalf("commit = %+v err=%v", result, err)
	}
	rows, err := db.Participants().List(t.Context(), "")
	if err != nil || len(rows) != 3 {
		t.Fatalf("roster = %+v err=%v", rows, err)
	}
	for _, row := range rows {
		if row.Name == "Fourth" && (row.AddressMatch != models.AddressMatchGuessed || row.MatchedAddress != "Matched 3 Raliegh St") {
			t.Fatalf("guessed address = %+v", row)
		}
	}
}

func TestDurableImportRejectsGeocodeAddressCapWithoutChangingSession(t *testing.T) {
	db := postgrestest.Open(t)
	g := successfulTestGeocoder()
	s := durableTestStore(t, db, g)
	var csv strings.Builder
	csv.WriteString("name,address\n")
	for i := 0; i <= MaxGeocodeAddresses; i++ {
		fmt.Fprintf(&csv, "Rider %d,%d Main St\n", i, i)
	}
	grid := testGrid(t, csv.String())
	created, err := s.CreateContext(t.Context(), KindParticipant, "synthetic.csv", grid)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplyMapping(t.Context(), created.ID, AutoMap(grid.Headers)); !errors.Is(err, ErrTooManyGeocodeAddresses) {
		t.Fatalf("mapping error = %v", err)
	}
	loaded, ok, err := s.Load(t.Context(), created.ID)
	if err != nil || !ok || loaded.Status != StatusMapping || g.callCount() != 0 {
		t.Fatalf("session = %+v found=%t calls=%d err=%v", loaded, ok, g.callCount(), err)
	}
}

func TestDurableCanceledCommitCanRetry(t *testing.T) {
	db := postgrestest.Open(t)
	s := durableTestStore(t, db, successfulTestGeocoder())
	preview := stageDurableImport(t, s, KindParticipant, "name,address\nRider,1 Main St\n")
	waitDurableImport(t, s, preview.ID)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := s.Commit(ctx, preview.ID, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled commit = %v", err)
	}
	loaded, ok, err := s.Load(t.Context(), preview.ID)
	if err != nil || !ok || loaded.Status != StatusPreviewing {
		t.Fatalf("after canceled commit = %+v found=%t err=%v", loaded, ok, err)
	}
	rows, err := db.Participants().List(t.Context(), "")
	if err != nil || len(rows) != 0 {
		t.Fatalf("roster after canceled commit = %+v err=%v", rows, err)
	}
	result, err := s.Commit(t.Context(), preview.ID, nil)
	if err != nil || result.Created != 1 {
		t.Fatalf("retry commit = %+v err=%v", result, err)
	}
}

func TestDurableFailedBatchRollsBackRosterAndToken(t *testing.T) {
	url := postgrestest.DatabaseURL(t)
	db := postgrestest.OpenURL(t, url)
	s := durableTestStore(t, db, successfulTestGeocoder())
	preview := stageDurableImport(t, s, KindParticipant, "name,address\nFirst,1 Main St\nSecond,2 Main St\n")
	waitDurableImport(t, s, preview.ID)
	conn, err := pgx.Connect(t.Context(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	_, err = conn.Exec(t.Context(), `
		CREATE FUNCTION reject_second_import() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF NEW.name = 'Second' THEN RAISE EXCEPTION 'synthetic write failure'; END IF;
			RETURN NEW;
		END $$;
		CREATE TRIGGER reject_second_import BEFORE INSERT ON participants
		FOR EACH ROW EXECUTE FUNCTION reject_second_import();`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Commit(t.Context(), preview.ID, nil); err == nil {
		t.Fatal("commit succeeded despite synthetic second-row failure")
	}
	rows, err := db.Participants().List(t.Context(), "")
	if err != nil || len(rows) != 0 {
		t.Fatalf("roster after failed batch = %+v err=%v", rows, err)
	}
	loaded, ok, err := s.Load(t.Context(), preview.ID)
	if err != nil || !ok || loaded.Status != StatusPreviewing || len(loaded.Rows) != 2 {
		t.Fatalf("session after failed batch = %+v found=%t err=%v", loaded, ok, err)
	}
	if _, err := conn.Exec(t.Context(), `DROP TRIGGER reject_second_import ON participants`); err != nil {
		t.Fatal(err)
	}
	result, err := s.Commit(t.Context(), preview.ID, nil)
	if err != nil || result.Created != 2 {
		t.Fatalf("retry = %+v err=%v", result, err)
	}
}

func TestDurableCancelDuringGeocodingRemovesSession(t *testing.T) {
	db := postgrestest.Open(t)
	started := make(chan struct{}, 1)
	g := &fakeGeocoder{result: func(ctx context.Context, _ string, _ int) (*geocoding.GeocodingResult, error) {
		started <- struct{}{}
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	s := durableTestStore(t, db, g)
	preview := stageDurableImport(t, s, KindParticipant, "name,address\nRider,1 Main St\n")
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("geocoding did not start")
	}
	canceled, err := s.CancelContext(t.Context(), preview.ID)
	if err != nil || !canceled {
		t.Fatalf("cancel = %t err=%v", canceled, err)
	}
	if _, ok, err := s.Load(t.Context(), preview.ID); err != nil || ok {
		t.Fatalf("canceled session found=%t err=%v", ok, err)
	}
	rows, err := db.Participants().List(t.Context(), "")
	if err != nil || len(rows) != 0 {
		t.Fatalf("roster after cancel = %+v err=%v", rows, err)
	}
}
