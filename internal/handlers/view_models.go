package handlers

import "ride-home-router/internal/models"

type ActivePage string

const (
	ActivePageHome              ActivePage = "home"
	ActivePageParticipants      ActivePage = "participants"
	ActivePageDrivers           ActivePage = "drivers"
	ActivePageActivityLocations ActivePage = "activity_locations"
	ActivePageVans              ActivePage = "vans"
	ActivePageSettings          ActivePage = "settings"
	ActivePageHistory           ActivePage = "history"
)

type BasePageView struct {
	Title      string
	ActivePage ActivePage
}

type IndexPageView struct {
	BasePageView
	Participants      []models.Participant
	Drivers           []models.Driver
	ActivityLocations []models.ActivityLocation
	OrgVehicles       []models.OrganizationVehicle
}

type ParticipantsPageView struct {
	BasePageView
	Participants []models.Participant
}

type DriversPageView struct {
	BasePageView
	Drivers []models.Driver
}

type ActivityLocationsPageView struct {
	BasePageView
	ActivityLocations []models.ActivityLocation
}

type VansPageView struct {
	BasePageView
	OrgVehicles []models.OrganizationVehicle
}

type DatabaseConfigView struct {
	DatabasePath string
	DefaultPath  string
	IsDefault    bool
}

type SettingsPageView struct {
	BasePageView
	Settings       *models.Settings
	DatabaseConfig DatabaseConfigView
}

type HistoryPageView struct {
	BasePageView
	Events         []EventWithSummary
	Total          int
	UseMiles       bool
	Limit          int
	Offset         int
	DisplayedCount int
	NextOffset     int
	PageSize       int
}

type ParticipantListView struct {
	Participants []models.Participant
}

type ParticipantFormView struct {
	Participant *models.Participant
}

type DriverListView struct {
	Drivers []models.Driver
}

type DriverFormView struct {
	Driver *models.Driver
}

type ActivityLocationFormView struct {
	ActivityLocation *models.ActivityLocation
}

type OrgVehicleFormView struct {
	OrgVehicle *models.OrganizationVehicle
}

type CapacityShortageErrorView struct {
	Message           string
	UnassignedCount   int
	TotalCapacity     int
	TotalParticipants int
	Shortage          int
}

type CapacityShortageView struct {
	Error                     CapacityShortageErrorView
	Drivers                   []models.Driver
	OrgVehicles               []models.OrganizationVehicle
	ParticipantIDs            []int64
	DriverIDs                 []int64
	ActivityLocation          *models.ActivityLocation
	Mode                      string
	UseMiles                  bool
	RouteTime                 string
	SelectedOrgVehicles       map[int64]int64
	EffectiveCapacityByDriver map[int64]int
}

type RouteResultsView struct {
	Routes           []models.CalculatedRoute
	Summary          models.RoutingSummary
	UseMiles         bool
	ActivityLocation *models.ActivityLocation
	RouteTime        string
	SessionID        string
	IsEditing        bool
	UnusedDrivers    []models.Driver
	Mode             string
	RoutingPayload   models.RoutingResult
}

type RoutingErrorDetails struct {
	UnassignedCount   int `json:"unassigned_count"`
	TotalCapacity     int `json:"total_capacity"`
	TotalParticipants int `json:"total_participants"`
}

type RouteCalculationResponse struct {
	Routes    []models.CalculatedRoute `json:"routes"`
	Summary   models.RoutingSummary    `json:"summary"`
	SessionID string                   `json:"session_id"`
	Mode      models.RouteMode         `json:"mode"`
}

type DatabasePathUpdateResponse struct {
	DatabasePath string `json:"database_path"`
	Message      string `json:"message"`
}
