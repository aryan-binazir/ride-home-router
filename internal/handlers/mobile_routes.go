package handlers

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"ride-home-router/internal/models"
	"ride-home-router/internal/plandraft"
	"ride-home-router/internal/routesession"
	"strconv"
	"strings"
	"time"
)

func (h *Handler) HandleMobileRoutes(w http.ResponseWriter, r *http.Request) {
	logMobileRequest(r)
	_, draft, _ := h.mobileDraft(w, r)
	if draft.RouteSessionID == "" {
		http.Redirect(w, r, "/m", http.StatusSeeOther)
		return
	}
	snapshot, ok := h.RouteSession.Snapshot(draft.RouteSessionID)
	if !ok {
		h.mobileRedirectError(w, r, "/m", "That route plan expired. Calculate it again.")
		return
	}
	view := mobileRoutesView{mobileBaseView: newMobileBase("Routes", "plan", r.URL.Query().Get("error")), Snapshot: snapshot}
	for index, route := range snapshot.Routes {
		view.Routes = append(view.Routes, mobileRoute{Index: index, Route: route, DriverText: formatMobileHandoff(snapshot, route, false), ParentText: formatMobileHandoff(snapshot, route, true), ETAs: mobileETAs(snapshot, route)})
	}
	h.renderTemplate(w, "mobile/routes.html", view)
}

func (h *Handler) HandleMobileMove(w http.ResponseWriter, r *http.Request) {
	logMobileRequest(r)
	_, draft, _ := h.mobileDraft(w, r)
	if err := r.ParseForm(); err != nil {
		h.mobileRedirectError(w, r, "/m/routes", messageMobileInvalidForm)
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
	_, err := h.RouteSession.ApplyMoves(r.Context(), draft.RouteSessionID, []routesession.Move{{ParticipantID: participantID, FromRouteIndex: from, ToRouteIndex: to, InsertAtPosition: -1}}, routesession.ApplyMovesOptions{RequireClaimedSource: true})
	if err != nil {
		log.Printf("[ERROR] Mobile move failed: err=%v", err)
		h.mobileRedirectError(w, r, "/m/routes", mobileRouteErrorMessage(err))
		return
	}
	http.Redirect(w, r, "/m/routes", http.StatusSeeOther)
}

func (h *Handler) HandleMobileSwap(w http.ResponseWriter, r *http.Request) {
	logMobileRequest(r)
	_, draft, _ := h.mobileDraft(w, r)
	if err := r.ParseForm(); err != nil {
		h.mobileRedirectError(w, r, "/m/routes", messageMobileInvalidForm)
		return
	}
	first, firstErr := strconv.Atoi(r.FormValue("route_index_1"))
	second, secondErr := strconv.Atoi(r.FormValue("route_index_2"))
	if firstErr != nil || secondErr != nil || first < 0 || second < 0 || first == second {
		h.mobileRedirectError(w, r, "/m/routes", messageInvalidRouteIndex)
		return
	}
	if _, err := h.RouteSession.SwapDrivers(r.Context(), draft.RouteSessionID, first, second); err != nil {
		log.Printf("[ERROR] Mobile driver swap failed: err=%v", err)
		h.mobileRedirectError(w, r, "/m/routes", mobileRouteErrorMessage(err))
		return
	}
	http.Redirect(w, r, "/m/routes", http.StatusSeeOther)
}

func (h *Handler) HandleMobileReset(w http.ResponseWriter, r *http.Request) {
	logMobileRequest(r)
	_, draft, _ := h.mobileDraft(w, r)
	if _, err := h.RouteSession.Reset(draft.RouteSessionID); err != nil {
		log.Printf("[ERROR] Mobile route reset failed: err=%v", err)
		h.mobileRedirectError(w, r, "/m/routes", mobileRouteErrorMessage(err))
		return
	}
	http.Redirect(w, r, "/m/routes", http.StatusSeeOther)
}

func (h *Handler) HandleMobileAddDriver(w http.ResponseWriter, r *http.Request) {
	logMobileRequest(r)
	_, draft, _ := h.mobileDraft(w, r)
	if err := r.ParseForm(); err != nil {
		h.mobileRedirectError(w, r, "/m/routes", messageMobileInvalidForm)
		return
	}
	driverID, err := strconv.ParseInt(r.FormValue("driver_id"), 10, 64)
	if err != nil || driverID <= 0 {
		h.mobileRedirectError(w, r, "/m/routes", messageInvalidDriverID)
		return
	}
	if _, err := h.RouteSession.AddDriver(r.Context(), draft.RouteSessionID, driverID); err != nil {
		log.Printf("[ERROR] Mobile add driver failed: err=%v", err)
		h.mobileRedirectError(w, r, "/m/routes", mobileRouteErrorMessage(err))
		return
	}
	http.Redirect(w, r, "/m/routes", http.StatusSeeOther)
}

func (h *Handler) HandleMobileSave(w http.ResponseWriter, r *http.Request) {
	logMobileRequest(r)
	id, draft, _ := h.mobileDraft(w, r)
	if err := r.ParseForm(); err != nil {
		h.mobileRedirectError(w, r, "/m/routes", messageMobileInvalidForm)
		return
	}
	date := r.FormValue("event_date")
	if date == "" {
		date = time.Now().Format("2006-01-02")
	}
	created, _, err := h.commitEventSession(r, draft.RouteSessionID, date, strings.TrimSpace(r.FormValue("notes")))
	if err != nil {
		log.Printf("[ERROR] Mobile event save failed: err=%v", err)
		if validationErr, ok := errors.AsType[eventValidationError](err); ok {
			h.mobileRedirectError(w, r, "/m/routes", validationErr.message)
			return
		}
		h.mobileRedirectError(w, r, "/m/routes", mobileRouteErrorMessage(err))
		return
	}
	h.PlanDraft.Update(id, func(d *plandraft.Draft) { d.RouteSessionID = "" })
	http.Redirect(w, r, fmt.Sprintf("/m/history/%d", created.ID), http.StatusSeeOther)
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
		if mapsURL := mobileMapsURL(snapshot, route); mapsURL != "" {
			fmt.Fprintf(&b, "\nMaps: %s\n", mapsURL)
		}
	}
	return b.String()
}

func formatSavedMobileHandoff(route models.EventRoute, parents bool) string {
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

func mobileMapsURL(snapshot routesession.Snapshot, route models.CalculatedRoute) string {
	if snapshot.ActivityLocation == nil || route.Driver == nil || len(route.Stops) == 0 {
		return ""
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
		return ""
	}
	stops := make([]string, 0, len(route.Stops))
	seen := map[string]bool{}
	for _, stop := range route.Stops {
		if stop.Participant == nil {
			return ""
		}
		value := location(stop.Participant.Lat, stop.Participant.Lng, stop.Participant.Address)
		if value == "" {
			return ""
		}
		key := strings.ToLower(value)
		if !seen[key] {
			seen[key] = true
			stops = append(stops, value)
		}
	}
	points := append([]string{activity}, stops...)
	points = append(points, driver)
	if snapshot.Mode == models.RouteModePickup {
		points[0], points[len(points)-1] = points[len(points)-1], points[0]
	}
	query := url.Values{"api": {"1"}, "travelmode": {"driving"}, "dir_action": {"navigate"}, "destination": {points[len(points)-1]}}
	if len(points) > 2 {
		query.Set("waypoints", strings.Join(points[1:len(points)-1], "|"))
	}
	return "https://www.google.com/maps/dir/?" + query.Encode()
}
