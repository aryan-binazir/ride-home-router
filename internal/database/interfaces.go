package database

import (
	"context"

	"ride-home-router/internal/models"
)

// DataStore is the interface for data persistence
type DataStore interface {
	Close() error
	HealthCheck(ctx context.Context) error
	Participants() ParticipantRepository
	Drivers() DriverRepository
	Settings() SettingsRepository
	ActivityLocations() ActivityLocationRepository
	OrganizationVehicles() OrganizationVehicleRepository
	Events() EventRepository
	DistanceCache() DistanceCacheRepository
}

// ParticipantRepository handles participant persistence
type ParticipantRepository interface {
	List(ctx context.Context, search string) ([]models.Participant, error)
	GetByID(ctx context.Context, id int64) (*models.Participant, error)
	GetByIDs(ctx context.Context, ids []int64) ([]models.Participant, error)
	Create(ctx context.Context, p *models.Participant) (*models.Participant, error)
	Update(ctx context.Context, p *models.Participant) (*models.Participant, error)
	Delete(ctx context.Context, id int64) error
}

// DriverRepository handles driver persistence
type DriverRepository interface {
	List(ctx context.Context, search string) ([]models.Driver, error)
	GetByID(ctx context.Context, id int64) (*models.Driver, error)
	GetByIDs(ctx context.Context, ids []int64) ([]models.Driver, error)
	Create(ctx context.Context, d *models.Driver) (*models.Driver, error)
	Update(ctx context.Context, d *models.Driver) (*models.Driver, error)
	Delete(ctx context.Context, id int64) error
}

// SettingsRepository handles settings persistence
type SettingsRepository interface {
	Get(ctx context.Context) (*models.Settings, error)
	Update(ctx context.Context, s *models.Settings) error
}

// ActivityLocationRepository handles activity location persistence
type ActivityLocationRepository interface {
	List(ctx context.Context) ([]models.ActivityLocation, error)
	GetByID(ctx context.Context, id int64) (*models.ActivityLocation, error)
	Create(ctx context.Context, loc *models.ActivityLocation) (*models.ActivityLocation, error)
	Delete(ctx context.Context, id int64) error
}

// OrganizationVehicleRepository handles organization vehicle persistence
type OrganizationVehicleRepository interface {
	List(ctx context.Context) ([]models.OrganizationVehicle, error)
	GetByID(ctx context.Context, id int64) (*models.OrganizationVehicle, error)
	GetByIDs(ctx context.Context, ids []int64) ([]models.OrganizationVehicle, error)
	Create(ctx context.Context, v *models.OrganizationVehicle) (*models.OrganizationVehicle, error)
	Update(ctx context.Context, v *models.OrganizationVehicle) (*models.OrganizationVehicle, error)
	Delete(ctx context.Context, id int64) error
}

// EventRepository handles event/history persistence
type EventRepository interface {
	List(ctx context.Context, limit, offset int) ([]models.Event, int, error)
	GetByID(ctx context.Context, id int64) (*models.Event, []models.EventAssignment, *models.EventSummary, error)
	Create(ctx context.Context, event *models.Event, assignments []models.EventAssignment, summary *models.EventSummary) (*models.Event, error)
	Delete(ctx context.Context, id int64) error
}

// DistanceCacheRepository handles distance cache persistence
type DistanceCacheRepository interface {
	Get(ctx context.Context, origin, dest models.Coordinates) (*models.DistanceCacheEntry, error)
	GetBatch(ctx context.Context, pairs []struct{ Origin, Dest models.Coordinates }) (map[string]*models.DistanceCacheEntry, error)
	Set(ctx context.Context, entry *models.DistanceCacheEntry) error
	SetBatch(ctx context.Context, entries []models.DistanceCacheEntry) error
	Clear(ctx context.Context) error
}
