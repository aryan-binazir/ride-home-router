package plandraft

import "testing"

func TestStoreCopiesDraftState(t *testing.T) {
	store := NewStore()
	t.Cleanup(store.Close)
	id := store.NewID()
	store.Update(id, func(d *Draft) {
		d.ParticipantIDs = []int64{1, 2}
		d.DriverVehicleIDs[7] = 9
	})

	draft := store.Get(id)
	draft.ParticipantIDs[0] = 99
	draft.DriverVehicleIDs[7] = 88

	got := store.Get(id)
	if got.ParticipantIDs[0] != 1 || got.DriverVehicleIDs[7] != 9 {
		t.Fatalf("store leaked mutable state: %#v", got)
	}
}
