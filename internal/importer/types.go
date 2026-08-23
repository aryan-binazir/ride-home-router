// Package importer parses and validates participant and driver roster files.
package importer

// Import limits shared with the HTTP layer.
const (
	MaxDataRows       = 2000
	MaxColumns        = 64
	MaxCellCharacters = 500

	MaxAddressNameLength = 200
	MinCapacity          = 1
	MaxCapacity          = 50
	DefaultCapacity      = 4

	UnmappedColumn = -1
)

// Format identifies a supported roster file format.
type Format string

const (
	FormatCSV  Format = "csv"
	FormatXLSX Format = "xlsx"
)

// Kind identifies the roster being imported.
type Kind string

const (
	KindParticipant Kind = "participant"
	KindDriver      Kind = "driver"
)

// Field identifies an importable roster field.
type Field string

const (
	FieldName        Field = "name"
	FieldAddress     Field = "address"
	FieldAddressName Field = "address_name"
	FieldLatitude    Field = "lat"
	FieldLongitude   Field = "lng"
	FieldCapacity    Field = "capacity"
)

// Mapping binds roster fields to zero-based Grid header columns. A value of
// UnmappedColumn leaves the field unbound. Callers may edit these fields after
// AutoMap returns.
type Mapping struct {
	NameColumn        int
	AddressColumn     int
	AddressNameColumn int
	LatitudeColumn    int
	LongitudeColumn   int
	CapacityColumn    int

	Ambiguous map[Field][]int
	Ignored   []int
}

// Grid is the normalized contents of a parsed roster file. Its data rows stay
// private so callers must pass them through Validate before use.
type Grid struct {
	Headers []string
	rows    []gridRow
}

// Len returns the number of data rows in the grid.
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
}

// Existing is the duplicate and household-coordinate information for one
// current roster entry.
type Existing struct {
	Name    string
	Address string
	Lat     float64
	Lng     float64
}

// Row is one validated roster row. HasCoordinates distinguishes valid zero
// coordinates from missing coordinates.
type Row struct {
	SourceRow int

	Name        string
	Address     string
	AddressName string
	Lat         float64
	Lng         float64
	Capacity    int

	HasCoordinates       bool
	NeedsGeocoding       bool
	CoordinatesInherited bool
	DuplicateInFile      bool
	DuplicateOfExisting  bool

	Errors   []string
	Warnings []string
}
