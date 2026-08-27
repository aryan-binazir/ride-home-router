package postgres_test

import (
	"context"
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
}
