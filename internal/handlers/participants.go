package handlers

import (
	"encoding/json"
	"fmt"
	"html"
	"log"
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
	log.Printf("[HTTP] GET /api/v1/participants: search=%s", search)

	participants, err := h.DB.Participants().List(r.Context(), search)
	if err != nil {
		log.Printf("[ERROR] Failed to list participants: search=%s err=%v", search, err)
		if h.isHTMX(r) {
			h.renderError(w, r, err)
			return
		}
		h.handleInternalError(w, err)
		return
	}

	log.Printf("[HTTP] Listed participants: count=%d", len(participants))
	if h.isHTMX(r) {
		h.renderTemplate(w, "participant_list", map[string]interface{}{
			"Participants": participants,
		})
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
		log.Printf("[HTTP] GET /api/v1/participants/{id}: invalid_id=%s err=%v", idStr, err)
		h.handleValidationError(w, "Invalid participant ID")
		return
	}

	log.Printf("[HTTP] GET /api/v1/participants/{id}: id=%d", id)
	participant, err := h.DB.Participants().GetByID(r.Context(), id)
	if err != nil {
		log.Printf("[ERROR] Failed to get participant: id=%d err=%v", id, err)
		h.handleInternalError(w, err)
		return
	}

	if participant == nil {
		log.Printf("[HTTP] Participant not found: id=%d", id)
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

	if h.isHTMX(r) {
		if err := r.ParseForm(); err != nil {
			log.Printf("[ERROR] Failed to parse form: err=%v", err)
			h.renderError(w, r, err)
			return
		}
		req.Name = r.FormValue("name")
		req.Address = r.FormValue("address")
	} else {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			log.Printf("[HTTP] POST /api/v1/participants: invalid_body err=%v", err)
			h.handleValidationError(w, "Invalid request body")
			return
		}
	}

	if req.Name == "" || req.Address == "" {
		log.Printf("[HTTP] POST /api/v1/participants: missing_fields name=%s address=%s", req.Name, req.Address)
		if h.isHTMX(r) {
			h.renderError(w, r, fmt.Errorf("Name and address are required"))
			return
		}
		h.handleValidationError(w, "Name and address are required")
		return
	}

	log.Printf("[HTTP] POST /api/v1/participants: name=%s address=%s", req.Name, req.Address)
	geocodeResult, err := h.Geocoder.GeocodeWithRetry(r.Context(), req.Address, 3)
	if err != nil {
		log.Printf("[ERROR] Failed to geocode participant address: address=%s err=%v", req.Address, err)
		if h.isHTMX(r) {
			h.renderError(w, r, err)
			return
		}
		h.handleGeocodingError(w, err)
		return
	}

	participant := &models.Participant{
		Name:    req.Name,
		Address: req.Address,
		Lat:     geocodeResult.Coords.Lat,
		Lng:     geocodeResult.Coords.Lng,
	}

	participant, err = h.DB.Participants().Create(r.Context(), participant)
	if err != nil {
		log.Printf("[ERROR] Failed to create participant: name=%s address=%s err=%v", req.Name, req.Address, err)
		if h.isHTMX(r) {
			h.renderError(w, r, err)
			return
		}
		h.handleInternalError(w, err)
		return
	}

	log.Printf("[HTTP] Created participant: id=%d name=%s", participant.ID, participant.Name)
	if h.isHTMX(r) {
		participants, err := h.DB.Participants().List(r.Context(), "")
		if err != nil {
			log.Printf("[ERROR] Failed to list participants after create: err=%v", err)
			h.renderError(w, r, err)
			return
		}
		w.Header().Set("HX-Trigger", fmt.Sprintf(`{"participantCreated": true, "showToast": {"message": "Participant '%s' added!", "type": "success"}}`, html.EscapeString(participant.Name)))
		h.renderTemplate(w, "participant_list", map[string]interface{}{
			"Participants": participants,
		})
		return
	}

	h.writeJSON(w, http.StatusCreated, participant)
}

// HandleUpdateParticipant handles PUT /api/v1/participants/{id}
func (h *Handler) HandleUpdateParticipant(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimPrefix(r.URL.Path, "/api/v1/participants/")
	if strings.HasSuffix(idStr, "/edit") {
		idStr = strings.TrimSuffix(idStr, "/edit")
	}
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		log.Printf("[HTTP] PUT /api/v1/participants/{id}: invalid_id=%s err=%v", idStr, err)
		if h.isHTMX(r) {
			h.renderError(w, r, fmt.Errorf("Invalid participant ID"))
			return
		}
		h.handleValidationError(w, "Invalid participant ID")
		return
	}

	log.Printf("[HTTP] PUT /api/v1/participants/{id}: id=%d", id)

	existing, err := h.DB.Participants().GetByID(r.Context(), id)
	if err != nil {
		log.Printf("[ERROR] Failed to get participant for update: id=%d err=%v", id, err)
		if h.isHTMX(r) {
			h.renderError(w, r, err)
			return
		}
		h.handleInternalError(w, err)
		return
	}
	if existing == nil {
		log.Printf("[HTTP] Participant not found for update: id=%d", id)
		if h.isHTMX(r) {
			h.renderError(w, r, fmt.Errorf("Participant not found"))
			return
		}
		h.handleNotFound(w, "Participant not found")
		return
	}

	var req struct {
		Name    string `json:"name"`
		Address string `json:"address"`
	}

	if h.isHTMX(r) {
		if err := r.ParseForm(); err != nil {
			h.renderError(w, r, err)
			return
		}
		req.Name = r.FormValue("name")
		req.Address = r.FormValue("address")
	} else {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			h.handleValidationError(w, "Invalid request body")
			return
		}
	}

	if req.Name == "" || req.Address == "" {
		if h.isHTMX(r) {
			h.renderError(w, r, fmt.Errorf("Name and address are required"))
			return
		}
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
			if h.isHTMX(r) {
				h.renderError(w, r, err)
				return
			}
			h.handleGeocodingError(w, err)
			return
		}
		participant.Lat = geocodeResult.Coords.Lat
		participant.Lng = geocodeResult.Coords.Lng
	}

	participant, err = h.DB.Participants().Update(r.Context(), participant)
	if err != nil {
		log.Printf("[ERROR] Failed to update participant: id=%d err=%v", id, err)
		if h.isHTMX(r) {
			h.renderError(w, r, err)
			return
		}
		h.handleInternalError(w, err)
		return
	}

	if participant == nil {
		log.Printf("[HTTP] Participant not found after update: id=%d", id)
		if h.isHTMX(r) {
			h.renderError(w, r, fmt.Errorf("Participant not found"))
			return
		}
		h.handleNotFound(w, "Participant not found")
		return
	}

	log.Printf("[HTTP] Updated participant: id=%d name=%s", participant.ID, participant.Name)
	if h.isHTMX(r) {
		participants, err := h.DB.Participants().List(r.Context(), "")
		if err != nil {
			log.Printf("[ERROR] Failed to list participants after update: err=%v", err)
			h.renderError(w, r, err)
			return
		}
		w.Header().Set("HX-Trigger", fmt.Sprintf(`{"participantUpdated": true, "showToast": {"message": "Participant '%s' updated!", "type": "success"}}`, html.EscapeString(participant.Name)))
		h.renderTemplate(w, "participant_list", map[string]interface{}{
			"Participants": participants,
		})
		return
	}

	h.writeJSON(w, http.StatusOK, participant)
}

// HandleDeleteParticipant handles DELETE /api/v1/participants/{id}
func (h *Handler) HandleDeleteParticipant(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimPrefix(r.URL.Path, "/api/v1/participants/")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		log.Printf("[HTTP] DELETE /api/v1/participants/{id}: invalid_id=%s err=%v", idStr, err)
		if h.isHTMX(r) {
			h.renderError(w, r, fmt.Errorf("Invalid participant ID"))
			return
		}
		h.handleValidationError(w, "Invalid participant ID")
		return
	}

	log.Printf("[HTTP] DELETE /api/v1/participants/{id}: id=%d", id)
	err = h.DB.Participants().Delete(r.Context(), id)
	if h.checkNotFound(err) {
		log.Printf("[HTTP] Participant not found for delete: id=%d", id)
		if h.isHTMX(r) {
			h.renderError(w, r, fmt.Errorf("Participant not found"))
			return
		}
		h.handleNotFound(w, "Participant not found")
		return
	}
	if err != nil {
		log.Printf("[ERROR] Failed to delete participant: id=%d err=%v", id, err)
		if h.isHTMX(r) {
			h.renderError(w, r, err)
			return
		}
		h.handleInternalError(w, err)
		return
	}

	log.Printf("[HTTP] Deleted participant: id=%d", id)
	if h.isHTMX(r) {
		w.Header().Set("HX-Trigger", `{"showToast": {"message": "Participant deleted", "type": "success"}}`)
		w.WriteHeader(http.StatusOK)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// HandleParticipantForm handles GET /api/v1/participants/new and GET /api/v1/participants/{id}/edit
func (h *Handler) HandleParticipantForm(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimPrefix(r.URL.Path, "/api/v1/participants/")
	idStr = strings.TrimSuffix(idStr, "/edit")

	var participant *models.Participant
	if idStr != "new" && idStr != "" {
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			h.renderError(w, r, fmt.Errorf("Invalid participant ID"))
			return
		}

		participant, err = h.DB.Participants().GetByID(r.Context(), id)
		if err != nil {
			h.renderError(w, r, err)
			return
		}
		if participant == nil {
			h.renderError(w, r, fmt.Errorf("Participant not found"))
			return
		}
	} else {
		participant = &models.Participant{}
	}

	h.renderTemplate(w, "participant_form", map[string]interface{}{
		"Participant": participant,
	})
}
