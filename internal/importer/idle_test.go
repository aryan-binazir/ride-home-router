package importer

import (
	"context"
	"errors"
	"ride-home-router/internal/database"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

type idleJobs struct {
	database.ImportJobRepository
	claims atomic.Int64
	err    error
}

func (j *idleJobs) Claim(context.Context, string, time.Duration) (database.ImportJob, bool, error) {
	j.claims.Add(1)
	return database.ImportJob{}, false, j.err
}

type idleRecords struct{ database.WorkflowRepository }

func TestPersistentIdleWorkerBoundsTrafficAndKeepsRecovering(t *testing.T) {
	for _, claimErr := range []error{nil, errors.New("database temporarily unavailable")} {
		synctest.Test(t, func(t *testing.T) {
			jobs := &idleJobs{err: claimErr}
			s := NewPersistentStore(t.Context(), nil, nil, idleRecords{}, jobs)
			defer s.Close()
			time.Sleep(time.Minute)
			synctest.Wait()
			if got := jobs.claims.Load(); got < 1 || got > 12 {
				t.Fatalf("idle claim requests in one minute = %d, want 1..12", got)
			}
			for range 3 {
				before := jobs.claims.Load()
				time.Sleep(31 * time.Second)
				synctest.Wait()
				if got := jobs.claims.Load() - before; got < 1 || got > 2 {
					t.Fatalf("recovery claims in 31 seconds = %d, want 1..2", got)
				}
			}
			before := time.Now()
			s.Close()
			if elapsed := time.Since(before); elapsed != 0 {
				t.Fatalf("Close waited %s for idle timer", elapsed)
			}
			count := jobs.claims.Load()
			time.Sleep(time.Minute)
			if jobs.claims.Load() != count {
				t.Fatal("worker queried after Close")
			}
		})
	}
}

type recoveringJobs struct {
	database.ImportJobRepository
	available time.Time
	finished  time.Time
}

func (j *recoveringJobs) Claim(context.Context, string, time.Duration) (database.ImportJob, bool, error) {
	if time.Now().Before(j.available) || !j.finished.IsZero() {
		return database.ImportJob{}, false, nil
	}
	// An exhausted job left by a crash still needs finalization, with no new API call.
	return database.ImportJob{SessionID: "recovery", Rows: []int{0}, Attempts: 4}, true, nil
}

func (j *recoveringJobs) RowsByIndices(context.Context, string, []int) ([]database.ImportRow, error) {
	return []database.ImportRow{{Index: 0, Data: []byte(`{"NeedsGeocoding":true}`)}}, nil
}

func (j *recoveringJobs) Finish(context.Context, database.ImportJob, []database.ImportRow, bool) error {
	j.finished = time.Now()
	return nil
}

func (j *recoveringJobs) Release(context.Context, database.ImportJob) error { return nil }
func TestPersistentWorkerRecoversDelayedJobsWithoutLocalWake(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		jobs := &recoveringJobs{available: time.Now().Add(65 * time.Second)}
		s := NewPersistentStore(t.Context(), nil, nil, idleRecords{}, jobs)
		defer s.Close()
		time.Sleep(96 * time.Second)
		synctest.Wait()
		if jobs.finished.IsZero() || jobs.finished.Before(jobs.available) || jobs.finished.Sub(jobs.available) > 30*time.Second {
			t.Fatalf("job available %s, finished %s", jobs.available, jobs.finished)
		}
	})
}
