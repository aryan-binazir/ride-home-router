package postgres

import (
	"ride-home-router/internal/importer"
	"ride-home-router/internal/models"
	"testing"
)

func TestRosterWriteKeysMatchRosterKey(t *testing.T) {
	const name = "O’Brien"
	const address = "123 Main St."
	want := models.RosterKey(name, address)

	if got := importer.DuplicateKey(name, address); got != want {
		t.Errorf("importer duplicate key = %q, models.RosterKey() = %q", got, want)
	}

	participantWrites := (&participantRepository{}).writes()
	participant := &models.Participant{Name: name, Address: address}
	if got := participantWrites.key(participant); got != want {
		t.Errorf("participant key = %q, models.RosterKey() = %q", got, want)
	}

	driverWrites := (&driverRepository{}).writes()
	driver := &models.Driver{Name: name, Address: address}
	if got := driverWrites.key(driver); got != want {
		t.Errorf("driver key = %q, models.RosterKey() = %q", got, want)
	}
}
