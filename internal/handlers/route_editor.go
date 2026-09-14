package handlers

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const editorPageSize = 25

type editorChoice struct {
	Value  int64
	Name   string
	Detail string
}

type routeEditorView struct {
	BasePageView
	SessionID     string
	Action        string
	From          int
	ParticipantID int64
	Mobile        bool
	Inline        bool
	Choices       []editorChoice
	Search        string
	SearchURL     string
	PreviousURL   string
	NextURL       string
	ReturnURL     string
}

// HandleRouteEditor renders only the requested page of route-edit choices.
// Opening/searching an editor never measures or changes an itinerary.
func (h *Handler) HandleRouteEditor(w http.ResponseWriter, r *http.Request) {
	mobile := strings.HasPrefix(r.URL.Path, "/m/")
	id := r.URL.Query().Get("session_id")
	if mobile {
		if _, _, ok := h.mobileRouteSession(w, r); !ok {
			return
		}
	}
	snapshot, found, err := h.RouteSession.Load(r.Context(), id)
	if err != nil {
		h.handleInternalError(w, r, err)
		return
	}
	if !found {
		h.handleNotFoundHTMX(w, r, messageSessionNotFound)
		return
	}
	action := r.URL.Query().Get("action")
	from, parseErr := strconv.Atoi(r.URL.Query().Get("from_route_index"))
	participantID, _ := strconv.ParseInt(r.URL.Query().Get("participant_id"), 10, 64)
	view := routeEditorView{SessionID: id, Action: action, From: from, ParticipantID: participantID, Mobile: mobile, Inline: h.isHTMX(r), Search: strings.TrimSpace(r.URL.Query().Get("search")), SearchURL: r.URL.Path, ReturnURL: "/"}
	if mobile {
		view.ReturnURL = "/m/routes"
	}
	if action != "move" && action != "swap" && action != "add" {
		h.handleValidationErrorHTMX(w, r, messageInvalidRequestBody)
		return
	}
	if action != "add" && (parseErr != nil || from < 0 || from >= len(snapshot.Routes)) {
		h.handleValidationErrorHTMX(w, r, messageInvalidRouteIndex)
		return
	}
	switch action {
	case "move":
		for _, stop := range snapshot.Routes[from].Stops {
			if stop.Participant != nil && stop.Participant.ID == participantID {
				view.Title = "Move " + stop.Participant.Name
				break
			}
		}
		if view.Title == "" {
			h.handleValidationErrorHTMX(w, r, "That rider is no longer on this route. Refresh the page and try again.")
			return
		}
	case "swap":
		if snapshot.Routes[from].Driver == nil {
			h.handleValidationErrorHTMX(w, r, messageInvalidRouteIndex)
			return
		}
		view.Title = "Swap " + snapshot.Routes[from].Driver.Name
	case "add":
		view.Title = "Add a driver"
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if offset < 0 {
		offset = 0
	}
	matches := 0
	add := func(value int64, name, detail string) {
		if !strings.Contains(strings.ToLower(name+" "+detail), strings.ToLower(view.Search)) {
			return
		}
		if matches >= offset && len(view.Choices) < editorPageSize {
			view.Choices = append(view.Choices, editorChoice{value, name, detail})
		}
		matches++
	}
	if action == "add" {
		for _, driver := range snapshot.UnusedDrivers {
			add(driver.ID, driver.Name, strconv.Itoa(driver.VehicleCapacity)+" seats")
		}
	} else {
		for i, route := range snapshot.Routes {
			if i == from || route.Driver == nil {
				continue
			}
			add(int64(i), route.Driver.Name, strconv.Itoa(len(route.Stops))+" riders · "+strconv.Itoa(route.EffectiveCapacity)+" seats")
		}
	}
	pageURL := func(next int) string {
		q := url.Values{"session_id": {id}, "action": {action}, "from_route_index": {strconv.Itoa(from)}, "participant_id": {strconv.FormatInt(participantID, 10)}, "search": {view.Search}, "offset": {strconv.Itoa(next)}}
		return r.URL.Path + "?" + q.Encode()
	}
	if offset > 0 {
		view.PreviousURL = pageURL(max(0, offset-editorPageSize))
	}
	if offset < matches-editorPageSize {
		view.NextURL = pageURL(offset + editorPageSize)
	}
	if h.isHTMX(r) {
		h.renderTemplate(w, "route_editor", view)
	} else {
		h.renderTemplate(w, "editor.html", view)
	}
}

// HandleRouteEditorAction is the ordinary form fallback. JavaScript submissions
// still use the desktop edit queue and its guarded, targeted responses.
func (h *Handler) HandleRouteEditorAction(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.handleValidationErrorHTMX(w, r, messageInvalidRequestBody)
		return
	}
	destination, err := strconv.ParseInt(r.Form.Get("destination"), 10, 64)
	if err != nil {
		h.handleValidationErrorHTMX(w, r, messageInvalidRequestBody)
		return
	}
	from, fromErr := strconv.Atoi(r.Form.Get("from_route_index"))
	payload := map[string]any{"session_id": r.Form.Get("session_id")}
	var action http.HandlerFunc
	switch r.Form.Get("action") {
	case "move":
		participant, err := strconv.ParseInt(r.Form.Get("participant_id"), 10, 64)
		if err != nil || fromErr != nil {
			h.handleValidationErrorHTMX(w, r, messageInvalidRequestBody)
			return
		}
		payload["participant_id"] = participant
		payload["from_route_index"] = from
		payload["to_route_index"] = destination
		payload["insert_at_position"] = -1
		action = h.HandleMoveParticipant
	case "swap":
		if fromErr != nil {
			h.handleValidationErrorHTMX(w, r, messageInvalidRequestBody)
			return
		}
		payload["route_index_1"] = from
		payload["route_index_2"] = destination
		action = h.HandleSwapDrivers
	case "add":
		payload["driver_id"] = destination
		action = h.HandleAddDriver
	default:
		h.handleValidationErrorHTMX(w, r, messageInvalidRequestBody)
		return
	}
	body, err := json.Marshal(payload)
	if err != nil {
		h.handleInternalError(w, r, err)
		return
	}
	request := r.Clone(r.Context())
	request.Body = io.NopCloser(bytes.NewReader(body))
	request.ContentLength = int64(len(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("HX-Request", "true")
	action(w, request)
}

// HandleMobileRouteEditorAction keeps the existing validated mobile mutations
// authoritative while sharing a single choice form for all editor actions.
func (h *Handler) HandleMobileRouteEditorAction(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.mobileRedirectError(w, r, "/m/routes", messageMobileInvalidForm)
		return
	}
	switch r.Form.Get("action") {
	case "move":
		r.Form.Set("to_route_index", r.Form.Get("destination"))
		h.HandleMobileMove(w, r)
	case "swap":
		r.Form.Set("route_index_1", r.Form.Get("from_route_index"))
		r.Form.Set("route_index_2", r.Form.Get("destination"))
		h.HandleMobileSwap(w, r)
	case "add":
		r.Form.Set("driver_id", r.Form.Get("destination"))
		h.HandleMobileAddDriver(w, r)
	default:
		h.mobileRedirectError(w, r, "/m/routes", messageMobileInvalidForm)
	}
}
