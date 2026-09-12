package plandraft

import (
	"ride-home-router/internal/postgres/postgrestest"
	"testing"
)

func TestPersistentDraftRejectsStaleAdoptionAndDetachment(t *testing.T) {
	db := postgrestest.Open(t)
	a, b := NewPersistentStore(db.Workflows()), NewPersistentStore(db.Workflows())
	id, original, err := a.CreateDraft(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	updated, err := b.Edit(t.Context(), id, func(d *Draft) { d.RouteTime = "12:34" })
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := a.Attach(t.Context(), id, original.Revision, "stale"); err != nil || ok {
		t.Fatalf("stale adoption: %v %v", ok, err)
	}
	if _, ok, err := a.Attach(t.Context(), id, updated.Revision, "winner"); err != nil || !ok {
		t.Fatalf("adoption: %v %v", ok, err)
	}
	if ok, err := b.Detach(t.Context(), id, "stale"); err != nil || ok {
		t.Fatalf("stale detach: %v %v", ok, err)
	}
	current, ok, err := b.Load(t.Context(), id)
	if err != nil || !ok || current.RouteSessionID != "winner" || current.RouteTime != "12:34" {
		t.Fatalf("current: %+v %v", current, err)
	}
	if ok, err := b.Detach(t.Context(), id, "winner"); err != nil || !ok {
		t.Fatalf("detach: %v %v", ok, err)
	}
}
