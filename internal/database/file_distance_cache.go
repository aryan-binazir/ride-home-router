package database

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sync"

	"ride-home-router/internal/models"
)

// FileDistanceCacheData represents the structure of the cache file
type FileDistanceCacheData struct {
	Entries []models.DistanceCacheEntry `json:"entries"`
}

// FileDistanceCache is a file-based implementation of DistanceCacheRepository
type FileDistanceCache struct {
	filePath string
	data     *FileDistanceCacheData
	index    map[string]int // O(1) lookup by coordinate pair (maps to index in Entries slice)
	mu       sync.RWMutex
}

// NewFileDistanceCache creates a new file-based distance cache
func NewFileDistanceCache() (*FileDistanceCache, error) {
	filePath, err := GetDistanceCachePath()
	if err != nil {
		return nil, fmt.Errorf("failed to get cache file path: %w", err)
	}
	log.Printf("Using distance cache file: %s", filePath)

	cache := &FileDistanceCache{
		filePath: filePath,
		data:     &FileDistanceCacheData{Entries: []models.DistanceCacheEntry{}},
		index:    make(map[string]int),
	}

	if err := cache.load(); err != nil {
		return nil, err
	}

	return cache, nil
}

func (c *FileDistanceCache) load() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	data, err := os.ReadFile(c.filePath)
	if os.IsNotExist(err) {
		c.data = &FileDistanceCacheData{Entries: []models.DistanceCacheEntry{}}
		return c.saveUnlocked()
	}
	if err != nil {
		return fmt.Errorf("failed to read cache file: %w", err)
	}

	if err := json.Unmarshal(data, c.data); err != nil {
		return fmt.Errorf("failed to parse cache file: %w", err)
	}

	if c.data.Entries == nil {
		c.data.Entries = []models.DistanceCacheEntry{}
	}

	c.rebuildIndex()

	log.Printf("Loaded distance cache: %d entries", len(c.data.Entries))
	return nil
}

func (c *FileDistanceCache) save() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.saveUnlocked()
}

func (c *FileDistanceCache) saveUnlocked() error {
	data, err := json.MarshalIndent(c.data, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal cache data: %w", err)
	}

	tmpFile := c.filePath + ".tmp"
	if err := os.WriteFile(tmpFile, data, 0600); err != nil {
		return fmt.Errorf("failed to write temp cache file: %w", err)
	}

	if err := os.Rename(tmpFile, c.filePath); err != nil {
		return fmt.Errorf("failed to rename temp cache file: %w", err)
	}

	return nil
}

func (c *FileDistanceCache) Get(ctx context.Context, origin, dest models.Coordinates) (*models.DistanceCacheEntry, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	// Use index for O(1) lookup if available
	if c.index != nil {
		key := makeCacheKey(origin, dest)
		if idx, ok := c.index[key]; ok {
			// Return a copy to prevent callers from modifying cache data without locks
			entryCopy := c.data.Entries[idx]
			return &entryCopy, nil
		}
		return nil, nil
	}

	// Fallback to linear scan if index not initialized
	for _, e := range c.data.Entries {
		if coordsMatch(e.Origin, origin) && coordsMatch(e.Destination, dest) {
			// Return a copy for consistency
			entryCopy := e
			return &entryCopy, nil
		}
	}
	return nil, nil
}

func (c *FileDistanceCache) GetBatch(ctx context.Context, pairs []struct{ Origin, Dest models.Coordinates }) (map[string]*models.DistanceCacheEntry, error) {
	result := make(map[string]*models.DistanceCacheEntry)

	for _, pair := range pairs {
		entry, err := c.Get(ctx, pair.Origin, pair.Dest)
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

func (c *FileDistanceCache) Set(ctx context.Context, entry *models.DistanceCacheEntry) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Initialize index if needed
	if c.index == nil {
		c.rebuildIndex()
	}

	key := makeCacheKey(entry.Origin, entry.Destination)

	// Check if entry exists using the index
	if idx, ok := c.index[key]; ok {
		// Update existing entry in place
		c.data.Entries[idx] = *entry
		return c.saveUnlocked()
	}

	// Add new entry
	c.data.Entries = append(c.data.Entries, *entry)
	c.index[key] = len(c.data.Entries) - 1
	return c.saveUnlocked()
}

func (c *FileDistanceCache) SetBatch(ctx context.Context, entries []models.DistanceCacheEntry) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Initialize index if needed
	if c.index == nil {
		c.rebuildIndex()
	}

	for _, entry := range entries {
		key := makeCacheKey(entry.Origin, entry.Destination)

		if idx, ok := c.index[key]; ok {
			// Update existing entry in place
			c.data.Entries[idx] = entry
		} else {
			// Add new entry
			c.data.Entries = append(c.data.Entries, entry)
			c.index[key] = len(c.data.Entries) - 1
		}
	}

	return c.saveUnlocked()
}

func (c *FileDistanceCache) Clear(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.data.Entries = []models.DistanceCacheEntry{}
	c.index = make(map[string]int)
	return c.saveUnlocked()
}

// coordsMatch checks if two coordinates are equal (rounded to 5 decimal places)
func coordsMatch(a, b models.Coordinates) bool {
	return models.RoundCoordinate(a.Lat) == models.RoundCoordinate(b.Lat) &&
		models.RoundCoordinate(a.Lng) == models.RoundCoordinate(b.Lng)
}

// makeCacheKey creates a unique key for a coordinate pair
func makeCacheKey(origin, dest models.Coordinates) string {
	return fmt.Sprintf("%.5f,%.5f->%.5f,%.5f",
		models.RoundCoordinate(origin.Lat), models.RoundCoordinate(origin.Lng),
		models.RoundCoordinate(dest.Lat), models.RoundCoordinate(dest.Lng))
}

// rebuildIndex creates the index map from the current entries slice.
// Must be called with the mutex already held.
func (c *FileDistanceCache) rebuildIndex() {
	c.index = make(map[string]int)
	for i := range c.data.Entries {
		key := makeCacheKey(c.data.Entries[i].Origin, c.data.Entries[i].Destination)
		c.index[key] = i
	}
}
