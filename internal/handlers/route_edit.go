package handlers

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"ride-home-router/internal/models"
	"ride-home-router/internal/routesession"
)

const maxParticipantMovesPerBatch = 64

type participantMove struct {
	ParticipantID    int64 `json:"participant_id"`
	FromRouteIndex   int   `json:"from_route_index"`
	ToRouteIndex     int   `json:"to_route_index"`
	InsertAtPosition int   `json:"insert_at_position"`
}

func buildRoutingPayload(routes []models.CalculatedRoute, summary models.RoutingSummary, mode models.RouteMode) models.RoutingResult {
	return models.RoutingResult{Routes: routes, Summary: summary, Mode: mode}
}

func buildRouteResultsView(snapshot routesession.Snapshot) RouteResultsView {
	return RouteResultsView{
		Routes: snapshot.Routes, OverCapacity: snapshot.OverCapacity, IsOutOfBalance: snapshot.IsOutOfBalance,
		Summary: snapshot.Summary, UseMiles: snapshot.UseMiles, ActivityLocation: snapshot.ActivityLocation,
		RouteTime: snapshot.RouteTime, SessionID: snapshot.ID, IsEditing: snapshot.IsEditing,
		UnusedDrivers: snapshot.UnusedDrivers, Mode: string(snapshot.Mode),
		RoutingPayload: buildRoutingPayload(snapshot.Routes, snapshot.Summary, snapshot.Mode),
	}
}

func (h *Handler) HandleMoveParticipant(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SessionID        string            `json:"session_id"`
		ParticipantID    int64             `json:"participant_id"`
		FromRouteIndex   int               `json:"from_route_index"`
		ToRouteIndex     int               `json:"to_route_index"`
		InsertAtPosition int               `json:"insert_at_position"`
		Moves            []participantMove `json:"moves"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.handleValidationErrorHTMX(w, r, messageInvalidRequestBody)
		return
	}
	legacy := req.Moves == nil
	moves := req.Moves
	if legacy {
		moves = []participantMove{{req.ParticipantID, req.FromRouteIndex, req.ToRouteIndex, req.InsertAtPosition}}
	} else if len(moves) == 0 {
		h.handleValidationErrorHTMX(w, r, messageMovesRequired)
		return
	}
	if len(moves) > maxParticipantMovesPerBatch {
		h.handleValidationErrorHTMX(w, r, messageTooManyMoves)
		return
	}
	storeMoves := make([]routesession.Move, len(moves))
	for i, move := range moves {
		if move.ParticipantID == 0 {
			h.handleValidationErrorHTMX(w, r, messageInvalidParticipantID)
			return
		}
		storeMoves[i] = routesession.Move{ParticipantID: move.ParticipantID, FromRouteIndex: move.FromRouteIndex, ToRouteIndex: move.ToRouteIndex, InsertAtPosition: move.InsertAtPosition}
	}
	snapshot, err := h.RouteSession.ApplyMoves(r.Context(), req.SessionID, storeMoves, routesession.ApplyMovesOptions{RequireClaimedSource: legacy})
	if err != nil {
		h.handleRouteSessionError(w, r, err)
		return
	}
	if len(moves) == 1 {
		log.Printf("[EDIT] Moved participant %d from route %d to route %d", moves[0].ParticipantID, moves[0].FromRouteIndex, moves[0].ToRouteIndex)
	} else {
		log.Printf("[EDIT] Applied %d participant moves in batch for session %s", len(moves), req.SessionID)
	}
	h.writeRouteSession(w, r, snapshot)
}

func (h *Handler) HandleSwapDrivers(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SessionID   string `json:"session_id"`
		RouteIndex1 int    `json:"route_index_1"`
		RouteIndex2 int    `json:"route_index_2"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.handleValidationErrorHTMX(w, r, messageInvalidRequestBody)
		return
	}
	snapshot, err := h.RouteSession.SwapDrivers(r.Context(), req.SessionID, req.RouteIndex1, req.RouteIndex2)
	if err != nil {
		h.handleRouteSessionError(w, r, err)
		return
	}
	log.Printf("[EDIT] Swapped drivers between routes %d and %d", req.RouteIndex1, req.RouteIndex2)
	h.writeRouteSession(w, r, snapshot)
}

func (h *Handler) HandleResetRoutes(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("session_id")
	if id == "" {
		id = r.FormValue("session_id")
	}
	snapshot, err := h.RouteSession.Reset(id)
	if err != nil {
		h.handleRouteSessionError(w, r, err)
		return
	}
	log.Printf("[EDIT] Reset routes for session %s", id)
	if h.isHTMX(r) {
		view := buildRouteResultsView(snapshot)
		view.IsEditing = true
		h.renderTemplate(w, "route_results", view)
		return
	}
	h.writeRouteSession(w, r, snapshot)
}

func (h *Handler) HandleAddDriver(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SessionID string `json:"session_id"`
		DriverID  int64  `json:"driver_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.handleValidationErrorHTMX(w, r, messageInvalidRequestBody)
		return
	}
	snapshot, err := h.RouteSession.AddDriver(r.Context(), req.SessionID, req.DriverID)
	if err != nil {
		h.handleRouteSessionError(w, r, err)
		return
	}
	log.Printf("[EDIT] Added unused driver %d to routes", req.DriverID)
	h.writeRouteSession(w, r, snapshot)
}

func (h *Handler) HandleGetRouteSession(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("session_id")
	if id == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	snapshot, ok := h.RouteSession.Snapshot(id)
	if !ok {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	h.writeRouteSession(w, r, snapshot)
}

func (h *Handler) writeRouteSession(w http.ResponseWriter, r *http.Request, snapshot routesession.Snapshot) {
	if h.isHTMX(r) {
		h.renderTemplate(w, "route_results", buildRouteResultsView(snapshot))
		return
	}
	h.writeJSON(w, http.StatusOK, RouteCalculationResponse{Routes: snapshot.Routes, Summary: snapshot.Summary, SessionID: snapshot.ID, Mode: snapshot.Mode})
}

func (h *Handler) handleRouteSessionError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, routesession.ErrNotFound):
		h.handleNotFoundHTMX(w, r, messageSessionNotFound)
	case errors.Is(err, routesession.ErrInvalidRouteIndex):
		h.handleValidationErrorHTMX(w, r, messageInvalidRouteIndex)
	case errors.Is(err, routesession.ErrParticipantNotFound):
		h.handleValidationErrorHTMX(w, r, messageParticipantNotFound)
	case errors.Is(err, routesession.ErrParticipantNotInSource):
		h.handleValidationErrorHTMX(w, r, "Participant not found in source route")
	case errors.Is(err, routesession.ErrSwapMissingDriver):
		h.handleValidationErrorHTMX(w, r, "Cannot swap - route is missing a driver")
	case errors.Is(err, routesession.ErrSwapCapacity):
		h.handleValidationErrorHTMX(w, r, "Cannot swap - capacity constraints violated")
	case errors.Is(err, routesession.ErrDriverNotSelected):
		h.handleValidationErrorHTMX(w, r, "Driver not found in selected drivers")
	case errors.Is(err, routesession.ErrDriverAlreadyInRoutes):
		h.handleValidationErrorHTMX(w, r, "Driver is already in routes")
	default:
		h.handleInternalError(w, err)
	}
}
