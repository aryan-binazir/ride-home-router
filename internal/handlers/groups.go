package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"

	"ride-home-router/internal/models"
)

// GroupListResponse represents the list response.
type GroupListResponse struct {
	Groups []models.Group `json:"groups"`
	Total  int            `json:"total"`
}

// HandleGroupsPage handles GET /groups.
func (h *Handler) HandleGroupsPage(w http.ResponseWriter, r *http.Request) {
	groups, err := h.DB.Groups().List(r.Context())
	if err != nil {
		h.renderError(w, r, err)
		return
	}

	h.renderTemplate(w, "groups.html", map[string]any{
		"Title":      "Groups",
		"ActivePage": "groups",
		"Groups":     groups,
	})
}

// HandleListGroups handles GET /api/v1/groups.
func (h *Handler) HandleListGroups(w http.ResponseWriter, r *http.Request) {
	groups, err := h.DB.Groups().List(r.Context())
	if err != nil {
		if h.isHTMX(r) {
			h.renderError(w, r, err)
			return
		}
		h.handleInternalError(w, err)
		return
	}

	if h.isHTMX(r) {
		h.renderTemplate(w, "group_list", map[string]any{
			"Groups": groups,
		})
		return
	}

	h.writeJSON(w, http.StatusOK, GroupListResponse{
		Groups: groups,
		Total:  len(groups),
	})
}

// HandleGetGroup handles GET /api/v1/groups/{id}.
func (h *Handler) HandleGetGroup(w http.ResponseWriter, r *http.Request) {
	id, ok := parseResourceID(w, r, "/api/v1/groups/")
	if !ok {
		return
	}

	group, err := h.DB.Groups().GetByID(r.Context(), id)
	if err != nil {
		h.handleInternalError(w, err)
		return
	}
	if group == nil {
		h.handleNotFound(w, "Group not found")
		return
	}

	h.writeJSON(w, http.StatusOK, group)
}

// HandleCreateGroup handles POST /api/v1/groups.
func (h *Handler) HandleCreateGroup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}

	if h.isHTMX(r) {
		if err := r.ParseForm(); err != nil {
			h.renderError(w, r, err)
			return
		}
		req.Name = strings.TrimSpace(r.FormValue("name"))
	} else {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			h.handleValidationError(w, "Invalid request body")
			return
		}
		req.Name = strings.TrimSpace(req.Name)
	}

	if req.Name == "" {
		h.handleGroupValidationError(w, r, "Group name is required")
		return
	}

	group, err := h.DB.Groups().Create(r.Context(), &models.Group{Name: req.Name})
	if err != nil {
		h.handleGroupWriteError(w, r, err)
		return
	}

	if h.isHTMX(r) {
		h.renderGroupsListWithToast(w, r, fmt.Sprintf("Group '%s' added!", group.Name))
		return
	}

	h.writeJSON(w, http.StatusCreated, group)
}

// HandleUpdateGroup handles PUT /api/v1/groups/{id}.
func (h *Handler) HandleUpdateGroup(w http.ResponseWriter, r *http.Request) {
	id, ok := parseResourceID(w, r, "/api/v1/groups/")
	if !ok {
		return
	}

	existing, err := h.DB.Groups().GetByID(r.Context(), id)
	if err != nil {
		if h.isHTMX(r) {
			h.renderError(w, r, err)
			return
		}
		h.handleInternalError(w, err)
		return
	}
	if existing == nil {
		h.handleGroupNotFound(w, r)
		return
	}

	var req struct {
		Name string `json:"name"`
	}
	if h.isHTMX(r) {
		if err := r.ParseForm(); err != nil {
			h.renderError(w, r, err)
			return
		}
		req.Name = strings.TrimSpace(r.FormValue("name"))
	} else {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			h.handleValidationError(w, "Invalid request body")
			return
		}
		req.Name = strings.TrimSpace(req.Name)
	}

	if req.Name == "" {
		h.handleGroupValidationError(w, r, "Group name is required")
		return
	}

	group := &models.Group{
		ID:        id,
		Name:      req.Name,
		CreatedAt: existing.CreatedAt,
	}

	if _, err := h.DB.Groups().Update(r.Context(), group); err != nil {
		h.handleGroupWriteError(w, r, err)
		return
	}

	if h.isHTMX(r) {
		h.renderGroupsListWithToast(w, r, fmt.Sprintf("Group '%s' updated!", req.Name))
		return
	}

	h.writeJSON(w, http.StatusOK, group)
}

// HandleDeleteGroup handles DELETE /api/v1/groups/{id}.
func (h *Handler) HandleDeleteGroup(w http.ResponseWriter, r *http.Request) {
	id, ok := parseResourceID(w, r, "/api/v1/groups/")
	if !ok {
		return
	}

	if err := h.DB.Groups().Delete(r.Context(), id); err != nil {
		if h.checkNotFound(err) {
			h.handleGroupNotFound(w, r)
			return
		}
		if h.isHTMX(r) {
			h.renderError(w, r, err)
			return
		}
		h.handleInternalError(w, err)
		return
	}

	if h.isHTMX(r) {
		h.renderGroupsListWithToast(w, r, "Group deleted")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// HandleGroupForm handles GET /api/v1/groups/new and GET /api/v1/groups/{id}/edit.
func (h *Handler) HandleGroupForm(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimPrefix(r.URL.Path, "/api/v1/groups/")
	idStr = strings.TrimSuffix(idStr, "/edit")

	var group *models.Group
	if idStr != "new" && idStr != "" {
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			h.renderError(w, r, fmt.Errorf("Invalid group ID"))
			return
		}

		group, err = h.DB.Groups().GetByID(r.Context(), id)
		if err != nil {
			h.renderError(w, r, err)
			return
		}
		if group == nil {
			h.renderError(w, r, fmt.Errorf("Group not found"))
			return
		}
	} else {
		group = &models.Group{}
	}

	h.renderTemplate(w, "group_form", map[string]any{
		"Group": group,
	})
}

func parseGroupIDs(r *http.Request) ([]int64, error) {
	values := r.Form["group_ids"]
	groupIDs := make([]int64, 0, len(values))
	for _, value := range values {
		id, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid group selection")
		}
		groupIDs = append(groupIDs, id)
	}
	return groupIDs, nil
}

func parseEntityIDs(r *http.Request, fieldName string) ([]int64, error) {
	values := r.Form[fieldName]
	ids := make([]int64, 0, len(values))
	for _, value := range values {
		id, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid selection")
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func parseSingleGroupID(r *http.Request) (int64, error) {
	groupID, err := strconv.ParseInt(strings.TrimSpace(r.FormValue("group_id")), 10, 64)
	if err != nil || groupID <= 0 {
		return 0, fmt.Errorf("group is required")
	}
	return groupID, nil
}

func buildSelectedGroupIDMap(groups []models.Group) map[int64]bool {
	selected := make(map[int64]bool, len(groups))
	for _, group := range groups {
		selected[group.ID] = true
	}
	return selected
}

func (h *Handler) loadGroupsForParticipant(r *http.Request, participantID int64) ([]models.Group, map[int64]bool, error) {
	groups, err := h.DB.Groups().List(r.Context())
	if err != nil {
		return nil, nil, err
	}

	selectedGroups, err := h.DB.Groups().ListGroupsForParticipant(r.Context(), participantID)
	if err != nil {
		return nil, nil, err
	}

	return groups, buildSelectedGroupIDMap(selectedGroups), nil
}

func (h *Handler) loadGroupsForDriver(r *http.Request, driverID int64) ([]models.Group, map[int64]bool, error) {
	groups, err := h.DB.Groups().List(r.Context())
	if err != nil {
		return nil, nil, err
	}

	selectedGroups, err := h.DB.Groups().ListGroupsForDriver(r.Context(), driverID)
	if err != nil {
		return nil, nil, err
	}

	return groups, buildSelectedGroupIDMap(selectedGroups), nil
}

func (h *Handler) renderGroupsListWithToast(w http.ResponseWriter, r *http.Request, message string) {
	groups, err := h.DB.Groups().List(r.Context())
	if err != nil {
		h.renderError(w, r, err)
		return
	}

	h.setHTMXEventToast(w, "groupsChanged", true, message, "success")
	h.renderTemplate(w, "group_list", map[string]any{
		"Groups": groups,
	})
}

func (h *Handler) HandleAddParticipantsToGroup(w http.ResponseWriter, r *http.Request) {
	h.handleBulkParticipantGroupMembership(w, r, "add")
}

func (h *Handler) HandleRemoveParticipantsFromGroup(w http.ResponseWriter, r *http.Request) {
	h.handleBulkParticipantGroupMembership(w, r, "remove")
}

func (h *Handler) HandleAddDriversToGroup(w http.ResponseWriter, r *http.Request) {
	h.handleBulkDriverGroupMembership(w, r, "add")
}

func (h *Handler) HandleRemoveDriversFromGroup(w http.ResponseWriter, r *http.Request) {
	h.handleBulkDriverGroupMembership(w, r, "remove")
}

func (h *Handler) handleBulkParticipantGroupMembership(w http.ResponseWriter, r *http.Request, action string) {
	if err := r.ParseForm(); err != nil {
		h.handleHTMXErrorNoSwap(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Invalid form data")
		return
	}

	groupID, err := parseSingleGroupID(r)
	if err != nil {
		h.handleHTMXErrorNoSwap(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Choose a group first")
		return
	}
	participantIDs, err := parseEntityIDs(r, "participant_ids")
	if err != nil {
		h.handleHTMXErrorNoSwap(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Invalid participant selection")
		return
	}
	if len(participantIDs) == 0 {
		h.handleHTMXErrorNoSwap(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Select at least one participant")
		return
	}

	group, err := h.DB.Groups().GetByID(r.Context(), groupID)
	if err != nil {
		h.handleInternalError(w, err)
		return
	}
	if group == nil {
		h.handleHTMXErrorNoSwap(w, r, http.StatusNotFound, "NOT_FOUND", "Group not found")
		return
	}

	switch action {
	case "add":
		err = h.DB.Groups().AddGroupToParticipants(r.Context(), groupID, participantIDs)
	case "remove":
		err = h.DB.Groups().RemoveGroupFromParticipants(r.Context(), groupID, participantIDs)
	default:
		err = fmt.Errorf("invalid action")
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

	verb := "added to"
	if action == "remove" {
		verb = "removed from"
	}
	h.setHTMXToast(w, fmt.Sprintf("%d participant%s %s '%s'.", len(participantIDs), pluralSuffix(len(participantIDs)), verb, group.Name), "success")
	data, err := h.participantListData(r, participants)
	if err != nil {
		h.handleInternalError(w, err)
		return
	}
	h.renderTemplate(w, "participant_list", data)
}

func (h *Handler) handleBulkDriverGroupMembership(w http.ResponseWriter, r *http.Request, action string) {
	if err := r.ParseForm(); err != nil {
		h.handleHTMXErrorNoSwap(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Invalid form data")
		return
	}

	groupID, err := parseSingleGroupID(r)
	if err != nil {
		h.handleHTMXErrorNoSwap(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Choose a group first")
		return
	}
	driverIDs, err := parseEntityIDs(r, "driver_ids")
	if err != nil {
		h.handleHTMXErrorNoSwap(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Invalid driver selection")
		return
	}
	if len(driverIDs) == 0 {
		h.handleHTMXErrorNoSwap(w, r, http.StatusBadRequest, "VALIDATION_ERROR", "Select at least one driver")
		return
	}

	group, err := h.DB.Groups().GetByID(r.Context(), groupID)
	if err != nil {
		h.handleInternalError(w, err)
		return
	}
	if group == nil {
		h.handleHTMXErrorNoSwap(w, r, http.StatusNotFound, "NOT_FOUND", "Group not found")
		return
	}

	switch action {
	case "add":
		err = h.DB.Groups().AddGroupToDrivers(r.Context(), groupID, driverIDs)
	case "remove":
		err = h.DB.Groups().RemoveGroupFromDrivers(r.Context(), groupID, driverIDs)
	default:
		err = fmt.Errorf("invalid action")
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

	verb := "added to"
	if action == "remove" {
		verb = "removed from"
	}
	h.setHTMXToast(w, fmt.Sprintf("%d driver%s %s '%s'.", len(driverIDs), pluralSuffix(len(driverIDs)), verb, group.Name), "success")
	data, err := h.driverListData(r, drivers)
	if err != nil {
		h.handleInternalError(w, err)
		return
	}
	h.renderTemplate(w, "driver_list", data)
}

func pluralSuffix(count int) string {
	if count == 1 {
		return ""
	}
	return "s"
}

func (h *Handler) handleGroupValidationError(w http.ResponseWriter, r *http.Request, message string) {
	if h.isHTMX(r) {
		h.renderError(w, r, errors.New(message))
		return
	}
	h.handleValidationError(w, message)
}

func (h *Handler) handleGroupNotFound(w http.ResponseWriter, r *http.Request) {
	if h.isHTMX(r) {
		h.renderError(w, r, fmt.Errorf("Group not found"))
		return
	}
	h.handleNotFound(w, "Group not found")
}

func (h *Handler) handleGroupWriteError(w http.ResponseWriter, r *http.Request, err error) {
	if h.checkNotFound(err) {
		h.handleGroupNotFound(w, r)
		return
	}

	if isUniqueConstraintError(err) {
		h.handleGroupValidationError(w, r, "A group with that name already exists")
		return
	}

	log.Printf("[ERROR] Failed to write group: err=%v", err)
	if h.isHTMX(r) {
		h.renderError(w, r, err)
		return
	}
	h.handleInternalError(w, err)
}

func parseResourceID(w http.ResponseWriter, r *http.Request, prefix string) (int64, bool) {
	idStr := strings.TrimPrefix(r.URL.Path, prefix)
	if strings.HasSuffix(idStr, "/edit") {
		idStr = strings.TrimSuffix(idStr, "/edit")
	}

	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		if r.Header.Get("HX-Request") == "true" {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `<div class="alert alert-error">Invalid ID</div>`)
			return 0, false
		}
		http.Error(w, `{"error":{"code":"VALIDATION_ERROR","message":"Invalid ID"}}`, http.StatusBadRequest)
		return 0, false
	}

	return id, true
}

func (h *Handler) participantListData(r *http.Request, participants []models.Participant) (map[string]any, error) {
	groupIDs, err := h.DB.Groups().ListGroupIDsForParticipants(r.Context())
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"Participants":        participants,
		"ParticipantGroupIDs": groupIDs,
	}, nil
}

func (h *Handler) driverListData(r *http.Request, drivers []models.Driver) (map[string]any, error) {
	groupIDs, err := h.DB.Groups().ListGroupIDsForDrivers(r.Context())
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"Drivers":        drivers,
		"DriverGroupIDs": groupIDs,
	}, nil
}

func isUniqueConstraintError(err error) bool {
	if err == nil {
		return false
	}
	message := err.Error()
	return strings.Contains(message, "UNIQUE constraint failed") || strings.Contains(message, "constraint failed: UNIQUE")
}
