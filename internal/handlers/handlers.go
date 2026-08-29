package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"log"
	"net/http"
	"ride-home-router/internal/database"
	"ride-home-router/internal/geocoding"
	"ride-home-router/internal/httpx"
	"ride-home-router/internal/importer"
	"ride-home-router/internal/routesession"
	"ride-home-router/internal/routing"
	"ride-home-router/internal/templates"
	"strconv"
	"strings"
)

// Handler owns the HTTP layer's dependencies.
type Handler struct {
	DB            database.DataStore
	Geocoder      geocoding.Geocoder
	Router        routing.Router
	Renderer      *templates.Renderer
	RouteSession  *routesession.Store
	ImportSession *importer.Store
}

// ErrorResponse is the API error envelope.
type ErrorResponse struct {
	Error ErrorDetail `json:"error"`
}

// ErrorDetail describes an API error.
type ErrorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details,omitempty"`
}

type htmxToast struct {
	Message string `json:"message"`
	Type    string `json:"type"`
}

type htmxTriggerPayload struct {
	ShowToast *htmxToast
	EventName string
	EventSet  bool
}

func (p htmxTriggerPayload) MarshalJSON() ([]byte, error) {
	type alias struct {
		ShowToast *htmxToast `json:"showToast,omitempty"`
	}

	payload := alias{ShowToast: p.ShowToast}
	if !p.EventSet {
		return json.Marshal(payload)
	}

	eventNameJSON, err := json.Marshal(p.EventName)
	if err != nil {
		return nil, err
	}

	baseJSON, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	if string(baseJSON) == "{}" {
		return fmt.Appendf(nil, "{%s:true}", eventNameJSON), nil
	}

	return fmt.Appendf(nil, "{%s:true,%s", eventNameJSON, string(baseJSON[1:])), nil
}

func (h *Handler) isHTMX(r *http.Request) bool {
	return httpx.IsHTMX(r)
}

func (h *Handler) writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set(httpx.HeaderContentType, httpx.MediaTypeJSON)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func (h *Handler) writeError(w http.ResponseWriter, status int, code, message string, details any) {
	h.writeJSON(w, status, ErrorResponse{
		Error: ErrorDetail{
			Code:    code,
			Message: message,
			Details: details,
		},
	})
}

func (h *Handler) handleNotFound(w http.ResponseWriter, message string) {
	h.writeError(w, http.StatusNotFound, "NOT_FOUND", message, nil)
}

func (h *Handler) handleNotFoundHTMX(w http.ResponseWriter, r *http.Request, message string) {
	if h.isHTMX(r) {
		w.Header().Set(httpx.HeaderContentType, httpx.MediaTypeHTML)
		w.WriteHeader(http.StatusNotFound)
		_, _ = fmt.Fprintf(w, `<div class="alert alert-warning">%s</div>`, html.EscapeString(message))
		return
	}
	h.handleNotFound(w, message)
}

func (h *Handler) handleValidationError(w http.ResponseWriter, message string) {
	h.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", message, nil)
}

func (h *Handler) handleValidationErrorHTMX(w http.ResponseWriter, r *http.Request, message string) {
	if h.isHTMX(r) {
		h.setHTMXToast(w, message, toastTypeError)
		w.Header().Set(httpx.HeaderContentType, httpx.MediaTypeHTML)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = fmt.Fprintf(w, `<div class="alert alert-warning">%s</div>`, html.EscapeString(message))
		return
	}
	h.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", message, nil)
}

func (h *Handler) handleHTMXErrorNoSwap(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	if h.isHTMX(r) {
		h.setHTMXToast(w, message, toastTypeError)
		w.Header().Set(httpx.HeaderHXReswap, httpx.ReswapNone)
		w.WriteHeader(status)
		return
	}
	h.writeError(w, status, code, message, nil)
}

func (h *Handler) setHTMXToast(w http.ResponseWriter, message, toastType string) {
	h.setHTMXTrigger(w, htmxTriggerPayload{
		ShowToast: &htmxToast{
			Message: message,
			Type:    toastType,
		},
	})
}

func (h *Handler) setHTMXToastWithEvent(w http.ResponseWriter, eventName, message, toastType string) {
	h.setHTMXTrigger(w, htmxTriggerPayload{
		ShowToast: &htmxToast{
			Message: message,
			Type:    toastType,
		},
		EventName: eventName,
		EventSet:  true,
	})
}

func (h *Handler) setHTMXTrigger(w http.ResponseWriter, payload htmxTriggerPayload) {
	bytes, err := json.Marshal(payload)
	if err != nil {
		log.Printf("[ERROR] Failed to marshal HX-Trigger toast payload: %v", err)
		return
	}

	w.Header().Set(httpx.HeaderHXTrigger, string(bytes))
}

func (h *Handler) handleGeocodingError(w http.ResponseWriter, err error) {
	h.writeError(w, http.StatusUnprocessableEntity, "GEOCODING_FAILED", err.Error(), nil)
}

func (h *Handler) handleRoutingError(w http.ResponseWriter, err error) {
	if rerr, ok := err.(*routing.ErrRoutingFailed); ok {
		h.writeError(w, http.StatusUnprocessableEntity, "ROUTING_FAILED", rerr.Reason, RoutingErrorDetails{
			UnassignedCount:   rerr.UnassignedCount,
			TotalCapacity:     rerr.TotalCapacity,
			TotalParticipants: rerr.TotalParticipants,
		})
		return
	}
	h.writeError(w, http.StatusUnprocessableEntity, "ROUTING_FAILED", err.Error(), nil)
}

func (h *Handler) handleInternalError(w http.ResponseWriter, err error) {
	log.Printf("[ERROR] Internal error: %v", err)
	h.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", messageGenericInternalError, nil)
}

func (h *Handler) checkNotFound(err error) bool {
	return errors.Is(err, database.ErrNotFound)
}

func parseRestoreID(r *http.Request) (int64, error) {
	if err := r.ParseForm(); err != nil {
		return 0, err
	}
	id, err := strconv.ParseInt(strings.TrimSpace(r.FormValue("id")), 10, 64)
	if err != nil {
		return 0, err
	}
	if id <= 0 {
		return 0, errors.New("restore ID must be positive")
	}
	return id, nil
}

func (h *Handler) renderTemplate(w http.ResponseWriter, name string, data any) {
	w.Header().Set(httpx.HeaderContentType, httpx.MediaTypeHTML)

	if err := h.Renderer.Render(w, name, data); err != nil {
		log.Printf("[ERROR] Template render error: template=%s err=%v", name, err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
	}
}

func (h *Handler) renderError(w http.ResponseWriter, r *http.Request, err error) {
	if h.isHTMX(r) {
		w.Header().Set(httpx.HeaderContentType, httpx.MediaTypeHTML)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = fmt.Fprintf(w, `<div class="alert alert-error">%s</div>`, html.EscapeString(err.Error()))
		return
	}
	h.handleInternalError(w, err)
}
