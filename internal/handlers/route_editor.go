package handlers

import (
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
	Inline        bool
	Choices       []editorChoice
	Search        string
	SearchURL     string
	PreviousURL   string
	NextURL       string
}

func (h *Handler) HandleRouteEditor(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("session_id")
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
	view := routeEditorView{SessionID: id, Action: action, From: from, ParticipantID: participantID, Inline: h.isHTMX(r), Search: strings.TrimSpace(r.URL.Query().Get("search")), SearchURL: r.URL.Path}
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
	id := string([]rune(r.Form.Get("session_id")))
	switch r.Form.Get("action") {
	case "move":
		participant, err := strconv.ParseInt(r.Form.Get("participant_id"), 10, 64)
		if err != nil || fromErr != nil {
			h.handleValidationErrorHTMX(w, r, messageInvalidRequestBody)
			return
		}
		r.Header.Set("HX-Request", "true")
		if int64(int(destination)) != destination {
			h.handleValidationErrorHTMX(w, r, messageInvalidRequestBody)
			return
		}
		h.moveParticipants(w, r, id, []participantMove{{participant, from, int(destination), -1}}, true)
	case "swap":
		if fromErr != nil {
			h.handleValidationErrorHTMX(w, r, messageInvalidRequestBody)
			return
		}
		r.Header.Set("HX-Request", "true")
		if int64(int(destination)) != destination {
			h.handleValidationErrorHTMX(w, r, messageInvalidRequestBody)
			return
		}
		h.swapDrivers(w, r, id, from, int(destination))
	case "add":
		r.Header.Set("HX-Request", "true")
		h.addDriver(w, r, id, destination)
	default:
		h.handleValidationErrorHTMX(w, r, messageInvalidRequestBody)
	}
}
