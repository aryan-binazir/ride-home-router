package database

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"ride-home-router/internal/models"
)

// JSONData represents the structure of the JSON file
type JSONData struct {
	Settings          JSONSettings              `json:"settings"`
	ActivityLocations []models.ActivityLocation `json:"activity_locations"`
	Participants      []models.Participant      `json:"participants"`
	Drivers           []models.Driver           `json:"drivers"`
	Events            []JSONEvent               `json:"events"`
	NextIDs           struct {
		Participant      int64 `json:"participant"`
		Driver           int64 `json:"driver"`
		Event            int64 `json:"event"`
		ActivityLocation int64 `json:"activity_location"`
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

	participantRepository      ParticipantRepository
	driverRepository           DriverRepository
	settingsRepository         SettingsRepository
	activityLocationRepository ActivityLocationRepository
	eventRepository            EventRepository
	distanceCacheRepository    DistanceCacheRepository
}

func (s *JSONStore) Participants() ParticipantRepository         { return s.participantRepository }
func (s *JSONStore) Drivers() DriverRepository                   { return s.driverRepository }
func (s *JSONStore) Settings() SettingsRepository                { return s.settingsRepository }
func (s *JSONStore) ActivityLocations() ActivityLocationRepository { return s.activityLocationRepository }
func (s *JSONStore) Events() EventRepository                     { return s.eventRepository }
func (s *JSONStore) DistanceCache() DistanceCacheRepository       { return s.distanceCacheRepository }

// NewJSONStore creates a new JSON-based data store
func NewJSONStore(distanceCache DistanceCacheRepository) (*JSONStore, error) {
	// Get home directory
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get home directory: %w", err)
	}

	filePath := filepath.Join(homeDir, "institute_transport.json")
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
	store.participantRepository = &jsonParticipantRepository{store: store}
	store.driverRepository = &jsonDriverRepository{store: store}
	store.settingsRepository = &jsonSettingsRepository{store: store}
	store.activityLocationRepository = &jsonActivityLocationRepository{store: store}
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
			ActivityLocations: []models.ActivityLocation{},
			Participants:      []models.Participant{},
			Drivers:           []models.Driver{},
			Events:            []JSONEvent{},
		}
		s.data.NextIDs.Participant = 1
		s.data.NextIDs.Driver = 1
		s.data.NextIDs.Event = 1
		s.data.NextIDs.ActivityLocation = 1
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
	if s.data.Events == nil {
		s.data.Events = []JSONEvent{}
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
	if err := os.WriteFile(tmpFile, data, 0644); err != nil {
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
	store *JSONStore
}

func (r *jsonParticipantRepository) List(ctx context.Context, search string) ([]models.Participant, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()

	var result []models.Participant
	for _, p := range r.store.data.Participants {
		if search == "" || strings.Contains(strings.ToLower(p.Name), strings.ToLower(search)) {
			result = append(result, p)
		}
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})

	return result, nil
}

func (r *jsonParticipantRepository) GetByID(ctx context.Context, id int64) (*models.Participant, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()

	for _, p := range r.store.data.Participants {
		if p.ID == id {
			return &p, nil
		}
	}
	return nil, nil
}

func (r *jsonParticipantRepository) GetByIDs(ctx context.Context, ids []int64) ([]models.Participant, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()

	idSet := make(map[int64]bool)
	for _, id := range ids {
		idSet[id] = true
	}

	var result []models.Participant
	for _, p := range r.store.data.Participants {
		if idSet[p.ID] {
			result = append(result, p)
		}
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})

	return result, nil
}

func (r *jsonParticipantRepository) Create(ctx context.Context, p *models.Participant) (*models.Participant, error) {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	p.ID = r.store.data.NextIDs.Participant
	r.store.data.NextIDs.Participant++
	now := time.Now()
	p.CreatedAt = now
	p.UpdatedAt = now

	r.store.data.Participants = append(r.store.data.Participants, *p)

	if err := r.store.saveUnlocked(); err != nil {
		return nil, err
	}

	log.Printf("[JSON] Created participant: id=%d name=%s", p.ID, p.Name)
	return p, nil
}

func (r *jsonParticipantRepository) Update(ctx context.Context, p *models.Participant) (*models.Participant, error) {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	for i, existing := range r.store.data.Participants {
		if existing.ID == p.ID {
			p.CreatedAt = existing.CreatedAt
			p.UpdatedAt = time.Now()
			r.store.data.Participants[i] = *p

			if err := r.store.saveUnlocked(); err != nil {
				return nil, err
			}

			log.Printf("[JSON] Updated participant: id=%d name=%s", p.ID, p.Name)
			return p, nil
		}
	}

	return nil, nil
}

func (r *jsonParticipantRepository) Delete(ctx context.Context, id int64) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	for i, p := range r.store.data.Participants {
		if p.ID == id {
			r.store.data.Participants = append(r.store.data.Participants[:i], r.store.data.Participants[i+1:]...)

			if err := r.store.saveUnlocked(); err != nil {
				return err
			}

			log.Printf("[JSON] Deleted participant: id=%d", id)
			return nil
		}
	}

	return fmt.Errorf("participant not found")
}

// ==================== Driver Repository ====================

type jsonDriverRepository struct {
	store *JSONStore
}

func (r *jsonDriverRepository) List(ctx context.Context, search string) ([]models.Driver, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()

	var result []models.Driver
	for _, d := range r.store.data.Drivers {
		if search == "" || strings.Contains(strings.ToLower(d.Name), strings.ToLower(search)) {
			result = append(result, d)
		}
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})

	return result, nil
}

func (r *jsonDriverRepository) GetByID(ctx context.Context, id int64) (*models.Driver, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()

	for _, d := range r.store.data.Drivers {
		if d.ID == id {
			return &d, nil
		}
	}
	return nil, nil
}

func (r *jsonDriverRepository) GetByIDs(ctx context.Context, ids []int64) ([]models.Driver, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()

	idSet := make(map[int64]bool)
	for _, id := range ids {
		idSet[id] = true
	}

	var result []models.Driver
	for _, d := range r.store.data.Drivers {
		if idSet[d.ID] {
			result = append(result, d)
		}
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})

	return result, nil
}

func (r *jsonDriverRepository) GetInstituteVehicle(ctx context.Context) (*models.Driver, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()

	for _, d := range r.store.data.Drivers {
		if d.IsInstituteVehicle {
			return &d, nil
		}
	}
	return nil, nil
}

func (r *jsonDriverRepository) Create(ctx context.Context, d *models.Driver) (*models.Driver, error) {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	d.ID = r.store.data.NextIDs.Driver
	r.store.data.NextIDs.Driver++
	now := time.Now()
	d.CreatedAt = now
	d.UpdatedAt = now

	r.store.data.Drivers = append(r.store.data.Drivers, *d)

	if err := r.store.saveUnlocked(); err != nil {
		return nil, err
	}

	log.Printf("[JSON] Created driver: id=%d name=%s capacity=%d", d.ID, d.Name, d.VehicleCapacity)
	return d, nil
}

func (r *jsonDriverRepository) Update(ctx context.Context, d *models.Driver) (*models.Driver, error) {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	for i, existing := range r.store.data.Drivers {
		if existing.ID == d.ID {
			d.CreatedAt = existing.CreatedAt
			d.UpdatedAt = time.Now()
			r.store.data.Drivers[i] = *d

			if err := r.store.saveUnlocked(); err != nil {
				return nil, err
			}

			log.Printf("[JSON] Updated driver: id=%d name=%s", d.ID, d.Name)
			return d, nil
		}
	}

	return nil, nil
}

func (r *jsonDriverRepository) Delete(ctx context.Context, id int64) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	for i, d := range r.store.data.Drivers {
		if d.ID == id {
			r.store.data.Drivers = append(r.store.data.Drivers[:i], r.store.data.Drivers[i+1:]...)

			if err := r.store.saveUnlocked(); err != nil {
				return err
			}

			log.Printf("[JSON] Deleted driver: id=%d", id)
			return nil
		}
	}

	return fmt.Errorf("driver not found")
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
	return nil, nil, nil, nil
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

	return fmt.Errorf("event not found")
}

// ==================== Activity Location Repository ====================

type jsonActivityLocationRepository struct {
	store *JSONStore
}

func (r *jsonActivityLocationRepository) List(ctx context.Context) ([]models.ActivityLocation, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()

	result := make([]models.ActivityLocation, len(r.store.data.ActivityLocations))
	copy(result, r.store.data.ActivityLocations)

	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})

	return result, nil
}

func (r *jsonActivityLocationRepository) GetByID(ctx context.Context, id int64) (*models.ActivityLocation, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()

	for _, loc := range r.store.data.ActivityLocations {
		if loc.ID == id {
			return &loc, nil
		}
	}
	return nil, nil
}

func (r *jsonActivityLocationRepository) Create(ctx context.Context, loc *models.ActivityLocation) (*models.ActivityLocation, error) {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	loc.ID = r.store.data.NextIDs.ActivityLocation
	r.store.data.NextIDs.ActivityLocation++

	r.store.data.ActivityLocations = append(r.store.data.ActivityLocations, *loc)

	if err := r.store.saveUnlocked(); err != nil {
		return nil, err
	}

	log.Printf("[JSON] Created activity location: id=%d name=%s", loc.ID, loc.Name)
	return loc, nil
}

func (r *jsonActivityLocationRepository) Delete(ctx context.Context, id int64) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	for i, loc := range r.store.data.ActivityLocations {
		if loc.ID == id {
			r.store.data.ActivityLocations = append(r.store.data.ActivityLocations[:i], r.store.data.ActivityLocations[i+1:]...)

			if err := r.store.saveUnlocked(); err != nil {
				return err
			}

			log.Printf("[JSON] Deleted activity location: id=%d", id)
			return nil
		}
	}

	return fmt.Errorf("activity location not found")
}
