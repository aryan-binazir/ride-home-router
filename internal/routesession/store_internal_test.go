package routesession

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestSnapshotTouchesTTLAndDeleteExpiredRemovesIdleSession(t *testing.T) {
	now := time.Unix(100, 0)
	store := newStore(nil, time.Minute, time.Hour, func() time.Time { return now })
	t.Cleanup(store.Close)
	created := store.Create(CreateInput{})

	now = now.Add(45 * time.Second)
	if _, ok := store.Snapshot(created.ID); !ok {
		t.Fatal("session expired before touch")
	}
	now = now.Add(45 * time.Second)
	store.deleteExpired(now)
	if _, ok := store.Snapshot(created.ID); !ok {
		t.Fatal("touch did not extend TTL")
	}
	now = now.Add(61 * time.Second)
	store.deleteExpired(now)
	if _, ok := store.Snapshot(created.ID); ok {
		t.Fatal("idle session survived TTL cleanup")
	}
}

func TestDeleteExpiredSkipsBusySession(t *testing.T) {
	now := time.Unix(100, 0)
	store := newStore(nil, time.Minute, time.Hour, func() time.Time { return now })
	t.Cleanup(store.Close)
	busy := store.Create(CreateInput{})
	other := store.Create(CreateInput{})

	busyState := store.sessions[busy.ID]
	busyState.mu.Lock()
	defer busyState.mu.Unlock()

	done := make(chan struct{})
	go func() {
		store.deleteExpired(now)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("cleanup blocked on a busy session")
	}
	if _, ok := store.Snapshot(other.ID); !ok {
		t.Fatal("cleanup of a busy session blocked or removed an unrelated session")
	}
}

func TestFailedCommitRefreshesSessionTTL(t *testing.T) {
	now := time.Unix(100, 0)
	store := newStore(nil, time.Minute, time.Hour, func() time.Time { return now })
	t.Cleanup(store.Close)
	created := store.Create(CreateInput{})
	wantErr := errors.New("persistence failed")

	err := store.Commit(context.Background(), created.ID, func(context.Context, CommitSnapshot) error {
		now = now.Add(2 * time.Minute)
		return wantErr
	})
	if err != wantErr {
		t.Fatalf("Commit error = %v, want callback error", err)
	}
	if _, ok := store.Snapshot(created.ID); !ok {
		t.Fatal("session expired immediately after failed persistence")
	}
}

func TestCommittedMarkerExpiresWithSessionTTL(t *testing.T) {
	now := time.Unix(100, 0)
	store := newStore(nil, time.Minute, time.Hour, func() time.Time { return now })
	t.Cleanup(store.Close)
	created := store.Create(CreateInput{})

	if err := store.Commit(context.Background(), created.ID, func(context.Context, CommitSnapshot) error { return nil }); err != nil {
		t.Fatalf("Commit error = %v", err)
	}
	if err := store.Commit(context.Background(), created.ID, func(context.Context, CommitSnapshot) error { return nil }); !errors.Is(err, ErrAlreadyCommitted) {
		t.Fatalf("second Commit error = %v, want ErrAlreadyCommitted", err)
	}

	now = now.Add(time.Minute + time.Second)
	store.deleteExpired(now)
	if err := store.Commit(context.Background(), created.ID, func(context.Context, CommitSnapshot) error { return nil }); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Commit after marker expiry error = %v, want ErrNotFound", err)
	}
}
