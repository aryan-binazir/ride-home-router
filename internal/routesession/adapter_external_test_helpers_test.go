package routesession_test

import (
	"context"
	"ride-home-router/internal/database"
	"ride-home-router/internal/routesession"
	"testing"
)

func mustCreate(t testing.TB, store *routesession.Store, input routesession.CreateInput) routesession.Snapshot {
	t.Helper()
	snapshot, err := store.CreateContext(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func mustLoad(t testing.TB, store *routesession.Store, id string) (routesession.Snapshot, bool) {
	t.Helper()
	snapshot, found, err := store.Load(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot, found
}

func commitSession(store *routesession.Store, ctx context.Context, id string, save func(context.Context, routesession.CommitSnapshot) error) error {
	return store.CommitEvent(ctx, id, func(ctx context.Context, snapshot routesession.CommitSnapshot, _ database.WorkflowWrites) error {
		return save(ctx, snapshot)
	})
}
