package database

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
)

const (
	AppDirName       = ".ride-home-router"
	SQLiteDBFileName = "data.db"
	ConfigFileName   = "config.json"
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
