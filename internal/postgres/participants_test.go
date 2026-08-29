package postgres_test

import (
	"context"
	"errors"
	"ride-home-router/internal/database"
	"ride-home-router/internal/models"
	"ride-home-router/internal/postgres/postgrestest"
	"testing"
)

func TestParticipantRepositoryRoundTrip(t *testing.T) {
	store := postgrestest.Open(t)
	ctx := context.Background()

	participant, err := store.Participants().CreateWithLabels(ctx, &models.Participant{
		Name:        "Rider One",
		Address:     "1000 Collins Crossing Dr",
		AddressName: "Collins Crossing",
		Lat:         40.1,
		Lng:         -73.9,
	}, nil)
	if err != nil {
		t.Fatalf("CreateWithLabels() error = %v", err)
	}
	if participant.ID == 0 || participant.AddressName != "Collins Crossing" {
		t.Fatalf("created participant = %#v, want id and address name", participant)
	}

	got, err := store.Participants().GetByID(ctx, participant.ID)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if got.AddressName != "Collins Crossing" || got.Name != "Rider One" {
		t.Fatalf("GetByID() = %#v", got)
	}

	participants, err := store.Participants().List(ctx, "rider")
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(participants) != 1 || participants[0].AddressName != "Collins Crossing" {
		t.Fatalf("List() participants = %#v, want case-insensitive match with address name", participants)
	}

	got.AddressName = "Community Center"
	if _, err := store.Participants().Update(ctx, got); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	updated, err := store.Participants().GetByID(ctx, participant.ID)
	if err != nil {
		t.Fatalf("GetByID() after update error = %v", err)
	}
	if updated.AddressName != "Community Center" {
		t.Fatalf("updated AddressName = %q, want Community Center", updated.AddressName)
	}

	byIDs, err := store.Participants().GetByIDs(ctx, []int64{participant.ID, 9999})
	if err != nil {
		t.Fatalf("GetByIDs() error = %v", err)
	}
	if len(byIDs) != 1 {
		t.Fatalf("GetByIDs() len = %d, want 1", len(byIDs))
	}

	if err := store.Participants().Delete(ctx, participant.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, err := store.Participants().GetByID(ctx, participant.ID); err != database.ErrNotFound {
		t.Fatalf("GetByID() after delete error = %v, want ErrNotFound", err)
	}
	if _, err := store.Participants().Update(ctx, participant); !errors.Is(err, database.ErrNotFound) {
		t.Fatalf("Update() after delete error = %v, want ErrNotFound", err)
	}
}

func TestParticipantRepositorySoftDeleteAndRestore(t *testing.T) {
	store := postgrestest.Open(t)
	ctx := context.Background()
	label, err := store.Labels().Create(ctx, &models.Label{Name: "Retained"})
	if err != nil {
		t.Fatalf("create label: %v", err)
	}
	participant, err := store.Participants().CreateWithLabels(ctx, &models.Participant{
		Name: "Archived Rider", Address: "1 Archive Way", Lat: 40, Lng: -73,
	}, []int64{label.ID})
	if err != nil {
		t.Fatalf("CreateWithLabels() error = %v", err)
	}
	if err := store.Participants().Restore(ctx, participant.ID); !errors.Is(err, database.ErrNotFound) {
		t.Fatalf("Restore() live row error = %v, want ErrNotFound", err)
	}
	if err := store.Participants().Delete(ctx, participant.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if err := store.Participants().Delete(ctx, participant.ID); !errors.Is(err, database.ErrNotFound) {
		t.Fatalf("second Delete() error = %v, want ErrNotFound", err)
	}

	for _, search := range []string{"", "Archived"} {
		participants, err := store.Participants().List(ctx, search)
		if err != nil || len(participants) != 0 {
			t.Fatalf("List(%q) = %#v, %v; want no archived rows", search, participants, err)
		}
	}
	if _, err := store.Participants().GetByID(ctx, participant.ID); !errors.Is(err, database.ErrNotFound) {
		t.Fatalf("GetByID() archived row error = %v, want ErrNotFound", err)
	}
	if participants, err := store.Participants().GetByIDs(ctx, []int64{participant.ID}); err != nil || len(participants) != 0 {
		t.Fatalf("GetByIDs() archived row = %#v, %v; want none", participants, err)
	}
	deleted, err := store.Participants().ListDeleted(ctx)
	if err != nil || len(deleted) != 1 || deleted[0].ID != participant.ID || deleted[0].DeletedAt == nil {
		t.Fatalf("ListDeleted() = %#v, %v; want archived row with DeletedAt", deleted, err)
	}

	participant.Name = "Should Not Change"
	if _, err := store.Participants().UpdateWithLabels(ctx, participant, nil); !errors.Is(err, database.ErrNotFound) {
		t.Fatalf("UpdateWithLabels() archived row error = %v, want ErrNotFound", err)
	}
	if err := store.Participants().Restore(ctx, participant.ID); err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	restored, err := store.Participants().GetByID(ctx, participant.ID)
	if err != nil || restored.Name != "Archived Rider" || restored.DeletedAt != nil {
		t.Fatalf("GetByID() restored row = %#v, %v", restored, err)
	}
	labels, err := store.Labels().ListLabelsForParticipant(ctx, participant.ID)
	if err != nil || len(labels) != 1 || labels[0].ID != label.ID {
		t.Fatalf("restored labels = %#v, %v; want retained label", labels, err)
	}
	if deleted, err := store.Participants().ListDeleted(ctx); err != nil || len(deleted) != 0 {
		t.Fatalf("ListDeleted() after restore = %#v, %v; want none", deleted, err)
	}
}
