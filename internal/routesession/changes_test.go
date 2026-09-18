package routesession

import (
	"context"
	"testing"
)

func descriptions(changes []RouteChange) []string {
	out := make([]string, len(changes))
	for i, change := range changes {
		out[i] = change.Description()
	}
	return out
}

func assertChanges(t *testing.T, snapshot Snapshot, want ...string) {
	t.Helper()
	got := descriptions(snapshot.Changes)
	if len(got) != len(want) {
		t.Fatalf("changes = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("changes = %q, want %q", got, want)
		}
	}
}

func TestSnapshotChangesReportNetMovesAndSwaps(t *testing.T) {
	store, created := changedFixture(t)
	assertChanges(t, created)

	moved, err := store.ApplyMoves(context.Background(), created.ID, []Move{{ParticipantID: 1, FromRouteIndex: 0, ToRouteIndex: 1, InsertAtPosition: -1}}, ApplyMovesOptions{})
	if err != nil {
		t.Fatal(err)
	}
	assertChanges(t, moved, "A moved from D1 to D2.")

	// Moving the rider back leaves no net change to explain.
	back, err := store.ApplyMoves(context.Background(), created.ID, []Move{{ParticipantID: 1, FromRouteIndex: 1, ToRouteIndex: 0, InsertAtPosition: -1}}, ApplyMovesOptions{})
	if err != nil {
		t.Fatal(err)
	}
	assertChanges(t, back)

	// Reordering stops within one car is not a change.
	reordered, err := store.ApplyMoves(context.Background(), created.ID, []Move{{ParticipantID: 2, FromRouteIndex: 0, ToRouteIndex: 0, InsertAtPosition: 0}}, ApplyMovesOptions{})
	if err != nil {
		t.Fatal(err)
	}
	assertChanges(t, reordered)

	swapped, err := store.SwapDrivers(context.Background(), created.ID, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	// Riders who changed driver only because of the swap are not listed again.
	assertChanges(t, swapped, "D1 and D2 swapped routes.")

	added, err := store.AddDriver(context.Background(), created.ID, 12)
	if err != nil {
		t.Fatal(err)
	}
	assertChanges(t, added, "D1 and D2 swapped routes.")

	toNew, err := store.ApplyMoves(context.Background(), created.ID, []Move{{ParticipantID: 3, FromRouteIndex: 1, ToRouteIndex: 2, InsertAtPosition: -1}}, ApplyMovesOptions{})
	if err != nil {
		t.Fatal(err)
	}
	assertChanges(t, toNew, "D1 and D2 swapped routes.", "C moved from D2 to D3.")

	reset, err := store.Reset(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertChanges(t, reset)
}

func TestReviewerNoteSurvivesEditsAndReachesCommit(t *testing.T) {
	store, created := changedFixture(t)
	noted, err := store.SetReviewerNote(context.Background(), created.ID, "  D1 lives closer to A  ")
	if err != nil {
		t.Fatal(err)
	}
	if noted.ReviewerNote != "D1 lives closer to A" {
		t.Fatalf("ReviewerNote = %q, want trimmed note", noted.ReviewerNote)
	}
	moved, err := store.ApplyMoves(context.Background(), created.ID, []Move{{ParticipantID: 1, FromRouteIndex: 0, ToRouteIndex: 1, InsertAtPosition: -1}}, ApplyMovesOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if moved.ReviewerNote != "D1 lives closer to A" {
		t.Fatalf("edit dropped the note: %q", moved.ReviewerNote)
	}
	var committed CommitSnapshot
	if err := store.Commit(context.Background(), created.ID, func(_ context.Context, snapshot CommitSnapshot) error {
		committed = snapshot
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if committed.ReviewerNote != "D1 lives closer to A" {
		t.Fatalf("commit ReviewerNote = %q", committed.ReviewerNote)
	}
	if got := descriptions(committed.Changes); len(got) != 1 || got[0] != "A moved from D1 to D2." {
		t.Fatalf("commit changes = %q", got)
	}
	if _, err := store.SetReviewerNote(context.Background(), "missing", "x"); err != ErrNotFound {
		t.Fatalf("SetReviewerNote on unknown session error = %v, want ErrNotFound", err)
	}
}
