package importer

import (
	"context"
	"encoding/json"
	"fmt"
	"ride-home-router/internal/database"
	"ride-home-router/internal/geocoding"
	"ride-home-router/internal/models"
	"ride-home-router/internal/postgres/postgrestest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestPersistentGeocodeRoundLimit(t *testing.T) {
	for _, tc := range []struct {
		name                                         string
		successAt, crashRounds, wantCalls            int
		configuration, quota, permanent, timeoutLast bool
	}{
		{name: "temporary exhaustion", wantCalls: 3},
		{name: "last round deadline", timeoutLast: true, wantCalls: 3},
		{name: "success on last round", successAt: 3, wantCalls: 3},
		{name: "permanent failure", permanent: true, wantCalls: 1},
		{name: "key rejected", configuration: true, wantCalls: 3},
		{name: "usage ceiling", quota: true, wantCalls: 3},
		{name: "crashes consume rounds", crashRounds: 3, wantCalls: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			url := postgrestest.DatabaseURL(t)
			db := postgrestest.OpenURL(t, url)
			conn, err := pgx.Connect(t.Context(), url)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = conn.Close(context.Background()) }()
			if err := db.Workflows().Create(t.Context(), "import", "retry", []byte(`{}`), time.Hour); err != nil {
				t.Fatal(err)
			}
			row, err := json.Marshal(Row{Address: "1 Example St", NeedsGeocoding: true})
			if err != nil {
				t.Fatal(err)
			}
			if err := db.Workflows().Transact(t.Context(), "import", "retry", time.Hour, func(_ *database.WorkflowRecord, w database.WorkflowWrites) error {
				return w.StageImport(t.Context(), "retry", []database.ImportRow{{Index: 0, Data: row, Selected: true}}, []database.ImportJob{{Index: 0, Address: "1 Example St", Rows: []int{0}}})
			}); err != nil {
				t.Fatal(err)
			}
			g := &fakeGeocoder{}
			calls := 0
			g.result = func(ctx context.Context, _ string, retries int) (*geocoding.GeocodingResult, error) {
				calls++
				if tc.timeoutLast && calls == 3 {
					<-ctx.Done()
					return nil, ctx.Err()
				}
				if retries != 3 {
					t.Fatalf("attempts per round = %d", retries)
				}
				if tc.configuration {
					return nil, &geocoding.ErrGeocodingFailed{Configuration: true, Cause: geocoding.ErrNotConfigured}
				}
				if tc.quota {
					return nil, &geocoding.ErrGeocodingFailed{Configuration: true, Cause: database.ErrUsageExhausted}
				}
				if tc.permanent {
					return nil, geocoding.ErrNoGeocodingResults
				}
				if calls == tc.successAt {
					return &geocoding.GeocodingResult{Coords: models.Coordinates{Lat: 35, Lng: -79}}, nil
				}
				return nil, &geocoding.ErrGeocodingFailed{Temporary: true}
			}
			for round := 1; round <= 4; round++ {
				// Reopen storage each round to verify the budget isn't process-local.
				replica := postgrestest.OpenURL(t, url)
				job, ok, err := replica.ImportJobs().Claim(t.Context(), fmt.Sprint(round), time.Minute)
				if err != nil || !ok || job.Attempts != round {
					t.Fatalf("round %d claim: %+v %v %v", round, job, ok, err)
				}
				if round > tc.crashRounds {
					s := &Store{geocoder: g, durableJobs: replica.ImportJobs()}
					err := s.processJob(t.Context(), job)
					if round < 3 && !tc.permanent {
						if err == nil {
							t.Fatal("expected temporary failure")
						}
					} else if err != nil {
						t.Fatal(err)
					}
				}
				done, _, err := replica.ImportJobs().Progress(t.Context(), "retry")
				if err != nil {
					t.Fatal(err)
				}
				if done == 1 {
					break
				}
				// Advance the lease/cooldown locally instead of sleeping between rounds.
				if _, err := conn.Exec(t.Context(), "UPDATE import_jobs SET claimed_until=clock_timestamp()-interval '1 second'"); err != nil {
					t.Fatal(err)
				}
			}
			if calls != tc.wantCalls {
				t.Fatalf("calls = %d, want %d", calls, tc.wantCalls)
			}
			done, total, err := db.ImportJobs().Progress(t.Context(), "retry")
			if err != nil || done != 1 || total != 1 {
				t.Fatalf("progress %d/%d: %v", done, total, err)
			}
			rows, err := db.ImportJobs().Rows(t.Context(), "retry")
			if err != nil {
				t.Fatal(err)
			}
			var result Row
			if err := json.Unmarshal(rows[0].Data, &result); err != nil {
				t.Fatal(err)
			}
			if result.NeedsGeocoding || result.HasCoordinates != (tc.successAt > 0) {
				t.Fatalf("result: %+v", result)
			}
			if tc.successAt == 0 && (len(result.Errors) == 0 || rows[0].Selected) {
				t.Fatalf("failed row not excluded: %+v %+v", result, rows[0])
			}
			if tc.configuration && !containsString(result.Errors, "Google address lookup is unavailable. Ask an administrator to check the saved key, Geocoding API permissions and billing, then upload the file again.") {
				t.Fatalf("missing configuration guidance: %v", result.Errors)
			}
			if tc.quota && !containsString(result.Errors, "The app's Google usage limit has been reached. Ask an administrator to check usage before uploading the file again.") {
				t.Fatalf("missing usage guidance: %v", result.Errors)
			}
			if _, ok, err := db.ImportJobs().Claim(t.Context(), "extra", time.Minute); err != nil || ok {
				t.Fatalf("finished job claimed: %v %v", ok, err)
			}
		})
	}
}
