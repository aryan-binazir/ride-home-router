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
	if err := os.WriteFile(tmpFile, data, 0644); err != nil {
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

	for _, e := range c.data.Entries {
		if coordsMatch(e.Origin, origin) && coordsMatch(e.Destination, dest) {
			return &e, nil
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

	for i, e := range c.data.Entries {
		if coordsMatch(e.Origin, entry.Origin) && coordsMatch(e.Destination, entry.Destination) {
			c.data.Entries[i] = *entry
			return c.saveUnlocked()
		}
	}

	c.data.Entries = append(c.data.Entries, *entry)
	return c.saveUnlocked()
}

func (c *FileDistanceCache) SetBatch(ctx context.Context, entries []models.DistanceCacheEntry) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, entry := range entries {
		found := false
		for i, e := range c.data.Entries {
			if coordsMatch(e.Origin, entry.Origin) && coordsMatch(e.Destination, entry.Destination) {
				c.data.Entries[i] = entry
				found = true
				break
			}
		}
		if !found {
			c.data.Entries = append(c.data.Entries, entry)
		}
	}

	return c.saveUnlocked()
}

func (c *FileDistanceCache) Clear(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.data.Entries = []models.DistanceCacheEntry{}
	return c.saveUnlocked()
}

// coordsMatch checks if two coordinates are equal (rounded to 5 decimal places)
func coordsMatch(a, b models.Coordinates) bool {
	return roundCoord(a.Lat) == roundCoord(b.Lat) && roundCoord(a.Lng) == roundCoord(b.Lng)
}
