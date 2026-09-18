package handlers

import (
	"log"
	"net/http"
	"ride-home-router/internal/models"
	"ride-home-router/internal/routefeedback"
	"ride-home-router/internal/routesession"
	"strings"
)

type routeFeedbackView struct {
	SessionID string
	Changes   []routesession.RouteChange
	Note      string
}

// collectsReviewerNotes reports whether this request may explain its route
// edits: the setting is on and the request belongs to the configured reviewer.
func (h *Handler) collectsReviewerNotes(r *http.Request) bool {
	if strings.TrimSpace(r.Header.Get(routefeedback.AuthenticatedUserEmailHeader)) == "" {
		return false
	}
	settings, err := h.DB.Settings().Get(r.Context())
	if err != nil {
		log.Printf("[FEEDBACK] settings read failed: err=%v", err)
		return false
	}
	if !settings.CollectReviewerNotes {
		return false
	}
	_, ok := routefeedback.ShouldCapture(r, settings)
	return ok
}

// HandleRouteFeedback handles GET and POST /api/v1/routes/feedback. GET renders
// the reviewer's feedback dialog for a session; POST stores their note on it.
func (h *Handler) HandleRouteFeedback(w http.ResponseWriter, r *http.Request) {
	if !h.collectsReviewerNotes(r) {
		h.handleHTMXErrorNoSwap(w, r, http.StatusForbidden, "FORBIDDEN", messageReviewerFeedbackUnavailable)
		return
	}
	if r.Method == http.MethodGet {
		snapshot, ok, err := h.RouteSession.Load(r.Context(), r.URL.Query().Get("session_id"))
		if err != nil {
			h.handleInternalError(w, r, err)
			return
		}
		if !ok {
			h.handleNotFoundHTMX(w, r, messageSessionNotFound)
			return
		}
		h.renderTemplate(w, "route_feedback", routeFeedbackView{SessionID: snapshot.ID, Changes: snapshot.Changes, Note: snapshot.ReviewerNote})
		return
	}
	if err := r.ParseForm(); err != nil {
		h.handleValidationErrorHTMX(w, r, messageInvalidFormData)
		return
	}
	note := strings.TrimSpace(r.FormValue("note"))
	if message := fieldLengthMessage("Feedback", note, models.MaxNotesLength); message != "" {
		h.handleValidationErrorHTMX(w, r, message)
		return
	}
	if _, err := h.RouteSession.SetReviewerNote(r.Context(), r.FormValue("session_id"), note); err != nil {
		h.handleRouteSessionError(w, r, err)
		return
	}
	h.setHTMXToast(w, messageReviewerNoteSaved, toastTypeSuccess)
	w.WriteHeader(http.StatusNoContent)
}
