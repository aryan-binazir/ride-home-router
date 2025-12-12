package models

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParticipantGetCoords(t *testing.T) {
	p := Participant{
		Lat: 40.7128,
		Lng: -74.0060,
	}

	coords := p.GetCoords()

	assert.Equal(t, 40.7128, coords.Lat)
	assert.Equal(t, -74.0060, coords.Lng)
}

func TestDriverGetCoords(t *testing.T) {
	d := Driver{
		Lat: 51.5074,
		Lng: -0.1278,
	}

	coords := d.GetCoords()

	assert.Equal(t, 51.5074, coords.Lat)
	assert.Equal(t, -0.1278, coords.Lng)
}

func TestSettingsGetCoords(t *testing.T) {
	s := Settings{
		InstituteLat: 48.8566,
		InstituteLng: 2.3522,
	}

	coords := s.GetCoords()

	assert.Equal(t, 48.8566, coords.Lat)
	assert.Equal(t, 2.3522, coords.Lng)
}

func TestCoordinatesCreation(t *testing.T) {
	coords := Coordinates{
		Lat: 35.6762,
		Lng: 139.6503,
	}

	assert.Equal(t, 35.6762, coords.Lat)
	assert.Equal(t, 139.6503, coords.Lng)
}
