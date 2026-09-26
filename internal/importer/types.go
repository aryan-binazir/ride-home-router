package importer

import (
	"ride-home-router/internal/models"
	"time"
)

const (
	MaxDataRows       = 2000
	MaxColumns        = 64
	MaxCellCharacters = 500

	MaxAddressNameLength = models.MaxAddressNameLength
	MinCapacity          = models.MinVehicleCapacity
	MaxCapacity          = models.MaxVehicleCapacity
	DefaultCapacity      = models.DefaultVehicleCapacity

	UnmappedColumn = -1
)

type Format string

const (
	FormatCSV  Format = "csv"
	FormatXLSX Format = "xlsx"
)

type Kind string

const (
	KindParticipant Kind = "participant"
	KindDriver      Kind = "driver"
)

type Field string

const (
	FieldName        Field = "name"
	FieldAddress     Field = "address"
	FieldAddressName Field = "address_name"
	FieldCapacity    Field = "capacity"
)

type Mapping struct {
	NameColumn        int
	AddressColumn     int
	AddressNameColumn int
	CapacityColumn    int

	Ambiguous map[Field][]int
	Ignored   []int
}

type Grid struct {
	Headers  []string
	Warnings []string
	rows     []gridRow
}

func (g *Grid) Len() int {
	if g == nil {
		return 0
	}
	return len(g.rows)
}

type gridRow struct {
	sourceRow int
	cells     []string
	errors    []string
	warnings  []string
	xlsx      bool
}

type Existing struct {
	Name    string
	Address string
}

type Row struct {
	GeocodedAt time.Time
	SourceRow  int

	Name             string
	Address          string
	AddressName      string
	Lat              float64
	Lng              float64
	Capacity         int
	CapacityUnmapped bool `json:"CapacityDefaulted"`

	MatchedAddress string
	AddressGuessed bool

	HasCoordinates      bool
	NeedsGeocoding      bool
	DuplicateInFile     bool
	DuplicateOfExisting bool

	Errors   []string
	Warnings []string
}
