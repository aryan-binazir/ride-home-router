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

// Validate maps every row and copies mapping errors onto each result.
func Validate(g *Grid, m Mapping, kind Kind, existing []Existing) []Row {
	if g == nil {
		return nil
	}
	mappingErrors := validateMapping(m, kind, len(g.Headers))
	rows := make([]Row, len(g.rows))
	existingPairs := make(map[string]struct{}, len(existing))
	for _, entry := range existing {
		if key := duplicateKey(entry.Name, entry.Address); key != "" {
			existingPairs[key] = struct{}{}
		}
	}

	seenPairs := make(map[string]struct{}, len(g.rows))
	for i, source := range g.rows {
		row := Row{
			SourceRow:      source.sourceRow,
			Name:           mappedCell(source.cells, m.NameColumn),
			Address:        mappedCell(source.cells, m.AddressColumn),
			NeedsGeocoding: true,
			Errors:         append([]string(nil), source.errors...),
			Warnings:       append([]string(nil), source.warnings...),
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

func validateCapacity(row *Row, cells []string, m Mapping, kind Kind) {
	if kind != KindDriver {
		return
	}
	if m.CapacityColumn == UnmappedColumn {
		row.Capacity = DefaultCapacity
		row.CapacityDefaulted = true
		row.addWarning(fmt.Sprintf("capacity column is not mapped; new drivers get capacity %d and existing drivers keep theirs", DefaultCapacity))
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
