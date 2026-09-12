package handlers

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"ride-home-router/internal/database"
	"ride-home-router/internal/models"
	"ride-home-router/internal/routesession"
	"strconv"
	"strings"
	"time"
)

func (h *Handler) HandleMobileRoutes(w http.ResponseWriter, r *http.Request) {
	logMobileRequest(r)
	_, draft, _, loadErr := h.mobileDraft(w, r)
	if loadErr != nil {
		h.renderMobileStoreError(w, r, loadErr, "That plan is no longer available. Start a new plan.")
		return
	}
	if draft.RouteSessionID == "" {
		if message := r.URL.Query().Get("error"); message != "" {
			h.mobileRedirectError(w, r, "/m", curatedMobileQueryError(message))
		} else {
			http.Redirect(w, r, "/m", http.StatusSeeOther)
		}
		return
	}
	snapshot, ok, err := h.RouteSession.Load(r.Context(), draft.RouteSessionID)
	if err != nil {
		h.renderMobileStoreError(w, r, err, "That route plan expired. Calculate it again.")
		return
	}
	if !ok {
		h.mobileRedirectError(w, r, "/m", "That route plan expired. Calculate it again.")
		return
	}
	h.renderMobileRoutes(w, r, snapshot, http.StatusOK, r.URL.Query().Get("error"), "", "")
}

func (h *Handler) renderMobileRoutes(w http.ResponseWriter, r *http.Request, snapshot routesession.Snapshot, status int, message, date, notes string) {
	view := mobileRoutesView{EventDate: date, Notes: notes, mobileBaseView: newMobileBase(mobileRoutesTitle(snapshot.Mode), "plan", message), Snapshot: snapshot}
	for index, route := range snapshot.Routes {
		view.Routes = append(view.Routes, mobileRoute{Index: index, Route: route, DriverText: formatMobileHandoff(snapshot, route, false), ParentText: formatMobileHandoff(snapshot, route, true), ETAs: mobileETAs(snapshot, route)})
	}
	h.renderMobileTemplateStatus(w, r, status, "mobile/routes.html", view)
}

func (h *Handler) HandleMobileMove(w http.ResponseWriter, r *http.Request) {
	logMobileRequest(r)
	_, sessionID, ok := h.mobileRouteSession(w, r)
	if !ok {
		return
	}
	participantID, participantErr := strconv.ParseInt(r.FormValue("participant_id"), 10, 64)
	from, fromErr := strconv.Atoi(r.FormValue("from_route_index"))
	to, toErr := strconv.Atoi(r.FormValue("to_route_index"))
	if participantErr != nil || participantID <= 0 || fromErr != nil || toErr != nil || from < 0 || to < 0 {
		h.mobileRedirectError(w, r, "/m/routes", messageMobileInvalidForm)
		return
	}
	if from == to {
		h.mobileRedirectError(w, r, "/m/routes", "Choose a different route.")
		return
	}
	_, err := h.RouteSession.ApplyMoves(r.Context(), sessionID, []routesession.Move{{ParticipantID: participantID, FromRouteIndex: from, ToRouteIndex: to, InsertAtPosition: -1}}, routesession.ApplyMovesOptions{RequireClaimedSource: true})
	if err != nil {
		log.Printf("[ERROR] Mobile move failed: err=%v", err)
		h.mobileRedirectError(w, r, "/m/routes", mobileRouteErrorMessage(err))
		return
	}
	http.Redirect(w, r, "/m/routes", http.StatusSeeOther)
}

func (h *Handler) HandleMobileSwap(w http.ResponseWriter, r *http.Request) {
	logMobileRequest(r)
	_, sessionID, ok := h.mobileRouteSession(w, r)
	if !ok {
		return
	}
	first, firstErr := strconv.Atoi(r.FormValue("route_index_1"))
	second, secondErr := strconv.Atoi(r.FormValue("route_index_2"))
	if firstErr != nil || secondErr != nil || first < 0 || second < 0 || first == second {
		h.mobileRedirectError(w, r, "/m/routes", messageInvalidRouteIndex)
		return
	}
	if _, err := h.RouteSession.SwapDrivers(r.Context(), sessionID, first, second); err != nil {
		log.Printf("[ERROR] Mobile driver swap failed: err=%v", err)
		h.mobileRedirectError(w, r, "/m/routes", mobileRouteErrorMessage(err))
		return
	}
	http.Redirect(w, r, "/m/routes", http.StatusSeeOther)
}

func (h *Handler) HandleMobileReset(w http.ResponseWriter, r *http.Request) {
	logMobileRequest(r)
	_, sessionID, ok := h.mobileRouteSession(w, r)
	if !ok {
		return
	}
	if _, err := h.RouteSession.ResetContext(r.Context(), sessionID); err != nil {
		log.Printf("[ERROR] Mobile route reset failed: err=%v", err)
		h.mobileRedirectError(w, r, "/m/routes", mobileRouteErrorMessage(err))
		return
	}
	http.Redirect(w, r, "/m/routes", http.StatusSeeOther)
}

func (h *Handler) HandleMobileAddDriver(w http.ResponseWriter, r *http.Request) {
	logMobileRequest(r)
	_, sessionID, ok := h.mobileRouteSession(w, r)
	if !ok {
		return
	}
	driverID, err := strconv.ParseInt(r.FormValue("driver_id"), 10, 64)
	if err != nil || driverID <= 0 {
		h.mobileRedirectError(w, r, "/m/routes", messageInvalidDriverID)
		return
	}
	if _, err := h.RouteSession.AddDriver(r.Context(), sessionID, driverID); err != nil {
		log.Printf("[ERROR] Mobile add driver failed: err=%v", err)
		h.mobileRedirectError(w, r, "/m/routes", mobileRouteErrorMessage(err))
		return
	}
	http.Redirect(w, r, "/m/routes", http.StatusSeeOther)
}

func (h *Handler) HandleMobileSave(w http.ResponseWriter, r *http.Request) {
	logMobileRequest(r)
	if err := r.ParseForm(); err != nil {
		h.renderMobileError(w, r, http.StatusBadRequest, messageMobileInvalidForm, nil)
		return
	}
	sessionID := strings.TrimSpace(r.FormValue("session_id"))
	id, draft, _, loadErr := h.mobileDraft(w, r)
	if loadErr != nil {
		h.renderMobileStoreError(w, r, loadErr, messageRoutePlanExpired)
		return
	}
	if sessionID == "" || sessionID != draft.RouteSessionID {
		if sessionID != "" && h.redirectSavedMobileEvent(w, r, sessionID) {
			return
		}
		h.mobileRedirectError(w, r, "/m/routes", "This route plan changed. Review the current routes and try again.")
		return
	}

	date := r.FormValue("event_date")
	if date == "" {
		date = time.Now().Format("2006-01-02")
	}
	created, _, err := h.commitEventSession(r, sessionID, date, strings.TrimSpace(r.FormValue("notes")))
	if err != nil {
		log.Printf("[ERROR] Mobile event save failed: err=%v", err)
		if errors.Is(err, routesession.ErrAlreadyCommitted) && h.redirectSavedMobileEvent(w, r, sessionID) {
			return
		}
		status, message := http.StatusInternalServerError, messageGenericInternalError
		if validationErr, ok := errors.AsType[eventValidationError](err); ok {
			status, message = http.StatusBadRequest, validationErr.message
		}
		snapshot, found, loadErr := h.RouteSession.Load(r.Context(), sessionID)
		if loadErr != nil || !found {
			h.renderMobileError(w, r, status, message, nil)
			return
		}
		h.renderMobileRoutes(w, r, snapshot, status, message, date, r.FormValue("notes"))
		return
	}
	if err := h.mobilePlan().ReleaseSavedSessionContext(r.Context(), id, sessionID); err != nil {
		log.Printf("[MOBILE] Saved event but draft release failed: %v", err)
	}
	http.Redirect(w, r, fmt.Sprintf("/m/history/%d", created.ID), http.StatusSeeOther)
}

// mobileRouteSession binds a submitted action to the routes its form displayed.
func (h *Handler) mobileRouteSession(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	id, draft, _, loadErr := h.mobileDraft(w, r)
	if loadErr != nil {
		h.renderMobileStoreError(w, r, loadErr, "That plan is no longer available. Start a new plan.")
		return "", "", false
	}
	if err := r.ParseForm(); err != nil {
		h.mobileRedirectError(w, r, "/m/routes", messageMobileInvalidForm)
		return "", "", false
	}
	sessionID := strings.TrimSpace(r.FormValue("session_id"))
	if sessionID == "" || sessionID != draft.RouteSessionID {
		h.mobileRedirectError(w, r, "/m/routes", "This route plan changed. Review the current routes and try again.")
		return "", "", false
	}
	return id, sessionID, true
}

func mobileETAs(snapshot routesession.Snapshot, route models.CalculatedRoute) []string {
	base, err := time.Parse("15:04", snapshot.RouteTime)
	if err != nil {
		return make([]string, len(route.Stops))
	}
	values := make([]string, len(route.Stops))
	for i, stop := range route.Stops {
		seconds := stop.CumulativeDurationSecs
		if snapshot.Mode == models.RouteModePickup {
			seconds -= route.RouteDurationSecs
		} else {
			seconds += 120
		}
		values[i] = base.Add(time.Duration(seconds * float64(time.Second))).Format("3:04 PM")
	}
	return values
}

func formatMobileHandoff(snapshot routesession.Snapshot, route models.CalculatedRoute, parents bool) string {
	var b strings.Builder
	locationName, locationAddress := "Activity location", ""
	if snapshot.ActivityLocation != nil {
		locationName, locationAddress = snapshot.ActivityLocation.Name, snapshot.ActivityLocation.Address
	}
	if routeTime, err := time.Parse("15:04", snapshot.RouteTime); err == nil {
		if snapshot.Mode == models.RouteModePickup {
			fmt.Fprintf(&b, "Pickup: arrive at %s by %s\n\n", locationName, routeTime.Format("3:04 PM"))
		} else {
			fmt.Fprintf(&b, "Dropoff: leave %s at %s\n\n", locationName, routeTime.Format("3:04 PM"))
		}
	}
	fmt.Fprintf(&b, "Activity Location: %s\n%s\n\n", locationName, locationAddress)
	if route.Driver == nil {
		return b.String()
	}
	fmt.Fprintf(&b, "Driver: %s\n", route.Driver.Name)
	if !parents {
		fmt.Fprintf(&b, "%s\n", displayMobileAddress(route.Driver.AddressName, route.Driver.Address))
	}
	etas := mobileETAs(snapshot, route)
	emitted := 0
	for i, stop := range route.Stops {
		if stop.Participant == nil {
			continue
		}
		emitted++
		fmt.Fprintf(&b, "%d. ", emitted)
		if i < len(etas) && etas[i] != "" {
			fmt.Fprintf(&b, "%s - ", etas[i])
		}
		b.WriteString(stop.Participant.Name)
		if !parents {
			address := displayMobileAddress(stop.Participant.AddressName, stop.Participant.Address)
			if strings.TrimSpace(address) != "" {
				fmt.Fprintf(&b, " - %s", address)
			}
		}
		b.WriteByte('\n')
	}
	if !parents {
		links := mobileMapsURLs(snapshot, route)
		for index, link := range links {
			if len(links) == 1 {
				fmt.Fprintf(&b, "\nMaps: %s\n", link)
			} else {
				fmt.Fprintf(&b, "\nMaps leg %d of %d: %s\n", index+1, len(links), link)
			}
		}
	}
	return b.String()
}

func formatSavedMobileHandoff(route models.EventRoute, parents bool) string {
	if parents && route.ParentHandoff != "" {
		return route.ParentHandoff
	}
	if !parents && route.DriverHandoff != "" {
		return route.DriverHandoff
	}
	// Legacy events did not retain the original location and timing context.
	var b strings.Builder
	fmt.Fprintf(&b, "Driver: %s\n", route.DriverName)
	if !parents {
		fmt.Fprintf(&b, "%s\n", displayMobileAddress(route.DriverAddressName, route.DriverAddress))
	}
	for index, stop := range route.Stops {
		fmt.Fprintf(&b, "%d. %s", index+1, stop.ParticipantName)
		if !parents {
			address := displayMobileAddress(stop.ParticipantAddressName, stop.ParticipantAddress)
			if strings.TrimSpace(address) != "" {
				fmt.Fprintf(&b, " - %s", address)
			}
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func displayMobileAddress(name, address string) string {
	if strings.TrimSpace(name) == "" {
		return address
	}
	return fmt.Sprintf("%s (%s)", name, address)
}

func mobileMapsURLs(snapshot routesession.Snapshot, route models.CalculatedRoute) []string {
	if snapshot.ActivityLocation == nil || route.Driver == nil || len(route.Stops) == 0 {
		return nil
	}
	location := func(lat, lng float64, address string) string {
		if lat != 0 || lng != 0 {
			return fmt.Sprintf("%.6f,%.6f", lat, lng)
		}
		return strings.TrimSpace(address)
	}
	activity := location(snapshot.ActivityLocation.Lat, snapshot.ActivityLocation.Lng, snapshot.ActivityLocation.Address)
	driver := location(route.Driver.Lat, route.Driver.Lng, route.Driver.Address)
	if activity == "" || driver == "" {
		return nil
	}
	stops := make([]string, 0, len(route.Stops))
	for _, stop := range route.Stops {
		if stop.Participant == nil {
			return nil
		}
		value := location(stop.Participant.Lat, stop.Participant.Lng, stop.Participant.Address)
		if value == "" {
			return nil
		}
		if len(stops) == 0 || !strings.EqualFold(stops[len(stops)-1], value) {
			stops = append(stops, value)
		}
	}
	points := append([]string{activity}, stops...)
	points = append(points, driver)
	if snapshot.Mode == models.RouteModePickup {
		points[0], points[len(points)-1] = points[len(points)-1], points[0]
	}
	// Mobile browsers support three intermediate waypoints per Maps URL.
	// Each later leg starts at the preceding leg's destination.
	var links []string
	for start := 0; start < len(points)-1; {
		end := min(start+4, len(points)-1)
		query := url.Values{"api": {"1"}, "travelmode": {"driving"}, "dir_action": {"navigate"}, "destination": {points[end]}}
		if start > 0 {
			query.Set("origin", points[start])
		}
		if end > start+1 {
			query.Set("waypoints", strings.Join(points[start+1:end], "|"))
		}
		links = append(links, "https://www.google.com/maps/dir/?"+query.Encode())
		start = end
	}
	return links
}

func mobileRoutesTitle(mode models.RouteMode) string {
	if mode == models.RouteModePickup {
		return "Pickup routes"
	}
	return "Dropoff routes"
}

func (h *Handler) redirectSavedMobileEvent(w http.ResponseWriter, r *http.Request, sessionID string) bool {
	repository, ok := h.DB.Events().(interface {
		FindByRouteSessionID(context.Context, string) (*models.Event, error)
	})
	if !ok {
		return false
	}
	event, err := repository.FindByRouteSessionID(r.Context(), sessionID)
	if errors.Is(err, database.ErrNotFound) {
		return false
	}
	if err != nil {
		h.renderMobileError(w, r, http.StatusInternalServerError, messageGenericInternalError, err)
		return true
	}
	//nolint:gosec // The target contains only a fixed local prefix and a numeric database ID.
	http.Redirect(w, r, fmt.Sprintf("/m/history/%d", event.ID), http.StatusSeeOther)
	return true
}
