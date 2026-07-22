package routesession

import (
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
