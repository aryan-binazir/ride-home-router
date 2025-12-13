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
	Settings      JSONSettings              `json:"settings"`
	Participants  []models.Participant      `json:"participants"`
	Drivers       []models.Driver           `json:"drivers"`
	Events        []JSONEvent               `json:"events"`
	DistanceCache []models.DistanceCacheEntry `json:"distance_cache"`
	NextIDs       struct {
		Participant int64 `json:"participant"`
		Driver      int64 `json:"driver"`
		Event       int64 `json:"event"`
	} `json:"next_ids"`
}

// JSONSettings stores settings in the JSON file
type JSONSettings struct {
	InstituteAddress string  `json:"institute_address"`
	InstituteLat     float64 `json:"institute_lat"`
	InstituteLng     float64 `json:"institute_lng"`
	UseMiles         bool    `json:"use_miles"`
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

	participantRepository   ParticipantRepository
	driverRepository        DriverRepository
	settingsRepository      SettingsRepository
	eventRepository         EventRepository
	distanceCacheRepository DistanceCacheRepository
}

func (s *JSONStore) Participants() ParticipantRepository   { return s.participantRepository }
func (s *JSONStore) Drivers() DriverRepository             { return s.driverRepository }
func (s *JSONStore) Settings() SettingsRepository          { return s.settingsRepository }
func (s *JSONStore) Events() EventRepository               { return s.eventRepository }
func (s *JSONStore) DistanceCache() DistanceCacheRepository { return s.distanceCacheRepository }

// NewJSONStore creates a new JSON-based data store
func NewJSONStore() (*JSONStore, error) {
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
	store.eventRepository = &jsonEventRepository{store: store}
	store.distanceCacheRepository = &jsonDistanceCacheRepository{store: store}

	return store, nil
}

func (s *JSONStore) load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.filePath)
	if os.IsNotExist(err) {
		// Initialize with empty data
		s.data = &JSONData{
			Participants:  []models.Participant{},
			Drivers:       []models.Driver{},
			Events:        []JSONEvent{},
			DistanceCache: []models.DistanceCacheEntry{},
		}
		s.data.NextIDs.Participant = 1
		s.data.NextIDs.Driver = 1
		s.data.NextIDs.Event = 1
		return s.saveUnlocked()
	}
	if err != nil {
		return fmt.Errorf("failed to read data file: %w", err)
	}

	if err := json.Unmarshal(data, s.data); err != nil {
		return fmt.Errorf("failed to parse data file: %w", err)
	}

	// Ensure slices are not nil
	if s.data.Participants == nil {
		s.data.Participants = []models.Participant{}
	}
	if s.data.Drivers == nil {
		s.data.Drivers = []models.Driver{}
	}
	if s.data.Events == nil {
		s.data.Events = []JSONEvent{}
	}
	if s.data.DistanceCache == nil {
		s.data.DistanceCache = []models.DistanceCacheEntry{}
	}

	log.Printf("Loaded data: %d participants, %d drivers, %d events, %d cached distances",
		len(s.data.Participants), len(s.data.Drivers), len(s.data.Events), len(s.data.DistanceCache))

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
		InstituteAddress: r.store.data.Settings.InstituteAddress,
		InstituteLat:     r.store.data.Settings.InstituteLat,
		InstituteLng:     r.store.data.Settings.InstituteLng,
		UseMiles:         r.store.data.Settings.UseMiles,
	}, nil
}

func (r *jsonSettingsRepository) Update(ctx context.Context, s *models.Settings) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	r.store.data.Settings = JSONSettings{
		InstituteAddress: s.InstituteAddress,
		InstituteLat:     s.InstituteLat,
		InstituteLng:     s.InstituteLng,
		UseMiles:         s.UseMiles,
	}

	if err := r.store.saveUnlocked(); err != nil {
		return err
	}

	log.Printf("[JSON] Updated settings: address=%s", s.InstituteAddress)
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

// ==================== Distance Cache Repository ====================

type jsonDistanceCacheRepository struct {
	store *JSONStore
}

func (r *jsonDistanceCacheRepository) Get(ctx context.Context, origin, dest models.Coordinates) (*models.DistanceCacheEntry, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()

	for _, e := range r.store.data.DistanceCache {
		if coordsMatch(e.Origin, origin) && coordsMatch(e.Destination, dest) {
			return &e, nil
		}
	}
	return nil, nil
}

func (r *jsonDistanceCacheRepository) GetBatch(ctx context.Context, pairs []struct{ Origin, Dest models.Coordinates }) (map[string]*models.DistanceCacheEntry, error) {
	result := make(map[string]*models.DistanceCacheEntry)

	for _, pair := range pairs {
		entry, err := r.Get(ctx, pair.Origin, pair.Dest)
		if err != nil {
			return nil, err
		}
		if entry != nil {
			key := makeCacheKey(pair.Origin, pair.Dest)
			result[key] = entry
		}
	}

	return result, nil
}

func (r *jsonDistanceCacheRepository) Set(ctx context.Context, entry *models.DistanceCacheEntry) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	// Check if exists, update if so
	for i, e := range r.store.data.DistanceCache {
		if coordsMatch(e.Origin, entry.Origin) && coordsMatch(e.Destination, entry.Destination) {
			r.store.data.DistanceCache[i] = *entry
			return r.store.saveUnlocked()
		}
	}

	// Add new entry
	r.store.data.DistanceCache = append(r.store.data.DistanceCache, *entry)
	return r.store.saveUnlocked()
}

func (r *jsonDistanceCacheRepository) SetBatch(ctx context.Context, entries []models.DistanceCacheEntry) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	for _, entry := range entries {
		found := false
		for i, e := range r.store.data.DistanceCache {
			if coordsMatch(e.Origin, entry.Origin) && coordsMatch(e.Destination, entry.Destination) {
				r.store.data.DistanceCache[i] = entry
				found = true
				break
			}
		}
		if !found {
			r.store.data.DistanceCache = append(r.store.data.DistanceCache, entry)
		}
	}

	return r.store.saveUnlocked()
}

func (r *jsonDistanceCacheRepository) Clear(ctx context.Context) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()

	r.store.data.DistanceCache = []models.DistanceCacheEntry{}
	return r.store.saveUnlocked()
}

// coordsMatch checks if two coordinates are equal (rounded to 5 decimal places)
func coordsMatch(a, b models.Coordinates) bool {
	return roundCoord(a.Lat) == roundCoord(b.Lat) && roundCoord(a.Lng) == roundCoord(b.Lng)
}
