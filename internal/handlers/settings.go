package handlers

import (
	"encoding/json"
	"fmt"
	"html"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"ride-home-router/internal/database"
	"ride-home-router/internal/models"
)

// HandleGetSettings handles GET /api/v1/settings
func (h *Handler) HandleGetSettings(w http.ResponseWriter, r *http.Request) {
	log.Printf("[HTTP] GET /api/v1/settings")
	settings, err := h.DB.Settings().Get(r.Context())
	if err != nil {
		log.Printf("[ERROR] Failed to get settings: err=%v", err)
		h.handleInternalError(w, err)
		return
	}

	h.writeJSON(w, http.StatusOK, settings)
}

// HandleUpdateSettings handles PUT /api/v1/settings
func (h *Handler) HandleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SelectedActivityLocationID int64 `json:"selected_activity_location_id"`
		UseMiles                   bool  `json:"use_miles"`
	}

	if h.isHTMX(r) {
		if err := r.ParseForm(); err != nil {
			log.Printf("[ERROR] Failed to parse form: err=%v", err)
			w.Header().Set("HX-Trigger", fmt.Sprintf(`{"showToast": {"message": "%s", "type": "error"}}`, html.EscapeString(err.Error())))
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if idStr := r.FormValue("selected_activity_location_id"); idStr != "" {
			if id, err := strconv.ParseInt(idStr, 10, 64); err == nil {
				req.SelectedActivityLocationID = id
			}
		}
		req.UseMiles = r.FormValue("use_miles") == "on" || r.FormValue("use_miles") == "true"
	} else {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			log.Printf("[HTTP] PUT /api/v1/settings: invalid_body err=%v", err)
			h.handleValidationError(w, "Invalid request body")
			return
		}
	}

	if req.SelectedActivityLocationID == 0 {
		log.Printf("[HTTP] PUT /api/v1/settings: missing selected_activity_location_id")
		if h.isHTMX(r) {
			w.Header().Set("HX-Trigger", `{"showToast": {"message": "Please select an activity location", "type": "error"}}`)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		h.handleValidationError(w, "Activity location is required")
		return
	}

	// Verify the location exists
	location, err := h.DB.ActivityLocations().GetByID(r.Context(), req.SelectedActivityLocationID)
	if err != nil {
		log.Printf("[ERROR] Failed to get activity location: err=%v", err)
		if h.isHTMX(r) {
			w.Header().Set("HX-Trigger", fmt.Sprintf(`{"showToast": {"message": "%s", "type": "error"}}`, html.EscapeString(err.Error())))
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		h.handleInternalError(w, err)
		return
	}

	if location == nil {
		log.Printf("[HTTP] PUT /api/v1/settings: activity location not found: id=%d", req.SelectedActivityLocationID)
		if h.isHTMX(r) {
			w.Header().Set("HX-Trigger", `{"showToast": {"message": "Selected activity location not found", "type": "error"}}`)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		h.handleNotFound(w, "Activity location not found")
		return
	}

	settings := &models.Settings{
		SelectedActivityLocationID: req.SelectedActivityLocationID,
		UseMiles:                   req.UseMiles,
	}

	if err := h.DB.Settings().Update(r.Context(), settings); err != nil {
		log.Printf("[ERROR] Failed to update settings: err=%v", err)
		if h.isHTMX(r) {
			w.Header().Set("HX-Trigger", fmt.Sprintf(`{"showToast": {"message": "%s", "type": "error"}}`, html.EscapeString(err.Error())))
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		h.handleInternalError(w, err)
		return
	}

	log.Printf("[HTTP] Updated settings: selected_location_id=%d", settings.SelectedActivityLocationID)
	if h.isHTMX(r) {
		w.Header().Set("HX-Trigger", fmt.Sprintf(`{"showToast": {"message": "Settings saved! Using: %s", "type": "success"}}`, html.EscapeString(location.Name)))
		w.WriteHeader(http.StatusNoContent)
		return
	}

	h.writeJSON(w, http.StatusOK, settings)
}

// HandleGetDatabaseConfig handles GET /api/v1/config/database
func (h *Handler) HandleGetDatabaseConfig(w http.ResponseWriter, r *http.Request) {
	log.Printf("[HTTP] GET /api/v1/config/database")

	config, err := database.LoadConfig()
	if err != nil {
		log.Printf("[ERROR] Failed to load config: err=%v", err)
		h.handleInternalError(w, err)
		return
	}

	defaultPath, _ := database.GetDefaultDBPath()

	response := struct {
		DatabasePath string `json:"database_path"`
		DefaultPath  string `json:"default_path"`
		IsDefault    bool   `json:"is_default"`
	}{
		DatabasePath: config.DatabasePath,
		DefaultPath:  defaultPath,
		IsDefault:    config.DatabasePath == defaultPath,
	}

	h.writeJSON(w, http.StatusOK, response)
}

// HandleUpdateDatabaseConfig handles PUT /api/v1/config/database
func (h *Handler) HandleUpdateDatabaseConfig(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DatabasePath string `json:"database_path"`
	}

	if h.isHTMX(r) {
		if err := r.ParseForm(); err != nil {
			log.Printf("[ERROR] Failed to parse form: err=%v", err)
			w.Header().Set("HX-Trigger", fmt.Sprintf(`{"showToast": {"message": "%s", "type": "error"}}`, html.EscapeString(err.Error())))
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		req.DatabasePath = r.FormValue("database_path")
	} else {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			log.Printf("[HTTP] PUT /api/v1/config/database: invalid_body err=%v", err)
			h.handleValidationError(w, "Invalid request body")
			return
		}
	}

	// If empty, use default
	if req.DatabasePath == "" {
		defaultPath, err := database.GetDefaultDBPath()
		if err != nil {
			log.Printf("[ERROR] Failed to get default DB path: err=%v", err)
			h.handleInternalError(w, err)
			return
		}
		req.DatabasePath = defaultPath
	}

	// Expand home directory if needed
	if len(req.DatabasePath) > 0 && req.DatabasePath[0] == '~' {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			log.Printf("[ERROR] Failed to get home directory: err=%v", err)
			h.handleInternalError(w, err)
			return
		}
		req.DatabasePath = filepath.Join(homeDir, req.DatabasePath[1:])
	}

	// Validate the path is absolute
	if !filepath.IsAbs(req.DatabasePath) {
		if h.isHTMX(r) {
			w.Header().Set("HX-Trigger", `{"showToast": {"message": "Database path must be absolute", "type": "error"}}`)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		h.handleValidationError(w, "Database path must be absolute")
		return
	}

	// Ensure the directory exists
	dir := filepath.Dir(req.DatabasePath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		log.Printf("[ERROR] Failed to create database directory: err=%v", err)
		if h.isHTMX(r) {
			w.Header().Set("HX-Trigger", fmt.Sprintf(`{"showToast": {"message": "Failed to create directory: %s", "type": "error"}}`, html.EscapeString(err.Error())))
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		h.handleValidationError(w, fmt.Sprintf("Failed to create directory: %v", err))
		return
	}

	// Save config
	config := &database.AppConfig{
		DatabasePath: req.DatabasePath,
	}

	if err := database.SaveConfig(config); err != nil {
		log.Printf("[ERROR] Failed to save config: err=%v", err)
		if h.isHTMX(r) {
			w.Header().Set("HX-Trigger", fmt.Sprintf(`{"showToast": {"message": "%s", "type": "error"}}`, html.EscapeString(err.Error())))
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		h.handleInternalError(w, err)
		return
	}

	log.Printf("[HTTP] Updated database config: path=%s", req.DatabasePath)

	if h.isHTMX(r) {
		w.Header().Set("HX-Trigger", `{"showToast": {"message": "Database path updated. Restart the application to apply changes.", "type": "success"}}`)
		w.WriteHeader(http.StatusNoContent)
		return
	}

	h.writeJSON(w, http.StatusOK, map[string]interface{}{
		"database_path": req.DatabasePath,
		"message":       "Database path updated. Restart the application to apply changes.",
	})
}
