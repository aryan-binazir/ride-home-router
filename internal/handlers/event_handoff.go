package handlers

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/aryan-binazir/ride-home-router/internal/models"
	"github.com/aryan-binazir/ride-home-router/internal/routesession"
)

func formatEventHandoff(snapshot routesession.CommitSnapshot, route models.CalculatedRoute, parents bool) string {
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
	fmt.Fprintf(&b, "Driver: %s\n", route.Driver.Name)
	if !parents {
		fmt.Fprintf(&b, "%s\n", displayHandoffAddress(route.Driver.AddressName, route.Driver.Address))
	}
	for i, stop := range route.Stops {
		fmt.Fprintf(&b, "%d. ", i+1)
		b.WriteString(stop.Participant.Name)
		if !parents {
			address := displayHandoffAddress(stop.Participant.AddressName, stop.Participant.Address)
			if strings.TrimSpace(address) != "" {
				fmt.Fprintf(&b, " - %s", address)
			}
		}
		b.WriteByte('\n')
	}
	if !parents {
		links := handoffMapsURLs(snapshot, route)
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

func displayHandoffAddress(name, address string) string {
	if strings.TrimSpace(name) == "" {
		return address
	}
	return fmt.Sprintf("%s (%s)", name, address)
}

func handoffMapsPoints(snapshot routesession.CommitSnapshot, route models.CalculatedRoute) []string {
	if snapshot.ActivityLocation == nil {
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
	return points
}

const maxIntermediateMapsWaypoints = 3

func handoffMapsURLs(snapshot routesession.CommitSnapshot, route models.CalculatedRoute) []string {
	points := handoffMapsPoints(snapshot, route)
	var links []string
	for start := 0; start < len(points)-1; {
		end := min(start+maxIntermediateMapsWaypoints+1, len(points)-1)
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
