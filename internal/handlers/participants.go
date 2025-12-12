package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"ride-home-router/internal/models"
)

// ParticipantListResponse represents the list response
type ParticipantListResponse struct {
	Participants []models.Participant `json:"participants"`
	Total        int                  `json:"total"`
}

// HandleListParticipants handles GET /api/v1/participants
func (h *Handler) HandleListParticipants(w http.ResponseWriter, r *http.Request) {
	search := r.URL.Query().Get("search")

	participants, err := h.DB.ParticipantRepository.List(r.Context(), search)
	if err != nil {
		h.handleInternalError(w, err)
		return
	}

	h.writeJSON(w, http.StatusOK, ParticipantListResponse{
		Participants: participants,
		Total:        len(participants),
	})
}

// HandleGetParticipant handles GET /api/v1/participants/{id}
func (h *Handler) HandleGetParticipant(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimPrefix(r.URL.Path, "/api/v1/participants/")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		h.handleValidationError(w, "Invalid participant ID")
		return
	}

	participant, err := h.DB.ParticipantRepository.GetByID(r.Context(), id)
	if err != nil {
		h.handleInternalError(w, err)
		return
	}

	if participant == nil {
		h.handleNotFound(w, "Participant not found")
		return
	}

	h.writeJSON(w, http.StatusOK, participant)
}

// HandleCreateParticipant handles POST /api/v1/participants
func (h *Handler) HandleCreateParticipant(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name    string `json:"name"`
		Address string `json:"address"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.handleValidationError(w, "Invalid request body")
		return
	}

	if req.Name == "" || req.Address == "" {
		h.handleValidationError(w, "Name and address are required")
		return
	}

	geocodeResult, err := h.Geocoder.GeocodeWithRetry(r.Context(), req.Address, 3)
	if err != nil {
		h.handleGeocodingError(w, err)
		return
	}

	participant := &models.Participant{
		Name:    req.Name,
		Address: req.Address,
		Lat:     geocodeResult.Coords.Lat,
		Lng:     geocodeResult.Coords.Lng,
	}

	participant, err = h.DB.ParticipantRepository.Create(r.Context(), participant)
	if err != nil {
		h.handleInternalError(w, err)
		return
	}

	h.writeJSON(w, http.StatusCreated, participant)
}

// HandleUpdateParticipant handles PUT /api/v1/participants/{id}
func (h *Handler) HandleUpdateParticipant(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimPrefix(r.URL.Path, "/api/v1/participants/")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		h.handleValidationError(w, "Invalid participant ID")
		return
	}

	existing, err := h.DB.ParticipantRepository.GetByID(r.Context(), id)
	if err != nil {
		h.handleInternalError(w, err)
		return
	}
	if existing == nil {
		h.handleNotFound(w, "Participant not found")
		return
	}

	var req struct {
		Name    string `json:"name"`
		Address string `json:"address"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.handleValidationError(w, "Invalid request body")
		return
	}

	if req.Name == "" || req.Address == "" {
		h.handleValidationError(w, "Name and address are required")
		return
	}

	participant := &models.Participant{
		ID:        id,
		Name:      req.Name,
		Address:   req.Address,
		Lat:       existing.Lat,
		Lng:       existing.Lng,
		CreatedAt: existing.CreatedAt,
	}

	if req.Address != existing.Address {
		geocodeResult, err := h.Geocoder.GeocodeWithRetry(r.Context(), req.Address, 3)
		if err != nil {
			h.handleGeocodingError(w, err)
			return
		}
		participant.Lat = geocodeResult.Coords.Lat
		participant.Lng = geocodeResult.Coords.Lng
	}

	participant, err = h.DB.ParticipantRepository.Update(r.Context(), participant)
	if err != nil {
		h.handleInternalError(w, err)
		return
	}

	if participant == nil {
		h.handleNotFound(w, "Participant not found")
		return
	}

	h.writeJSON(w, http.StatusOK, participant)
}

// HandleDeleteParticipant handles DELETE /api/v1/participants/{id}
func (h *Handler) HandleDeleteParticipant(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimPrefix(r.URL.Path, "/api/v1/participants/")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		h.handleValidationError(w, "Invalid participant ID")
		return
	}

	err = h.DB.ParticipantRepository.Delete(r.Context(), id)
	if h.checkNotFound(err) {
		h.handleNotFound(w, "Participant not found")
		return
	}
	if err != nil {
		h.handleInternalError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
