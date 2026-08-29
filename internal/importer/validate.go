package importer

import (
	"fmt"
	"math"
	"ride-home-router/internal/models"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/xuri/excelize/v2"
)

const coordinateTolerance = 1e-6

type coordinateState byte

const (
	coordinatesMissing coordinateState = iota
	coordinatesValid
	coordinatesInvalid
)

// Validate maps every row and copies mapping errors onto each result.
func Validate(g *Grid, m Mapping, kind Kind, existing []Existing) []Row {
	if g == nil {
		return nil
	}
	mappingErrors := validateMapping(m, kind, len(g.Headers))
	rows := make([]Row, len(g.rows))
	coordinateStates := make([]coordinateState, len(g.rows))

	existingPairs := make(map[string]struct{}, len(existing))
	for _, entry := range existing {
		if key := duplicateKey(entry.Name, entry.Address); key != "" {
			existingPairs[key] = struct{}{}
		}
	}

	seenPairs := make(map[string]struct{}, len(g.rows))
	for i, source := range g.rows {
		row := Row{
			SourceRow: source.sourceRow,
			Name:      mappedCell(source.cells, m.NameColumn),
			Address:   mappedCell(source.cells, m.AddressColumn),
			Errors:    append([]string(nil), source.errors...),
			Warnings:  append([]string(nil), source.warnings...),
		}
		row.AddressName = mappedCell(source.cells, m.AddressNameColumn)
		row.Errors = append(row.Errors, mappingErrors...)
		if source.xlsx {
			row.Errors = append(row.Errors, xlsxCellErrors(source.cells, source.sourceRow, m)...)
		}

		if row.Name == "" {
			row.addError("name is required")
		}
		if row.Address == "" {
			row.addError("address is required")
		}
		if utf8.RuneCountInString(row.AddressName) > MaxAddressNameLength {
			row.addError(fmt.Sprintf("location name must be %d characters or fewer", MaxAddressNameLength))
		}

		latText := mappedCell(source.cells, m.LatitudeColumn)
		lngText := mappedCell(source.cells, m.LongitudeColumn)
		coordinateStates[i] = validateCoordinates(&row, latText, lngText)
		validateCapacity(&row, source.cells, m, kind)

		if key := duplicateKey(row.Name, row.Address); key != "" {
			if _, ok := seenPairs[key]; ok {
				row.DuplicateInFile = true
			} else {
				seenPairs[key] = struct{}{}
			}
			_, row.DuplicateOfExisting = existingPairs[key]
		}
		rows[i] = row
	}

	reconcileHouseholdCoordinates(rows, coordinateStates, existing)
	return rows
}

func xlsxCellErrors(cells []string, row int, mapping Mapping) []string {
	var rowErrors []string
	mapped := make(map[int]struct{}, len(mapping.columns()))
	for _, binding := range mapping.columns() {
		column := binding.column
		if column < 0 || column >= len(cells) {
			continue
		}
		if _, exists := mapped[column]; exists {
			continue
		}
		mapped[column] = struct{}{}
		value := cells[column]
		if !xlsxErrorValues[value] {
			continue
		}
		cell, err := excelize.CoordinatesToCellName(column+1, row)
		if err == nil {
			rowErrors = append(rowErrors, fmt.Sprintf("cell %s contains spreadsheet error %s", cell, value))
		}
	}
	return rowErrors
}

func validateMapping(m Mapping, kind Kind, width int) []string {
	var validationErrors []string
	if kind != KindParticipant && kind != KindDriver {
		validationErrors = append(validationErrors, fmt.Sprintf("unsupported roster kind %q", kind))
	}
	for _, field := range requiredFields {
		if m.column(field) == UnmappedColumn {
			validationErrors = append(validationErrors, fmt.Sprintf("mapping must include the required %s field", field))
		}
	}

	used := make(map[int]Field)
	for _, binding := range m.columns() {
		if binding.column == UnmappedColumn {
			continue
		}
		if binding.column < 0 || binding.column >= width {
			validationErrors = append(validationErrors, fmt.Sprintf("mapping for %s refers to invalid column %d", binding.field, binding.column))
			continue
		}
		if previous, ok := used[binding.column]; ok {
			validationErrors = append(validationErrors, fmt.Sprintf("column %d cannot map to both %s and %s", binding.column, previous, binding.field))
			continue
		}
		used[binding.column] = binding.field
	}
	return validationErrors
}

func validateCoordinates(row *Row, latText, lngText string) coordinateState {
	if latText == "" && lngText == "" {
		row.NeedsGeocoding = true
		return coordinatesMissing
	}
	if latText == "" || lngText == "" {
		row.addError("latitude and longitude must either both be provided or both be empty")
		return coordinatesInvalid
	}

	lat, latErr := parseFiniteFloat(latText)
	lng, lngErr := parseFiniteFloat(lngText)
	if latErr != nil {
		row.addError(fmt.Sprintf("latitude must be a finite decimal number: %v", latErr))
	}
	if lngErr != nil {
		row.addError(fmt.Sprintf("longitude must be a finite decimal number: %v", lngErr))
	}
	if latErr != nil || lngErr != nil {
		return coordinatesInvalid
	}
	if lat < -90 || lat > 90 {
		row.addError("latitude must be between -90 and 90")
	}
	if lng < -180 || lng > 180 {
		row.addError("longitude must be between -180 and 180")
	}
	if lat < -90 || lat > 90 || lng < -180 || lng > 180 {
		return coordinatesInvalid
	}
	row.Lat = lat
	row.Lng = lng
	row.HasCoordinates = true
	return coordinatesValid
}

func parseFiniteFloat(value string) (float64, error) {
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, fmt.Errorf("%q is not a number", value)
	}
	if math.IsNaN(parsed) || math.IsInf(parsed, 0) {
		return 0, fmt.Errorf("%q is not finite", value)
	}
	return parsed, nil
}

func validateCapacity(row *Row, cells []string, m Mapping, kind Kind) {
	if kind != KindDriver {
		return
	}
	if m.CapacityColumn == UnmappedColumn {
		row.Capacity = DefaultCapacity
		row.addWarning(fmt.Sprintf("capacity column is not mapped; using default capacity %d", DefaultCapacity))
		return
	}
	value := mappedCell(cells, m.CapacityColumn)
	if value == "" {
		row.addError("capacity is required when the capacity column is mapped")
		return
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) || math.Trunc(parsed) != parsed {
		row.addError("capacity must be a whole number")
		return
	}
	if parsed < MinCapacity || parsed > MaxCapacity {
		row.addError(fmt.Sprintf("capacity must be between %d and %d", MinCapacity, MaxCapacity))
		return
	}
	row.Capacity = int(parsed)
}

func reconcileHouseholdCoordinates(rows []Row, states []coordinateState, existing []Existing) {
	groups := make(map[string][]int)
	for i := range rows {
		if address := normalize(rows[i].Address); address != "" {
			groups[address] = append(groups[address], i)
		}
	}

	existingByAddress := make(map[string][]Existing)
	for _, entry := range existing {
		if address := normalize(entry.Address); address != "" {
			existingByAddress[address] = append(existingByAddress[address], entry)
		}
	}

	for address, indices := range groups {
		if current := existingByAddress[address]; len(current) > 0 {
			reconcileWithExisting(rows, states, indices, current)
			continue
		}
		reconcileWithinFile(rows, states, indices)
	}
}

func reconcileWithExisting(rows []Row, states []coordinateState, indices []int, existing []Existing) {
	winning := existing[0]
	if !validCoordinatePair(winning.Lat, winning.Lng) {
		for _, index := range indices {
			rows[index].addError("existing roster entry has invalid coordinates for this address")
		}
		return
	}
	for _, entry := range existing[1:] {
		if !validCoordinatePair(entry.Lat, entry.Lng) || coordinatesConflict(winning.Lat, winning.Lng, entry.Lat, entry.Lng) {
			for _, index := range indices {
				rows[index].addWarning("existing roster entries disagree about this address's coordinates; this row will use its own")
			}
			reconcileWithinFile(rows, states, indices)
			return
		}
	}

	for _, index := range indices {
		switch states[index] {
		case coordinatesValid:
			if coordinatesConflict(rows[index].Lat, rows[index].Lng, winning.Lat, winning.Lng) {
				rows[index].addError("coordinates conflict with the existing roster entry for this address")
			}
		case coordinatesMissing:
			inheritCoordinates(&rows[index], winning.Lat, winning.Lng, "coordinates copied from an existing roster entry with the same address")
		case coordinatesInvalid:
			// Parsing already recorded the error.
		}
	}
}

func reconcileWithinFile(rows []Row, states []coordinateState, indices []int) {
	representative := -1
	conflict := false
	for _, index := range indices {
		if states[index] != coordinatesValid {
			continue
		}
		if representative == -1 {
			representative = index
			continue
		}
		if coordinatesConflict(rows[representative].Lat, rows[representative].Lng, rows[index].Lat, rows[index].Lng) {
			conflict = true
		}
	}
	if conflict {
		for _, index := range indices {
			rows[index].addError("rows with this address have conflicting coordinates")
		}
		return
	}
	if representative == -1 {
		return
	}
	for _, index := range indices {
		if states[index] == coordinatesMissing {
			inheritCoordinates(&rows[index], rows[representative].Lat, rows[representative].Lng, "coordinates copied from another row with the same address")
		}
	}
}

func inheritCoordinates(row *Row, lat, lng float64, warning string) {
	row.Lat = lat
	row.Lng = lng
	row.HasCoordinates = true
	row.NeedsGeocoding = false
	row.CoordinatesInherited = true
	row.addWarning(warning)
}

func validCoordinatePair(lat, lng float64) bool {
	return !math.IsNaN(lat) && !math.IsInf(lat, 0) && !math.IsNaN(lng) && !math.IsInf(lng, 0) &&
		lat >= -90 && lat <= 90 && lng >= -180 && lng <= 180
}

func coordinatesConflict(latA, lngA, latB, lngB float64) bool {
	return math.Abs(latA-latB) > coordinateTolerance || math.Abs(lngA-lngB) > coordinateTolerance
}

func mappedCell(cells []string, column int) string {
	if column < 0 || column >= len(cells) {
		return ""
	}
	return cells[column]
}

// DuplicateKey returns the roster key, or blank for incomplete rows.
func DuplicateKey(name, address string) string {
	return models.RosterKey(name, address)
}

func duplicateKey(name, address string) string { return DuplicateKey(name, address) }

func (r *Row) addError(message string) {
	if message != "" && !containsString(r.Errors, message) {
		r.Errors = append(r.Errors, message)
	}
}

func (r *Row) addWarning(message string) {
	if message != "" && !containsString(r.Warnings, message) {
		r.Warnings = append(r.Warnings, message)
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if strings.EqualFold(value, target) {
			return true
		}
	}
	return false
}
