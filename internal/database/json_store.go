package database

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sort"
	"sync"
	"time"

	"ride-home-router/internal/models"
)

// JSONData represents the structure of the JSON file
type JSONData struct {
	Settings             JSONSettings                  `json:"settings"`
	ActivityLocations    []models.ActivityLocation     `json:"activity_locations"`
	Participants         []models.Participant          `json:"participants"`
	Drivers              []models.Driver               `json:"drivers"`
	OrganizationVehicles []models.OrganizationVehicle  `json:"organization_vehicles"`
	Events               []JSONEvent                   `json:"events"`
	NextIDs              struct {
		Participant         int64 `json:"participant"`
		Driver              int64 `json:"driver"`
		Event               int64 `json:"event"`
		ActivityLocation    int64 `json:"activity_location"`
		OrganizationVehicle int64 `json:"organization_vehicle"`
	} `json:"next_ids"`
}

// JSONSettings stores settings in the JSON file
type JSONSettings struct {
	InstituteAddress           string  `json:"institute_address"` // Deprecated
	InstituteLat               float64 `json:"institute_lat"`     // Deprecated
	InstituteLng               float64 `json:"institute_lng"`     // Deprecated
	SelectedActivityLocationID int64   `json:"selected_activity_location_id"`
	UseMiles                   bool    `json:"use_miles"`
}

// JSONEvent stores event data including assignments and summary
type JSONEvent struct {
	models.Event
	Assignments []models.EventAssignment `json:"assignments"`
	Summary     models.EventSummary      `json:"summary"`
}

// JSONStore is a JSON file-based data store
type JSONStore struct {
	filePath string
	data     *JSONData
	mu       sync.RWMutex

	participantRepository         ParticipantRepository
	driverRepository              DriverRepository
	settingsRepository            SettingsRepository
	activityLocationRepository    ActivityLocationRepository
	organizationVehicleRepository OrganizationVehicleRepository
	eventRepository               EventRepository
	distanceCacheRepository       DistanceCacheRepository
}

func (s *JSONStore) Participants() ParticipantRepository              { return s.participantRepository }
func (s *JSONStore) Drivers() DriverRepository                        { return s.driverRepository }
func (s *JSONStore) Settings() SettingsRepository                     { return s.settingsRepository }
func (s *JSONStore) ActivityLocations() ActivityLocationRepository    { return s.activityLocationRepository }
func (s *JSONStore) OrganizationVehicles() OrganizationVehicleRepository { return s.organizationVehicleRepository }
func (s *JSONStore) Events() EventRepository                          { return s.eventRepository }
func (s *JSONStore) DistanceCache() DistanceCacheRepository           { return s.distanceCacheRepository }

// NewJSONStore creates a new JSON-based data store
func NewJSONStore(distanceCache DistanceCacheRepository) (*JSONStore, error) {
	// Migrate old data if needed
	if err := MigrateOldData(); err != nil {
		log.Printf("Warning: failed to migrate old data: %v", err)
	}

	filePath, err := GetDataFilePath()
	if err != nil {
		return nil, fmt.Errorf("failed to get data file path: %w", err)
	}
	log.Printf("Using JSON data file: %s", filePath)

	store := &JSONStore{
		filePath: filePath,
		data:     &JSONData{},
	}

	// Load existing data or create new
	if err := store.load(); err != nil {
		return nil, err
	}

	// Initialize repositories
	store.participantRepository = &jsonParticipantRepository{
		genericRepository: &genericRepository[models.Participant]{
			store:       store,
			getSlice:    func(d *JSONData) *[]models.Participant { return &d.Participants },
			getNextID:   func(d *JSONData) *int64 { return &d.NextIDs.Participant },
			entityName:  "participant",
			getID:       func(p *models.Participant) int64 { return p.ID },
			setID:       func(p *models.Participant, id int64) { p.ID = id },
			getName:     func(p *models.Participant) string { return p.Name },
			withTimestamps: true,
			getCreatedAt: func(p *models.Participant) time.Time { return p.CreatedAt },
			setCreatedAt: func(p *models.Participant, t time.Time) { p.CreatedAt = t },
			setUpdatedAt: func(p *models.Participant, t time.Time) { p.UpdatedAt = t },
		},
	}

	store.driverRepository = &jsonDriverRepository{
		genericRepository: &genericRepository[models.Driver]{
			store:       store,
			getSlice:    func(d *JSONData) *[]models.Driver { return &d.Drivers },
			getNextID:   func(d *JSONData) *int64 { return &d.NextIDs.Driver },
			entityName:  "driver",
			getID:       func(dr *models.Driver) int64 { return dr.ID },
			setID:       func(dr *models.Driver, id int64) { dr.ID = id },
			getName:     func(dr *models.Driver) string { return dr.Name },
			withTimestamps: true,
			getCreatedAt: func(dr *models.Driver) time.Time { return dr.CreatedAt },
			setCreatedAt: func(dr *models.Driver, t time.Time) { dr.CreatedAt = t },
			setUpdatedAt: func(dr *models.Driver, t time.Time) { dr.UpdatedAt = t },
		},
	}

	store.activityLocationRepository = &jsonActivityLocationRepository{
		genericRepository: &genericRepository[models.ActivityLocation]{
			store:       store,
			getSlice:    func(d *JSONData) *[]models.ActivityLocation { return &d.ActivityLocations },
			getNextID:   func(d *JSONData) *int64 { return &d.NextIDs.ActivityLocation },
			entityName:  "activity location",
			getID:       func(loc *models.ActivityLocation) int64 { return loc.ID },
			setID:       func(loc *models.ActivityLocation, id int64) { loc.ID = id },
			getName:     func(loc *models.ActivityLocation) string { return loc.Name },
			withTimestamps: false,
		},
	}

	store.organizationVehicleRepository = &jsonOrganizationVehicleRepository{
		genericRepository: &genericRepository[models.OrganizationVehicle]{
			store:       store,
			getSlice:    func(d *JSONData) *[]models.OrganizationVehicle { return &d.OrganizationVehicles },
			getNextID:   func(d *JSONData) *int64 { return &d.NextIDs.OrganizationVehicle },
			entityName:  "organization vehicle",
			getID:       func(v *models.OrganizationVehicle) int64 { return v.ID },
			setID:       func(v *models.OrganizationVehicle, id int64) { v.ID = id },
			getName:     func(v *models.OrganizationVehicle) string { return v.Name },
			withTimestamps: true,
			getCreatedAt: func(v *models.OrganizationVehicle) time.Time { return v.CreatedAt },
			setCreatedAt: func(v *models.OrganizationVehicle, t time.Time) { v.CreatedAt = t },
			setUpdatedAt: func(v *models.OrganizationVehicle, t time.Time) { v.UpdatedAt = t },
		},
	}

	store.settingsRepository = &jsonSettingsRepository{store: store}
	store.eventRepository = &jsonEventRepository{store: store}
	store.distanceCacheRepository = distanceCache

	return store, nil
}

func (s *JSONStore) load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.filePath)
	if os.IsNotExist(err) {
		// Initialize with empty data
		s.data = &JSONData{
			ActivityLocations:    []models.ActivityLocation{},
			Participants:         []models.Participant{},
			Drivers:              []models.Driver{},
			OrganizationVehicles: []models.OrganizationVehicle{},
			Events:               []JSONEvent{},
		}
		s.data.NextIDs.Participant = 1
		s.data.NextIDs.Driver = 1
		s.data.NextIDs.Event = 1
		s.data.NextIDs.ActivityLocation = 1
		s.data.NextIDs.OrganizationVehicle = 1
		return s.saveUnlocked()
	}
	if err != nil {
		return fmt.Errorf("failed to read data file: %w", err)
	}

	if err := json.Unmarshal(data, s.data); err != nil {
		return fmt.Errorf("failed to parse data file: %w", err)
	}

	// Ensure slices are not nil
	if s.data.ActivityLocations == nil {
		s.data.ActivityLocations = []models.ActivityLocation{}
	}
	if s.data.Participants == nil {
		s.data.Participants = []models.Participant{}
	}
	if s.data.Drivers == nil {
		s.data.Drivers = []models.Driver{}
	}
	if s.data.OrganizationVehicles == nil {
		s.data.OrganizationVehicles = []models.OrganizationVehicle{}
	}
	if s.data.Events == nil {
		s.data.Events = []JSONEvent{}
	}
	if s.data.NextIDs.OrganizationVehicle == 0 {
		s.data.NextIDs.OrganizationVehicle = 1
	}

	// Migrate old institute settings to first activity location
	if s.data.Settings.InstituteAddress != "" && len(s.data.ActivityLocations) == 0 {
		log.Printf("Migrating old institute settings to activity location")
		loc := models.ActivityLocation{
			ID:      1,
			Name:    "Institute",
			Address: s.data.Settings.InstituteAddress,
			Lat:     s.data.Settings.InstituteLat,
			Lng:     s.data.Settings.InstituteLng,
		}
		s.data.ActivityLocations = append(s.data.ActivityLocations, loc)
		s.data.Settings.SelectedActivityLocationID = 1
		s.data.NextIDs.ActivityLocation = 2
		if err := s.saveUnlocked(); err != nil {
			log.Printf("Failed to save migrated data: %v", err)
		}
	}

	log.Printf("Loaded data: %d activity locations, %d participants, %d drivers, %d events",
		len(s.data.ActivityLocations), len(s.data.Participants), len(s.data.Drivers), len(s.data.Events))

	return nil
}

func (s *JSONStore) save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveUnlocked()
}

func (s *JSONStore) saveUnlocked() error {
	data, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal data: %w", err)
	}

	// Write to temp file first, then rename (atomic)
	tmpFile := s.filePath + ".tmp"
	if err := os.WriteFile(tmpFile, data, 0600); err != nil {
		return fmt.Errorf("failed to write temp file: %w", err)
	}

	if err := os.Rename(tmpFile, s.filePath); err != nil {
		return fmt.Errorf("failed to rename temp file: %w", err)
	}

	return nil
}

// Close is a no-op for JSON store (data is saved after each operation)
func (s *JSONStore) Close() error {
	return nil
}

// HealthCheck always returns nil for JSON store
func (s *JSONStore) HealthCheck(ctx context.Context) error {
	return nil
}

// ==================== Participant Repository ====================

type jsonParticipantRepository struct {
	*genericRepository[models.Participant]
}

func (r *jsonParticipantRepository) List(ctx context.Context, search string) ([]models.Participant, error) {
	return r.list(ctx, search)
}

func (r *jsonParticipantRepository) GetByID(ctx context.Context, id int64) (*models.Participant, error) {
	return r.getByID(ctx, id)
}

func (r *jsonParticipantRepository) GetByIDs(ctx context.Context, ids []int64) ([]models.Participant, error) {
	return r.getByIDs(ctx, ids)
}

func (r *jsonParticipantRepository) Create(ctx context.Context, p *models.Participant) (*models.Participant, error) {
	return r.create(ctx, p)
}

func (r *jsonParticipantRepository) Update(ctx context.Context, p *models.Participant) (*models.Participant, error) {
	return r.update(ctx, p)
}

func (r *jsonParticipantRepository) Delete(ctx context.Context, id int64) error {
	return r.delete(ctx, id)
}

// ==================== Driver Repository ====================

type jsonDriverRepository struct {
	*genericRepository[models.Driver]
}

func (r *jsonDriverRepository) List(ctx context.Context, search string) ([]models.Driver, error) {
	return r.list(ctx, search)
}

func (r *jsonDriverRepository) GetByID(ctx context.Context, id int64) (*models.Driver, error) {
	return r.getByID(ctx, id)
}

func (r *jsonDriverRepository) GetByIDs(ctx context.Context, ids []int64) ([]models.Driver, error) {
	return r.getByIDs(ctx, ids)
}

func (r *jsonDriverRepository) Create(ctx context.Context, d *models.Driver) (*models.Driver, error) {
	return r.create(ctx, d)
}

func (r *jsonDriverRepository) Update(ctx context.Context, d *models.Driver) (*models.Driver, error) {
	return r.update(ctx, d)
}

func (r *jsonDriverRepository) Delete(ctx context.Context, id int64) error {
	return r.delete(ctx, id)
}

// ==================== Settings Repository ====================

type jsonSettingsRepository struct {
	store *JSONStore
}

func (r *jsonSettingsRepository) Get(ctx context.Context) (*models.Settings, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()

	return &models.Settings{
		InstituteAddress:           r.store.data.Settings.InstituteAddress,
		InstituteLat:               r.store.data.Settings.InstituteLat,
		InstituteLng:               r.store.data.Settings.InstituteLng,
		SelectedActivityLocationID: r.store.data.Settings.SelectedActivityLocationID,
		UseMiles:                   r.store.data.Settings.UseMiles,
	}, nil
}

func (r *jsonSettingsRepository) Update(ctx context.Context, s *models.Settings) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	r.store.data.Settings = JSONSettings{
		InstituteAddress:           s.InstituteAddress,
		InstituteLat:               s.InstituteLat,
		InstituteLng:               s.InstituteLng,
		SelectedActivityLocationID: s.SelectedActivityLocationID,
		UseMiles:                   s.UseMiles,
	}

	if err := r.store.saveUnlocked(); err != nil {
		return err
	}

	log.Printf("[JSON] Updated settings: selected_location_id=%d", s.SelectedActivityLocationID)
	return nil
}

// ==================== Event Repository ====================

type jsonEventRepository struct {
	store *JSONStore
}

func (r *jsonEventRepository) List(ctx context.Context, limit, offset int) ([]models.Event, int, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()

	total := len(r.store.data.Events)

	// Sort by date descending
	sorted := make([]JSONEvent, len(r.store.data.Events))
	copy(sorted, r.store.data.Events)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].EventDate.After(sorted[j].EventDate)
	})

	// Apply pagination
	start := offset
	if start > len(sorted) {
		start = len(sorted)
	}
	end := start + limit
	if end > len(sorted) {
		end = len(sorted)
	}

	var result []models.Event
	for _, e := range sorted[start:end] {
		result = append(result, e.Event)
	}

	return result, total, nil
}

func (r *jsonEventRepository) GetByID(ctx context.Context, id int64) (*models.Event, []models.EventAssignment, *models.EventSummary, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()

	for _, e := range r.store.data.Events {
		if e.ID == id {
			return &e.Event, e.Assignments, &e.Summary, nil
		}
	}
	return nil, nil, nil, nil // Not found is not an error for GetByID
}

func (r *jsonEventRepository) Create(ctx context.Context, event *models.Event, assignments []models.EventAssignment, summary *models.EventSummary) (*models.Event, error) {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	event.ID = r.store.data.NextIDs.Event
	r.store.data.NextIDs.Event++
	event.CreatedAt = time.Now()

	// Update assignment event IDs
	for i := range assignments {
		assignments[i].EventID = event.ID
	}
	summary.EventID = event.ID

	jsonEvent := JSONEvent{
		Event:       *event,
		Assignments: assignments,
		Summary:     *summary,
	}

	r.store.data.Events = append(r.store.data.Events, jsonEvent)

	if err := r.store.saveUnlocked(); err != nil {
		return nil, err
	}

	log.Printf("[JSON] Created event: id=%d date=%s", event.ID, event.EventDate.Format("2006-01-02"))
	return event, nil
}

func (r *jsonEventRepository) Delete(ctx context.Context, id int64) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	for i, e := range r.store.data.Events {
		if e.ID == id {
			r.store.data.Events = append(r.store.data.Events[:i], r.store.data.Events[i+1:]...)

			if err := r.store.saveUnlocked(); err != nil {
				return err
			}

			log.Printf("[JSON] Deleted event: id=%d", id)
			return nil
		}
	}

	return ErrNotFound
}

// ==================== Activity Location Repository ====================

type jsonActivityLocationRepository struct {
	*genericRepository[models.ActivityLocation]
}

func (r *jsonActivityLocationRepository) List(ctx context.Context) ([]models.ActivityLocation, error) {
	return r.list(ctx, "")
}

func (r *jsonActivityLocationRepository) GetByID(ctx context.Context, id int64) (*models.ActivityLocation, error) {
	return r.getByID(ctx, id)
}

func (r *jsonActivityLocationRepository) Create(ctx context.Context, loc *models.ActivityLocation) (*models.ActivityLocation, error) {
	return r.create(ctx, loc)
}

func (r *jsonActivityLocationRepository) Delete(ctx context.Context, id int64) error {
	return r.delete(ctx, id)
}

// ==================== Organization Vehicle Repository ====================

type jsonOrganizationVehicleRepository struct {
	*genericRepository[models.OrganizationVehicle]
}

func (r *jsonOrganizationVehicleRepository) List(ctx context.Context) ([]models.OrganizationVehicle, error) {
	return r.list(ctx, "")
}

func (r *jsonOrganizationVehicleRepository) GetByID(ctx context.Context, id int64) (*models.OrganizationVehicle, error) {
	return r.getByID(ctx, id)
}

func (r *jsonOrganizationVehicleRepository) GetByIDs(ctx context.Context, ids []int64) ([]models.OrganizationVehicle, error) {
	return r.getByIDs(ctx, ids)
}

func (r *jsonOrganizationVehicleRepository) Create(ctx context.Context, v *models.OrganizationVehicle) (*models.OrganizationVehicle, error) {
	return r.create(ctx, v)
}

func (r *jsonOrganizationVehicleRepository) Update(ctx context.Context, v *models.OrganizationVehicle) (*models.OrganizationVehicle, error) {
	return r.update(ctx, v)
}

func (r *jsonOrganizationVehicleRepository) Delete(ctx context.Context, id int64) error {
	return r.delete(ctx, id)
}
