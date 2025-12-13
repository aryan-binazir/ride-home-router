package models

import "time"

// Coordinates represents a geographic point
type Coordinates struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

// Participant represents a person to be driven home
type Participant struct {
	ID        int64       `json:"id"`
	Name      string      `json:"name"`
	Address   string      `json:"address"`
	Lat       float64     `json:"lat"`
	Lng       float64     `json:"lng"`
	CreatedAt time.Time   `json:"created_at"`
	UpdatedAt time.Time   `json:"updated_at"`
}

// GetCoords returns the coordinates of the participant
func (p *Participant) GetCoords() Coordinates {
	return Coordinates{Lat: p.Lat, Lng: p.Lng}
}

// Coords returns the coordinates for template use
func (p *Participant) Coords() Coordinates {
	return Coordinates{Lat: p.Lat, Lng: p.Lng}
}

// Driver represents a person who can drive participants home
type Driver struct {
	ID              int64     `json:"id"`
	Name            string    `json:"name"`
	Address         string    `json:"address"`
	Lat             float64   `json:"lat"`
	Lng             float64   `json:"lng"`
	VehicleCapacity int       `json:"vehicle_capacity"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// GetCoords returns the coordinates of the driver
func (d *Driver) GetCoords() Coordinates {
	return Coordinates{Lat: d.Lat, Lng: d.Lng}
}

// Coords returns the coordinates for template use
func (d *Driver) Coords() Coordinates {
	return Coordinates{Lat: d.Lat, Lng: d.Lng}
}

// ActivityLocation represents a location where activities take place
type ActivityLocation struct {
	ID      int64   `json:"id"`
	Name    string  `json:"name"`
	Address string  `json:"address"`
	Lat     float64 `json:"lat"`
	Lng     float64 `json:"lng"`
}

// GetCoords returns the coordinates of the activity location
func (a *ActivityLocation) GetCoords() Coordinates {
	return Coordinates{Lat: a.Lat, Lng: a.Lng}
}

// OrganizationVehicle represents a vehicle owned by the organization
type OrganizationVehicle struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Capacity  int       `json:"capacity"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Settings holds application configuration
type Settings struct {
	InstituteAddress           string  `json:"institute_address"` // Deprecated: use SelectedActivityLocationID
	InstituteLat               float64 `json:"institute_lat"`     // Deprecated: use SelectedActivityLocationID
	InstituteLng               float64 `json:"institute_lng"`     // Deprecated: use SelectedActivityLocationID
	SelectedActivityLocationID int64   `json:"selected_activity_location_id"`
	UseMiles                   bool    `json:"use_miles"`
}

// GetCoords returns the institute coordinates
func (s *Settings) GetCoords() Coordinates {
	return Coordinates{Lat: s.InstituteLat, Lng: s.InstituteLng}
}

// Event represents a historical event record
type Event struct {
	ID        int64     `json:"id"`
	EventDate time.Time `json:"event_date"`
	Notes     string    `json:"notes"`
	CreatedAt time.Time `json:"created_at"`
}

// EventAssignment represents a snapshot of a participant assignment
type EventAssignment struct {
	ID                 int64   `json:"id"`
	EventID            int64   `json:"event_id"`
	DriverID           int64   `json:"driver_id"`
	DriverName         string  `json:"driver_name"`
	DriverAddress      string  `json:"driver_address"`
	RouteOrder         int     `json:"route_order"`
	ParticipantID      int64   `json:"participant_id"`
	ParticipantName    string  `json:"participant_name"`
	ParticipantAddress string  `json:"participant_address"`
	DistanceFromPrev   float64 `json:"distance_from_prev_meters"`
	OrgVehicleID       int64   `json:"org_vehicle_id,omitempty"`
	OrgVehicleName     string  `json:"org_vehicle_name,omitempty"`
}

// EventSummary contains aggregate stats for an event
type EventSummary struct {
	EventID             int64   `json:"event_id"`
	TotalParticipants   int     `json:"total_participants"`
	TotalDrivers        int     `json:"total_drivers"`
	TotalDistanceMeters float64 `json:"total_distance_meters"`
	OrgVehiclesUsed     int     `json:"org_vehicles_used,omitempty"`
}

// RouteStop represents a single stop in a calculated route
type RouteStop struct {
	Order                    int          `json:"order"`
	Participant              *Participant `json:"participant"`
	DistanceFromPrevMeters   float64      `json:"distance_from_prev_meters"`
	CumulativeDistanceMeters float64      `json:"cumulative_distance_meters"`
	DurationFromPrevSecs     float64      `json:"duration_from_prev_secs"`
	CumulativeDurationSecs   float64      `json:"cumulative_duration_secs"`
}

// CalculatedRoute represents a single driver's route
type CalculatedRoute struct {
	Driver                     *Driver     `json:"driver"`
	Stops                      []RouteStop `json:"stops"`
	TotalDropoffDistanceMeters float64     `json:"total_dropoff_distance_meters"`
	DistanceToDriverHomeMeters float64     `json:"distance_to_driver_home_meters"`
	TotalDistanceMeters        float64     `json:"total_distance_meters"`
	OrgVehicleID               int64       `json:"org_vehicle_id,omitempty"`
	OrgVehicleName             string      `json:"org_vehicle_name,omitempty"`
	EffectiveCapacity          int         `json:"effective_capacity"` // Driver's capacity or org vehicle capacity
	BaselineDurationSecs       float64     `json:"baseline_duration_secs"`
	RouteDurationSecs          float64     `json:"route_duration_secs"`
	DetourSecs                 float64     `json:"detour_secs"`
}

// RoutingSummary contains aggregate stats for a routing calculation
type RoutingSummary struct {
	TotalParticipants          int     `json:"total_participants"`
	TotalDriversUsed           int     `json:"total_drivers_used"`
	TotalDropoffDistanceMeters float64 `json:"total_dropoff_distance_meters"`
	TotalDistanceMeters        float64 `json:"total_distance_meters"`
	OrgVehiclesUsed            int     `json:"org_vehicles_used,omitempty"`
	UnassignedParticipants     []int64 `json:"unassigned_participants"`
	MaxDetourSecs              float64 `json:"max_detour_secs"`
	SumDetourSecs              float64 `json:"sum_detour_secs"`
	AverageDetourSecs          float64 `json:"average_detour_secs"`
}

// RoutingResult contains the full result of a route calculation
type RoutingResult struct {
	Routes   []CalculatedRoute `json:"routes"`
	Summary  RoutingSummary    `json:"summary"`
	Warnings []string          `json:"warnings"`
}

// DistanceCacheEntry represents a cached distance lookup
type DistanceCacheEntry struct {
	Origin         Coordinates `json:"origin"`
	Destination    Coordinates `json:"destination"`
	DistanceMeters float64     `json:"distance_meters"`
	DurationSecs   float64     `json:"duration_secs"`
}
