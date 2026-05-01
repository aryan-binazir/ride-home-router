package database

import (
	"path/filepath"
	"testing"
)

func TestAppConfig_PreservesGoogleMapsAPIKey(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	config := &AppConfig{
		DatabasePath:     filepath.Join(home, "custom.db"),
		GoogleMapsAPIKey: "test-key",
	}
	if err := SaveConfig(config); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	loaded, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if loaded.DatabasePath != config.DatabasePath {
		t.Fatalf("DatabasePath = %q, want %q", loaded.DatabasePath, config.DatabasePath)
	}
	if loaded.GoogleMapsAPIKey != config.GoogleMapsAPIKey {
		t.Fatalf("GoogleMapsAPIKey = %q, want preserved key", loaded.GoogleMapsAPIKey)
	}
}

func TestAppConfig_DefaultKeepsGoogleMapsAPIKeyEmpty(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	loaded, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if loaded.GoogleMapsAPIKey != "" {
		t.Fatalf("GoogleMapsAPIKey = %q, want empty", loaded.GoogleMapsAPIKey)
	}
	if loaded.DatabasePath != filepath.Join(home, AppDirName, SQLiteDBFileName) {
		t.Fatalf("DatabasePath = %q, want default under temp HOME", loaded.DatabasePath)
	}
}
