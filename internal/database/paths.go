package database

import (
	"encoding/json"
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
	SQLiteDBFileName  = "data.db"
	ConfigFileName    = "config.json"
)

// GetAppDir returns ~/.ride-home-router, creating it if needed
func GetAppDir() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}

	appDir := filepath.Join(homeDir, AppDirName)
	if err := os.MkdirAll(appDir, 0700); err != nil {
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
	if err := os.MkdirAll(cacheDir, 0700); err != nil {
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
			if err := os.WriteFile(newDataPath, data, 0600); err != nil {
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
			if err := os.WriteFile(newCachePath, data, 0600); err != nil {
				return fmt.Errorf("failed to write new cache file: %w", err)
			}
			log.Printf("Cache migration complete")
		}
	}

	return nil
}

// GetDefaultDBPath returns the default SQLite database path: ~/.ride-home-router/data.db
func GetDefaultDBPath() (string, error) {
	appDir, err := GetAppDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(appDir, SQLiteDBFileName), nil
}

// GetConfigFilePath returns ~/.ride-home-router/config.json
func GetConfigFilePath() (string, error) {
	appDir, err := GetAppDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(appDir, ConfigFileName), nil
}

// AppConfig stores application configuration
type AppConfig struct {
	DatabasePath string `json:"database_path"`
}

// LoadConfig loads the application config, returning defaults if not found
func LoadConfig() (*AppConfig, error) {
	configPath, err := GetConfigFilePath()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(configPath)
	if os.IsNotExist(err) {
		// Return default config
		defaultDBPath, err := GetDefaultDBPath()
		if err != nil {
			return nil, err
		}
		return &AppConfig{DatabasePath: defaultDBPath}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var config AppConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	// If database path is empty, use default
	if config.DatabasePath == "" {
		config.DatabasePath, err = GetDefaultDBPath()
		if err != nil {
			return nil, err
		}
	}

	return &config, nil
}

// SaveConfig saves the application config
func SaveConfig(config *AppConfig) error {
	configPath, err := GetConfigFilePath()
	if err != nil {
		return err
	}

	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	// Atomic write
	tmpPath := configPath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0600); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	if err := os.Rename(tmpPath, configPath); err != nil {
		return fmt.Errorf("failed to rename config file: %w", err)
	}

	log.Printf("Config saved: database_path=%s", config.DatabasePath)
	return nil
}
