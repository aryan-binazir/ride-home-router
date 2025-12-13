package database

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
)

const (
	AppDirName        = ".ride-home-router"
	DataFileName      = "data.json"
	CacheDirName      = "cache"
	DistanceCacheFile = "distances.json"
)

// GetAppDir returns ~/.ride-home-router, creating it if needed
func GetAppDir() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}

	appDir := filepath.Join(homeDir, AppDirName)
	if err := os.MkdirAll(appDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create app directory: %w", err)
	}

	return appDir, nil
}

// GetDataFilePath returns ~/.ride-home-router/data.json
func GetDataFilePath() (string, error) {
	appDir, err := GetAppDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(appDir, DataFileName), nil
}

// GetCacheDir returns ~/.ride-home-router/cache, creating it if needed
func GetCacheDir() (string, error) {
	appDir, err := GetAppDir()
	if err != nil {
		return "", err
	}

	cacheDir := filepath.Join(appDir, CacheDirName)
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create cache directory: %w", err)
	}

	return cacheDir, nil
}

// GetDistanceCachePath returns ~/.ride-home-router/cache/distances.json
func GetDistanceCachePath() (string, error) {
	cacheDir, err := GetCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cacheDir, DistanceCacheFile), nil
}

// MigrateOldData migrates data from old locations to new
func MigrateOldData() error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	// Migrate main data file
	oldDataPath := filepath.Join(homeDir, "institute_transport.json")
	newDataPath, err := GetDataFilePath()
	if err != nil {
		return err
	}

	if _, err := os.Stat(oldDataPath); err == nil {
		if _, err := os.Stat(newDataPath); os.IsNotExist(err) {
			log.Printf("Migrating data from %s to %s", oldDataPath, newDataPath)
			data, err := os.ReadFile(oldDataPath)
			if err != nil {
				return fmt.Errorf("failed to read old data file: %w", err)
			}
			if err := os.WriteFile(newDataPath, data, 0644); err != nil {
				return fmt.Errorf("failed to write new data file: %w", err)
			}
			log.Printf("Data migration complete")
		}
	}

	// Migrate cache file
	oldCachePath := filepath.Join(homeDir, "institute_cache", "distances.json")
	newCachePath, err := GetDistanceCachePath()
	if err != nil {
		return err
	}

	if _, err := os.Stat(oldCachePath); err == nil {
		if _, err := os.Stat(newCachePath); os.IsNotExist(err) {
			log.Printf("Migrating cache from %s to %s", oldCachePath, newCachePath)
			data, err := os.ReadFile(oldCachePath)
			if err != nil {
				return fmt.Errorf("failed to read old cache file: %w", err)
			}
			if err := os.WriteFile(newCachePath, data, 0644); err != nil {
				return fmt.Errorf("failed to write new cache file: %w", err)
			}
			log.Printf("Cache migration complete")
		}
	}

	return nil
}
