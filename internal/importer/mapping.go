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

// AutoMap maps exact normalized header aliases. Ambiguous aliases remain
// unmapped so the caller must choose explicitly.
func AutoMap(headers []string) Mapping {
	m := emptyMapping()
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

func emptyMapping() Mapping {
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

// NormalizeRosterText canonicalizes user-entered roster text for exact-match
// comparisons. Persistence uses the same normalization when re-checking
// duplicates at commit time.
func NormalizeRosterText(value string) string {
	return models.NormalizeRosterField(value)
}

func normalize(value string) string { return models.NormalizeRosterField(value) }
