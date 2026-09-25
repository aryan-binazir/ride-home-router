package routesession

import (
	"context"
	"ride-home-router/internal/database"
	"testing"
)

func mustCreate(t testing.TB, store *Store, input CreateInput) Snapshot {
	t.Helper()
	snapshot, err := store.CreateContext(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func mustLoad(t testing.TB, store *Store, id string) (Snapshot, bool) {
	t.Helper()
	snapshot, found, err := store.Load(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot, found
}

func commitSession(store *Store, ctx context.Context, id string, save func(context.Context, CommitSnapshot) error) error {
	return store.CommitEvent(ctx, id, func(ctx context.Context, snapshot CommitSnapshot, _ database.WorkflowWrites) error {
		return save(ctx, snapshot)
	})
}
