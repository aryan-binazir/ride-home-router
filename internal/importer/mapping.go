package importer

import "ride-home-router/internal/models"

var fieldAliases = map[Field]map[string]struct{}{
	FieldName:        aliasSet("name", "full name", "participant", "participant name", "driver", "driver name", "person"),
	FieldAddress:     aliasSet("address", "street address", "home address", "location"),
	FieldAddressName: aliasSet("location name", "address name", "place", "place name", "building", "complex"),
	FieldLatitude:    aliasSet("lat", "latitude"),
	FieldLongitude:   aliasSet("lng", "lon", "long", "longitude"),
	FieldCapacity:    aliasSet("passenger capacity", "available seats", "capacity"),
}

var fieldOrder = []Field{
	FieldName,
	FieldAddress,
	FieldAddressName,
	FieldLatitude,
	FieldLongitude,
	FieldCapacity,
}

var requiredFields = []Field{FieldName, FieldAddress}

// FieldColumn binds one roster field to a zero-based grid column.
type FieldColumn struct {
	Field  Field
	Column int
}

// MappingTransition describes a mapping after ordered assignments. DuplicateFields
// and MissingRequired are advisory facts for adapters; Validate remains the final
// authority for whether a mapping can produce importable rows.
type MappingTransition struct {
	Mapping         Mapping
	DuplicateFields []Field
	MissingRequired []Field
}

// AutoMap maps exact normalized header aliases and leaves ambiguous aliases
// unmapped.
func AutoMap(headers []string) Mapping {
	m := NewMapping()
	claimed := make(map[int]bool)
	for _, field := range fieldOrder {
		var matches []int
		for column, header := range headers {
			if _, ok := fieldAliases[field][normalize(header)]; ok {
				matches = append(matches, column)
				claimed[column] = true
			}
		}
		switch len(matches) {
		case 1:
			m.set(field, matches[0])
		case 2:
			m.Ambiguous[field] = matches
		default:
			if len(matches) > 2 {
				m.Ambiguous[field] = matches
			}
		}
	}
	for column := range headers {
		if !claimed[column] {
			m.Ignored = append(m.Ignored, column)
		}
	}
	return m
}

// NewMapping returns a mapping with every field unbound.
func NewMapping() Mapping {
	return Mapping{
		NameColumn:        UnmappedColumn,
		AddressColumn:     UnmappedColumn,
		AddressNameColumn: UnmappedColumn,
		LatitudeColumn:    UnmappedColumn,
		LongitudeColumn:   UnmappedColumn,
		CapacityColumn:    UnmappedColumn,
		Ambiguous:         make(map[Field][]int),
	}
}

// Assign applies field assignments in order to a copy of the mapping and
// recomputes which in-range columns are ignored. The first assignment for a
// field wins. Column range and cross-field ownership remain Validate concerns.
func (m Mapping) Assign(assignments []FieldColumn, width int) MappingTransition {
	next := copyMapping(m)
	assigned := make(map[Field]bool, len(assignments))
	reportedDuplicate := make(map[Field]bool)
	var duplicateFields []Field

	for _, assignment := range assignments {
		if !knownField(assignment.Field) {
			continue
		}
		if assigned[assignment.Field] {
			if !reportedDuplicate[assignment.Field] {
				reportedDuplicate[assignment.Field] = true
				duplicateFields = append(duplicateFields, assignment.Field)
			}
			continue
		}
		assigned[assignment.Field] = true
		next.set(assignment.Field, assignment.Column)
		if assignment.Column != UnmappedColumn {
			delete(next.Ambiguous, assignment.Field)
		}
	}

	claimed := make(map[int]bool)
	for _, binding := range next.columns() {
		if binding.column >= 0 {
			claimed[binding.column] = true
		}
	}
	for _, columns := range next.Ambiguous {
		for _, column := range columns {
			claimed[column] = true
		}
	}
	next.Ignored = nil
	for column := range width {
		if !claimed[column] {
			next.Ignored = append(next.Ignored, column)
		}
	}

	var missingRequired []Field
	for _, field := range requiredFields {
		if next.column(field) == UnmappedColumn {
			missingRequired = append(missingRequired, field)
		}
	}
	return MappingTransition{
		Mapping:         next,
		DuplicateFields: duplicateFields,
		MissingRequired: missingRequired,
	}
}

func (m *Mapping) set(field Field, column int) {
	switch field {
	case FieldName:
		m.NameColumn = column
	case FieldAddress:
		m.AddressColumn = column
	case FieldAddressName:
		m.AddressNameColumn = column
	case FieldLatitude:
		m.LatitudeColumn = column
	case FieldLongitude:
		m.LongitudeColumn = column
	case FieldCapacity:
		m.CapacityColumn = column
	}
}

func (m Mapping) column(field Field) int {
	switch field {
	case FieldName:
		return m.NameColumn
	case FieldAddress:
		return m.AddressColumn
	case FieldAddressName:
		return m.AddressNameColumn
	case FieldLatitude:
		return m.LatitudeColumn
	case FieldLongitude:
		return m.LongitudeColumn
	case FieldCapacity:
		return m.CapacityColumn
	default:
		return UnmappedColumn
	}
}

func (m Mapping) columns() []struct {
	field  Field
	column int
} {
	return []struct {
		field  Field
		column int
	}{
		{FieldName, m.NameColumn},
		{FieldAddress, m.AddressColumn},
		{FieldAddressName, m.AddressNameColumn},
		{FieldLatitude, m.LatitudeColumn},
		{FieldLongitude, m.LongitudeColumn},
		{FieldCapacity, m.CapacityColumn},
	}
}

func aliasSet(values ...string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	return set
}

func knownField(field Field) bool {
	switch field {
	case FieldName, FieldAddress, FieldAddressName, FieldLatitude, FieldLongitude, FieldCapacity:
		return true
	default:
		return false
	}
}

// NormalizeRosterText applies the normalization used for header matching and
// address grouping during household coordinate reconciliation and geocoding.
// Duplicate detection uses models.RosterKey.
func NormalizeRosterText(value string) string {
	return models.NormalizeRosterField(value)
}

func normalize(value string) string { return models.NormalizeRosterField(value) }
