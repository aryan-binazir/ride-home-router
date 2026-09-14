package handlers

import "ride-home-router/internal/models"

type ActivePage string

const (
	ActivePageHome              ActivePage = "home"
	ActivePageParticipants      ActivePage = "participants"
	ActivePageDrivers           ActivePage = "drivers"
	ActivePageLabels            ActivePage = "labels"
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
	SelectedLocation                                          *models.ActivityLocation
	PagedPickers                                              bool
	ParticipantNext, DriverNext, PickerOffset, PickerPrevious int
	SelectedParticipants, SelectedDrivers                     map[int64]bool
	HiddenParticipants                                        []int64
	HiddenDrivers                                             []models.Driver
	AssignedVehicles                                          map[int64]*models.OrganizationVehicle
	Participants                                              []models.Participant
	Drivers                                                   []models.Driver
	Labels                                                    []models.Label
	ParticipantLabels                                         map[int64][]int64
	DriverLabels                                              map[int64][]int64
	ActivityLocations                                         []models.ActivityLocation
}

type ParticipantsPageView struct {
	Pagination rosterPagination
	BasePageView
	Participants []models.Participant
	Labels       []models.Label
	LabelIDs     map[int64][]int64
}

type DriversPageView struct {
	Pagination rosterPagination
	BasePageView
	Drivers  []models.Driver
	Labels   []models.Label
	LabelIDs map[int64][]int64
}

type LabelsPageView struct {
	BasePageView
	Labels []models.Label
}

type ActivityLocationsPageView struct {
	BasePageView
	ActivityLocations []models.ActivityLocation
}

type VansPageView struct {
	BasePageView
	OrgVehicles []models.OrganizationVehicle
}

type SettingsPageView struct {
	BasePageView
	IsAdmin  bool
	Settings *models.Settings
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
	Pagination   rosterPagination
	Participants []models.Participant
	LabelIDs     map[int64][]int64
	Labels       []models.Label
}

type ParticipantFormView struct {
	Participant      *models.Participant
	Labels           []models.Label
	SelectedLabelIDs map[int64]bool
}

type DriverListView struct {
	Pagination rosterPagination
	Drivers    []models.Driver
	LabelIDs   map[int64][]int64
	Labels     []models.Label
}

type DriverFormView struct {
	Driver           *models.Driver
	Labels           []models.Label
	SelectedLabelIDs map[int64]bool
}

type LabelListView struct {
	Labels []models.Label
}

type LabelFormView struct {
	Label *models.Label
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
	AssignedVehicles          map[int64]*models.OrganizationVehicle
}

type RouteResultsView struct {
	// Partial restricts card rendering to RenderIndexes; full views render all cards.
	Partial       bool
	RenderIndexes map[int]bool
	// Timings holds this response's measured copy of each route, or why it has none.
	Timings []RouteTiming
	// ShowAggregates is true only when every occupied car was measured in this response.
	ShowAggregates bool
	// Attribution is true when Google-measured values appear on the page.
	Attribution      bool
	Routes           []models.CalculatedRoute
	OverCapacity     []bool
	IsOutOfBalance   bool
	Summary          models.RoutingSummary
	UseMiles         bool
	ActivityLocation *models.ActivityLocation
	RouteTime        string
	SessionID        string
	IsEditing        bool
	UnusedDrivers    []models.Driver
	Mode             string
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
	// Timings carries provider-measured values for this response only.
	Timings []RouteTimingJSON `json:"timings,omitempty"`
}
