package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"

	"ride-home-router/internal/models"
)

const (
	messageLabelNameRequired       = "Label name is required"
	messageLabelNotFound           = "Label not found"
	messageDuplicateLabelName      = "A label with that name already exists"
	messageChooseLabelFirst        = "Choose a label first"
	messageInvalidLabelSelection   = "Invalid label selection"
	messageSelectParticipantForTag = "Select at least one participant"
	messageSelectDriverForTag      = "Select at least one driver"
)

var (
	errInvalidParticipantSelection = errors.New("invalid participant selection")
	errInvalidDriverSelection      = errors.New("invalid driver selection")
)

type LabelListResponse struct {
	Labels []models.Label `json:"labels"`
	Total  int            `json:"total"`
}

func parseLabelID(path string) (int64, error) {
	idStr := strings.TrimPrefix(path, "/api/v1/labels/")
	idStr = strings.TrimSuffix(idStr, "/edit")
	idStr = strings.Trim(idStr, "/")
	if idStr == "" || strings.Contains(idStr, "/") {
		return 0, fmt.Errorf("invalid label path")
	}
	return strconv.ParseInt(idStr, 10, 64)
}

func (h *Handler) HandleListLabels(w http.ResponseWriter, r *http.Request) {
	labels, err := h.DB.Labels().List(r.Context())
	if err != nil {
		if h.isHTMX(r) {
			h.renderError(w, r, err)
			return
		}
		h.handleInternalError(w, err)
		return
	}

	if h.isHTMX(r) {
		h.renderTemplate(w, "label_list", LabelListView{Labels: labels})
		return
	}

	h.writeJSON(w, http.StatusOK, LabelListResponse{
		Labels: labels,
		Total:  len(labels),
	})
}

func (h *Handler) HandleGetLabel(w http.ResponseWriter, r *http.Request) {
	id, err := parseLabelID(r.URL.Path)
	if err != nil {
		h.handleValidationError(w, messageInvalidLabelSelection)
		return
	}

	label, err := h.DB.Labels().GetByID(r.Context(), id)
	if err != nil {
		h.handleInternalError(w, err)
		return
	}
	if label == nil {
		h.handleNotFound(w, messageLabelNotFound)
		return
	}

	h.writeJSON(w, http.StatusOK, label)
}

func (h *Handler) HandleCreateLabel(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}

	if h.isHTMX(r) {
		if err := r.ParseForm(); err != nil {
			h.handleHTMXErrorNoSwap(w, r, http.StatusBadRequest, "VALIDATION_ERROR", messageInvalidFormData)
			return
		}
		req.Name = strings.TrimSpace(r.FormValue("name"))
	} else {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			h.handleValidationError(w, messageInvalidRequestBody)
			return
		}
		req.Name = strings.TrimSpace(req.Name)
	}

	if req.Name == "" {
		h.handleLabelValidationError(w, r, messageLabelNameRequired)
		return
	}

	label, err := h.DB.Labels().Create(r.Context(), &models.Label{Name: req.Name})
	if err != nil {
		h.handleLabelWriteError(w, r, err)
		return
	}

	if h.isHTMX(r) {
		h.renderLabelsListWithToast(w, r, messageEntityAdded("Label", label.Name))
		return
	}

	h.writeJSON(w, http.StatusCreated, label)
}

func (h *Handler) HandleUpdateLabel(w http.ResponseWriter, r *http.Request) {
	id, err := parseLabelID(r.URL.Path)
	if err != nil {
		h.handleLabelValidationError(w, r, messageInvalidLabelSelection)
		return
	}

	existing, err := h.DB.Labels().GetByID(r.Context(), id)
	if err != nil {
		h.handleInternalError(w, err)
		return
	}
	if existing == nil {
		h.handleLabelNotFound(w, r)
		return
	}

	var req struct {
		Name string `json:"name"`
	}
	if h.isHTMX(r) {
		if err := r.ParseForm(); err != nil {
			h.handleHTMXErrorNoSwap(w, r, http.StatusBadRequest, "VALIDATION_ERROR", messageInvalidFormData)
			return
		}
		req.Name = strings.TrimSpace(r.FormValue("name"))
	} else {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			h.handleValidationError(w, messageInvalidRequestBody)
			return
		}
		req.Name = strings.TrimSpace(req.Name)
	}

	if req.Name == "" {
		h.handleLabelValidationError(w, r, messageLabelNameRequired)
		return
	}

	label := &models.Label{
		ID:        id,
		Name:      req.Name,
		CreatedAt: existing.CreatedAt,
	}
	if _, err := h.DB.Labels().Update(r.Context(), label); err != nil {
		h.handleLabelWriteError(w, r, err)
		return
	}

	if h.isHTMX(r) {
		h.renderLabelsListWithToast(w, r, messageEntityUpdated("Label", req.Name))
		return
	}

	updated, err := h.DB.Labels().GetByID(r.Context(), id)
	if err != nil {
		h.handleInternalError(w, err)
		return
	}
	if updated != nil {
		label = updated
	}
	h.writeJSON(w, http.StatusOK, label)
}

func (h *Handler) HandleDeleteLabel(w http.ResponseWriter, r *http.Request) {
	id, err := parseLabelID(r.URL.Path)
	if err != nil {
		h.handleLabelValidationError(w, r, messageInvalidLabelSelection)
		return
	}

	if err := h.DB.Labels().Delete(r.Context(), id); err != nil {
		if h.checkNotFound(err) {
			h.handleLabelNotFound(w, r)
			return
		}
		h.handleInternalError(w, err)
		return
	}

	if h.isHTMX(r) {
		h.renderLabelsListWithToast(w, r, messageEntityDeleted("Label"))
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) HandleLabelForm(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimPrefix(r.URL.Path, "/api/v1/labels/")
	idStr = strings.TrimSuffix(idStr, "/edit")

	var label *models.Label
	if idStr != "new" && idStr != "" {
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			h.renderError(w, r, errors.New(messageInvalidLabelSelection))
			return
		}
		label, err = h.DB.Labels().GetByID(r.Context(), id)
		if err != nil {
			h.renderError(w, r, err)
			return
		}
		if label == nil {
			h.renderError(w, r, errors.New(messageLabelNotFound))
			return
		}
	} else {
		label = &models.Label{}
	}

	h.renderTemplate(w, "label_form", LabelFormView{Label: label})
}

func (h *Handler) HandleAddParticipantsToLabel(w http.ResponseWriter, r *http.Request) {
	h.handleBulkParticipantLabelMembership(w, r, "add")
}

func (h *Handler) HandleRemoveParticipantsFromLabel(w http.ResponseWriter, r *http.Request) {
	h.handleBulkParticipantLabelMembership(w, r, "remove")
}

func (h *Handler) HandleAddDriversToLabel(w http.ResponseWriter, r *http.Request) {
	h.handleBulkDriverLabelMembership(w, r, "add")
}

func (h *Handler) HandleRemoveDriversFromLabel(w http.ResponseWriter, r *http.Request) {
	h.handleBulkDriverLabelMembership(w, r, "remove")
}

func (h *Handler) handleBulkParticipantLabelMembership(w http.ResponseWriter, r *http.Request, action string) {
	labelID, label, ok := h.parseBulkLabelAction(w, r)
	if !ok {
		return
	}
	participantIDs, err := parseInt64FormValues(r, "participant_ids")
	if err != nil {
		h.handleHTMXErrorNoSwap(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Invalid participant selection")
		return
	}
	if len(participantIDs) == 0 {
		h.handleHTMXErrorNoSwap(w, r, http.StatusBadRequest, "VALIDATION_ERROR", messageSelectParticipantForTag)
		return
	}
	if err := h.validateBulkParticipantIDs(r.Context(), participantIDs); err != nil {
		if errors.Is(err, errInvalidParticipantSelection) {
			h.handleHTMXErrorNoSwap(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Invalid participant selection")
			return
		}
		h.handleInternalError(w, err)
		return
	}

	switch action {
	case "add":
		err = h.DB.Labels().AddLabelToParticipants(r.Context(), labelID, participantIDs)
	case "remove":
		err = h.DB.Labels().RemoveLabelFromParticipants(r.Context(), labelID, participantIDs)
	default:
		err = fmt.Errorf("invalid bulk label action")
	}
	if err != nil {
		h.handleInternalError(w, err)
		return
	}

	participants, err := h.DB.Participants().List(r.Context(), "")
	if err != nil {
		h.handleInternalError(w, err)
		return
	}
	data, err := h.participantListView(r, participants)
	if err != nil {
		h.handleInternalError(w, err)
		return
	}

	verb := "added to"
	if action == "remove" {
		verb = "removed from"
	}
	h.setHTMXToast(w, fmt.Sprintf("%d participant%s %s '%s'.", len(participantIDs), pluralSuffix(len(participantIDs)), verb, label.Name), toastTypeSuccess)
	h.renderTemplate(w, "participant_list", data)
}

func (h *Handler) handleBulkDriverLabelMembership(w http.ResponseWriter, r *http.Request, action string) {
	labelID, label, ok := h.parseBulkLabelAction(w, r)
	if !ok {
		return
	}
	driverIDs, err := parseInt64FormValues(r, "driver_ids")
	if err != nil {
		h.handleHTMXErrorNoSwap(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Invalid driver selection")
		return
	}
	if len(driverIDs) == 0 {
		h.handleHTMXErrorNoSwap(w, r, http.StatusBadRequest, "VALIDATION_ERROR", messageSelectDriverForTag)
		return
	}
	if err := h.validateBulkDriverIDs(r.Context(), driverIDs); err != nil {
		if errors.Is(err, errInvalidDriverSelection) {
			h.handleHTMXErrorNoSwap(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Invalid driver selection")
			return
		}
		h.handleInternalError(w, err)
		return
	}

	switch action {
	case "add":
		err = h.DB.Labels().AddLabelToDrivers(r.Context(), labelID, driverIDs)
	case "remove":
		err = h.DB.Labels().RemoveLabelFromDrivers(r.Context(), labelID, driverIDs)
	default:
		err = fmt.Errorf("invalid bulk label action")
	}
	if err != nil {
		h.handleInternalError(w, err)
		return
	}

	drivers, err := h.DB.Drivers().List(r.Context(), "")
	if err != nil {
		h.handleInternalError(w, err)
		return
	}
	data, err := h.driverListView(r, drivers)
	if err != nil {
		h.handleInternalError(w, err)
		return
	}

	verb := "added to"
	if action == "remove" {
		verb = "removed from"
	}
	h.setHTMXToast(w, fmt.Sprintf("%d driver%s %s '%s'.", len(driverIDs), pluralSuffix(len(driverIDs)), verb, label.Name), toastTypeSuccess)
	h.renderTemplate(w, "driver_list", data)
}

func (h *Handler) parseBulkLabelAction(w http.ResponseWriter, r *http.Request) (int64, *models.Label, bool) {
	if err := r.ParseForm(); err != nil {
		h.handleHTMXErrorNoSwap(w, r, http.StatusBadRequest, "VALIDATION_ERROR", messageInvalidFormData)
		return 0, nil, false
	}

	labelID, err := strconv.ParseInt(strings.TrimSpace(r.FormValue("label_id")), 10, 64)
	if err != nil || labelID <= 0 {
		h.handleHTMXErrorNoSwap(w, r, http.StatusBadRequest, "VALIDATION_ERROR", messageChooseLabelFirst)
		return 0, nil, false
	}

	label, err := h.DB.Labels().GetByID(r.Context(), labelID)
	if err != nil {
		h.handleInternalError(w, err)
		return 0, nil, false
	}
	if label == nil {
		h.handleHTMXErrorNoSwap(w, r, http.StatusNotFound, "NOT_FOUND", messageLabelNotFound)
		return 0, nil, false
	}

	return labelID, label, true
}

func (h *Handler) renderLabelsListWithToast(w http.ResponseWriter, r *http.Request, message string) {
	labels, err := h.DB.Labels().List(r.Context())
	if err != nil {
		h.renderError(w, r, err)
		return
	}

	h.setHTMXToast(w, message, toastTypeSuccess)
	h.renderTemplate(w, "label_list", LabelListView{Labels: labels})
}

func (h *Handler) handleLabelValidationError(w http.ResponseWriter, r *http.Request, message string) {
	if h.isHTMX(r) {
		h.handleHTMXErrorNoSwap(w, r, http.StatusBadRequest, "VALIDATION_ERROR", message)
		return
	}
	h.handleValidationError(w, message)
}

func (h *Handler) handleLabelNotFound(w http.ResponseWriter, r *http.Request) {
	if h.isHTMX(r) {
		h.handleHTMXErrorNoSwap(w, r, http.StatusNotFound, "NOT_FOUND", messageLabelNotFound)
		return
	}
	h.handleNotFound(w, messageLabelNotFound)
}

func (h *Handler) handleLabelWriteError(w http.ResponseWriter, r *http.Request, err error) {
	if h.checkNotFound(err) {
		h.handleLabelNotFound(w, r)
		return
	}
	if isUniqueConstraintError(err) {
		h.handleLabelValidationError(w, r, messageDuplicateLabelName)
		return
	}
	log.Printf("[ERROR] Failed to write label: err=%v", err)
	if h.isHTMX(r) {
		h.handleHTMXErrorNoSwap(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", messageGenericInternalError)
		return
	}
	h.handleInternalError(w, err)
}

func parseInt64FormValues(r *http.Request, fieldName string) ([]int64, error) {
	values := r.Form[fieldName]
	ids := make([]int64, 0, len(values))
	for _, value := range values {
		id, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func parseLabelIDs(r *http.Request) ([]int64, error) {
	return parseInt64FormValues(r, "label_ids")
}

func (h *Handler) validateLabelIDs(ctx context.Context, labelIDs []int64) error {
	uniqueIDs, ok := uniquePositiveIDs(labelIDs)
	if !ok {
		return errors.New(messageInvalidLabelSelection)
	}
	labels, err := h.DB.Labels().GetByIDs(ctx, uniqueIDs)
	if err != nil {
		return err
	}
	if len(labels) != len(uniqueIDs) {
		return errors.New(messageInvalidLabelSelection)
	}
	return nil
}

func (h *Handler) validateBulkParticipantIDs(ctx context.Context, participantIDs []int64) error {
	uniqueIDs, ok := uniquePositiveIDs(participantIDs)
	if !ok {
		return errInvalidParticipantSelection
	}
	participants, err := h.DB.Participants().GetByIDs(ctx, uniqueIDs)
	if err != nil {
		return err
	}
	if len(participants) != len(uniqueIDs) {
		return errInvalidParticipantSelection
	}
	return nil
}

func (h *Handler) validateBulkDriverIDs(ctx context.Context, driverIDs []int64) error {
	uniqueIDs, ok := uniquePositiveIDs(driverIDs)
	if !ok {
		return errInvalidDriverSelection
	}
	drivers, err := h.DB.Drivers().GetByIDs(ctx, uniqueIDs)
	if err != nil {
		return err
	}
	if len(drivers) != len(uniqueIDs) {
		return errInvalidDriverSelection
	}
	return nil
}

func uniquePositiveIDs(ids []int64) ([]int64, bool) {
	seen := make(map[int64]struct{}, len(ids))
	uniqueIDs := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			return nil, false
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		uniqueIDs = append(uniqueIDs, id)
	}
	return uniqueIDs, true
}

func buildSelectedLabelIDMap(labels []models.Label) map[int64]bool {
	selected := make(map[int64]bool, len(labels))
	for _, label := range labels {
		selected[label.ID] = true
	}
	return selected
}

func pluralSuffix(count int) string {
	if count == 1 {
		return ""
	}
	return "s"
}

func isUniqueConstraintError(err error) bool {
	if err == nil {
		return false
	}
	message := err.Error()
	return strings.Contains(message, "UNIQUE constraint failed") || strings.Contains(message, "constraint failed: UNIQUE")
}
