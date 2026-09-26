package models

import (
	"errors"
	"math"
	"strings"
	"time"

	"golang.org/x/text/unicode/norm"
)

const CoordinateMaxAge = 30 * 24 * time.Hour

const (
	MaxNameLength          = 200
	MaxAddressLength       = 500
	MaxNotesLength         = 4000
	MaxLabelNameLength     = 200
	MaxAddressNameLength   = 200
	MinVehicleCapacity     = 1
	DefaultVehicleCapacity = 4
	MaxVehicleCapacity     = 50
)

func RosterKey(name, address string) string {
	name = normalizeRosterKeyField(name, true)
	address = normalizeRosterKeyField(address, false)
	if name == "" || address == "" {
		return ""
	}
	return name + "\x00" + address
}

func normalizeRosterKeyField(value string, hyphensAsWhitespace bool) string {
	original := value
	normalized := strings.ToLower(norm.NFC.String(value))
	normalized = strings.Map(func(r rune) rune {
		switch r {
		case '\'', '\u2018', '\u2019', '\u02bc', '\u00b4', '`', '\u2032',
			'\ufeff', '.':
			return -1
		case ',', '\u200b':
			return ' '
		case '-', '\u00ad', '\u2010', '\u2011', '\u2012', '\u2013', '\u2014', '\u2212':
			if hyphensAsWhitespace {
				return ' '
			}
			return '-'
		}
		return r
	}, normalized)
	normalized = strings.Join(strings.Fields(normalized), " ")
	if normalized == "" && strings.TrimSpace(original) != "" {
		return NormalizeRosterField(original)
	}
	return normalized
}

// NormalizeRosterField applies the loose normalization used for import header
// matching and address grouping. Duplicate keys use RosterKey.
func NormalizeRosterField(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(value)), " "))
}

type Coordinates struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

type RouteMode string

const (
	RouteModeDropoff RouteMode = "dropoff"
	RouteModePickup  RouteMode = "pickup"
)

var ErrInvalidRouteMode = errors.New("invalid route mode")

func ParseRouteMode(value string) (RouteMode, error) {
	switch strings.TrimSpace(value) {
	case "", string(RouteModeDropoff):
		return RouteModeDropoff, nil
	case string(RouteModePickup):
		return RouteModePickup, nil
	default:
		return "", ErrInvalidRouteMode
	}
}

func RoundCoordinate(coord float64) float64 {
	return math.Round(coord*100000) / 100000
}

const (
	AddressMatchVerified  = "verified"
	AddressMatchGuessed   = "guessed"
	AddressMatchConfirmed = "confirmed"
)

func AddressMatchFor(guessed bool) string {
	if guessed {
		return AddressMatchGuessed
	}
	return AddressMatchVerified
}

type Participant struct {
	GeocodedAt     time.Time  `json:"-"`
	ID             int64      `json:"id"`
	Name           string     `json:"name"`
	Address        string     `json:"address"`
	MatchedAddress string     `json:"matched_address,omitempty"`
	AddressMatch   string     `json:"address_match,omitempty"`
	AddressName    string     `json:"address_name"`
	Lat            float64    `json:"lat"`
	Lng            float64    `json:"lng"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	DeletedAt      *time.Time `json:"deleted_at,omitempty"`
}

func (p *Participant) GetCoords() Coordinates {
	return Coordinates{Lat: p.Lat, Lng: p.Lng}
}

type Driver struct {
	GeocodedAt      time.Time  `json:"-"`
	ID              int64      `json:"id"`
	Name            string     `json:"name"`
	Address         string     `json:"address"`
	MatchedAddress  string     `json:"matched_address,omitempty"`
	AddressMatch    string     `json:"address_match,omitempty"`
	AddressName     string     `json:"address_name"`
	Lat             float64    `json:"lat"`
	Lng             float64    `json:"lng"`
	VehicleCapacity int        `json:"vehicle_capacity"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	DeletedAt       *time.Time `json:"deleted_at,omitempty"`
}

func (d *Driver) GetCoords() Coordinates {
	return Coordinates{Lat: d.Lat, Lng: d.Lng}
}

type Label struct {
	ID               int64     `json:"id"`
	Name             string    `json:"name"`
	ParticipantCount int       `json:"participant_count"`
	DriverCount      int       `json:"driver_count"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type ActivityLocation struct {
	GeocodedAt time.Time  `json:"-"`
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	Address    string     `json:"address"`
	Lat        float64    `json:"lat"`
	Lng        float64    `json:"lng"`
	DeletedAt  *time.Time `json:"deleted_at,omitempty"`
}

func (a *ActivityLocation) GetCoords() Coordinates {
	return Coordinates{Lat: a.Lat, Lng: a.Lng}
}

type OrganizationVehicle struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Capacity  int       `json:"capacity"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Settings struct {
	InstituteAddress           string  `json:"institute_address"` // Deprecated: use SelectedActivityLocationID
	InstituteLat               float64 `json:"institute_lat"`     // Deprecated: use SelectedActivityLocationID
	InstituteLng               float64 `json:"institute_lng"`     // Deprecated: use SelectedActivityLocationID
	SelectedActivityLocationID int64   `json:"selected_activity_location_id"`
	UseMiles                   bool    `json:"use_miles"`
	SMEEmail                   string  `json:"sme_email"`
	CollectReviewerNotes       bool    `json:"collect_reviewer_notes"`
}

type RouteFeedbackRecord struct {
	EventID       int64                 `json:"event_id"`
	SessionID     string                `json:"session_id"`
	SMEEmail      string                `json:"sme_email"`
	SchemaVersion int                   `json:"schema_version"`
	Mode          RouteMode             `json:"mode"`
	Input         RouteFeedbackInput    `json:"input"`
	Proposed      []RouteFeedbackRoute  `json:"proposed"`
	Final         []RouteFeedbackRoute  `json:"final"`
	Changes       []RouteFeedbackChange `json:"changes"`
	ReviewerNote  string                `json:"reviewer_note"`
}

type RouteFeedbackChange struct {
	Kind          string `json:"kind"`
	ParticipantID int64  `json:"participant_id,omitempty"`
	FromDriverID  int64  `json:"from_driver_id,omitempty"`
	ToDriverID    int64  `json:"to_driver_id,omitempty"`
}

type RouteFeedbackInput struct {
	Activity     RouteFeedbackActivity      `json:"activity"`
	Drivers      []RouteFeedbackDriver      `json:"drivers"`
	Participants []RouteFeedbackParticipant `json:"participants"`
}

type RouteFeedbackActivity struct {
	ID  int64   `json:"id"`
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

type RouteFeedbackDriver struct {
	ID           int64   `json:"id"`
	Address      string  `json:"address"`
	Lat          float64 `json:"lat"`
	Lng          float64 `json:"lng"`
	Capacity     int     `json:"capacity"`
	OrgVehicleID *int64  `json:"org_vehicle_id,omitempty"`
}

type RouteFeedbackParticipant struct {
	ID      int64   `json:"id"`
	Address string  `json:"address"`
	Lat     float64 `json:"lat"`
	Lng     float64 `json:"lng"`
}

type RouteFeedbackRoute struct {
	DriverID            int64   `json:"driver_id"`
	OrgVehicleID        *int64  `json:"org_vehicle_id,omitempty"`
	ParticipantIDs      []int64 `json:"participant_ids"`
	TotalDistanceMeters float64 `json:"total_distance_meters"`
	RouteDurationSecs   float64 `json:"route_duration_secs"`
	DetourSecs          float64 `json:"detour_secs"`
}

type Event struct {
	RouteSessionID string    `json:"-"`
	ID             int64     `json:"id"`
	EventDate      time.Time `json:"event_date"`
	Notes          string    `json:"notes"`
	Mode           RouteMode `json:"mode"`
	CreatedAt      time.Time `json:"created_at"`
}

type EventRoute struct {
	// Handoffs preserve exactly the instructions available when the event was saved.
	DriverHandoff              string           `json:"driver_handoff,omitempty"`
	ParentHandoff              string           `json:"parent_handoff,omitempty"`
	ID                         int64            `json:"id"`
	EventID                    int64            `json:"event_id"`
	RouteOrder                 int              `json:"route_order"`
	DriverID                   int64            `json:"driver_id"`
	DriverName                 string           `json:"driver_name"`
	DriverAddress              string           `json:"driver_address"`
	DriverAddressName          string           `json:"driver_address_name,omitempty"`
	EffectiveCapacity          int              `json:"effective_capacity"`
	OrgVehicleID               int64            `json:"org_vehicle_id,omitempty"`
	OrgVehicleName             string           `json:"org_vehicle_name,omitempty"`
	TotalDropoffDistanceMeters float64          `json:"total_dropoff_distance_meters"`
	DistanceToDriverHomeMeters float64          `json:"distance_to_driver_home_meters"`
	TotalDistanceMeters        float64          `json:"total_distance_meters"`
	BaselineDurationSecs       float64          `json:"baseline_duration_secs"`
	RouteDurationSecs          float64          `json:"route_duration_secs"`
	DetourSecs                 float64          `json:"detour_secs"`
	Mode                       RouteMode        `json:"mode"`
	SnapshotVersion            int              `json:"-"`
	MetricsComplete            bool             `json:"-"`
	Stops                      []EventRouteStop `json:"stops,omitempty"`
}

type EventRouteStop struct {
	ID                       int64   `json:"id"`
	EventRouteID             int64   `json:"event_route_id"`
	Order                    int     `json:"order"`
	ParticipantID            int64   `json:"participant_id"`
	ParticipantName          string  `json:"participant_name"`
	ParticipantAddress       string  `json:"participant_address"`
	ParticipantAddressName   string  `json:"participant_address_name,omitempty"`
	DistanceFromPrevMeters   float64 `json:"distance_from_prev_meters"`
	CumulativeDistanceMeters float64 `json:"cumulative_distance_meters"`
	DurationFromPrevSecs     float64 `json:"duration_from_prev_secs"`
	CumulativeDurationSecs   float64 `json:"cumulative_duration_secs"`
}

type EventSummary struct {
	EventID             int64     `json:"event_id"`
	TotalParticipants   int       `json:"total_participants"`
	TotalDrivers        int       `json:"total_drivers"`
	TotalDistanceMeters float64   `json:"total_distance_meters"`
	OrgVehiclesUsed     int       `json:"org_vehicles_used,omitempty"`
	Mode                RouteMode `json:"mode"`
}

type RouteStop struct {
	Order                    int          `json:"order"`
	Participant              *Participant `json:"participant"`
	DistanceFromPrevMeters   float64      `json:"distance_from_prev_meters"`
	CumulativeDistanceMeters float64      `json:"cumulative_distance_meters"`
	DurationFromPrevSecs     float64      `json:"duration_from_prev_secs"`
	CumulativeDurationSecs   float64      `json:"cumulative_duration_secs"`
}

type CalculatedRoute struct {
	Driver                     *Driver     `json:"driver"`
	Stops                      []RouteStop `json:"stops"`
	TotalDropoffDistanceMeters float64     `json:"total_dropoff_distance_meters"`
	DistanceToDriverHomeMeters float64     `json:"distance_to_driver_home_meters"`
	TotalDistanceMeters        float64     `json:"total_distance_meters"`
	OrgVehicleID               int64       `json:"org_vehicle_id,omitempty"`
	OrgVehicleName             string      `json:"org_vehicle_name,omitempty"`
	EffectiveCapacity          int         `json:"effective_capacity"`
	BaselineDurationSecs       float64     `json:"baseline_duration_secs"`
	RouteDurationSecs          float64     `json:"route_duration_secs"`
	DetourSecs                 float64     `json:"detour_secs"`
	Mode                       RouteMode   `json:"mode"`
}

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

type RoutingResult struct {
	Routes  []CalculatedRoute `json:"routes"`
	Summary RoutingSummary    `json:"summary"`
	Mode    RouteMode         `json:"mode"`
}

type DistanceCacheEntry struct {
	Origin         Coordinates `json:"origin"`
	Destination    Coordinates `json:"destination"`
	DistanceMeters float64     `json:"distance_meters"`
	DurationSecs   float64     `json:"duration_secs"`
}
